package login

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/vislake/speed/pkg/authn"
)

func humanType(t *testing.T, body string) string {
	t.Helper()
	return strings.TrimPrefix(fmt.Sprint(decode(t, body)["type"]), "urn:speed:authn:")
}

type fakeHuman struct {
	required       bool
	byScope        map[authn.HumanScope]bool
	provider       string
	siteKey        string
	action         string
	accept         map[string]bool
	err            error
	calls          []authn.HumanProof
	requiredScopes []authn.HumanScope
	verifyScopes   []authn.HumanScope
}

func (h *fakeHuman) Required(scope authn.HumanScope) bool {
	h.requiredScopes = append(h.requiredScopes, scope)
	if h.byScope != nil {
		return h.byScope[scope]
	}
	return h.required
}
func (h *fakeHuman) Provider(authn.HumanScope) string { return h.provider }
func (h *fakeHuman) SiteKey(authn.HumanScope) string  { return h.siteKey }
func (h *fakeHuman) Action(authn.HumanScope) string   { return h.action }

func (h *fakeHuman) Verify(_ context.Context, p authn.HumanProof, scope authn.HumanScope) error {
	h.calls = append(h.calls, p)
	h.verifyScopes = append(h.verifyScopes, scope)
	if h.err != nil {
		return h.err
	}
	if !h.required && h.byScope == nil {
		return nil
	}
	if p.Token == "" || !h.accept[p.Token] {
		return fmt.Errorf("%w: the proof is missing or not valid", authn.ErrHumanVerificationRequired)
	}
	return nil
}

func (h *fakeHuman) askedScopes() []authn.HumanScope {
	scopes := make([]authn.HumanScope, 0, len(h.requiredScopes)+len(h.verifyScopes))
	scopes = append(scopes, h.requiredScopes...)
	scopes = append(scopes, h.verifyScopes...)
	return scopes
}

func requiredHuman() *fakeHuman {
	return &fakeHuman{required: true, provider: "pow", accept: map[string]bool{"proof-1": true}}
}

func assertOnlyScope(t *testing.T, human *fakeHuman, want authn.HumanScope) {
	t.Helper()
	scopes := human.askedScopes()
	if len(scopes) == 0 {
		t.Fatalf("the gate was asked about no scope, want at least %q", want)
	}
	for _, got := range scopes {
		if got != want {
			t.Errorf("the gate was asked about scope %q, want only %q", got, want)
		}
	}
}

func phoneMethod() *fakeMethod {
	return &fakeMethod{name: "sms", desc: authn.MethodDescriptor{
		Name: "sms", Kind: authn.MethodKindForm,
		Fields: []authn.Field{{Name: "phone", Kind: authn.FieldPhone, Required: true}},
	}}
}

