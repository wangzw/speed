package invite

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/vislake/speed/pkg/authn/authntest"
)

const eligibilityPathPrefix = "/api/v1/auth/invitations/"

// TestTheEligibilityReadAnswersForTheSignedInCallerAlone holds the guarded read
// the invitation page opens with: an anonymous request is 401, a signed-in
// caller who proved the invited address reads pending with the proof, one who
// did not reads pending without it, and the answer carries no address, inviter
// or organization. An unknown token is the bare 404 every acceptance route
// answers.
func TestTheEligibilityReadAnswersForTheSignedInCallerAlone(t *testing.T) {
	h := newHarness(t)
	issued := h.issue("tenant-a", "alice@example.com")

	// No identity: the gateway refuses the guarded route before the handler.
	h.mounter.AsAnonymous()
	if w := h.request(http.MethodGet, eligibilityPathPrefix+issued.Token+"/eligibility", nil); w.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous eligibility status = %d, want 401: %s", w.Code, w.Body.String())
	}

	// A signed-in caller who proved the invited address.
	h.acceptAsHTTP("acct-alice", "alice@example.com")
	w := h.request(http.MethodGet, eligibilityPathPrefix+issued.Token+"/eligibility", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("eligibility status = %d, want 200: %s", w.Code, w.Body.String())
	}
	var body struct {
		State       string `json:"state"`
		EmailProved bool   `json:"email_proved"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("the body %q is not JSON: %v", w.Body.String(), err)
	}
	if body.State != viewStatePending || !body.EmailProved {
		t.Errorf("answer = %+v, want pending with the proof", body)
	}
	for _, leaked := range []string{"alice@example.com", string(adminID), `"email"`, `"invited_by_name"`, `"tenant"`} {
		if strings.Contains(w.Body.String(), leaked) {
			t.Errorf("the eligibility answer carries %q: %s", leaked, w.Body.String())
		}
	}

	// A signed-in caller who holds another address.
	h.mounter.As(authntest.WithAccount("acct-bob"))
	w = h.request(http.MethodGet, eligibilityPathPrefix+issued.Token+"/eligibility", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("eligibility status for another caller = %d, want 200: %s", w.Code, w.Body.String())
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("the body %q is not JSON: %v", w.Body.String(), err)
	}
	if body.State != viewStatePending || body.EmailProved {
		t.Errorf("answer = %+v, want pending without the proof", body)
	}

	// A token that points at no invitation.
	w = h.request(http.MethodGet, eligibilityPathPrefix+"spd_inv_unknown/eligibility", nil)
	if w.Code != http.StatusNotFound {
		t.Fatalf("eligibility status for an unknown token = %d, want 404: %s", w.Code, w.Body.String())
	}
}

// TestTheEligibilityReadAnswersEveryStateWithoutLeakingTheInvitation holds the
// same answer for the three states a token can reach after the pending one: it
// reports the state and whether the caller proved the invited address, and it
// carries neither an address nor the inviter nor the organization.
func TestTheEligibilityReadAnswersEveryStateWithoutLeakingTheInvitation(t *testing.T) {
	h := newHarness(t)
	h.acceptAsHTTP("acct-alice", "alice@example.com")

	read := func(token string) (string, bool, string) {
		t.Helper()
		w := h.request(http.MethodGet, eligibilityPathPrefix+token+"/eligibility", nil)
		if w.Code != http.StatusOK {
			t.Fatalf("eligibility status = %d, want 200: %s", w.Code, w.Body.String())
		}
		var body struct {
			State       string `json:"state"`
			EmailProved bool   `json:"email_proved"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
			t.Fatalf("the body %q is not JSON: %v", w.Body.String(), err)
		}
		return body.State, body.EmailProved, w.Body.String()
	}

	pending := h.issue("tenant-a", "alice@example.com")
	state, proved, _ := read(pending.Token)
	if state != viewStatePending || !proved {
		t.Errorf("pending answer = %q/%v, want pending with the proof", state, proved)
	}

	if w := h.request(http.MethodPost, "/api/v1/auth/invitations/"+pending.Token, map[string]string{}); w.Code != http.StatusCreated {
		t.Fatalf("accepting answered %d, want 201: %s", w.Code, w.Body.String())
	}
	state, proved, raw := read(pending.Token)
	if state != viewStateAccepted || !proved {
		t.Errorf("accepted answer = %q/%v, want accepted with the proof", state, proved)
	}
	for _, leaked := range []string{"alice@example.com", `"email"`, `"invited_by_name"`, `"tenant"`} {
		if strings.Contains(raw, leaked) {
			t.Errorf("the accepted eligibility answer carries %q: %s", leaked, raw)
		}
	}

	revoked := h.issue("tenant-a", "bob@example.com")
	h.asAdmin()
	if w := h.request(http.MethodDelete, pathAdmin+"/"+revoked.Invitation.ID, nil); w.Code != http.StatusNoContent {
		t.Fatalf("revoking answered %d, want 204: %s", w.Code, w.Body.String())
	}
	h.acceptAsHTTP("acct-alice", "alice@example.com")
	state, proved, raw = read(revoked.Token)
	if state != viewStateRevoked || proved {
		t.Errorf("revoked answer = %q/%v, want revoked without the proof", state, proved)
	}
	for _, leaked := range []string{"bob@example.com", `"email"`, `"invited_by_name"`, `"tenant"`} {
		if strings.Contains(raw, leaked) {
			t.Errorf("the revoked eligibility answer carries %q: %s", leaked, raw)
		}
	}

	expired := h.issue("tenant-a", "carol@example.com")
	h.clock.advance(DefaultTTL + time.Minute)
	state, proved, raw = read(expired.Token)
	if state != viewStateExpired || proved {
		t.Errorf("expired answer = %q/%v, want expired without the proof", state, proved)
	}
	for _, leaked := range []string{"carol@example.com", `"email"`, `"invited_by_name"`, `"tenant"`} {
		if strings.Contains(raw, leaked) {
			t.Errorf("the expired eligibility answer carries %q: %s", leaked, raw)
		}
	}
}

