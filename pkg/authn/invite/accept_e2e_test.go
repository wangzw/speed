package invite

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"gorm.io/gorm"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/authn/account"
	"github.com/vislake/speed/pkg/authn/authntest"
	"github.com/vislake/speed/pkg/authn/internal/mount/mounttest"
	"github.com/vislake/speed/pkg/authn/provisioning"
	"github.com/vislake/speed/pkg/authn/provisioning/provisioningtest"
	"github.com/vislake/speed/pkg/config"
	"github.com/vislake/speed/pkg/core"
	"github.com/vislake/speed/pkg/db"
	"github.com/vislake/speed/pkg/db/sqlite"
	"github.com/vislake/speed/pkg/settings"
)

// TestAcceptAddsAMembershipForAProvenAccountThroughTheRealAccountModule checks that a redemption adds the membership and writes no account or identifier.
func TestAcceptAddsAMembershipForAProvenAccountThroughTheRealAccountModule(t *testing.T) {
	withRealAccountProduct(t, func(handle *gorm.DB, _ authn.Signups, admin authn.AccountAdmin, accounts authn.Accounts,
		verification authn.SignupVerification, memberships authn.Memberships,
	) {
		applyMigrations(t, handle)
		p := newRealProduct(t, handle, admin, accounts, verification, memberships)

		ctx := context.Background()
		created, err := admin.Create(ctx, authn.CreateAccount{
			Kind: authn.KindUser, Username: "newcomer", DisplayName: "Newcomer",
			Email: "login@example.com", EmailVerifiedVia: authn.Verification{Via: authn.VerifiedViaEmailCode},
			Via: authn.ReasonAdmin,
		})
		if err != nil {
			t.Fatalf("creating the account: %v", err)
		}
		if addErr := admin.AddIdentifier(ctx, created.ID, authn.IdentifierEmail, "newcomer@example.com",
			authn.Verification{Via: authn.VerifiedViaEmailCode}); addErr != nil {
			t.Fatalf("proving the invited address: %v", addErr)
		}

		issueCtx := authntest.Context(t, ctx, authntest.WithAccount(adminID))
		issued, err := p.Issue(issueCtx, IssueInvite{Tenant: tenant, Email: "Newcomer@Example.com"})
		if err != nil {
			t.Fatalf("issuing an invitation: %v", err)
		}

		acceptCtx := authntest.Context(t, ctx, authntest.WithAccount(created.ID))
		mem, err := p.Accept(acceptCtx, AcceptInvite{Token: issued.Token})
		if err != nil {
			t.Fatalf("accepting the invitation: %v", err)
		}
		if mem.Account != created.ID || !mem.Active || mem.Source != SourceInvite {
			t.Errorf("membership = %+v, want the proven account active with source %q", mem, SourceInvite)
		}

		got, ok, err := memberships.Membership(ctx, tenant, created.ID)
		if err != nil || !ok {
			t.Fatalf("reading the membership back: ok=%t, err=%v", ok, err)
		}
		if !got.Active || got.Source != SourceInvite {
			t.Errorf("membership = %+v, want active with source %q", got, SourceInvite)
		}

		identifier := emailIdentifier(t, ctx, accounts, created.ID, "newcomer@example.com")
		if identifier.VerifiedVia != authn.VerifiedViaEmailCode {
			t.Errorf("the invited address reads via %q, want the email-code proof it already had", identifier.VerifiedVia)
		}
		if identifier.SignIn {
			t.Error("the acceptance made the invited address a sign-in identifier")
		}
		if n := identifierCount(t, handle, created.ID, "newcomer@example.com"); n != 1 {
			t.Errorf("the invited address holds %d identifier rows, want the one it had", n)
		}
	})
}

