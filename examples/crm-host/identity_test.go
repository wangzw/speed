package main

import (
	"context"
	nethttp "net/http"
	"strings"
	"testing"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/authn/authntest"
)

// requireRecord waits for the record of a request's refusal, whose message and
// fields the host wrote, and fails with the run's output when none arrives.
func requireRecord(t *testing.T, h *crmHost, what, msg string, attrs ...string) {
	t.Helper()
	if !eventually(settleLimit, func() bool {
		_, found := findRecord(h.output(), msg, attrs...)
		return found
	}) {
		t.Errorf("no record of %s with the fields %q; the run wrote:\n%s", what, attrs, h.outputForReport())
	}
}

// TestTheAdminEndpointServesTheOperatorAndNoOtherAccount is the case for the
// operator check, which reads the account the gateway stamped: the one subject
// the operators' table holds under the name the check serves is let through, and
// a CRM user and another subject of the same table are each answered 403.
//
// The table holds a second subject on purpose. A check that took any account
// under the static prefix for an operator, or any authenticated caller, lets the
// second subject through, and a check that read the password alone would too.
// Each refusal is recorded with the account and the backend that verified it,
// the fields the gateway puts on a request's records once it has stamped an
// identity.
func TestTheAdminEndpointServesTheOperatorAndNoOtherAccount(t *testing.T) {
	t.Parallel()
	const other = "mallory"
	forEachStack(t, func(t *testing.T, c *crmCase) {
		hash, err := operatorHash()
		if err != nil {
			t.Fatalf("deriving the second subject's entry: %v", err)
		}
		c.config.set("authn.static.subjects", map[string]string{operatorSubject: hash, other: hash})
		c.migrate()
		alice := c.createUser("alice")
		h := c.serve()

		expect(t, "the operator on the admin endpoint",
			h.get(t, adminEndpoint, "/tenants", "", operator()), nethttp.StatusOK)

		expect(t, "a CRM user's session on the admin endpoint",
			h.get(t, adminEndpoint, "/tenants", "", signedIn(alice)), nethttp.StatusForbidden)
		requireRecord(t, h, "the CRM user's refusal", adminRefusal,
			"account", alice.ID, "backend", sessionBackendName)

		expect(t, "another subject of the operators' table on the admin endpoint",
			h.get(t, adminEndpoint, "/tenants", "",
				basicCredential{user: other, password: operatorPassword}), nethttp.StatusForbidden)
		requireRecord(t, h, "the other subject's refusal", adminRefusal,
			"account", authn.StaticPrefix+other, "backend", "static")
	})
}

// TestAnOperatorBelongsToNoTenant is the case for the operator's account in the
// tenant surface: the account of the static table is no member of any tenant, so
// the admission layer answers it as it answers any stranger — the same 404 it
// answers a member the tenant does not hold — while a member of the same tenant
// is let in. The account endpoint answers who the caller is with the account and
// no user and no tenant.
//
// The member's request is the control: the refusal is about whose account asks,
// not about the tenant or the route.
func TestAnOperatorBelongsToNoTenant(t *testing.T) {
	t.Parallel()
	forEachStack(t, func(t *testing.T, c *crmCase) {
		c.migrate()
		acme := c.createTenant("acme")
		alice := c.createUser("alice")
		c.grant(acme, alice, roleOwner)
		h := c.serve()

		expect(t, "a member of acme", h.get(t, apiEndpoint, "/members", acme.Alias, signedIn(alice)), nethttp.StatusOK)
		expect(t, "the operator naming acme", h.get(t, apiEndpoint, "/members", acme.Alias, operator()), nethttp.StatusNotFound)

		me := h.me(t, operator())
		if me.Identity != string(operatorAccount) {
			t.Errorf("GET /me answered the identity %q for the operator, want %q", me.Identity, operatorAccount)
		}
		if me.Account != nil || len(me.Tenants) != 0 {
			t.Errorf("GET /me answered account %+v and tenants %+v for the operator, want neither", me.Account, me.Tenants)
		}
	})
}