// TestTheEngineForwardsTheStartProofWithoutCheckingIt holds that a start request's proof reaches the method and the gate is never asked.
func TestTheEngineForwardsTheStartProofWithoutCheckingIt(t *testing.T) {
	m := phoneMethod()
	var got authn.Start
	m.begin = func(s authn.Start) (authn.Step, error) {
		got = s
		return authn.StepPrompt("code", nil), nil
	}
	r := newRig(t, m)
	human := requiredHuman()
	r.p.human = human

	rec := r.send(http.MethodPost, "/api/v1/auth/flows",
		`{"method":"sms","input":{"phone":"+8613800000000"},"human_proof":"proof-1"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("a start answered %d: %s", rec.Code, rec.Body.String())
	}
	if m.calls != 1 {
		t.Fatalf("the method was called %d times, want once", m.calls)
	}
	if got.Proof != "proof-1" {
		t.Errorf("the method was handed proof %q, want the request's proof-1", got.Proof)
	}
	if len(human.calls) != 0 {
		t.Errorf("the engine asked the gate %v, want not asked: the code engine checks the proof", human.calls)
	}
}

// TestTheEngineForwardsAnAnswerProofWithoutCheckingIt holds that an answer's proof reaches the method in FlowState and the gate is never asked.
func TestTheEngineForwardsAnAnswerProofWithoutCheckingIt(t *testing.T) {
	m := phoneMethod()
	m.begin = func(authn.Start) (authn.Step, error) {
		return authn.StepPrompt("phone", nil, authn.Field{Name: "phone", Kind: authn.FieldPhone, Required: true}), nil
	}
	var got authn.FlowState
	m.ans = func(f authn.FlowState, _ authn.StepInput) (authn.Step, error) {
		got = f
		return authn.StepPrompt("code", nil), nil
	}
	r := newRig(t, m)
	human := requiredHuman()
	r.p.human = human

	start := r.send(http.MethodPost, "/api/v1/auth/flows", `{"method":"sms"}`)
	if start.Code != http.StatusOK {
		t.Fatalf("the start answered %d: %s", start.Code, start.Body.String())
	}
	cookie := flowCookie(t, start)
	seq := int(decode(t, start.Body.String())["seq"].(float64))

	rec := r.send(http.MethodPost, "/api/v1/auth/flows/answer",
		fmt.Sprintf(`{"seq":%d,"step":"answer","input":{"phone":"+8613800000000"},"human_proof":"proof-1"}`, seq),
		"Cookie", cookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("the answer answered %d: %s", rec.Code, rec.Body.String())
	}
	if got.Proof != "proof-1" {
		t.Errorf("the method was handed proof %q, want the request's proof-1", got.Proof)
	}
	if len(human.calls) != 0 {
		t.Errorf("the engine asked the gate %v, want not asked: the code engine checks the proof", human.calls)
	}
}

// TestTheEngineForwardsAFactorChallengeProofWithoutCheckingIt holds that choosing an SMS factor hands the proof to the factor and the engine does not check it.
func TestTheEngineForwardsAFactorChallengeProofWithoutCheckingIt(t *testing.T) {
	m := &fakeMethod{name: "password"}
	m.begin = func(authn.Start) (authn.Step, error) {
		return authn.StepProven(authn.Proof{Account: "acct1", Methods: []authn.Method{authn.MethodPassword}}), nil
	}
	r := newRig(t, m)
	r.allow("acct1")
	r.mfa.outcome = authn.MFAFactor
	r.mfa.offered = []authn.FactorRecord{{ID: "f1", Kind: authn.FactorSMS, Label: "Phone"}}
	human := requiredHuman()
	r.p.human = human

	start := r.send(http.MethodPost, "/api/v1/auth/flows", `{"method":"password"}`)
	if start.Code != http.StatusOK {
		t.Fatalf("the start answered %d: %s", start.Code, start.Body.String())
	}
	cookie := flowCookie(t, start)
	seq := int(decode(t, start.Body.String())["seq"].(float64))

	rec := r.send(http.MethodPost, "/api/v1/auth/flows/answer",
		fmt.Sprintf(`{"seq":%d,"step":"factor","input":{"factor":"f1"},"human_proof":"proof-1"}`, seq),
		"Cookie", cookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("the factor choice answered %d: %s", rec.Code, rec.Body.String())
	}
	if r.mfa.challenges != 1 {
		t.Fatalf("the factor challenge was started %d times, want once", r.mfa.challenges)
	}
	if got := r.mfa.challengeStates[0].Proof; got != "proof-1" {
		t.Errorf("the factor was handed proof %q, want the request's proof-1", got)
	}
	if len(human.calls) != 0 {
		t.Errorf("the engine asked the gate %v, want not asked: the code engine checks the proof", human.calls)
	}
}

// TestARejectedStartLeavesNoContinuableFlowAndChargesNothing holds the
// record the start writes before the method runs.
func TestARejectedStartLeavesNoContinuableFlowAndChargesNothing(t *testing.T) {
	begins := 0
	m := phoneMethod()
	m.begin = func(authn.Start) (authn.Step, error) {
		begins++
		return authn.StepRefused(fmt.Errorf("authn.login.sms: %w", authn.ErrHumanVerificationRequired)), nil
	}
	r := newRig(t, m)

	start := r.send(http.MethodPost, "/api/v1/auth/flows",
		`{"method":"sms","input":{"phone":"+8613800000000"}}`)
	if start.Code != http.StatusForbidden {
		t.Fatalf("a start the gate refused answered %d, want 403: %s", start.Code, start.Body.String())
	}
	if begins != 1 {
		t.Fatalf("the method's Begin ran %d times, want once", begins)
	}

	for _, c := range start.Result().Cookies() {
		if strings.Contains(c.Name, "authn-flow") {
			t.Errorf("the refused start handed the client the flow cookie %q, want none", c.Name)
		}
	}
	keys := r.kv.keys()
	if len(keys) != 1 {
		t.Fatalf("the refused start left %d store keys, want exactly one flow record: %v", len(keys), keys)
	}
	raw, present, err := r.kv.Get(t.Context(), keys[0])
	if err != nil || !present {
		t.Fatalf("reading the refused start's record: present=%v err=%v", present, err)
	}
	held, err := decodeRecord(raw)
	if err != nil {
		t.Fatalf("decoding the refused start's record: %v", err)
	}
	if held.State != stateFirst || held.MethodStep != "" {
		t.Errorf("the refused start left state %q step %q, want the first state with no method step",
			held.State, held.MethodStep)
	}
	if held.Sends != 0 {
		t.Errorf("the refused start's record counts %d sends, want none: the proof was refused before any send", held.Sends)
	}
	if held.Answers != 0 {
		t.Errorf("the refused start's record counts %d answers, want none: a Begin is not a client answer", held.Answers)
	}
}

// TestAProoflessFirstStepIsNotReportedAsAFailedLogin holds that a gate refusal answers 403 and records neither a failed outcome nor an audit row.
func TestAProoflessFirstStepIsNotReportedAsAFailedLogin(t *testing.T) {
	reader, mp := newTestMetrics(t)
	m := phoneMethod()
	m.begin = func(authn.Start) (authn.Step, error) {
		return authn.StepRefused(fmt.Errorf("authn.login.sms: %w", authn.ErrHumanVerificationRequired)), nil
	}
	r := newRig(t, m)
	sink := &auditing{}
	r.p.audit = sink
	var err error
	if r.p.meters, err = newMeters(mp); err != nil {
		t.Fatalf("creating the instrument: %v", err)
	}

	rec := r.send(http.MethodPost, "/api/v1/auth/flows",
		`{"method":"sms","input":{"phone":"+8613800000000"}}`)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("a first step the gate refused answered %d, want 403: %s", rec.Code, rec.Body.String())
	}
	if got := humanType(t, rec.Body.String()); got != "human-verification-required" {
		t.Errorf("the refusal code = %q, want human-verification-required", got)
	}
	if pairs := loginMetricPairs(t, reader); len(pairs) != 0 {
		t.Errorf("a proof refusal was counted as a login outcome: %v", pairs)
	}
	if rows := sink.rows(); len(rows) != 0 {
		t.Errorf("a proof refusal wrote %d login audit rows, want none: %v", len(rows), rows)
	}
}

// TestAFirstStepRefusedForAnArgumentIsReportedAsAFailedLogin holds that ErrInvalidArgument is an ordinary refusal, counted and audited as a failed login.
func TestAFirstStepRefusedForAnArgumentIsReportedAsAFailedLogin(t *testing.T) {
	reader, mp := newTestMetrics(t)
	m := phoneMethod()
	m.begin = func(authn.Start) (authn.Step, error) {
		return authn.StepRefused(fmt.Errorf("authn.login.sms: %w", authn.ErrInvalidArgument)), nil
	}
	r := newRig(t, m)
	sink := &auditing{}
	r.p.audit = sink
	var err error
	if r.p.meters, err = newMeters(mp); err != nil {
		t.Fatalf("creating the instrument: %v", err)
	}

	rec := r.send(http.MethodPost, "/api/v1/auth/flows",
		`{"method":"sms","input":{"phone":"+8613800000000"}}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("a first step refused for an argument answered %d, want 400: %s", rec.Code, rec.Body.String())
	}
	if got := humanType(t, rec.Body.String()); got != "bad-request" {
		t.Errorf("the refusal code = %q, want bad-request", got)
	}
	if pairs := loginMetricPairs(t, reader); len(pairs) != 1 || pairs["sms/failed"] != 1 {
		t.Errorf("the failed login's metric pairs = %v, want one sms/failed", pairs)
	}
	if rows := sink.rows(); len(rows) != 1 {
		t.Errorf("the failed login wrote %d audit rows, want one: %v", len(rows), rows)
	}
}

