package main

import (
	"sort"
	"testing"

	"github.com/vislake/speed/pkg/authz"
	"github.com/vislake/speed/pkg/core"
)

// The operator-provisioning command binds the full static credential account of
// the operator subject to the operator role in the reserved platform domain,
// through the policy manager. The shape case holds it to the host's
// command-group mechanism; the live case runs the command over a migrated
// database and reads the policy rows back, so what it holds is the persisted
// policy rather than a value in process.

// operatorManagementActions is the identity and authorization management
// actions the platform operator holds, written out independently of the host's
// grant matrix so a matrix that dropped one is caught here as well. It is every
// management action that decides in the platform domain: the account, session,
// factor, client, identity-provider and policy-role actions, and the carried
// settings and audit surface an operator administers in the platform domain.
//
// It holds neither authn.tenant.access (the operator stays a stranger to every
// tenant) nor authn.impersonation.start (a static subject holds no tenant
// session to start one from), and not authn.membership.manage,
// authn.impersonation.read or authn.impersonation.revoke: those three decide in
// the tenant domain, the operator holds no tenant binding, so the operator does
// not carry them.
var operatorManagementActions = []authz.Permission{
	authz.PermissionAccountRead, authz.PermissionAccountCreate,
	authz.PermissionAccountUpdate, authz.PermissionAccountDisable, authz.PermissionAccountDelete,
	authz.PermissionSessionRead, authz.PermissionSessionRevoke,
	authz.PermissionMFAReset,
	authz.PermissionClientManage,
	authz.PermissionIDPManage,
	authz.PermissionRoleRead, authz.PermissionRoleManage,
	authz.PermissionSettingsRead, authz.PermissionSettingsWrite,
	authz.PermissionAuditRead,
}

// TestTheOperatorProvisionCommandRunsUnderTheHostsCommandGroup is the case for
// the shape the provision command is registered with: it is a leaf of the host's
// own command-group mechanism, it drives to Start because its write is recorded
// through the audit seam, and its module names the policy manager it writes
// through, so a command that took the module in without the manager fails at
// startup rather than at the first write.
func TestTheOperatorProvisionCommandRunsUnderTheHostsCommandGroup(t *testing.T) {
	t.Parallel()
	if operatorProvisionGroup.word != "operator" {
		t.Fatalf("the operator group is %q, want operator", operatorProvisionGroup.word)
	}
	if len(operatorProvisionGroup.commands) != 1 {
		t.Fatalf("the operator group carries %d commands, want one", len(operatorProvisionGroup.commands))
	}
	leaf := operatorProvisionGroup.commands[0]
	if leaf.use != "provision" {
		t.Errorf("the operator group's leaf is %q, want provision", leaf.use)
	}
	if leaf.depth != core.StageStart {
		t.Errorf("the provision command drives to %v, want Start: its write is recorded through the audit seam", leaf.depth)
	}

	var found *application
	for i := range applications {
		if applications[i].module.Name == operatorCommandsModuleName {
			found = &applications[i]
		}
	}
	if found == nil {
		t.Fatalf("no application is registered under %q", operatorCommandsModuleName)
	}
	if !contains(found.runsOn, operatorProvisionGroup.word) {
		t.Errorf("%q runs on %v, which does not name the operator group", operatorCommandsModuleName, found.runsOn)
	}
	if !contains(modulesOn(operatorProvisionGroup.word), operatorCommandsModuleName) {
		t.Errorf("the command tree does not take %q into the operator group", operatorCommandsModuleName)
	}
	if !requiresCapability(found.module, (*authz.Manager)(nil)) {
		t.Errorf("%q does not require the policy manager it writes through", operatorCommandsModuleName)
	}
}

// operatorGrantPairs reads a role's grant rows in a domain as the
// (resource, action) pairs the grant table stores, sorted. It reads the rows the
// command wrote rather than a manager's view, so the case holds the persisted
// policy.
func operatorGrantPairs(t *testing.T, c *crmCase, tenant string, role authz.Role) []string {
	t.Helper()
	rows, err := c.sql().Query(
		c.rebind("SELECT resource, action FROM authz_grants WHERE tenant_id = ? AND role = ?"),
		tenant, string(role))
	if err != nil {
		t.Fatalf("reading the %s role's grants in %q: %v", role, tenant, err)
	}
	defer func() { _ = rows.Close() }()
	var out []string
	for rows.Next() {
		var resource, action string
		if err := rows.Scan(&resource, &action); err != nil {
			t.Fatalf("scanning a grant row of the %s role in %q: %v", role, tenant, err)
		}
		out = append(out, resource+"\x00"+action)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("reading the %s role's grants in %q: %v", role, tenant, err)
	}
	sort.Strings(out)
	return out
}

