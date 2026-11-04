package login

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/authn/internal/mount"
	"github.com/vislake/speed/pkg/settings"
)

func decode(t *testing.T, body string) map[string]any {
	t.Helper()
	var out map[string]any
	if err := json.Unmarshal([]byte(body), &out); err != nil {
		t.Fatalf("decoding %q: %v", body, err)
	}
	return out
}

// startPassword begins a password flow and returns the flow cookie.
func startPassword(t *testing.T, r *rig) string {
	t.Helper()
	rec := r.send(http.MethodPost, "/api/v1/auth/flows", `{"method":"password"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("POST flows answered %d: %s", rec.Code, rec.Body.String())
	}
	return flowCookie(t, rec)
}

// errBoom is the cause a fake refusal carries.
var errBoom = errors.New("boom")

// TestFirstFactorPromptRefuseAndProveWalksTheStateMachine holds the first
// factor's moves: a prompt keeps the flow at the first factor and bumps the step
// number, a refusal keeps it there at the same step number and answers
// login-failed, and a proof moves it to ready.
func TestFirstFactorPromptRefuseAndProveWalksTheStateMachine(t *testing.T) {
	step := 0
	m := &fakeMethod{name: "password"}
	m.begin = func(authn.Start) (authn.Step, error) {
		return authn.StepPrompt("credentials", nil,
			authn.Field{Name: "identifier", Kind: authn.FieldText, Required: true}), nil
	}
	m.ans = func(authn.FlowState, authn.StepInput) (authn.Step, error) {
		step++
		switch step {
		case 1:
			return authn.StepRefused(errBoom), nil
		default:
			return authn.StepProven(authn.Proof{Account: "acct1", Methods: []authn.Method{authn.MethodPassword}}), nil
		}
	}
	r := newRig(t, m)
	r.allow("acct1")

	cookie := startPassword(t, r)

	refused := r.send(http.MethodPost, "/api/v1/auth/flows/answer", `{"seq":1,"step":"answer","input":{}}`, "Cookie", cookie)
	if refused.Code != http.StatusUnauthorized || refused.Header().Get("WWW-Authenticate") == "" {
		t.Fatalf("a refused answer answered %d with challenge %q, want 401 login-failed",
			refused.Code, refused.Header().Get("WWW-Authenticate"))
	}
	if !strings.Contains(refused.Header().Get("WWW-Authenticate"), "Bearer") ||
		strings.Contains(refused.Header().Get("WWW-Authenticate"), "Basic") {
		t.Errorf("the 401 challenge carries Basic or no Bearer: %q", refused.Header().Get("WWW-Authenticate"))
	}

	proven := r.send(http.MethodPost, "/api/v1/auth/flows/answer", `{"seq":1,"step":"answer","input":{}}`, "Cookie", cookie)
	if proven.Code != http.StatusOK {
		t.Fatalf("a proven answer answered %d: %s", proven.Code, proven.Body.String())
	}
	next := decode(t, proven.Body.String())["next"].(map[string]any)
	if next["kind"] != "complete" {
		t.Errorf("a proof moved to next %v, want complete", next)
	}
}

// TestARefusedFirstFactorKeepsItsStepForTheSameSequence holds the design's rule
// for a submit-class refusal: the sequence number does not move and the method's
// step and private state stay in the record, because that step is what the
// retry answers. The refusal reply carries no sequence number, so the client has
// nothing but the one it already holds.
func TestARefusedFirstFactorKeepsItsStepForTheSameSequence(t *testing.T) {
	private := []byte(`{"challenge":"abc"}`)
	attempts := 0
	var seen authn.FlowState
	m := &fakeMethod{name: "password"}
	m.begin = func(authn.Start) (authn.Step, error) {
		return authn.StepPrompt("credentials", private,
			authn.Field{Name: "password", Kind: authn.FieldPassword, Required: true}), nil
	}
	m.ans = func(f authn.FlowState, _ authn.StepInput) (authn.Step, error) {
		attempts++
		if attempts == 1 {
			return authn.StepRefused(authn.ErrInvalidCredentials), nil
		}
		seen = f
		return authn.StepProven(authn.Proof{Account: "acct1", Methods: []authn.Method{authn.MethodPassword}}), nil
	}
	r := newRig(t, m)
	r.allow("acct1")
	cookie := startPassword(t, r)

	refused := r.send(http.MethodPost, "/api/v1/auth/flows/answer", `{"seq":1,"step":"answer","input":{}}`, "Cookie", cookie)
	if refused.Code != http.StatusUnauthorized {
		t.Fatalf("a refused answer answered %d: %s", refused.Code, refused.Body.String())
	}
	if _, carried := decode(t, refused.Body.String())["seq"]; carried {
		t.Errorf("the refusal carried a sequence number: %s", refused.Body.String())
	}

	token, ok := strings.CutPrefix(cookie, "authn-flow=")
	if !ok {
		t.Fatalf("the flow cookie is not named authn-flow: %q", cookie)
	}
	held, _, err := r.p.loadFlow(t.Context(), token)
	if err != nil {
		t.Fatalf("reading the refused flow's record: %v", err)
	}
	if held.MethodStep != "credentials" || string(held.MethodState) != string(private) {
		t.Fatalf("the refusal left step %q private %q, want the prompt's %q %q",
			held.MethodStep, held.MethodState, "credentials", private)
	}

	proven := r.send(http.MethodPost, "/api/v1/auth/flows/answer", `{"seq":1,"step":"answer","input":{}}`, "Cookie", cookie)
	if proven.Code != http.StatusOK {
		t.Fatalf("the retry with the same seq answered %d: %s", proven.Code, proven.Body.String())
	}
	if seen.Step != "credentials" || string(seen.Private) != string(private) {
		t.Errorf("the retried answer reached step %q private %q, want the prompt's", seen.Step, seen.Private)
	}
}

// TestARefusedSecondFactorKeepsItsChallengeForTheSameSequence holds that a code
// the factor refuses leaves the flow at the second factor with its sequence
// number and the started challenge in place, so the corrected code answers the
// same number.
func TestARefusedSecondFactorKeepsItsChallengeForTheSameSequence(t *testing.T) {
	m := &fakeMethod{name: "password"}
	m.begin = func(authn.Start) (authn.Step, error) {
		return authn.StepProven(authn.Proof{Account: "acct1", Methods: []authn.Method{authn.MethodPassword}}), nil
	}
	r := newRig(t, m)
	r.allow("acct1")
	r.mfa.outcome = authn.MFAFactor
	r.mfa.offered = []authn.FactorRecord{{ID: "f1", Kind: authn.FactorTOTP}}

	start := r.send(http.MethodPost, "/api/v1/auth/flows", `{"method":"password"}`)
	cookie := flowCookie(t, start)
	challenge := r.send(http.MethodPost, "/api/v1/auth/flows/answer",
		`{"seq":0,"step":"factor","input":{"factor":"f1"}}`, "Cookie", cookie)
	if challenge.Code != http.StatusOK {
		t.Fatalf("starting the challenge answered %d: %s", challenge.Code, challenge.Body.String())
	}
	seq := int(decode(t, challenge.Body.String())["seq"].(float64))

	attempts := 0
	r.mfa.verify = func(string) authn.Step {
		attempts++
		if attempts == 1 {
			return authn.StepRefused(authn.ErrInvalidCredentials)
		}
		return authn.StepPassed(authn.Pass{Methods: []authn.Method{authn.MethodTOTP}})
	}

	refused := r.send(http.MethodPost, "/api/v1/auth/flows/answer",
		`{"seq":`+itoa(seq)+`,"step":"factor","input":{"factor":"f1","code":"000000"}}`, "Cookie", cookie)
	if refused.Code != http.StatusUnauthorized {
		t.Fatalf("a refused code answered %d: %s", refused.Code, refused.Body.String())
	}
	if _, carried := decode(t, refused.Body.String())["seq"]; carried {
		t.Errorf("the refusal carried a sequence number: %s", refused.Body.String())
	}

	passed := r.send(http.MethodPost, "/api/v1/auth/flows/answer",
		`{"seq":`+itoa(seq)+`,"step":"factor","input":{"factor":"f1","code":"123456"}}`, "Cookie", cookie)
	if passed.Code != http.StatusOK {
		t.Fatalf("the corrected code with the same seq answered %d: %s", passed.Code, passed.Body.String())
	}
	if next := decode(t, passed.Body.String())["next"].(map[string]any); next["kind"] != "complete" {
		t.Errorf("the corrected code moved to next %v, want complete", next)
	}
}

// TestARefusedChallengeKeepsTheOfferedFactorsForTheSameSequence holds that a
// factor whose challenge is refused leaves the flow at the second factor with
// its sequence number and the whole offered list, so the same number may choose
// another factor.
func TestARefusedChallengeKeepsTheOfferedFactorsForTheSameSequence(t *testing.T) {
	m := &fakeMethod{name: "password"}
	m.begin = func(authn.Start) (authn.Step, error) {
		return authn.StepProven(authn.Proof{Account: "acct1", Methods: []authn.Method{authn.MethodPassword}}), nil
	}
	r := newRig(t, m)
	r.allow("acct1")
	r.mfa.outcome = authn.MFAFactor
	r.mfa.offered = []authn.FactorRecord{
		{ID: "f1", Kind: authn.FactorTOTP},
		{ID: "f2", Kind: authn.FactorTOTP},
	}
	r.mfa.challenge = func(factorID string) authn.Step {
		if factorID == "f1" {
			return authn.StepRefused(authn.ErrInvalidCredentials)
		}
		return authn.StepPrompt("code", nil, authn.Field{Name: "code", Kind: authn.FieldCode, Required: true})
	}

	start := r.send(http.MethodPost, "/api/v1/auth/flows", `{"method":"password"}`)
	cookie := flowCookie(t, start)

	refused := r.send(http.MethodPost, "/api/v1/auth/flows/answer",
		`{"seq":0,"step":"factor","input":{"factor":"f1"}}`, "Cookie", cookie)
	if refused.Code != http.StatusUnauthorized {
		t.Fatalf("a refused challenge answered %d: %s", refused.Code, refused.Body.String())
	}
	if _, carried := decode(t, refused.Body.String())["seq"]; carried {
		t.Errorf("the refusal carried a sequence number: %s", refused.Body.String())
	}

	chosen := r.send(http.MethodPost, "/api/v1/auth/flows/answer",
		`{"seq":0,"step":"factor","input":{"factor":"f2"}}`, "Cookie", cookie)
	if chosen.Code != http.StatusOK {
		t.Fatalf("choosing another factor with the same seq answered %d: %s", chosen.Code, chosen.Body.String())
	}
	if got := int(decode(t, chosen.Body.String())["seq"].(float64)); got != 1 {
		t.Errorf("the chosen factor's prompt is at seq %d, want 1", got)
	}
	if r.mfa.challenges != 2 {
		t.Errorf("the factor's challenge ran %d times, want two", r.mfa.challenges)
	}
}

// TestARefusalIsAnsweredByItsCause holds the design's rule that a first factor's
// refusal is not always login-failed: a phone prefix outside the allow-list is a
// 400 unsupported-region and an identifier that cannot name an account is a 400
// bad-request, while a credential that did not hold stays a 401 login-failed.
func TestARefusalIsAnsweredByItsCause(t *testing.T) {
	cases := []struct {
		name  string
		cause error
		code  int
		typ   string
	}{
		{"a credential that did not hold", authn.ErrInvalidCredentials, http.StatusUnauthorized, "login-failed"},
		{"an identifier that cannot name an account", authn.ErrInvalidIdentifier, http.StatusBadRequest, "bad-request"},
		{"a phone prefix outside the allow-list", authn.ErrUnsupportedRegion, http.StatusBadRequest, "unsupported-region"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := &fakeMethod{name: "password"}
			m.begin = func(authn.Start) (authn.Step, error) { return authn.StepPrompt("credentials", nil), nil }
			m.ans = func(authn.FlowState, authn.StepInput) (authn.Step, error) {
				return authn.StepRefused(tc.cause), nil
			}
			r := newRig(t, m)
			r.allow("acct1")
			cookie := startPassword(t, r)

			rec := r.send(http.MethodPost, "/api/v1/auth/flows/answer", `{"seq":1,"step":"answer","input":{}}`, "Cookie", cookie)
			if rec.Code != tc.code {
				t.Fatalf("a refusal caused by %v answered %d, want %d: %s", tc.cause, rec.Code, tc.code, rec.Body.String())
			}
			if got, _ := decode(t, rec.Body.String())["type"].(string); got != "urn:speed:authn:"+tc.typ {
				t.Errorf("the refusal caused by %v wrote problem type %q, want %q", tc.cause, got, tc.typ)
			}
		})
	}
}

// TestNegativeVectorsAreRefusedBeforeTheMethodRuns holds the request checks that
// come before a method is called: a wrong step number, a step the state does not
// take, a missing or mismatched binding, an unknown field, and the wrong content
// type.
func TestNegativeVectorsAreRefusedBeforeTheMethodRuns(t *testing.T) {
	m := &fakeMethod{name: "password"}
	m.begin = func(authn.Start) (authn.Step, error) { return authn.StepPrompt("credentials", nil), nil }
	m.ans = func(authn.FlowState, authn.StepInput) (authn.Step, error) {
		return authn.StepProven(authn.Proof{Account: "acct1"}), nil
	}
	r := newRig(t, m)
	r.allow("acct1")
	cookie := startPassword(t, r)
	before := m.calls

	cases := []struct {
		name   string
		method string
		path   string
		body   string
		hdr    []string
		want   int
	}{
		{
			"wrong step number", http.MethodPost, "/api/v1/auth/flows/answer", `{"seq":99,"step":"answer","input":{}}`,
			[]string{"Cookie", cookie},
			http.StatusUnauthorized,
		},
		{
			"step the state does not take", http.MethodPost, "/api/v1/auth/flows/answer", `{"seq":1,"step":"complete","input":{}}`,
			[]string{"Cookie", cookie},
			http.StatusUnauthorized,
		},
		{"missing binding", http.MethodPost, "/api/v1/auth/flows/answer", `{"seq":1,"step":"answer","input":{}}`, nil, http.StatusUnauthorized},
		{
			"mismatched binding", http.MethodPost, "/api/v1/auth/flows/answer", `{"seq":1,"step":"answer","input":{}}`,
			[]string{"Cookie", "authn-flow=not-the-token"},
			http.StatusUnauthorized,
		},
		{
			"unknown field", http.MethodPost, "/api/v1/auth/flows/answer", `{"seq":1,"step":"answer","surprise":1}`,
			[]string{"Cookie", cookie},
			http.StatusBadRequest,
		},
		{"unknown start field", http.MethodPost, "/api/v1/auth/flows", `{"method":"password","surprise":1}`, nil, http.StatusBadRequest},
	}
	for _, c := range cases {
		rec := r.send(c.method, c.path, c.body, c.hdr...)
		if rec.Code != c.want {
			t.Errorf("%s answered %d, want %d: %s", c.name, rec.Code, c.want, rec.Body.String())
		}
	}
	if m.calls != before {
		t.Errorf("a refused request reached the method: calls went from %d to %d", before, m.calls)
	}
}

// TestWrongContentTypeIsRefused holds the 415 that comes before the body is read.
func TestWrongContentTypeIsRefused(t *testing.T) {
	m := &fakeMethod{name: "password"}
	m.begin = func(authn.Start) (authn.Step, error) { return authn.StepPrompt("credentials", nil), nil }
	r := newRig(t, m)
	rec := r.send(http.MethodPost, "/api/v1/auth/flows", `<method></method>`, "Content-Type", "text/xml")
	if rec.Code != http.StatusUnsupportedMediaType {
		t.Errorf("a text/xml body answered %d, want 415: %s", rec.Code, rec.Body.String())
	}
}

// TestMethodDefectsAreAnsweredWithoutLeakingTheCause holds the failure direction
// for a method's own faults: a panic and an over-budget call are the method's
// outage, and no answer at all is a server error.
func TestMethodDefectsAreAnsweredWithoutLeakingTheCause(t *testing.T) {
	m := &fakeMethod{name: "password"}
	m.begin = func(authn.Start) (authn.Step, error) {
		panic("the method panicked")
	}
	r := newRig(t, m)
	if rec := r.send(http.MethodPost, "/api/v1/auth/flows", `{"method":"password"}`); rec.Code != http.StatusServiceUnavailable {
		t.Errorf("a panicking method answered %d, want 503: %s", rec.Code, rec.Body.String())
	}

	none := &fakeMethod{name: "password", begin: func(authn.Start) (authn.Step, error) { return authn.Step{}, nil }}
	r2 := newRig(t, none)
	if rec := r2.send(http.MethodPost, "/api/v1/auth/flows", `{"method":"password"}`); rec.Code != http.StatusInternalServerError {
		t.Errorf("a method that gave no answer answered %d, want 500: %s", rec.Code, rec.Body.String())
	}
}

// TestKVUnavailableIsRefused holds the fail-closed direction of the key-value
// store: beginning a flow against a store that cannot be read is a 503.
func TestKVUnavailableIsRefused(t *testing.T) {
	m := &fakeMethod{name: "password"}
	m.begin = func(authn.Start) (authn.Step, error) { return authn.StepPrompt("credentials", nil), nil }
	r := newRig(t, m)
	r.kv.failing = true
	if rec := r.send(http.MethodPost, "/api/v1/auth/flows", `{"method":"password"}`); rec.Code != http.StatusServiceUnavailable {
		t.Errorf("a flow against an unavailable store answered %d, want 503: %s", rec.Code, rec.Body.String())
	}
}

// TestASecondFactorIsOfferedAndPassed holds the second-factor path: a different
// kind is offered, its challenge is started, and a pass moves the flow to ready.
func TestASecondFactorIsOfferedAndPassed(t *testing.T) {
	m := &fakeMethod{name: "password"}
	m.begin = func(authn.Start) (authn.Step, error) {
		return authn.StepProven(authn.Proof{Account: "acct1", Methods: []authn.Method{authn.MethodPassword}}), nil
	}
	r := newRig(t, m)
	r.allow("acct1")
	r.mfa.outcome = authn.MFAFactor
	r.mfa.offered = []authn.FactorRecord{{ID: "f1", Kind: authn.FactorTOTP, Label: "Authenticator"}}
	r.mfa.assurance = authn.MFAResult{Assurance: authn.AssuranceMulti}

	rec := r.send(http.MethodPost, "/api/v1/auth/flows", `{"method":"password"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("POST flows answered %d: %s", rec.Code, rec.Body.String())
	}
	cookie := flowCookie(t, rec)
	next := decode(t, rec.Body.String())["next"].(map[string]any)
	if next["kind"] != "factor" {
		t.Fatalf("a login needing a factor answered next %v, want factor", next)
	}

	challenge := r.send(http.MethodPost, "/api/v1/auth/flows/answer",
		`{"seq":0,"step":"factor","input":{"factor":"f1"}}`, "Cookie", cookie)
	if challenge.Code != http.StatusOK {
		t.Fatalf("the factor selection answered %d: %s", challenge.Code, challenge.Body.String())
	}
	if k := decode(t, challenge.Body.String())["next"].(map[string]any)["kind"]; k != "challenge" {
		t.Fatalf("the factor challenge answered next kind %v, want challenge", k)
	}

	seq := int(decode(t, challenge.Body.String())["seq"].(float64))
	passed := r.send(http.MethodPost, "/api/v1/auth/flows/answer",
		`{"seq":`+itoa(seq)+`,"step":"factor","input":{"factor":"f1","code":"123456"}}`, "Cookie", cookie)
	if passed.Code != http.StatusOK {
		t.Fatalf("the factor pass answered %d: %s", passed.Code, passed.Body.String())
	}
	if k := decode(t, passed.Body.String())["next"].(map[string]any)["kind"]; k != "complete" {
		t.Errorf("a passed factor answered next kind %v, want complete", k)
	}
}

// TestEnrollFlowCompletes holds the enrollment's terminal action: a registration
// started on an authenticated identity reaches ready, and its completion revokes
// the account's other sessions without changing the session's level.
func TestEnrollFlowCompletes(t *testing.T) {
	m := &fakeMethod{name: "password"}
	r := newRig(t, m)
	r.loginAsAuthenticated("acct1", "sid")

	start := r.send(http.MethodPost, "/api/v1/auth/flows/authenticated",
		`{"purpose":"enroll","input":{"kind":"totp"}}`)
	if start.Code != http.StatusOK {
		t.Fatalf("POST flows/authenticated answered %d: %s", start.Code, start.Body.String())
	}
	if k := decode(t, start.Body.String())["next"].(map[string]any)["kind"]; k != "complete" {
		t.Fatalf("a registration answered next kind %v, want complete", k)
	}
	cookie := flowCookie(t, start)
	seq := int(decode(t, start.Body.String())["seq"].(float64))

	done := r.send(http.MethodPost, "/api/v1/auth/flows/answer",
		`{"seq":`+itoa(seq)+`,"step":"complete"}`, "Cookie", cookie)
	if done.Code != http.StatusOK {
		t.Fatalf("the enrollment completion answered %d: %s", done.Code, done.Body.String())
	}
	if r.sessions.revokedAll == 0 {
		t.Errorf("the enrollment completion revoked no sessions")
	}
}

// TestStepUpWithASecondFactorCompletes holds the step-up: a session's factors are
// offered, a challenge is started, and a pass elevates the session.
func TestStepUpWithASecondFactorCompletes(t *testing.T) {
	m := &fakeMethod{name: "password"}
	r := newRig(t, m)
	r.loginAsAuthenticated("acct1", "sid")
	r.mfa.outcome = authn.MFAFactor
	r.mfa.offered = []authn.FactorRecord{{ID: "f1", Kind: authn.FactorTOTP, ActivatedAt: time.Now().Add(-time.Hour)}}
	r.mfa.assurance = authn.MFAResult{Assurance: authn.AssuranceMulti}

	start := r.send(http.MethodPost, "/api/v1/auth/flows/authenticated", `{"purpose":"step-up"}`)
	if start.Code != http.StatusOK {
		t.Fatalf("POST flows/authenticated answered %d: %s", start.Code, start.Body.String())
	}
	if k := decode(t, start.Body.String())["next"].(map[string]any)["kind"]; k != "factor" {
		t.Fatalf("a step-up with factors answered next kind %v, want factor", k)
	}
	cookie := flowCookie(t, start)

	challenge := r.send(http.MethodPost, "/api/v1/auth/flows/answer",
		`{"seq":0,"step":"factor","input":{"factor":"f1"}}`, "Cookie", cookie)
	if challenge.Code != http.StatusOK {
		t.Fatalf("the factor challenge answered %d: %s", challenge.Code, challenge.Body.String())
	}
	seq := int(decode(t, challenge.Body.String())["seq"].(float64))
	passed := r.send(http.MethodPost, "/api/v1/auth/flows/answer",
		`{"seq":`+itoa(seq)+`,"step":"factor","input":{"factor":"f1","code":"123456"}}`, "Cookie", cookie)
	if passed.Code != http.StatusOK {
		t.Fatalf("the factor pass answered %d: %s", passed.Code, passed.Body.String())
	}
	done := r.send(http.MethodPost, "/api/v1/auth/flows/answer",
		`{"seq":`+itoa(seq+1)+`,"step":"complete"}`, "Cookie", cookie)
	if done.Code != http.StatusOK {
		t.Fatalf("the step-up completion answered %d: %s", done.Code, done.Body.String())
	}
	if len(r.sessions.elevated) != 1 {
		t.Fatalf("the step-up elevated %d sessions, want one", len(r.sessions.elevated))
	}
	if r.sessions.elevated[0].Assurance != authn.AssuranceMulti {
		t.Errorf("the elevation is %v, want multi-factor", r.sessions.elevated[0].Assurance)
	}
}

// TestAStepUpWithOnlyFactorsRegisteredThisSessionReprovesThePassword holds that
// an account whose factors were all activated after the session authenticated
// does not have its step-up refused: the flow starts at the password and offers
// those factors as the second step once the password is proved again.
func TestAStepUpWithOnlyFactorsRegisteredThisSessionReprovesThePassword(t *testing.T) {
	m := &fakeMethod{name: "password"}
	m.begin = func(authn.Start) (authn.Step, error) {
		return authn.StepPrompt("password", nil, authn.Field{Name: "password", Kind: authn.FieldPassword, Required: true}), nil
	}
	m.ans = func(authn.FlowState, authn.StepInput) (authn.Step, error) {
		return authn.StepProven(authn.Proof{Account: "acct1", Methods: []authn.Method{authn.MethodPassword}}), nil
	}
	r := newRig(t, m)
	r.loginAsAuthenticated("acct1", "sid")
	r.allowWithPassword("acct1")
	r.mfa.outcome = authn.MFAFactor
	// A factor activated after the session's own authentication time may
	// strengthen it only once the password is proved again.
	r.mfa.offered = []authn.FactorRecord{{
		ID: "f1", Kind: authn.FactorTOTP, Status: authn.FactorActive, ActivatedAt: time.Now().Add(time.Minute),
	}}
	r.mfa.assurance = authn.MFAResult{Assurance: authn.AssuranceMulti}

	start := r.send(http.MethodPost, "/api/v1/auth/flows/authenticated", `{"purpose":"step-up"}`)
	if start.Code != http.StatusOK {
		t.Fatalf("a step-up with only factors registered this session answered %d, want 200: %s", start.Code, start.Body.String())
	}
	body := decode(t, start.Body.String())
	if body["state"] != "first" {
		t.Fatalf("the step-up state is %v, want first: the password is proved again", body["state"])
	}
	if k := body["next"].(map[string]any)["kind"]; k != "challenge" {
		t.Fatalf("the start answered next kind %v, want the password challenge", k)
	}
	cookie := flowCookie(t, start)
	seq := int(body["seq"].(float64))

	answered := r.send(http.MethodPost, "/api/v1/auth/flows/answer",
		`{"seq":`+itoa(seq)+`,"step":"answer","input":{"password":"secret"}}`, "Cookie", cookie)
	if answered.Code != http.StatusOK {
		t.Fatalf("the password answer answered %d: %s", answered.Code, answered.Body.String())
	}
	answeredBody := decode(t, answered.Body.String())
	if answeredBody["state"] != "second" {
		t.Fatalf("the flow state after the password is %v, want second: the new factor is offered", answeredBody["state"])
	}
	next := answeredBody["next"].(map[string]any)
	if k := next["kind"]; k != "factor" {
		t.Fatalf("the password answer offered next kind %v, want factor", k)
	}
	factors := next["factors"].([]any)
	if len(factors) != 1 || factors[0].(map[string]any)["id"] != "f1" {
		t.Fatalf("the offered factors are %v, want f1", factors)
	}
	seq = int(answeredBody["seq"].(float64))

	challenge := r.send(http.MethodPost, "/api/v1/auth/flows/answer",
		`{"seq":`+itoa(seq)+`,"step":"factor","input":{"factor":"f1"}}`, "Cookie", cookie)
	if challenge.Code != http.StatusOK {
		t.Fatalf("the factor challenge answered %d: %s", challenge.Code, challenge.Body.String())
	}
	seq = int(decode(t, challenge.Body.String())["seq"].(float64))
	passed := r.send(http.MethodPost, "/api/v1/auth/flows/answer",
		`{"seq":`+itoa(seq)+`,"step":"factor","input":{"factor":"f1","code":"123456"}}`, "Cookie", cookie)
	if passed.Code != http.StatusOK {
		t.Fatalf("the factor pass answered %d: %s", passed.Code, passed.Body.String())
	}
	seq = int(decode(t, passed.Body.String())["seq"].(float64))
	done := r.send(http.MethodPost, "/api/v1/auth/flows/answer",
		`{"seq":`+itoa(seq)+`,"step":"complete"}`, "Cookie", cookie)
	if done.Code != http.StatusOK {
		t.Fatalf("the step-up completion answered %d: %s", done.Code, done.Body.String())
	}
	if len(r.sessions.elevated) != 1 {
		t.Fatalf("the step-up elevated %d sessions, want one", len(r.sessions.elevated))
	}
	if r.sessions.elevated[0].Assurance != authn.AssuranceMulti {
		t.Errorf("the elevation is %v, want multi-factor", r.sessions.elevated[0].Assurance)
	}
}

// TestAStepUpWithAFactorRegisteredThisSessionHonoursTheRequestedMethod holds
// that a step-up whose only factors were registered during this session starts
// at the method the request names when the deployment takes it in: that
// method's own first step runs, not the password's, and the registered factors
// stay on offer as the second step.
func TestAStepUpWithAFactorRegisteredThisSessionHonoursTheRequestedMethod(t *testing.T) {
	password := &fakeMethod{name: "password"}
	password.begin = func(authn.Start) (authn.Step, error) {
		return authn.StepPrompt("password", nil,
			authn.Field{Name: "password", Kind: authn.FieldPassword, Required: true}), nil
	}
	sms := &fakeMethod{name: "sms"}
	sms.desc = authn.MethodDescriptor{
		Name: "sms", Kind: authn.MethodKindForm,
		Fields: []authn.Field{{Name: "phone", Kind: authn.FieldPhone, Required: true}},
	}
	sms.begin = func(authn.Start) (authn.Step, error) {
		return authn.StepPrompt("code", nil,
			authn.Field{Name: "code", Kind: authn.FieldCode, Required: true}), nil
	}
	r := newRigMethods(t, password, sms)
	r.loginAsAuthenticated("acct1", "sid")
	r.allowWithPassword("acct1")
	r.mfa.outcome = authn.MFAFactor
	r.mfa.offered = []authn.FactorRecord{{
		ID: "f1", Kind: authn.FactorTOTP, Status: authn.FactorActive,
		ActivatedAt: time.Now().Add(time.Minute),
	}}

	start := r.send(http.MethodPost, "/api/v1/auth/flows/authenticated",
		`{"purpose":"step-up","method":"sms","input":{"phone":"+14155550123"}}`)
	if start.Code != http.StatusOK {
		t.Fatalf("a step-up naming sms answered %d, want 200: %s", start.Code, start.Body.String())
	}
	body := decode(t, start.Body.String())
	if body["state"] != "first" {
		t.Fatalf("the step-up state is %v, want first", body["state"])
	}
	next := body["next"].(map[string]any)
	fields := next["fields"].([]any)
	if name := fields[0].(map[string]any)["name"]; name != "code" {
		t.Fatalf("the first step asks for %v, want the sms code challenge", name)
	}
	if sms.calls != 1 {
		t.Errorf("the sms method began %d times, want one", sms.calls)
	}
	if password.calls != 0 {
		t.Errorf("the password method began %d times, want none", password.calls)
	}
}

// TestAStepUpWithAFactorRegisteredThisSessionFallsBackToThePasswordForAnUnassembledMethod
// holds the fallback a method this deployment does not take in gets: it is not
// a bad request there, and the flow starts at the password the account holds
// again, exactly as a request that named no method does.
func TestAStepUpWithAFactorRegisteredThisSessionFallsBackToThePasswordForAnUnassembledMethod(t *testing.T) {
	m := &fakeMethod{name: "password"}
	m.begin = func(authn.Start) (authn.Step, error) {
		return authn.StepPrompt("password", nil,
			authn.Field{Name: "password", Kind: authn.FieldPassword, Required: true}), nil
	}
	r := newRig(t, m)
	r.loginAsAuthenticated("acct1", "sid")
	r.allowWithPassword("acct1")
	r.mfa.outcome = authn.MFAFactor
	r.mfa.offered = []authn.FactorRecord{{
		ID: "f1", Kind: authn.FactorTOTP, Status: authn.FactorActive,
		ActivatedAt: time.Now().Add(time.Minute),
	}}

	start := r.send(http.MethodPost, "/api/v1/auth/flows/authenticated",
		`{"purpose":"step-up","method":"carrier-pigeon"}`)
	if start.Code != http.StatusOK {
		t.Fatalf("a step-up naming an unassembled method answered %d, want 200: %s", start.Code, start.Body.String())
	}
	body := decode(t, start.Body.String())
	if body["state"] != "first" {
		t.Fatalf("the step-up state is %v, want first", body["state"])
	}
	fields := body["next"].(map[string]any)["fields"].([]any)
	if name := fields[0].(map[string]any)["name"]; name != "password" {
		t.Fatalf("the first step asks for %v, want the password challenge", name)
	}
}

// TestAStepUpNamingAPasswordTheAccountCannotProveIsRefused holds that a step-up
// whose only factors were registered during this session does not start on a
// password the account cannot prove: naming it explicitly is the same bad
// request as naming a method this deployment does not take in, refused at the
// start rather than left to fail when the password is asked for.
func TestAStepUpNamingAPasswordTheAccountCannotProveIsRefused(t *testing.T) {
	m := &fakeMethod{name: "password"}
	m.begin = func(authn.Start) (authn.Step, error) {
		return authn.StepPrompt("password", nil,
			authn.Field{Name: "password", Kind: authn.FieldPassword, Required: true}), nil
	}
	r := newRig(t, m)
	r.loginAsAuthenticated("acct1", "sid")
	r.allow("acct1") // an account row without a password credential
	r.mfa.outcome = authn.MFAFactor
	r.mfa.offered = []authn.FactorRecord{{
		ID: "f1", Kind: authn.FactorTOTP, Status: authn.FactorActive,
		ActivatedAt: time.Now().Add(time.Minute),
	}}

	start := r.send(http.MethodPost, "/api/v1/auth/flows/authenticated",
		`{"purpose":"step-up","method":"password"}`)
	if start.Code != http.StatusBadRequest {
		t.Fatalf("a step-up naming a password the account cannot prove answered %d, want 400: %s",
			start.Code, start.Body.String())
	}
	if got := humanType(t, start.Body.String()); got != mount.CodeBadRequest {
		t.Errorf("the refusal code = %q, want %q", got, mount.CodeBadRequest)
	}
	if m.calls != 0 {
		t.Errorf("the password method began %d times, want none", m.calls)
	}
}

// TestAStepUpWithNoFactorNamingAPasswordTheAccountCannotProveIsRefused holds the
// same refusal where the account holds no factor at all: the named password is
// the account's only first factor there, and an account that cannot prove one
// gets a bad request before any flow record is written.
func TestAStepUpWithNoFactorNamingAPasswordTheAccountCannotProveIsRefused(t *testing.T) {
	m := &fakeMethod{name: "password"}
	m.begin = func(authn.Start) (authn.Step, error) {
		return authn.StepPrompt("password", nil,
			authn.Field{Name: "password", Kind: authn.FieldPassword, Required: true}), nil
	}
	r := newRig(t, m)
	r.loginAsAuthenticated("acct1", "sid")
	r.allow("acct1") // an account row without a password credential
	r.mfa.outcome = authn.MFANone

	start := r.send(http.MethodPost, "/api/v1/auth/flows/authenticated",
		`{"purpose":"step-up","method":"password"}`)
	if start.Code != http.StatusBadRequest {
		t.Fatalf("a no-factor step-up naming a password the account cannot prove answered %d, want 400: %s",
			start.Code, start.Body.String())
	}
	if got := humanType(t, start.Body.String()); got != mount.CodeBadRequest {
		t.Errorf("the refusal code = %q, want %q", got, mount.CodeBadRequest)
	}
	if m.calls != 0 {
		t.Errorf("the password method began %d times, want none", m.calls)
	}
}

// TestAStepUpWithNoFactorRefusesAMethodTheSessionWasNotEstablishedWith holds
// REQ-security-016's first-factor channel: an account with no factors re-proves
// the channel its session was established with, so a password session naming an
// assembled SMS method is a bad request at the start, and the method never
// begins.
func TestAStepUpWithNoFactorRefusesAMethodTheSessionWasNotEstablishedWith(t *testing.T) {
	password := &fakeMethod{name: "password"}
	sms := &fakeMethod{name: "sms"}
	r := newRigMethods(t, password, sms)
	r.loginAsAuthenticated("acct1", "sid") // a password session
	r.mfa.outcome = authn.MFANone

	start := r.send(http.MethodPost, "/api/v1/auth/flows/authenticated",
		`{"purpose":"step-up","method":"sms"}`)
	if start.Code != http.StatusBadRequest {
		t.Fatalf("a no-factor step-up naming a channel the session did not use answered %d, want 400: %s",
			start.Code, start.Body.String())
	}
	if got := humanType(t, start.Body.String()); got != mount.CodeBadRequest {
		t.Errorf("the refusal code = %q, want %q", got, mount.CodeBadRequest)
	}
	if sms.calls != 0 {
		t.Errorf("the SMS method began %d times, want none", sms.calls)
	}
	if password.calls != 0 {
		t.Errorf("the password method began %d times, want none", password.calls)
	}
}

// TestAStepUpWithNoFactorStartsTheSessionChannel holds the positive half: a
// session established by an SMS code may re-prove that code, and the method of
// the same channel begins.
func TestAStepUpWithNoFactorStartsTheSessionChannel(t *testing.T) {
	password := &fakeMethod{name: "password"}
	sms := &fakeMethod{name: "sms"}
	sms.begin = func(authn.Start) (authn.Step, error) {
		return authn.StepPrompt("phone", nil,
			authn.Field{Name: "phone", Kind: authn.FieldPhone, Required: true}), nil
	}
	r := newRigMethods(t, password, sms)
	r.loginAsAuthenticated("acct1", "sid")
	r.sessions.status.Methods = []authn.Method{authn.MethodSMS}
	r.mfa.outcome = authn.MFANone

	start := r.send(http.MethodPost, "/api/v1/auth/flows/authenticated",
		`{"purpose":"step-up","method":"sms"}`)
	if start.Code != http.StatusOK {
		t.Fatalf("a no-factor step-up naming the session's channel answered %d, want 200: %s",
			start.Code, start.Body.String())
	}
	if sms.calls != 1 {
		t.Errorf("the SMS method began %d times, want one", sms.calls)
	}
	if password.calls != 0 {
		t.Errorf("the password method began %d times, want none", password.calls)
	}
}

// TestAStepUpWithNoPasswordIsRefused holds the boundary: an account with no
// password to prove again has no first factor that lets factors registered
// during the session strengthen it, so the step-up is refused and the person
// re-establishes the session instead.
func TestAStepUpWithNoPasswordIsRefused(t *testing.T) {
	m := &fakeMethod{name: "password"}
	r := newRig(t, m)
	r.loginAsAuthenticated("acct1", "sid")
	r.allow("acct1") // an account row without a password credential
	r.mfa.outcome = authn.MFAFactor
	r.mfa.offered = []authn.FactorRecord{{
		ID: "f1", Kind: authn.FactorTOTP, Status: authn.FactorActive, ActivatedAt: time.Now().Add(time.Minute),
	}}

	start := r.send(http.MethodPost, "/api/v1/auth/flows/authenticated", `{"purpose":"step-up"}`)
	if start.Code != http.StatusUnauthorized {
		t.Errorf("a step-up with no password answered %d, want 401: %s", start.Code, start.Body.String())
	}
}

// TestCompleteMakesASessionOnce holds the terminal login action: a ready flow
// completed twice makes a session once, the second completion refused by the
// tombstone.
func TestCompleteMakesASessionOnce(t *testing.T) {
	m := &fakeMethod{name: "password"}
	m.begin = func(authn.Start) (authn.Step, error) {
		return authn.StepProven(authn.Proof{Account: "acct1", Methods: []authn.Method{authn.MethodPassword}}), nil
	}
	r := newRig(t, m)
	r.allow("acct1")
	rec := r.send(http.MethodPost, "/api/v1/auth/flows", `{"method":"password"}`)
	cookie := flowCookie(t, rec)
	seq := int(decode(t, rec.Body.String())["seq"].(float64))

	done := r.send(http.MethodPost, "/api/v1/auth/flows/answer", `{"seq":`+itoa(seq)+`,"step":"complete"}`, "Cookie", cookie)
	if done.Code != http.StatusOK {
		t.Fatalf("complete answered %d: %s", done.Code, done.Body.String())
	}
	if len(r.sessions.created) != 1 {
		t.Fatalf("complete made %d sessions, want one", len(r.sessions.created))
	}
	if got := r.sessions.created[0].Account; got != "acct1" {
		t.Errorf("the session is for %q, want acct1", got)
	}

	again := r.send(http.MethodPost, "/api/v1/auth/flows/answer", `{"seq":`+itoa(seq+1)+`,"step":"complete"}`, "Cookie", cookie)
	if again.Code != http.StatusUnauthorized {
		t.Errorf("a second complete answered %d, want 401: %s", again.Code, again.Body.String())
	}
	if len(r.sessions.created) != 1 {
		t.Errorf("a second complete made another session: %d", len(r.sessions.created))
	}
}

// TestTheLoginAuditIsWrittenUnderADerivedIdentity holds that the audit row of a
// completed login is written under an identity the engine derives for that one
// call: the login runs on an Open route whose own context carries no identity,
// yet the row's actor is the account the login just proved. The request's own
// context is left as it was, so the login identity does not flow back into the
// chain the request runs in.
func TestTheLoginAuditIsWrittenUnderADerivedIdentity(t *testing.T) {
	m := &fakeMethod{name: "password"}
	m.begin = func(authn.Start) (authn.Step, error) {
		return authn.StepProven(authn.Proof{Account: "acct1", Methods: []authn.Method{authn.MethodPassword}}), nil
	}
	r := newRig(t, m)
	r.allow("acct1")
	sink := &auditing{}
	r.p.audit = sink
	// The route carries no identity, so anything on the request's own context
	// after the handler came from the engine writing it back.
	r.watchCtx.Store(true)

	rec := r.send(http.MethodPost, "/api/v1/auth/flows", `{"method":"password"}`)
	cookie := flowCookie(t, rec)
	seq := int(decode(t, rec.Body.String())["seq"].(float64))
	done := r.send(http.MethodPost, "/api/v1/auth/flows/answer", `{"seq":`+itoa(seq)+`,"step":"complete"}`, "Cookie", cookie)
	if done.Code != http.StatusOK {
		t.Fatalf("complete answered %d: %s", done.Code, done.Body.String())
	}
	got := sink.identities()
	if len(got) != 1 || got[0] != "acct1" {
		t.Fatalf("the audit rows were written under identities %v, want [acct1]", got)
	}
	if r.leaked.Load() {
		t.Errorf("the login engine left its derived identity on the request's own context")
	}
}

// TestAMustChangeLoginRevokesTheAccountsOtherSessions holds the terminal action's
// password-change branch: a login whose proof replaced the account's password
// revokes the account's other sessions and keeps the new one, while a login that
// did not touch the password leaves them alone.
func TestAMustChangeLoginRevokesTheAccountsOtherSessions(t *testing.T) {
	login := func(passwordChanged bool) *rig {
		t.Helper()
		m := &fakeMethod{name: "password"}
		m.begin = func(authn.Start) (authn.Step, error) {
			return authn.StepProven(authn.Proof{
				Account:         "acct1",
				Methods:         []authn.Method{authn.MethodPassword},
				PasswordChanged: passwordChanged,
			}), nil
		}
		r := newRig(t, m)
		r.allow("acct1")
		rec := r.send(http.MethodPost, "/api/v1/auth/flows", `{"method":"password"}`)
		cookie := flowCookie(t, rec)
		seq := int(decode(t, rec.Body.String())["seq"].(float64))
		done := r.send(http.MethodPost, "/api/v1/auth/flows/answer", `{"seq":`+itoa(seq)+`,"step":"complete"}`, "Cookie", cookie)
		if done.Code != http.StatusOK {
			t.Fatalf("complete answered %d: %s", done.Code, done.Body.String())
		}
		return r
	}

	changed := login(true)
	if changed.sessions.revokedAll != 1 {
		t.Fatalf("a password-changing login revoked the account's sessions %d times, want one", changed.sessions.revokedAll)
	}
	if len(changed.sessions.made) != 1 || changed.sessions.except != changed.sessions.made[0].ID {
		t.Errorf("the revoke kept session %q, want the new one %q", changed.sessions.except, changed.sessions.made[0].ID)
	}

	plain := login(false)
	if plain.sessions.revokedAll != 0 {
		t.Errorf("a login that did not change the password revoked the account's sessions %d times, want none", plain.sessions.revokedAll)
	}
}

// TestAnswersAreCappedAtEight holds the per-flow answer limit: the ninth answer
// makes the flow a tombstone and is refused.
func TestAnswersAreCappedAtEight(t *testing.T) {
	m := &fakeMethod{name: "password"}
	m.begin = func(authn.Start) (authn.Step, error) { return authn.StepPrompt("credentials", nil), nil }
	m.ans = func(authn.FlowState, authn.StepInput) (authn.Step, error) {
		return authn.StepPrompt("credentials", nil), nil
	}
	r := newRig(t, m)
	rec := r.send(http.MethodPost, "/api/v1/auth/flows", `{"method":"password"}`)
	cookie := flowCookie(t, rec)
	for i := 1; i <= maxAnswers; i++ {
		out := r.send(http.MethodPost, "/api/v1/auth/flows/answer", `{"seq":`+itoa(i)+`,"step":"answer","input":{}}`, "Cookie", cookie)
		if out.Code != http.StatusOK {
			t.Fatalf("answer %d answered %d: %s", i, out.Code, out.Body.String())
		}
	}
	ninth := r.send(http.MethodPost, "/api/v1/auth/flows/answer", `{"seq":`+itoa(maxAnswers+1)+`,"step":"answer","input":{}}`, "Cookie", cookie)
	if ninth.Code != http.StatusUnauthorized {
		t.Errorf("the answer past the limit answered %d, want 401: %s", ninth.Code, ninth.Body.String())
	}
}

// TestCrossSiteStartIsRefusedByTheWrapper holds that the cross-origin check runs
// before the handler: a cross-site POST flows is refused without reaching it.
func TestCrossSiteStartIsRefusedByTheWrapper(t *testing.T) {
	m := &fakeMethod{name: "password"}
	m.begin = func(authn.Start) (authn.Step, error) { return authn.StepPrompt("credentials", nil), nil }
	r := newRig(t, m)
	rec := r.send(http.MethodPost, "/api/v1/auth/flows", `{"method":"password"}`,
		"Sec-Fetch-Site", "cross-site", "Origin", "https://evil.example.org")
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("a cross-site start answered %d, want the gateway's 401: %s", rec.Code, rec.Body.String())
	}
	if m.calls != 0 {
		t.Errorf("a cross-site start reached the handler: calls %d", m.calls)
	}
}

