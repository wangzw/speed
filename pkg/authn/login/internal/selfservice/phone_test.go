package selfservice_test

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/authn/authntest"
	"github.com/vislake/speed/pkg/authn/login/internal/selfservice"
)

const phoneNumber = "+8613800000000"

func sha256hex(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

func keyOf(s authn.SessionID) string { return "authn:phone:" + sha256hex(string(s)) }

func (h *harness) startPhone() {
	h.t.Helper()
	if w := h.post("/phone", `{"phone":"`+phoneNumber+`"}`); w.Code != http.StatusOK {
		h.t.Fatalf("POST /phone = %d %s", w.Code, w.Body)
	}
}

// TestStartingAVerificationSendsTheCodeAndKeepsTheRecord holds the send, the record's fields and expiry, and the answer's two durations.
func TestStartingAVerificationSendsTheCodeAndKeepsTheRecord(t *testing.T) {
	h := newHarness(t)
	w := h.post("/phone", `{"phone":"`+phoneNumber+`"}`)
	if w.Code != http.StatusOK {
		t.Fatalf("POST /phone = %d %s, want 200", w.Code, w.Body)
	}
	var body map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if len(body) != 2 || body["expires_in"] != float64(300) || body["resend_after"] != float64(60) {
		t.Errorf("answer = %v, want exactly expires_in 300 and resend_after 60", body)
	}
	if want := (authn.SendCode{Purpose: authn.SMSPurposeEnroll, Phone: phoneNumber, Account: account, Deliver: true, Source: "192.0.2.1"}); len(h.sms.sent) != 1 || h.sms.sent[0] != want {
		t.Errorf("Send was called with %+v, want %+v", h.sms.sent, want)
	}
	key := keyOf(session)
	if got := h.kv.keys(); !slices.Equal(got, []string{key}) {
		t.Fatalf("keys = %v, want only %s", got, key)
	}
	var rec map[string]string
	if err := json.Unmarshal(h.kv.values[key], &rec); err != nil {
		t.Fatal(err)
	}
	if len(rec) != 3 || rec["account"] != string(account) || rec["phone"] != phoneNumber || rec["challenge_id"] != "challenge-1" {
		t.Errorf("record = %v", rec)
	}
	if ttl := h.kv.ttls[key]; ttl != 5*time.Minute {
		t.Errorf("record ttl = %v, want the code's five minutes", ttl)
	}
}

// TestThePhoneSendCarriesTheRequestAddress holds that the enroll send carries the request's source address.
func TestThePhoneSendCarriesTheRequestAddress(t *testing.T) {
	h := newHarness(t)
	w := h.do(request{method: http.MethodPost, path: "/phone", body: `{"phone":"` + phoneNumber + `"}`, remote: "203.0.113.7:4567"})
	if w.Code != http.StatusOK {
		t.Fatalf("POST /phone = %d %s", w.Code, w.Body)
	}
	if len(h.sms.sent) != 1 {
		t.Fatalf("Send was called %d times, want once", len(h.sms.sent))
	}
	if got := h.sms.sent[0].Source; got != "203.0.113.7" {
		t.Errorf("the enroll send carried source %q, want the request's address 203.0.113.7", got)
	}
}

// TestTheAnswerNeverCarriesTheChallenge holds that the challenge identifier stays in the store and out of the answer.
func TestTheAnswerNeverCarriesTheChallenge(t *testing.T) {
	h := newHarness(t)
	w := h.post("/phone", `{"phone":"`+phoneNumber+`"}`)
	if strings.Contains(w.Body.String(), "challenge") || strings.Contains(w.Header().Get("Location"), "challenge") {
		t.Errorf("the answer carries the challenge: %s", w.Body)
	}
	if strings.Contains(h.logs.String(), "challenge-1") {
		t.Errorf("the challenge is in the log: %s", h.logs)
	}
}

// TestTheDurationsFollowTheSettings holds that the record lives as long as the
// code-ttl setting says and that both numbers in the answer are the settings'.
func TestTheDurationsFollowTheSettings(t *testing.T) {
	cases := []struct {
		name         string
		code, resend time.Duration
		wantTTL      time.Duration
		wantResend   float64
	}{
		{"both set", 2 * time.Minute, 90 * time.Second, 2 * time.Minute, 90},
		{"a code ttl outside the domain is the default", 30 * time.Minute, 0, 5 * time.Minute, 60},
		{"a resend interval outside the domain is the default", 0, time.Second, 5 * time.Minute, 60},
		{"a fractional second is rounded up", 0, 45*time.Second + time.Millisecond, 5 * time.Minute, 46},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			h := newHarness(t)
			if c.code > 0 {
				h.settings.values["authn.sms.code-ttl"] = c.code
			}
			if c.resend > 0 {
				h.settings.values["authn.sms.resend-interval"] = c.resend
			}
			w := h.post("/phone", `{"phone":"`+phoneNumber+`"}`)
			var body map[string]float64
			if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
				t.Fatal(err)
			}
			if got := h.kv.ttls[keyOf(session)]; got != c.wantTTL {
				t.Errorf("record ttl = %v, want %v", got, c.wantTTL)
			}
			if body["expires_in"] != c.wantTTL.Seconds() || body["resend_after"] != c.wantResend {
				t.Errorf("answer = %v, want expires_in %v resend_after %v", body, c.wantTTL.Seconds(), c.wantResend)
			}
		})
	}
}