// TestAcceptRefusesAnAccountWithoutTheInvitedAddressThroughTheRealAccountModule checks that a caller proving another address is refused and nothing is written.
func TestAcceptRefusesAnAccountWithoutTheInvitedAddressThroughTheRealAccountModule(t *testing.T) {
	withRealAccountProduct(t, func(handle *gorm.DB, _ authn.Signups, admin authn.AccountAdmin, accounts authn.Accounts,
		verification authn.SignupVerification, memberships authn.Memberships,
	) {
		applyMigrations(t, handle)
		p := newRealProduct(t, handle, admin, accounts, verification, memberships)

		ctx := context.Background()
		created, err := admin.Create(ctx, authn.CreateAccount{
			Kind: authn.KindUser, Username: "newcomer", Email: "login@example.com",
			EmailVerifiedVia: authn.Verification{Via: authn.VerifiedViaEmailCode}, Via: authn.ReasonAdmin,
		})
		if err != nil {
			t.Fatalf("creating the account: %v", err)
		}

		issueCtx := authntest.Context(t, ctx, authntest.WithAccount(adminID))
		issued, err := p.Issue(issueCtx, IssueInvite{Tenant: tenant, Email: "newcomer@example.com"})
		if err != nil {
			t.Fatalf("issuing an invitation: %v", err)
		}

		acceptCtx := authntest.Context(t, ctx, authntest.WithAccount(created.ID))
		if _, err := p.Accept(acceptCtx, AcceptInvite{Token: issued.Token}); !errors.Is(err, ErrInvitationEmailUnproved) {
			t.Fatalf("accepting without the invited address gave %v, want ErrInvitationEmailUnproved", err)
		}
		if _, ok, err := memberships.Membership(ctx, tenant, created.ID); err != nil || ok {
			t.Errorf("a membership was written: ok=%t, err=%v", ok, err)
		}
		if got := rowOf(t, handle, issued.Invitation.ID).Status; got != statusPending {
			t.Errorf("the invitation is %d, want pending", got)
		}
		if n := accountCount(t, handle); n != 1 {
			t.Errorf("the database holds %d accounts, want the one caller alone", n)
		}
	})
}

// TestAFreshlyRegisteredAccountCannotAcceptUntilItProvesTheInvitedAddress checks that a pending registration is refused until the address is proved, then the same token redeems.
func TestAFreshlyRegisteredAccountCannotAcceptUntilItProvesTheInvitedAddress(t *testing.T) {
	withRealAccountProduct(t, func(handle *gorm.DB, signups authn.Signups, admin authn.AccountAdmin,
		accounts authn.Accounts, verification authn.SignupVerification, memberships authn.Memberships,
	) {
		applyMigrations(t, handle)
		p := newRealProduct(t, handle, admin, accounts, verification, memberships)

		ctx := context.Background()
		registered, err := signups.Register(ctx, authn.RegisterAccount{
			Username: "newcomer", Email: "Newcomer@Example.com", Password: "password123",
		})
		if err != nil {
			t.Fatalf("registering the account: %v", err)
		}
		if status, sErr := verification.SignupStateOf(ctx, registered.ID); sErr != nil || status.State != authn.SignupPending {
			t.Fatalf("the registered account is %+v (%v), want a pending registration", status, sErr)
		}

		issueCtx := authntest.Context(t, ctx, authntest.WithAccount(adminID))
		issued, err := p.Issue(issueCtx, IssueInvite{Tenant: tenant, Email: "newcomer@example.com"})
		if err != nil {
			t.Fatalf("issuing an invitation: %v", err)
		}

		acceptCtx := authntest.Context(t, ctx, authntest.WithAccount(registered.ID))
		if _, acceptErr := p.Accept(acceptCtx, AcceptInvite{Token: issued.Token}); !errors.Is(acceptErr, ErrInvitationEmailUnproved) {
			t.Fatalf("accepting before the proof gave %v, want ErrInvitationEmailUnproved", acceptErr)
		}
		if _, ok, readErr := memberships.Membership(ctx, tenant, registered.ID); readErr != nil || ok {
			t.Errorf("a membership was written before the proof: ok=%t, err=%v", ok, readErr)
		}

		if addErr := admin.AddIdentifier(ctx, registered.ID, authn.IdentifierEmail, "newcomer@example.com",
			authn.Verification{Via: authn.VerifiedViaEmailCode}); addErr != nil {
			t.Fatalf("proving the address: %v", addErr)
		}
		status, err := verification.SignupStateOf(ctx, registered.ID)
		if err != nil || status.State != authn.SignupVerified {
			t.Fatalf("the account reads %+v (%v), want verified", status, err)
		}

		mem, err := p.Accept(acceptCtx, AcceptInvite{Token: issued.Token})
		if err != nil {
			t.Fatalf("accepting after the proof: %v", err)
		}
		if mem.Account != registered.ID {
			t.Errorf("the membership names %q, want the registered account %q", mem.Account, registered.ID)
		}
		got, ok, err := memberships.Membership(ctx, tenant, registered.ID)
		if err != nil || !ok || !got.Active || got.Source != SourceInvite {
			t.Errorf("membership = %+v (%v, %v), want an active invite membership", got, ok, err)
		}
		if n := accountCount(t, handle); n != 1 {
			t.Errorf("the database holds %d accounts, want the one registration alone", n)
		}
		identifier := emailIdentifier(t, ctx, accounts, registered.ID, "newcomer@example.com")
		if identifier.VerifiedVia != authn.VerifiedViaEmailCode {
			t.Errorf("the address reads via %q, want the email-code proof", identifier.VerifiedVia)
		}
	})
}

