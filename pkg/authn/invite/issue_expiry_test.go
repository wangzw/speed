package invite

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"
)

// TestAnExplicitZeroDeadlineIsRefused holds the issue request's two ways of
// naming no deadline apart from a malformed one: an explicit zero instant is a
// value outside the domain, so it answers 400 and writes nothing.
func TestAnExplicitZeroDeadlineIsRefused(t *testing.T) {
	h := newHarness(t)
	h.asAdmin()
	w := h.request(http.MethodPost, pathAdmin, map[string]any{
		"email":      "alice@example.com",
		"expires_at": time.Time{}.Format(time.RFC3339),
	})
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 for the zero instant: %s", w.Code, w.Body.String())
	}
	var problem struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &problem); err != nil {
		t.Fatalf("decoding the problem: %v", err)
	}
	if problem.Type != "urn:speed:authn:bad-request" {
		t.Errorf("problem type = %q, want the bad-request type", problem.Type)
	}
	if got := h.rowCount(); got != 0 {
		t.Errorf("the refused issuance wrote %d rows, want none", got)
	}
}

// TestANullDeadlineTakesTheDefaultLifetime holds the counterpart: a null field
// is an omission, so the configured lifetime still applies.
func TestANullDeadlineTakesTheDefaultLifetime(t *testing.T) {
	h := newHarness(t)
	h.asAdmin()
	w := h.request(http.MethodPost, pathAdmin, map[string]any{
		"email":      "alice@example.com",
		"expires_at": nil,
	})
	if w.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201 for a null deadline: %s", w.Code, w.Body.String())
	}
	var answered issuedAnswer
	if err := json.Unmarshal(w.Body.Bytes(), &answered); err != nil {
		t.Fatalf("decoding the answer: %v", err)
	}
	if want := h.clock.read().Add(DefaultTTL); !answered.Invitation.ExpiresAt.Equal(want) {
		t.Errorf("expires_at = %v, want the configured default %v", answered.Invitation.ExpiresAt, want)
	}
}
