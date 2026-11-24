package admin

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/vislake/speed/pkg/authn/authntest"
	"github.com/vislake/speed/pkg/tenant"
)

func TestTheOwnerTransferRequestGoesThroughTheOrganizationWriter(t *testing.T) {
	r := newRig(t)
	r.catalog.held = tenant.Tenant{
		ID: "t_acme", Name: "acme", Status: tenant.StatusActive,
		OwnerAccount: "acc-owner",
	}
	r.catalog.found = true

	w := r.do(http.MethodPost, pathTenants+"/t_acme/owner/requests", map[string]any{"account": "acc-2"})
	if w.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201 (body %s)", w.Code, w.Body.String())
	}
	calls := r.organization.recorded()
	if len(calls) != 1 || calls[0].op != "RequestTransfer" || calls[0].id != "t_acme" ||
		calls[0].owner != "acc-2" || calls[0].expectedOwner != "" {
		t.Fatalf("the organization writer was called %+v, want one RequestTransfer of t_acme to acc-2 with no expectation", calls)
	}
	if rows := r.audit.entries(); len(rows) != 1 || rows[0].Action != actionTenantOwnerTransferRequest {
		t.Errorf("audit rows = %+v, want one %q", rows, actionTenantOwnerTransferRequest)
	}
}

func TestTheOwnerTransferRequestAuditsBeforeTheWrite(t *testing.T) {
	r := newRig(t)
	r.catalog.held = tenant.Tenant{ID: "t_acme", Name: "acme", Status: tenant.StatusActive}
	r.catalog.found = true
	atWrite := -1
	r.organization.during = func() { atWrite = len(r.audit.entries()) }
	r.do(http.MethodPost, pathTenants+"/t_acme/owner/requests", map[string]any{"account": "acc-2"})
	if atWrite != 1 {
		t.Errorf("the writer ran with %d audit row(s), want the mandatory one already written", atWrite)
	}
}

