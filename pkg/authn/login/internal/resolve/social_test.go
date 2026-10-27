package resolve

import (
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/vislake/speed/pkg/authn"
)

// A3 and first-login provisioning for a social identity with no binding.

func TestSocialLinkRequiredOnlyForAVerifiedAddressOnAnExistingAccount(t *testing.T) {
	e := newEnv(t, withSignup(SignupOpen))
	e.s.seed(t, seed{id: "a1", status: authn.StatusActive, email: "ann@home.org", emailVia: "idp:google"})
	_, err := e.resolve(social("google", "g9", "ann@home.org", true))
	if !errors.Is(err, authn.ErrLinkRequired) {
		t.Fatalf("err = %v, want ErrLinkRequired", err)
	}
	var conflicted *authn.LinkRequiredError
	if !errors.As(err, &conflicted) {
		t.Fatalf("err = %v, want a *authn.LinkRequiredError", err)
	}
	if conflicted.Email != "ann@home.org" {
		t.Errorf("the refusal names %q, want the fixture address", conflicted.Email)
	}
	if e.s.writes() != 0 {
		t.Errorf("link-required wrote %d times, want nothing written", e.s.writes())
	}
	if _, ok := e.s.boundTo("google", "g9"); ok {
		t.Error("the identity was bound on the way to link-required")
	}
}

func TestSocialUnverifiedAddressIsNotAClue(t *testing.T) {
	// The provider's own claim that it did not verify the address does not turn
	// into link-required, which would tell a stranger an account holds it. The
	// answer is the one for an address nobody holds.
	e := newEnv(t, withSignup(SignupClosed))
	e.s.seed(t, seed{id: "a1", status: authn.StatusActive, email: "ann@home.org", emailVia: "idp:google"})
	_, held := e.resolve(social("google", "g9", "ann@home.org", false))
	_, unheld := e.resolve(social("google", "g10", "nobody@home.org", false))
	if !errors.Is(held, authn.ErrSignupClosed) || !errors.Is(unheld, authn.ErrSignupClosed) {
		t.Errorf("held = %v, unheld = %v, want both ErrSignupClosed", held, unheld)
	}
	if errors.Is(held, authn.ErrLinkRequired) {
		t.Error("an unverified claim produced link-required")
	}
}

func TestSocialUnverifiedAddressOpensTheAccountWithoutTheAddress(t *testing.T) {
	e := newEnv(t, withSignup(SignupOpen))
	holder := e.s.seed(t, seed{id: "a1", status: authn.StatusActive, email: "ann@home.org", emailVia: "idp:google"})
	res, err := e.resolve(social("google", "g9", "ann@home.org", false))
	if err != nil || res.Outcome != OutcomeCreated || res.Account == holder {
		t.Fatalf("Resolve = %+v, %v, want a new account", res, err)
	}
	if ids := e.s.identifiersOf(res.Account); len(ids) != 0 {
		t.Errorf("identifiers = %+v, want none for an address the provider did not verify", ids)
	}
	if ids := e.s.identifiersOf(holder); len(ids) != 1 || e.s.bindingsOf(holder) != nil {
		t.Errorf("the account that holds the address changed: %+v", ids)
	}
}

func TestSocialClosedSignupCreatesNothing(t *testing.T) {
	e := newEnv(t)
	_, err := e.resolve(social("github", "gh1", "", false))
	if !errors.Is(err, authn.ErrSignupClosed) {
		t.Fatalf("err = %v, want ErrSignupClosed", err)
	}
	if e.s.writes() != 0 {
		t.Errorf("%d writes, want none", e.s.writes())
	}
}

