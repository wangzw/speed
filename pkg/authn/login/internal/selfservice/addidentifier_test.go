package selfservice_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/authn/authntest"
)

// pendingEmail mirrors the record the email branch keeps in the store, so a test
// can read it back without the handler's unexported type.
type pendingEmail struct {
	Account    string `json:"account"`
	Email      string `json:"email"`
	CodeDigest string `json:"code_digest"`
	ExpiresAt  int64  `json:"expires_at"`
}

// pendingEmailKey is the one key of the pending email verification in the store,
// or "" when there is none.
func pendingEmailKey(h *harness) (string, pendingEmail, bool) {
	for _, key := range h.kv.keys() {
		if !strings.HasPrefix(key, "authn:email:") {
			continue
		}
		var rec pendingEmail
		if json.Unmarshal(h.kv.values[key], &rec) != nil {
			continue
		}
		return key, rec, true
	}
	return "", pendingEmail{}, false
}

// pendingAttempts is the failed-attempt count kept against a pending
// verification, read off the count's own key, or 0 when nothing is counted.
func pendingAttempts(h *harness) float64 {
	for _, key := range h.kv.keys() {
		if !strings.HasPrefix(key, "authn:email-attempts:") {
			continue
		}
		v, err := strconv.ParseFloat(string(h.kv.values[key]), 64)
		if err != nil {
			return -1
		}
		return v
	}
	return 0
}

// emailHarness is a harness with the email branch switched on.
func emailHarness(t *testing.T) *harness {
	t.Helper()
	return newHarness(t, func(c *config) { c.email = true })
}

// addBody is the POST /identifiers body for a kind and a value.
func addBody(kind, value string) string {
	body, _ := json.Marshal(map[string]string{"kind": kind, "value": value})
	return string(body)
}

// verifyBody is the POST /identifiers/verify body for a code.
func verifyBody(code string) string {
	body, _ := json.Marshal(map[string]string{"code": code})
	return string(body)
}

// TestAUsernameIsAddedAsTheAccountOwnAssertion holds the ruling that a login
// name is written the moment the caller asks for it, recorded as the account's
// own assertion and not as a proof: a username names no contact address, so
// there is no ownership to establish.
func TestAUsernameIsAddedAsTheAccountOwnAssertion(t *testing.T) {
	h := newHarness(t)
	w := h.post("/identifiers", addBody("username", "Ada-2"))
	if w.Code != http.StatusOK {
		t.Fatalf("answer = %d %s, want 200", w.Code, w.Body)
	}
	if got := problemState(t, w); got != "added" {
		t.Errorf("state = %q, want added", got)
	}
	if len(h.admin.added) != 1 {
		t.Fatalf("AddIdentifier was called %d times, want once", len(h.admin.added))
	}
	got := h.admin.added[0]
	if got.account != account || got.kind != authn.IdentifierUsername || got.value != "Ada-2" ||
		got.via != (authn.Verification{Via: authn.VerifiedViaSelf}) {
		t.Errorf("AddIdentifier(%+v), want the account's own username assertion", got)
	}
	if len(h.sessions.rotated) != 1 || len(h.sessions.revoked) != 1 {
		t.Errorf("a username add did not settle the sessions: rotated %v, revoked %v", h.sessions.rotated, h.sessions.revoked)
	}
}

