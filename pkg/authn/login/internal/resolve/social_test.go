package resolve

import (
	"errors"
	"strings"
	"testing"

	"github.com/vislake/speed/pkg/authn"
)

// TestSocialLinkRequiredOnlyForAVerifiedAddressOnAnExistingAccount checks that such an address answers link-required with its mask and writes nothing.
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

// TestSocialUnverifiedAddressIsNotAClue checks that an unverified address never yields link-required.
func TestSocialUnverifiedAddressIsNotAClue(t *testing.T) {
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

// TestSocialUnverifiedAddressOpensTheAccountWithoutTheAddress checks that a new account is opened without the unverified address.
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

// TestSocialClosedSignupCreatesNothing checks that a closed signup refuses a new
// social login as signup-closed and writes nothing.
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

// TestSocialOpenSignupCreatesAnEnabledUserWithTheDeclaredAddressAsAContact checks the enabled user, the 100-character name and the verified contact.
func TestSocialOpenSignupCreatesAnEnabledUserWithTheDeclaredAddressAsAContact(t *testing.T) {
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
	if ids := e.s.identifiersOf(res.Account); len(ids) != 1 || ids[0].Kind != authn.IdentifierEmail || ids[0].Value != "new@home.org" {
		t.Errorf("identifiers = %+v, want the provider's verified address alone", ids)
	}
	c := e.s.created[0]
	if c.Email != "new@home.org" || c.EmailVerifiedVia.Via != "idp:google" {
		t.Errorf("Create carried the address %q under %q, want the verified one", c.Email, c.EmailVerifiedVia.Via)
	}
	if c.Password != testPassword {
		t.Errorf("Create carried password %q, want the one the social step collected", c.Password)
	}
	if got, ok := e.s.boundTo("google", "g1"); !ok || got != res.Account {
		t.Errorf("binding = %v, %v, want it on the new account", got, ok)
	}
	if e.s.membershipRead != 0 || len(e.s.membershipPuts) != 0 {
		t.Errorf("memberships touched: %d reads, %d writes, want none", e.s.membershipRead, len(e.s.membershipPuts))
	}
	if c.Via != "signup" {
		t.Errorf("Via = %q, want signup", c.Via)
	}
}

// TestSocialOpenSignupWritesNoAddressTheProviderDidNotVerify checks that an open
// social signup writes no address the provider did not verify.
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

// TestSocialOpenSignupWritesTheVerifiedAddressNoAccountHolds checks that an open
// social signup writes the provider's verified address when no account holds it.
func TestSocialOpenSignupWritesTheVerifiedAddressNoAccountHolds(t *testing.T) {
	e := newEnv(t, withSignup(SignupOpen))
	res, err := e.resolve(social("google", "g1", "new@home.org", true))
	if err != nil || res.Outcome != OutcomeCreated {
		t.Fatalf("Resolve = %+v, %v", res, err)
	}
	if ids := e.s.identifiersOf(res.Account); len(ids) != 1 || ids[0].Value != "new@home.org" {
		t.Errorf("identifiers = %+v, want the declared address alone", ids)
	}
	if got := e.s.created[0]; got.Email != "new@home.org" || got.EmailVerifiedVia.Via != "idp:google" {
		t.Errorf("Create carried the address %+v, want the verified one", got)
	}
}

// TestSocialProofInAnEnterpriseNamespaceIsRefused checks that such a proof is an invalid argument and writes nothing.
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

// TestSocialUnbindableAddressIsNoClue checks that a declared address that cannot
// be an identifier opens the account without it.
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
