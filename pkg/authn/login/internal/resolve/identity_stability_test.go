package resolve

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/vislake/speed/pkg/audit"
	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/authn/account"
	"github.com/vislake/speed/pkg/authn/authntest"
	mountinternal "github.com/vislake/speed/pkg/authn/internal/mount"
	"github.com/vislake/speed/pkg/authn/internal/slug"
	"github.com/vislake/speed/pkg/authn/invite"
	"github.com/vislake/speed/pkg/authn/provisioning/provisioningtest"
	"github.com/vislake/speed/pkg/config"
	"github.com/vislake/speed/pkg/core"
	"github.com/vislake/speed/pkg/db/sqlite"
	speedhttp "github.com/vislake/speed/pkg/http"
	kvmemory "github.com/vislake/speed/pkg/kv/memory"
	"github.com/vislake/speed/pkg/settings"
)

// TestExternalIdentityStillResolvesToItsAccountAfterTheProviderChangesTheEmail holds that the same provider and subject land on the same account despite a changed address.
func TestExternalIdentityStillResolvesToItsAccountAfterTheProviderChangesTheEmail(t *testing.T) {
	e := newEnv(t, withSignup(SignupOpen))
	first, err := e.resolve(social("google", "g1", "ann@first.example", true))
	if err != nil || first.Outcome != OutcomeCreated {
		t.Fatalf("the first login = %+v, %v, want a new account", first, err)
	}
	accountsBefore := e.s.live()
	identifiersBefore := e.s.identifiersOf(first.Account)

	again, err := e.resolve(social("google", "g1", "ann@second.example", true))
	if err != nil {
		t.Fatalf("the login after the provider changed the address: %v", err)
	}
	if again.Account != first.Account || again.Outcome != OutcomeBound {
		t.Errorf("the second login = %+v, want the original account bound with no cleanup", again)
	}
	if got := e.s.live(); !slices.Equal(got, accountsBefore) {
		t.Errorf("accounts = %v after the second login, want %v unchanged", got, accountsBefore)
	}
	bindings := e.s.bindingsOf(first.Account)
	if len(bindings) != 1 || bindings[0].Provider != "google" || bindings[0].Subject != "g1" {
		t.Errorf("bindings = %+v, want the one (google, g1) binding", bindings)
	}
	if bindings[0].Email != "ann@second.example" {
		t.Errorf("the binding's display address = %q, want the provider's latest", bindings[0].Email)
	}
	if got := e.s.identifiersOf(first.Account); !slices.Equal(got, identifiersBefore) {
		t.Errorf("identifiers = %+v, want %+v unchanged: the provider's new address is not the account's", got, identifiersBefore)
	}
	if len(e.s.created) != 1 {
		t.Errorf("created %d accounts, want the first login's one alone", len(e.s.created))
	}
}

// TestOneAddressUnderTwoProvidersIsTwoIdentities holds that the provider is part of the key.
func TestOneAddressUnderTwoProvidersIsTwoIdentities(t *testing.T) {
	e := newEnv(t, withSignup(SignupOpen))
	google := e.s.seed(t, seed{id: "a1", status: authn.StatusActive, bindings: [][2]string{{"google", "shared-subject"}}})
	github := e.s.seed(t, seed{id: "a2", status: authn.StatusActive, bindings: [][2]string{{"github", "shared-subject"}}})
	if google == github {
		t.Fatal("the fixture seeded one account twice")
	}

	first, err := e.resolve(social("google", "shared-subject", "shared@home.example", true))
	if err != nil || first.Account != google {
		t.Fatalf("the Google login = %+v, %v, want account %q", first, err, google)
	}
	second, err := e.resolve(social("github", "shared-subject", "shared@home.example", true))
	if err != nil || second.Account != github {
		t.Fatalf("the GitHub login = %+v, %v, want account %q", second, err, github)
	}
	if first.Account == second.Account {
		t.Errorf("both providers resolved to %q, want two identities", first.Account)
	}
	if len(e.s.created) != 0 || len(e.s.live()) != 2 {
		t.Errorf("created %d accounts with %v live, want the two fixtures alone", len(e.s.created), e.s.live())
	}
	for _, id := range []authn.AccountID{google, github} {
		if got := e.s.bindingsOf(id); len(got) != 1 {
			t.Errorf("account %q holds the bindings %+v, want the one it came with", id, got)
		}
	}
}

