package resolve

import (
	"errors"
	"testing"

	"github.com/vislake/speed/pkg/authn"
)

// TestADependencyReadThatFailsIsNotANotFound holds the fail-closed direction of
// the two reads that decide whether an account or a binding exists: a dependency
// that cannot answer is passed on, not read as none.
func TestADependencyReadThatFailsIsNotANotFound(t *testing.T) {
	t.Run("an account read", func(t *testing.T) {
		e := newEnv(t)
		boom := errors.New("accounts down")
		e.s.failGet = boom
		_, err := e.resolve(authn.Proof{Account: "acct-1"})
		if !errors.Is(err, boom) {
			t.Errorf("err = %v, want the dependency's failure", err)
		}
		if e.s.writes() != 0 {
			t.Errorf("%d writes, want none", e.s.writes())
		}
	})
	t.Run("a binding read", func(t *testing.T) {
		e := newEnv(t, withSignup(SignupOpen))
		boom := errors.New("bindings down")
		e.s.failByExt = boom
		_, err := e.resolve(social("google", "g1", "", false))
		if !errors.Is(err, boom) {
			t.Errorf("err = %v, want the dependency's failure", err)
		}
		if e.s.writes() != 0 {
			t.Errorf("%d writes, want none: a failed binding read opened a login", e.s.writes())
		}
	})
}

// TestProviderAddressMatchingIsCaseInsensitive holds that an address a provider
// declares in a different case than the store holds still matches, for the
// preprovisioned match.
func TestProviderAddressMatchingIsCaseInsensitive(t *testing.T) {
	t.Run("preprovisioned", func(t *testing.T) {
		e := newEnv(t, withProvider(authn.ProvisioningPreprovisioned, false))
		id := e.s.seed(t, seed{id: "a1", status: authn.StatusActive, email: "ann@acme.com", emailVia: authn.VerifiedViaAdmin, password: true, tenants: []string{tenantAcme}})
		res, err := e.resolve(enterprise("u1", "ANN@acme.com"))
		if err != nil || res.Account != id || res.Outcome != OutcomeMatched {
			t.Fatalf("Resolve = %+v, %v, want a1 matched", res, err)
		}
	})
}

// TestABindingWithTheSameDisplayFieldsIsNotRewritten holds the dedup of the
// display refresh: a provider that declares the address the binding already
// carries writes nothing.
func TestABindingWithTheSameDisplayFieldsIsNotRewritten(t *testing.T) {
	e := newEnv(t)
	id := e.s.seed(t, seed{id: "a1", status: authn.StatusActive, bindings: [][2]string{{"google", "g1"}}})
	e.s.accounts[id].bindings[0].Email = "ann@x.org"
	e.s.accounts[id].bindings[0].EmailVerified = true
	before := e.s.writes()

	res, err := e.resolve(social("google", "g1", "ann@x.org", true))
	if err != nil || res.Account != id || res.Outcome != OutcomeBound {
		t.Fatalf("Resolve = %+v, %v, want a1 bound", res, err)
	}
	if e.s.writes() != before {
		t.Errorf("the binding was rewritten although it already carried the same display fields")
	}
	if len(e.s.linked) != 0 {
		t.Errorf("LinkExternal was called %d times, want none", len(e.s.linked))
	}
}

// TestACreateFailureIsNeverTurnedIntoALogin holds that a create that fails for a
// reason other than a taken key is passed on as it came, not answered by reading
// the account some other login might hold.
func TestACreateFailureIsNeverTurnedIntoALogin(t *testing.T) {
	t.Run("a phone signup", func(t *testing.T) {
		e := newEnv(t, withSignup(SignupOpen))
		boom := errors.New("create down")
		e.s.failCreate = boom
		e.s.beforeCreate = func(in authn.CreateAccount) {
			if in.Phone != "" {
				e.s.seed(t, seed{id: "winner", status: authn.StatusActive, phone: in.Phone})
			}
		}
		_, err := e.resolve(phone("+8613800000002"))
		if !errors.Is(err, boom) {
			t.Errorf("err = %v, want the create failure, not a login on another account", err)
		}
	})
	t.Run("a social signup", func(t *testing.T) {
		e := newEnv(t, withSignup(SignupOpen))
		boom := errors.New("create down")
		e.s.failCreate = boom
		e.s.beforeCreate = func(authn.CreateAccount) {
			e.s.seed(t, seed{id: "winner", status: authn.StatusActive, bindings: [][2]string{{"google", "g1"}}})
		}
		_, err := e.resolve(social("google", "g1", "", false))
		if !errors.Is(err, boom) {
			t.Errorf("err = %v, want the create failure, not a login on a bound account", err)
		}
	})
}
