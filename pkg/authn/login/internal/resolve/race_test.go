package resolve

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"

	"github.com/vislake/speed/pkg/authn"
)

// A9: the same identity logging in for the first time twice at once ends in one
// account and one binding. The request that lost the binding reads the winner's;
// one that lost an address the winner's creation took is refused that address,
// which the engine answers as link-required (A3).

type answer struct {
	res Result
	err error
}

func resolveTogether(e *env, n int, p authn.Proof) []answer {
	out := make([]answer, n)
	var wg sync.WaitGroup
	for i := range out {
		wg.Add(1)
		go func() {
			defer wg.Done()
			// Each flow picks its own username, as two flows of one person may:
			// a username is unique across accounts, so two flows that typed the
			// same one would race on the name rather than on the identity this
			// case is about.
			res, err := e.r.Resolve(context.Background(), Request{Proof: p, Purpose: authn.PurposeLogin})
			if err == nil && res.Pending != nil {
				res, err = e.r.Create(context.Background(), *res.Pending, fmt.Sprintf("racer-%d", i), passwordOf(*res.Pending), "")
			}
			out[i] = answer{res, err}
		}()
	}
	wg.Wait()
	return out
}

func checkOneAccount(t *testing.T, e *env, answers []answer, wantBound int) {
	t.Helper()
	var created, bound int
	var id authn.AccountID
	for i, a := range answers {
		if a.err != nil {
			t.Fatalf("login %d: %v", i, a.err)
		}
		if id == "" {
			id = a.res.Account
		}
		if a.res.Account != id {
			t.Errorf("login %d landed on %s, another landed on %s", i, a.res.Account, id)
		}
		switch a.res.Outcome {
		case OutcomeCreated:
			created++
		case OutcomeBound:
			bound++
		default:
			t.Errorf("login %d: outcome %v", i, a.res.Outcome)
		}
	}
	if created != 1 || bound != wantBound {
		t.Errorf("%d created and %d bound, want 1 and %d", created, bound, wantBound)
	}
	if live := e.s.live(); len(live) != 1 || live[0] != id {
		t.Errorf("accounts = %v, want only %s", live, id)
	}
}

func TestConcurrentFirstLoginMakesOneAccount(t *testing.T) {
	// Both logins have created their account before either binds, so the
	// uniqueness of the binding is what decides.
	e := newEnv(t, withSignup(SignupOpen))
	gate := newRendezvous(2)
	e.s.afterCreate = gate.wait
	answers := resolveTogether(e, 2, social("github", "gh1", "", false))
	checkOneAccount(t, e, answers, 1)
	if len(e.s.deleted) != 1 {
		t.Errorf("deleted %v, want the loser's account unwound", e.s.deleted)
	}
	if bs := e.s.bindingsOf(answers[0].res.Account); len(bs) != 1 {
		t.Errorf("bindings = %v, want one", bs)
	}
}

func TestConcurrentFirstLoginWithAnAddressMakesOneAccount(t *testing.T) {
	// Both logins look the address up before either writes, so the address is
	// what decides: it is written in the same transaction as the account, before
	// any binding exists. Exactly one account is opened; the request that loses
	// the address is refused it, which the engine answers as link-required.
	e := newEnv(t, withSignup(SignupOpen))
	gate := newRendezvous(2)
	e.s.beforeCreate = func(authn.CreateAccount) { gate.wait() }
	answers := resolveTogether(e, 2, social("google", "g1", "ann@home.org", true))
	var created, refused int
	for i, a := range answers {
		switch {
		case a.err == nil && a.res.Outcome == OutcomeCreated:
			created++
		case errors.Is(a.err, authn.ErrContactTaken):
			refused++
		default:
			t.Errorf("login %d: %+v, %v; want one opened account and one refused address", i, a.res, a.err)
		}
	}
	if created != 1 || refused != 1 {
		t.Errorf("%d created and %d refused the address, want one of each", created, refused)
	}
	live := e.s.live()
	if len(live) != 1 {
		t.Fatalf("accounts = %v, want one", live)
	}
	if owner, ok := e.s.boundTo("google", "g1"); !ok || owner != live[0] {
		t.Errorf("the identity is bound to %q, want the surviving account %s", owner, live[0])
	}
	held := false
	for _, idf := range e.s.identifiersOf(live[0]) {
		if idf.Kind == authn.IdentifierEmail && idf.Value == "ann@home.org" {
			held = true
		}
	}
	if !held {
		t.Errorf("the surviving account does not hold the address: %+v", e.s.identifiersOf(live[0]))
	}
}

func TestConcurrentFirstLoginOfAPhoneMakesOneAccount(t *testing.T) {
	e := newEnv(t, withSignup(SignupOpen))
	gate := newRendezvous(2)
	e.s.beforeCreate = func(authn.CreateAccount) { gate.wait() }
	answers := resolveTogether(e, 2, phone("+8613800000001"))
	checkOneAccount(t, e, answers, 1)
}

func TestConcurrentEnterpriseFirstLoginMakesOneAccountAndOneMembership(t *testing.T) {
	e := newEnv(t, withProvider(authn.ProvisioningJIT, false))
	gate := newRendezvous(2)
	e.s.beforeCreate = func(authn.CreateAccount) { gate.wait() }
	answers := resolveTogether(e, 2, enterprise("u1", "ann@acme.com"))
	// Both looked the address up before either made an account. The address is
	// held by the winner's account once it is made, so the second Create fails on
	// it; the loser ends on the winner's account or at link-required.
	var created int
	for _, a := range answers {
		if a.err == nil && a.res.Outcome == OutcomeCreated {
			created++
		}
	}
	if created != 1 {
		t.Fatalf("answers = %+v, want exactly one created", answers)
	}
	live := e.s.live()
	if len(live) != 1 {
		t.Fatalf("accounts = %v, want one", live)
	}
	if len(e.s.membershipPuts) != 1 {
		t.Errorf("%d membership writes, want one: only the winner's account gets a membership", len(e.s.membershipPuts))
	}
	if _, ok := e.s.membershipOf(tenantAcme, live[0]); !ok {
		t.Error("the surviving account has no membership")
	}
}
