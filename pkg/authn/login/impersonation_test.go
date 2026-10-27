package login

import (
	"errors"
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
	r.accounts.byID[actor] = authn.Account{ID: actor, Kind: authn.KindUser, Status: authn.StatusActive}
	r.accounts.byID[target] = authn.Account{ID: target, Kind: authn.KindUser, Status: authn.StatusActive}
	r.imp.grant = authn.ImpersonationGrant{
		ID: "grant-1", Actor: actor, Target: target, Tenant: "tenant-a",
		StartedAt: now, EndsAt: now.Add(30 * time.Minute),
	}
	r.imp.member[target] = true
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

// TestAnImpersonatedEnrollmentCannotCompleteAfterTheRecordLapses holds the
// completion review against the record itself. A record that lapses without End
// leaves its marker on the session row, so every cause that ends a record in
// the background must still stop an enrollment begun during the impersonation
// from landing its terminal action on the target. An explicit End clears the
// marker, and the review refuses it on that ground.
func TestAnImpersonatedEnrollmentCannotCompleteAfterTheRecordLapses(t *testing.T) {
	cases := []struct {
		name   string
		lapsed func(*rig)
	}{
		{"expired", func(r *rig) {
			r.imp.now = func() time.Time { return time.Now().Add(31 * time.Minute) }
		}},
		{"the initiator was disabled", func(r *rig) {
			r.accounts.byID["acct-admin"] = authn.Account{ID: "acct-admin", Kind: authn.KindUser, Status: authn.StatusDisabled}
		}},
		{"the target was disabled", func(r *rig) {
			r.accounts.byID["acct-target"] = authn.Account{ID: "acct-target", Kind: authn.KindUser, Status: authn.StatusDisabled}
		}},
		{"the membership was removed", func(r *rig) {
			delete(r.imp.member, "acct-target")
		}},
		{"explicitly ended, the marker is cleared", func(r *rig) {
			r.sessions.status.ImpersonationID = ""
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := newRig(t, &fakeMethod{name: "password"})
			r.impersonatedAuthenticated("acct-admin", "acct-target", "sid-op")

			start := r.send(http.MethodPost, "/api/v1/auth/flows/authenticated",
				`{"purpose":"enroll","input":{"kind":"totp"}}`)
			if start.Code != http.StatusOK {
				t.Fatalf("POST flows/authenticated answered %d: %s", start.Code, start.Body.String())
			}
			cookie := flowCookie(t, start)
			seq := int(decode(t, start.Body.String())["seq"].(float64))

			c.lapsed(r)
			done := r.send(http.MethodPost, "/api/v1/auth/flows/answer",
				`{"seq":`+itoa(seq)+`,"step":"complete"}`, "Cookie", cookie)
			if done.Code != http.StatusUnauthorized {
				t.Fatalf("the completion after the record lapsed answered %d: %s, want 401 flow-invalid",
					done.Code, done.Body.String())
			}
			if got := decode(t, done.Body.String())["type"]; got != "urn:speed:authn:flow-invalid" {
				t.Errorf("the completion answered type %v, want urn:speed:authn:flow-invalid", got)
			}
			if r.sessions.revokedAll != 0 {
				t.Error("a completion under a lapsed record revoked the target's sessions")
			}
		})
	}
}

// TestAnOwnFlowCompletesOnASessionWithAStaleImpersonationMarker holds the
// non-impersonating branch of the completion review: the flow belongs to the
// account's own session, and a marker a lapsed record left behind is not a
// reason to refuse it. The session's account is the whole test, so an operator
// whose impersonation lapsed can still link, step up and enroll with their own
// session.
func TestAnOwnFlowCompletesOnASessionWithAStaleImpersonationMarker(t *testing.T) {
	r := newRig(t, &fakeMethod{name: "password"})
	r.loginAsAuthenticated("acct1", "sid")
	// A record that lapsed without End leaves its marker on the session row.
	r.sessions.status.ImpersonationID = "grant-stale"

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
		t.Fatalf("the completion on the account's own session answered %d: %s, want 200",
			done.Code, done.Body.String())
	}
	if r.sessions.revokedAccount != "acct1" || r.sessions.except != "sid" {
		t.Errorf("the completion revoked %q except %q, want the account's own sessions except its session",
			r.sessions.revokedAccount, r.sessions.except)
	}
}

