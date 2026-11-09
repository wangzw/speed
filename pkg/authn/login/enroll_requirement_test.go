package login

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"testing"

	"github.com/vislake/speed/pkg/authn"
)

// tenantSeam is the authn.TenantMFARequirement seam a case scripts.
type tenantSeam struct {
	required bool
	err      error
}

func (s tenantSeam) RequiresMFA(context.Context, authn.AccountID) (bool, error) {
	return s.required, s.err
}

// tenantEnrollmentRig builds a login whose account "acct1" has no factor, whose
// method proves a password, and whose policy requires a registration. The first
// Require call answers as the policy does before the registration; every later
// one offers the factor the registration created, as the real policy does once
// the account holds an active factor.
func tenantEnrollmentRig(t *testing.T) *rig {
	t.Helper()
	m := &fakeMethod{name: "password"}
	m.begin = func(authn.Start) (authn.Step, error) {
		return authn.StepPrompt("credentials", nil,
			authn.Field{Name: "identifier", Kind: authn.FieldText, Required: true},
			authn.Field{Name: "password", Kind: authn.FieldPassword, Required: true, Secret: true}), nil
	}
	m.ans = func(authn.FlowState, authn.StepInput) (authn.Step, error) {
		return authn.StepProven(authn.Proof{
			Account: "acct1",
			Methods: []authn.Method{authn.MethodPassword},
		}), nil
	}
	r := newRig(t, m)
	r.allow("acct1")

	steps := 0
	r.mfa.enroll = func(_ authn.FactorKind, _ authn.StepInput) authn.Step {
		steps++
		if steps == 1 {
			return authn.StepPrompt("confirm", nil,
				authn.Field{Name: "code", Kind: authn.FieldCode, Required: true})
		}
		return authn.StepEnrolled(authn.FactorRecord{ID: "f1", Kind: authn.FactorTOTP, Status: authn.FactorActive})
	}
	calls := 0
	r.mfa.require = func() (authn.MFAOutcome, []authn.FactorRecord, error) {
		calls++
		if calls == 1 {
			return authn.MFAEnroll, nil, nil
		}
		return authn.MFAFactor, []authn.FactorRecord{
			{ID: "f1", Kind: authn.FactorTOTP, Status: authn.FactorActive},
		}, nil
	}
	return r
}

// answerStep answers one flow step and returns the decoded reply.
func answerStep(t *testing.T, r *rig, cookie string, seq int, step, input string) map[string]any {
	t.Helper()
	body := fmt.Sprintf(`{"seq":%d,"step":%q,"input":%s}`, seq, step, input)
	rec := r.send(http.MethodPost, "/api/v1/auth/flows/answer", body, "Cookie", cookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("the %s step answered %d: %s", step, rec.Code, rec.Body.String())
	}
	return decode(t, rec.Body.String())
}

func seqOf(t *testing.T, reply map[string]any) int {
	t.Helper()
	seq, ok := reply["seq"].(float64)
	if !ok {
		t.Fatalf("the reply carries no sequence number: %v", reply)
	}
	return int(seq)
}

func stateOf(t *testing.T, reply map[string]any) string {
	t.Helper()
	state, ok := reply["state"].(string)
	if !ok {
		t.Fatalf("the reply carries no state: %v", reply)
	}
	return state
}

func nextKindOf(t *testing.T, reply map[string]any) string {
	t.Helper()
	next, ok := reply["next"].(map[string]any)
	if !ok {
		t.Fatalf("the reply carries no next step: %v", reply)
	}
	kind, ok := next["kind"].(string)
	if !ok {
		t.Fatalf("the next step carries no kind: %v", next)
	}
	return kind
}

