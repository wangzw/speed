package resolve

import (
	"errors"
	"slices"
	"testing"

	"github.com/vislake/speed/pkg/authn"
)

// A2: an existing binding is the account's login, and nothing but the binding's
// display fields change.
func TestExistingBindingLogsInAndTouchesOnlyDisplayFields(t *testing.T) {
	e := newEnv(t)
	id := e.s.seed(t, seed{id: "a1", status: authn.StatusActive, email: "ann@home.org", emailVia: "idp:google", bindings: [][2]string{{"google", "g1"}}})
	res, err := e.resolve(social("google", "g1", "ann@new.example", true))
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if res.Account != id || res.Outcome != OutcomeBound || len(res.Cleanup) != 0 || res.MembershipCreated {
		t.Errorf("result = %+v, want the bound account with no cleanup", res)
	}
	if got := e.s.bindingsOf(id); len(got) != 1 || got[0].Email != "ann@new.example" {
		t.Errorf("binding = %+v, want its display email refreshed", got)
	}
	// The address the provider declares now is not the account's address: the
	// account's identifiers are exactly what they were.
	ids := e.s.identifiersOf(id)
	if len(ids) != 1 || ids[0].Value != "ann@home.org" {
		t.Errorf("identifiers = %+v, want the original email alone", ids)
	}
	if e.s.membershipRead != 0 || len(e.s.membershipPuts) != 0 {
		t.Errorf("memberships touched: %d reads, %d writes, want none", e.s.membershipRead, len(e.s.membershipPuts))
	}
	if len(e.s.created) != 0 || len(e.s.discovered) != 0 {
		t.Errorf("created %d accounts and asked the SSO register %d times, want neither", len(e.s.created), len(e.s.discovered))
	}
}

func TestExistingBindingWithTheSameDisplayFieldsWritesNothing(t *testing.T) {
	e := newEnv(t)
	e.s.seed(t, seed{id: "a1", status: authn.StatusActive, bindings: [][2]string{{"google", "g1"}}})
	if _, err := e.resolve(social("google", "g1", "", false)); err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if n := e.s.writes(); n != 0 {
		t.Errorf("a provider that declares no address caused %d writes, want 0", n)
	}
}

func TestExistingBindingKeepsTheStoredAddressWhenTheProviderDeclaresNone(t *testing.T) {
	e := newEnv(t)
	id := e.s.seed(t, seed{id: "a1", status: authn.StatusActive, bindings: [][2]string{{"github", "gh1"}}})
	e.s.mu.Lock()
	e.s.accounts[id].bindings[0].Email = "old@home.org"
	e.s.mu.Unlock()
	if _, err := e.resolve(social("github", "gh1", "", false)); err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if got := e.s.bindingsOf(id)[0].Email; got != "old@home.org" {
		t.Errorf("binding email = %q, want the stored one kept", got)
	}
	if len(e.s.linked) != 0 {
		t.Errorf("linked %v, want no write", e.s.linked)
	}
}

func TestExistingBindingKeepsAnEnterpriseLoginOffTheMembership(t *testing.T) {
	// A suspended member who logs in again through the tenant's provider is not
	// restored, and no membership is read or written on the way.
	e := newEnv(t, withProvider(authn.ProvisioningJIT, true))
	id := e.s.seed(t, seed{id: "a1", status: authn.StatusActive, bindings: [][2]string{{"oidc:" + idpAcme, "u1"}}, suspended: []string{tenantAcme}})
	res, err := e.resolve(enterprise("u1", "ann@acme.com"))
	if err != nil || res.Account != id {
		t.Fatalf("Resolve = %+v, %v", res, err)
	}
	if m, ok := e.s.membershipOf(tenantAcme, id); !ok || m.Active {
		t.Errorf("membership = %+v, %v, want it still suspended", m, ok)
	}
	if e.s.membershipRead != 0 || len(e.s.membershipPuts) != 0 || len(e.s.discovered) != 0 {
		t.Errorf("reads %d, writes %d, discovers %d, want none", e.s.membershipRead, len(e.s.membershipPuts), len(e.s.discovered))
	}
}

func TestDisabledAccountByBindingIsUnavailableForAnExternalLogin(t *testing.T) {
	e := newEnv(t)
	e.s.seed(t, seed{id: "a1", status: authn.StatusDisabled, bindings: [][2]string{{"google", "g1"}}})
	_, err := e.resolve(social("google", "g1", "", false))
	if !errors.Is(err, authn.ErrAccountUnavailable) || errors.Is(err, authn.ErrInvalidCredentials) {
		t.Errorf("err = %v, want ErrAccountUnavailable and not the shape of a wrong password", err)
	}
}