// TestABindingOutranksAnAddressHeldByAnotherAccount holds that a verified address of another account does not divert a bound login.
func TestABindingOutranksAnAddressHeldByAnotherAccount(t *testing.T) {
	e := newEnv(t, withSignup(SignupOpen))
	bound := e.s.seed(t, seed{id: "a1", status: authn.StatusActive, bindings: [][2]string{{"google", "g1"}}})
	holder := e.s.seed(t, seed{id: "a2", status: authn.StatusActive, email: "ann@elsewhere.example", emailVia: "idp:github"})
	holderIdentifiers := e.s.identifiersOf(holder)

	res, err := e.resolve(social("google", "g1", "ann@elsewhere.example", true))
	if err != nil || res.Account != bound || res.Outcome != OutcomeBound {
		t.Fatalf("Resolve = %+v, %v, want the bound account %q", res, err, bound)
	}
	if len(e.s.live()) != 2 || len(e.s.created) != 0 {
		t.Errorf("accounts = %v with %d created, want the two fixtures and no new account", e.s.live(), len(e.s.created))
	}
	if got := e.s.bindingsOf(bound); len(got) != 1 || got[0].Subject != "g1" {
		t.Errorf("the bound account's bindings = %+v, want the one it had", got)
	}
	if got := e.s.identifiersOf(holder); !slices.Equal(got, holderIdentifiers) {
		t.Errorf("the address holder's identifiers = %+v, want %+v unchanged", got, holderIdentifiers)
	}
}

// TestTwoSubjectsOfOneProviderOnFirstLoginOpenTwoAccounts holds that the subject alone decides which identity a first login is.
func TestTwoSubjectsOfOneProviderOnFirstLoginOpenTwoAccounts(t *testing.T) {
	e := newEnv(t, withSignup(SignupOpen))
	first, err := e.resolve(social("google", "subject-one", "", false))
	if err != nil || first.Outcome != OutcomeCreated {
		t.Fatalf("the first subject = %+v, %v, want a new account", first, err)
	}
	second, err := e.resolve(social("google", "subject-two", "", false))
	if err != nil || second.Outcome != OutcomeCreated {
		t.Fatalf("the second subject = %+v, %v, want a new account", second, err)
	}
	if first.Account == "" || second.Account == "" || first.Account == second.Account {
		t.Fatalf("accounts = %q and %q, want two distinct accounts", first.Account, second.Account)
	}
	if got, ok := e.s.boundTo("google", "subject-one"); !ok || got != first.Account {
		t.Errorf("(google, subject-one) is bound to %q, %v, want %q", got, ok, first.Account)
	}
	if got, ok := e.s.boundTo("google", "subject-two"); !ok || got != second.Account {
		t.Errorf("(google, subject-two) is bound to %q, %v, want %q", got, ok, second.Account)
	}
	if got := e.s.live(); len(got) != 2 {
		t.Errorf("accounts = %v, want exactly the two the logins made", got)
	}
}

type createdBy struct {
	path     string
	id       authn.AccountID
	notEqual []string
}