// TestTheDecisionSeamRefusesTenantAccessAndServesTheOperator is the case for
// the host's Decider: a management action is allowed to the one operator
// account alone, judged by its full identifier, and authn.tenant.access is
// refused whatever the actor, so no platform role crosses a tenant until a
// permission model is written.
//
// The actor identities are built with authntest, which is the package a host's
// tests reach for and its code does not; the tenant-access refusal is also held
// through the running host by TestAnOperatorBelongsToNoTenant, where the
// admission layer asks this decision and answers its 404.
func TestTheDecisionSeamRefusesTenantAccessAndServesTheOperator(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	identity := func(account authn.AccountID, backend string) authn.Identity {
		t.Helper()
		id, ok := authn.FromContext(authntest.Context(t, ctx,
			authntest.WithAccount(account), authntest.WithBackend(backend)))
		if !ok {
			t.Fatalf("the test context carries no identity")
		}
		return id
	}
	operatorID := identity(operatorAccount, "static")
	userID := identity(authn.AccountID("u_0123456789abcdef"), sessionBackendName)

	var d opsDecider
	for _, tc := range []struct {
		what   string
		action authn.Action
		actor  authn.Identity
		want   bool
	}{
		{"tenant access by the operator", authn.ActionTenantAccess, operatorID, false},
		{"tenant access by a user", authn.ActionTenantAccess, userID, false},
		{"a management action by the operator", authn.ActionAccountRead, operatorID, true},
		{"a management action by a user", authn.ActionAccountRead, userID, false},
	} {
		got, err := d.Decide(ctx, authn.Decision{Action: tc.action, Actor: tc.actor})
		if err != nil || got != tc.want {
			t.Errorf("%s: Decide = %v, %v, want %v", tc.what, got, err, tc.want)
		}
	}
}

// TestAUsersPasswordIsNoCredentialOnAGuardedRoute is the case for the one scheme
// with one backend: the operators' table takes Basic, so a CRM user's Basic
// credential, with the right password, is refused on every guarded endpoint with
// a 401 that carries the Basic challenge, and the refusal is the operators'
// table's. The same user's token reaches the same route, which tells the
// refusal of the scheme from a refusal of the route.
func TestAUsersPasswordIsNoCredentialOnAGuardedRoute(t *testing.T) {
	t.Parallel()
	forEachStack(t, func(t *testing.T, c *crmCase) {
		c.migrate()
		acme := c.createTenant("acme")
		alice := c.createUser("alice")
		c.grant(acme, alice, roleOwner)
		h := c.serve()

		for _, route := range []struct{ endpoint, path, tenant string }{
			{apiEndpoint, "/members", acme.Alias},
			{accountEndpoint, "/me", ""},
			{adminEndpoint, "/tenants", ""},
		} {
			resp := h.get(t, route.endpoint, route.path, route.tenant, basic(alice))
			expect(t, "alice's Basic credential on "+route.endpoint+route.path, resp, nethttp.StatusUnauthorized)
			if challenge := resp.header.Get("WWW-Authenticate"); !strings.Contains(challenge, "Basic") {
				t.Errorf("the refusal on %s%s carries the challenge %q, want one naming Basic", route.endpoint, route.path, challenge)
			}
		}
		requireRecord(t, h, "the operators' table refusing the user's credential", "authn.gate: the backend refused this credential",
			"outcome", "denied", "attributed_to", "static")

		expect(t, "alice's token on the account endpoint", h.get(t, accountEndpoint, "/me", "", signedIn(alice)), nethttp.StatusOK)
	})
}

// TestTheStartupNamesTheWeakOperatorEntry is the case for the startup
// diagnostic the password-table backend writes about an entry below what OWASP
// recommends: the operator's entry is written at a cost a case can afford, so
// the backend names it at Warn while it wires, and the case reads that line
// rather than the run leaving it unsaid.
func TestTheStartupNamesTheWeakOperatorEntry(t *testing.T) {
	t.Parallel()
	c := newCase(t, stackNamed(t, localStack))
	c.migrate()
	h := c.serve()
	requireRecord(t, h, "the weak operator entry",
		staticModuleName+": a subject's password entry is below the least OWASP recommends, "+
			"so a copy of the table is cheap to guess against. The entry is read as it is",
		"level", "WARN", "module", staticModuleName, "entry", operatorSubject, "algorithm", "pbkdf2-sha256")
	h.stop(t)
}