func TestTheOwnerTransferRequestRefusalIsAConflict(t *testing.T) {
	for name, refusal := range map[string]error{
		"not a member":           tenant.ErrOwnerNotMember,
		"an inactive account":    tenant.ErrOwnerInactive,
		"the tenant was deleted": tenant.ErrTenantDeleted,
		"a request is pending":   tenant.ErrTransferAlreadyPending,
		"no owner":               tenant.ErrNoOwner,
	} {
		t.Run(name, func(t *testing.T) {
			r := newRig(t)
			r.catalog.held = tenant.Tenant{ID: "t_acme", Name: "acme", Status: tenant.StatusActive}
			r.catalog.found = true
			r.organization.fail(refusal)

			w := r.do(http.MethodPost, pathTenants+"/t_acme/owner/requests", map[string]any{"account": "acc-2"})
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

func TestTheOwnerTransferRequestRefusesTheCurrentOwner(t *testing.T) {
	r := newRig(t)
	r.catalog.held = tenant.Tenant{ID: "t_acme", Name: "acme", Status: tenant.StatusActive}
	r.catalog.found = true
	r.organization.fail(tenant.ErrTransferTargetIsOwner)

	w := r.do(http.MethodPost, pathTenants+"/t_acme/owner/requests", map[string]any{"account": "acc-2"})
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 (body %s)", w.Code, w.Body.String())
	}
	if got := problemOf(t, w); got.Type != typePrefix+badRequestCode {
		t.Errorf("problem type = %q, want %q", got.Type, typePrefix+badRequestCode)
	}
}

func TestTheOwnerTransferRequestRefusesAPersonalOrganization(t *testing.T) {
	r := newRig(t)
	r.catalog.held = tenant.Tenant{ID: "personal-acc-1", Name: "personal", Status: tenant.StatusActive}
	r.catalog.found = true
	r.p.kind = stubKind{"personal-acc-1": tenant.KindPersonal}

	w := r.do(http.MethodPost, pathTenants+"/personal-acc-1/owner/requests", map[string]any{"account": "acc-2"})
	if w.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403 (body %s)", w.Code, w.Body.String())
	}
	if got := problemOf(t, w); got.Type != typePrefix+protectedCode {
		t.Errorf("problem type = %q, want %q", got.Type, typePrefix+protectedCode)
	}
	if calls := r.organization.recorded(); len(calls) != 0 {
		t.Errorf("the writer was called %+v, want no call", calls)
	}
}

func TestPlatformStaffDeleteWithoutAnOwnerExpectation(t *testing.T) {
	r := newRig(t)
	r.catalog.held = tenant.Tenant{
		ID: "t_acme", Name: "acme", Status: tenant.StatusActive,
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

func TestTheOwnerDeletesWithTheObservedOwner(t *testing.T) {
	r := newRig(t)
	r.guard.allow = false
	r.catalog.held = tenant.Tenant{
		ID: "t_acme", Name: "acme", Status: tenant.StatusActive,
		OwnerAccount: authntest.DefaultAccount.String(),
	}
	r.catalog.found = true

	w := r.doInTenant(http.MethodDelete, tenantPath(pathOrganization, "t_acme"), "t_acme", nil)
	if w.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204 (body %s)", w.Code, w.Body.String())
	}
	calls := r.organization.recorded()
	if len(calls) != 1 || calls[0].expectedOwner != authntest.DefaultAccount.String() {
		t.Fatalf("the organization writer was called %+v, want the observed owner", calls)
	}
	rows := r.audit.entries()
	if len(rows) != 1 || rows[0].Detail[detailSurface] != surfaceTenant {
		t.Errorf("audit rows = %+v, want one naming the tenant surface", rows)
	}
}

func TestTheOwnerCannotDeleteThroughThePlatformSurface(t *testing.T) {
	r := newRig(t)
	r.guard.allow = false
	r.catalog.held = tenant.Tenant{
		ID: "t_acme", Name: "acme", Status: tenant.StatusActive,
		OwnerAccount: authntest.DefaultAccount.String(),
	}
	r.catalog.found = true

	w := r.do(http.MethodDelete, pathTenant, nil)
	if w.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403 (body %s)", w.Code, w.Body.String())
	}
	if calls := r.organization.recorded(); len(calls) != 0 {
		t.Errorf("the writer was called %+v, want no call", calls)
	}
}

func TestTheOwnerCannotTransferThroughThePlatformSurface(t *testing.T) {
	r := newRig(t)
	r.guard.allow = false
	r.catalog.held = tenant.Tenant{
		ID: "t_acme", Name: "acme", Status: tenant.StatusActive,
		OwnerAccount: authntest.DefaultAccount.String(),
	}
	r.catalog.found = true

	w := r.do(http.MethodPost, pathTenants+"/t_acme/owner/requests", map[string]any{"account": "acc-2"})
	if w.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403 (body %s)", w.Code, w.Body.String())
	}
	if calls := r.organization.recorded(); len(calls) != 0 {
		t.Errorf("the writer was called %+v, want no call", calls)
	}
}

func TestTheOwnerRequestsATransferThroughTheTenantSurface(t *testing.T) {
	r := newRig(t)
	r.guard.allow = false
	r.catalog.held = tenant.Tenant{
		ID: "t_acme", Name: "acme", Status: tenant.StatusActive,
		OwnerAccount: authntest.DefaultAccount.String(),
	}
	r.catalog.found = true

	w := r.doInTenant(http.MethodPost, tenantPath(pathOwnerRequests, "t_acme"), "t_acme", map[string]any{"account": "acc-2"})
	if w.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201 (body %s)", w.Code, w.Body.String())
	}
	calls := r.organization.recorded()
	if len(calls) != 1 || calls[0].op != "RequestTransfer" || calls[0].id != "t_acme" ||
		calls[0].owner != "acc-2" || calls[0].expectedOwner != authntest.DefaultAccount.String() {
		t.Fatalf("the organization writer was called %+v, want one RequestTransfer of t_acme to acc-2 expected by the caller", calls)
	}
	rows := r.audit.entries()
	if len(rows) != 1 || rows[0].Detail[detailSurface] != surfaceTenant {
		t.Errorf("audit rows = %+v, want one naming the tenant surface", rows)
	}
}

