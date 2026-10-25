package login

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/authn/internal/mount"
)

// fakeHuman is a human-verification gate a case scripts: whether it requires a
// proof, the mechanism it names, and the tokens it accepts. Every call is
// recorded so a case can see what the request presented.
type fakeHuman struct {
	required bool
	provider string
	siteKey  string
	accept   map[string]bool
	err      error
	calls    []authn.HumanProof
}

func (h *fakeHuman) Required() bool   { return h.required }
func (h *fakeHuman) Provider() string { return h.provider }
func (h *fakeHuman) SiteKey() string  { return h.siteKey }

func (h *fakeHuman) Verify(_ context.Context, p authn.HumanProof) error {
	h.calls = append(h.calls, p)
	if h.err != nil {
		return h.err
	}
	if !h.required {
		return nil
	}
	if p.Token == "" || !h.accept[p.Token] {
		return fmt.Errorf("%w: the proof is missing or not valid", authn.ErrHumanVerificationRequired)
	}
	return nil
}

// requiredHuman is a gate that requires a proof and accepts proof-1.
func requiredHuman() *fakeHuman {
	return &fakeHuman{required: true, provider: "pow", accept: map[string]bool{"proof-1": true}}
}

// phoneMethod is a login method whose first step declares the phone field, the
// shape a method that sends a code has.
func phoneMethod() *fakeMethod {
	return &fakeMethod{name: "sms", desc: authn.MethodDescriptor{
		Name: "sms", Kind: authn.MethodKindForm,
		Fields: []authn.Field{{Name: "phone", Kind: authn.FieldPhone, Required: true}},
	}}
}

// humanType is the problem code a response body carries, without the type
// prefix.
func humanType(t *testing.T, body string) string {
	t.Helper()
	return strings.TrimPrefix(fmt.Sprint(decode(t, body)["type"]), "urn:speed:authn:")
}

// keysWithPrefix reports the keys the fake store holds under one prefix.
func keysWithPrefix(kv *fakeKV, prefix string) []string {
	var out []string
	for _, key := range kv.keys() {
		if strings.HasPrefix(key, prefix) {
			out = append(out, key)
		}
	}
	return out
}

// TestALoginStartWithoutAProofIsRefusedAndMintsNothing holds the first gated
// entry point: a first step that sends a code is refused with the
// human-verification code when the deployment requires a proof and the request
// presents none. The method is never called, so nothing is sent, and no flow
// record or send budget is written.
func TestALoginStartWithoutAProofIsRefusedAndMintsNothing(t *testing.T) {
	m := phoneMethod()
	m.begin = func(authn.Start) (authn.Step, error) { return authn.StepPrompt("code", nil), nil }
	r := newRig(t, m)
	human := requiredHuman()
	r.p.human = human

	rec := r.send(http.MethodPost, "/api/v1/auth/flows",
		`{"method":"sms","input":{"phone":"+8613800000000"}}`)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("a start with no proof answered %d: %s", rec.Code, rec.Body.String())
	}
	if got := humanType(t, rec.Body.String()); got != mount.CodeHumanVerificationRequired {
		t.Errorf("the refusal code = %q, want %q", got, mount.CodeHumanVerificationRequired)
	}
	if m.calls != 0 {
		t.Errorf("the method was called %d times, want none: a refused proof must send nothing", m.calls)
	}
	if keys := r.kv.keys(); len(keys) != 0 {
		t.Errorf("the store holds %v, want no flow record and no send budget", keys)
	}
	if len(human.calls) != 1 {
		t.Fatalf("the gate was asked %d times, want once", len(human.calls))
	}
	// The address is the request's, never a value the body could carry.
	if human.calls[0].RemoteIP != "192.0.2.1" {
		t.Errorf("the gate was handed RemoteIP %q, want the request's 192.0.2.1", human.calls[0].RemoteIP)
	}
}