// TestTheOperatorProvisionCommandWritesThePlatformOperator is the case for the
// provision command over a migrated database. Running it binds the operator's
// static account to the operator role in the reserved platform domain and writes
// the role's grants; a second run is idempotent; and the role carries neither
// authn.tenant.access (so the operator stays a stranger to every tenant,
// answered the same 404 as a non-member) nor authn.impersonation.start (so a
// static subject, which holds no tenant session to start one from, is refused).
func TestTheOperatorProvisionCommandWritesThePlatformOperator(t *testing.T) {
	t.Parallel()
	c := newCase(t, stackNamed(t, localStack))
	c.migrate()

	c.mustRun("", "operator", "provision")

	domain := string(authz.PlatformDomain)
	account := string(operatorAccount)
	role := string(authzRoleOperator)

	// The operator's binding is in the reserved platform domain: the full
	// static.ops account, the operator role, and that domain.
	if got := c.intFrom("SELECT COUNT(*) FROM authz_bindings WHERE tenant_id = ? AND account_id = ? AND role = ?",
		domain, account, role); got != 1 {
		t.Fatalf("the platform domain holds %d bindings of %s to %s, want one", got, account, role)
	}

	// Its grant set is exactly the operator matrix.
	got := operatorGrantPairs(t, c, domain, authzRoleOperator)
	want := grantPairSetOf(t, platformOperatorGrants)
	if !equalSets(got, want) {
		t.Errorf("the operator role carries %v, want exactly %v", got, want)
	}

	// Every authn.* management action is granted, held independently of the
	// host's grant matrix.
	held := map[string]bool{}
	for _, row := range got {
		held[row] = true
	}
	for _, perm := range operatorManagementActions {
		object, action, ok := resourceActionOf(perm)
		if !ok {
			t.Fatalf("the carried permission %q has no translation row", perm)
		}
		if !held[object+"\x00"+action] {
			t.Errorf("the operator role is not granted %q (%s, %s)", perm, object, action)
		}
	}

	// The actions the operator must not hold, held to their pairs. An operator
	// stays a stranger to every tenant (authn.tenant.access) and a static subject
	// holds no tenant session (authn.impersonation.start); authn.membership.manage
	// and authn.impersonation.read/revoke decide in the tenant domain, so a
	// platform-domain grant of one would be inert anyway and is not written.
	for _, forbidden := range []authz.Permission{
		authz.PermissionTenantAccess, authz.PermissionImpersonateStart,
		authz.PermissionMembershipManage,
		authz.PermissionImpersonationRead, authz.PermissionImpersonationRevoke,
	} {
		object, action, ok := resourceActionOf(forbidden)
		if !ok {
			t.Fatalf("the carried permission %q has no translation row", forbidden)
		}
		for _, row := range got {
			if row == object+"\x00"+action {
				t.Errorf("the operator role carries %q (%s, %s), which it must not", forbidden, object, action)
			}
		}
	}

	// A second run is idempotent: it rewrites the same rows and adds none.
	c.mustRun("", "operator", "provision")
	if again := operatorGrantPairs(t, c, domain, authzRoleOperator); !equalSets(again, want) {
		t.Errorf("a second run left the operator grants %v, want %v", again, want)
	}
	if got := c.intFrom("SELECT COUNT(*) FROM authz_bindings WHERE tenant_id = ?", domain); got != 1 {
		t.Errorf("a second run left %d platform bindings, want one", got)
	}

	// The binding lives in the platform domain alone: no tenant domain holds a
	// binding of the operator.
	if got := c.intFrom("SELECT COUNT(*) FROM authz_bindings WHERE account_id = ? AND tenant_id <> ?",
		account, domain); got != 0 {
		t.Errorf("a tenant domain holds %d bindings of %s, want none", got, account)
	}
}