func TestThePendingTransferRead(t *testing.T) {
	t.Run("a pending request", func(t *testing.T) {
		r := newRig(t)
		r.catalog.held = tenant.Tenant{ID: "t_acme", Name: "acme", Status: tenant.StatusActive}
		r.catalog.found = true
		r.organization.pending = tenant.TransferRequest{
			ID: "22222222-2222-2222-2222-222222222222", To: "acc-2",
			Status: tenant.TransferPending, RequestedAt: time.Unix(10, 0), ExpiresAt: time.Unix(20, 0),
		}
		r.organization.pendingFound = true

		w := r.do(http.MethodGet, pathTenants+"/t_acme/owner/requests/pending", nil)
		if w.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200 (body %s)", w.Code, w.Body.String())
		}
		var got struct {
			Request *TenantTransferView `json:"request"`
		}
		decode(t, w, &got)
		if got.Request == nil || got.Request.ID != "22222222-2222-2222-2222-222222222222" ||
			got.Request.Status != "pending" {
			t.Errorf("the answer is %+v, want the pending request", got.Request)
		}
		var raw struct {
			Request map[string]any `json:"request"`
		}
		decode(t, w, &raw)
		for _, key := range []string{"target_display_name", "target_username", "requested_at", "expires_at"} {
			if _, present := raw.Request[key]; !present {
				t.Errorf("the transfer view is missing %q: %v", key, raw.Request)
			}
		}
		for _, key := range []string{"targetDisplayName", "targetUsername"} {
			if _, present := raw.Request[key]; present {
				t.Errorf("the transfer view carries %q, want the underscore spelling: %v", key, raw.Request)
			}
		}
	})
	t.Run("no pending request", func(t *testing.T) {
		r := newRig(t)
		r.catalog.held = tenant.Tenant{ID: "t_acme", Name: "acme", Status: tenant.StatusActive}
		r.catalog.found = true
		w := r.do(http.MethodGet, pathTenants+"/t_acme/owner/requests/pending", nil)
		if w.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200 (body %s)", w.Code, w.Body.String())
		}
		if body := strings.TrimSpace(w.Body.String()); body != `{"request":null}` {
			t.Errorf("the answer is %s, want an explicit null request", body)
		}
	})
}

func TestCancellingAnOwnerTransferRequest(t *testing.T) {
	t.Run("platform staff cancel with no expectation", func(t *testing.T) {
		r := newRig(t)
		r.catalog.held = tenant.Tenant{ID: "t_acme", Name: "acme", Status: tenant.StatusActive}
		r.catalog.found = true
		requestID := "22222222-2222-2222-2222-222222222222"
		w := r.do(http.MethodDelete, pathTenants+"/t_acme/owner/requests/"+requestID, nil)
		if w.Code != http.StatusNoContent {
			t.Fatalf("status = %d, want 204 (body %s)", w.Code, w.Body.String())
		}
		calls := r.organization.recorded()
		if len(calls) != 1 || calls[0].op != "CancelTransfer" || calls[0].request != requestID ||
			calls[0].expectedOwner != "" {
			t.Fatalf("the writer was called %+v, want one cancellation with no expectation", calls)
		}
		if rows := r.audit.entries(); len(rows) != 1 || rows[0].Action != actionTenantOwnerTransferCancel {
			t.Errorf("audit rows = %+v, want one %q", rows, actionTenantOwnerTransferCancel)
		}
	})
	t.Run("the owner cancels with the caller as the expectation", func(t *testing.T) {
		r := newRig(t)
		r.guard.allow = false
		r.catalog.held = tenant.Tenant{
			ID: "t_acme", Name: "acme", Status: tenant.StatusActive,
			OwnerAccount: authntest.DefaultAccount.String(),
		}
		r.catalog.found = true
		requestID := "22222222-2222-2222-2222-222222222222"
		target := strings.Replace(tenantPath(pathOwnerRequest, "t_acme"), ":request", requestID, 1)
		w := r.doInTenant(http.MethodDelete, target, "t_acme", nil)
		if w.Code != http.StatusNoContent {
			t.Fatalf("status = %d, want 204 (body %s)", w.Code, w.Body.String())
		}
		calls := r.organization.recorded()
		if len(calls) != 1 || calls[0].op != "CancelTransfer" ||
			calls[0].expectedOwner != authntest.DefaultAccount.String() {
			t.Fatalf("the writer was called %+v, want the caller as the expectation", calls)
		}
	})
}

