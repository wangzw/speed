package admin

import (
	"net/http"
	"testing"
	"time"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/authn/authntest"
	"github.com/vislake/speed/pkg/tenant"
)

func requestForTarget() tenant.TransferRequest {
	return tenant.TransferRequest{
		ID:          "22222222-2222-2222-2222-222222222222",
		Tenant:      "t_acme",
		From:        "acc-owner",
		To:          authntest.DefaultAccount.String(),
		Status:      tenant.TransferPending,
		RequestedAt: time.Unix(10, 0).UTC(),
		ExpiresAt:   time.Unix(20, 0).UTC(),
	}
}

func TestTheTargetListsItsOwnPendingRequests(t *testing.T) {
	r := newRig(t)
	r.catalog.held = tenant.Tenant{ID: "t_acme", Name: "acme", Status: tenant.StatusActive}
	r.catalog.found = true
	r.target.answer(requestForTarget(), true, []tenant.TransferRequest{requestForTarget()}, nil)

	w := r.do(http.MethodGet, pathMyRequests, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body %s)", w.Code, w.Body.String())
	}
	var got struct {
		Requests []MyTransferView `json:"requests"`
	}
	decode(t, w, &got)
	if len(got.Requests) != 1 || got.Requests[0].ID != requestForTarget().ID ||
		got.Requests[0].Tenant != "acme" {
		t.Errorf("the answer is %+v, want the pending request naming acme", got.Requests)
	}
	if asked := r.guard.askedFor(); len(asked) != 0 {
		t.Errorf("the guard was asked for %v, want no permission decision", asked)
	}
}

func TestTheTargetReadsOneRequest(t *testing.T) {
	r := newRig(t)
	r.catalog.held = tenant.Tenant{ID: "t_acme", Name: "acme", Status: tenant.StatusActive}
	r.catalog.found = true
	r.target.answer(requestForTarget(), true, nil, nil)

	path := pathMyRequest[:len(pathMyRequest)-len(":request")] + requestForTarget().ID
	w := r.do(http.MethodGet, path, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body %s)", w.Code, w.Body.String())
	}

	r.target.fail(tenant.ErrTransferNotTarget)
	w = r.do(http.MethodGet, path, nil)
	if w.Code != http.StatusNotFound {
		t.Fatalf("another account's request answered %d, want 404 (body %s)", w.Code, w.Body.String())
	}
}

func TestTheTargetConfirmsThroughTheOwnershipTarget(t *testing.T) {
	r := newRig(t)
	r.catalog.held = tenant.Tenant{ID: "t_acme", Name: "acme", Status: tenant.StatusActive}
	r.catalog.found = true
	r.target.answer(requestForTarget(), true, nil, nil)

	path := pathMyConfirm[:len(pathMyConfirm)-len(":request/confirm")] + requestForTarget().ID + "/confirm"
	w := r.do(http.MethodPost, path, nil)
	if w.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204 (body %s)", w.Code, w.Body.String())
	}
	calls := r.target.recorded()
	if len(calls) != 1 || calls[0].op != "ConfirmTransfer" ||
		calls[0].request != requestForTarget().ID || calls[0].owner != authntest.DefaultAccount.String() {
		t.Fatalf("the target surface was called %+v, want one confirmation by the caller", calls)
	}
	rows := r.audit.entries()
	if len(rows) != 1 || rows[0].Action != actionTenantOwnerTransfer ||
		rows[0].Detail[detailSurface] != surfaceTarget {
		t.Errorf("audit rows = %+v, want one %q naming the target surface", rows, actionTenantOwnerTransfer)
	}
}

func TestConfirmIsRegistrationGatedAndDeclineIsNot(t *testing.T) {
	confirmPath := pathMyConfirm[:len(pathMyConfirm)-len(":request/confirm")] + requestForTarget().ID + "/confirm"
	declinePath := pathMyDecline[:len(pathMyDecline)-len(":request/decline")] + requestForTarget().ID + "/decline"

	t.Run("confirm is refused", func(t *testing.T) {
		r := newRig(t)
		r.catalog.held = tenant.Tenant{ID: "t_acme", Name: "acme", Status: tenant.StatusActive}
		r.catalog.found = true
		r.target.answer(requestForTarget(), true, nil, nil)
		r.signup.answer(authn.SignupPending, nil)

		w := r.do(http.MethodPost, confirmPath, nil)
		if w.Code != http.StatusForbidden {
			t.Fatalf("status = %d, want 403 (body %s)", w.Code, w.Body.String())
		}
		if calls := r.target.recorded(); len(calls) != 0 {
			t.Errorf("the target surface was called %+v, want no call", calls)
		}
	})

	t.Run("decline is not gated", func(t *testing.T) {
		r := newRig(t)
		r.catalog.held = tenant.Tenant{ID: "t_acme", Name: "acme", Status: tenant.StatusActive}
		r.catalog.found = true
		r.target.answer(requestForTarget(), true, nil, nil)
		r.signup.answer(authn.SignupPending, nil)

		w := r.do(http.MethodPost, declinePath, nil)
		if w.Code != http.StatusNoContent {
			t.Fatalf("status = %d, want 204 (body %s)", w.Code, w.Body.String())
		}
		calls := r.target.recorded()
		if len(calls) != 1 || calls[0].op != "DeclineTransfer" {
			t.Fatalf("the target surface was called %+v, want one decline", calls)
		}
	})
}

func TestATargetConfirmationRefusalIsAConflict(t *testing.T) {
	r := newRig(t)
	r.catalog.held = tenant.Tenant{ID: "t_acme", Name: "acme", Status: tenant.StatusActive}
	r.catalog.found = true
	r.target.answer(requestForTarget(), true, nil, nil)
	r.target.failAction(tenant.ErrOwnerChanged)

	path := pathMyConfirm[:len(pathMyConfirm)-len(":request/confirm")] + requestForTarget().ID + "/confirm"
	w := r.do(http.MethodPost, path, nil)
	if w.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409 (body %s)", w.Code, w.Body.String())
	}
	if got := problemOf(t, w).Type; got != typePrefix+conflictCode {
		t.Errorf("problem type = %q, want %q", got, typePrefix+conflictCode)
	}
	rows := r.audit.entries()
	if len(rows) != 2 || rows[0].Outcome != outcomeSuccess || rows[1].Outcome != outcomeFailed {
		t.Errorf("audit rows = %+v, want a success row and a linked failed row", rows)
	}
}
