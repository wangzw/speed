package login

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"testing"

	"github.com/vislake/speed/pkg/authn"
)

type abandoningMFA struct {
	*fakeMFA
	states    []authn.FlowState
	err       error
	panicWith any
}

func (m *abandoningMFA) Abandon(_ context.Context, f authn.FlowState) error {
	m.states = append(m.states, f)
	if m.panicWith != nil {
		panic(m.panicWith)
	}
	return m.err
}

type abandoningMethod struct {
	*fakeMethod
	states []authn.FlowState
}

func (m *abandoningMethod) Abandon(_ context.Context, f authn.FlowState) error {
	m.states = append(m.states, f)
	return nil
}

func startEnrollment(t *testing.T, r *rig) (string, int) {
	t.Helper()
	r.loginAsAuthenticated("acct1", "sid")
	start := r.send(http.MethodPost, "/api/v1/auth/flows/authenticated", `{"purpose":"enroll","input":{"kind":"totp"}}`)
	if start.Code != http.StatusOK {
		t.Fatalf("POST flows/authenticated answered %d: %s", start.Code, start.Body.String())
	}
	return flowCookie(t, start), int(decode(t, start.Body.String())["seq"].(float64))
}

func promptWithPrivate(private string) authn.Step {
	return authn.StepPrompt("confirm", []byte(private),
		authn.Field{Name: "code", Kind: authn.FieldCode, Required: true})
}

// TestAbandonHandsTheFlowsPrivateStateToItsModule holds that deleting a flow releases its state to the owning module and removes the record.
func TestAbandonHandsTheFlowsPrivateStateToItsModule(t *testing.T) {
	r := newRig(t, &fakeMethod{name: "password"})
	mfa := &abandoningMFA{fakeMFA: r.mfa}
	r.p.mfa = mfa
	r.mfa.enroll = func(authn.FactorKind, authn.StepInput) authn.Step {
		return promptWithPrivate("row-state")
	}
	cookie, _ := startEnrollment(t, r)

	del := r.send(http.MethodDelete, "/api/v1/auth/flows/current", "", "Cookie", cookie)
	if del.Code != http.StatusNoContent {
		t.Fatalf("DELETE flows/current answered %d: %s", del.Code, del.Body.String())
	}
	if len(mfa.states) != 1 {
		t.Fatalf("the module was asked to release %d flows, want one", len(mfa.states))
	}
	if got := string(mfa.states[0].Private); got != "row-state" {
		t.Errorf("the module was handed the private state %q, want the flow's own %q", got, "row-state")
	}
	if got := mfa.states[0].Account; got != "acct1" {
		t.Errorf("the module was handed the account %q, want acct1", got)
	}
	if got := mfa.states[0].Purpose; got != authn.PurposeEnroll {
		t.Errorf("the module was handed the purpose %q, want enroll", got)
	}

	if after := r.send(http.MethodGet, "/api/v1/auth/flows/current", "", "Cookie", cookie); after.Code != http.StatusUnauthorized {
		t.Errorf("the abandoned flow still answers %d, want 401: the record was not removed", after.Code)
	}
	if again := r.send(http.MethodDelete, "/api/v1/auth/flows/current", "", "Cookie", cookie); again.Code != http.StatusUnauthorized {
		t.Errorf("deleting the gone flow again answered %d, want 401", again.Code)
	}
	if len(mfa.states) != 1 {
		t.Errorf("the module was asked to release %d flows after a second delete, want the flow to be released once", len(mfa.states))
	}
}

// TestAbandonHandsALoginMethodsFlowToTheMethod holds that a first-factor flow's state is released through its method.
func TestAbandonHandsALoginMethodsFlowToTheMethod(t *testing.T) {
	m := &abandoningMethod{fakeMethod: &fakeMethod{name: "password"}}
	m.begin = func(authn.Start) (authn.Step, error) {
		return promptWithPrivate("method-state"), nil
	}
	r := newRigMethod(t, m)

	start := r.send(http.MethodPost, "/api/v1/auth/flows", `{"method":"password"}`)
	if start.Code != http.StatusOK {
		t.Fatalf("POST flows answered %d: %s", start.Code, start.Body.String())
	}
	cookie := flowCookie(t, start)
	if del := r.send(http.MethodDelete, "/api/v1/auth/flows/current", "", "Cookie", cookie); del.Code != http.StatusNoContent {
		t.Fatalf("DELETE flows/current answered %d: %s", del.Code, del.Body.String())
	}
	if len(m.states) != 1 {
		t.Fatalf("the method was asked to release %d flows, want one", len(m.states))
	}
	if got := string(m.states[0].Private); got != "method-state" {
		t.Errorf("the method was handed the private state %q, want the flow's own %q", got, "method-state")
	}
}

type abandoningRedirectMethod struct {
	*redirectMethod
	states []authn.FlowState
}

func (m *abandoningRedirectMethod) Abandon(_ context.Context, f authn.FlowState) error {
	m.states = append(m.states, f)
	return nil
}