// TestATenantRequirementMakesTheLoginRegisterAndThenProveBothFactors holds the
// product rule: a tenant's require-mfa makes a login with no factor register
// one, then prove the password again and use the new factor, and only then
// records a multi-factor session.
func TestATenantRequirementMakesTheLoginRegisterAndThenProveBothFactors(t *testing.T) {
	r := tenantEnrollmentRig(t)
	r.p.tenantMFA = tenantSeam{required: true}
	var second *authn.Pass
	r.mfa.assure = func(_ authn.Proof, s *authn.Pass) authn.MFAResult {
		second = s
		if s == nil {
			return authn.MFAResult{Assurance: authn.AssuranceSingle}
		}
		return authn.MFAResult{Assurance: authn.AssuranceMulti}
	}

	start := r.send(http.MethodPost, "/api/v1/auth/flows", `{"method":"password"}`)
	if start.Code != http.StatusOK {
		t.Fatalf("the start answered %d: %s", start.Code, start.Body.String())
	}
	cookie := flowCookie(t, start)
	started := decode(t, start.Body.String())

	rec := answerStep(t, r, cookie, seqOf(t, started), "answer", `{}`)
	if got := stateOf(t, rec); got != stateEnroll {
		t.Fatalf("the first factor left the flow at %q, want enroll", got)
	}
	if got := nextKindOf(t, rec); got != nextEnroll {
		t.Fatalf("the flow asked for %q after enrolling, want enroll", got)
	}

	rec = answerStep(t, r, cookie, seqOf(t, rec), "enroll", `{"kind":"totp"}`)
	if got := stateOf(t, rec); got != stateEnroll {
		t.Fatalf("the registration's first step left the flow at %q, want enroll", got)
	}

	rec = answerStep(t, r, cookie, seqOf(t, rec), "enroll", `{"code":"123456"}`)
	if got := stateOf(t, rec); got != stateReprove {
		t.Fatalf("completing the registration left the flow at %q, want reprove: the tenant requirement makes the login prove the password again", got)
	}
	if got := nextKindOf(t, rec); got != nextChallenge {
		t.Fatalf("the reprove step asked for %q, want challenge", got)
	}

	rec = answerStep(t, r, cookie, seqOf(t, rec), "answer", `{"identifier":"acct1","password":"secret"}`)
	if got := stateOf(t, rec); got != stateSecond {
		t.Fatalf("proving the password again left the flow at %q, want second", got)
	}
	if got := nextKindOf(t, rec); got != nextFactor {
		t.Fatalf("the re-proof asked for %q, want factor", got)
	}

	rec = answerStep(t, r, cookie, seqOf(t, rec), "factor", `{"factor":"f1"}`)
	if got := stateOf(t, rec); got != stateSecond {
		t.Fatalf("starting the factor's challenge left the flow at %q, want second", got)
	}

	rec = answerStep(t, r, cookie, seqOf(t, rec), "factor", `{"factor":"f1","code":"123456"}`)
	if got := stateOf(t, rec); got != stateReady {
		t.Fatalf("the second factor left the flow at %q, want ready", got)
	}

	done := answerStep(t, r, cookie, seqOf(t, rec), "complete", `{}`)
	if got := stateOf(t, done); got != stateAuthenticated {
		t.Fatalf("the completion answered %q, want authenticated", got)
	}
	if len(r.sessions.created) != 1 {
		t.Fatalf("the completion created %d sessions, want one", len(r.sessions.created))
	}
	if got := r.sessions.created[0].Assurance; got != authn.AssuranceMulti {
		t.Errorf("the session's assurance is %v, want multi: the password and the new factor were both proved", got)
	}
	if second == nil {
		t.Fatal("the completion composed the assurance from no second factor; the newly registered factor was never used")
	}
	if got := second.Methods; len(got) != 1 || got[0] != authn.MethodTOTP {
		t.Errorf("the second factor's methods are %v, want [totp]", got)
	}
}

// TestAnEnrollmentWithoutATenantRequirementCompletesSingle holds that the
// pre-existing behaviour is kept where no tenant requires a factor: a policy
// that asks an account to register completes the login as a single factor once
// the registration lands.
func TestAnEnrollmentWithoutATenantRequirementCompletesSingle(t *testing.T) {
	r := tenantEnrollmentRig(t)
	r.p.tenantMFA = tenantSeam{required: false}
	var second *authn.Pass
	r.mfa.assure = func(_ authn.Proof, s *authn.Pass) authn.MFAResult {
		second = s
		return authn.MFAResult{Assurance: authn.AssuranceSingle}
	}

	start := r.send(http.MethodPost, "/api/v1/auth/flows", `{"method":"password"}`)
	if start.Code != http.StatusOK {
		t.Fatalf("the start answered %d: %s", start.Code, start.Body.String())
	}
	cookie := flowCookie(t, start)
	rec := answerStep(t, r, cookie, seqOf(t, decode(t, start.Body.String())), "answer", `{}`)
	rec = answerStep(t, r, cookie, seqOf(t, rec), "enroll", `{"kind":"totp"}`)
	rec = answerStep(t, r, cookie, seqOf(t, rec), "enroll", `{"code":"123456"}`)
	if got := stateOf(t, rec); got != stateReady {
		t.Fatalf("the registration left the flow at %q, want ready: no tenant requires a factor", got)
	}
	if got := nextKindOf(t, rec); got != nextComplete {
		t.Fatalf("the flow asked for %q, want complete", got)
	}
	done := answerStep(t, r, cookie, seqOf(t, rec), "complete", `{}`)
	if got := stateOf(t, done); got != stateAuthenticated {
		t.Fatalf("the completion answered %q, want authenticated", got)
	}
	if second != nil {
		t.Errorf("the completion composed the assurance from %+v, want no second factor", second)
	}
}