// TestTheDurationSettingsAdmitTheirExactBounds holds that a value at either bound is inside its domain and used as written.
func TestTheDurationSettingsAdmitTheirExactBounds(t *testing.T) {
	cases := []struct {
		name         string
		code, resend time.Duration
		wantTTL      time.Duration
		wantResend   float64
	}{
		{"code-ttl at its low bound", time.Minute, 60 * time.Second, time.Minute, 60},
		{"code-ttl at its high bound", 10 * time.Minute, 60 * time.Second, 10 * time.Minute, 60},
		{"resend-interval at its low bound", 5 * time.Minute, 30 * time.Second, 5 * time.Minute, 30},
		{"resend-interval at its high bound", 5 * time.Minute, 10 * time.Minute, 5 * time.Minute, 600},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			h := newHarness(t)
			h.settings.values["authn.sms.code-ttl"] = c.code
			h.settings.values["authn.sms.resend-interval"] = c.resend
			w := h.post("/phone", `{"phone":"`+phoneNumber+`"}`)
			var body map[string]float64
			if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
				t.Fatal(err)
			}
			if got := h.kv.ttls[keyOf(session)]; got != c.wantTTL {
				t.Errorf("record ttl = %v, want %v: a value at the bound is inside the domain", got, c.wantTTL)
			}
			if body["resend_after"] != c.wantResend {
				t.Errorf("resend_after = %v, want %v", body["resend_after"], c.wantResend)
			}
		})
	}
}

// TestTheRecordDoesNotOutliveTheChallenge holds that the send's elapsed time is subtracted from the record's life.
func TestTheRecordDoesNotOutliveTheChallenge(t *testing.T) {
	var elapsed time.Duration
	h := newHarness(t, func(c *config) {
		c.mutateDeps = func(d *selfservice.Deps) { d.Clock = func() time.Time { return now.Add(elapsed) } }
	})
	h.sms.onSend = func() { elapsed = 7 * time.Second }
	h.startPhone()
	if got, want := h.kv.ttls[keyOf(session)], 5*time.Minute-7*time.Second; got != want {
		t.Errorf("record ttl = %v, want %v", got, want)
	}

	elapsed = 0
	h2 := newHarness(t, func(c *config) {
		c.mutateDeps = func(d *selfservice.Deps) { d.Clock = func() time.Time { return now.Add(elapsed) } }
	})
	h2.sms.onSend = func() { elapsed = 5 * time.Minute }
	if w := h2.post("/phone", `{"phone":"`+phoneNumber+`"}`); w.Code != http.StatusServiceUnavailable {
		t.Errorf("POST /phone = %d, want 503", w.Code)
	}
	if len(h2.kv.keys()) != 0 {
		t.Error("a record of an expired code was stored")
	}
}

// TestASecondCodeReplacesTheFirstOfTheSameSession holds that a session has one record, so the last number asked for wins.
func TestASecondCodeReplacesTheFirstOfTheSameSession(t *testing.T) {
	h := newHarness(t)
	h.startPhone()
	if w := h.post("/phone", `{"phone":"+8613900000000"}`); w.Code != http.StatusOK {
		t.Fatalf("second POST /phone = %d", w.Code)
	}
	if got := h.kv.keys(); len(got) != 1 {
		t.Fatalf("keys = %v, want one record for the session", got)
	}
	var rec phoneRecordView
	if err := json.Unmarshal(h.kv.values[keyOf(session)], &rec); err != nil {
		t.Fatal(err)
	}
	if rec.Phone != "+8613900000000" || rec.ChallengeID != "challenge-2" {
		t.Errorf("record = %+v, want the second request's", rec)
	}
}