// TestAnImpersonatedEnrollmentAuditNamesTheOperator holds the identity the
// registration's terminal audit row is written under. The row is written by the
// multi-factor module on the step that completes a registration, which a client
// submits on the Open answer route: that route carries no request identity, so
// the row can only name the operator if the step is called with an identity
// derived from the flow's record — the effective account, the operator as the
// actor, the grant's tenant and the session. Those are the facts the journal
// turns into the actor column and the on_behalf_of, impersonation_scope and
// session detail keys.
func TestAnImpersonatedEnrollmentAuditNamesTheOperator(t *testing.T) {
	r := newRig(t, &fakeMethod{name: "password"})
	r.impersonatedAuthenticated("acct-admin", "acct-target", "sid-op")
	// The first step prompts; the step that completes the registration is the
	// answer submitted on the Open route, where the request carries no identity.
	r.mfa.enroll = func(authn.FactorKind, authn.StepInput) authn.Step {
		if len(r.mfa.enrolls) == 1 {
			return authn.StepPrompt("code", nil, authn.Field{Name: "code", Kind: authn.FieldCode, Required: true})
		}
		return authn.StepEnrolled(authn.FactorRecord{ID: "f1", Kind: authn.FactorTOTP, Status: authn.FactorActive})
	}

	start := r.send(http.MethodPost, "/api/v1/auth/flows/authenticated",
		`{"purpose":"enroll","input":{"kind":"totp"}}`)
	if start.Code != http.StatusOK {
		t.Fatalf("POST flows/authenticated answered %d: %s", start.Code, start.Body.String())
	}
	cookie := flowCookie(t, start)
	seq := int(decode(t, start.Body.String())["seq"].(float64))
	done := r.send(http.MethodPost, "/api/v1/auth/flows/answer",
		`{"seq":`+itoa(seq)+`,"step":"enroll","input":{"code":"123456"}}`, "Cookie", cookie)
	if done.Code != http.StatusOK {
		t.Fatalf("the completing enrollment step answered %d: %s", done.Code, done.Body.String())
	}

	if len(r.mfa.enrollIDs) != 2 {
		t.Fatalf("MFA.Enroll ran %d times, want the prompt and the completing step", len(r.mfa.enrollIDs))
	}
	if !r.mfa.enrollFound[1] {
		t.Fatal("the completing step ran in the anonymous context of the Open answer route, so its audit row loses the operator")
	}
	requireOperatorIdentity(t, r.mfa.enrollIDs[1])
}

// TestAnOwnEnrollmentKeepsItsAuditShape holds the other side: a registration the
// account's own holder starts is not impersonating anyone, so the completing
// step keeps the anonymous context of the Open answer route and the row keeps
// its shape — no actor invented, and no on_behalf_of, impersonation_scope or
// session detail.
func TestAnOwnEnrollmentKeepsItsAuditShape(t *testing.T) {
	r := newRig(t, &fakeMethod{name: "password"})
	r.loginAsAuthenticated("acct1", "sid")
	r.mfa.enroll = func(authn.FactorKind, authn.StepInput) authn.Step {
		if len(r.mfa.enrolls) == 1 {
			return authn.StepPrompt("code", nil, authn.Field{Name: "code", Kind: authn.FieldCode, Required: true})
		}
		return authn.StepEnrolled(authn.FactorRecord{ID: "f1", Kind: authn.FactorTOTP, Status: authn.FactorActive})
	}

	start := r.send(http.MethodPost, "/api/v1/auth/flows/authenticated",
		`{"purpose":"enroll","input":{"kind":"totp"}}`)
	if start.Code != http.StatusOK {
		t.Fatalf("POST flows/authenticated answered %d: %s", start.Code, start.Body.String())
	}
	cookie := flowCookie(t, start)
	seq := int(decode(t, start.Body.String())["seq"].(float64))
	done := r.send(http.MethodPost, "/api/v1/auth/flows/answer",
		`{"seq":`+itoa(seq)+`,"step":"enroll","input":{"code":"123456"}}`, "Cookie", cookie)
	if done.Code != http.StatusOK {
		t.Fatalf("the completing enrollment step answered %d: %s", done.Code, done.Body.String())
	}

	if len(r.mfa.enrollFound) != 2 {
		t.Fatalf("MFA.Enroll ran %d times, want the prompt and the completing step", len(r.mfa.enrollFound))
	}
	if r.mfa.enrollFound[1] {
		t.Errorf("the completing step ran under the identity %q, want the anonymous context its audit row has always used",
			r.mfa.enrollIDs[1].Key())
	}
}

