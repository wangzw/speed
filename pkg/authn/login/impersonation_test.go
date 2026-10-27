package login

import (
	"net/http"
	"testing"
	"time"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/authn/authntest"
)

// impersonatedAuthenticated puts an impersonating identity on the request and
// the operator's session status behind it: the session belongs to the actor and
// names the record, while the identity's account is the target.
func (r *rig) impersonatedAuthenticated(actor, target authn.AccountID, session authn.SessionID) {
	r.t.Helper()
	now := time.Now()
	r.m.As(
		authntest.WithAccount(target),
		authntest.WithActor(actor),
		authntest.WithSession(session),
		authntest.WithScope("tenant-a"),
		authntest.WithMethods(authn.MethodPassword, authn.MethodTOTP),
		authntest.WithAssurance(authn.AssuranceMulti),
		authntest.WithAuthTime(now),
	)
	r.sessions.status = authn.SessionStatus{
		Active: true, Account: actor, Kind: authn.SessionBrowser,
		Methods:   []authn.Method{authn.MethodPassword, authn.MethodTOTP},
		Assurance: authn.AssuranceMulti, AuthTime: now,
		ImpersonationID: "grant-1",
	}
}

// TestAnImpersonatingIdentityStartsAnAuthenticatedFlow holds that the route on
// an already authenticated identity is open to an impersonating identity: the
// session it was started on belongs to the operator and names the record, and
// the flow is recorded for the effective account.
func TestAnImpersonatingIdentityStartsAnAuthenticatedFlow(t *testing.T) {
	r := newRig(t, &fakeMethod{name: "password"})
	r.impersonatedAuthenticated("acct-admin", "acct-target", "sid-op")

	start := r.send(http.MethodPost, "/api/v1/auth/flows/authenticated",
		`{"purpose":"enroll","input":{"kind":"totp"}}`)
	if start.Code != http.StatusOK {
		t.Fatalf("POST flows/authenticated while impersonating = %d: %s", start.Code, start.Body.String())
	}
	if len(r.mfa.enrolls) != 1 {
		t.Errorf("MFA.Enroll ran %d times, want the enrollment to start", len(r.mfa.enrolls))
	}
}

// TestAnImpersonatedStepUpAsksTheTargetsRequirementOfTheOperatorsFactors holds
// the split: the effective account's policy decides that a second factor is
// needed, and the factors offered are the operator's own.
func TestAnImpersonatedStepUpAsksTheTargetsRequirementOfTheOperatorsFactors(t *testing.T) {
	r := newRig(t, &fakeMethod{name: "password"})
	r.impersonatedAuthenticated("acct-admin", "acct-target", "sid-op")
	r.mfa.outcome = authn.MFAFactor
	r.mfa.offered = []authn.FactorRecord{{
		ID: "f1", Kind: authn.FactorTOTP, Status: authn.FactorActive, ActivatedAt: time.Now().Add(-time.Hour),
	}}

	start := r.send(http.MethodPost, "/api/v1/auth/flows/authenticated", `{"purpose":"step-up"}`)
	if start.Code != http.StatusOK {
		t.Fatalf("POST flows/authenticated answered %d: %s", start.Code, start.Body.String())
	}
	want := requireCall{proof: "acct-admin", requirement: "acct-target"}
	if len(r.mfa.required) != 1 || r.mfa.required[0] != want {
		t.Errorf("MFA.Require was asked %+v, want the operator proving under the effective account's policy", r.mfa.required)
	}
}