type phoneRecordView struct {
	Account     string `json:"account"`
	Phone       string `json:"phone"`
	ChallengeID string `json:"challenge_id"`
}

// TestTheRecordsOfTwoSessionsAreApart holds that one session cannot overwrite or verify another's record.
func TestTheRecordsOfTwoSessionsAreApart(t *testing.T) {
	h := newHarness(t)
	h.startPhone()
	h.as(authntest.WithSession("sess-2"))
	if w := h.post("/phone", `{"phone":"+8613900000000"}`); w.Code != http.StatusOK {
		t.Fatalf("POST /phone = %d", w.Code)
	}
	want := []string{keyOf("sess-1"), keyOf("sess-2")}
	slices.Sort(want)
	got := h.kv.keys()
	slices.Sort(got)
	if !slices.Equal(got, want) {
		t.Fatalf("keys = %v, want %v", got, want)
	}
	var first phoneRecordView
	if err := json.Unmarshal(h.kv.values[keyOf("sess-1")], &first); err != nil || first.Phone != phoneNumber {
		t.Errorf("the first session's record = %+v (%v), was touched by the second's request", first, err)
	}

	// A session that never asked has no record to verify, whatever another one holds.
	h.as(authntest.WithSession("sess-3"))
	h.log.reset()
	w := h.post("/phone/verify", `{"code":"123456"}`)
	if w.Code != http.StatusUnauthorized || problemType(t, w) != "flow-invalid" {
		t.Errorf("verify from a third session = %d %s, want 401 flow-invalid", w.Code, w.Body)
	}
	if h.log.count("sms.Verify") != 0 {
		t.Error("a session verified a challenge another session started")
	}
}

// TestANumberAnotherAccountHoldsIsSentACodeLikeAnyOther holds that the route does not look the number up and answers in the same shape.
func TestANumberAnotherAccountHoldsIsSentACodeLikeAnyOther(t *testing.T) {
	h := newHarness(t)
	h.accounts.identifiers = []authn.Identifier{{ID: "x", Kind: authn.IdentifierPhone, Value: phoneNumber}}
	held := h.post("/phone", `{"phone":"`+phoneNumber+`"}`)
	free := h.post("/phone", `{"phone":"+8613900000000"}`)
	if held.Code != free.Code || held.Body.String() != free.Body.String() {
		t.Errorf("the answers differ: %d %s and %d %s", held.Code, held.Body, free.Code, free.Body)
	}
	if h.log.count("accounts.Lookup") != 0 || h.log.count("accounts.Identifiers") != 0 {
		t.Errorf("the number was looked up: %v", h.log.all())
	}
	if len(h.sms.sent) != 2 || !h.sms.sent[0].Deliver || !h.sms.sent[1].Deliver {
		t.Errorf("sends = %+v, want two deliveries", h.sms.sent)
	}
}

// TestANumberThatIsNotE164IsRefusedBeforeAnythingIsSent holds that every non-strict spelling is a 400 with no send and no record.
func TestANumberThatIsNotE164IsRefusedBeforeAnythingIsSent(t *testing.T) {
	for _, phone := range []string{
		"", "13800000000", "8613800000000", "+0123456789", "+123456", "+1234567890123456",
		" +8613800000000", "+8613800000000 ", "+86 138 0000 0000", "+86-138-0000-0000", "+8613800000000x",
		"+8613800000000\n", "+８６13800000000",
	} {
		h := newHarness(t)
		body, _ := json.Marshal(map[string]string{"phone": phone})
		w := h.post("/phone", string(body))
		if w.Code != http.StatusBadRequest {
			t.Errorf("phone %q = %d, want 400", phone, w.Code)
		}
		if h.log.count("sms.Send") != 0 || len(h.kv.keys()) != 0 {
			t.Errorf("phone %q was sent a code or stored", phone)
		}
	}
	for _, phone := range []string{"+1234567", "+123456789012345", "+8613800000000"} {
		h := newHarness(t)
		if w := h.post("/phone", `{"phone":"`+phone+`"}`); w.Code != http.StatusOK {
			t.Errorf("phone %q = %d, want 200", phone, w.Code)
		}
	}
}

