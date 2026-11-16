package invite

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

// requireNullField asserts that one field of a decoded JSON object is null.
func requireNullField(t *testing.T, object map[string]json.RawMessage, name string) {
	t.Helper()
	raw, present := object[name]
	if !present {
		t.Errorf("the view carries no %q field", name)
		return
	}
	if string(raw) != "null" {
		t.Errorf("%s = %s, want null for an absent instant", name, raw)
	}
}

// requireUTCInstant asserts that one field of a decoded JSON object is a
// quoted string and that its wire text ends in Z.
func requireUTCInstant(t *testing.T, object map[string]json.RawMessage, name string) {
	t.Helper()
	raw, present := object[name]
	if !present {
		t.Errorf("the view carries no %q field", name)
		return
	}
	if !strings.HasSuffix(string(raw), `Z"`) {
		t.Errorf("%s = %s, want an RFC 3339 instant in UTC ending in Z", name, raw)
	}
}

// TestAnUnacceptedInvitationPublishesANullAcceptedAt holds the wire rule for an
// invitation that has not been accepted: accepted_at is null in the issue
// answer and in the list, not the zero instant.
func TestAnUnacceptedInvitationPublishesANullAcceptedAt(t *testing.T) {
	h := newHarness(t)
	h.asAdmin()

	w := h.request(http.MethodPost, pathAdmin, map[string]string{"email": "alice@example.com"})
	if w.Code != http.StatusCreated {
		t.Fatalf("issue status = %d, want 201: %s", w.Code, w.Body.String())
	}
	var issued struct {
		Invitation map[string]json.RawMessage `json:"invitation"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &issued); err != nil {
		t.Fatalf("decoding the issue answer: %v", err)
	}
	requireNullField(t, issued.Invitation, "accepted_at")

	w = h.request(http.MethodGet, pathAdmin, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("list status = %d, want 200: %s", w.Code, w.Body.String())
	}
	var page struct {
		Invitations []map[string]json.RawMessage `json:"invitations"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &page); err != nil {
		t.Fatalf("decoding the list: %v", err)
	}
	if len(page.Invitations) != 1 {
		t.Fatalf("invitations = %+v, want one", page.Invitations)
	}
	requireNullField(t, page.Invitations[0], "accepted_at")
}

// TestAnAcceptedInvitationPublishesAcceptedAtInUTC holds the wire rule for a
// present acceptance instant: it leaves as an RFC 3339 instant in UTC.
func TestAnAcceptedInvitationPublishesAcceptedAtInUTC(t *testing.T) {
	h := newHarness(t)
	issued := h.issue("tenant-a", "alice@example.com")
	if _, err := h.p.Accept(h.acceptCtx("acct-alice", "alice@example.com"), AcceptInvite{Token: issued.Token}); err != nil {
		t.Fatalf("accepting: %v", err)
	}
	h.asAdmin()

	w := h.request(http.MethodGet, pathAdmin, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("list status = %d, want 200: %s", w.Code, w.Body.String())
	}
	var page struct {
		Invitations []map[string]json.RawMessage `json:"invitations"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &page); err != nil {
		t.Fatalf("decoding the list: %v", err)
	}
	if len(page.Invitations) != 1 {
		t.Fatalf("invitations = %+v, want one", page.Invitations)
	}
	requireUTCInstant(t, page.Invitations[0], "accepted_at")
}
