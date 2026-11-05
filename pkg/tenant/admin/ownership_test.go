package admin

import (
	"net/http"
	"testing"

	"github.com/vislake/speed/pkg/authn/authntest"
	"github.com/vislake/speed/pkg/tenant"
)

// The transfer route is platform staff's: it reads the tenant for its two
// existence answers, lands the mandatory audit row and calls the organization
// writer with the target.
func TestTheOwnerTransferGoesThroughTheOrganizationWriter(t *testing.T) {
	r := newRig(t)
	r.catalog.held = tenant.Tenant{
		ID: "t_acme", Alias: "acme", Status: tenant.StatusActive,
		OwnerAccount: "acc-owner",
	}
	r.catalog.found = true

	w := r.do(http.MethodPut, pathTenants+"/t_acme/owner", map[string]any{"account": "acc-2"})
	if w.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204 (body %s)", w.Code, w.Body.String())
	}
	calls := r.organization.recorded()
	if len(calls) != 1 || calls[0].op != "TransferOwner" || calls[0].id != "t_acme" || calls[0].owner != "acc-2" {
		t.Fatalf("the organization writer was called %+v, want one TransferOwner of t_acme to acc-2", calls)
	}
	if rows := r.audit.entries(); len(rows) != 1 || rows[0].Action != actionTenantOwnerTransfer {
		t.Errorf("audit rows = %+v, want one %q", rows, actionTenantOwnerTransfer)
	}
}

// The transfer's mandatory audit row lands before the writer is reached.
func TestTheOwnerTransferAuditsBeforeTheWrite(t *testing.T) {
	r := newRig(t)
	r.catalog.held = tenant.Tenant{ID: "t_acme", Alias: "acme", Status: tenant.StatusActive}
	r.catalog.found = true
	atWrite := -1
	r.organization.during = func() { atWrite = len(r.audit.entries()) }
	r.do(http.MethodPut, pathTenants+"/t_acme/owner", map[string]any{"account": "acc-2"})
	if atWrite != 1 {
		t.Errorf("the writer ran with %d audit row(s), want the mandatory one already written", atWrite)
	}
}

// A transfer refusal is a 409 with the capability's conflict type, and the
// failed row is linked to the mandatory one.
func TestTheOwnerTransferRefusalIsAConflict(t *testing.T) {
	for name, refusal := range map[string]error{
		"not a member":           tenant.ErrOwnerNotMember,
		"the tenant was deleted": tenant.ErrTenantDeleted,
	} {
		t.Run(name, func(t *testing.T) {
			r := newRig(t)
			r.catalog.held = tenant.Tenant{ID: "t_acme", Alias: "acme", Status: tenant.StatusActive}
			r.catalog.found = true
			r.organization.fail(refusal)

			w := r.do(http.MethodPut, pathTenants+"/t_acme/owner", map[string]any{"account": "acc-2"})
			if w.Code != http.StatusConflict {
				t.Fatalf("status = %d, want 409 (body %s)", w.Code, w.Body.String())
			}
			if got := problemOf(t, w); got.Type != typePrefix+conflictCode {
				t.Errorf("problem type = %q, want %q", got.Type, typePrefix+conflictCode)
			}
			rows := r.audit.entries()
			if len(rows) != 2 || rows[0].Outcome != outcomeSuccess || rows[1].Outcome != outcomeFailed {
				t.Errorf("audit rows = %+v, want a success row and a linked failed row", rows)
			}
		})
	}
}

// Platform staff delete without an ownership expectation; the route passes the
// empty expectation to the writer.
func TestPlatformStaffDeleteWithoutAnOwnerExpectation(t *testing.T) {
	r := newRig(t)
	r.catalog.held = tenant.Tenant{
		ID: "t_acme", Alias: "acme", Status: tenant.StatusActive,
		OwnerAccount: "acc-owner",
	}
	r.catalog.found = true

	w := r.do(http.MethodDelete, pathTenants+"/t_acme", nil)
	if w.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204 (body %s)", w.Code, w.Body.String())
	}
	calls := r.organization.recorded()
	if len(calls) != 1 || calls[0].op != "DeleteOrganization" || calls[0].expectedOwner != "" {
		t.Fatalf("the organization writer was called %+v, want a deletion with no expectation", calls)
	}
	if rows := r.audit.entries(); len(rows) != 1 || rows[0].Action != actionTenantDelete {
		t.Errorf("audit rows = %+v, want one %q", rows, actionTenantDelete)
	}
}

// The organization's owner deletes without platform permission, and the
// deletion carries the owner it observed so a racing transfer refuses it.
func TestTheOwnerDeletesWithTheObservedOwner(t *testing.T) {
	r := newRig(t)
	r.guard.allow = false
	r.catalog.held = tenant.Tenant{
		ID: "t_acme", Alias: "acme", Status: tenant.StatusActive,
		OwnerAccount: authntest.DefaultAccount.String(),
	}
	r.catalog.found = true

	w := r.do(http.MethodDelete, pathTenants+"/t_acme", nil)
	if w.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204 (body %s)", w.Code, w.Body.String())
	}
	calls := r.organization.recorded()
	if len(calls) != 1 || calls[0].expectedOwner != authntest.DefaultAccount.String() {
		t.Fatalf("the organization writer was called %+v, want the observed owner", calls)
	}
}