// TestAnEmailIsAddedOnlyAfterItsCodeComesBack holds the whole loop: an add sends
// a code and answers pending, nothing is written yet, and only the matching code
// writes the address, with the code proof.
func TestAnEmailIsAddedOnlyAfterItsCodeComesBack(t *testing.T) {
	h := emailHarness(t)
	w := h.post("/identifiers", addBody("email", "Ada@Example.COM"))
	if w.Code != http.StatusOK {
		t.Fatalf("the add answered %d %s, want 200", w.Code, w.Body)
	}
	if got := problemState(t, w); got != "pending" {
		t.Fatalf("state = %q, want pending", got)
	}
	if len(h.admin.added) != 0 {
		t.Fatalf("the add wrote an identifier before the code was checked: %+v", h.admin.added)
	}
	if len(h.email.sent) != 1 {
		t.Fatalf("SendCode was called %d times, want once", len(h.email.sent))
	}
	sent := h.email.sent[0]
	if sent.to != "ada@example.com" {
		t.Errorf("the code went to %q, want the lowered address", sent.to)
	}
	if len(sent.code) != 6 || strings.Trim(sent.code, "0123456789") != "" {
		t.Errorf("the code is %q, want six digits", sent.code)
	}
	if _, _, ok := pendingEmailKey(h); !ok {
		t.Fatal("no pending record was kept")
	}

	w = h.post("/identifiers/verify", verifyBody(sent.code))
	if w.Code != http.StatusOK {
		t.Fatalf("the verify answered %d %s, want 200", w.Code, w.Body)
	}
	if got := problemState(t, w); got != "added" {
		t.Errorf("state = %q, want added", got)
	}
	if len(h.admin.added) != 1 {
		t.Fatalf("AddIdentifier was called %d times, want once", len(h.admin.added))
	}
	got := h.admin.added[0]
	if got.kind != authn.IdentifierEmail || got.value != "ada@example.com" ||
		got.via != (authn.Verification{Via: authn.VerifiedViaEmailCode}) {
		t.Errorf("AddIdentifier(%+v), want the address with the email-code proof", got)
	}
	if _, _, ok := pendingEmailKey(h); ok {
		t.Error("the pending record was not deleted after the code was accepted")
	}
}

// TestTheEmailBranchRefusesWhenTheDeploymentHasNoChannel holds that an assembly
// with no email channel cannot prove an address, so it answers the email branch
// as unavailable rather than writing an unproven address.
func TestTheEmailBranchRefusesWhenTheDeploymentHasNoChannel(t *testing.T) {
	h := newHarness(t, func(c *config) { c.noSMS = true })
	w := h.post("/identifiers", addBody("email", "ada@example.com"))
	if w.Code != http.StatusServiceUnavailable || problemType(t, w) != "unavailable" {
		t.Fatalf("answer = %d %s, want 503 unavailable", w.Code, w.Body)
	}
	if len(h.admin.added) != 0 {
		t.Errorf("an address was written with no channel: %+v", h.admin.added)
	}
}

// TestAnUnknownIdentifierKindIsRefused holds that the route names a closed set:
// a phone is not added here, it goes through the SMS routes.
func TestAnUnknownIdentifierKindIsRefused(t *testing.T) {
	h := newHarness(t)
	for _, kind := range []string{"phone", "", "other"} {
		w := h.post("/identifiers", addBody(kind, "+8613800000000"))
		if w.Code != http.StatusBadRequest || problemType(t, w) != "bad-request" {
			t.Errorf("kind %q answered %d %s, want 400 bad-request", kind, w.Code, w.Body)
		}
	}
	if len(h.admin.added) != 0 {
		t.Errorf("an unknown kind wrote an identifier: %+v", h.admin.added)
	}
}

// TestTheCodeNeverComesBackInTheAnswerOrTheLog holds that the answer of an add
// carries only the state and the two durations, and that the code reaches
// neither the body nor the log.
func TestTheCodeNeverComesBackInTheAnswerOrTheLog(t *testing.T) {
	h := emailHarness(t)
	w := h.post("/identifiers", addBody("email", "ada@example.com"))
	code := h.email.sent[0].code
	if strings.Contains(w.Body.String(), code) {
		t.Errorf("the answer carries the code: %s", w.Body)
	}
	if strings.Contains(h.logs.String(), code) {
		t.Errorf("the code reached the log: %s", h.logs)
	}
}