// TestAbandonHandsAProcessingRedirectFlowsStateToItsMethod holds the reserved seam: a processing redirect flow's upstream transaction is released through its method.
func TestAbandonHandsAProcessingRedirectFlowsStateToItsMethod(t *testing.T) {
	m := &abandoningRedirectMethod{redirectMethod: &redirectMethod{}}
	r := newRigMethod(t, m)
	r.allow("acct1")
	cookie := startRedirect(t, r, "")
	editFlowRecord(t, r, cookie, func(rec *record) {
		rec.State = stateProcessing
		rec.MethodState = []byte("upstream-transaction")
	})

	del := r.send(http.MethodDelete, "/api/v1/auth/flows/current", "", "Cookie", cookie)
	if del.Code != http.StatusNoContent {
		t.Fatalf("DELETE flows/current answered %d: %s", del.Code, del.Body.String())
	}
	if len(m.states) != 1 {
		t.Fatalf("the method was asked to release %d flows, want one", len(m.states))
	}
	if got := string(m.states[0].Private); got != "upstream-transaction" {
		t.Errorf("the method was handed the private state %q, want the flow's own %q", got, "upstream-transaction")
	}
	if after := r.send(http.MethodGet, "/api/v1/auth/flows/current", "", "Cookie", cookie); after.Code != http.StatusUnauthorized {
		t.Errorf("the abandoned flow still answers %d, want 401: the record was not removed", after.Code)
	}
}

// TestAFailedAbandonCallbackStillRemovesTheFlowAndIsLogged holds that a failed release still removes the record, logs it, and leaves a later answer invalid.
func TestAFailedAbandonCallbackStillRemovesTheFlowAndIsLogged(t *testing.T) {
	r := newRig(t, &fakeMethod{name: "password"})
	mfa := &abandoningMFA{fakeMFA: r.mfa, err: errors.New("the release failed")}
	r.p.mfa = mfa
	r.mfa.enroll = func(authn.FactorKind, authn.StepInput) authn.Step {
		return promptWithPrivate("row-state")
	}
	var logs bytes.Buffer
	r.p.logger = slog.New(slog.NewTextHandler(&logs, nil))
	cookie, seq := startEnrollment(t, r)

	del := r.send(http.MethodDelete, "/api/v1/auth/flows/current", "", "Cookie", cookie)
	if del.Code != http.StatusNoContent {
		t.Fatalf("DELETE flows/current with a failing release answered %d: %s", del.Code, del.Body.String())
	}
	after := r.send(http.MethodPost, "/api/v1/auth/flows/answer",
		`{"seq":`+itoa(seq)+`,"step":"enroll","input":{"code":"000000"}}`, "Cookie", cookie)
	if after.Code != http.StatusUnauthorized {
		t.Errorf("an answer after the failed release answered %d, want 401: the record was not removed", after.Code)
	}
	logged := logs.String()
	if !strings.Contains(logged, "level=WARN") || !strings.Contains(logged, "the release failed") {
		t.Errorf("the failed release was not logged at WARN with its cause: %q", logged)
	}
}

// TestAPanickingAbandonCallbackIsLoggedAndStillRemovesTheFlow holds that a panicking release is recovered, logged at ERROR and the record still removed.
func TestAPanickingAbandonCallbackIsLoggedAndStillRemovesTheFlow(t *testing.T) {
	r := newRig(t, &fakeMethod{name: "password"})
	mfa := &abandoningMFA{fakeMFA: r.mfa, panicWith: "the release exploded"}
	r.p.mfa = mfa
	r.mfa.enroll = func(authn.FactorKind, authn.StepInput) authn.Step {
		return promptWithPrivate("row-state")
	}
	var logs bytes.Buffer
	r.p.logger = slog.New(slog.NewTextHandler(&logs, nil))
	cookie, seq := startEnrollment(t, r)

	del := r.send(http.MethodDelete, "/api/v1/auth/flows/current", "", "Cookie", cookie)
	if del.Code != http.StatusNoContent {
		t.Fatalf("DELETE flows/current with a panicking release answered %d: %s", del.Code, del.Body.String())
	}
	if len(mfa.states) != 1 {
		t.Errorf("the module was asked to release %d flows, want one", len(mfa.states))
	}
	after := r.send(http.MethodPost, "/api/v1/auth/flows/answer",
		`{"seq":`+itoa(seq)+`,"step":"enroll","input":{"code":"000000"}}`, "Cookie", cookie)
	if after.Code != http.StatusUnauthorized {
		t.Errorf("an answer after the panicking release answered %d, want 401: the record was not removed", after.Code)
	}
	logged := logs.String()
	if !strings.Contains(logged, "level=ERROR") ||
		!strings.Contains(logged, "abandon callback panicked") ||
		!strings.Contains(logged, "the release exploded") {
		t.Errorf("the panicking release was not logged at ERROR with its cause: %q", logged)
	}
}
