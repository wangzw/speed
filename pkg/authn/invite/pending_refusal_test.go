package invite

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	speedhttp "github.com/vislake/speed/pkg/http"
)

// TestThePendingRefusalIsAConflictProblem checks that the pending refusal is a
// 409 invitation-pending.
func TestThePendingRefusalIsAConflictProblem(t *testing.T) {
	h := newHarness(t)
	err := h.p.answer("issuing an invitation", ErrInvitationPending)

	var fault speedhttp.Fault
	if !errors.As(err, &fault) {
		t.Fatalf("answer gave %T %v, want a speedhttp.Fault", err, err)
	}
	if fault.Status != http.StatusConflict {
		t.Errorf("status = %d, want 409", fault.Status)
	}
	if want := "urn:speed:authn:invitation-pending"; fault.Type != want {
		t.Errorf("problem type = %q, want %q", fault.Type, want)
	}
}

// TestASecondIssuanceToAPendingAddressIsRefused checks that a second issuance to
// a pending address is refused and changes nothing.
func TestASecondIssuanceToAPendingAddressIsRefused(t *testing.T) {
	h := newHarness(t)
	first := h.issue(tenant, "alice@example.com")
	before := h.rowCount()

	_, err := h.p.Issue(context.Background(), IssueInvite{Tenant: tenant, Email: "ALICE@example.com"})
	if !errors.Is(err, ErrInvitationPending) {
		t.Fatalf("the second issuance gave %v, want ErrInvitationPending", err)
	}
	if got := h.rowCount(); got != before {
		t.Errorf("the refused issuance changed the row count from %d to %d", before, got)
	}
	held := h.row(first.Invitation.ID)
	if held.Status != statusPending {
		t.Errorf("the first invitation's status = %d, want pending", held.Status)
	}
	if held.TokenDigest != tokenDigest(first.Token) {
		t.Error("the refused issuance replaced the first invitation's token")
	}
}

// TestConcurrentIssuancesLeaveOneInvitation checks that two concurrent issuances
// leave one row and one pending refusal.
func TestConcurrentIssuancesLeaveOneInvitation(t *testing.T) {
	h := newHarness(t)
	results := make(chan error, 2)
	for i := 0; i < 2; i++ {
		go func() {
			_, err := h.p.Issue(context.Background(), IssueInvite{Tenant: tenant, Email: "alice@example.com"})
			results <- err
		}()
	}
	var issued, refused int
	for i := 0; i < 2; i++ {
		switch err := <-results; {
		case err == nil:
			issued++
		case errors.Is(err, ErrInvitationPending):
			refused++
		default:
			t.Errorf("a concurrent issuance gave %v, want success or ErrInvitationPending", err)
		}
	}
	if issued != 1 || refused != 1 {
		t.Errorf("%d issuances succeeded and %d were refused, want one of each", issued, refused)
	}
	if got := h.rowCount(); got != 1 {
		t.Errorf("the race left %d rows, want one", got)
	}
}

