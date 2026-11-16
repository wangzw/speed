package invite

import (
	"context"
	"net/http"
	"testing"
)

// TestAResendVoidsTheOldTokenOnEveryAcceptanceFace holds that a resend's old
// token is dead everywhere it is presented: the acceptance write and the
// eligibility read both answer the bare 404, while the new token redeems the
// invitation and writes the membership.
func TestAResendVoidsTheOldTokenOnEveryAcceptanceFace(t *testing.T) {
	h := newHarness(t)
	h.proveEmail("acct-alice", "alice@example.com")
	first := h.issue(tenant, "alice@example.com")
	resent, err := h.p.Resend(context.Background(), tenant, first.Invitation.ID)
	if err != nil {
		t.Fatalf("resending: %v", err)
	}
	h.acceptAsHTTP("acct-alice", "alice@example.com")

	// The replaced token on the acceptance write.
	w := h.request(http.MethodPost, "/api/v1/auth/invitations/"+first.Token, map[string]string{})
	if w.Code != http.StatusNotFound {
		t.Fatalf("accepting with the replaced token answered %d, want 404: %s", w.Code, w.Body.String())
	}
	if w.Body.Len() != 0 {
		t.Errorf("the 404 carries the body %q, want none", w.Body.String())
	}
	if h.members.count != 0 {
		t.Errorf("a replaced token wrote %d membership(s), want none", h.members.count)
	}

	// The replaced token on the eligibility read.
	w = h.request(http.MethodGet, eligibilityPathPrefix+first.Token+"/eligibility", nil)
	if w.Code != http.StatusNotFound {
		t.Fatalf("the eligibility read of the replaced token answered %d, want 404: %s", w.Code, w.Body.String())
	}
	if w.Body.Len() != 0 {
		t.Errorf("the 404 carries the body %q, want none", w.Body.String())
	}

	// The new token is the one that redeems.
	w = h.request(http.MethodPost, "/api/v1/auth/invitations/"+resent.Token, map[string]string{})
	if w.Code != http.StatusCreated {
		t.Fatalf("accepting with the new token answered %d, want 201: %s", w.Code, w.Body.String())
	}
	if h.members.count != 1 {
		t.Errorf("memberships written = %d, want one", h.members.count)
	}
	if got := h.row(first.Invitation.ID).Status; got != statusAccepted {
		t.Errorf("the row's status = %d, want accepted", got)
	}
}

// TestTheEligibilityReadCountsDistinctTokensAgainstOneClientBound holds the
// coarse client tier on the guarded read: one client address spends a fixed
// number of eligibility probes per window whatever tokens it walks. Two tokens
// are presented because one token cannot separate the client counter from the
// token counter: the second token still has its own token allowance and is
// refused by the client bound alone. The refusal is the standard 429 with
// Retry-After.
func TestTheEligibilityReadCountsDistinctTokensAgainstOneClientBound(t *testing.T) {
	h := newHarness(t)
	h.store.set(probeTokenPath, 100)
	h.store.set(probeClientPath, 1)
	first := h.issue(tenant, "alice@example.com")
	second := h.issue(tenant, "bob@example.com")
	h.acceptAsHTTP("acct-alice", "alice@example.com")

	if w := h.request(http.MethodGet, eligibilityPathPrefix+first.Token+"/eligibility", nil); w.Code != http.StatusOK {
		t.Fatalf("the first eligibility read answered %d, want 200: %s", w.Code, w.Body.String())
	}
	w := h.request(http.MethodGet, eligibilityPathPrefix+second.Token+"/eligibility", nil)
	if w.Code != http.StatusTooManyRequests {
		t.Fatalf("the eligibility read of a second token answered %d, want 429 from the one client bound "+
			"the first token spent: %s", w.Code, w.Body.String())
	}
	if retry := w.Header().Get("Retry-After"); retry == "" || retry == "0" {
		t.Errorf("Retry-After = %q, want the seconds until the window opens", retry)
	}
}

// TestTheEligibilityAndPublicReadsShareTheClientProbeBudget holds that the two
// acceptance reads count against one client counter whichever token each
// presents: the guarded read cannot be used to get a second allowance beside the
// public one. A public read of one token and an eligibility read of another from
// the same client spend two units of a two-unit bound, and the next read is
// refused whether it is the public one or the eligibility one.
func TestTheEligibilityAndPublicReadsShareTheClientProbeBudget(t *testing.T) {
	h := newHarness(t)
	h.store.set(probeTokenPath, 100)
	h.store.set(probeClientPath, 2)
	first := h.issue(tenant, "alice@example.com")
	second := h.issue(tenant, "bob@example.com")
	view := "/api/v1/auth/invitations/" + first.Token
	eligibility := eligibilityPathPrefix + second.Token + "/eligibility"

	h.mounter.AsAnonymous()
	if w := h.request(http.MethodGet, view, nil); w.Code != http.StatusOK {
		t.Fatalf("the public read answered %d, want 200: %s", w.Code, w.Body.String())
	}
	h.acceptAsHTTP("acct-alice", "alice@example.com")
	if w := h.request(http.MethodGet, eligibility, nil); w.Code != http.StatusOK {
		t.Fatalf("the eligibility read of another token answered %d, want 200: %s", w.Code, w.Body.String())
	}
	if w := h.request(http.MethodGet, view, nil); w.Code != http.StatusTooManyRequests {
		t.Fatalf("a third read from the same client answered %d, want 429 from the shared client "+
			"counter: %s", w.Code, w.Body.String())
	}
	if w := h.request(http.MethodGet, eligibility, nil); w.Code != http.StatusTooManyRequests {
		t.Fatalf("the eligibility read after the bound was spent answered %d, want the same 429: %s",
			w.Code, w.Body.String())
	}
}