// TestEveryCreationPathMintsAnAccountIdentifierThatOutlivesProfileChanges holds that every path mints a distinct identifier unaffected by profile changes.
func TestEveryCreationPathMintsAnAccountIdentifierThatOutlivesProfileChanges(t *testing.T) {
	withRealAccountStore(t, func(ctx context.Context, reg *core.Registry) {
		accounts := mustResolve[authn.Accounts](t, reg)
		admin := mustResolve[authn.AccountAdmin](t, reg)
		memberships := mustResolve[authn.Memberships](t, reg)
		signups := mustResolve[authn.Signups](t, reg)
		invites := mustResolve[invite.Invites](t, reg)

		r, err := New(Config{
			Accounts:          accounts,
			Admin:             admin,
			Memberships:       memberships,
			SSO:               jitRegister{},
			NormalizeUsername: slug.Normalize,
			Signup:            SignupOpen,
		})
		if err != nil {
			t.Fatalf("New: %v", err)
		}

		var minted []createdBy
		record := func(path string, id authn.AccountID, notEqual ...string) authn.AccountID {
			t.Helper()
			if id == "" {
				t.Fatalf("%s: the account has no identifier", path)
			}
			if _, parseErr := authn.ParseAccountID(id.String()); parseErr != nil {
				t.Errorf("%s: identifier %q is not well formed: %v", path, id, parseErr)
			}
			minted = append(minted, createdBy{path: path, id: id, notEqual: notEqual})
			return id
		}
		firstLogin := func(req Request, username string) (Result, error) {
			res, resolveErr := r.Resolve(ctx, req)
			if resolveErr != nil || res.Pending == nil {
				return res, resolveErr
			}
			return r.Create(ctx, *res.Pending, username, passwordOf(*res.Pending), "")
		}

		registered, err := signups.Register(ctx, authn.RegisterAccount{
			Username: "self-made", Email: "self-made@example.org", Password: "correct horse battery staple",
		})
		if err != nil {
			t.Fatalf("password self-registration: %v", err)
		}
		passwordID := record("password self-registration", registered.ID, "self-made", "self-made@example.org")

		phoneLogin, err := firstLogin(Request{Proof: phone("+8613800000001"), Purpose: authn.PurposeLogin}, "phone-made")
		if err != nil || phoneLogin.Outcome != OutcomeCreated {
			t.Fatalf("the first phone login = %+v, %v, want a new account", phoneLogin, err)
		}
		phoneID := record("SMS first sign-in", phoneLogin.Account, "+8613800000001")
		if held := identifiersOf(t, accounts, phoneID); !slices.ContainsFunc(held, func(i authn.Identifier) bool {
			return i.Kind == authn.IdentifierPhone && i.Value == "+8613800000001" && i.SignIn
		}) {
			t.Errorf("the phone the login proved is not a sign-in way: %+v", held)
		}

		socialLogin, err := firstLogin(Request{
			Proof: social("google", "google-subject", "social@example.org", true), Purpose: authn.PurposeLogin,
		}, "social-made")
		if err != nil || socialLogin.Outcome != OutcomeCreated {
			t.Fatalf("the first social login = %+v, %v, want a new account", socialLogin, err)
		}
		socialID := record("social first sign-in", socialLogin.Account, "google-subject", "social@example.org")
		if held := identifiersOf(t, accounts, socialID); len(held) != 2 ||
			!slices.ContainsFunc(held, func(i authn.Identifier) bool {
				return i.Kind == authn.IdentifierUsername && i.Value == "social-made"
			}) ||
			!slices.ContainsFunc(held, func(i authn.Identifier) bool {
				return i.Kind == authn.IdentifierEmail && i.Value == "social@example.org" &&
					i.VerifiedVia == "idp:google" && i.Contact && i.DefaultContact && i.SignIn
			}) {
			t.Errorf("the social account holds the identifiers %+v, want its username and the declared address as a default contact and sign-in way", held)
		}

		otherProvider, err := firstLogin(Request{
			Proof: social("github", "google-subject", "", false), Purpose: authn.PurposeLogin,
		}, "other-made")
		if err != nil || otherProvider.Outcome != OutcomeCreated {
			t.Fatalf("the same subject under another provider = %+v, %v, want a new account", otherProvider, err)
		}
		if otherProvider.Account == socialID {
			t.Error("the same subject under another provider resolved to the Google account")
		}
		record("the same subject under another provider", otherProvider.Account, "google-subject")

		unverified, err := firstLogin(Request{
			Proof: social("github", "github-subject", "unverified@example.org", false), Purpose: authn.PurposeLogin,
		}, "unverified-made")
		if err != nil || unverified.Outcome != OutcomeCreated {
			t.Fatalf("the first unverified social login = %+v, %v, want a new account", unverified, err)
		}
		unverifiedID := record("social first sign-in without a verified address", unverified.Account,
			"github-subject", "unverified@example.org")
		if identifiers := identifiersOf(t, accounts, unverifiedID); len(identifiers) != 1 ||
			identifiers[0].Kind != authn.IdentifierUsername || identifiers[0].Value != "unverified-made" {
			t.Errorf("the account made without a verified address holds the identifiers %+v, want only its username", identifiers)
		}
		if again, resolveErr := r.Resolve(ctx, Request{
			Proof: social("github", "github-subject", "", false), Purpose: authn.PurposeLogin,
		}); resolveErr != nil || again.Account != unverifiedID || again.Outcome != OutcomeBound {
			t.Errorf("the second login of the unverified identity = %+v, %v, want the same account bound", again, resolveErr)
		}

		jitLogin, err := firstLogin(Request{
			Proof: authn.Proof{
				Provider: idpAcme,
				External: &authn.External{
					Provider: "oidc:" + idpAcme, Subject: "jit-subject", Email: "jit@acme.com",
					Tenant: tenantAcme, DisplayName: "Jit Person",
				},
			},
			Purpose: authn.PurposeLogin,
		}, "jit-made")
		if err != nil || jitLogin.Outcome != OutcomeCreated {
			t.Fatalf("the first enterprise login = %+v, %v, want a new account", jitLogin, err)
		}
		jitID := record("enterprise just-in-time first sign-in", jitLogin.Account, "jit-subject", "jit@acme.com")
		if held := identifiersOf(t, accounts, jitID); len(held) != 1 ||
			held[0].Kind != authn.IdentifierUsername || held[0].Value != "jit-made" {
			t.Errorf("the enterprise account holds the identifiers %+v, want its username alone: the provider's address is written nowhere", held)
		}
		if member, ok, memberErr := memberships.Membership(ctx, tenantAcme, jitID); memberErr != nil || !ok || !member.Active {
			t.Errorf("the enterprise account's membership = %+v, %v, %v, want an active one", member, ok, memberErr)
		}

		inviteHolder, err := admin.Create(ctx, authn.CreateAccount{
			Kind: authn.KindUser, Username: "invite-made", Email: "invite@example.org",
			EmailVerifiedVia: authn.Verification{Via: authn.VerifiedViaEmailCode}, Via: authn.ReasonAdmin,
		})
		if err != nil {
			t.Fatalf("preparing the invited account: %v", err)
		}
		issued, err := invites.Issue(ctx, invite.IssueInvite{Tenant: tenantAcme, Email: "invite@example.org"})
		if err != nil {
			t.Fatalf("issuing an invitation: %v", err)
		}
		acceptCtx := authntest.Context(t, ctx, authntest.WithAccount(inviteHolder.ID))
		invited, err := invites.Accept(acceptCtx, invite.AcceptInvite{Token: issued.Token})
		if err != nil {
			t.Fatalf("accepting an invitation: %v", err)
		}
		inviteID := record("invitation acceptance", invited.Account, "invite@example.org")

		administrator, err := admin.Create(ctx, authn.CreateAccount{
			Kind: authn.KindUser, Username: "admin-made", Via: authn.ReasonAdmin,
		})
		if err != nil {
			t.Fatalf("an administrator's create: %v", err)
		}
		adminID := record("administrator create", administrator.ID, "admin-made")

		imported, err := admin.Create(ctx, authn.CreateAccount{
			ID: "legacy_42", Kind: authn.KindUser, Username: "legacy42", Via: "import",
		})
		if err != nil {
			t.Fatalf("an import: %v", err)
		}
		if imported.ID != "legacy_42" {
			t.Errorf("the imported account's identifier = %q, want the one the host brought", imported.ID)
		}
		record("import with the host's identifier", imported.ID, "legacy42")

		seen := map[authn.AccountID]string{}
		for _, made := range minted {
			if first, dup := seen[made.id]; dup {
				t.Errorf("%s and %s share the identifier %q", first, made.path, made.id)
			}
			seen[made.id] = made.path
			if made.id.IsStatic() {
				t.Errorf("%s: identifier %q is a static-table subject", made.path, made.id)
			}
			for _, forbidden := range made.notEqual {
				if string(made.id) == forbidden {
					t.Errorf("%s: identifier %q is the login identifier or subject itself", made.path, made.id)
				}
			}
		}

		for _, made := range []authn.AccountID{passwordID, phoneID, socialID, unverifiedID, jitID, inviteID, adminID} {
			parsed, err := uuid.Parse(string(made))
			if err != nil || parsed.Version() != 7 {
				t.Errorf("identifier %q is not a UUIDv7 text", made)
			}
		}
		if string(imported.ID) != "legacy_42" {
			t.Errorf("the imported identifier was replaced")
		}

		for i, made := range minted {
			if err := admin.SetDisplayName(ctx, made.id, fmt.Sprintf("Renamed %d", i)); err != nil {
				t.Errorf("%s: setting the display name: %v", made.path, err)
			}
			if err := admin.AddIdentifier(ctx, made.id, authn.IdentifierEmail,
				fmt.Sprintf("changed-%d@example.org", i), authn.Verification{Via: authn.VerifiedViaAdmin}); err != nil {
				t.Errorf("%s: adding an email: %v", made.path, err)
			}
		}
		if res, err := r.Resolve(ctx, Request{
			Proof: social("google", "google-subject", "social-changed@example.org", true), Purpose: authn.PurposeLogin,
		}); err != nil || res.Account != socialID {
			t.Errorf("the social login after the profile changed = %+v, %v, want %q", res, err, socialID)
		}
		if res, err := r.Resolve(ctx, Request{
			Proof: authn.Proof{
				Provider: idpAcme,
				External: &authn.External{
					Provider: "oidc:" + idpAcme, Subject: "jit-subject", Email: "jit-changed@acme.com",
					Tenant: tenantAcme, DisplayName: "Renamed Jit",
				},
			},
			Purpose: authn.PurposeLogin,
		}); err != nil || res.Account != jitID {
			t.Errorf("the enterprise login after the profile changed = %+v, %v, want %q", res, err, jitID)
		}
		for _, made := range minted {
			read, err := accounts.Get(ctx, made.id)
			if err != nil {
				t.Errorf("%s: reading %q after the profile changes: %v", made.path, made.id, err)
				continue
			}
			if read.ID != made.id {
				t.Errorf("%s: the identifier changed from %q to %q", made.path, made.id, read.ID)
			}
		}
	})
}

