package personal

import (
	"context"
	"errors"
	"testing"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/authn/authntest"
	"github.com/vislake/speed/pkg/authz"
	"github.com/vislake/speed/pkg/tenant"
)

// The backfill writes the same five pieces, and a second call changes nothing.
func TestEnsureIsIdempotent(t *testing.T) {
	p, roster, memberships, manager, _ := unitProduct()
	ctx := context.Background()

	first, err := p.ensure(ctx, unitAccount)
	if err != nil {
		t.Fatalf("the first ensure: %v", err)
	}
	if !first.Created {
		t.Errorf("the first ensure reports created=false, want true")
	}
	wantID := TenantID(unitAccount)
	if first.Tenant.ID != wantID {
		t.Errorf("the tenant is %q, want %q", first.Tenant.ID, wantID)
	}

	second, err := p.ensure(ctx, unitAccount)
	if err != nil {
		t.Fatalf("the second ensure: %v", err)
	}
	if second.Created {
		t.Errorf("the second ensure reports created=true, want false")
	}
	if roster.creates != 1 || len(memberships.rows[unitAccount]) != 1 ||
		len(manager.roles[string(wantID)]) != 1 || len(manager.bindings[string(wantID)]) != 1 {
		t.Errorf("the second ensure wrote duplicates: tenants=%d memberships=%v roles=%v bindings=%v",
			roster.creates, memberships.rows[unitAccount],
			manager.roles[string(wantID)], manager.bindings[string(wantID)])
	}
}

// The backfill names the tenant's alias after the account's username.
func TestEnsureNamesTheAliasAfterTheUsername(t *testing.T) {
	p, roster, _, _, _ := unitProduct()
	p.accounts = &fakeAccounts{identifiers: []authn.Identifier{
		{Kind: authn.IdentifierEmail, Value: "alice@example.com"},
		{Kind: authn.IdentifierUsername, Value: "alice"},
	}}
	if _, err := p.ensure(context.Background(), unitAccount); err != nil {
		t.Fatalf("the ensure: %v", err)
	}
	row := roster.rows[TenantID(unitAccount)]
	if row.Alias != "alice" {
		t.Errorf("the alias is %q, want the account's username", row.Alias)
	}
}

// Without the account read surface the alias falls back to the derived
// identifier.
func TestEnsureFallsBackToTheDerivedAliasWithoutAnAccountRead(t *testing.T) {
	p, roster, _, _, _ := unitProduct()
	if _, err := p.ensure(context.Background(), unitAccount); err != nil {
		t.Fatalf("the ensure: %v", err)
	}
	id := TenantID(unitAccount)
	if row := roster.rows[id]; row.Alias != string(id) {
		t.Errorf("the alias is %q, want the derived identifier %q", row.Alias, id)
	}
}

// An alias another tenant holds is the identifier-unavailable refusal.
func TestEnsureReportsAHeldAlias(t *testing.T) {
	p, roster, _, _, _ := unitProduct()
	p.accounts = &fakeAccounts{identifiers: []authn.Identifier{{Kind: authn.IdentifierUsername, Value: "alice"}}}
	roster.createErr = tenant.ErrAliasConflict
	_, err := p.ensure(context.Background(), unitAccount)
	if !errors.Is(err, ErrIdentifierUnavailable) {
		t.Fatalf("the ensure gave %v, want ErrIdentifierUnavailable", err)
	}
}

// A disabled tenant is refused without a write.
func TestEnsureRefusesADisabledTenant(t *testing.T) {
	p, roster, memberships, manager, _ := unitProduct()
	id := TenantID(unitAccount)
	roster.rows[id] = tenant.Tenant{ID: id, Alias: string(id), Status: tenant.StatusDisabled}
	memberships.rows[unitAccount] = []authn.Membership{{Account: unitAccount, Tenant: string(id), Active: true}}
	before := len(manager.bindings[string(id)])
	_, err := p.ensure(context.Background(), unitAccount)
	if !errors.Is(err, ErrTenantUnavailable) {
		t.Fatalf("the ensure gave %v, want ErrTenantUnavailable", err)
	}
	if len(manager.bindings[string(id)]) != before {
		t.Errorf("the refused ensure wrote a binding")
	}
}

// A create this call lost to another call leaves a failed audit row beside the
// winner's tenant.
func TestEnsureClosesALostRaceWithAFailedAuditRow(t *testing.T) {
	p, roster, _, _, _ := unitProduct()
	id := TenantID(unitAccount)
	roster.rows[id] = tenant.Tenant{ID: id, Alias: string(id), Status: tenant.StatusActive}
	roster.creates = 0
	roster.createErr = tenant.ErrTenantExists
	result, err := p.ensure(context.Background(), unitAccount)
	if err != nil {
		t.Fatalf("the ensure: %v", err)
	}
	if result.Created {
		t.Errorf("the lost race reports created=true")
	}
	rows := entriesOfAction(p.audit.(*fakeAudit).rows(), personalCreateAction)
	if len(rows) != 2 || rows[0].Outcome != personalOutcomeSuccess || rows[1].Outcome != personalOutcomeFailed {
		t.Errorf("the create audit rows are %+v, want a success and a linked failure", rows)
	}
}

// The kind resolver names only the caller's own space.
func TestKindOfNamesOnlyTheCallersOwnSpace(t *testing.T) {
	p := &product{}
	ctx := authntest.Context(t, context.Background(), authntest.WithAccount(unitAccount))
	if kind, err := p.KindOf(ctx, TenantID(unitAccount)); err != nil || kind != tenant.KindPersonal {
		t.Errorf("KindOf(the caller's space) = (%v, %v), want personal", kind, err)
	}
	for _, id := range []tenant.ID{TenantID("another-account"), "business"} {
		if kind, err := p.KindOf(ctx, id); err != nil || kind != tenant.KindBusiness {
			t.Errorf("KindOf(%q) = (%v, %v), want business", id, kind, err)
		}
	}
	if kind, err := p.KindOf(context.Background(), TenantID(unitAccount)); err != nil || kind != tenant.KindBusiness {
		t.Errorf("KindOf without an identity = (%v, %v), want business", kind, err)
	}
}

// The allowlist is the business set intersected with the governed set, less
// the management actions a single-person space must not have.
func TestTheAllowlistKeepsOnlyGovernedBusinessPermissions(t *testing.T) {
	kept := authz.Permission("crm.deal.read")
	excluded := authz.PermissionMembershipManage
	notGoverned := authz.Permission("crm.deal.delete")
	catalog := fakeCatalog{governed: []authz.Permission{kept, excluded}}
	got := personalAllowlist(catalog, []authz.Permission{kept, excluded, notGoverned})
	if len(got) != 1 || got[0] != kept {
		t.Errorf("the allowlist is %v, want only %q", got, kept)
	}
}
