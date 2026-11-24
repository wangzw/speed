package policy

import (
	"errors"
	"slices"
	"testing"

	"github.com/vislake/speed/pkg/authz"
)

// TestReconcilingABuiltinTenantAdminConvergesOnlyTowardTheLists holds the
// convergence: a missing target grant is added, a held grant the caller
// lists as retired is removed, and a held grant in neither list stays.
func TestReconcilingABuiltinTenantAdminConvergesOnlyTowardTheLists(t *testing.T) {
	p := newProductOver(t)
	bus := newRecordingBus()
	wireProduct(t, p, bus)
	ctx := t.Context()
	if err := p.begin(ctx); err != nil {
		t.Fatalf("begin: %v", err)
	}
	const tenant = "acme"
	role := authz.TenantAdminRole
	if err := p.PutRole(ctx, authz.RoleRecord{Tenant: tenant, Role: role, DescriptionKey: "k"}); err != nil {
		t.Fatalf("PutRole: %v", err)
	}
	if _, err := p.SeedGrants(ctx, tenant, role, []authz.Grant{
		{Resource: "account", Action: "read"},   // in the target, already held
		{Resource: "audit", Action: "read"},     // retired, held
		{Resource: "session", Action: "revoke"}, // in neither list
	}); err != nil {
		t.Fatalf("SeedGrants: %v", err)
	}
	if err := p.MarkRoleBuiltin(ctx, tenant, role); err != nil {
		t.Fatalf("MarkRoleBuiltin: %v", err)
	}
	if err := p.PutBinding(ctx, authz.Binding{Tenant: tenant, Account: "alice", Role: role}); err != nil {
		t.Fatalf("PutBinding: %v", err)
	}
	baseline := len(bus.events())

	target := []authz.Grant{
		{Resource: "account", Action: "read"},
		{Resource: "settings", Action: "write"},
	}
	retired := []authz.Grant{{Resource: "audit", Action: "read"}}
	added, removed, err := p.ReconcileGrants(ctx, tenant, role, target, retired)
	if err != nil {
		t.Fatalf("ReconcileGrants: %v", err)
	}
	if added != 1 || removed != 1 {
		t.Errorf("ReconcileGrants added %d and removed %d, want one each", added, removed)
	}
	want := []authz.Grant{
		{Resource: "account", Action: "read"},
		{Resource: "session", Action: "revoke"},
		{Resource: "settings", Action: "write"},
	}
	if got := grantedPairs(t, &fixture{product: p}, tenant, role); !slices.Equal(got, want) {
		t.Errorf("the role holds %+v, want %+v", got, want)
	}

	// The convergence reloaded the snapshot: the added permission decides and
	// the retired one is gone from the subject's answers.
	held, err := p.Held(ctx, authz.Domain(tenant), "alice")
	if err != nil {
		t.Fatalf("Held: %v", err)
	}
	wantHeld := []authz.Permission{
		authz.PermissionAccountRead,
		authz.PermissionSessionRevoke,
		authz.PermissionSettingsWrite,
	}
	if !slices.Equal(held, wantHeld) {
		t.Errorf("Held = %v, want %v", held, wantHeld)
	}

	events := bus.events()
	if len(events) != baseline+1 || events[baseline].Type != policyChangedEventType {
		t.Fatalf("the convergence published %v, want one %q event",
			events[baseline:], policyChangedEventType)
	}

	// The same lists again, now that the role holds the target and not the
	// retired grant: nothing to write and nothing to publish.
	added, removed, err = p.ReconcileGrants(ctx, tenant, role, target, retired)
	if err != nil {
		t.Fatalf("the second ReconcileGrants: %v", err)
	}
	if added != 0 || removed != 0 {
		t.Errorf("the second ReconcileGrants added %d and removed %d, want zero", added, removed)
	}
	if got := len(bus.events()); got != baseline+1 {
		t.Errorf("the second convergence left %d event(s), want %d", got, baseline+1)
	}
}

// TestReconcilingRefusesAnythingButABuiltinTenantAdmin holds the target rules:
// another role and a tenant-admin that is not builtin are ErrReservedRole, an
// absent role is ErrUnknownRole and an empty tenant ErrTenantMismatch, each
// before a row is written.
func TestReconcilingRefusesAnythingButABuiltinTenantAdmin(t *testing.T) {
	p := newProductOver(t)
	wireProduct(t, p, nil)
	ctx := t.Context()
	if err := p.begin(ctx); err != nil {
		t.Fatalf("begin: %v", err)
	}

	if err := p.PutRole(ctx, authz.RoleRecord{Tenant: "acme", Role: "editor", DescriptionKey: "k"}); err != nil {
		t.Fatalf("PutRole(editor): %v", err)
	}
	if err := p.MarkRoleBuiltin(ctx, "acme", "editor"); err != nil {
		t.Fatalf("MarkRoleBuiltin(editor): %v", err)
	}
	if _, _, err := p.ReconcileGrants(ctx, "acme", "editor",
		[]authz.Grant{{Resource: "deal", Action: "read"}}, nil); !errors.Is(err, authz.ErrReservedRole) {
		t.Errorf("converging a non-tenant-admin role gave %v, want ErrReservedRole", err)
	}

	if err := p.PutRole(ctx, authz.RoleRecord{Tenant: "globex", Role: authz.TenantAdminRole, DescriptionKey: "k"}); err != nil {
		t.Fatalf("PutRole(globex tenant-admin): %v", err)
	}
	if _, _, err := p.ReconcileGrants(ctx, "globex", authz.TenantAdminRole, nil, nil); !errors.Is(err, authz.ErrReservedRole) {
		t.Errorf("converging a non-builtin tenant-admin gave %v, want ErrReservedRole", err)
	}

	if _, _, err := p.ReconcileGrants(ctx, "nowhere", authz.TenantAdminRole, nil, nil); !errors.Is(err, authz.ErrUnknownRole) {
		t.Errorf("converging an absent role gave %v, want ErrUnknownRole", err)
	}
	if _, _, err := p.ReconcileGrants(ctx, "", authz.TenantAdminRole, nil, nil); !errors.Is(err, authz.ErrTenantMismatch) {
		t.Errorf("converging in an empty tenant gave %v, want ErrTenantMismatch", err)
	}
}