// TestAnImpersonatedLinkAuditNamesTheOperator holds the two terminal rows of a
// link completed while impersonating. The row's object is the impersonated
// account, its actor is the operator, and the detail carries the journal's three
// impersonation keys with the target, the grant's tenant and the session.
func TestAnImpersonatedLinkAuditNamesTheOperator(t *testing.T) {
	m := &redirectMethod{}
	m.answer = func(authn.FlowState, authn.StepInput) (authn.Step, error) {
		return authn.StepProven(authn.Proof{External: &authn.External{
			Provider: "google", Subject: "g1", Email: "ann@x.org", EmailVerified: true,
		}}), nil
	}
	r := newRigMethod(t, m)
	r.impersonatedAuthenticated("acct-admin", "acct-target", "sid-op")
	sink := &auditing{}
	r.p.audit = sink

	start := r.send(http.MethodPost, "/api/v1/auth/flows/authenticated",
		`{"purpose":"link","method":"password","return_to":"/account/callback"}`)
	if start.Code != http.StatusOK {
		t.Fatalf("POST flows/authenticated answered %d: %s", start.Code, start.Body.String())
	}
	cookie := flowCookie(t, start)
	cb := r.send(http.MethodGet, "/api/v1/auth/callback/password", "", "Cookie", cookie)
	if cb.Code != http.StatusSeeOther {
		t.Fatalf("the link callback answered %d, want 303: %s", cb.Code, cb.Body.String())
	}
	done := r.send(http.MethodPost, "/api/v1/auth/flows/answer", `{"seq":1,"step":"complete"}`, "Cookie", cookie)
	if done.Code != http.StatusOK {
		t.Fatalf("the completing answer answered %d, want 200: %s", done.Code, done.Body.String())
	}

	rows := sink.rows()
	ids, found := sink.writtenUnder()
	index := -1
	for i, row := range rows {
		if row.Action == "authn.binding.link" {
			index = i
			break
		}
	}
	if index < 0 {
		t.Fatalf("the audit holds no authn.binding.link row: %+v", rows)
	}
	row := rows[index]
	if row.Object != "acct-target" || row.Outcome != "success" {
		t.Errorf("the link row = %+v, want object acct-target and outcome success", row)
	}
	if row.Detail["provider"] != "google" {
		t.Errorf("the link row's provider detail = %q, want google", row.Detail["provider"])
	}
	if !found[index] {
		t.Fatal("the link row ran in the anonymous context of the Open reply route, so its actor is the target rather than the operator")
	}
	if got, want := ids[index].Key(), "acct-admin"; got != want {
		t.Errorf("the link row's actor = %q, want the operator %q", got, want)
	}
	requireImpersonationDetail(t, row.Detail)
}

// requireOperatorIdentity holds an identity to the operator's facts under an
// impersonation: the actor key is the operator, the account is the effective
// account, and the grant's tenant and the session are set.
func requireOperatorIdentity(t *testing.T, id authn.Identity) {
	t.Helper()
	scope, scoped := id.Scope()
	if !id.Impersonating() || id.Key() != "acct-admin" || id.Account() != "acct-target" ||
		!scoped || scope != "tenant-a" || id.Session() != "sid-op" {
		t.Errorf("the identity is key %q, account %q, scope %q (scoped %t), session %q (impersonating %t); "+
			"want the operator acct-admin acting for acct-target in tenant-a under sid-op",
			id.Key(), id.Account(), scope, scoped, id.Session(), id.Impersonating())
	}
}