func TestCancellingAResolvedRequestIsAConflict(t *testing.T) {
	r := newRig(t)
	r.catalog.held = tenant.Tenant{ID: "t_acme", Name: "acme", Status: tenant.StatusActive}
	r.catalog.found = true
	r.organization.fail(tenant.ErrTransferNotPending)
	w := r.do(http.MethodDelete, pathTenants+"/t_acme/owner/requests/22222222-2222-2222-2222-222222222222", nil)
	if w.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409 (body %s)", w.Code, w.Body.String())
	}
}

func TestPlatformStaffCannotUseTheTenantSurfaceWithoutBeingTheOwner(t *testing.T) {
	for name, call := range map[string]func(*rig) int{
		"request": func(r *rig) int {
			return r.doInTenant(http.MethodPost, tenantPath(pathOwnerRequests, "t_acme"), "t_acme", map[string]any{"account": "acc-2"}).Code
		},
		"delete": func(r *rig) int {
			return r.doInTenant(http.MethodDelete, tenantPath(pathOrganization, "t_acme"), "t_acme", nil).Code
		},
	} {
		t.Run(name, func(t *testing.T) {
			r := newRig(t)
			r.guard.allow = true
			r.catalog.held = tenant.Tenant{
				ID: "t_acme", Name: "acme", Status: tenant.StatusActive,
				OwnerAccount: "acc-owner",
			}
			r.catalog.found = true
			if code := call(r); code != http.StatusForbidden {
				t.Fatalf("status = %d, want 403", code)
			}
			if calls := r.organization.recorded(); len(calls) != 0 {
				t.Errorf("the writer was called %+v, want no call", calls)
			}
			if rows := r.audit.entries(); len(rows) != 0 {
				t.Errorf("audit rows = %+v, want none", rows)
			}
		})
	}
}

func TestTheOwnerDeletionExpectsTheCallerNotTheRowItReadsAgain(t *testing.T) {
	r := newRig(t)
	r.guard.allow = false
	owner := authntest.DefaultAccount
	r.catalog.held = tenant.Tenant{
		ID: "t_acme", Name: "acme", Status: tenant.StatusActive,
		OwnerAccount: owner.String(),
	}
	r.catalog.found = true
	reads := 0
	r.catalog.getHook = func() {
		reads++
		if reads > 1 {
			r.catalog.held.OwnerAccount = "acc-new-owner"
		}
	}

	w := r.doInTenant(http.MethodDelete, tenantPath(pathOrganization, "t_acme"), "t_acme", nil)
	if w.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204 (body %s)", w.Code, w.Body.String())
	}
	calls := r.organization.recorded()
	if len(calls) != 1 || calls[0].expectedOwner != owner.String() {
		t.Fatalf("the organization writer was called %+v, want the caller as the expected owner", calls)
	}
}

func TestANonOwnerTenantAdministratorCannotDelete(t *testing.T) {
	r := newRig(t)
	r.guard.allow = false
	r.catalog.held = tenant.Tenant{
		ID: "t_acme", Name: "acme", Status: tenant.StatusActive,
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

func TestTheDeleteRefusesATombstoneAndAProtectedTenant(t *testing.T) {
	t.Run("a tombstone", func(t *testing.T) {
		r := newRig(t)
		r.catalog.held = tenant.Tenant{ID: "t_acme", Name: "acme", Status: tenant.StatusDeleted}
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
		r.catalog.held = tenant.Tenant{ID: "t_acme", Name: "acme", Status: tenant.StatusActive}
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

func TestTheDeleteRefusesAnOwnerThatChanged(t *testing.T) {
	r := newRig(t)
	r.catalog.held = tenant.Tenant{ID: "t_acme", Name: "acme", Status: tenant.StatusActive}
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

func TestTheDeleteAuditsBeforeTheWrite(t *testing.T) {
	r := newRig(t)
	r.catalog.held = tenant.Tenant{ID: "t_acme", Name: "acme", Status: tenant.StatusActive}
	r.catalog.found = true
	atWrite := -1
	r.organization.during = func() { atWrite = len(r.audit.entries()) }
	r.do(http.MethodDelete, pathTenants+"/t_acme", nil)
	if atWrite != 1 {
		t.Errorf("the writer ran with %d audit row(s), want the mandatory one already written", atWrite)
	}
}

func TestTheTombstoneReadCarriesTheDeletedState(t *testing.T) {
	r := newRig(t)
	r.catalog.held = tenant.Tenant{ID: "t_acme", Name: "acme", Status: tenant.StatusDeleted}
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