// TestTheSendersRefusalsAreAnswered holds each answer the sender can give, and
// that none leaves a record.
func TestTheSendersRefusalsAreAnswered(t *testing.T) {
	cases := []struct {
		err   error
		code  int
		kind  string
		retry string
	}{
		{authn.ErrUnsupportedRegion, http.StatusBadRequest, "unsupported-region", ""},
		{&authn.ThrottledError{RetryAfter: 42 * time.Second}, http.StatusTooManyRequests, "throttled", "42"},
		{authn.ErrThrottled, http.StatusTooManyRequests, "throttled", ""},
		{authn.ErrUnavailable, http.StatusServiceUnavailable, "unavailable", ""},
		{errors.New("a defect of the caller"), http.StatusServiceUnavailable, "unavailable", ""},
	}
	for _, c := range cases {
		t.Run(c.err.Error(), func(t *testing.T) {
			h := newHarness(t)
			h.sms.sendErr = c.err
			w := h.post("/phone", `{"phone":"`+phoneNumber+`"}`)
			if w.Code != c.code || problemType(t, w) != c.kind || w.Header().Get("Retry-After") != c.retry {
				t.Fatalf("answer = %d %s Retry-After %q, want %d %q %q", w.Code, w.Body, w.Header().Get("Retry-After"), c.code, c.kind, c.retry)
			}
			if len(h.kv.keys()) != 0 {
				t.Error("a record was stored for a code that was not sent")
			}
		})
	}
}

// TestAStoreThatCannotBeReadIsA503BeforeAnythingIsSent holds that an unreadable store fails both routes closed with no send or verify.
func TestAStoreThatCannotBeReadIsA503BeforeAnythingIsSent(t *testing.T) {
	h := newHarness(t)
	h.kv.getErr = errors.New("redis: connection refused")
	for _, r := range []request{
		{method: http.MethodPost, path: "/phone", body: `{"phone":"` + phoneNumber + `"}`},
		{method: http.MethodPost, path: "/phone/verify", body: `{"code":"123456"}`},
	} {
		w := h.do(r)
		if w.Code != http.StatusServiceUnavailable || problemType(t, w) != "unavailable" {
			t.Errorf("%s with the store down = %d %s, want 503 unavailable", r.path, w.Code, w.Body)
		}
		if strings.Contains(w.Body.String(), "redis") {
			t.Errorf("the cause is in the answer: %s", w.Body)
		}
	}
	for _, name := range []string{"sms.Send", "sms.Verify", "throttle.Check", "throttle.Fail", "throttle.Succeed", "admin.AddIdentifier", "sessions.Rotate"} {
		if h.log.count(name) != 0 {
			t.Errorf("%s was called with the store down", name)
		}
	}
}

// TestAStoreThatCannotBeWrittenIsA503 holds that a sent code without a record answers 503, never 200.
func TestAStoreThatCannotBeWrittenIsA503(t *testing.T) {
	h := newHarness(t)
	h.kv.setErr = errors.New("redis: read-only replica")
	if w := h.post("/phone", `{"phone":"`+phoneNumber+`"}`); w.Code != http.StatusServiceUnavailable {
		t.Fatalf("answer = %d, want 503", w.Code)
	}
}

