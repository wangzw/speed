package resolve

import (
	"errors"
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
	if got := e.s.live(); len(got) != 1 || got[0] != "a1" {
		t.Errorf("accounts = %v, want the fixture alone", got)
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

func TestSocialOpenSignupCreatesAnEnabledUserWithNoMembershipOrAddress(t *testing.T) {
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
	if ids := e.s.identifiersOf(res.Account); len(ids) != 0 {
		t.Errorf("identifiers = %+v, want none: the provider's address is not written", ids)
	}
	if e.s.created[0].Email != "" || e.s.created[0].EmailVerifiedVia.Valid() {
		t.Errorf("Create carried the address %+v, want none", e.s.created[0])
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

func TestSocialOpenSignupWritesNoAddressTheProviderVerified(t *testing.T) {
	// A verified address no account holds is no more written than an unverified
	// one: the account takes the provider's display name alone.
	e := newEnv(t, withSignup(SignupOpen))
	res, err := e.resolve(social("google", "g1", "new@home.org", true))
	if err != nil || res.Outcome != OutcomeCreated {
		t.Fatalf("Resolve = %+v, %v", res, err)
	}
	if ids := e.s.identifiersOf(res.Account); len(ids) != 0 {
		t.Errorf("identifiers = %+v, want none", ids)
	}
	if got := e.s.created[0]; got.Email != "" || got.EmailVerifiedVia.Valid() {
		t.Errorf("Create carried the address %+v, want none", got)
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
