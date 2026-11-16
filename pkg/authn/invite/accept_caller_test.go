package invite

import (
	"errors"
	"net/http"
	"testing"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/authn/authntest"
)

// TestAServiceAccountCannotAcceptAnInvitation holds the acceptance's caller
// check: a service account can hold the invited address as a proven identifier
// and reach the optional route with a client-credentials identity, but it signs
// in with a service secret and has no acceptable membership. The acceptance
// refuses it with 403 invitation-target-invalid, not a 401: the caller is
// authenticated, and a client that reads a 401 as an expired credential would
// refresh and retry a permanent refusal. The invitation stays pending and no
// membership is written.
func TestAServiceAccountCannotAcceptAnInvitation(t *testing.T) {
	h := newHarness(t)
	issued := h.issue(tenant, "alice@example.com")
	h.accounts.kinds["acct-service"] = authn.KindService
	h.acceptAsHTTP("acct-service", "alice@example.com")

	w := h.request(http.MethodPost, "/api/v1/auth/invitations/"+issued.Token, map[string]any{})
	if w.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403: %s", w.Code, w.Body.String())
	}
	if got, want := problemTypeOf(t, w), typePrefix+"invitation-target-invalid"; got != want {
		t.Errorf("problem type = %q, want %q", got, want)
	}
	if challenge := w.Header().Get("WWW-Authenticate"); challenge != "" {
		t.Errorf("the 403 carries the challenge %q, want none: the caller is authenticated", challenge)
	}
	if row := h.row(issued.Invitation.ID); row.Status != statusPending || row.AcceptedAt != nil {
		t.Errorf("the row reads status %d and accepted_at %v, want pending and not accepted", row.Status, row.AcceptedAt)
	}
	if h.members.count != 0 {
		t.Errorf("the refused acceptance wrote %d membership(s), want none", h.members.count)
	}
}

// TestASubjectWithNoAccountRowCannotAcceptAnInvitation holds the same refusal
// for a subject that has no account row at all, such as a static password-table
// subject: it is authenticated, but it is not a user account, so the answer is
// the same 403 invitation-target-invalid rather than a 401 challenge.
func TestASubjectWithNoAccountRowCannotAcceptAnInvitation(t *testing.T) {
	h := newHarness(t)
	issued := h.issue(tenant, "alice@example.com")
	h.proveEmail("static.ops", "alice@example.com")
	h.mounter.As(authntest.WithBackend("static"), authntest.WithAccount("static.ops"))
	h.accounts.gone("static.ops")

	w := h.request(http.MethodPost, "/api/v1/auth/invitations/"+issued.Token, map[string]any{})
	if w.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403: %s", w.Code, w.Body.String())
	}
	if got, want := problemTypeOf(t, w), typePrefix+"invitation-target-invalid"; got != want {
		t.Errorf("problem type = %q, want %q", got, want)
	}
	if row := h.row(issued.Invitation.ID); row.Status != statusPending {
		t.Errorf("the row reads status %d, want pending", row.Status)
	}
	if h.members.count != 0 {
		t.Errorf("the refused acceptance wrote %d membership(s), want none", h.members.count)
	}
}

// TestAReadFailureOnTheAcceptingAccountAnswers503 holds that a caller's account
// read that fails is unavailable, not a refusal and not a pass: the acceptance
// answers 503, and the invitation stays pending so the same token can be
// redeemed once the dependency answers again.
func TestAReadFailureOnTheAcceptingAccountAnswers503(t *testing.T) {
	h := newHarness(t)
	issued := h.issue(tenant, "alice@example.com")
	h.acceptAsHTTP("acct-alice", "alice@example.com")
	h.accounts.failsFor("acct-alice", errors.New("the account store is down"))

	w := h.request(http.MethodPost, "/api/v1/auth/invitations/"+issued.Token, map[string]any{})
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503: %s", w.Code, w.Body.String())
	}
	if got, want := problemTypeOf(t, w), typePrefix+"unavailable"; got != want {
		t.Errorf("problem type = %q, want %q", got, want)
	}
	if row := h.row(issued.Invitation.ID); row.Status != statusPending || row.AcceptedAt != nil {
		t.Errorf("the row reads status %d and accepted_at %v, want pending and not accepted", row.Status, row.AcceptedAt)
	}
	if h.members.count != 0 {
		t.Errorf("the failed acceptance wrote %d membership(s), want none", h.members.count)
	}
}