// TestACompletedVerificationBindsTheNumber holds the call order: record read, back-off check, pass, proved identifier written, record deleted.
func TestACompletedVerificationBindsTheNumber(t *testing.T) {
	h := newHarness(t)
	h.startPhone()
	h.log.reset()
	w := h.post("/phone/verify", `{"code":"123456"}`)
	if w.Code != http.StatusNoContent {
		t.Fatalf("POST /phone/verify = %d %s, want 204", w.Code, w.Body)
	}
	want := []string{
		"kv.Get", "throttle.Check",
		// What the proof would change is decided before the code is compared: the
		// account's identifiers and factors say whether the number is an active
		// SMS factor's target, and that consequence asks for freshness. Deciding
		// it first is what leaves a step-up refusal with its challenge unspent.
		"accounts.Identifiers", "mfa.Factors",
		"sms.Verify", "throttle.Succeed",
		"admin.AddIdentifier", "kv.Delete",
	}
	if got := h.log.names(); !slices.Equal(got, want) {
		t.Fatalf("calls = %v, want %v", got, want)
	}
	if got := h.sms.verified; len(got) != 1 || got[0] != (verifyCall{"challenge-1", "123456"}) {
		t.Errorf("Verify was called with %+v, want the challenge of the record", got)
	}
	wantKey := throttleCall{authn.ScopePhone, sha256hex(phoneNumber)}
	if len(h.throttle.checks) != 1 || h.throttle.checks[0] != wantKey || len(h.throttle.succeeds) != 1 || h.throttle.succeeds[0] != wantKey {
		t.Errorf("checks %+v succeeds %+v, want the phone scope under the SHA-256 of the number", h.throttle.checks, h.throttle.succeeds)
	}
	if len(h.throttle.fails) != 0 {
		t.Error("a code that passed was counted as a failure")
	}
	if got := h.admin.added; len(got) != 1 || got[0] != (addCall{account, authn.IdentifierPhone, phoneNumber, authn.Verification{Via: "sms-code"}}) {
		t.Errorf("AddIdentifier was called with %+v", got)
	}
	if h.log.count("sessions.Rotate") != 0 || h.log.count("sessions.RevokeAccount") != 0 || h.log.count("sessions.SetCookie") != 0 {
		t.Errorf("proving a number settled the sessions: %v", h.log.names())
	}
	if len(h.kv.keys()) != 0 {
		t.Errorf("the record is still there: %v", h.kv.keys())
	}
	if _, ok := cookieValue(w, "sid"); ok {
		t.Error("the answer set a session cookie for a change that rotates no session")
	}
}

// TestACodeVerifiesOnce holds that the record is gone after the success, so the
// same code cannot be replayed through the session.
func TestACodeVerifiesOnce(t *testing.T) {
	h := newHarness(t)
	h.startPhone()
	h.post("/phone/verify", `{"code":"123456"}`)
	w := h.post("/phone/verify", `{"code":"123456"}`)
	if w.Code != http.StatusUnauthorized || problemType(t, w) != "flow-invalid" {
		t.Errorf("second verify = %d %s, want 401 flow-invalid", w.Code, w.Body)
	}
	if len(h.sms.verified) != 1 || len(h.admin.added) != 1 {
		t.Errorf("verified %d times and bound %d, want once each", len(h.sms.verified), len(h.admin.added))
	}
}

// TestAStepUpRefusalLeavesTheCodeReplayable holds that the fresh-authentication refusal precedes consuming the single-use challenge.
func TestAStepUpRefusalLeavesTheCodeReplayable(t *testing.T) {
	h := newHarness(t)
	h.mfa.factors = []authn.FactorRecord{
		{ID: "factor-sms", Account: account, Kind: authn.FactorSMS, Status: authn.FactorActive},
		{ID: "factor-totp", Account: account, Kind: authn.FactorTOTP, Status: authn.FactorActive},
	}
	h.startPhone()

	w := h.post("/phone/verify", `{"code":"123456"}`)
	if w.Code != http.StatusUnauthorized || problemType(t, w) != "insufficient_user_authentication" {
		t.Fatalf("the stale verify answered %d %s, want 401 insufficient_user_authentication", w.Code, w.Body)
	}
	if h.log.count("sms.Verify") != 0 {
		t.Fatalf("the step-up refusal compared the code and spent the challenge: %v", h.log.all())
	}
	if len(h.kv.keys()) != 1 {
		t.Fatalf("the step-up refusal dropped the pending record: %v", h.kv.keys())
	}

	h.as(localMulti(time.Minute)...)
	if w := h.post("/phone/verify", `{"code":"123456"}`); w.Code != http.StatusNoContent {
		t.Fatalf("the replay answered %d %s, want 204", w.Code, w.Body)
	}
	if len(h.admin.added) != 1 {
		t.Fatalf("the number was bound %d times, want once", len(h.admin.added))
	}
}