// TestSocialAccountWithoutAVerifiedAddressIsCompleteAndKeepsItsIdentifier holds that an unverified address still opens a complete account without it.
func TestSocialAccountWithoutAVerifiedAddressIsCompleteAndKeepsItsIdentifier(t *testing.T) {
	e := newEnv(t, withSignup(SignupOpen))
	first, err := e.resolve(social("github", "gh-subject", "unverified@example.org", false))
	if err != nil || first.Outcome != OutcomeCreated {
		t.Fatalf("Resolve = %+v, %v, want a new account", first, err)
	}
	if first.Account == "" {
		t.Fatal("the account has no identifier")
	}
	for _, forbidden := range []string{"gh-subject", "unverified@example.org"} {
		if string(first.Account) == forbidden {
			t.Errorf("the account identifier is %q itself, want an identifier of its own", forbidden)
		}
	}
	if ids := e.s.identifiersOf(first.Account); len(ids) != 0 {
		t.Errorf("identifiers = %+v, want none: the provider did not verify the address", ids)
	}
	if a := e.s.account(first.Account); a.DisplayName != "Ann" || a.Status != authn.StatusActive {
		t.Errorf("the account = %+v, want the provider's profile on an enabled account", a)
	}
	again, err := e.resolve(social("github", "gh-subject", "", false))
	if err != nil || again.Account != first.Account || again.Outcome != OutcomeBound {
		t.Errorf("the second login = %+v, %v, want the same account bound", again, err)
	}
}

