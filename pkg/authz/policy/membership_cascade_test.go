package policy

import (
	"testing"
	"time"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/authz"
	"github.com/vislake/speed/pkg/event"
)

// TestRemovingASystemMembershipRevokesThePlatformDomainBindings checks that removing a system membership revokes the platform domain bindings.
func TestRemovingASystemMembershipRevokesThePlatformDomainBindings(t *testing.T) {
	p := newFixture(t).product
	bus := newRecordingBus()
	wireProduct(t, p, bus)

	seedPolicy(t, p, string(authz.PlatformDomain), string(authz.PlatformAdminRole), "acct-1", "tenant", "read")
	seedPolicy(t, p, string(authz.PlatformDomain), string(authz.TenantAdminRole), "acct-1", "tenant", "read")
	seedPolicy(t, p, "acme", string(authz.TenantAdminRole), "acct-1", "tenant", "read")
	seedBinding(t, p, string(authz.PlatformDomain), "acct-2", string(authz.PlatformAdminRole))

	removed := event.Event{
		Type:     authn.EventMembershipChanged,
		TenantID: string(authz.PlatformDomain),
		Payload:  authn.MembershipChanged{AccountID: "acct-1", Change: "removed"},
	}
	bus.deliver(t, authn.EventMembershipChanged, removed)
	assertBindingCount(t, p, string(authz.PlatformDomain), "acct-1", 0)
	assertBindingCount(t, p, "acme", "acct-1", 1)
	assertBindingCount(t, p, string(authz.PlatformDomain), "acct-2", 1)

	bus.deliver(t, authn.EventMembershipChanged, removed)
	assertBindingCount(t, p, string(authz.PlatformDomain), "acct-1", 0)
}

// TestOnlyARemovalCascadesAndOnlyInItsOwnTenant checks that only a removal cascades and only in its own tenant.
func TestOnlyARemovalCascadesAndOnlyInItsOwnTenant(t *testing.T) {
	p := newFixture(t).product
	bus := newRecordingBus()
	wireProduct(t, p, bus)
	seedPolicy(t, p, string(authz.PlatformDomain), string(authz.PlatformAdminRole), "acct-1", "tenant", "read")

	for _, evt := range []event.Event{
		{
			Type:     authn.EventMembershipChanged,
			TenantID: string(authz.PlatformDomain),
			Payload:  authn.MembershipChanged{AccountID: "acct-1", Change: "suspended"},
		},
		{
			Type:     authn.EventMembershipChanged,
			TenantID: "acme",
			Payload:  authn.MembershipChanged{AccountID: "acct-1", Change: "removed"},
		},
	} {
		bus.deliver(t, authn.EventMembershipChanged, evt)
		assertBindingCount(t, p, string(authz.PlatformDomain), "acct-1", 1)
	}
}

// TestTheCascadeReadsATransportShapedPayload checks that the cascade reads a transport shaped payload.
func TestTheCascadeReadsATransportShapedPayload(t *testing.T) {
	p := newFixture(t).product
	bus := newRecordingBus()
	wireProduct(t, p, bus)
	seedPolicy(t, p, string(authz.PlatformDomain), string(authz.PlatformAdminRole), "acct-1", "tenant", "read")

	bus.deliver(t, authn.EventMembershipChanged, event.Event{
		Type:     authn.EventMembershipChanged,
		TenantID: string(authz.PlatformDomain),
		Payload:  map[string]any{"account_id": "acct-1", "change": "removed"},
	})
	assertBindingCount(t, p, string(authz.PlatformDomain), "acct-1", 0)
}

func seedBinding(t *testing.T, p *product, tenant, account, role string) {
	t.Helper()
	err := p.crossing(t.Context()).Exec(
		"INSERT INTO authz_bindings (tenant_id, account_id, role, granted_at) VALUES (?,?,?,?)",
		tenant, account, role, time.Now().UTC()).Error
	if err != nil {
		t.Fatalf("seeding the binding of %s in %s: %v", account, tenant, err)
	}
}

func assertBindingCount(t *testing.T, p *product, tenant, account string, want int64) {
	t.Helper()
	var got int64
	err := p.crossing(t.Context()).Model(&bindingRow{}).
		Where("tenant_id = ? AND account_id = ?", tenant, account).
		Count(&got).Error
	if err != nil {
		t.Fatalf("counting the bindings of %s in %s: %v", account, tenant, err)
	}
	if got != want {
		t.Errorf("the bindings of %s in %s = %d, want %d", account, tenant, got, want)
	}
}

// TestThePolicyModuleSubscribesToMembershipChanges checks that the policy module subscribes to membership changes.
func TestThePolicyModuleSubscribesToMembershipChanges(t *testing.T) {
	bus := newRecordingBus()
	wireProduct(t, newFixture(t).product, bus)
	if n := len(bus.handlers[authn.EventMembershipChanged]); n != 1 {
		t.Fatalf("%d handlers are subscribed to %s, want 1", n, authn.EventMembershipChanged)
	}
}

// TestRemovingAMembershipClearsEveryBindingInItsTenant checks that removing a membership clears every binding in its tenant.
func TestRemovingAMembershipClearsEveryBindingInItsTenant(t *testing.T) {
	p := newFixture(t).product
	seedPolicy(t, p, "acme", "alpha", "acct-1", "account", "read")
	seedPolicy(t, p, "globex", "globex-only", "acct-1", "account", "read")
	seedPolicy(t, p, string(authz.PlatformDomain), string(authz.TenantAdminRole), "acct-1", "tenant", "read")

	if err := p.MembershipRemoved(t.Context(), "acct-1", "acme"); err != nil {
		t.Fatalf("MembershipRemoved(acme): %v", err)
	}
	assertBindingCount(t, p, "acme", "acct-1", 0)
	assertBindingCount(t, p, "globex", "acct-1", 1)
	assertBindingCount(t, p, string(authz.PlatformDomain), "acct-1", 1)

	if err := p.MembershipRemoved(t.Context(), "acct-1", "acme"); err != nil {
		t.Fatalf("the retried removal: %v", err)
	}
	assertBindingCount(t, p, "acme", "acct-1", 0)
}

// TestAMembershipRemovalEventClearsTheTenantsBindings checks that a membership removal event clears the tenants bindings.
func TestAMembershipRemovalEventClearsTheTenantsBindings(t *testing.T) {
	p := newFixture(t).product
	bus := newRecordingBus()
	wireProduct(t, p, bus)
	seedPolicy(t, p, "acme", "alpha", "acct-1", "account", "read")
	seedPolicy(t, p, "globex", "globex-only", "acct-1", "account", "read")

	bus.deliver(t, authn.EventMembershipChanged, event.Event{
		Type:     authn.EventMembershipChanged,
		TenantID: "acme",
		Payload:  authn.MembershipChanged{AccountID: "acct-1", Change: "suspended"},
	})
	assertBindingCount(t, p, "acme", "acct-1", 1)

	removed := event.Event{
		Type:     authn.EventMembershipChanged,
		TenantID: "acme",
		Payload:  authn.MembershipChanged{AccountID: "acct-1", Change: "removed"},
	}
	bus.deliver(t, authn.EventMembershipChanged, removed)
	assertBindingCount(t, p, "acme", "acct-1", 0)
	assertBindingCount(t, p, "globex", "acct-1", 1)

	bus.deliver(t, authn.EventMembershipChanged, removed)
	assertBindingCount(t, p, "acme", "acct-1", 0)
}