func TestDisabledAccountReadsAsAWrongPasswordForAPasswordOrPhoneLogin(t *testing.T) {
	e := newEnv(t)
	e.s.seed(t, seed{id: "a1", status: authn.StatusDisabled, phone: "+8613800000001"})
	for name, p := range map[string]authn.Proof{
		"account":    {Account: "a1"},
		"identifier": phone("+8613800000001"),
	} {
		_, err := e.resolve(p)
		if !errors.Is(err, authn.ErrInvalidCredentials) || errors.Is(err, authn.ErrAccountUnavailable) {
			t.Errorf("%s: err = %v, want the shape of a wrong password and not ErrAccountUnavailable", name, err)
		}
	}
	if _, err := e.resolve(authn.Proof{Account: "gone"}); !errors.Is(err, authn.ErrInvalidCredentials) {
		t.Errorf("an account that does not exist: err = %v, want the shape of a wrong password", err)
	}
}

func TestEnabledAccountProofsResolveToTheAccount(t *testing.T) {
	e := newEnv(t)
	e.s.seed(t, seed{id: "a1", status: authn.StatusActive, phone: "+8613800000001"})
	for name, p := range map[string]authn.Proof{
		"account":    {Account: "a1"},
		"identifier": phone("+8613800000001"),
	} {
		res, err := e.resolve(p)
		if err != nil || res.Account != "a1" || res.Outcome != OutcomeBound {
			t.Errorf("%s: %+v, %v, want account a1 bound", name, res, err)
		}
	}
}

func TestAServiceAccountDoesNotLogInInteractively(t *testing.T) {
	e := newEnv(t)
	e.s.seed(t, seed{id: "svc", status: authn.StatusActive, kind: authn.KindService, bindings: [][2]string{{"google", "g1"}}})
	if _, err := e.resolve(social("google", "g1", "", false)); !errors.Is(err, authn.ErrAccountUnavailable) {
		t.Errorf("err = %v, want ErrAccountUnavailable", err)
	}
}

func TestADisplayRefreshFailureDoesNotFailTheLogin(t *testing.T) {
	e := newEnv(t)
	id := e.s.seed(t, seed{id: "a1", status: authn.StatusActive, bindings: [][2]string{{"google", "g1"}}})
	boom := errors.New("write failed")
	e.s.failLink = boom
	res, err := e.resolve(social("google", "g1", "ann@new.example", true))
	if err != nil || res.Account != id {
		t.Fatalf("Resolve = %+v, %v, want the login to hold", res, err)
	}
	if !errors.Is(res.RefreshErr, boom) {
		t.Errorf("RefreshErr = %v, want the write's failure for the caller's log", res.RefreshErr)
	}
}

func TestADanglingBindingIsAnAccountThatIsNotThere(t *testing.T) {
	e := newEnv(t)
	e.s.mu.Lock()
	e.s.binding[bkey("google", "g1")] = "ghost"
	e.s.mu.Unlock()
	res, err := e.resolve(social("google", "g1", "", false))
	if !errors.Is(err, authn.ErrAccountNotFound) || res.Account != "" {
		t.Errorf("Resolve = %+v, %v, want ErrAccountNotFound and no account", res, err)
	}
}

func TestOutcomesOtherThanAMergeCarryNoCleanup(t *testing.T) {
	e := newEnv(t, withSignup(SignupOpen))
	res, err := e.resolve(social("github", "gh1", "", false))
	if err != nil || res.Outcome != OutcomeCreated {
		t.Fatalf("Resolve = %+v, %v", res, err)
	}
	if res.Cleanup != nil {
		t.Errorf("Cleanup = %v, want none for a new account", res.Cleanup)
	}
	again, err := e.resolve(social("github", "gh1", "", false))
	if err != nil || again.Outcome != OutcomeBound || again.Account != res.Account || len(again.Cleanup) != 0 {
		t.Errorf("second login = %+v, %v, want the same account bound with no cleanup", again, err)
	}
}

func TestCleanupIsCopiedOut(t *testing.T) {
	r := Result{Cleanup: mergeCleanup()}
	c := r.Clone()
	c.Cleanup[0] = 0
	if !slices.Equal(r.Cleanup, []Cleanup{CleanupRevokeSessions, CleanupClearPassword}) {
		t.Errorf("Cleanup = %v after the clone changed, want the original order intact", r.Cleanup)
	}
}