// TestVerifyWithoutAMatchingRecordIsFlowInvalid holds that a missing, undecodable or foreign record is a 401 without calling the back-off or verifier.
func TestVerifyWithoutAMatchingRecordIsFlowInvalid(t *testing.T) {
	cases := []struct {
		name  string
		store func(h *harness)
	}{
		{"no record", func(*harness) {}},
		{"a record that is not JSON", func(h *harness) { h.kv.values[keyOf(session)] = []byte("not json") }},
		{"a record of another account", func(h *harness) {
			h.kv.values[keyOf(session)] = []byte(`{"account":"someone-else","phone":"` + phoneNumber + `","challenge_id":"challenge-9"}`)
		}},
		{"a record with no challenge", func(h *harness) {
			h.kv.values[keyOf(session)] = []byte(`{"account":"acct-1","phone":"` + phoneNumber + `"}`)
		}},
		{"a record with no number", func(h *harness) {
			h.kv.values[keyOf(session)] = []byte(`{"account":"acct-1","challenge_id":"challenge-9"}`)
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			h := newHarness(t)
			c.store(h)
			w := h.post("/phone/verify", `{"code":"123456"}`)
			if w.Code != http.StatusUnauthorized || problemType(t, w) != "flow-invalid" {
				t.Fatalf("answer = %d %s, want 401 flow-invalid", w.Code, w.Body)
			}
			want := []string{"kv.Get"}
			if got := h.log.names(); !slices.Equal(got, want) {
				t.Errorf("calls = %v, want %v", got, want)
			}
		})
	}
}

// TestANumberInBackOffIsNotOfferedAGuess holds that a waiting number is a 429 before the verifier and counts nothing.
func TestANumberInBackOffIsNotOfferedAGuess(t *testing.T) {
	h := newHarness(t)
	h.startPhone()
	h.throttle.wait = 30 * time.Second
	w := h.post("/phone/verify", `{"code":"123456"}`)
	if w.Code != http.StatusTooManyRequests || problemType(t, w) != "throttled" || w.Header().Get("Retry-After") != "30" {
		t.Fatalf("answer = %d %s Retry-After %q, want 429 throttled 30", w.Code, w.Body, w.Header().Get("Retry-After"))
	}
	if h.log.count("sms.Verify") != 0 || h.log.count("throttle.Fail") != 0 || h.log.count("throttle.Succeed") != 0 {
		t.Errorf("calls = %v, want no verification and no count", h.log.all())
	}
	if len(h.kv.keys()) != 1 {
		t.Error("the record was dropped by a refusal that is only a wait")
	}
}

// TestABackOffThatCannotBeReadIsA503 holds that a back-off that cannot be read is
// not read as none: the code is not compared.
func TestABackOffThatCannotBeReadIsA503(t *testing.T) {
	h := newHarness(t)
	h.startPhone()
	h.throttle.checkErr = errors.New("db: down")
	w := h.post("/phone/verify", `{"code":"123456"}`)
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("answer = %d, want 503", w.Code)
	}
	if h.log.count("sms.Verify") != 0 {
		t.Error("the code was compared with the back-off unread")
	}
}

// TestAWrongCodeIsCountedAgainstTheNumber holds the phone-scope count, no binding or revocation, login-failed, and the record kept.
func TestAWrongCodeIsCountedAgainstTheNumber(t *testing.T) {
	h := newHarness(t)
	h.startPhone()
	h.sms.verifyErr = authn.ErrInvalidCredentials
	w := h.post("/phone/verify", `{"code":"000000"}`)
	if w.Code != http.StatusUnauthorized || problemType(t, w) != "login-failed" {
		t.Fatalf("answer = %d %s, want 401 login-failed", w.Code, w.Body)
	}
	if len(h.throttle.fails) != 1 || h.throttle.fails[0] != (throttleCall{authn.ScopePhone, sha256hex(phoneNumber)}) {
		t.Errorf("fails = %+v", h.throttle.fails)
	}
	if h.log.count("throttle.Succeed") != 0 || h.log.count("admin.AddIdentifier") != 0 || h.log.count("sessions.Rotate") != 0 || h.log.count("sessions.RevokeAccount") != 0 {
		t.Errorf("calls = %v: a wrong code bound, cleared or revoked", h.log.all())
	}
	if len(h.kv.keys()) != 1 {
		t.Error("the record was dropped by a wrong code")
	}
	if len(h.bus.published()) != 0 {
		t.Errorf("events = %v, want none for a failure that did not start a back-off", h.bus.published())
	}
}

// TestAFailureThatStartsABackOffIsAnnounced holds that the engaging failure is published under the phone scope.
func TestAFailureThatStartsABackOffIsAnnounced(t *testing.T) {
	h := newHarness(t)
	h.startPhone()
	h.sms.verifyErr = authn.ErrInvalidCredentials
	h.throttle.engaged = true
	h.post("/phone/verify", `{"code":"000000"}`)
	events := h.bus.published()
	if len(events) != 1 || events[0].Type != authn.EventThrottleEngaged {
		t.Fatalf("events = %+v, want authn.throttle.engaged", events)
	}
	if p, ok := events[0].Payload.(authn.ThrottleEngaged); !ok || p.Scope != "phone" || p.AccountID != "" {
		t.Errorf("payload = %+v, want scope phone and no account", events[0].Payload)
	}
	if !slices.Equal(h.engaged, []authn.ThrottleScope{authn.ScopePhone}) {
		t.Errorf("the counter was told %v", h.engaged)
	}
}

