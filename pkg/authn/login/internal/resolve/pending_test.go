package resolve

import (
	"context"
	"errors"
	"testing"

	"github.com/vislake/speed/pkg/authn"
)

// The pending-signup step: a first login that would open an account decides the
// facts, writes nothing and keeps them in a Pending, and Create opens the
// account once its person has chosen a username.

func TestAPendingSignupCarriesTheFactsTheAccountIsOpenedWith(t *testing.T) {
	t.Run("social", func(t *testing.T) {
		e := newEnv(t, withSignup(SignupOpen))
		pending, err := e.pending(social("google", "g1", "ann@home.org", true))
		if err != nil || pending.Pending == nil {
			t.Fatalf("Resolve = %+v, %v, want a pending signup", pending, err)
		}
		p := pending.Pending
		if p.Source != SourceSocial || p.Via != "signup" {
			t.Errorf("source = %q via = %q, want a signup social path", p.Source, p.Via)
		}
		if p.External == nil || p.External.Subject != "g1" || p.DisplayName != "Ann" {
			t.Errorf("external = %+v display name = %q, want the identity the binding is made under", p.External, p.DisplayName)
		}
		if p.Email != "ann@home.org" || p.EmailVerifiedVia != "idp:google" {
			t.Errorf("email = %q via = %q, want the verified address the account opens with", p.Email, p.EmailVerifiedVia)
		}
		if p.Phone != "" || p.Provider != "" || p.Tenant != "" {
			t.Errorf("a social pending names a phone, a provider or a tenant: %+v", p)
		}
	})

	t.Run("phone", func(t *testing.T) {
		e := newEnv(t, withSignup(SignupOpen))
		pending, err := e.pending(phone("+8613800000001"))
		if err != nil || pending.Pending == nil {
			t.Fatalf("Resolve = %+v, %v, want a pending signup", pending, err)
		}
		p := pending.Pending
		if p.Source != SourcePhone || p.Via != "signup" || p.Phone != "+8613800000001" {
			t.Errorf("pending = %+v, want the proved phone under signup", p)
		}
		if p.External != nil {
			t.Errorf("a phone pending names an external identity: %+v", p)
		}
	})

	t.Run("enterprise", func(t *testing.T) {
		e := newEnv(t, withProvider(authn.ProvisioningJIT, false))
		pending, err := e.pending(enterprise("u1", "ann@acme.com"))
		if err != nil || pending.Pending == nil {
			t.Fatalf("Resolve = %+v, %v, want a pending signup", pending, err)
		}
		p := pending.Pending
		if p.Source != SourceEnterprise || p.Via != "jit" {
			t.Errorf("source = %q via = %q, want the enterprise just-in-time path", p.Source, p.Via)
		}
		if p.Provider != idpAcme || p.Tenant != tenantAcme {
			t.Errorf("provider = %q tenant = %q, want the provider's own", p.Provider, p.Tenant)
		}
	})
}

func TestTheCollectedUsernameOpensThePendingAccount(t *testing.T) {
	t.Run("social", func(t *testing.T) {
		e := newEnv(t, withSignup(SignupOpen))
		pending, err := e.pending(social("google", "g1", "ann@home.org", true))
		if err != nil || pending.Pending == nil {
			t.Fatalf("Resolve = %+v, %v, want a pending signup", pending, err)
		}
		res, err := e.r.Create(context.Background(), *pending.Pending, "collected", passwordOf(*pending.Pending), "")
		if err != nil || res.Outcome != OutcomeCreated || res.Account == "" {
			t.Fatalf("Create = %+v, %v, want a new account", res, err)
		}
		if got := e.s.created[0].Username; got != "collected" {
			t.Errorf("the account was opened with %q, want the collected username", got)
		}
		if got, ok := e.s.boundTo("google", "g1"); !ok || got != res.Account {
			t.Errorf("the identity is bound to %q, %v, want the new account", got, ok)
		}
	})

	t.Run("phone", func(t *testing.T) {
		e := newEnv(t, withSignup(SignupOpen))
		pending, err := e.pending(phone("+8613800000001"))
		if err != nil || pending.Pending == nil {
			t.Fatalf("Resolve = %+v, %v, want a pending signup", pending, err)
		}
		res, err := e.r.Create(context.Background(), *pending.Pending, "collected", passwordOf(*pending.Pending), "")
		if err != nil || res.Outcome != OutcomeCreated {
			t.Fatalf("Create = %+v, %v, want a new account", res, err)
		}
		if got := e.s.created[0].Phone; got != "+8613800000001" {
			t.Errorf("the account was opened with the phone %q, want the proved one", got)
		}
	})

	t.Run("enterprise", func(t *testing.T) {
		e := newEnv(t, withProvider(authn.ProvisioningJIT, false))
		pending, err := e.pending(enterprise("u1", "ann@acme.com"))
		if err != nil || pending.Pending == nil {
			t.Fatalf("Resolve = %+v, %v, want a pending signup", pending, err)
		}
		res, err := e.r.Create(context.Background(), *pending.Pending, "collected", passwordOf(*pending.Pending), "")
		if err != nil || res.Outcome != OutcomeCreated || !res.MembershipCreated {
			t.Fatalf("Create = %+v, %v, want a new account in the tenant", res, err)
		}
		if len(e.s.membershipPuts) != 1 || e.s.membershipPuts[0].Tenant != tenantAcme {
			t.Errorf("memberships written = %+v, want the provider's tenant alone", e.s.membershipPuts)
		}
	})
}

func TestAUsernameAnotherAccountHoldsOpensNothing(t *testing.T) {
	e := newEnv(t, withSignup(SignupOpen))
	e.s.seed(t, seed{id: "a1", status: authn.StatusActive, username: "taken"})
	pending, err := e.pending(social("github", "gh1", "", false))
	if err != nil || pending.Pending == nil {
		t.Fatalf("Resolve = %+v, %v, want a pending signup", pending, err)
	}
	_, err = e.r.Create(context.Background(), *pending.Pending, "taken", passwordOf(*pending.Pending), "")
	if !errors.Is(err, authn.ErrAccountExists) {
		t.Fatalf("Create = %v, want ErrAccountExists", err)
	}
	if e.s.writes() != 0 {
		t.Errorf("%d writes, want none: a taken username opens no account", e.s.writes())
	}
	if live := e.s.live(); len(live) != 1 || live[0] != "a1" {
		t.Errorf("accounts = %v, want the fixture alone", live)
	}
}

func TestARefusedUsernameOpensNothing(t *testing.T) {
	e := newEnv(t, withSignup(SignupOpen))
	pending, err := e.pending(social("github", "gh1", "", false))
	if err != nil || pending.Pending == nil {
		t.Fatalf("Resolve = %+v, %v, want a pending signup", pending, err)
	}
	_, err = e.r.Create(context.Background(), *pending.Pending, "", passwordOf(*pending.Pending), "")
	if !errors.Is(err, authn.ErrInvalidIdentifier) {
		t.Fatalf("Create with no username = %v, want ErrInvalidIdentifier", err)
	}
	if e.s.writes() != 0 {
		t.Errorf("%d writes, want none: no username opens no account", e.s.writes())
	}
}