// TestAFactorChallengeProofRefusalIsNotReportedAsAFailedLogin holds the
// second factor's observation side.
func TestAFactorChallengeProofRefusalIsNotReportedAsAFailedLogin(t *testing.T) {
	cases := []struct {
		name   string
		cause  error
		status int
		code   string
	}{
		{"missing proof", authn.ErrHumanVerificationRequired, http.StatusForbidden, "human-verification-required"},
		{"over-long proof", authn.ErrHumanProofTooLong, http.StatusBadRequest, "bad-request"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			reader, mp := newTestMetrics(t)
			m := &fakeMethod{name: "password"}
			m.begin = func(authn.Start) (authn.Step, error) {
				return authn.StepProven(authn.Proof{Account: "acct1", Methods: []authn.Method{authn.MethodPassword}}), nil
			}
			r := newRig(t, m)
			r.allow("acct1")
			r.mfa.outcome = authn.MFAFactor
			r.mfa.offered = []authn.FactorRecord{{ID: "f1", Kind: authn.FactorSMS, Label: "Phone"}}
			r.mfa.challenge = func(string) authn.Step {
				return authn.StepRefused(fmt.Errorf("authn.mfa.sms: %w", tc.cause))
			}
			sink := &auditing{}
			r.p.audit = sink
			var err error
			if r.p.meters, err = newMeters(mp); err != nil {
				t.Fatalf("creating the instrument: %v", err)
			}

			start := r.send(http.MethodPost, "/api/v1/auth/flows", `{"method":"password"}`)
			if start.Code != http.StatusOK {
				t.Fatalf("the start answered %d: %s", start.Code, start.Body.String())
			}
			cookie := flowCookie(t, start)
			seq := int(decode(t, start.Body.String())["seq"].(float64))

			rec := r.send(http.MethodPost, "/api/v1/auth/flows/answer",
				fmt.Sprintf(`{"seq":%d,"step":"factor","input":{"factor":"f1"}}`, seq),
				"Cookie", cookie)
			if rec.Code != tc.status {
				t.Fatalf("the refused challenge answered %d, want %d: %s", rec.Code, tc.status, rec.Body.String())
			}
			if got := humanType(t, rec.Body.String()); got != tc.code {
				t.Errorf("the refusal code = %q, want %q", got, tc.code)
			}
			if pairs := loginMetricPairs(t, reader); len(pairs) != 0 {
				t.Errorf("a proof refusal was counted as a login outcome: %v", pairs)
			}
			if rows := sink.rows(); len(rows) != 0 {
				t.Errorf("a proof refusal wrote %d login audit rows, want none: %v", len(rows), rows)
			}
			if r.mfa.challenges != 1 {
				t.Errorf("the factor challenge was started %d times, want once", r.mfa.challenges)
			}
		})
	}
}

