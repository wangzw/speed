package invite

import (
	"errors"
	"net/http"
	"testing"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/authn/authntest"
)

// TestAServiceAccountCannotAcceptAnInvitation checks that a service account, even one holding the invited address, is refused with invitation-target-invalid.
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

// TestASubjectWithNoAccountRowCannotAcceptAnInvitation checks that a subject with no account row is refused the same invitation-target-invalid, not a 401.
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

// TestAReadFailureOnTheAcceptingAccountAnswers503 checks that a failed account read answers 503 and leaves the invitation pending.
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