// TestDisabledMethodIsRefused holds that a method the operator turned off answers
// method-disabled without reaching its Begin.
func TestDisabledMethodIsRefusedWithAMethodDisabledCode(t *testing.T) {
	m := &fakeMethod{name: "sms"}
	m.begin = func(authn.Start) (authn.Step, error) { return authn.StepPrompt("phone", nil), nil }
	r := newRig(t, m)
	r.p.settings = readerWithDisabled(t, "sms")
	rec := r.send(http.MethodPost, "/api/v1/auth/flows", `{"method":"sms"}`)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("a disabled method answered %d, want 403: %s", rec.Code, rec.Body.String())
	}
	if m.calls != 0 {
		t.Errorf("a disabled method's Begin ran: calls %d", m.calls)
	}
}

// TestThePasswordMethodIsNeverTurnedOffByTheSwitch holds that a raw value naming
// password neither hides it from the listing nor refuses its flow, while the
// other names in the same value still turn their methods off.
func TestThePasswordMethodIsNeverTurnedOffByTheSwitch(t *testing.T) {
	password := &fakeMethod{name: "password"}
	password.begin = func(authn.Start) (authn.Step, error) { return authn.StepPrompt("credentials", nil), nil }
	sms := &fakeMethod{name: "sms"}
	sms.begin = func(authn.Start) (authn.Step, error) { return authn.StepPrompt("phone", nil), nil }

	r := newRigMethods(t, password, sms)
	r.p.settings = readerWithDisabled(t, "sms", "password")

	listing := r.send(http.MethodGet, "/api/v1/auth/methods", "")
	methods := decode(t, listing.Body.String())["methods"].([]any)
	if len(methods) != 1 || methods[0].(map[string]any)["name"] != "password" {
		t.Fatalf("methods = %v, want password alone: sms is off and password never is", methods)
	}
	if rec := r.send(http.MethodPost, "/api/v1/auth/flows", `{"method":"sms"}`); rec.Code != http.StatusForbidden {
		t.Errorf("start of sms answered %d, want the 403 method-disabled", rec.Code)
	}
	if rec := r.send(http.MethodPost, "/api/v1/auth/flows", `{"method":"password"}`); rec.Code != http.StatusOK {
		t.Errorf("start of password answered %d, want 200: the switch cannot turn it off", rec.Code)
	}

	only := newRig(t, password)
	only.p.settings = readerWithDisabled(t, "password")
	if rec := only.send(http.MethodPost, "/api/v1/auth/flows", `{"method":"password"}`); rec.Code != http.StatusOK {
		t.Errorf("start of password with a raw value naming it answered %d, want 200", rec.Code)
	}
}

