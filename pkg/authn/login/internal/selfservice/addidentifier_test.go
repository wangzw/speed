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

type pendingEmail struct {
	Account    string `json:"account"`
	Email      string `json:"email"`
	CodeDigest string `json:"code_digest"`
	ExpiresAt  int64  `json:"expires_at"`
}

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

func emailHarness(t *testing.T) *harness {
	t.Helper()
	return newHarness(t, func(c *config) { c.email = true })
}

func addBody(kind, value string) string {
	body, _ := json.Marshal(map[string]string{"kind": kind, "value": value})
	return string(body)
}

func verifyBody(code string) string {
	body, _ := json.Marshal(map[string]string{"code": code})
	return string(body)
}

// TestAUsernameIsAddedAsTheAccountOwnAssertion holds that a login name is written at once as the account's own assertion, since it names no contact.
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

// TestAnEmailIsAddedOnlyAfterItsCodeComesBack holds that the add answers pending and only the matching code writes the address.
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
	if len(h.sessions.rotated) != 0 || len(h.sessions.revoked) != 0 {
		t.Errorf("proving an address settled the sessions: rotated %v, revoked %v", h.sessions.rotated, h.sessions.revoked)
	}
}

// TestProvingTheAccountsOwnUnprovedEmailSucceeds holds that its own code proves the account's unproved address in place.
func TestProvingTheAccountsOwnUnprovedEmailSucceeds(t *testing.T) {
	h := emailHarness(t)
	h.accounts.lookup["ada@example.com"] = account
	w := h.post("/identifiers", addBody("email", "ada@example.com"))
	if w.Code != http.StatusOK {
		t.Fatalf("the add answered %d %s, want 200", w.Code, w.Body)
	}
	if len(h.email.sent) != 1 {
		t.Fatalf("SendCode was called %d times, want once", len(h.email.sent))
	}
	w = h.post("/identifiers/verify", verifyBody(h.email.sent[0].code))
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
}

// TestAStepUpRefusalLeavesAnEmailCodeReplayable holds that the pending record is spent only when the address is written, not when the code matches.
func TestAStepUpRefusalLeavesAnEmailCodeReplayable(t *testing.T) {
	h := emailHarness(t)
	h.activeFactor()
	h.as()
	if w := h.post("/identifiers", addBody("email", "ada@example.com")); w.Code != http.StatusOK || problemState(t, w) != "pending" {
		t.Fatalf("the add answered %d %s, want 200 pending", w.Code, w.Body)
	}
	code := h.email.sent[0].code

	w := h.post("/identifiers/verify", verifyBody(code))
	if w.Code != http.StatusUnauthorized || problemType(t, w) != "insufficient_user_authentication" {
		t.Fatalf("the stale verify answered %d %s, want 401 insufficient_user_authentication", w.Code, w.Body)
	}
	if _, _, ok := pendingEmailKey(h); !ok {
		t.Fatal("the step-up refusal dropped the pending record, so the code cannot be replayed")
	}

	h.as(localMulti(time.Minute)...)
	w = h.post("/identifiers/verify", verifyBody(code))
	if w.Code != http.StatusOK || problemState(t, w) != "added" {
		t.Fatalf("the replay answered %d %s, want 200 added", w.Code, w.Body)
	}
	if len(h.admin.added) != 1 {
		t.Fatalf("the address was written %d times, want once", len(h.admin.added))
	}
}

// TestARepeatedIdentifierRowStaysAConflict holds that a duplicate row written around the reservation table still answers 409 conflict.
func TestARepeatedIdentifierRowStaysAConflict(t *testing.T) {
	h := emailHarness(t)
	h.post("/identifiers", addBody("email", "ada@example.com"))
	h.admin.addErr = authn.ErrAccountExists
	w := h.post("/identifiers/verify", verifyBody(h.email.sent[0].code))
	if w.Code != http.StatusConflict || problemType(t, w) != "conflict" {
		t.Fatalf("the verify answered %d %s, want 409 conflict", w.Code, w.Body)
	}
}

// TestAnAddressTheAccountModuleCannotNormalizeIsStillSent holds that a normalization refusal is not an outage and the write step refuses it.
func TestAnAddressTheAccountModuleCannotNormalizeIsStillSent(t *testing.T) {
	h := emailHarness(t)
	h.signup.pendingErr = authn.ErrInvalidIdentifier
	w := h.post("/identifiers", addBody("email", "ada@example.com"))
	if w.Code != http.StatusOK || problemState(t, w) != "pending" {
		t.Fatalf("the add answered %d %s, want 200 pending", w.Code, w.Body)
	}
	if len(h.email.sent) != 1 {
		t.Fatalf("SendCode was called %d times, want once", len(h.email.sent))
	}
}

// TestTheEmailBranchRefusesWhenTheDeploymentHasNoChannel holds that an assembly without a channel answers unavailable rather than writing an unproved address.
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

// TestTheCodeNeverComesBackInTheAnswerOrTheLog holds that the answer carries only state and durations, and the code reaches neither body nor log.
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

// TestAResendWithinTheIntervalIsThrottled holds that a second code inside the interval is a 429.
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

// TestAResendFromAnotherSessionIsThrottledByAddress holds that the resend
// window is held against the address rather than the session.
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

// TestAWrongCodeIsRejectedAndCounted holds login-failed, no identifier written and the attempt counted.
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

// TestTheRecordIsSpentAfterTheAttemptsAreUsedUp holds that once attempts are spent the next verify is the same login-failed.
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
	if w.Code != http.StatusUnauthorized || problemType(t, w) != "login-failed" {
		t.Fatalf("answer = %d %s, want 401 login-failed once the record is spent", w.Code, w.Body)
	}
}

// TestAWrongCodeAdvancesTheCountByAnAtomicIncrement holds the store's atomic increment behind the attempt bound.
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

// TestAnAddressOfAnotherAccountIsAConflict holds that a reserved address answers 409 email-taken.
func TestAnAddressOfAnotherAccountIsAConflict(t *testing.T) {
	h := emailHarness(t)
	h.post("/identifiers", addBody("email", "ada@example.com"))
	code := h.email.sent[0].code
	h.admin.addErr = authn.ErrContactTaken
	w := h.post("/identifiers/verify", verifyBody(code))
	if w.Code != http.StatusConflict || problemType(t, w) != "email-taken" {
		t.Fatalf("answer = %d %s, want 409 email-taken", w.Code, w.Body)
	}
}

// TestAVerificationWithNoPendingRecordIsLoginFailed holds that a verify without an add answers like a wrong code, revealing nothing.
func TestAVerificationWithNoPendingRecordIsLoginFailed(t *testing.T) {
	h := emailHarness(t)
	w := h.post("/identifiers/verify", verifyBody("123456"))
	if w.Code != http.StatusUnauthorized || problemType(t, w) != "login-failed" {
		t.Fatalf("answer = %d %s, want 401 login-failed", w.Code, w.Body)
	}
}

// TestAnEmailAddThatCannotBeSentFails holds that an undeliverable code ends as unavailable and keeps no pending record.
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