// TestAcceptRefusesASuspendedMembershipThroughTheRealAccountModule checks that a suspended member is refused, stays suspended, and the invitation is revoked.
func TestAcceptRefusesASuspendedMembershipThroughTheRealAccountModule(t *testing.T) {
	withRealAccountProduct(t, func(handle *gorm.DB, _ authn.Signups, admin authn.AccountAdmin, accounts authn.Accounts,
		verification authn.SignupVerification, memberships authn.Memberships,
	) {
		applyMigrations(t, handle)
		p := newRealProduct(t, handle, admin, accounts, verification, memberships)

		ctx := context.Background()
		created, err := admin.Create(ctx, authn.CreateAccount{
			Kind: authn.KindUser, Username: "newcomer", Email: "newcomer@example.com",
			EmailVerifiedVia: authn.Verification{Via: authn.VerifiedViaEmailCode}, Via: authn.ReasonAdmin,
		})
		if err != nil {
			t.Fatalf("creating the account: %v", err)
		}
		issueCtx := authntest.Context(t, ctx, authntest.WithAccount(adminID))
		issued, err := p.Issue(issueCtx, IssueInvite{Tenant: tenant, Email: "newcomer@example.com"})
		if err != nil {
			t.Fatalf("issuing an invitation: %v", err)
		}
		if putErr := memberships.Put(ctx, authn.Membership{
			Account: created.ID, Tenant: tenant, Active: false, Source: "admin",
		}); putErr != nil {
			t.Fatalf("suspending the membership: %v", putErr)
		}

		acceptCtx := authntest.Context(t, ctx, authntest.WithAccount(created.ID))
		if _, acceptErr := p.Accept(acceptCtx, AcceptInvite{Token: issued.Token}); !errors.Is(acceptErr, ErrInvitationMembershipInactive) {
			t.Fatalf("accepting over a suspended membership gave %v, want ErrInvitationMembershipInactive", acceptErr)
		}
		got, ok, err := memberships.Membership(ctx, tenant, created.ID)
		if err != nil || !ok {
			t.Fatalf("reading the membership back: ok=%t, err=%v", ok, err)
		}
		if got.Active {
			t.Error("the acceptance reactivated the suspended membership")
		}
		if got.Source != "admin" {
			t.Errorf("the membership source = %q, want admin", got.Source)
		}
		if status := rowOf(t, handle, issued.Invitation.ID).Status; status != statusRevoked {
			t.Errorf("the invitation is %d, want revoked", status)
		}
	})
}