// fakeStore is a settings store that answers string and int items and errors for
// the rest.
type fakeStore struct{ values map[string]string }

func (s *fakeStore) Get(_ settings.Layer, key string, target any) error {
	v, ok := s.values[key]
	if !ok {
		return settings.ErrUnknownKey
	}
	switch p := target.(type) {
	case *string:
		*p = v
	case *int:
		n, err := strconv.Atoi(v)
		if err != nil {
			return err
		}
		*p = n
	case *time.Duration:
		d, err := time.ParseDuration(v)
		if err != nil {
			return err
		}
		*p = d
	default:
		return errors.New("the target is not a string, an int or a duration")
	}
	return nil
}
func (*fakeStore) Set(context.Context, settings.Write) error                      { return nil }
func (*fakeStore) Delete(context.Context, settings.Layer, string, string) error   { return nil }
func (*fakeStore) List(context.Context, settings.Layer) ([]settings.Entry, error) { return nil, nil }
func (*fakeStore) Declared() []settings.DeclaredItem                              { return nil }

// readerWithDisabled builds a settings reader whose disabled-methods switch lists
// the names given.
func readerWithDisabled(t *testing.T, names ...string) *reader {
	t.Helper()
	encoded, err := json.Marshal(names)
	if err != nil {
		t.Fatalf("encoding the disabled methods: %v", err)
	}
	store := &fakeStore{values: map[string]string{disabledMethodsPath: string(encoded)}}
	return newReader(store, slog.New(slog.NewTextHandler(ioDiscard{}, nil)))
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	neg := n < 0
	if neg {
		n = -n
	}
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	if neg {
		b = append([]byte{'-'}, b...)
	}
	return string(b)
}