// TestAFailureThatCannotBeCountedIsA503 holds that an unrecorded rejection is not answered as a wrong code.
func TestAFailureThatCannotBeCountedIsA503(t *testing.T) {
	h := newHarness(t)
	h.startPhone()
	h.sms.verifyErr = authn.ErrInvalidCredentials
	h.throttle.failErr = errors.New("db: down")
	if w := h.post("/phone/verify", `{"code":"000000"}`); w.Code != http.StatusServiceUnavailable {
		t.Fatalf("answer = %d, want 503", w.Code)
	}
}

// TestAnAnswerThatCouldNotBeHadIsNeitherAFailureNorASuccess holds that an unavailable verifier counts nothing either way.
func TestAnAnswerThatCouldNotBeHadIsNeitherAFailureNorASuccess(t *testing.T) {
	for _, err := range []error{authn.ErrUnavailable, errors.New("db: down")} {
		h := newHarness(t)
		h.startPhone()
		h.sms.verifyErr = err
		w := h.post("/phone/verify", `{"code":"123456"}`)
		if w.Code != http.StatusServiceUnavailable {
			t.Errorf("with %v the answer = %d, want 503", err, w.Code)
		}
		if h.log.count("throttle.Fail") != 0 || h.log.count("throttle.Succeed") != 0 || h.log.count("admin.AddIdentifier") != 0 {
			t.Errorf("with %v calls = %v", err, h.log.all())
		}
	}
}

// TestANumberAnotherAccountHoldsIsAConflictAtVerify holds that the conflict surfaces after a correct code as a 409 revoking no session.
func TestANumberAnotherAccountHoldsIsAConflictAtVerify(t *testing.T) {
	h := newHarness(t)
	h.startPhone()
	h.admin.addErr = authn.ErrAccountExists
	w := h.post("/phone/verify", `{"code":"123456"}`)
	if w.Code != http.StatusConflict || problemType(t, w) != "conflict" {
		t.Fatalf("answer = %d %s, want 409 conflict", w.Code, w.Body)
	}
	if h.log.count("sessions.Rotate") != 0 || h.log.count("sessions.RevokeAccount") != 0 {
		t.Error("a binding that failed rotated or revoked sessions")
	}
	if len(h.kv.keys()) != 0 {
		t.Error("the record of a spent challenge is still there")
	}
	if h.log.count("throttle.Succeed") != 1 {
		t.Error("a correct code was not cleared from the back-off")
	}
}

// TestABindingThatFailsOtherwiseIsAnswered holds the other answers of the write.
func TestABindingThatFailsOtherwiseIsAnswered(t *testing.T) {
	for _, c := range []struct {
		err  error
		code int
	}{
		{authn.ErrInvalidIdentifier, http.StatusBadRequest},
		{authn.ErrUsernameReserved, http.StatusBadRequest},
		{authn.ErrUnavailable, http.StatusServiceUnavailable},
		{authn.ErrAccountNotFound, http.StatusUnauthorized},
	} {
		h := newHarness(t)
		h.startPhone()
		h.admin.addErr = c.err
		if w := h.post("/phone/verify", `{"code":"123456"}`); w.Code != c.code {
			t.Errorf("with %v the answer = %d, want %d", c.err, w.Code, c.code)
		}
	}
}

// TestHousekeepingThatFailsDoesNotFailAVerifiedNumber holds that a failed back-off clear or record delete is logged and does not undo the binding.
func TestHousekeepingThatFailsDoesNotFailAVerifiedNumber(t *testing.T) {
	h := newHarness(t)
	h.startPhone()
	h.throttle.succeedErr = errors.New("db: down")
	h.kv.delErr = errors.New("redis: down")
	if w := h.post("/phone/verify", `{"code":"123456"}`); w.Code != http.StatusNoContent {
		t.Fatalf("answer = %d, want 204", w.Code)
	}
	if len(h.admin.added) != 1 {
		t.Error("the number was not bound")
	}
	if !strings.Contains(h.logs.String(), "cannot be cleared") || !strings.Contains(h.logs.String(), "could not be deleted") {
		t.Errorf("the failures were not logged: %s", h.logs)
	}
}