func TestSocialOpenSignupCreatesAnEnabledUserWithNoMembership(t *testing.T) {
	e := newEnv(t, withSignup(SignupOpen))
	long := strings.Repeat("名", 150)
	p := social("google", "g1", "new@home.org", true)
	p.External.DisplayName = long
	res, err := e.resolve(p)
	if err != nil || res.Outcome != OutcomeCreated || res.MembershipCreated {
		t.Fatalf("Resolve = %+v, %v", res, err)
	}
	a := e.s.account(res.Account)
	if a.Kind != authn.KindUser || a.Status != authn.StatusActive {
		t.Errorf("account = %+v, want an enabled user", a)
	}
	if n := len([]rune(a.DisplayName)); n != 100 {
		t.Errorf("display name is %d characters, want it cut to 100", n)
	}
	ids := e.s.identifiersOf(res.Account)
	if len(ids) != 1 || ids[0].Value != "new@home.org" || ids[0].VerifiedVia != "idp:google" {
		t.Errorf("identifiers = %+v, want the verified address written with idp:google", ids)
	}
	if got, ok := e.s.boundTo("google", "g1"); !ok || got != res.Account {
		t.Errorf("binding = %v, %v, want it on the new account", got, ok)
	}
	if e.s.membershipRead != 0 || len(e.s.membershipPuts) != 0 {
		t.Errorf("memberships touched: %d reads, %d writes, want none", e.s.membershipRead, len(e.s.membershipPuts))
	}
	if e.s.created[0].Via != "signup" {
		t.Errorf("Via = %q, want signup", e.s.created[0].Via)
	}
}

func TestSocialOpenSignupWritesNoAddressTheProviderDidNotVerify(t *testing.T) {
	e := newEnv(t, withSignup(SignupOpen))
	res, err := e.resolve(social("github", "gh1", "x@home.org", false))
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if ids := e.s.identifiersOf(res.Account); len(ids) != 0 {
		t.Errorf("identifiers = %+v, want none", ids)
	}
	if e.s.created[0].Email != "" {
		t.Errorf("Create got email %q, want none", e.s.created[0].Email)
	}
}

func TestSocialOpenSignupKeepsGoingWhenTheAddressIsTakenMeanwhile(t *testing.T) {
	e := newEnv(t, withSignup(SignupOpen))
	taken := false
	e.s.beforeCreate = func(in authn.CreateAccount) {
		if !taken && in.Email != "" {
			taken = true
			e.s.seed(t, seed{id: "late", status: authn.StatusActive, email: in.Email, emailVia: "idp:google"})
		}
	}
	res, err := e.resolve(social("google", "g1", "race@home.org", true))
	if err != nil || res.Outcome != OutcomeCreated {
		t.Fatalf("Resolve = %+v, %v", res, err)
	}
	if ids := e.s.identifiersOf(res.Account); len(ids) != 0 {
		t.Errorf("identifiers = %+v, want the account made without the address another holds", ids)
	}
}

// A4. Every case below satisfies all the conditions but one, and the one that
// fails sends the login back to A3: link-required, nothing bound.

func autoLinkEnv(t *testing.T, sd seed, opts ...option) *env {
	t.Helper()
	e := newEnv(t, append([]option{withAutoLink(AutoLinkVerifiedEmail), withSignup(SignupOpen)}, opts...)...)
	sd.id, sd.status = "a1", authn.StatusActive
	if sd.email == "" {
		sd.email, sd.emailVia = "ann@home.org", "idp:google"
	}
	e.s.seed(t, sd)
	return e
}

func TestAutoLinkMergesAndOwesTheCleanupInOrder(t *testing.T) {
	e := autoLinkEnv(t, seed{password: true, mfaRequired: true, factors: 2, tenants: []string{tenantAcme, tenantElse}})
	before := e.s.account("a1")
	res, err := e.resolve(social("google", "g9", "ann@home.org", true))
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if res.Account != "a1" || res.Outcome != OutcomeMerged {
		t.Fatalf("Resolve = %+v, want a merge into a1", res)
	}
	if !slices.Equal(res.Cleanup, []Cleanup{CleanupRevokeSessions, CleanupClearPassword}) {
		t.Errorf("Cleanup = %v, want revoke the sessions first and clear the password second", res.Cleanup)
	}
	if got, ok := e.s.boundTo("google", "g9"); !ok || got != "a1" {
		t.Errorf("binding = %v, %v, want it on a1", got, ok)
	}
	// The link changes the first factor only. The account's own requirements, its
	// factors and its memberships are what they were, and the resolver did not
	// touch them.
	if after := e.s.account("a1"); after.MFARequired != before.MFARequired || after.PasswordSet != before.PasswordSet {
		t.Errorf("account changed from %+v to %+v, want the requirements untouched", before, after)
	}
	if len(e.s.factors["a1"]) != 2 {
		t.Errorf("factors = %d, want 2 still registered", len(e.s.factors["a1"]))
	}
	if len(e.s.other) != 0 || len(e.s.membershipPuts) != 0 || len(e.s.created) != 0 {
		t.Errorf("the merge wrote %v, %d memberships, %d accounts, want only the binding", e.s.other, len(e.s.membershipPuts), len(e.s.created))
	}
	if e.s.membershipRead != 0 {
		t.Errorf("a social merge read memberships %d times, want 0", e.s.membershipRead)
	}
}