// TestAStepUpOffersTheOtherFactorsWhenOneIsExcluded holds the deletion case
// with factors to spare: the caller excludes the factor it is about to remove,
// so the offered list carries the others and never the one leaving.
func TestAStepUpOffersTheOtherFactorsWhenOneIsExcluded(t *testing.T) {
	m := &fakeMethod{name: "password"}
	r := newRig(t, m)
	r.loginAsAuthenticated("acct1", "sid")
	r.mfa.outcome = authn.MFAFactor
	r.mfa.offered = []authn.FactorRecord{
		{ID: "f1", Kind: authn.FactorTOTP, Status: authn.FactorActive, ActivatedAt: time.Now().Add(-time.Hour)},
		{ID: "f2", Kind: authn.FactorTOTP, Status: authn.FactorActive, ActivatedAt: time.Now().Add(-time.Hour)},
	}
	r.mfa.assurance = authn.MFAResult{Assurance: authn.AssuranceMulti}

	start := r.send(http.MethodPost, "/api/v1/auth/flows/authenticated",
		`{"purpose":"step-up","input":{"exclude_factor":"f1"}}`)
	if start.Code != http.StatusOK {
		t.Fatalf("the step-up start answered %d: %s", start.Code, start.Body.String())
	}
	body := decode(t, start.Body.String())
	if body["state"] != "second" {
		t.Fatalf("the step-up state is %v, want second", body["state"])
	}
	factors, ok := body["next"].(map[string]any)["factors"].([]any)
	if !ok || len(factors) != 1 {
		t.Fatalf("the offered factors are %v, want the one not excluded", body["next"])
	}
	if id := factors[0].(map[string]any)["id"]; id != "f2" {
		t.Errorf("the offered factor is %v, want f2", id)
	}
}