// TestALoginStartWithAValidProofProceeds holds the other side: the same request
// with a proof the gate accepts goes on, the method runs, and the flow exists.
func TestALoginStartWithAValidProofProceeds(t *testing.T) {
	m := phoneMethod()
	m.begin = func(authn.Start) (authn.Step, error) { return authn.StepPrompt("code", nil), nil }
	r := newRig(t, m)
	human := requiredHuman()
	r.p.human = human

	rec := r.send(http.MethodPost, "/api/v1/auth/flows",
		`{"method":"sms","input":{"phone":"+8613800000000"},"human_proof":"proof-1"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("a start with a valid proof answered %d: %s", rec.Code, rec.Body.String())
	}
	if m.calls != 1 {
		t.Errorf("the method was called %d times, want once", m.calls)
	}
	if keys := keysWithPrefix(r.kv, flowKeyPrefix); len(keys) != 1 {
		t.Errorf("the store holds %v, want one flow record", keys)
	}
	if len(human.calls) != 1 || human.calls[0].Token != "proof-1" {
		t.Errorf("the gate was asked %v, want the one proof proof-1", human.calls)
	}
}

// TestAStartThatOnlyAsksForThePhoneNeedsNoProof holds that the gate follows the
// send, not the method: a start whose submission carries no phone sends nothing
// and asks for no proof, so the client can obtain one before the send.
func TestAStartThatOnlyAsksForThePhoneNeedsNoProof(t *testing.T) {
	m := phoneMethod()
	m.begin = func(authn.Start) (authn.Step, error) { return authn.StepPrompt("phone", nil), nil }
	r := newRig(t, m)
	human := requiredHuman()
	r.p.human = human

	rec := r.send(http.MethodPost, "/api/v1/auth/flows", `{"method":"sms"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("a start that only asks for the phone answered %d: %s", rec.Code, rec.Body.String())
	}
	if len(human.calls) != 0 {
		t.Errorf("the gate was asked %v, want not asked for a request that sends nothing", human.calls)
	}
}

// TestTheAuthenticatedStartRefusesAnSMSWithoutAProof holds the step-up's first
// factor: an account with no factor steps up through a method, and a method that
// sends a code is gated before any flow record is written.
func TestTheAuthenticatedStartRefusesAnSMSWithoutAProof(t *testing.T) {
	m := phoneMethod()
	m.begin = func(authn.Start) (authn.Step, error) { return authn.StepPrompt("code", nil), nil }
	r := newRig(t, m)
	r.loginAsAuthenticated("acct1", "sid")
	human := requiredHuman()
	r.p.human = human

	body := `{"purpose":"step-up","method":"sms","input":{"phone":"+8613800000000"}}`
	rec := r.send(http.MethodPost, "/api/v1/auth/flows/authenticated", body)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("a step-up with no proof answered %d: %s", rec.Code, rec.Body.String())
	}
	if got := humanType(t, rec.Body.String()); got != mount.CodeHumanVerificationRequired {
		t.Errorf("the refusal code = %q, want %q", got, mount.CodeHumanVerificationRequired)
	}
	if m.calls != 0 {
		t.Errorf("the method was called %d times, want none", m.calls)
	}
	if keys := r.kv.keys(); len(keys) != 0 {
		t.Errorf("the store holds %v, want no flow record", keys)
	}

	ok := r.send(http.MethodPost, "/api/v1/auth/flows/authenticated",
		`{"purpose":"step-up","method":"sms","input":{"phone":"+8613800000000"},"human_proof":"proof-1"}`)
	if ok.Code != http.StatusOK {
		t.Fatalf("a step-up with a valid proof answered %d: %s", ok.Code, ok.Body.String())
	}
	if m.calls != 1 {
		t.Errorf("the method was called %d times, want once", m.calls)
	}
}