// TestATenantRequirementThatCannotBeReadRefusesTheLogin holds the failure
// direction: a seam read that fails does not let the login through as a single
// factor. The answer is the dependency's 503.
func TestATenantRequirementThatCannotBeReadRefusesTheLogin(t *testing.T) {
	r := tenantEnrollmentRig(t)
	r.p.tenantMFA = tenantSeam{err: errors.New("settings unavailable")}

	start := r.send(http.MethodPost, "/api/v1/auth/flows", `{"method":"password"}`)
	if start.Code != http.StatusOK {
		t.Fatalf("the start answered %d: %s", start.Code, start.Body.String())
	}
	cookie := flowCookie(t, start)
	body := fmt.Sprintf(`{"seq":%d,"step":"answer","input":{}}`, seqOf(t, decode(t, start.Body.String())))
	rec := r.send(http.MethodPost, "/api/v1/auth/flows/answer", body, "Cookie", cookie)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("a requirement that cannot be read answered %d, want 503: %s", rec.Code, rec.Body.String())
	}
	if len(r.sessions.created) != 0 {
		t.Errorf("the login created %d sessions on a requirement it could not establish, want none", len(r.sessions.created))
	}
}

// TestTheReprovedPasswordMustProveTheSameAccount holds the account the flow was
// bound to: proving another account's password at the reprove step is refused,
// so a factor is never enabled for an account this login did not prove.
func TestTheReprovedPasswordMustProveTheSameAccount(t *testing.T) {
	r := tenantEnrollmentRig(t)
	r.p.tenantMFA = tenantSeam{required: true}
	r.allow("acct2")
	// The first answer proves the account the flow is bound to; the reproof
	// proves another account whose credentials are equally valid.
	answers := 0
	r.p.methods["password"].(*fakeMethod).ans = func(authn.FlowState, authn.StepInput) (authn.Step, error) {
		answers++
		id := authn.AccountID("acct1")
		if answers > 1 {
			id = "acct2"
		}
		return authn.StepProven(authn.Proof{Account: id, Methods: []authn.Method{authn.MethodPassword}}), nil
	}

	start := r.send(http.MethodPost, "/api/v1/auth/flows", `{"method":"password"}`)
	if start.Code != http.StatusOK {
		t.Fatalf("the start answered %d: %s", start.Code, start.Body.String())
	}
	cookie := flowCookie(t, start)
	rec := answerStep(t, r, cookie, seqOf(t, decode(t, start.Body.String())), "answer", `{}`)
	rec = answerStep(t, r, cookie, seqOf(t, rec), "enroll", `{"kind":"totp"}`)
	rec = answerStep(t, r, cookie, seqOf(t, rec), "enroll", `{"code":"123456"}`)
	if got := stateOf(t, rec); got != stateReprove {
		t.Fatalf("the registration left the flow at %q, want reprove", got)
	}

	body := fmt.Sprintf(`{"seq":%d,"step":"answer","input":{"identifier":"acct2","password":"other"}}`, seqOf(t, rec))
	refused := r.send(http.MethodPost, "/api/v1/auth/flows/answer", body, "Cookie", cookie)
	if refused.Code != http.StatusUnauthorized {
		t.Fatalf("a reproof of another account answered %d, want 401: %s", refused.Code, refused.Body.String())
	}
	if len(r.sessions.created) != 0 {
		t.Errorf("the login created %d sessions after a reproof of another account, want none", len(r.sessions.created))
	}
}