// TestACodeThatCannotBeOneIsRefusedWithoutBeingCounted holds that an empty or too-long code and a refused body reach neither store nor verifier.
func TestACodeThatCannotBeOneIsRefusedWithoutBeingCounted(t *testing.T) {
	cases := []struct {
		name   string
		body   string
		header map[string]string
		code   int
	}{
		{"empty", `{"code":""}`, nil, http.StatusBadRequest},
		{"missing", `{}`, nil, http.StatusBadRequest},
		{"long", `{"code":"` + strings.Repeat("1", 33) + `"}`, nil, http.StatusBadRequest},
		{"unknown field", `{"code":"123456","phone":"+8613900000000"}`, nil, http.StatusBadRequest},
		{"form", `code=123456`, map[string]string{"Content-Type": "application/x-www-form-urlencoded"}, http.StatusUnsupportedMediaType},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			h := newHarness(t)
			h.startPhone()
			h.log.reset()
			w := h.do(request{method: http.MethodPost, path: "/phone/verify", body: c.body, header: c.header})
			if w.Code != c.code {
				t.Fatalf("answer = %d %s, want %d", w.Code, w.Body, c.code)
			}
			for _, name := range []string{"kv.Get", "throttle.Check", "sms.Verify", "throttle.Fail"} {
				if h.log.count(name) != 0 {
					t.Errorf("%s was called", name)
				}
			}
		})
	}
}

// TestACodeAtTheLengthBoundPassesTheShapeGate holds that a maximum-length code is checked and one character over is refused.
func TestACodeAtTheLengthBoundPassesTheShapeGate(t *testing.T) {
	const bound = 32
	t.Run("at the bound", func(t *testing.T) {
		h := newHarness(t)
		h.startPhone()
		h.log.reset()
		w := h.post("/phone/verify", `{"code":"`+strings.Repeat("1", bound)+`"}`)
		if w.Code == http.StatusBadRequest {
			t.Fatalf("a code of the maximum length was refused as malformed: %d %s", w.Code, w.Body)
		}
		if h.log.count("sms.Verify") == 0 {
			t.Errorf("a code of the maximum length did not reach the verifier: calls %v", h.log.all())
		}
	})
	t.Run("one over the bound", func(t *testing.T) {
		h := newHarness(t)
		h.startPhone()
		h.log.reset()
		w := h.post("/phone/verify", `{"code":"`+strings.Repeat("1", bound+1)+`"}`)
		if w.Code != http.StatusBadRequest {
			t.Errorf("a code over the maximum answered %d, want 400", w.Code)
		}
		if h.log.count("sms.Verify") != 0 {
			t.Errorf("an over-long code reached the verifier: calls %v", h.log.all())
		}
	})
}

// TestTheClientCannotChooseTheNumberItVerifies holds that the number written is
// the record's and that a request that names another is refused by the decoder.
func TestTheClientCannotChooseTheNumberItVerifies(t *testing.T) {
	h := newHarness(t)
	h.startPhone()
	h.post("/phone/verify", `{"code":"123456","phone":"+8613900000000"}`)
	if len(h.admin.added) != 0 {
		t.Fatal("a number named in the verify request was bound")
	}
	h.post("/phone/verify", `{"code":"123456"}`)
	if len(h.admin.added) != 1 || h.admin.added[0].value != phoneNumber {
		t.Errorf("bound %+v, want the number of the record", h.admin.added)
	}
}

// TestNoCodeIsInAnAnswerOrALogLine holds that the code a user typed and the
// challenge are in neither, on the paths that fail.
func TestNoCodeIsInAnAnswerOrALogLine(t *testing.T) {
	for _, err := range []error{authn.ErrInvalidCredentials, authn.ErrUnavailable, errors.New("db: down")} {
		h := newHarness(t)
		h.startPhone()
		h.sms.verifyErr = err
		w := h.post("/phone/verify", `{"code":"735913"}`)
		for _, secret := range []string{"735913", "challenge-1"} {
			if strings.Contains(w.Body.String(), secret) || strings.Contains(h.logs.String(), secret) {
				t.Errorf("with %v, %q is in the answer %q or the log %q", err, secret, w.Body, h.logs)
			}
		}
	}
}