// TestAFactorChallengeNonProofRefusalIsReportedAsAFailedLogin holds that a wrong credential on a factor challenge answers 401 and is counted and audited once.
func TestAFactorChallengeNonProofRefusalIsReportedAsAFailedLogin(t *testing.T) {
	reader, mp := newTestMetrics(t)
	m := &fakeMethod{name: "password"}
	m.begin = func(authn.Start) (authn.Step, error) {
		return authn.StepProven(authn.Proof{Account: "acct1", Methods: []authn.Method{authn.MethodPassword}}), nil
	}
	r := newRig(t, m)
	r.allow("acct1")
	r.mfa.outcome = authn.MFAFactor
	r.mfa.offered = []authn.FactorRecord{{ID: "f1", Kind: authn.FactorSMS, Label: "Phone"}}
	r.mfa.challenge = func(string) authn.Step {
		return authn.StepRefused(fmt.Errorf("authn.mfa.sms: %w", authn.ErrInvalidCredentials))
	}
	sink := &auditing{}
	r.p.audit = sink
	var err error
	if r.p.meters, err = newMeters(mp); err != nil {
		t.Fatalf("creating the instrument: %v", err)
	}

	start := r.send(http.MethodPost, "/api/v1/auth/flows", `{"method":"password"}`)
	if start.Code != http.StatusOK {
		t.Fatalf("the start answered %d: %s", start.Code, start.Body.String())
	}
	cookie := flowCookie(t, start)
	seq := int(decode(t, start.Body.String())["seq"].(float64))

	rec := r.send(http.MethodPost, "/api/v1/auth/flows/answer",
		fmt.Sprintf(`{"seq":%d,"step":"factor","input":{"factor":"f1"}}`, seq),
		"Cookie", cookie)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("the refused challenge answered %d, want 401: %s", rec.Code, rec.Body.String())
	}
	if got := humanType(t, rec.Body.String()); got != "login-failed" {
		t.Errorf("the refusal code = %q, want login-failed", got)
	}
	if pairs := loginMetricPairs(t, reader); len(pairs) != 1 || pairs["password/failed"] != 1 {
		t.Errorf("the failed login's metric pairs = %v, want one password/failed", pairs)
	}
	if rows := sink.rows(); len(rows) != 1 {
		t.Errorf("the failed login wrote %d audit rows, want one: %v", len(rows), rows)
	}
	if r.mfa.challenges != 1 {
		t.Errorf("the factor challenge was started %d times, want once", r.mfa.challenges)
	}
}

