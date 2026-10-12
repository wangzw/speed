package main

import (
	"testing"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/authz"
	"github.com/vislake/speed/pkg/tenant"
)

// These cases hold the members module's role writes and reads to the policy
// store: a grant writes a binding the policy manager answers for and no row of
// the host's own role table; a cross-tenant grant writes into the tenant it
// names alone; the caller's and each member's role are read from the policy; and
// the command line's grant records its purpose. The matrix cases hold the
// provisioners; these hold where a member's role now lives.

// TestGrantWritesABindingTheManagerAnswersFor is the case for where a member's
// role lives after a grant: a binding of the tenant, the account and the role,
// written through the policy manager, and the tenant's four roles provisioned so
// the binding names a role that exists. A grant that wrote a table of its own
// would leave the manager answering nothing for the account, which is what the
// read-back fails on.
func TestGrantWritesABindingTheManagerAnswersFor(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	mgr := newAuthzFakeManager()
	id, account := tenant.ID("t_acme"), authn.AccountID("u_alice")

	got, err := grantIn(ctx, mgr, id, account, roleRep)
	if err != nil {
		t.Fatalf("granting the role: %v", err)
	}
	if got.Tenant != string(id) || got.Account != string(account) || got.Role != authz.Role(roleRep) {
		t.Errorf("the grant answered %+v, want a binding of %s to %s in %s", got, account, roleRep, id)
	}
	bindings, err := mgr.BindingsOf(ctx, string(id), string(account))
	if err != nil {
		t.Fatalf("reading the binding back: %v", err)
	}
	if len(bindings) != 1 || bindings[0].Role != authz.Role(roleRep) {
		t.Fatalf("the manager answers %+v for the account, want one binding to %s: a grant that "+
			"wrote a table of its own leaves the policy empty", bindings, roleRep)
	}
	roles, err := mgr.Roles(ctx, string(id))
	if err != nil {
		t.Fatalf("reading the tenant's roles: %v", err)
	}
	if len(roles) != len(tenantRoles) {
		t.Errorf("a grant left %d roles in the tenant, want the %d a tenant's roles are", len(roles), len(tenantRoles))
	}
}

// TestACrossTenantGrantWritesIntoTheNamedTenantAlone is the case for the
// tenancy of a grant issued from outside a request: the binding lands in the
// domain the caller named, and no other tenant's policy is touched. A grant that
// read the domain from somewhere other than its argument would land in the wrong
// tenant, which the two read-backs tell apart.
func TestACrossTenantGrantWritesIntoTheNamedTenantAlone(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	mgr := newAuthzFakeManager()
	account := authn.AccountID("u_alice")

	if _, err := grantIn(ctx, mgr, tenant.ID("t_acme"), account, roleRep); err != nil {
		t.Fatalf("granting in acme: %v", err)
	}
	inAcme, err := mgr.BindingsOf(ctx, "t_acme", string(account))
	if err != nil {
		t.Fatalf("reading acme's binding: %v", err)
	}
	if len(inAcme) != 1 || inAcme[0].Role != authz.Role(roleRep) {
		t.Errorf("acme answers %+v, want the rep binding alone", inAcme)
	}
	inGlobex, err := mgr.BindingsOf(ctx, "t_globex", string(account))
	if err != nil {
		t.Fatalf("reading globex's binding: %v", err)
	}
	if len(inGlobex) != 0 {
		t.Errorf("globex answers %+v, want none: a grant in acme touched no other tenant", inGlobex)
	}
	globexAll, err := mgr.Bindings(ctx, "t_globex")
	if err != nil {
		t.Fatalf("reading every globex binding: %v", err)
	}
	if len(globexAll) != 0 {
		t.Errorf("globex holds %+v, want no binding at all", globexAll)
	}
}