func identifiersOf(t *testing.T, accounts authn.Accounts, id authn.AccountID) []authn.Identifier {
	t.Helper()
	identifiers, err := accounts.Identifiers(context.Background(), id)
	if err != nil {
		t.Fatalf("reading the identifiers of %q: %v", id, err)
	}
	return identifiers
}

func mustResolve[T any](t *testing.T, reg *core.Registry) T {
	t.Helper()
	value, err := core.Resolve[T](reg)
	if err != nil {
		t.Fatalf("taking up %T: %v", value, err)
	}
	return value
}

type jitRegister struct{ authn.SSOProviders }

func (jitRegister) provider() authn.SSOProvider {
	return authn.SSOProvider{
		ID: idpAcme, Tenant: tenantAcme, Protocol: authn.SSOProtocolOIDC,
		Enabled: true, Provisioning: authn.ProvisioningJIT,
		UsernameStrategy: authn.UsernameStrategyUserConfirm,
	}
}

func (r jitRegister) Get(_ context.Context, id string) (authn.SSOProvider, error) {
	if id != idpAcme {
		return authn.SSOProvider{}, fmt.Errorf("no provider %q", id)
	}
	return r.provider(), nil
}

func (r jitRegister) Discover(_ context.Context, domain string) ([]authn.SSOProvider, error) {
	if domain != "acme.com" {
		return nil, nil
	}
	return []authn.SSOProvider{r.provider()}, nil
}

type standInSettings struct{}

const passwordSignupKey = "authn.account.password-signup"