// TestAResendWithinTheIntervalIsThrottled holds that a second code asked for
// inside the interval is a 429, so the route cannot be turned into a flood of
// messages to one address.
func TestAResendWithinTheIntervalIsThrottled(t *testing.T) {
	h := emailHarness(t)
	if w := h.post("/identifiers", addBody("email", "ada@example.com")); w.Code != http.StatusOK {
		t.Fatalf("the first add answered %d", w.Code)
	}
	w := h.post("/identifiers", addBody("email", "ada@example.com"))
	if w.Code != http.StatusTooManyRequests || problemType(t, w) != "throttled" {
		t.Fatalf("the second add answered %d %s, want 429 throttled", w.Code, w.Body)
	}
	if w.Header().Get("Retry-After") == "" {
		t.Error("the 429 carries no Retry-After")
	}
	if len(h.email.sent) != 1 {
		t.Errorf("a throttled resend still sent: %d messages", len(h.email.sent))
	}
}

// TestAResendAfterTheIntervalSendsAgain holds the other side of the interval: a
// code asked for once it has passed goes out.
func TestAResendAfterTheIntervalSendsAgain(t *testing.T) {
	h := emailHarness(t)
	if w := h.post("/identifiers", addBody("email", "ada@example.com")); w.Code != http.StatusOK {
		t.Fatalf("the first add answered %d", w.Code)
	}
	h.clockNow = h.clockNow.Add(2 * time.Minute)
	if w := h.post("/identifiers", addBody("email", "ada@example.com")); w.Code != http.StatusOK {
		t.Fatalf("the resend answered %d %s, want 200", w.Code, w.Body)
	}
	if len(h.email.sent) != 2 {
		t.Errorf("SendCode was called %d times, want two", len(h.email.sent))
	}
}

// TestAResendFromAnotherSessionIsThrottledByAddress holds that the resend window
// is held against the address rather than the session. A second session naming
// the same address within the interval is a 429, so the route cannot be turned
// into a flood of messages to one address by opening another session.
func TestAResendFromAnotherSessionIsThrottledByAddress(t *testing.T) {
	h := emailHarness(t)
	if w := h.post("/identifiers", addBody("email", "ada@example.com")); w.Code != http.StatusOK {
		t.Fatalf("the first add answered %d", w.Code)
	}
	h.as(authntest.WithSession("sess-2"))
	w := h.post("/identifiers", addBody("email", "Ada@Example.com"))
	if w.Code != http.StatusTooManyRequests || problemType(t, w) != "throttled" {
		t.Fatalf("a second session's add answered %d %s, want 429 throttled", w.Code, w.Body)
	}
	if len(h.email.sent) != 1 {
		t.Errorf("a throttled resend still sent: %d messages", len(h.email.sent))
	}
}

// TestAWrongCodeIsRejectedAndCounted holds that a code that does not match is
// login-failed, that no identifier is written, and that the attempt is counted
// against the pending record.
func TestAWrongCodeIsRejectedAndCounted(t *testing.T) {
	h := emailHarness(t)
	h.post("/identifiers", addBody("email", "ada@example.com"))
	real := h.email.sent[0].code
	wrong := "000000"
	if wrong == real {
		wrong = "111111"
	}
	w := h.post("/identifiers/verify", verifyBody(wrong))
	if w.Code != http.StatusUnauthorized || problemType(t, w) != "login-failed" {
		t.Fatalf("answer = %d %s, want 401 login-failed", w.Code, w.Body)
	}
	if len(h.admin.added) != 0 {
		t.Errorf("a wrong code wrote an identifier: %+v", h.admin.added)
	}
	if _, _, ok := pendingEmailKey(h); !ok {
		t.Fatal("the pending record was dropped on a wrong code")
	}
	if got := pendingAttempts(h); got != 1 {
		t.Errorf("attempts = %v, want 1", got)
	}
}

