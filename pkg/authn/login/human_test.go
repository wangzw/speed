package login

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/vislake/speed/pkg/authn"
)

// humanType is the problem code a response body carries, without the type
// prefix.
func humanType(t *testing.T, body string) string {
	t.Helper()
	return strings.TrimPrefix(fmt.Sprint(decode(t, body)["type"]), "urn:speed:authn:")
}

// fakeHuman is a human-verification gate a case scripts: whether it requires a
// proof, the mechanism it names, and the tokens it accepts. Every call is
// recorded, so a case can see that the engine never asks the gate.
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

// TestTheEngineForwardsTheStartProofWithoutCheckingIt holds the start route's
// side of the gate: the request's proof reaches the method in Start, and the
// engine does not ask the gate. A proof is single-use, so a caller-side check
// here would consume it before the code engine's own check.
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

// TestTheEngineForwardsAnAnswerProofWithoutCheckingIt holds the answer route:
// an answer that carries a phone reaches the method with the proof in its
// FlowState, and the gate is never asked.
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

// TestTheEngineForwardsAFactorChallengeProofWithoutCheckingIt holds the second
// factor: choosing an SMS factor hands the factor the request's proof in its
// FlowState, and the engine does not ask the gate itself.
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

// TestAStartWithNoProofReachesTheMethod holds the other direction: the engine
// no longer refuses a send for want of a proof. The code engine makes that
// decision, and the method is called with an empty proof so the code engine can
// answer it.
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