// TestTheIssueRouteAnswers409ForAPendingAddress checks that the HTTP face answers
// 409 invitation-pending for a pending address.
func TestTheIssueRouteAnswers409ForAPendingAddress(t *testing.T) {
	h := newHarness(t)
	h.asAdmin()
	if w := h.request(http.MethodPost, pathAdmin, map[string]string{"email": "alice@example.com"}); w.Code != http.StatusCreated {
		t.Fatalf("the first issuance answered %d, want 201: %s", w.Code, w.Body.String())
	}
	w := h.request(http.MethodPost, pathAdmin, map[string]string{"email": "alice@example.com"})
	if w.Code != http.StatusConflict {
		t.Fatalf("the second issuance answered %d, want 409: %s", w.Code, w.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("the 409 body %q is not JSON: %v", w.Body.String(), err)
	}
	if got, _ := body["type"].(string); got != "urn:speed:authn:invitation-pending" {
		t.Errorf("problem type = %q, want urn:speed:authn:invitation-pending", got)
	}
	if got := h.rowCount(); got != 1 {
		t.Errorf("the refused issuance left %d rows, want one", got)
	}
}

// TestConcurrentIssuancesOverTheRouteAnswerOne201AndOne409 checks that two
// concurrent requests leave one 201 and one 409.
func TestConcurrentIssuancesOverTheRouteAnswerOne201AndOne409(t *testing.T) {
	h := newHarness(t)
	h.asAdmin()

	type result struct {
		code int
		body string
	}
	results := make(chan result, 2)
	for i := 0; i < 2; i++ {
		go func() {
			w := h.request(http.MethodPost, pathAdmin, map[string]string{"email": "alice@example.com"})
			results <- result{code: w.Code, body: w.Body.String()}
		}()
	}
	var created, refused int
	for i := 0; i < 2; i++ {
		got := <-results
		switch got.code {
		case http.StatusCreated:
			created++
		case http.StatusConflict:
			refused++
			if !strings.Contains(got.body, "urn:speed:authn:invitation-pending") {
				t.Errorf("the 409 body %s does not carry invitation-pending", got.body)
			}
		default:
			t.Errorf("a concurrent issuance answered %d, want 201 or 409: %s", got.code, got.body)
		}
	}
	if created != 1 || refused != 1 {
		t.Errorf("%d issuances answered 201 and %d answered 409, want one of each", created, refused)
	}
	if got := h.rowCount(); got != 1 {
		t.Errorf("the race left %d rows, want one", got)
	}
}

// TestTheIssueRouteAnswers201OverAnExpiredInvitationAndSupersedesIt checks that
// issuing over an expired invitation is a 201 that supersedes the old row.
func TestTheIssueRouteAnswers201OverAnExpiredInvitationAndSupersedesIt(t *testing.T) {
	h := newHarness(t)
	first := h.issue(tenant, "alice@example.com")
	h.clock.advance(DefaultTTL + time.Minute)
	h.asAdmin()

	w := h.request(http.MethodPost, pathAdmin, map[string]string{"email": "alice@example.com"})
	if w.Code != http.StatusCreated {
		t.Fatalf("issuing over an expired invitation answered %d, want 201: %s", w.Code, w.Body.String())
	}
	var answered issuedAnswer
	if err := json.Unmarshal(w.Body.Bytes(), &answered); err != nil {
		t.Fatalf("decoding the answer: %v", err)
	}
	if answered.Invitation.Status != viewStatePending {
		t.Errorf("the new invitation reads %q, want pending", answered.Invitation.Status)
	}
	if got := h.rowCount(); got != 2 {
		t.Errorf("rows = %d, want the old and the new one", got)
	}
	old := h.row(first.Invitation.ID)
	if old.Status != statusPending {
		t.Errorf("the old row's stored status = %d, want pending so it still reads expired", old.Status)
	}
	if old.RevokedAt != nil {
		t.Errorf("the old row was revoked at %v, want it left as expired", old.RevokedAt)
	}
	if old.SupersededAt == nil {
		t.Fatal("the old row carries no superseded_at")
	}
	if !old.SupersededAt.Equal(h.clock.read()) {
		t.Errorf("superseded_at = %v, want the issuance instant %v", old.SupersededAt, h.clock.read())
	}

	h.mounter.AsAnonymous()
	if w := h.request(http.MethodGet, "/api/v1/auth/invitations/"+first.Token, nil); w.Code != http.StatusOK {
		t.Fatalf("the old token answered %d, want 200 with its expired state: %s", w.Code, w.Body.String())
	} else {
		var view struct {
			State string `json:"state"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &view); err != nil {
			t.Fatalf("the old token's body %q is not JSON: %v", w.Body.String(), err)
		}
		if view.State != viewStateExpired {
			t.Errorf("the old token reads %q, want expired", view.State)
		}
	}
	next := h.request(http.MethodGet, "/api/v1/auth/invitations/"+answered.Token, nil)
	if next.Code != http.StatusOK {
		t.Fatalf("the new token answered %d, want 200: %s", next.Code, next.Body.String())
	}
	var view struct {
		State string `json:"state"`
	}
	if err := json.Unmarshal(next.Body.Bytes(), &view); err != nil {
		t.Fatalf("the new token's body %q is not JSON: %v", next.Body.String(), err)
	}
	if view.State != viewStatePending {
		t.Errorf("the new token reads %q, want pending", view.State)
	}
}