// TestTheRecordIsSpentAfterTheAttemptsAreUsedUp holds that a pending code cannot
// be guessed over its lifetime: once the attempts are used up the record is gone
// and the next verify is flow-invalid, so the caller has to ask for another code.
func TestTheRecordIsSpentAfterTheAttemptsAreUsedUp(t *testing.T) {
	h := emailHarness(t)
	h.post("/identifiers", addBody("email", "ada@example.com"))
	real := h.email.sent[0].code
	wrong := "000000"
	if wrong == real {
		wrong = "111111"
	}
	for i := 0; i < 5; i++ {
		if w := h.post("/identifiers/verify", verifyBody(wrong)); w.Code != http.StatusUnauthorized {
			t.Fatalf("attempt %d answered %d %s, want 401", i+1, w.Code, w.Body)
		}
	}
	if _, _, ok := pendingEmailKey(h); ok {
		t.Fatal("the record survived the attempts being used up")
	}
	w := h.post("/identifiers/verify", verifyBody(real))
	if w.Code != http.StatusUnauthorized || problemType(t, w) != "flow-invalid" {
		t.Fatalf("answer = %d %s, want 401 flow-invalid once the record is spent", w.Code, w.Body)
	}
}

// TestAWrongCodeAdvancesTheCountByAnAtomicIncrement holds the shape the attempt
// bound needs under concurrency: a wrong code advances a count of its own
// through the store's atomic increment. A count kept as a field of the record is
// advanced by reading it, adding one and writing it back, and concurrent wrong
// codes each read the same value and write back the same one, so every count but
// one is lost and the bound no longer holds. The count therefore lives on a key
// of its own, advanced without a read-modify-write of anything.
func TestAWrongCodeAdvancesTheCountByAnAtomicIncrement(t *testing.T) {
	h := emailHarness(t)
	h.post("/identifiers", addBody("email", "ada@example.com"))
	real := h.email.sent[0].code
	wrong := "000000"
	if wrong == real {
		wrong = "111111"
	}

	increments := h.log.count("kv.IncrByFloatWithTTL")
	h.post("/identifiers/verify", verifyBody(wrong))

	if got := h.log.count("kv.IncrByFloatWithTTL") - increments; got != 1 {
		t.Errorf("the count was advanced by an atomic increment %d times, want 1", got)
	}
	if got := pendingAttempts(h); got != 1 {
		t.Errorf("the count on its own key is %v, want 1", got)
	}
}

// TestAnAddressOfAnotherAccountIsAConflict holds that the account module's
// refusal of an address another account holds reaches the client as a 409.
func TestAnAddressOfAnotherAccountIsAConflict(t *testing.T) {
	h := emailHarness(t)
	h.post("/identifiers", addBody("email", "ada@example.com"))
	code := h.email.sent[0].code
	h.admin.addErr = authn.ErrAccountExists
	w := h.post("/identifiers/verify", verifyBody(code))
	if w.Code != http.StatusConflict || problemType(t, w) != "conflict" {
		t.Fatalf("answer = %d %s, want 409 conflict", w.Code, w.Body)
	}
}

// TestAVerificationWithNoPendingRecordIsFlowInvalid holds that a verify with no
// add behind it proves nothing and says nothing about any other session's
// pending address.
func TestAVerificationWithNoPendingRecordIsFlowInvalid(t *testing.T) {
	h := emailHarness(t)
	w := h.post("/identifiers/verify", verifyBody("123456"))
	if w.Code != http.StatusUnauthorized || problemType(t, w) != "flow-invalid" {
		t.Fatalf("answer = %d %s, want 401 flow-invalid", w.Code, w.Body)
	}
}

// TestAnEmailAddThatCannotBeSentFails holds that a channel that cannot deliver
// the code ends the add as unavailable and keeps no pending record, so a code
// nobody received is never one a caller could answer.
func TestAnEmailAddThatCannotBeSentFails(t *testing.T) {
	h := emailHarness(t)
	h.email.err = authn.ErrUnavailable
	w := h.post("/identifiers", addBody("email", "ada@example.com"))
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("answer = %d %s, want 503", w.Code, w.Body)
	}
	if _, _, ok := pendingEmailKey(h); ok {
		t.Error("a pending record was kept for a code that was not sent")
	}
}

// problemState is the state field of a success answer.
func problemState(t *testing.T, w *httptest.ResponseRecorder) string {
	t.Helper()
	var body struct {
		State string `json:"state"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("the answer is not JSON: %v", err)
	}
	return body.State
}