func TestAutoLinkConditionsEachSendTheLoginBackToLinkRequired(t *testing.T) {
	for _, tc := range []struct {
		name     string
		sd       seed
		opts     []option
		provider string
		disabled bool
	}{
		{name: "switch off", opts: []option{withAutoLink(AutoLinkNever)}},
		{name: "zero switch reads as off", opts: []option{withAutoLink("")}},
		{name: "address asserted by an administrator", sd: seed{email: "ann@home.org", emailVia: authn.VerifiedViaAdmin}},
		{name: "address recorded by an import", sd: seed{email: "ann@home.org", emailVia: authn.VerifiedViaImportUnverified}},
		{name: "address under a phone code", sd: seed{email: "ann@home.org", emailVia: authn.VerifiedViaSMSCode}},
		{name: "address under no channel", sd: seed{email: "ann@home.org", emailVia: ""}},
		{name: "account disabled", disabled: true},
		{name: "domain is a tenant's verified SSO domain", opts: []option{func(_ *Config, s *store) {
			s.domains["home.org"] = []authn.SSOProvider{{ID: "p", Tenant: tenantElse, Enabled: true}}
		}}},
		{name: "wechat", provider: "wechat"},
		{name: "wechat application key", provider: "wechat-openid:app1"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := newEnv(t, append([]option{withAutoLink(AutoLinkVerifiedEmail), withSignup(SignupOpen)}, tc.opts...)...)
			sd := tc.sd
			if sd.email == "" {
				sd.email, sd.emailVia = "ann@home.org", "idp:google"
			}
			sd.id, sd.status = "a1", authn.StatusActive
			if tc.disabled {
				sd.status = authn.StatusDisabled
			}
			e.s.seed(t, sd)
			provider := tc.provider
			if provider == "" {
				provider = "google"
			}
			p := social(provider, "g9", "ann@home.org", true)
			_, err := e.resolve(p)
			if !errors.Is(err, authn.ErrLinkRequired) {
				t.Fatalf("err = %v, want ErrLinkRequired", err)
			}
			if _, ok := e.s.boundTo(provider, "g9"); ok || len(e.s.linked) != 0 {
				t.Errorf("the identity was bound, want nothing bound")
			}
			if e.s.writes() != 0 {
				t.Errorf("%d writes, want none", e.s.writes())
			}
		})
	}
}

func TestAutoLinkNeedsTheProviderToHaveVerifiedTheAddress(t *testing.T) {
	e := autoLinkEnv(t, seed{})
	res, err := e.resolve(social("google", "g9", "ann@home.org", false))
	if err != nil || res.Outcome != OutcomeCreated || res.Account == "a1" {
		t.Fatalf("Resolve = %+v, %v, want a new account and no link", res, err)
	}
	if got := e.s.bindingsOf("a1"); len(got) != 0 {
		t.Errorf("a1 bindings = %v, want none", got)
	}
}

func TestAutoLinkRefusesWhenTheDomainRegisterCannotAnswer(t *testing.T) {
	boom := errors.New("register down")
	e := autoLinkEnv(t, seed{})
	e.s.failDiscover = boom
	_, err := e.resolve(social("google", "g9", "ann@home.org", true))
	if !errors.Is(err, boom) {
		t.Fatalf("err = %v, want the register's failure", err)
	}
	if len(e.s.linked) != 0 {
		t.Error("the identity was linked without knowing whether the domain is a tenant's")
	}
}