// TestTheMemberRolesReadFromThePolicy is the case for the role a member is shown
// under: it is the highest role the account holds in the tenant's policy, read
// through the same manager a grant writes through. An account bound to no role
// reads as the empty string; one bound to two reads as the higher. A role read
// from a table of the host's own would leave every account empty, which the
// listing case fails on.
func TestTheMemberRolesReadFromThePolicy(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	mgr := newAuthzFakeManager()
	for _, b := range []authz.Binding{
		{Tenant: "t_acme", Account: "u_alice", Role: authzRoleOwner},
		{Tenant: "t_acme", Account: "u_bob", Role: authzRoleViewer},
		{Tenant: "t_acme", Account: "u_carol", Role: authzRoleViewer},
		{Tenant: "t_acme", Account: "u_carol", Role: authzRoleRep},
		{Tenant: "t_globex", Account: "u_alice", Role: authzRoleAdmin},
	} {
		if err := mgr.PutBinding(ctx, b); err != nil {
			t.Fatalf("binding %s to %s in %s: %v", b.Account, b.Role, b.Tenant, err)
		}
	}
	p := &membersProduct{mgr: mgr}

	roles, err := p.rolesByAccount(ctx, tenant.ID("t_acme"))
	if err != nil {
		t.Fatalf("reading acme's roles by account: %v", err)
	}
	want := map[string]string{"u_alice": roleOwner, "u_bob": roleViewer, "u_carol": roleRep}
	for account, role := range want {
		if got := roles[account]; got != role {
			t.Errorf("acme shows %s as %q, want %q", account, got, role)
		}
	}
	if len(roles) != len(want) {
		t.Errorf("acme's roles by account are %v, want exactly %v", roles, want)
	}

	for _, tc := range []struct{ account, role string }{
		{"u_alice", roleOwner}, // the only binding
		{"u_carol", roleRep},   // the higher of two
		{"u_dan", ""},          // no binding
	} {
		got, err := p.roleOf(ctx, tenant.ID("t_acme"), authn.AccountID(tc.account))
		if err != nil {
			t.Fatalf("reading %s's role: %v", tc.account, err)
		}
		if got != tc.role {
			t.Errorf("acme reads %s's role as %q, want %q", tc.account, got, tc.role)
		}
	}
}

// TestTheCommandLineGrantRecordsItsPurpose is the case for a command-line
// grant's write: the role is a policy binding and no row of the host's own role
// table, and the seam carries the row stating why the write crossed tenants. A
// grant that wrote the host's table leaves no binding; one that skipped the seam
// leaves no purpose.
func TestTheCommandLineGrantRecordsItsPurpose(t *testing.T) {
	t.Parallel()
	forEachStack(t, func(t *testing.T, c *crmCase) {
		c.migrate()
		acme := c.createTenant("acme")
		alice := c.createUser("alice")
		c.grant(acme, alice, roleRep)

		seam := seamRowNamed(c, "membership.grant")
		if seam.purpose == "" {
			t.Errorf("the command-line grant's seam row states no purpose: %+v", seam)
		}
		if want := memberBindingObject(tenant.ID(acme.ID), authn.AccountID(alice.ID)); seam.object != want {
			t.Errorf("the command-line grant's seam row names %q, want %q", seam.object, want)
		}

		if got := c.count("SELECT count(*) FROM authz_bindings WHERE tenant_id = ? AND account_id = ?", acme.ID, alice.ID); got != 1 {
			t.Errorf("the grant left %d policy bindings, want one", got)
		}
		if got := c.count("SELECT count(*) FROM member_roles WHERE account_id = ?", alice.ID); got != 0 {
			t.Errorf("the grant left %d member_roles rows, want none: the role is a policy binding now", got)
		}
	})
}

// seamRowNamed finds the seam row a command wrote for an action. It fails the
// case when no such row is there.
func seamRowNamed(c *crmCase, action string) auditRow {
	c.t.Helper()
	for _, r := range c.journal() {
		if r.source == "seam" && r.action == action {
			return r
		}
	}
	c.t.Fatalf("the journal holds no seam row for %q; it holds %+v", action, c.journal())
	return auditRow{}
}

// TestMeAnswersTheRoleFromThePolicy is the case for the caller's role at the
// account endpoint: GET /me answers the role the caller holds in each tenant
// from that tenant's policy, so changing the binding changes the answer at once,
// with no token to reissue in between.
func TestMeAnswersTheRoleFromThePolicy(t *testing.T) {
	t.Parallel()
	forEachStack(t, func(t *testing.T, c *crmCase) {
		c.migrate()
		h := c.serve()
		defer h.stop(t)
		acme := c.tenantNamed("acme")
		h.adminCreateTenant(t, acme)
		alice := h.adminCreateUser(t, c.userNamed("alice"))
		h.adminGrant(t, acme, alice, roleRep)
		cookie := h.login(t, alice)

		me := h.me(t, bearer(cookie))
		if len(me.Tenants) != 1 || me.Tenants[0].Role != roleRep {
			t.Fatalf("me answers alice in %+v, want one tenant as %s", me.Tenants, roleRep)
		}

		// The binding is changed on the same replica; the next read answers the
		// new role, so the role is the policy's and not something cached on the
		// session.
		h.adminGrant(t, acme, alice, roleAdmin)
		me = h.me(t, bearer(cookie))
		if len(me.Tenants) != 1 || me.Tenants[0].Role != roleAdmin {
			t.Errorf("me answers alice in %+v after the binding changed to %s, want the new role at once",
				me.Tenants, roleAdmin)
		}
	})
}