// TestDeletingTheLastFactorCanProveThePassword holds the lockout case: when the
// excluded factor is the account's only one, the step-up starts at the password,
// and a proved password alone completes the elevation. No factor is offered,
// because the one being removed may not prove its own removal.
func TestDeletingTheLastFactorCanProveThePassword(t *testing.T) {
	m := &fakeMethod{name: "password"}
	m.begin = func(authn.Start) (authn.Step, error) {
		return authn.StepPrompt("password", nil, authn.Field{Name: "password", Kind: authn.FieldPassword, Required: true}), nil
	}
	m.ans = func(authn.FlowState, authn.StepInput) (authn.Step, error) {
		return authn.StepProven(authn.Proof{Account: "acct1", Methods: []authn.Method{authn.MethodPassword}}), nil
	}
	r := newRig(t, m)
	r.loginAsAuthenticated("acct1", "sid")
	r.allowWithPassword("acct1")
	r.mfa.outcome = authn.MFAFactor
	r.mfa.offered = []authn.FactorRecord{{
		ID: "f1", Kind: authn.FactorTOTP, Status: authn.FactorActive, ActivatedAt: time.Now().Add(-time.Hour),
	}}
	// A password proved alone is a single factor; the policy answering single
	// here is what the elevation must carry through unchanged.
	r.mfa.assurance = authn.MFAResult{Assurance: authn.AssuranceSingle}

	start := r.send(http.MethodPost, "/api/v1/auth/flows/authenticated",
		`{"purpose":"step-up","input":{"exclude_factor":"f1"}}`)
	if start.Code != http.StatusOK {
		t.Fatalf("the step-up start answered %d: %s", start.Code, start.Body.String())
	}
	body := decode(t, start.Body.String())
	if body["state"] != "first" {
		t.Fatalf("the step-up state is %v, want first: the password is proved again", body["state"])
	}
	if k := body["next"].(map[string]any)["kind"]; k != "challenge" {
		t.Fatalf("the start answered next kind %v, want the password challenge", k)
	}
	cookie := flowCookie(t, start)
	seq := int(body["seq"].(float64))

	answered := r.send(http.MethodPost, "/api/v1/auth/flows/answer",
		`{"seq":`+itoa(seq)+`,"step":"answer","input":{"password":"secret"}}`, "Cookie", cookie)
	if answered.Code != http.StatusOK {
		t.Fatalf("the password answer answered %d: %s", answered.Code, answered.Body.String())
	}
	answeredBody := decode(t, answered.Body.String())
	if answeredBody["state"] != "ready" {
		t.Fatalf("the flow state after the password is %v, want ready: no factor is left to offer", answeredBody["state"])
	}
	done := r.send(http.MethodPost, "/api/v1/auth/flows/answer",
		`{"seq":`+itoa(int(answeredBody["seq"].(float64)))+`,"step":"complete"}`, "Cookie", cookie)
	if done.Code != http.StatusOK {
		t.Fatalf("the step-up completion answered %d: %s", done.Code, done.Body.String())
	}
	if len(r.sessions.elevated) != 1 {
		t.Fatalf("the step-up elevated %d sessions, want one", len(r.sessions.elevated))
	}
	// The password alone is a single factor: the elevation must not claim
	// multi-factor strength, which would unlock every other account action.
	if got := r.sessions.elevated[0].Assurance; got != authn.AssuranceSingle {
		t.Errorf("the elevation is %v, want single-factor", got)
	}
}