// TestAnImpersonatedStepUpFromTheFirstFactorProvesTheOperatorsAccount holds
// that a step-up begun at the first factor proves the operator: the method is
// handed the operator's account, and the proof resolves to it while the flow's
// effective account stays the target.
func TestAnImpersonatedStepUpFromTheFirstFactorProvesTheOperatorsAccount(t *testing.T) {
	m := &fakeMethod{name: "password"}
	r := newRig(t, m)
	r.impersonatedAuthenticated("acct-admin", "acct-target", "sid-op")
	r.allow("acct-admin")
	var proved authn.AccountID
	m.begin = func(s authn.Start) (authn.Step, error) {
		proved = s.Account
		return authn.StepPrompt("password", nil, authn.Field{Name: "password", Kind: authn.FieldPassword, Required: true}), nil
	}
	m.ans = func(authn.FlowState, authn.StepInput) (authn.Step, error) {
		return authn.StepProven(authn.Proof{Account: "acct-admin", Methods: []authn.Method{authn.MethodPassword}}), nil
	}

	start := r.send(http.MethodPost, "/api/v1/auth/flows/authenticated",
		`{"purpose":"step-up","method":"password"}`)
	if start.Code != http.StatusOK {
		t.Fatalf("POST flows/authenticated answered %d: %s", start.Code, start.Body.String())
	}
	if proved != "acct-admin" {
		t.Errorf("the first method was asked to prove %q, want the operator acct-admin", proved)
	}
	cookie := flowCookie(t, start)
	seq := int(decode(t, start.Body.String())["seq"].(float64))

	answered := r.send(http.MethodPost, "/api/v1/auth/flows/answer",
		`{"seq":`+itoa(seq)+`,"step":"answer","input":{"password":"secret"}}`, "Cookie", cookie)
	if answered.Code != http.StatusOK {
		t.Fatalf("the first-factor answer answered %d: %s", answered.Code, answered.Body.String())
	}
	seq = int(decode(t, answered.Body.String())["seq"].(float64))

	done := r.send(http.MethodPost, "/api/v1/auth/flows/answer",
		`{"seq":`+itoa(seq)+`,"step":"complete"}`, "Cookie", cookie)
	if done.Code != http.StatusOK {
		t.Fatalf("the step-up completion answered %d: %s", done.Code, done.Body.String())
	}
	if len(r.sessions.elevatedFor) != 1 || r.sessions.elevatedFor[0] != "sid-op" {
		t.Errorf("the step-up elevated %v, want the operator's session sid-op", r.sessions.elevatedFor)
	}
}

// TestAnImpersonatedEnrollmentActsOnTheEffectiveAccount holds that the flow's
// terminal action lands on the impersonated account while the operator's own
// session is the one kept: the enrollment revokes the target's sessions and
// nothing of the operator's.
func TestAnImpersonatedEnrollmentActsOnTheEffectiveAccount(t *testing.T) {
	r := newRig(t, &fakeMethod{name: "password"})
	r.impersonatedAuthenticated("acct-admin", "acct-target", "sid-op")

	start := r.send(http.MethodPost, "/api/v1/auth/flows/authenticated",
		`{"purpose":"enroll","input":{"kind":"totp"}}`)
	if start.Code != http.StatusOK {
		t.Fatalf("POST flows/authenticated answered %d: %s", start.Code, start.Body.String())
	}
	cookie := flowCookie(t, start)
	seq := int(decode(t, start.Body.String())["seq"].(float64))

	done := r.send(http.MethodPost, "/api/v1/auth/flows/answer",
		`{"seq":`+itoa(seq)+`,"step":"complete"}`, "Cookie", cookie)
	if done.Code != http.StatusOK {
		t.Fatalf("the enrollment completion answered %d: %s", done.Code, done.Body.String())
	}
	if r.sessions.revokedAccount != "acct-target" || r.sessions.except != "sid-op" {
		t.Errorf("the completion revoked %q except %q, want the effective account's sessions except the operator's",
			r.sessions.revokedAccount, r.sessions.except)
	}
}

// TestAnImpersonatedFlowCannotCompleteUnderADifferentRecord holds the completion
// re-check against the effective account: a session that no longer names the
// record the flow was started under, whether because the impersonation ended or
// another one began, cannot carry the flow onto the account it was started for.
func TestAnImpersonatedFlowCannotCompleteUnderADifferentRecord(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(*authn.SessionStatus)
	}{
		{"the impersonation ended", func(s *authn.SessionStatus) { s.ImpersonationID = "" }},
		{"another record began", func(s *authn.SessionStatus) { s.ImpersonationID = "grant-2" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := newRig(t, &fakeMethod{name: "password"})
			r.impersonatedAuthenticated("acct-admin", "acct-target", "sid-op")

			start := r.send(http.MethodPost, "/api/v1/auth/flows/authenticated",
				`{"purpose":"enroll","input":{"kind":"totp"}}`)
			if start.Code != http.StatusOK {
				t.Fatalf("POST flows/authenticated answered %d: %s", start.Code, start.Body.String())
			}
			cookie := flowCookie(t, start)
			seq := int(decode(t, start.Body.String())["seq"].(float64))

			tc.change(&r.sessions.status)
			done := r.send(http.MethodPost, "/api/v1/auth/flows/answer",
				`{"seq":`+itoa(seq)+`,"step":"complete"}`, "Cookie", cookie)
			if done.Code != http.StatusUnauthorized {
				t.Fatalf("the completion answered %d: %s, want 401 flow-invalid", done.Code, done.Body.String())
			}
			if r.sessions.revokedAll != 0 {
				t.Error("a completion under a different record revoked the target's sessions")
			}
		})
	}
}