func TestAutoLinkWithoutAnSSORegisterHasNoDomainToRefuse(t *testing.T) {
	e := autoLinkEnv(t, seed{}, withoutSSO())
	res, err := e.resolve(social("google", "g9", "ann@home.org", true))
	if err != nil || res.Outcome != OutcomeMerged {
		t.Errorf("Resolve = %+v, %v, want a merge", res, err)
	}
}

func TestAutoLinkOnlyLooksAtTheAddressesDomainWhenItIsTheCandidate(t *testing.T) {
	// The domain of a verified address is asked of the register exactly when an
	// account holds the address and the other conditions have held.
	e := autoLinkEnv(t, seed{})
	if _, err := e.resolve(social("google", "g9", "ann@home.org", true)); err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if !slices.Equal(e.s.discovered, []string{"home.org"}) {
		t.Errorf("discovered = %v, want home.org", e.s.discovered)
	}
}

func TestAutoLinkRaceLoserOwesNoCleanup(t *testing.T) {
	// A concurrent login binds the same identity to the same account between the
	// lookup and the link: the loser carries on from the binding and the cleanup
	// is the winner's.
	e := autoLinkEnv(t, seed{password: true})
	e.s.linkIsStrict = true
	e.s.beforeLink = func(id authn.AccountID, l authn.ExternalLink) {
		e.s.mu.Lock()
		defer e.s.mu.Unlock()
		if _, taken := e.s.binding[bkey(l.Provider, l.Subject)]; !taken {
			e.s.binding[bkey(l.Provider, l.Subject)] = id
			e.s.accounts[id].bindings = append(e.s.accounts[id].bindings, authn.ExternalBinding{Provider: l.Provider, Subject: l.Subject})
		}
	}
	res, err := e.resolve(social("google", "g9", "ann@home.org", true))
	if err != nil || res.Account != "a1" || res.Outcome != OutcomeBound || len(res.Cleanup) != 0 {
		t.Errorf("Resolve = %+v, %v, want a1 bound with no cleanup", res, err)
	}
}

func TestAutoLinkBindingTakenByAnotherAccountIsAConflict(t *testing.T) {
	e := autoLinkEnv(t, seed{})
	e.s.seed(t, seed{id: "rival", status: authn.StatusActive})
	e.s.beforeLink = func(_ authn.AccountID, l authn.ExternalLink) {
		e.s.mu.Lock()
		defer e.s.mu.Unlock()
		e.s.binding[bkey(l.Provider, l.Subject)] = "rival"
	}
	_, err := e.resolve(social("google", "g9", "ann@home.org", true))
	if !errors.Is(err, authn.ErrBindingConflict) {
		t.Errorf("err = %v, want ErrBindingConflict", err)
	}
}

func TestSocialProofInAnEnterpriseNamespaceIsRefused(t *testing.T) {
	e := newEnv(t, withSignup(SignupOpen), withProvider(authn.ProvisioningJIT, false))
	for _, provider := range []string{"oidc:" + idpAcme, "saml:" + idpAcme} {
		_, err := e.resolve(social(provider, "u1", "ann@acme.com", true))
		if !errors.Is(err, authn.ErrInvalidArgument) {
			t.Errorf("%s: err = %v, want ErrInvalidArgument", provider, err)
		}
	}
	if e.s.writes() != 0 {
		t.Errorf("%d writes, want none: an account under the tenant's namespace skips its domain check", e.s.writes())
	}
}

func TestSocialUnbindableAddressIsNoClue(t *testing.T) {
	e := newEnv(t, withSignup(SignupOpen))
	res, err := e.resolve(social("google", "g1", "not-an-address", true))
	if err != nil || res.Outcome != OutcomeCreated {
		t.Fatalf("Resolve = %+v, %v", res, err)
	}
	if ids := e.s.identifiersOf(res.Account); len(ids) != 0 {
		t.Errorf("identifiers = %+v, want none", ids)
	}
}