// TestTheEligibilityReadChargesTheProbeBudget holds that the guarded read spends
// the same per-token probe budget as the public one, so an authenticated account
// cannot walk tokens faster than an anonymous visitor, and the refusal comes
// before the token is looked up.
func TestTheEligibilityReadChargesTheProbeBudget(t *testing.T) {
	h := newHarness(t)
	h.store.set(probeTokenPath, 1)
	h.store.set(probeClientPath, 100)
	issued := h.issue("tenant-a", "alice@example.com")
	h.acceptAsHTTP("acct-alice", "alice@example.com")

	if w := h.request(http.MethodGet, eligibilityPathPrefix+issued.Token+"/eligibility", nil); w.Code != http.StatusOK {
		t.Fatalf("the first eligibility read answered %d, want 200: %s", w.Code, w.Body.String())
	}
	real := h.request(http.MethodGet, eligibilityPathPrefix+issued.Token+"/eligibility", nil)
	if real.Code != http.StatusTooManyRequests {
		t.Fatalf("the second eligibility read answered %d, want 429", real.Code)
	}
	const unknownToken = "spd_inv_unknown"
	if w := h.request(http.MethodGet, eligibilityPathPrefix+unknownToken+"/eligibility", nil); w.Code != http.StatusNotFound {
		t.Fatalf("the first eligibility read of an unknown token answered %d, want 404: %s", w.Code, w.Body.String())
	}
	unknown := h.request(http.MethodGet, eligibilityPathPrefix+unknownToken+"/eligibility", nil)
	if unknown.Code != http.StatusTooManyRequests {
		t.Fatalf("the second eligibility read of an unknown token answered %d, want the same 429: a spent "+
			"budget must not tell a caller whether the token exists", unknown.Code)
	}
	if real.Body.String() != unknown.Body.String() {
		t.Errorf("the throttled answer to a real token is %q and to an unknown token %q; they must read alike",
			real.Body.String(), unknown.Body.String())
	}
}