// requireImpersonationDetail holds a completed row's detail to the journal's
// three impersonation keys and the values the impersonated flow fixes.
func requireImpersonationDetail(t *testing.T, detail map[string]string) {
	t.Helper()
	want := map[string]string{
		"on_behalf_of":        "acct-target",
		"impersonation_scope": "tenant-a",
		"session":             "sid-op",
	}
	for key, value := range want {
		if got := detail[key]; got != value {
			t.Errorf("the detail's %s = %q, want %q; detail = %v", key, got, value, detail)
		}
	}
}

// TestAnImpersonatedEnrollmentStepCannotEnableAFactorAfterTheRecordLapses holds
// the enrollment step itself to the impersonation record. The multi-factor
// module makes the pending row active on the step that completes a
// registration, which runs before the flow's completion review; a record that
// lapsed in the background between the start and that step must therefore stop
// the step from running, or the factor lands on the account the flow was
// started for after the authority to act there is gone.
func TestAnImpersonatedEnrollmentStepCannotEnableAFactorAfterTheRecordLapses(t *testing.T) {
	cases := []struct {
		name   string
		lapsed func(*rig)
	}{
		{"expired", func(r *rig) {
			r.imp.now = func() time.Time { return time.Now().Add(31 * time.Minute) }
		}},
		{"the initiator was disabled", func(r *rig) {
			r.accounts.byID["acct-admin"] = authn.Account{ID: "acct-admin", Kind: authn.KindUser, Status: authn.StatusDisabled}
		}},
		{"the target was disabled", func(r *rig) {
			r.accounts.byID["acct-target"] = authn.Account{ID: "acct-target", Kind: authn.KindUser, Status: authn.StatusDisabled}
		}},
		{"the membership was removed", func(r *rig) {
			delete(r.imp.member, "acct-target")
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := newRig(t, &fakeMethod{name: "password"})
			r.impersonatedAuthenticated("acct-admin", "acct-target", "sid-op")
			// The first step asks for the factor's confirmation; the step that
			// makes the row active is the answer submitted after the record lapses.
			enabled := false
			r.mfa.enroll = func(authn.FactorKind, authn.StepInput) authn.Step {
				if len(r.mfa.enrolls) == 1 {
					return authn.StepPrompt("code", nil, authn.Field{Name: "code", Kind: authn.FieldCode, Required: true})
				}
				enabled = true
				return authn.StepEnrolled(authn.FactorRecord{ID: "f1", Kind: authn.FactorTOTP, Status: authn.FactorActive})
			}

			start := r.send(http.MethodPost, "/api/v1/auth/flows/authenticated",
				`{"purpose":"enroll","input":{"kind":"totp"}}`)
			if start.Code != http.StatusOK {
				t.Fatalf("POST flows/authenticated answered %d: %s", start.Code, start.Body.String())
			}
			cookie := flowCookie(t, start)
			seq := int(decode(t, start.Body.String())["seq"].(float64))

			c.lapsed(r)
			done := r.send(http.MethodPost, "/api/v1/auth/flows/answer",
				`{"seq":`+itoa(seq)+`,"step":"enroll","input":{"code":"123456"}}`, "Cookie", cookie)
			if done.Code != http.StatusUnauthorized {
				t.Fatalf("the enrolling step after the record lapsed answered %d: %s, want 401 flow-invalid",
					done.Code, done.Body.String())
			}
			if got := decode(t, done.Body.String())["type"]; got != "urn:speed:authn:flow-invalid" {
				t.Errorf("the enrolling step answered type %v, want urn:speed:authn:flow-invalid", got)
			}
			if enabled {
				t.Error("the enrolling step made the factor active after the record lapsed")
			}
		})
	}
}

