package invite

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"
)

// TestASupersededUnexpiredRowReadsExpiredOnTheLink holds the public read's
// judgement of a row a later invitation replaced.
func TestASupersededUnexpiredRowReadsExpiredOnTheLink(t *testing.T) {
	h := newHarness(t)
	issued := h.issue(tenant, "alice@example.com")
	h.supersede(issued.Invitation.ID)

	h.mounter.AsAnonymous()
	w := h.request(http.MethodGet, "/api/v1/auth/invitations/"+issued.Token, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", w.Code, w.Body.String())
	}
	var answered AuthnInviteView
	if err := json.Unmarshal(w.Body.Bytes(), &answered); err != nil {
		t.Fatalf("decoding the answer %q: %v", w.Body.String(), err)
	}
	if answered.State != viewStateExpired {
		t.Errorf("state = %q, want %q: a superseded row grants nothing", answered.State, viewStateExpired)
	}
	if answered.Invitation != nil {
		t.Errorf("the expired answer carries %+v, want the state alone", answered.Invitation)
	}
}

// TestASupersededUnexpiredRowReadsExpiredInTheList checks that the same row is
// listed as expired and kept out of the pending filter.
func TestASupersededUnexpiredRowReadsExpiredInTheList(t *testing.T) {
	h := newHarness(t)
	pending := h.issue(tenant, "pending@example.com")
	h.clock.advance(time.Minute)
	superseded := h.issue(tenant, "superseded@example.com")
	h.supersede(superseded.Invitation.ID)
	h.asAdmin()

	w := h.request(http.MethodGet, pathAdmin, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", w.Code, w.Body.String())
	}
	var whole AuthnInvitePage
	if err := json.Unmarshal(w.Body.Bytes(), &whole); err != nil {
		t.Fatalf("decoding the page %q: %v", w.Body.String(), err)
	}
	if len(whole.Invitations) != 2 {
		t.Fatalf("the page holds %d invitations, want the pending one and the superseded one", len(whole.Invitations))
	}
	if got := whole.Invitations[0]; got.ID != pending.Invitation.ID || got.State != viewStatePending {
		t.Errorf("the first row = %+v, want the unexpired pending invitation %s in the pending group",
			got, pending.Invitation.ID)
	}
	if got := whole.Invitations[1]; got.ID != superseded.Invitation.ID || got.State != viewStateExpired {
		t.Errorf("the second row = %+v, want the superseded invitation %s reading expired",
			got, superseded.Invitation.ID)
	}

	cases := []struct {
		status string
		wantID string
	}{
		{status: "pending", wantID: pending.Invitation.ID},
		{status: "expired", wantID: superseded.Invitation.ID},
	}
	for _, c := range cases {
		w := h.request(http.MethodGet, pathAdmin+"?status="+c.status, nil)
		if w.Code != http.StatusOK {
			t.Fatalf("status=%s answered %d: %s", c.status, w.Code, w.Body.String())
		}
		var filtered AuthnInvitePage
		if err := json.Unmarshal(w.Body.Bytes(), &filtered); err != nil {
			t.Fatalf("decoding the %s page %q: %v", c.status, w.Body.String(), err)
		}
		if len(filtered.Invitations) != 1 || filtered.Invitations[0].ID != c.wantID {
			t.Errorf("status=%s listed %+v, want only %s", c.status, filtered.Invitations, c.wantID)
		}
	}
}
