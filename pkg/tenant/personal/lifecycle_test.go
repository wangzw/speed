package personal

import (
	"context"
	"testing"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/authn/provisioning"
	"github.com/vislake/speed/pkg/authz"
	"github.com/vislake/speed/pkg/tenant"
)

// The creation hook completes the personal tenant when the seam did not.
func TestAccountCreatedBuildsThePersonalTenant(t *testing.T) {
	p, roster, memberships, manager, _ := unitProduct()
	if err := p.AccountCreated(context.Background(), unitAccount); err != nil {
		t.Fatalf("the creation hook: %v", err)
	}
	id := TenantID(unitAccount)
	if row, found := roster.rows[id]; !found || row.Status != tenant.StatusActive {
		t.Errorf("the creation hook left %+v, want an active personal tenant", roster.rows)
	}
	if len(memberships.rows[unitAccount]) != 1 {
		t.Errorf("the creation hook wrote %d membership(s), want one", len(memberships.rows[unitAccount]))
	}
	if len(manager.roles[string(id)]) != 1 || len(manager.bindings[string(id)]) != 1 {
		t.Errorf("the creation hook wrote roles=%v bindings=%v, want one of each",
			manager.roles[string(id)], manager.bindings[string(id)])
	}
}

// A service account's creation hook is no work.
func TestAccountCreatedSkipsAServiceAccount(t *testing.T) {
	p, roster, _, _, _ := unitProduct()
	p.accounts = &fakeAccounts{kind: authn.KindService, status: authn.StatusActive}
	if err := p.AccountCreated(context.Background(), unitAccount); err != nil {
		t.Fatalf("the creation hook: %v", err)
	}
	if len(roster.rows) != 0 {
		t.Errorf("a service account was given a personal tenant: %v", roster.rows)
	}
}

// The creation hook on a tombstone is no work.
func TestAccountCreatedLeavesATombstoneAlone(t *testing.T) {
	p, roster, _, _, _ := unitProduct()
	id := TenantID(unitAccount)
	roster.rows[id] = tenant.Tenant{ID: id, Alias: string(id), Status: tenant.StatusDeleted}
	if err := p.AccountCreated(context.Background(), unitAccount); err != nil {
		t.Fatalf("the creation hook: %v", err)
	}
	if got := roster.rows[id].Status; got != tenant.StatusDeleted {
		t.Errorf("the creation hook changed a tombstone to %v", got)
	}
}

// The account-caused disable disables the tenant and records the account as the
// source.
func TestAccountDisabledDisablesThePersonalTenant(t *testing.T) {
	p, roster, _, _, _ := seededPersonal(t)
	id := TenantID(unitAccount)
	if err := p.AccountDisabled(context.Background(), unitAccount); err != nil {
		t.Fatalf("the disable hook: %v", err)
	}
	row := roster.rows[id]
	if row.Status != tenant.StatusDisabled || row.DisabledBy != tenant.DisabledByAccount {
		t.Errorf("the tenant is %v/%v, want disabled by the account", row.Status, row.DisabledBy)
	}
}

// An account's own disable is lifted; an operator's disable stands.
func TestTheEnableHookRestoresOnlyTheAccountsDisable(t *testing.T) {
	t.Run("an account's disable is lifted", func(t *testing.T) {
		p, roster, _, _, _ := seededPersonal(t)
		id := TenantID(unitAccount)
		if err := p.AccountDisabled(context.Background(), unitAccount); err != nil {
			t.Fatalf("the disable hook: %v", err)
		}
		if err := p.AccountEnabled(context.Background(), unitAccount); err != nil {
			t.Fatalf("the enable hook: %v", err)
		}
		if got := roster.rows[id].Status; got != tenant.StatusActive {
			t.Errorf("the tenant is %v, want active", got)
		}
	})

	t.Run("an operator's disable stands", func(t *testing.T) {
		p, roster, _, _, _ := seededPersonal(t)
		id := TenantID(unitAccount)
		row := roster.rows[id]
		row.Status = tenant.StatusDisabled
		row.DisabledBy = tenant.DisabledByOperator
		roster.rows[id] = row

		if err := p.AccountEnabled(context.Background(), unitAccount); err != nil {
			t.Fatalf("the enable hook: %v", err)
		}
		if got := roster.rows[id].Status; got != tenant.StatusDisabled {
			t.Errorf("the tenant is %v, want the operator's disable to stand", got)
		}
		if roster.statusWrites != 0 {
			t.Errorf("the hook wrote the status %d time(s), want none", roster.statusWrites)
		}
	})
}

// The status hooks leave a missing row and a tombstone alone.
func TestTheStatusHooksLeaveAMissingRowAndATombstoneAlone(t *testing.T) {
	t.Run("a missing row", func(t *testing.T) {
		p, roster, _, _, _ := unitProduct()
		if err := p.AccountDisabled(context.Background(), unitAccount); err != nil {
			t.Fatalf("the disable hook: %v", err)
		}
		if roster.statusCalls != 0 {
			t.Errorf("the hook called the roster %d time(s), want none", roster.statusCalls)
		}
	})

	t.Run("a tombstone", func(t *testing.T) {
		p, roster, _, _, _ := unitProduct()
		id := TenantID(unitAccount)
		roster.rows[id] = tenant.Tenant{ID: id, Alias: string(id), Status: tenant.StatusDeleted}
		if err := p.AccountDisabled(context.Background(), unitAccount); err != nil {
			t.Fatalf("the disable hook: %v", err)
		}
		if roster.statusCalls != 0 {
			t.Errorf("the hook called the roster %d time(s), want none", roster.statusCalls)
		}
	})
}