// TestAnImpersonatedFlowIsRefusedWithoutTheImpersonationCapability holds the
// absent-dependency direction: an assembly with no impersonation capability
// cannot confirm the record, so the completion of an impersonating flow is
// refused rather than carried through on the session's marker alone.
func TestAnImpersonatedFlowIsRefusedWithoutTheImpersonationCapability(t *testing.T) {
	r := newRig(t, &fakeMethod{name: "password"})
	r.impersonatedAuthenticated("acct-admin", "acct-target", "sid-op")

	start := r.send(http.MethodPost, "/api/v1/auth/flows/authenticated",
		`{"purpose":"enroll","input":{"kind":"totp"}}`)
	if start.Code != http.StatusOK {
		t.Fatalf("POST flows/authenticated answered %d: %s", start.Code, start.Body.String())
	}
	cookie := flowCookie(t, start)
	seq := int(decode(t, start.Body.String())["seq"].(float64))

	r.p.impersonation = nil
	done := r.send(http.MethodPost, "/api/v1/auth/flows/answer",
		`{"seq":`+itoa(seq)+`,"step":"complete"}`, "Cookie", cookie)
	if done.Code != http.StatusUnauthorized {
		t.Fatalf("the completion with no impersonation capability answered %d: %s, want 401 flow-invalid",
			done.Code, done.Body.String())
	}
	if r.sessions.revokedAll != 0 {
		t.Error("a completion with no impersonation capability revoked the target's sessions")
	}
}

// TestAnImpersonatedFlowIsRefusedWhenTheRecordCannotBeRead holds the failure
// direction: a record store that cannot answer is a 503, and the completion's
// terminal action does not run.
func TestAnImpersonatedFlowIsRefusedWhenTheRecordCannotBeRead(t *testing.T) {
	r := newRig(t, &fakeMethod{name: "password"})
	r.impersonatedAuthenticated("acct-admin", "acct-target", "sid-op")

	start := r.send(http.MethodPost, "/api/v1/auth/flows/authenticated",
		`{"purpose":"enroll","input":{"kind":"totp"}}`)
	if start.Code != http.StatusOK {
		t.Fatalf("POST flows/authenticated answered %d: %s", start.Code, start.Body.String())
	}
	cookie := flowCookie(t, start)
	seq := int(decode(t, start.Body.String())["seq"].(float64))

	r.imp.err = errors.New("login test: the record store is down")
	done := r.send(http.MethodPost, "/api/v1/auth/flows/answer",
		`{"seq":`+itoa(seq)+`,"step":"complete"}`, "Cookie", cookie)
	if done.Code != http.StatusServiceUnavailable {
		t.Fatalf("the completion with an unreadable record answered %d: %s, want 503",
			done.Code, done.Body.String())
	}
	if r.sessions.revokedAll != 0 {
		t.Error("a completion with an unreadable record revoked the target's sessions")
	}
}

// TestAnImpersonatedFlowIsRefusedWhenTheRecordBindsAnotherPair holds the
// record's own identity against the flow: the completion re-reads the record and
// refuses when its actor or target is no longer the pair the flow was started
// under, even though the record is still active. The flipped account is seeded
// live, so the refusal comes from the comparison and not from the record
// reading as lapsed.
func TestAnImpersonatedFlowIsRefusedWhenTheRecordBindsAnotherPair(t *testing.T) {
	cases := []struct {
		name string
		flip func(*authn.ImpersonationGrant)
	}{
		{"the record's actor is another account", func(g *authn.ImpersonationGrant) { g.Actor = "acct-other" }},
		{"the record's target is another account", func(g *authn.ImpersonationGrant) { g.Target = "acct-other" }},
	}
	for _, tc := range cases {
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

			r.accounts.byID["acct-other"] = authn.Account{ID: "acct-other", Kind: authn.KindUser, Status: authn.StatusActive}
			r.imp.member["acct-other"] = true
			tc.flip(&r.imp.grant)

			done := r.send(http.MethodPost, "/api/v1/auth/flows/answer",
				`{"seq":`+itoa(seq)+`,"step":"complete"}`, "Cookie", cookie)
			if done.Code != http.StatusUnauthorized {
				t.Fatalf("the completion under a record binding another pair answered %d: %s, want 401 flow-invalid",
					done.Code, done.Body.String())
			}
			if got := decode(t, done.Body.String())["type"]; got != "urn:speed:authn:flow-invalid" {
				t.Errorf("the completion answered type %v, want urn:speed:authn:flow-invalid", got)
			}
			if r.sessions.revokedAll != 0 {
				t.Error("a completion under a record binding another pair revoked the target's sessions")
			}
		})
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