// TestAcceptPreservesAnExistingMembershipsSourceAndExemptionThroughTheRealAccountModule checks that an existing active membership keeps its source and SSO exemption.
func TestAcceptPreservesAnExistingMembershipsSourceAndExemptionThroughTheRealAccountModule(t *testing.T) {
	withRealAccountProduct(t, func(handle *gorm.DB, _ authn.Signups, admin authn.AccountAdmin, accounts authn.Accounts,
		verification authn.SignupVerification, memberships authn.Memberships,
	) {
		applyMigrations(t, handle)
		p := newRealProduct(t, handle, admin, accounts, verification, memberships)

		ctx := context.Background()
		created, err := admin.Create(ctx, authn.CreateAccount{
			Kind: authn.KindUser, Username: "newcomer", Email: "newcomer@example.com",
			EmailVerifiedVia: authn.Verification{Via: authn.VerifiedViaEmailCode}, Via: authn.ReasonAdmin,
		})
		if err != nil {
			t.Fatalf("creating the account: %v", err)
		}
		issueCtx := authntest.Context(t, ctx, authntest.WithAccount(adminID))
		issued, err := p.Issue(issueCtx, IssueInvite{Tenant: tenant, Email: "newcomer@example.com"})
		if err != nil {
			t.Fatalf("issuing an invitation: %v", err)
		}
		if putErr := memberships.Put(ctx, authn.Membership{
			Account: created.ID, Tenant: tenant, Active: true, Source: "admin", SSOExempt: true,
		}); putErr != nil {
			t.Fatalf("seeding the membership: %v", putErr)
		}

		acceptCtx := authntest.Context(t, ctx, authntest.WithAccount(created.ID))
		if _, acceptErr := p.Accept(acceptCtx, AcceptInvite{Token: issued.Token}); acceptErr != nil {
			t.Fatalf("accepting with an active membership: %v", acceptErr)
		}
		got, ok, err := memberships.Membership(ctx, tenant, created.ID)
		if err != nil || !ok {
			t.Fatalf("reading the membership back: ok=%t, err=%v", ok, err)
		}
		if got.Source != "admin" || !got.SSOExempt || !got.Active {
			t.Errorf("membership = %+v, want admin, SSO-exempt and active", got)
		}
	})
}

func newRealProduct(t *testing.T, handle *gorm.DB, admin authn.AccountAdmin, accounts authn.Accounts,
	verification authn.SignupVerification, memberships authn.Memberships,
) *product {
	t.Helper()
	p := &product{
		handle:       handle,
		accounts:     accounts,
		verification: verification,
		memberships:  memberships,
		mounter:      mounttest.New(t),
		audit:        &recordingAudit{},
		kv:           newFakeKV(),
		decider:      &fakeDecider{allow: true},
		tenantNames:  identityNames{},
		tenantDescs:  &fakeTenantDescriptions{byID: map[string]authn.TenantDescription{}},
		logger:       (&logBuffer{}).logger(),
	}
	if writer, ok := memberships.(provisioning.MembershipWriter); ok {
		p.membershipWriter = writer
	}
	p.now = newTestClock().read
	return p
}

func rowOf(t *testing.T, handle *gorm.DB, id string) invitationRow {
	t.Helper()
	var rows []invitationRow
	if err := handle.Where("id = ?", id).Find(&rows).Error; err != nil {
		t.Fatalf("reading %s: %v", id, err)
	}
	if len(rows) == 0 {
		t.Fatalf("no row %s", id)
	}
	return rows[0]
}

func emailIdentifier(t *testing.T, ctx context.Context, accounts authn.Accounts, id authn.AccountID, value string) authn.Identifier {
	t.Helper()
	identifiers, err := accounts.Identifiers(ctx, id)
	if err != nil {
		t.Fatalf("reading the identifiers of %q: %v", id, err)
	}
	var found []authn.Identifier
	for _, identifier := range identifiers {
		if identifier.Kind == authn.IdentifierEmail && identifier.Value == value {
			found = append(found, identifier)
		}
	}
	if len(found) != 1 {
		t.Fatalf("the account holds %d email identifier(s) for %s, want one", len(found), value)
	}
	return found[0]
}

func identifierCount(t *testing.T, handle *gorm.DB, id authn.AccountID, value string) int64 {
	t.Helper()
	var n int64
	if err := handle.Raw("SELECT count(*) FROM authn_identifiers WHERE account_id = ? AND value = ?",
		id.String(), value).Scan(&n).Error; err != nil {
		t.Fatalf("counting the identifiers of %q: %v", id, err)
	}
	return n
}