// A transfer that commits between the guard's read and the handler's read does
// not hand the deletion the new owner as its own expectation: the caller is the
// expected owner, so the writer refuses with ErrOwnerChanged.
func TestTheOwnerDeletionExpectsTheCallerNotTheRowItReadsAgain(t *testing.T) {
	r := newRig(t)
	r.guard.allow = false
	owner := authntest.DefaultAccount
	r.catalog.held = tenant.Tenant{
		ID: "t_acme", Alias: "acme", Status: tenant.StatusActive,
		OwnerAccount: owner.String(),
	}
	r.catalog.found = true
	reads := 0
	r.catalog.getHook = func() {
		reads++
		if reads > 1 {
			// A transfer committed after the guard saw the caller as owner.
			r.catalog.held.OwnerAccount = "acc-new-owner"
		}
	}

	w := r.do(http.MethodDelete, pathTenants+"/t_acme", nil)
	if w.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204 (body %s)", w.Code, w.Body.String())
	}
	calls := r.organization.recorded()
	if len(calls) != 1 || calls[0].expectedOwner != owner.String() {
		t.Fatalf("the organization writer was called %+v, want the caller as the expected owner", calls)
	}
}

// A tenant administrator who is not the owner is refused, and nothing reaches
// the writer or the audit.
func TestANonOwnerTenantAdministratorCannotDelete(t *testing.T) {
	r := newRig(t)
	r.guard.allow = false
	r.catalog.held = tenant.Tenant{
		ID: "t_acme", Alias: "acme", Status: tenant.StatusActive,
		OwnerAccount: "acc-owner",
	}
	r.catalog.found = true

	w := r.do(http.MethodDelete, pathTenants+"/t_acme", nil)
	if w.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403 (body %s)", w.Code, w.Body.String())
	}
	if calls := r.organization.recorded(); len(calls) != 0 {
		t.Errorf("the writer was called %+v, want no call", calls)
	}
	if rows := r.audit.entries(); len(rows) != 0 {
		t.Errorf("audit rows = %+v, want none", rows)
	}
}

// A tombstone is a 409 for a deletion, and a protected tenant is a 403 with the
// write guard's type; neither reaches the writer.
func TestTheDeleteRefusesATombstoneAndAProtectedTenant(t *testing.T) {
	t.Run("a tombstone", func(t *testing.T) {
		r := newRig(t)
		r.catalog.held = tenant.Tenant{ID: "t_acme", Alias: "acme", Status: tenant.StatusDeleted}
		r.catalog.found = true
		w := r.do(http.MethodDelete, pathTenants+"/t_acme", nil)
		if w.Code != http.StatusConflict {
			t.Fatalf("status = %d, want 409 (body %s)", w.Code, w.Body.String())
		}
		if calls := r.organization.recorded(); len(calls) != 0 {
			t.Errorf("the writer was called %+v, want no call", calls)
		}
	})

	t.Run("a protected tenant", func(t *testing.T) {
		r := newRig(t)
		r.catalog.held = tenant.Tenant{ID: "t_acme", Alias: "acme", Status: tenant.StatusActive}
		r.catalog.found = true
		r.writeGuard.denied = true
		w := r.do(http.MethodDelete, pathTenants+"/t_acme", nil)
		if w.Code != http.StatusForbidden {
			t.Fatalf("status = %d, want 403 (body %s)", w.Code, w.Body.String())
		}
		if got := problemOf(t, w); got.Type != typePrefix+protectedCode {
			t.Errorf("problem type = %q, want %q", got.Type, typePrefix+protectedCode)
		}
		if calls := r.organization.recorded(); len(calls) != 0 {
			t.Errorf("the writer was called %+v, want no call", calls)
		}
	})
}

// A deletion whose transfer-won race is refused is a 409, and the failure is
// closed with a failed audit row.
func TestTheDeleteRefusesAnOwnerThatChanged(t *testing.T) {
	r := newRig(t)
	r.catalog.held = tenant.Tenant{ID: "t_acme", Alias: "acme", Status: tenant.StatusActive}
	r.catalog.found = true
	r.organization.fail(tenant.ErrOwnerChanged)

	w := r.do(http.MethodDelete, pathTenants+"/t_acme", nil)
	if w.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409 (body %s)", w.Code, w.Body.String())
	}
	if got := problemOf(t, w); got.Type != typePrefix+conflictCode {
		t.Errorf("problem type = %q, want %q", got.Type, typePrefix+conflictCode)
	}
	rows := r.audit.entries()
	if len(rows) != 2 || rows[0].Outcome != outcomeSuccess || rows[1].Outcome != outcomeFailed {
		t.Errorf("audit rows = %+v, want a success row and a linked failed row", rows)
	}
}

// The deletion's mandatory audit row lands before the writer is reached.
func TestTheDeleteAuditsBeforeTheWrite(t *testing.T) {
	r := newRig(t)
	r.catalog.held = tenant.Tenant{ID: "t_acme", Alias: "acme", Status: tenant.StatusActive}
	r.catalog.found = true
	atWrite := -1
	r.organization.during = func() { atWrite = len(r.audit.entries()) }
	r.do(http.MethodDelete, pathTenants+"/t_acme", nil)
	if atWrite != 1 {
		t.Errorf("the writer ran with %d audit row(s), want the mandatory one already written", atWrite)
	}
}

// A tombstone read still answers 200 with the deleted state: the row is there,
// and a surface renders it read-only.
func TestTheTombstoneReadCarriesTheDeletedState(t *testing.T) {
	r := newRig(t)
	r.catalog.held = tenant.Tenant{ID: "t_acme", Alias: "acme", Status: tenant.StatusDeleted}
	r.catalog.found = true
	w := r.do(http.MethodGet, pathTenants+"/t_acme", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body %s)", w.Code, w.Body.String())
	}
	var got struct {
		Tenant tenantView `json:"tenant"`
	}
	decode(t, w, &got)
	if got.Tenant.Status != "deleted" {
		t.Errorf("status = %q, want deleted", got.Tenant.Status)
	}
}