// The deletion runs its four steps in order: memberships, policy, audit,
// tombstone.
func TestAccountDeletedRunsTheStepsInOrder(t *testing.T) {
	p, roster, memberships, manager, auditor := seededPersonal(t)
	id := TenantID(unitAccount)
	other := authn.AccountID("9other")
	memberships.rows[other] = []authn.Membership{{Account: other, Tenant: string(id), Active: true, Source: "admin"}}
	manager.bindings[string(id)] = append(manager.bindings[string(id)], authz.Binding{
		Tenant: string(id), Account: other.String(), Role: authz.TenantAdminRole,
	})
	steps := &stepLog{}
	roster.steps, memberships.steps, manager.steps = steps, steps, steps
	auditor.steps = steps

	if err := p.AccountDeleted(context.Background(), unitAccount); err != nil {
		t.Fatalf("the deletion hook: %v", err)
	}
	got := steps.snapshot()
	order := []string{"members", "remove:", "unbind:", "role:", "audit:", "delete"}
	at := 0
	for _, step := range got {
		if at < len(order) && len(step) >= len(order[at]) && step[:len(order[at])] == order[at] {
			at++
		}
	}
	if at != len(order) {
		t.Errorf("the deletion steps are %v, want %v in that order", got, order)
	}
	if got := roster.rows[id].Status; got != tenant.StatusDeleted {
		t.Errorf("the tenant is %v after the deletion, want deleted", got)
	}
}

// The deletion's tombstone is its completion marker: a second call writes
// nothing.
func TestAccountDeletedIsIdempotentOnATombstone(t *testing.T) {
	p, roster, _, _, auditor := seededPersonal(t)
	if err := p.AccountDeleted(context.Background(), unitAccount); err != nil {
		t.Fatalf("the deletion hook: %v", err)
	}
	if err := p.AccountDeleted(context.Background(), unitAccount); err != nil {
		t.Fatalf("the repeated deletion hook: %v", err)
	}
	if roster.deletes != 1 {
		t.Errorf("the repeated deletion wrote the tombstone %d time(s), want once", roster.deletes)
	}
	if rows := entriesOfAction(auditor.rows(), personalDeleteAction); len(rows) != 1 {
		t.Errorf("the repeated deletion wrote %d audit row(s), want one", len(rows))
	}
}

// The transaction seam writes the tombstone before the deletion hook runs, so
// the hook must still remove the memberships the seam could not reach: the rows
// an administrator added are not the account's own row, which the account
// transaction erased.
func TestAccountDeletedClearsTheMembershipsOfATombstonedTenant(t *testing.T) {
	p, roster, memberships, _, _ := unitProduct()
	id := TenantID(unitAccount)
	roster.rows[id] = tenant.Tenant{ID: id, Alias: string(id), Status: tenant.StatusDeleted}
	other := authn.AccountID("9other")
	memberships.rows[other] = []authn.Membership{
		{Account: other, Tenant: string(id), Active: true, Source: "admin"},
	}
	if err := p.AccountDeleted(context.Background(), unitAccount); err != nil {
		t.Fatalf("the deletion hook: %v", err)
	}
	if len(memberships.rows[other]) != 0 {
		t.Errorf("the deletion left the membership of %s in the tombstoned tenant: %v",
			other, memberships.rows[other])
	}
	if roster.deleteAttempts != 0 {
		t.Errorf("the deletion hook wrote a tombstone over an existing one")
	}
}

// A deletion whose audit row cannot land stops before the tombstone.
func TestAccountDeletedStopsWhenTheAuditRowFails(t *testing.T) {
	p, roster, _, _, auditor := seededPersonal(t)
	auditor.err = context.DeadlineExceeded
	if err := p.AccountDeleted(context.Background(), unitAccount); err == nil {
		t.Fatal("the deletion succeeded with a failing audit row")
	}
	if got := roster.rows[TenantID(unitAccount)].Status; got == tenant.StatusDeleted {
		t.Error("the deletion wrote the tombstone despite the failed audit row")
	}
}

// A deletion that finds no tenant is no work.
func TestAccountDeletedDoesNothingWithoutAPersonalTenant(t *testing.T) {
	p, _, _, _, _ := unitProduct()
	auditor := p.audit.(*fakeAudit)
	if err := p.AccountDeleted(context.Background(), unitAccount); err != nil {
		t.Fatalf("the deletion hook: %v", err)
	}
	if len(auditor.rows()) != 0 {
		t.Errorf("the deletion wrote audit rows for a missing tenant: %v", auditor.rows())
	}
}

// Revoking a service secret touches no personal tenant.
func TestServiceSecretRevokedLeavesThePersonalTenantAlone(t *testing.T) {
	p, roster, _, _, _ := seededPersonal(t)
	before := roster.statusCalls
	if err := p.ServiceSecretRevoked(context.Background(), unitAccount, "secret"); err != nil {
		t.Fatalf("the revocation hook: %v", err)
	}
	if roster.statusCalls != before {
		t.Errorf("the revocation hook moved the tenant")
	}
}

// The lifecycle, provisioning and kind surfaces are the ones the descriptor
// provides.
func TestTheProductDeliversTheThreePromisedCapabilities(t *testing.T) {
	p := &product{}
	if _, ok := any(p).(authn.AccountLifecycle); !ok {
		t.Error("the product does not implement authn.AccountLifecycle")
	}
	if _, ok := any(p).(provisioning.AccountProvisioning); !ok {
		t.Error("the product does not implement provisioning.AccountProvisioning")
	}
	if _, ok := any(p).(tenant.KindResolver); !ok {
		t.Error("the product does not implement tenant.KindResolver")
	}
}
