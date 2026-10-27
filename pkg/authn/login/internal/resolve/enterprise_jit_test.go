package resolve

import (
	"errors"
	"slices"
	"testing"

	"github.com/vislake/speed/pkg/authn"
)

// jitMergeSeed is an account that meets every condition of an A5 merge: the
// address is a provider's proof, the account has no password, phone, other
// binding or factor, and its only membership is the provider's tenant.
func jitMergeSeed() seed {
	return seed{
		id:       "a1",
		status:   authn.StatusActive,
		email:    "ann@acme.com",
		emailVia: "idp:google",
		tenants:  []string{tenantAcme},
	}
}

// wantJITLinkRequired asserts a refusal is the link-required kind and names the
// address whose existing account collided.
func wantJITLinkRequired(t *testing.T, err error, want string) {
	t.Helper()
	var conflicted *authn.LinkRequiredError
	if !errors.As(err, &conflicted) {
		t.Fatalf("err = %v, want a *authn.LinkRequiredError", err)
	}
	if conflicted.Email != want {
		t.Errorf("the refusal names %q, want %q", conflicted.Email, want)
	}
}

// REQ-signin-022: with merging off, an address an existing account holds is
// link-required and no account is created.
func TestJITAddressTakenWithMergingOffIsLinkRequired(t *testing.T) {
	e := newEnv(t, withProvider(authn.ProvisioningJIT, false))
	e.s.seed(t, jitMergeSeed())
	before := e.s.live()

	res, err := e.resolve(enterprise("u1", "ann@acme.com"))
	if !errors.Is(err, authn.ErrLinkRequired) {
		t.Fatalf("Resolve = %+v, %v, want ErrLinkRequired", res, err)
	}
	wantJITLinkRequired(t, err, "ann@acme.com")
	if after := e.s.live(); !slices.Equal(before, after) {
		t.Errorf("accounts = %v, want unchanged %v", after, before)
	}
	if len(e.s.created) != 0 || e.s.writes() != 0 {
		t.Errorf("%d accounts created, %d writes, want none", len(e.s.created), e.s.writes())
	}
	if _, ok := e.s.boundTo("oidc:"+idpAcme, "u1"); ok {
		t.Error("the identity was bound")
	}
}

// REQ-signin-034: merging on with every condition satisfied lands the login on
// the existing account and keeps the account count. The result owes the
// caller the A10 cleanup — ending the account's sessions and clearing its
// password — and the merge itself writes the binding alone.
func TestJITMergeWithEveryConditionSatisfied(t *testing.T) {
	e := newEnv(t, withProvider(authn.ProvisioningJIT, true))
	e.s.seed(t, jitMergeSeed())
	before := e.s.live()

	res, err := e.resolve(enterprise("u1", "ann@acme.com"))
	if err != nil || res.Account != "a1" || res.Outcome != OutcomeMerged {
		t.Fatalf("Resolve = %+v, %v, want a merge into a1", res, err)
	}
	if after := e.s.live(); !slices.Equal(before, after) {
		t.Errorf("accounts = %v, want unchanged %v", after, before)
	}
	if len(e.s.created) != 0 {
		t.Errorf("%d accounts created, want none", len(e.s.created))
	}
	if want := []Cleanup{CleanupRevokeSessions, CleanupClearPassword}; !slices.Equal(res.Cleanup, want) {
		t.Errorf("Cleanup = %v, want %v: the sessions end first and the password is cleared second", res.Cleanup, want)
	}
	if got, ok := e.s.boundTo("oidc:"+idpAcme, "u1"); !ok || got != "a1" {
		t.Errorf("binding = %v, %v, want it on a1", got, ok)
	}
	if len(e.s.membershipPuts) != 0 || res.MembershipCreated {
		t.Errorf("%d membership writes, created=%t, want none: the merge writes the binding alone",
			len(e.s.membershipPuts), res.MembershipCreated)
	}
}

// REQ-signin-034: each unmet condition refuses with link-required and leaves the
// target account exactly as it was.
func TestJITMergeRefusesWhenAConditionIsUnmet(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(*seed)
		opts   []option
	}{
		{name: "address asserted by an administrator", mutate: func(s *seed) { s.emailVia = authn.VerifiedViaAdmin }},
		{name: "address recorded by an import", mutate: func(s *seed) { s.emailVia = authn.VerifiedViaImportUnverified }},
		{name: "address recorded under no channel", mutate: func(s *seed) { s.emailVia = "" }},
		{name: "password set", mutate: func(s *seed) { s.password = true }},
		{name: "phone held", mutate: func(s *seed) { s.phone = "+8613800000001" }},
		{name: "another binding", mutate: func(s *seed) { s.bindings = [][2]string{{"google", "g1"}} }},
		{name: "a factor", mutate: func(s *seed) { s.factors = 1 }},
		{name: "member of another tenant", mutate: func(s *seed) { s.tenants = []string{tenantAcme, tenantElse} }},
		{name: "member of only another tenant", mutate: func(s *seed) { s.tenants = []string{tenantElse} }},
		{name: "suspended in another tenant", mutate: func(s *seed) { s.suspended = []string{tenantElse} }},
		{name: "account disabled", mutate: func(s *seed) { s.status = authn.StatusDisabled }},
		{name: "service account", mutate: func(s *seed) { s.kind = authn.KindService }},
		{name: "no way to ask about factors", mutate: func(*seed) {}, opts: []option{withoutFactors()}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := newEnv(t, append([]option{withProvider(authn.ProvisioningJIT, true)}, tc.opts...)...)
			sd := jitMergeSeed()
			tc.mutate(&sd)
			e.s.seed(t, sd)
			before := e.s.account("a1")
			beforeIDs := e.s.identifiersOf("a1")
			beforeBindings := e.s.bindingsOf("a1")
			liveBefore := e.s.live()

			res, err := e.resolve(enterprise("u1", "ann@acme.com"))
			if !errors.Is(err, authn.ErrLinkRequired) {
				t.Fatalf("Resolve = %+v, %v, want ErrLinkRequired", res, err)
			}
			wantJITLinkRequired(t, err, "ann@acme.com")
			if len(res.Cleanup) != 0 {
				t.Errorf("Cleanup = %v, want none for a refusal", res.Cleanup)
			}
			if len(e.s.created) != 0 || e.s.writes() != 0 {
				t.Errorf("%d accounts created, %d writes, want none", len(e.s.created), e.s.writes())
			}
			if after := e.s.live(); !slices.Equal(liveBefore, after) {
				t.Errorf("accounts = %v, want unchanged %v", after, liveBefore)
			}
			if _, ok := e.s.boundTo("oidc:"+idpAcme, "u1"); ok {
				t.Error("the identity was bound")
			}
			after := e.s.account("a1")
			if after.PasswordSet != before.PasswordSet || after.Status != before.Status {
				t.Errorf("account changed from %+v to %+v", before, after)
			}
			if !slices.Equal(beforeIDs, e.s.identifiersOf("a1")) {
				t.Errorf("identifiers = %v, want unchanged %v", e.s.identifiersOf("a1"), beforeIDs)
			}
			if !slices.Equal(beforeBindings, e.s.bindingsOf("a1")) {
				t.Errorf("bindings = %v, want unchanged %v", e.s.bindingsOf("a1"), beforeBindings)
			}
		})
	}
}