// TestDeletingTheLastFactorWithoutAPasswordStillNeedsRelogin holds the account
// that cannot prove a password: excluding its only factor leaves no door, and
// the start is the insufficient answer the dialog sends to re-login.
func TestDeletingTheLastFactorWithoutAPasswordStillNeedsRelogin(t *testing.T) {
	m := &fakeMethod{name: "password"}
	r := newRig(t, m)
	r.loginAsAuthenticated("acct1", "sid")
	r.allow("acct1")
	r.mfa.outcome = authn.MFAFactor
	r.mfa.offered = []authn.FactorRecord{{
		ID: "f1", Kind: authn.FactorTOTP, Status: authn.FactorActive, ActivatedAt: time.Now().Add(-time.Hour),
	}}
	r.mfa.assurance = authn.MFAResult{Assurance: authn.AssuranceMulti}

	start := r.send(http.MethodPost, "/api/v1/auth/flows/authenticated",
		`{"purpose":"step-up","input":{"exclude_factor":"f1"}}`)
	if start.Code != http.StatusUnauthorized {
		t.Fatalf("the start answered %d, want 401: %s", start.Code, start.Body.String())
	}
	if challenge := start.Header().Get("WWW-Authenticate"); !strings.Contains(challenge, `insufficient_user_authentication`) {
		t.Errorf("the challenge %q does not name insufficient_user_authentication", challenge)
	}
}