// TestAnAnswerCarryingAPhoneIsRefusedWithoutAProof holds the answer route's
// gate: a first-factor submission that carries the phone the method declares is
// a send, so it is refused without a proof and the step number is left where it
// was, so the same answer may be repeated with one.
func TestAnAnswerCarryingAPhoneIsRefusedWithoutAProof(t *testing.T) {
	m := phoneMethod()
	m.begin = func(authn.Start) (authn.Step, error) {
		return authn.StepPrompt("phone", nil, authn.Field{Name: "phone", Kind: authn.FieldPhone, Required: true}), nil
	}
	m.ans = func(authn.FlowState, authn.StepInput) (authn.Step, error) {
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

	blocked := r.send(http.MethodPost, "/api/v1/auth/flows/answer",
		fmt.Sprintf(`{"seq":%d,"step":"answer","input":{"phone":"+8613800000000"}}`, seq), "Cookie", cookie)
	if blocked.Code != http.StatusForbidden {
		t.Fatalf("an answer with no proof answered %d: %s", blocked.Code, blocked.Body.String())
	}
	if got := humanType(t, blocked.Body.String()); got != mount.CodeHumanVerificationRequired {
		t.Errorf("the refusal code = %q, want %q", got, mount.CodeHumanVerificationRequired)
	}
	if m.calls != 1 {
		t.Errorf("the method was called %d times, want only the start's one: the refused answer sent nothing", m.calls)
	}
	if keys := keysWithPrefix(r.kv, "authn:ip:"); len(keys) != 0 {
		t.Errorf("the source-address budget holds %v, want nothing: a refused proof spends no budget", keys)
	}

	// The refused answer did not advance the flow: the same seq is accepted once
	// a proof is presented.
	ok := r.send(http.MethodPost, "/api/v1/auth/flows/answer",
		fmt.Sprintf(`{"seq":%d,"step":"answer","input":{"phone":"+8613800000000"},"human_proof":"proof-1"}`, seq),
		"Cookie", cookie)
	if ok.Code != http.StatusOK {
		t.Fatalf("the same answer with a valid proof answered %d: %s", ok.Code, ok.Body.String())
	}
	if m.calls != 2 {
		t.Errorf("the method was called %d times, want two", m.calls)
	}
}

// TestTheSMSSecondFactorChallengeIsRefusedWithoutAProof holds the MFA SMS send:
// choosing an SMS factor starts a challenge, which sends a code, so it is gated
// before the factor is asked. The refused call leaves the flow at its step, so
// the same choice succeeds once a proof is presented.
func TestTheSMSSecondFactorChallengeIsRefusedWithoutAProof(t *testing.T) {
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

	blocked := r.send(http.MethodPost, "/api/v1/auth/flows/answer",
		fmt.Sprintf(`{"seq":%d,"step":"factor","input":{"factor":"f1"}}`, seq), "Cookie", cookie)
	if blocked.Code != http.StatusForbidden {
		t.Fatalf("an SMS factor choice with no proof answered %d: %s", blocked.Code, blocked.Body.String())
	}
	if got := humanType(t, blocked.Body.String()); got != mount.CodeHumanVerificationRequired {
		t.Errorf("the refusal code = %q, want %q", got, mount.CodeHumanVerificationRequired)
	}
	if r.mfa.challenges != 0 {
		t.Errorf("the factor challenge was started %d times, want none: a refused proof sends nothing", r.mfa.challenges)
	}

	ok := r.send(http.MethodPost, "/api/v1/auth/flows/answer",
		fmt.Sprintf(`{"seq":%d,"step":"factor","input":{"factor":"f1"},"human_proof":"proof-1"}`, seq),
		"Cookie", cookie)
	if ok.Code != http.StatusOK {
		t.Fatalf("the same factor choice with a valid proof answered %d: %s", ok.Code, ok.Body.String())
	}
	if r.mfa.challenges != 1 {
		t.Errorf("the factor challenge was started %d times, want once", r.mfa.challenges)
	}
}

// TestAFactorChallengeThatSendsNoCodeNeedsNoProof holds that the MFA gate
// follows the kind: a factor that does not send a message is challenged without
// a proof, which is what keeps the gate off a TOTP or WebAuthn login.
func TestAFactorChallengeThatSendsNoCodeNeedsNoProof(t *testing.T) {
	m := &fakeMethod{name: "password"}
	m.begin = func(authn.Start) (authn.Step, error) {
		return authn.StepProven(authn.Proof{Account: "acct1", Methods: []authn.Method{authn.MethodPassword}}), nil
	}
	r := newRig(t, m)
	r.allow("acct1")
	r.mfa.outcome = authn.MFAFactor
	r.mfa.offered = []authn.FactorRecord{{ID: "f1", Kind: authn.FactorTOTP, Label: "Authenticator"}}
	human := requiredHuman()
	r.p.human = human

	start := r.send(http.MethodPost, "/api/v1/auth/flows", `{"method":"password"}`)
	cookie := flowCookie(t, start)
	seq := int(decode(t, start.Body.String())["seq"].(float64))

	ok := r.send(http.MethodPost, "/api/v1/auth/flows/answer",
		fmt.Sprintf(`{"seq":%d,"step":"factor","input":{"factor":"f1"}}`, seq), "Cookie", cookie)
	if ok.Code != http.StatusOK {
		t.Fatalf("a TOTP factor choice without a proof answered %d: %s", ok.Code, ok.Body.String())
	}
	if r.mfa.challenges != 1 {
		t.Errorf("the factor challenge was started %d times, want once", r.mfa.challenges)
	}
	if len(human.calls) != 0 {
		t.Errorf("the gate was asked %v, want not asked for a factor that sends nothing", human.calls)
	}
}

// TestAnOverLongHumanProofIsABadRequest holds the field's own bound: a proof over
// 8 KiB is refused as a bad request before the provider is asked, so an
// unbounded request never reaches it and nothing is sent.
func TestAnOverLongHumanProofIsABadRequest(t *testing.T) {
	m := phoneMethod()
	m.begin = func(authn.Start) (authn.Step, error) { return authn.StepPrompt("code", nil), nil }
	r := newRig(t, m)
	human := requiredHuman()
	r.p.human = human

	proof := strings.Repeat("x", mount.HumanProofMaxBytes+1)
	rec := r.send(http.MethodPost, "/api/v1/auth/flows",
		fmt.Sprintf(`{"method":"sms","input":{"phone":"+8613800000000"},"human_proof":%q}`, proof))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("an over-long proof answered %d: %s", rec.Code, rec.Body.String())
	}
	if got := humanType(t, rec.Body.String()); got != mount.CodeBadRequest {
		t.Errorf("the refusal code = %q, want %q", got, mount.CodeBadRequest)
	}
	if len(human.calls) != 0 {
		t.Errorf("the provider was asked %d times, want none for a proof over the field's bound", len(human.calls))
	}
	if m.calls != 0 {
		t.Errorf("the method was called %d times, want none", m.calls)
	}
	if keys := r.kv.keys(); len(keys) != 0 {
		t.Errorf("the store holds %v, want nothing", keys)
	}
}