func (standInSettings) Get(_ settings.Layer, key string, target any) error {
	if key != passwordSignupKey {
		return settings.ErrUnknownKey
	}
	value, ok := target.(*string)
	if !ok {
		return settings.ErrTypeMismatch
	}
	*value = "open"
	return nil
}

func (standInSettings) Set(context.Context, settings.Write) error {
	return errors.New("identity-stability test: the settings stand-in is read-only")
}

func (standInSettings) Delete(context.Context, settings.Layer, string, string) error {
	return errors.New("identity-stability test: the settings stand-in is read-only")
}

func (standInSettings) List(context.Context, settings.Layer) ([]settings.Entry, error) {
	return nil, nil
}

func (standInSettings) Declared() []settings.DeclaredItem { return nil }

func (standInSettings) WrittenAt(settings.Layer, string) (time.Time, bool, error) {
	return time.Time{}, false, nil
}

func (s standInSettings) Read(key string, reads ...settings.LayerRead) ([]settings.LayerReadResult, error) {
	results := make([]settings.LayerReadResult, len(reads))
	for i, read := range reads {
		if err := s.Get(read.Layer, key, read.Target); err != nil {
			return nil, err
		}
		results[i] = settings.LayerReadResult{}
	}
	return results, nil
}

type standInAudit struct{}

func (standInAudit) Record(context.Context, audit.Entry) error { return nil }

type standInTenantDescriptions struct{}

func (standInTenantDescriptions) TenantDescriptionOf(_ context.Context, id string) (authn.TenantDescription, error) {
	return authn.TenantDescription{Name: id}, nil
}

type noopMounter struct{}

func (noopMounter) Public(string, string, gin.HandlerFunc, ...mountinternal.Option)   {}
func (noopMounter) Optional(string, string, gin.HandlerFunc, ...mountinternal.Option) {}
func (noopMounter) Guarded(string, string, gin.HandlerFunc, ...mountinternal.Option)  {}
func (noopMounter) Callback([]string, string, gin.HandlerFunc)                        {}
func (noopMounter) BaseIn(speedhttp.Scope) string                                     { return "" }
func (noopMounter) Endpoint() string                                                  { return "public" }

func (noopMounter) PublicURL() string                 { return "http://127.0.0.1" }
func (noopMounter) CSRF() *http.CrossOriginProtection { return nil }

func module(name string, token any, build func() (any, error)) core.Module {
	return core.Module{
		Name:     name,
		Provides: []core.Provision{{Token: token, Exclusive: true}},
		New:      func(context.Context, *core.Registry) (any, error) { return build() },
	}
}

func withRealAccountStore(t *testing.T, use func(context.Context, *core.Registry)) {
	t.Helper()
	dsn := filepath.Join(t.TempDir(), "identity-stability.db")
	document := map[string]any{
		"db": map[string]any{"sqlite": map[string]any{"dsn": dsn}},
	}
	body, err := json.Marshal(document)
	if err != nil {
		t.Fatalf("encoding the configuration: %v", err)
	}

	modules := []core.Module{
		sqlite.Module(), account.Module(), provisioningtest.Module(), invite.Module(), kvmemory.Module(),
		module("stand-in.identity-settings", (*settings.Store)(nil), func() (any, error) { return standInSettings{}, nil }),
		module("stand-in.identity-audit", (*audit.Audit)(nil), func() (any, error) { return standInAudit{}, nil }),
		module("stand-in.identity-tenant-descriptions", (*authn.TenantDescriptionResolver)(nil),
			func() (any, error) { return standInTenantDescriptions{}, nil }),
		module("stand-in.identity-mounter", (*mountinternal.Mounter)(nil), func() (any, error) { return noopMounter{}, nil }),
	}
	reader := authntest.ConfigReaderAll(t, string(body), modules...)

	reg := core.New()
	names := make([]string, 0, len(modules))
	for _, m := range modules {
		reg.Register(m)
		names = append(names, m.Name)
	}
	reg.Register(config.Module())
	reg.Supply(reader, config.ModuleName)

	err = reg.Drive(t.Context(), core.Drive{
		Depth:        core.StageStart,
		Participants: names,
		Action: func(ctx context.Context, reg *core.Registry) error {
			use(ctx, reg)
			return nil
		},
	})
	if err != nil {
		t.Fatalf("driving the real account and invitation modules: %v", err)
	}
}
