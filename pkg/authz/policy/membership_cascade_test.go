package policy

import (
	"testing"
	"time"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/authz"
	"github.com/vislake/speed/pkg/event"
)

// The platform administrator role exists only in the platform domain, and a
// binding to it is refused for an account that is not an active member of that
// tenant. Removing the membership afterwards therefore has to withdraw the
// account's platform-domain bindings too: a binding that outlived its membership
// would keep granting the role the write-side check refused to create.
//
// The cascade is driven by the membership change event, which is idempotent and
// at least once. The case holds the event, not one delivery of it: the second
// delivery must change nothing.
func TestRemovingASystemMembershipRevokesThePlatformDomainBindings(t *testing.T) {
	p := newFixture(t).product
	bus := newRecordingBus()
	wireProduct(t, p, bus)

	// acct-1 holds both platform-administrator and tenant-admin in the platform
	// domain and a tenant-admin binding in a business tenant. acct-2 holds one
	// platform-administrator binding that must survive.
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

	// The second delivery of the same event is no work and no error.
	bus.deliver(t, authn.EventMembershipChanged, removed)
	assertBindingCount(t, p, string(authz.PlatformDomain), "acct-1", 0)
}

// Only a removal cascades, and only in the tenant the envelope names:
// suspending the membership is a state change the account can be resumed from,
// and a removal in a business tenant leaves every other tenant's bindings alone.
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

// A payload a transport decoded arrives as a map rather than the publisher's
// value, and the handler reads both shapes the same way.
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

// seedBinding writes one binding straight into the table, for an account whose
// domain already holds the role the binding names.
func seedBinding(t *testing.T, p *product, tenant, account, role string) {
	t.Helper()
	err := p.crossing(t.Context()).Exec(
		"INSERT INTO authz_bindings (tenant_id, account_id, role, granted_at) VALUES (?,?,?,?)",
		tenant, account, role, time.Now().UTC()).Error
	if err != nil {
		t.Fatalf("seeding the binding of %s in %s: %v", account, tenant, err)
	}
}

// assertBindingCount holds how many bindings one account holds in one domain.
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

// The subscription is registered when the module is wired, not when the first
// event arrives: a bus freezes its subscription table at its start stage.
func TestThePolicyModuleSubscribesToMembershipChanges(t *testing.T) {
	bus := newRecordingBus()
	wireProduct(t, newFixture(t).product, bus)
	if n := len(bus.handlers[authn.EventMembershipChanged]); n != 1 {
		t.Fatalf("%d handlers are subscribed to %s, want 1", n, authn.EventMembershipChanged)
	}
}

// An explicit removal clears every binding the account holds in the tenant it
// left, not only in the platform domain: a member who is removed from a business
// tenant loses the roles that tenant gave them, while their roles elsewhere stay.
// The lifecycle hook is the ordinary path; the event is the crash-window
// compensation and runs the same statement.
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

	// A retry, and the rejoin that follows it, start from no binding: the hook is
	// idempotent and the membership store writes no binding back.
	if err := p.MembershipRemoved(t.Context(), "acct-1", "acme"); err != nil {
		t.Fatalf("the retried removal: %v", err)
	}
	assertBindingCount(t, p, "acme", "acct-1", 0)
}

// The membership.changed subscription is the compensation for a crash between
// the removal's commit and the lifecycle hook, so it clears the tenant the
// envelope names. A suspension is a state the member can be resumed from and
// clears nothing.
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

	// A second delivery of the same event is no work and no error.
	bus.deliver(t, authn.EventMembershipChanged, removed)
	assertBindingCount(t, p, "acme", "acct-1", 0)
}