// TestAHumanProviderFailureMapsToItsAnswer holds the seam's three failures: an
// unavailable provider or token store is a 503 that sends nothing, and a proof
// the provider itself refuses as too long is a 400. Neither minting a flow nor
// calling the method.
func TestAHumanProviderFailureMapsToItsAnswer(t *testing.T) {
	cases := []struct {
		name string
		err  error
		code int
		typ  string
	}{
		{"a provider that cannot answer", authn.ErrUnavailable, http.StatusServiceUnavailable, mount.CodeUnavailable},
		{"a proof the provider refuses as too long", authn.ErrInvalidArgument, http.StatusBadRequest, mount.CodeBadRequest},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := phoneMethod()
			m.begin = func(authn.Start) (authn.Step, error) { return authn.StepPrompt("code", nil), nil }
			r := newRig(t, m)
			r.p.human = &fakeHuman{required: true, provider: "turnstile", err: fmt.Errorf("provider: %w", tc.err)}

			rec := r.send(http.MethodPost, "/api/v1/auth/flows",
				`{"method":"sms","input":{"phone":"+8613800000000"},"human_proof":"proof-1"}`)
			if rec.Code != tc.code {
				t.Fatalf("a provider failing with %v answered %d, want %d: %s", tc.err, rec.Code, tc.code, rec.Body.String())
			}
			if got := humanType(t, rec.Body.String()); got != tc.typ {
				t.Errorf("the refusal code = %q, want %q", got, tc.typ)
			}
			if m.calls != 0 {
				t.Errorf("the method was called %d times, want none", m.calls)
			}
			if keys := r.kv.keys(); len(keys) != 0 {
				t.Errorf("the store holds %v, want nothing", keys)
			}
		})
	}
}

// TestRegistrationIsNotGated holds the requirement's boundary: a registration
// sends no message, so a deployment that requires a human proof still opens an
// account without one.
func TestRegistrationIsNotGated(t *testing.T) {
	r := registerRig(t)
	human := requiredHuman()
	r.p.human = human

	rec := r.send(http.MethodPost, "/api/v1/auth/register", registerBody)
	if rec.Code != http.StatusCreated {
		t.Fatalf("a registration answered %d, want 201: %s", rec.Code, rec.Body.String())
	}
	if len(human.calls) != 0 {
		t.Errorf("the gate was asked %v, want not asked: registration sends nothing", human.calls)
	}
}

// TestTheMethodsListingReportsTheHumanGate holds the discovery document's human
// block: no gate and the off mechanism both read as off and require nothing, the
// proof of work requires a proof and has no site key, and Turnstile adds the
// site key a widget renders with.
func TestTheMethodsListingReportsTheHumanGate(t *testing.T) {
	cases := []struct {
		name       string
		human      authn.HumanVerifier
		required   bool
		provider   string
		siteKey    string
		hasSiteKey bool
	}{
		{"no gate", nil, false, "off", "", false},
		{"off", &fakeHuman{required: false, provider: "off"}, false, "off", "", false},
		{"proof of work", &fakeHuman{required: true, provider: "pow"}, true, "pow", "", false},
		{"turnstile", &fakeHuman{required: true, provider: "turnstile", siteKey: "site-1"}, true, "turnstile", "site-1", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := newRig(t, &fakeMethod{name: "password"})
			r.p.human = tc.human

			body := decode(t, r.send(http.MethodGet, "/api/v1/auth/methods", "").Body.String())
			published, ok := body["human"].(map[string]any)
			if !ok {
				t.Fatalf("the listing carries no human block: %v", body)
			}
			if got := published["required"]; got != tc.required {
				t.Errorf("human.required = %v, want %v", got, tc.required)
			}
			if got := published["provider"]; got != tc.provider {
				t.Errorf("human.provider = %v, want %v", got, tc.provider)
			}
			siteKey, present := published["site_key"]
			if present != tc.hasSiteKey {
				t.Errorf("human.site_key present = %v, want %v", present, tc.hasSiteKey)
			}
			if present && siteKey != tc.siteKey {
				t.Errorf("human.site_key = %v, want %v", siteKey, tc.siteKey)
			}
		})
	}
}