func accountCount(t *testing.T, handle *gorm.DB) int64 {
	t.Helper()
	var n int64
	if err := handle.Raw("SELECT count(*) FROM authn_accounts").Scan(&n).Error; err != nil {
		t.Fatalf("counting the accounts: %v", err)
	}
	return n
}

const openPasswordSignupKey = "authn.account.password-signup"

type openPasswordSignup struct{}

func (openPasswordSignup) Get(_ settings.Layer, key string, target any) error {
	if key != openPasswordSignupKey {
		return settings.ErrUnknownKey
	}
	value, ok := target.(*string)
	if !ok {
		return settings.ErrTypeMismatch
	}
	*value = "open"
	return nil
}

func (openPasswordSignup) Set(context.Context, settings.Write) error {
	return errors.New("invite test: the stand-in settings store is read-only")
}

func (openPasswordSignup) Delete(context.Context, settings.Layer, string, string) error {
	return errors.New("invite test: the stand-in settings store is read-only")
}

func (openPasswordSignup) List(context.Context, settings.Layer) ([]settings.Entry, error) {
	return nil, nil
}

func (openPasswordSignup) Declared() []settings.DeclaredItem { return nil }

func (openPasswordSignup) WrittenAt(settings.Layer, string) (time.Time, bool, error) {
	return time.Time{}, false, nil
}

func (openPasswordSignup) Read(key string, reads ...settings.LayerRead) ([]settings.LayerReadResult, error) {
	results := make([]settings.LayerReadResult, len(reads))
	for i, read := range reads {
		if err := (openPasswordSignup{}).Get(read.Layer, key, read.Target); err != nil {
			return nil, err
		}
		results[i] = settings.LayerReadResult{}
	}
	return results, nil
}

func provideSettings() core.Module {
	return core.Module{
		Name:     "stand-in.invite-settings",
		Provides: []core.Provision{{Token: (*settings.Store)(nil), Exclusive: true}},
		New:      func(context.Context, *core.Registry) (any, error) { return openPasswordSignup{}, nil },
	}
}

// use runs before the drive closes the pool, which is why it takes a callback.
func withRealAccountProduct(t *testing.T,
	use func(*gorm.DB, authn.Signups, authn.AccountAdmin, authn.Accounts, authn.SignupVerification, authn.Memberships),
) {
	t.Helper()
	dsn := filepath.Join(t.TempDir(), "invite-e2e.db")
	body := `{"db":{"sqlite":{"dsn":"` + dsn + `"}}}`
	mods := []core.Module{sqlite.Module(), account.Module(), provisioningtest.Module(), provideSettings()}

	reader := authntest.ConfigReaderAll(t, body, mods...)
	reg := core.New()
	names := make([]string, 0, len(mods))
	for _, m := range mods {
		reg.Register(m)
		names = append(names, m.Name)
	}
	reg.Register(config.Module())
	reg.Supply(reader, config.ModuleName)

	err := reg.Drive(t.Context(), core.Drive{
		Depth:        core.StageStart,
		Participants: names,
		Action: func(_ context.Context, reg *core.Registry) error {
			database, err := core.Resolve[db.Database](reg)
			if err != nil {
				return err
			}
			signups, err := core.Resolve[authn.Signups](reg)
			if err != nil {
				return err
			}
			admin, err := core.Resolve[authn.AccountAdmin](reg)
			if err != nil {
				return err
			}
			accounts, err := core.Resolve[authn.Accounts](reg)
			if err != nil {
				return err
			}
			verification, err := core.Resolve[authn.SignupVerification](reg)
			if err != nil {
				return err
			}
			memberships, err := core.Resolve[authn.Memberships](reg)
			if err != nil {
				return err
			}
			use(database.DB(), signups, admin, accounts, verification, memberships)
			return nil
		},
	})
	if err != nil {
		t.Fatalf("driving the account module: %v", err)
	}
}
