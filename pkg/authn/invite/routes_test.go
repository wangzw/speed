package invite

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/authn/authntest"
)

const pathAdmin = "/api/v1/auth/admin/tenants/tenant-a/invitations"

// request sends one request through the Mounter with the body given. The caller
// sets the identity first with Mounter.As or Mounter.AsAnonymous.
func (h *harness) request(method, path string, body any) *httptest.ResponseRecorder {
	h.t.Helper()
	var reader io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			h.t.Fatalf("encoding the body: %v", err)
		}
		reader = bytes.NewReader(encoded)
	}
	req := httptest.NewRequest(method, path, reader)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	return h.mounter.Do(req)
}

func (h *harness) asAdmin() { h.mounter.As(authntest.WithAccount(adminID)) }

func (h *harness) rowCount() int64 {
	h.t.Helper()
	var n int64
	if err := h.handle.Model(&invitationRow{}).Count(&n).Error; err != nil {
		h.t.Fatalf("counting rows: %v", err)
	}
	return n
}

func TestIssueRouteAnswers201AndAuditsBeforeTheAction(t *testing.T) {
	h := newHarness(t)
	h.asAdmin()
	w := h.request(http.MethodPost, pathAdmin, map[string]string{"email": "alice@example.com"})

	if w.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201: %s", w.Code, w.Body.String())
	}
	var answered struct {
		Invitation invitationView `json:"invitation"`
		Token      string         `json:"token"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &answered); err != nil {
		t.Fatalf("decoding the answer: %v", err)
	}
	if answered.Token == "" || answered.Invitation.Status != "pending" {
		t.Errorf("answer = %+v, want a pending invitation and a token", answered)
	}
	rows := h.audit.entries()
	if len(rows) == 0 {
		t.Fatal("no audit row was written for a write")
	}
	if rows[0].Action != actionIssue {
		t.Errorf("audit action = %q, want %q", rows[0].Action, actionIssue)
	}
	dec, ok := h.decider.last()
	if !ok || dec.Action != authn.ActionMembershipManage || dec.Tenant != "tenant-a" {
		t.Errorf("decision = %+v, want membership.manage in tenant-a", dec)
	}
}

func TestIssueRouteWithoutAnIdentityIs401(t *testing.T) {
	h := newHarness(t)
	h.mounter.AsAnonymous()
	w := h.request(http.MethodPost, pathAdmin, map[string]string{"email": "alice@example.com"})

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", w.Code)
	}
	if h.decider.count() != 0 {
		t.Errorf("the Decider was asked %d times for a request with no identity, want none", h.decider.count())
	}
	if h.rowCount() != 0 {
		t.Errorf("a row was written for a request with no identity")
	}
}

func TestIssueRouteRefusedByTheDeciderWritesNothing(t *testing.T) {
	h := newHarness(t)
	h.decider.allow = false
	h.asAdmin()
	w := h.request(http.MethodPost, pathAdmin, map[string]string{"email": "alice@example.com"})

	if w.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", w.Code)
	}
	if h.rowCount() != 0 {
		t.Errorf("a row was written for a refused request")
	}
	if len(h.audit.entries()) != 0 {
		t.Errorf("an audit row was written for a refused request")
	}
}

func TestIssueRouteWithoutADeciderIs403(t *testing.T) {
	h := newHarness(t)
	h.p.decider = nil
	h.asAdmin()
	w := h.request(http.MethodPost, pathAdmin, map[string]string{"email": "alice@example.com"})
	if w.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403 when no Decider is present", w.Code)
	}
	if h.rowCount() != 0 {
		t.Errorf("a row was written with no Decider present")
	}
}

func TestIssueRouteWithAFailedAuditDoesNotAct(t *testing.T) {
	h := newHarness(t)
	h.audit.fails(errors.New("the audit store is down"))
	h.asAdmin()
	w := h.request(http.MethodPost, pathAdmin, map[string]string{"email": "alice@example.com"})

	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503 when the mandatory audit does not land", w.Code)
	}
	if h.rowCount() != 0 {
		t.Errorf("a row was written although the mandatory audit did not land")
	}
}

func TestIssueRouteRefusesAMalformedAddress(t *testing.T) {
	h := newHarness(t)
	h.asAdmin()
	w := h.request(http.MethodPost, pathAdmin, map[string]string{"email": "not-an-address"})
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 for an address that is not well formed", w.Code)
	}
	if h.rowCount() != 0 {
		t.Errorf("a row was written for a refused address")
	}
}

func TestListAndRevokeRoutes(t *testing.T) {
	h := newHarness(t)
	issued := h.issue("tenant-a", "alice@example.com")
	h.asAdmin()

	w := h.request(http.MethodGet, pathAdmin, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("list status = %d, want 200: %s", w.Code, w.Body.String())
	}
	if len(h.audit.entries()) != 0 {
		t.Errorf("a read wrote %d audit rows, want none", len(h.audit.entries()))
	}

	w = h.request(http.MethodDelete, "/api/v1/auth/admin/tenants/tenant-a/invitations/"+issued.Invitation.ID, nil)
	if w.Code != http.StatusNoContent {
		t.Fatalf("revoke status = %d, want 204: %s", w.Code, w.Body.String())
	}
	if got := h.row(issued.Invitation.ID).Status; got != statusRevoked {
		t.Errorf("status after revoke = %d, want revoked", got)
	}
}

func TestRevokingAnAcceptedRouteAnswers409(t *testing.T) {
	h := newHarness(t)
	issued := h.issue("tenant-a", "alice@example.com")
	if _, err := h.p.Accept(context.Background(), AcceptInvite{Token: issued.Token}); err != nil {
		t.Fatalf("accepting: %v", err)
	}
	h.asAdmin()
	w := h.request(http.MethodDelete, "/api/v1/auth/admin/tenants/tenant-a/invitations/"+issued.Invitation.ID, nil)
	if w.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409 revoking an accepted invitation", w.Code)
	}
}

func TestAcceptFaceIsPublicAndTokenAddressed(t *testing.T) {
	h := newHarness(t)
	h.accounts.put("alice@example.com", "acct-alice")
	issued := h.issue("tenant-a", "alice@example.com")

	// The GET carries no identity: the route is public.
	h.mounter.AsAnonymous()
	w := h.request(http.MethodGet, "/api/v1/auth/invitations/"+issued.Token, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("accept view status = %d, want 200: %s", w.Code, w.Body.String())
	}

	// The POST redeems with no identity too.
	w = h.request(http.MethodPost, "/api/v1/auth/invitations/"+issued.Token, map[string]string{"display_name": "Alice"})
	if w.Code != http.StatusCreated {
		t.Fatalf("accept status = %d, want 201: %s", w.Code, w.Body.String())
	}
	if h.members.count != 1 {
		t.Errorf("memberships written = %d, want one", h.members.count)
	}
	// A second acceptance of the same token is refused.
	w = h.request(http.MethodPost, "/api/v1/auth/invitations/"+issued.Token, map[string]string{"display_name": "Alice"})
	if w.Code != http.StatusConflict {
		t.Fatalf("second accept status = %d, want 409", w.Code)
	}
}