// TestAFactorChallengeWithoutAProvenPhoneIsThePhoneRequiredProblem holds that a missing verified phone answers 403 phone-required with no failed outcome or audit row.
func TestAFactorChallengeWithoutAProvenPhoneIsThePhoneRequiredProblem(t *testing.T) {
	reader, mp := newTestMetrics(t)
	m := &fakeMethod{name: "password"}
	m.begin = func(authn.Start) (authn.Step, error) {
		return authn.StepProven(authn.Proof{Account: "acct1", Methods: []authn.Method{authn.MethodPassword}}), nil
	}
	r := newRig(t, m)
	r.allow("acct1")
	r.mfa.outcome = authn.MFAFactor
	r.mfa.offered = []authn.FactorRecord{{ID: "f1", Kind: authn.FactorSMS, Label: "Phone"}}
	r.mfa.challenge = func(string) authn.Step {
		return authn.StepRefused(fmt.Errorf("authn.mfa.sms: %w: the account has no proven phone", authn.ErrPhoneRequired))
	}
	sink := &auditing{}
	r.p.audit = sink
	var err error
	if r.p.meters, err = newMeters(mp); err != nil {
		t.Fatalf("creating the instrument: %v", err)
	}

	start := r.send(http.MethodPost, "/api/v1/auth/flows", `{"method":"password"}`)
	if start.Code != http.StatusOK {
		t.Fatalf("the start answered %d: %s", start.Code, start.Body.String())
	}
	cookie := flowCookie(t, start)
	seq := int(decode(t, start.Body.String())["seq"].(float64))

	rec := r.send(http.MethodPost, "/api/v1/auth/flows/answer",
		fmt.Sprintf(`{"seq":%d,"step":"factor","input":{"factor":"f1"}}`, seq),
		"Cookie", cookie)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("the refused challenge answered %d, want 403: %s", rec.Code, rec.Body.String())
	}
	if got := humanType(t, rec.Body.String()); got != "phone-required" {
		t.Errorf("the refusal code = %q, want phone-required", got)
	}
	if pairs := loginMetricPairs(t, reader); len(pairs) != 0 {
		t.Errorf("a missing phone was counted as a login outcome: %v", pairs)
	}
	if rows := sink.rows(); len(rows) != 0 {
		t.Errorf("a missing phone wrote %d login audit rows, want none: %v", len(rows), rows)
	}
	if r.mfa.challenges != 1 {
		t.Errorf("the factor challenge was started %d times, want once", r.mfa.challenges)
	}
}

// TestAStartWithNoProofReachesTheMethod holds the other direction: the
// engine no longer refuses a send for want of a proof.
func TestAStartWithNoProofReachesTheMethod(t *testing.T) {
	m := phoneMethod()
	var got authn.Start
	m.begin = func(s authn.Start) (authn.Step, error) {
		got = s
		return authn.StepPrompt("code", nil), nil
	}
	r := newRig(t, m)
	human := requiredHuman()
	r.p.human = human

	rec := r.send(http.MethodPost, "/api/v1/auth/flows",
		`{"method":"sms","input":{"phone":"+8613800000000"}}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("a start with no proof answered %d, want the method to decide: %s", rec.Code, rec.Body.String())
	}
	if m.calls != 1 {
		t.Errorf("the method was called %d times, want once", m.calls)
	}
	if got.Proof != "" {
		t.Errorf("the method was handed proof %q, want the empty proof the request presented", got.Proof)
	}
	if len(human.calls) != 0 {
		t.Errorf("the engine asked the gate %v, want not asked", human.calls)
	}
}
