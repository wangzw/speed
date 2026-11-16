package login

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"testing"
	"time"

	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/authn/internal/mount"
	"github.com/vislake/speed/pkg/authn/internal/step"
	"github.com/vislake/speed/pkg/core"
)

// readerWith builds a settings reader over the given values, with the disabled
// methods defaulted to none so a read of the mandatory switch succeeds.
func readerWith(values map[string]string) *reader {
	all := map[string]string{disabledMethodsPath: "[]"}
	for k, v := range values {
		all[k] = v
	}
	return newReader(&fakeStore{values: all}, slog.New(slog.NewTextHandler(ioDiscard{}, nil)))
}

// sendBudgetRig builds a rig whose answer reports the given send counts, one per
// answer, against the default per-flow budget.
func sendBudgetRig(t *testing.T, reports []int) *rig {
	t.Helper()
	m := &fakeMethod{name: "password"}
	m.begin = func(authn.Start) (authn.Step, error) { return authn.StepPrompt("credentials", nil), nil }
	i := 0
	m.ans = func(authn.FlowState, authn.StepInput) (authn.Step, error) {
		n := 0
		if i < len(reports) {
			n = reports[i]
		}
		i++
		return authn.StepPrompt("credentials", nil).WithSends(n), nil
	}
	return newRig(t, m)
}

// TestThePerFlowSendBudgetHolds holds the per-flow send budget: a report that is
// exactly what is left is allowed, more than is left swaps the flow to its
// tombstone and answers the method's outage, a negative report spends nothing,
// and what earlier answers spent is subtracted.
func TestThePerFlowSendBudgetHolds(t *testing.T) {
	cases := []struct {
		name    string
		reports []int
		want    []int
	}{
		{"exactly what is left is allowed", []int{3, 0}, []int{http.StatusOK, http.StatusOK}},
		{"more than is left is refused", []int{4}, []int{http.StatusServiceUnavailable}},
		{"a negative report spends nothing", []int{-1, 4}, []int{http.StatusOK, http.StatusServiceUnavailable}},
		{"what was spent is subtracted", []int{2, 2}, []int{http.StatusOK, http.StatusServiceUnavailable}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := sendBudgetRig(t, c.reports)
			rec := r.send(http.MethodPost, "/api/v1/auth/flows", `{"method":"password"}`)
			cookie := flowCookie(t, rec)
			for i, want := range c.want {
				out := r.send(http.MethodPost, "/api/v1/auth/flows/answer",
					`{"seq":`+itoa(i+1)+`,"step":"answer","input":{}}`, "Cookie", cookie)
				if out.Code != want {
					t.Errorf("answer %d answered %d, want %d: %s", i+1, out.Code, want, out.Body.String())
				}
			}
		})
	}
}

// TestAnOverstatedSendReportLeavesATombstone holds that a report that would widen
// the budget ends the flow: the record is swapped to its tombstone, so a later
// answer is refused.
func TestAnOverstatedSendReportLeavesATombstone(t *testing.T) {
	r := sendBudgetRig(t, []int{9})
	rec := r.send(http.MethodPost, "/api/v1/auth/flows", `{"method":"password"}`)
	cookie := flowCookie(t, rec)
	if out := r.send(http.MethodPost, "/api/v1/auth/flows/answer", `{"seq":1,"step":"answer","input":{}}`, "Cookie", cookie); out.Code != http.StatusServiceUnavailable {
		t.Fatalf("an overstated report answered %d, want 503: %s", out.Code, out.Body.String())
	}
	if after := r.send(http.MethodGet, "/api/v1/auth/flows/current", "", "Cookie", cookie); after.Code != http.StatusUnauthorized {
		t.Errorf("the flow after an overstated report answered %d, want 401: the record was not tombstoned", after.Code)
	}
}

// TestARefusedEnrollmentKeepsItsStepForTheSameSequence holds that a wrong
// confirmation code leaves the registration at its step with its sequence number
// unchanged, so the corrected code answers the same number and completes the
// registration.
func TestARefusedEnrollmentKeepsItsStepForTheSameSequence(t *testing.T) {
	m := &fakeMethod{name: "password"}
	r := newRig(t, m)
	r.loginAsAuthenticated("acct1", "sid")
	n := 0
	r.mfa.enroll = func(_ authn.FactorKind, _ authn.StepInput) authn.Step {
		n++
		switch n {
		case 1:
			return authn.StepPrompt("confirm", nil, authn.Field{Name: "code", Kind: authn.FieldCode, Required: true})
		case 2:
			return authn.StepRefused(authn.ErrInvalidCredentials)
		default:
			return authn.StepEnrolled(authn.FactorRecord{ID: "f1", Kind: authn.FactorTOTP, Status: authn.FactorActive})
		}
	}

	start := r.send(http.MethodPost, "/api/v1/auth/flows/authenticated", `{"purpose":"enroll","input":{"kind":"totp"}}`)
	if start.Code != http.StatusOK {
		t.Fatalf("POST flows/authenticated answered %d: %s", start.Code, start.Body.String())
	}
	cookie := flowCookie(t, start)
	seq := int(decode(t, start.Body.String())["seq"].(float64))

	refused := r.send(http.MethodPost, "/api/v1/auth/flows/answer",
		`{"seq":`+itoa(seq)+`,"step":"enroll","input":{"code":"000000"}}`, "Cookie", cookie)
	if refused.Code != http.StatusUnauthorized {
		t.Fatalf("a refused confirmation answered %d: %s", refused.Code, refused.Body.String())
	}
	if _, carried := decode(t, refused.Body.String())["seq"]; carried {
		t.Errorf("the refusal carried a sequence number: %s", refused.Body.String())
	}

	enrolled := r.send(http.MethodPost, "/api/v1/auth/flows/answer",
		`{"seq":`+itoa(seq)+`,"step":"enroll","input":{"code":"123456"}}`, "Cookie", cookie)
	if enrolled.Code != http.StatusOK {
		t.Fatalf("the corrected confirmation with the same seq answered %d: %s", enrolled.Code, enrolled.Body.String())
	}
	if next := decode(t, enrolled.Body.String())["next"].(map[string]any); next["kind"] != "complete" {
		t.Errorf("the completed registration moved to next %v, want complete", next)
	}
}

// TestApplyEnrollForwardsTheClientInput holds that the enroll step hands the
// client's own input to MFA.Enroll, so a factor's confirmation step sees its own
// fields.
func TestApplyEnrollForwardsTheClientInput(t *testing.T) {
	m := &fakeMethod{name: "password"}
	r := newRig(t, m)
	r.loginAsAuthenticated("acct1", "sid")
	n := 0
	r.mfa.enroll = func(_ authn.FactorKind, _ authn.StepInput) authn.Step {
		n++
		if n == 1 {
			return authn.StepPrompt("confirm", nil, authn.Field{Name: "code", Kind: authn.FieldCode, Required: true})
		}
		return authn.StepEnrolled(authn.FactorRecord{ID: "f1", Kind: authn.FactorTOTP, Status: authn.FactorActive})
	}

	start := r.send(http.MethodPost, "/api/v1/auth/flows/authenticated", `{"purpose":"enroll","input":{"kind":"totp"}}`)
	if start.Code != http.StatusOK {
		t.Fatalf("POST flows/authenticated answered %d: %s", start.Code, start.Body.String())
	}
	cookie := flowCookie(t, start)
	seq := int(decode(t, start.Body.String())["seq"].(float64))

	ans := r.send(http.MethodPost, "/api/v1/auth/flows/answer",
		`{"seq":`+itoa(seq)+`,"step":"enroll","input":{"code":"123456"}}`, "Cookie", cookie)
	if ans.Code != http.StatusOK {
		t.Fatalf("the enroll answer answered %d: %s", ans.Code, ans.Body.String())
	}
	if len(r.mfa.enrolls) != 2 {
		t.Fatalf("MFA.Enroll ran %d times, want two", len(r.mfa.enrolls))
	}
	if got := r.mfa.enrolls[1].Fields["code"]; got != "123456" {
		t.Errorf("the confirmation step's input was dropped: MFA.Enroll saw %v", r.mfa.enrolls[1].Fields)
	}
}

// TestAnEnrollmentRefusedForAMissingPhoneIsThePhoneRequiredProblem holds that the
// SMS factor's missing-phone precondition is not answered as a failed login: the
// enrollment answers the 403 phone-required that tells the client to verify a
// phone first, just as a malformed input keeps its own bad-request.
func TestAnEnrollmentRefusedForAMissingPhoneIsThePhoneRequiredProblem(t *testing.T) {
	m := &fakeMethod{name: "password"}
	r := newRig(t, m)
	r.loginAsAuthenticated("acct1", "sid")
	r.mfa.enroll = func(_ authn.FactorKind, _ authn.StepInput) authn.Step {
		return authn.StepRefused(fmt.Errorf("%w: the account has no proven phone", authn.ErrPhoneRequired))
	}

	start := r.send(http.MethodPost, "/api/v1/auth/flows/authenticated", `{"purpose":"enroll","input":{"kind":"sms"}}`)
	if start.Code != http.StatusForbidden {
		t.Fatalf("an enrollment without a proven phone answered %d, want 403: %s", start.Code, start.Body.String())
	}
	if got := decode(t, start.Body.String())["type"]; got != "urn:speed:authn:phone-required" {
		t.Errorf("the refusal wrote problem type %v, want urn:speed:authn:phone-required", got)
	}
}

// TestAnAnswerCarriesAFieldLargerThanAStartField holds the two input bounds
// apart: a WebAuthn credential's JSON runs past the 512-byte bound a start
// field carries, and an answer that carries one must reach the method rather
// than be refused as a bad request. The start bound and the answer bound were
// once the same constant, so a credential was refused 400 on every passkey
// step.
func TestAnAnswerCarriesAFieldLargerThanAStartField(t *testing.T) {
	reached := false
	m := &fakeMethod{
		name: "passkey",
		desc: authn.MethodDescriptor{Name: "passkey", Kind: authn.MethodKindWebAuthn},
		begin: func(authn.Start) (authn.Step, error) {
			return authn.StepData("passkey", nil, map[string]string{"challenge": "abc"}), nil
		},
		ans: func(_ authn.FlowState, in authn.StepInput) (authn.Step, error) {
			reached = true
			if len(in.Fields["credential"]) <= maxStartFieldBytes {
				t.Errorf("the method saw a credential of %d bytes, not one clearing the start bound",
					len(in.Fields["credential"]))
			}
			return authn.StepRefused(errors.New("done")), nil
		},
	}
	r := newRig(t, m)
	start := r.send(http.MethodPost, "/api/v1/auth/flows", `{"method":"passkey"}`)
	if start.Code != http.StatusOK {
		t.Fatalf("POST flows answered %d: %s", start.Code, start.Body.String())
	}
	cookie := flowCookie(t, start)
	seq := int(decode(t, start.Body.String())["seq"].(float64))
	credential := strings.Repeat("A", 1100)
	body := `{"seq":` + itoa(seq) + `,"step":"answer","input":{"credential":"` + credential + `"}}`
	out := r.send(http.MethodPost, "/api/v1/auth/flows/answer", body, "Cookie", cookie)
	if out.Code == http.StatusBadRequest {
		t.Fatalf("an answer carrying a %d-byte credential was refused as a bad request: %s", len(credential), out.Body.String())
	}
	if !reached {
		t.Fatal("the answer never reached the method")
	}
}

// TestASingleFactorEnrollmentCompletesAfterItsOwnFactorTurnsActive holds that a
// registration does not fail on the very factor enablement it just caused: the
// multi-factor module makes a pending factor active on the factor's last step,
// before the engine's completion, and the completion's freshness re-check reads
// the requirement the flow fixed at its start -- the fresh first factor a
// factorless account asked for -- not one recomputed against the account's new
// factor.
func TestASingleFactorEnrollmentCompletesAfterItsOwnFactorTurnsActive(t *testing.T) {
	m := &fakeMethod{name: "password"}
	r := newRig(t, m)
	r.loginAsAuthenticated("acct1", "sid")
	n := 0
	r.mfa.enroll = func(_ authn.FactorKind, _ authn.StepInput) authn.Step {
		n++
		if n == 1 {
			return authn.StepPrompt("confirm", nil, authn.Field{Name: "code", Kind: authn.FieldCode, Required: true})
		}
		// The last step enables the factor, as MFA.Enroll does: the account now
		// holds an active factor by the time the completion's review runs.
		r.mfa.factors = []authn.FactorRecord{{ID: "f1", Kind: authn.FactorTOTP, Status: authn.FactorActive}}
		return authn.StepEnrolled(authn.FactorRecord{ID: "f1", Kind: authn.FactorTOTP, Status: authn.FactorActive})
	}

	start := r.send(http.MethodPost, "/api/v1/auth/flows/authenticated", `{"purpose":"enroll","input":{"kind":"totp"}}`)
	if start.Code != http.StatusOK {
		t.Fatalf("POST flows/authenticated answered %d: %s", start.Code, start.Body.String())
	}
	cookie := flowCookie(t, start)
	seq := int(decode(t, start.Body.String())["seq"].(float64))
	ans := r.send(http.MethodPost, "/api/v1/auth/flows/answer",
		`{"seq":`+itoa(seq)+`,"step":"enroll","input":{"code":"123456"}}`, "Cookie", cookie)
	if ans.Code != http.StatusOK {
		t.Fatalf("the enroll answer answered %d: %s", ans.Code, ans.Body.String())
	}
	nextSeq := int(decode(t, ans.Body.String())["seq"].(float64))
	done := r.send(http.MethodPost, "/api/v1/auth/flows/answer",
		`{"seq":`+itoa(nextSeq)+`,"step":"complete"}`, "Cookie", cookie)
	if done.Code != http.StatusOK {
		t.Fatalf("the completion answered %d, want 200: %s", done.Code, done.Body.String())
	}
}

// TestTheCompletionReviewRefusesAnotherAccountsSession holds the guard the
// session's own status feeds: a session that now belongs to another account
// cannot carry the flow onto the account that started it. A marker left by a
// lapsed impersonation is not this guard's business, because the flow is not
// impersonating and the session still belongs to the account that started it.
func TestTheCompletionReviewRefusesAnotherAccountsSession(t *testing.T) {
	m := &fakeMethod{name: "password"}
	r := newRig(t, m)
	r.loginAsAuthenticated("acct1", "sid")
	start := r.send(http.MethodPost, "/api/v1/auth/flows/authenticated", `{"purpose":"enroll","input":{"kind":"totp"}}`)
	if start.Code != http.StatusOK {
		t.Fatalf("POST flows/authenticated answered %d: %s", start.Code, start.Body.String())
	}
	cookie := flowCookie(t, start)
	seq := int(decode(t, start.Body.String())["seq"].(float64))
	r.sessions.status.Account = "other"

	done := r.send(http.MethodPost, "/api/v1/auth/flows/answer", `{"seq":`+itoa(seq)+`,"step":"complete"}`, "Cookie", cookie)
	if done.Code != http.StatusUnauthorized {
		t.Errorf("the completion answered %d, want 401: %s", done.Code, done.Body.String())
	}
}

// TestTheEnrollNextStepCarriesTheKinds holds that a login whose policy requires a
// factor answers next.kind=enroll with the kinds the assembly takes in.
func TestTheEnrollNextStepCarriesTheKinds(t *testing.T) {
	m := &fakeMethod{name: "password"}
	m.begin = func(authn.Start) (authn.Step, error) {
		return authn.StepProven(authn.Proof{Account: "acct1", Methods: []authn.Method{authn.MethodPassword}}), nil
	}
	r := newRig(t, m)
	r.allow("acct1")
	r.mfa.outcome = authn.MFAEnroll

	rec := r.send(http.MethodPost, "/api/v1/auth/flows", `{"method":"password"}`)
	next := decode(t, rec.Body.String())["next"].(map[string]any)
	if next["kind"] != "enroll" {
		t.Fatalf("a login the policy requires a factor of answered next %v, want enroll", next)
	}
	kinds, ok := next["kinds"].([]any)
	if !ok || len(kinds) != 1 || kinds[0] != "totp" {
		t.Errorf("the enroll next step carries kinds %v, want [totp]", next["kinds"])
	}
}

// TestTheFirstRegistrationAnswerDropsTheFirstFactorsStepState holds that the
// private state a first factor carried while it proved does not reach the
// registration as that step's own in-progress state: the enrollment's first
// answer is handed an empty Private, so the multi-factor module starts a
// registration instead of looking for a pending factor it never made. A
// redirect first factor (social or enterprise) leaves such a state behind, and
// a deployment that requires a factor parks that login at enroll.
func TestTheFirstRegistrationAnswerDropsTheFirstFactorsStepState(t *testing.T) {
	m := &fakeMethod{name: "password"}
	m.begin = func(authn.Start) (authn.Step, error) {
		return authn.StepPrompt("code", []byte(`{"state":"first-factor"}`),
			authn.Field{Name: "code", Kind: authn.FieldCode, Required: true}), nil
	}
	m.ans = func(authn.FlowState, authn.StepInput) (authn.Step, error) {
		return authn.StepProven(authn.Proof{
			Account: "acct1",
			Methods: []authn.Method{authn.MethodPassword},
		}), nil
	}
	r := newRig(t, m)
	r.allow("acct1")
	r.mfa.outcome = authn.MFAEnroll

	start := r.send(http.MethodPost, "/api/v1/auth/flows", `{"method":"password"}`)
	if start.Code != http.StatusOK {
		t.Fatalf("POST flows answered %d: %s", start.Code, start.Body.String())
	}
	cookie := flowCookie(t, start)
	startSeq := int(decode(t, start.Body.String())["seq"].(float64))
	first := r.send(http.MethodPost, "/api/v1/auth/flows/answer",
		`{"seq":`+itoa(startSeq)+`,"step":"answer","input":{"code":"1"}}`, "Cookie", cookie)
	if first.Code != http.StatusOK {
		t.Fatalf("the first factor's answer answered %d: %s", first.Code, first.Body.String())
	}
	next, ok := decode(t, first.Body.String())["next"].(map[string]any)
	if !ok || next["kind"] != "enroll" {
		t.Fatalf("a login the policy requires a factor of answered next %v, want enroll", next)
	}

	seq := int(decode(t, first.Body.String())["seq"].(float64))
	answer := r.send(http.MethodPost, "/api/v1/auth/flows/answer",
		`{"seq":`+itoa(seq)+`,"step":"enroll","input":{"kind":"totp"}}`, "Cookie", cookie)
	if answer.Code != http.StatusOK {
		t.Fatalf("the first registration answer answered %d: %s", answer.Code, answer.Body.String())
	}
	if len(r.mfa.enrollStates) != 1 {
		t.Fatalf("MFA.Enroll ran %d times, want one", len(r.mfa.enrollStates))
	}
	if got := r.mfa.enrollStates[0].Private; len(got) != 0 {
		t.Errorf("the registration was handed the first factor's private state %q, want none", got)
	}
}

// TestLoginWithNoUsableFactorIsRefused holds that a login whose account has
// factors but none the engine can offer is a credential failure, not an account
// with no factor.
func TestLoginWithNoUsableFactorIsRefused(t *testing.T) {
	m := &fakeMethod{name: "password"}
	m.begin = func(authn.Start) (authn.Step, error) {
		return authn.StepProven(authn.Proof{Account: "acct1", Methods: []authn.Method{authn.MethodPassword}}), nil
	}
	r := newRig(t, m)
	r.allow("acct1")
	r.mfa.outcome = authn.MFAFactor
	r.mfa.offered = nil

	rec := r.send(http.MethodPost, "/api/v1/auth/flows", `{"method":"password"}`)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("a login with no usable factor answered %d, want 401: %s", rec.Code, rec.Body.String())
	}
}

// TestStartAuthenticatedRefusesAGoneSession holds the session check of the
// authenticated start: a missing or revoked session is a flow-invalid 401, and a
// status that cannot be read is a 503.
func TestStartAuthenticatedRefusesAGoneSession(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want int
	}{
		{"a missing session", authn.ErrSessionNotFound, http.StatusUnauthorized},
		{"a revoked session", authn.ErrSessionRevoked, http.StatusUnauthorized},
		{"an unreadable status", errors.New("boom"), http.StatusServiceUnavailable},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			m := &fakeMethod{name: "password"}
			r := newRig(t, m)
			r.loginAsAuthenticated("acct1", "sid")
			r.sessions.statusErr = c.err
			rec := r.send(http.MethodPost, "/api/v1/auth/flows/authenticated", `{"purpose":"enroll","input":{"kind":"totp"}}`)
			if rec.Code != c.want {
				t.Errorf("%s answered %d, want %d: %s", c.name, rec.Code, c.want, rec.Body.String())
			}
		})
	}
}

// TestTheDeliveryModeSettingNarrowsWhatIsAllowed holds that a delivery mode
// outside the configured set is refused even when the capability is present.
func TestTheDeliveryModeSettingNarrowsWhatIsAllowed(t *testing.T) {
	m := &fakeMethod{name: "password"}
	m.begin = func(authn.Start) (authn.Step, error) { return authn.StepPrompt("credentials", nil), nil }
	r := newRig(t, m)
	r.p.tokens = &fakeTokens{}
	r.p.settings = readerWith(map[string]string{deliveryModesPath: `["session"]`})

	rec := r.send(http.MethodPost, "/api/v1/auth/flows", `{"method":"password","delivery":"token"}`)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("a delivery mode outside the setting answered %d, want 400: %s", rec.Code, rec.Body.String())
	}
}

// TestAnActiveFactorRaisesTheLinkRequirement holds that an account with an active
// factor is held to a fresh local multi-factor authentication before a link,
// which a single-factor session does not satisfy.
func TestAnActiveFactorRaisesTheLinkRequirement(t *testing.T) {
	m := &fakeMethod{name: "password"}
	r := newRig(t, m)
	r.loginAsAuthenticated("acct1", "sid")
	r.mfa.factors = []authn.FactorRecord{{ID: "f1", Kind: authn.FactorTOTP, Status: authn.FactorActive}}

	rec := r.send(http.MethodPost, "/api/v1/auth/flows/authenticated", `{"purpose":"link","method":"password"}`)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("a link on a single-factor session answered %d, want 401: %s", rec.Code, rec.Body.String())
	}
}

// TestTheSourceAddressPreThrottleHolds holds the boundary of the source-address
// pre-throttle: the sends up to the limit are served and the one past it is
// throttled before the method runs, so it sends nothing.
func TestTheSourceAddressPreThrottleHolds(t *testing.T) {
	m := &fakeMethod{name: "password"}
	m.begin = func(authn.Start) (authn.Step, error) {
		return authn.StepPrompt("code", nil).WithSends(1), nil
	}
	r := newRig(t, m)
	r.p.settings = readerWith(map[string]string{ModuleName + "." + ipLimitKey: "2"})

	for i := 1; i <= 2; i++ {
		if rec := r.send(http.MethodPost, "/api/v1/auth/flows", `{"method":"password"}`); rec.Code != http.StatusOK {
			t.Fatalf("send %d answered %d, want 200: %s", i, rec.Code, rec.Body.String())
		}
	}
	if rec := r.send(http.MethodPost, "/api/v1/auth/flows", `{"method":"password"}`); rec.Code != http.StatusTooManyRequests {
		t.Errorf("the send past the limit answered %d, want 429: %s", rec.Code, rec.Body.String())
	}
	// The refused request never reached the method, so it sent no message.
	if m.calls != 2 {
		t.Errorf("the method ran %d times, want two: the send past the limit still sent a message", m.calls)
	}
}

// TestTheSourceAddressPreThrottleCountsSendsNotStarts holds that the allowance
// is spent by a verification message, not by opening a flow: a flow that sends
// nothing spends nothing, and a flow that sends a code spends one.
func TestTheSourceAddressPreThrottleCountsSendsNotStarts(t *testing.T) {
	m := &fakeMethod{name: "password"}
	m.begin = func(s authn.Start) (authn.Step, error) {
		if s.Fields["phone"] == "" {
			return authn.StepPrompt("phone", nil), nil
		}
		return authn.StepPrompt("code", nil).WithSends(1), nil
	}
	r := newRig(t, m)
	r.p.settings = readerWith(map[string]string{ModuleName + "." + ipLimitKey: "1"})

	// Opening two flows that send nothing must not spend the allowance.
	for i := 1; i <= 2; i++ {
		if rec := r.send(http.MethodPost, "/api/v1/auth/flows", `{"method":"password"}`); rec.Code != http.StatusOK {
			t.Fatalf("flow %d that sends nothing answered %d, want 200: %s", i, rec.Code, rec.Body.String())
		}
	}
	// A flow that sends one code spends the one available send.
	if rec := r.send(http.MethodPost, "/api/v1/auth/flows",
		`{"method":"password","input":{"phone":"+15550001111"}}`); rec.Code != http.StatusOK {
		t.Fatalf("the flow that sends one code answered %d, want 200: %s", rec.Code, rec.Body.String())
	}
	if rec := r.send(http.MethodPost, "/api/v1/auth/flows",
		`{"method":"password","input":{"phone":"+15550001111"}}`); rec.Code != http.StatusTooManyRequests {
		t.Errorf("the code past the limit answered %d, want 429: %s", rec.Code, rec.Body.String())
	}
	// Three flows reached the method; the fourth was refused before it did.
	if m.calls != 3 {
		t.Errorf("the method ran %d times, want three: the refused flow still sent a message", m.calls)
	}
}

// TestTheSourceAddressPreThrottleCountsEveryReportedSend holds that the counter
// follows the method's report: a call that reports two sends spends two, so a
// limit of ten is reached after five such calls, and the counter reads back a
// two-digit value on the way.
func TestTheSourceAddressPreThrottleCountsEveryReportedSend(t *testing.T) {
	m := &fakeMethod{name: "password"}
	m.begin = func(authn.Start) (authn.Step, error) {
		return authn.StepPrompt("code", nil).WithSends(2), nil
	}
	r := newRig(t, m)
	r.p.settings = readerWith(map[string]string{ModuleName + "." + ipLimitKey: "10"})

	for i := 1; i <= 5; i++ {
		if rec := r.send(http.MethodPost, "/api/v1/auth/flows", `{"method":"password"}`); rec.Code != http.StatusOK {
			t.Fatalf("two-send flow %d answered %d, want 200: %s", i, rec.Code, rec.Body.String())
		}
	}
	if rec := r.send(http.MethodPost, "/api/v1/auth/flows", `{"method":"password"}`); rec.Code != http.StatusTooManyRequests {
		t.Errorf("the flow past the limit answered %d, want 429: %s", rec.Code, rec.Body.String())
	}
	if m.calls != 5 {
		t.Errorf("the method ran %d times, want five: the counter did not spend two per call", m.calls)
	}
}

// TestASourceAddressPreThrottleThatIsOffCountsNothing holds the layer turned off
// (limit 0): every send is served and no counter is written for the address.
func TestASourceAddressPreThrottleThatIsOffCountsNothing(t *testing.T) {
	m := &fakeMethod{name: "password"}
	m.begin = func(authn.Start) (authn.Step, error) {
		return authn.StepPrompt("code", nil).WithSends(1), nil
	}
	r := newRig(t, m)
	r.p.settings = readerWith(map[string]string{ModuleName + "." + ipLimitKey: "0"})

	for i := 1; i <= 5; i++ {
		if rec := r.send(http.MethodPost, "/api/v1/auth/flows", `{"method":"password"}`); rec.Code != http.StatusOK {
			t.Fatalf("send %d with the layer off answered %d, want 200: %s", i, rec.Code, rec.Body.String())
		}
	}
	for _, key := range r.kv.keys() {
		if strings.HasPrefix(key, "authn:ip:") {
			t.Errorf("the layer wrote a counter while it was off: %s", key)
		}
	}
}

// TestAnUnreadablePreThrottleCounterIsSkipped holds the layer's fail-open when
// the counter cannot be read: it skips the layer and writes a Warn, so the sends
// are still served.
func TestAnUnreadablePreThrottleCounterIsSkipped(t *testing.T) {
	m := &fakeMethod{name: "password"}
	m.begin = func(authn.Start) (authn.Step, error) {
		return authn.StepPrompt("code", nil).WithSends(1), nil
	}
	r := newRig(t, m)
	var buf bytes.Buffer
	r.p.logger = slog.New(slog.NewTextHandler(&buf, nil))
	r.p.settings = readerWith(map[string]string{ModuleName + "." + ipLimitKey: "5"})
	r.kv.failGetPrefix = "authn:ip:"

	for i := 1; i <= 3; i++ {
		if rec := r.send(http.MethodPost, "/api/v1/auth/flows", `{"method":"password"}`); rec.Code != http.StatusOK {
			t.Fatalf("send %d with an unreadable counter answered %d, want 200: %s", i, rec.Code, rec.Body.String())
		}
	}
	if !strings.Contains(buf.String(), "could not read") {
		t.Errorf("the skipped layer wrote no Warn: %s", buf.String())
	}
}

// TestAnUnwritablePreThrottleCounterIsSkipped holds the same fail-open when the
// counter cannot be charged after a send.
func TestAnUnwritablePreThrottleCounterIsSkipped(t *testing.T) {
	m := &fakeMethod{name: "password"}
	m.begin = func(authn.Start) (authn.Step, error) {
		return authn.StepPrompt("code", nil).WithSends(1), nil
	}
	r := newRig(t, m)
	var buf bytes.Buffer
	r.p.logger = slog.New(slog.NewTextHandler(&buf, nil))
	r.p.settings = readerWith(map[string]string{ModuleName + "." + ipLimitKey: "1"})
	r.kv.failIncrPrefix = "authn:ip:"

	for i := 1; i <= 3; i++ {
		if rec := r.send(http.MethodPost, "/api/v1/auth/flows", `{"method":"password"}`); rec.Code != http.StatusOK {
			t.Fatalf("send %d with an unwritable counter answered %d, want 200: %s", i, rec.Code, rec.Body.String())
		}
	}
	if !strings.Contains(buf.String(), "could not count") {
		t.Errorf("the skipped layer wrote no Warn: %s", buf.String())
	}
}

// TestTokenDeliveryIssuesATokenPair holds the token delivery: the session made is
// a token session and the answer carries the access token, the refresh token and
// a non-negative expiry.
func TestTokenDeliveryIssuesATokenPair(t *testing.T) {
	m := &fakeMethod{name: "password"}
	m.begin = func(authn.Start) (authn.Step, error) {
		return authn.StepProven(authn.Proof{Account: "acct1", Methods: []authn.Method{authn.MethodPassword}}), nil
	}
	r := newRig(t, m)
	r.allow("acct1")
	r.p.tokens = &fakeTokens{}

	start := r.send(http.MethodPost, "/api/v1/auth/flows", `{"method":"password","delivery":"token"}`)
	if start.Code != http.StatusOK {
		t.Fatalf("POST flows answered %d: %s", start.Code, start.Body.String())
	}
	cookie := flowCookie(t, start)
	done := r.send(http.MethodPost, "/api/v1/auth/flows/answer", `{"seq":0,"step":"complete"}`, "Cookie", cookie)
	if done.Code != http.StatusOK {
		t.Fatalf("the token completion answered %d: %s", done.Code, done.Body.String())
	}
	if len(r.sessions.created) != 1 || r.sessions.created[0].Kind != authn.SessionToken {
		t.Fatalf("the token login made a session of kind %v, want a token session", r.sessions.created)
	}
	body := decode(t, done.Body.String())
	if body["access_token"] != "access-1" || body["refresh_token"] != "refresh-1" {
		t.Errorf("the token answer carries %v, want the issued pair", body)
	}
	if exp, ok := body["expires_in"].(float64); !ok || exp < 0 {
		t.Errorf("the token answer carries expires_in %v, want a non-negative number", body["expires_in"])
	}
}

// TestASessionExpiryIsWrittenInUTC holds the wire rule for the login session
// answer: the expiry leaves as a UTC instant whose text ends in Z, even when
// the issued instant carries another zone.
func TestASessionExpiryIsWrittenInUTC(t *testing.T) {
	m := &fakeMethod{name: "password"}
	m.begin = func(authn.Start) (authn.Step, error) {
		return authn.StepProven(authn.Proof{Account: "acct1", Methods: []authn.Method{authn.MethodPassword}}), nil
	}
	r := newRig(t, m)
	r.allow("acct1")
	r.sessions.expires = time.Date(2027, 3, 4, 5, 6, 7, 0, time.FixedZone("UTC+8", 8*60*60))

	start := r.send(http.MethodPost, "/api/v1/auth/flows", `{"method":"password"}`)
	if start.Code != http.StatusOK {
		t.Fatalf("POST flows answered %d: %s", start.Code, start.Body.String())
	}
	cookie := flowCookie(t, start)
	done := r.send(http.MethodPost, "/api/v1/auth/flows/answer", `{"seq":0,"step":"complete"}`, "Cookie", cookie)
	if done.Code != http.StatusOK {
		t.Fatalf("the session completion answered %d: %s", done.Code, done.Body.String())
	}
	if body := done.Body.String(); !strings.Contains(body, `"expires_at":"2027-03-03T21:06:07Z"`) {
		t.Errorf("the session answer is %s, want expires_at written in UTC ending in Z", body)
	}
}

// TestMethodsAndCurrentBehave holds the two listing routes: GET methods hides a
// disabled method, and GET flows/current refuses a tombstone and echoes the flow
// token of a header-bound flow.
func TestMethodsAndCurrentBehave(t *testing.T) {
	m := &fakeMethod{name: "password"}
	m.begin = func(authn.Start) (authn.Step, error) { return authn.StepPrompt("credentials", nil), nil }
	r := newRig(t, m)

	listing := r.send(http.MethodGet, "/api/v1/auth/methods", "")
	if body := decode(t, listing.Body.String())["methods"].([]any); len(body) != 1 ||
		body[0].(map[string]any)["name"] != "password" {
		t.Errorf("GET methods = %v, want one descriptor named password", listing.Body.String())
	}

	// The password method is never hidden by the disabled-methods switch, not
	// even by a raw value that names it.
	hidden := newRig(t, m)
	hidden.p.settings = readerWithDisabled(t, "password")
	got := hidden.send(http.MethodGet, "/api/v1/auth/methods", "")
	if body := decode(t, got.Body.String())["methods"].([]any); len(body) != 1 ||
		body[0].(map[string]any)["name"] != "password" {
		t.Errorf("GET methods with password named by the switch = %v, want the password descriptor kept", got.Body.String())
	}

	// A header-bound flow echoes its token in the flow reply and in a resume.
	header := r.send(http.MethodPost, "/api/v1/auth/flows", `{"method":"password","binding":"header"}`)
	tok, ok := decode(t, header.Body.String())["flow_token"].(string)
	if !ok || tok == "" {
		t.Fatalf("a header-bound start echoed no flow token: %s", header.Body.String())
	}
	current := r.send(http.MethodGet, "/api/v1/auth/flows/current", "", "Authn-Flow", tok)
	if got := decode(t, current.Body.String())["flow_token"]; got != tok {
		t.Errorf("GET flows/current echoed flow_token %v, want %q", got, tok)
	}
}

// TestTheStartupDiagnosticWarnsWhenThePreThrottleIsOff holds the relaxation
// warning: the source-address pre-throttle being off is written as a Warn of its
// own, and turning it on writes the proxy-bucket hint instead.
func TestTheStartupDiagnosticWarnsWhenThePreThrottleIsOff(t *testing.T) {
	cases := []struct {
		name  string
		limit string
		want  bool
	}{
		{"off", "0", true},
		{"on", "5", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			m := &fakeMethod{name: "password"}
			r := newRig(t, m)
			var buf bytes.Buffer
			r.p.logger = slog.New(slog.NewTextHandler(&buf, nil))
			r.p.settings = readerWith(map[string]string{ModuleName + "." + ipLimitKey: c.limit})
			r.p.diagnose(core.New())
			got := strings.Contains(buf.String(), "pre-throttle is off")
			if got != c.want {
				t.Errorf("the off warning present = %v, want %v: %s", got, c.want, buf.String())
			}
		})
	}
}

// TestASecondFactorMustBeOneTheFlowOffered holds the second-factor selection
// guards: a factor the flow did not offer is a flow-invalid 401, and an empty
// factor is the 400 of a malformed step.
func TestASecondFactorMustBeOneTheFlowOffered(t *testing.T) {
	m := &fakeMethod{name: "password"}
	m.begin = func(authn.Start) (authn.Step, error) {
		return authn.StepProven(authn.Proof{Account: "acct1", Methods: []authn.Method{authn.MethodPassword}}), nil
	}
	r := newRig(t, m)
	r.allow("acct1")
	r.mfa.outcome = authn.MFAFactor
	r.mfa.offered = []authn.FactorRecord{{ID: "f1", Kind: authn.FactorTOTP}}

	rec := r.send(http.MethodPost, "/api/v1/auth/flows", `{"method":"password"}`)
	cookie := flowCookie(t, rec)

	notOffered := r.send(http.MethodPost, "/api/v1/auth/flows/answer",
		`{"seq":0,"step":"factor","input":{"factor":"nope"}}`, "Cookie", cookie)
	if notOffered.Code != http.StatusUnauthorized {
		t.Errorf("a factor the flow did not offer answered %d, want 401: %s", notOffered.Code, notOffered.Body.String())
	}
	empty := r.send(http.MethodPost, "/api/v1/auth/flows/answer",
		`{"seq":0,"step":"factor","input":{"factor":""}}`, "Cookie", cookie)
	if empty.Code != http.StatusBadRequest {
		t.Errorf("an empty factor answered %d, want 400: %s", empty.Code, empty.Body.String())
	}
}

// TestTheAnswerRouteDistinguishesItsRefusals holds that the 401 of a wrong step
// number and the 401 of a refused credential carry different challenges, and
// that an unknown step is the 400 the answer route writes.
func TestTheAnswerRouteDistinguishesItsRefusals(t *testing.T) {
	m := &fakeMethod{name: "password"}
	m.begin = func(authn.Start) (authn.Step, error) { return authn.StepPrompt("credentials", nil), nil }
	m.ans = func(authn.FlowState, authn.StepInput) (authn.Step, error) { return authn.StepRefused(errBoom), nil }
	r := newRig(t, m)
	cookie := startPassword(t, r)

	bogus := r.send(http.MethodPost, "/api/v1/auth/flows/answer", `{"seq":1,"step":"bogus","input":{}}`, "Cookie", cookie)
	if bogus.Code != http.StatusBadRequest {
		t.Errorf("an unknown step answered %d, want 400: %s", bogus.Code, bogus.Body.String())
	}
	wrongSeq := r.send(http.MethodPost, "/api/v1/auth/flows/answer", `{"seq":99,"step":"answer","input":{}}`, "Cookie", cookie)
	failed := r.send(http.MethodPost, "/api/v1/auth/flows/answer", `{"seq":1,"step":"answer","input":{}}`, "Cookie", cookie)
	if wrongSeq.Code != http.StatusUnauthorized || failed.Code != http.StatusUnauthorized {
		t.Fatalf("wrong-seq answered %d and login-failed %d, want both 401", wrongSeq.Code, failed.Code)
	}
	if !strings.Contains(wrongSeq.Body.String(), "flow-invalid") {
		t.Errorf("a wrong step number answered %s, want the flow-invalid problem", wrongSeq.Body.String())
	}
	if !strings.Contains(failed.Body.String(), "login-failed") {
		t.Errorf("a refused credential answered %s, want the login-failed problem", failed.Body.String())
	}
}

// TestTheAssuranceNamesAndTheIDPKeyBoundArePinned holds the two small mappings
// the login event carries: the assurance level's name, and that an idp.* key of
// exactly the maximum length is kept.
func TestTheAssuranceNamesAndTheIDPKeyBoundArePinned(t *testing.T) {
	if got := assuranceName(authn.AssuranceSingle); got != "single" {
		t.Errorf("AssuranceSingle is named %q, want single", got)
	}
	if got := assuranceName(authn.AssuranceMulti); got != "multi" {
		t.Errorf("AssuranceMulti is named %q, want multi", got)
	}
	key := authn.ReservedPrefixIDP + strings.Repeat("k", authn.SessionIDPKeyMax-len(authn.ReservedPrefixIDP))
	if len(key) != authn.SessionIDPKeyMax {
		t.Fatalf("the test key is %d bytes, want exactly the maximum %d", len(key), authn.SessionIDPKeyMax)
	}
	out := idpAttributes(&authn.External{Attributes: map[string]string{key: "v"}})
	if _, ok := out[key]; !ok {
		t.Errorf("a key of exactly the maximum length was dropped: %v", out)
	}
}

// TestTheFlowBoundsArePinned pins the published flow bounds to their literal
// values, so a change to a constant is caught rather than followed by the test
// that builds its boundary from it.
func TestTheFlowBoundsArePinned(t *testing.T) {
	if maxAnswers != 8 || maxInputFields != 8 || maxStartFieldBytes != 512 || maxAnswerFieldBytes != 8<<10 || maxReturnToBytes != 2048 {
		t.Errorf("the flow bounds changed: answers %d, input fields %d, start field bytes %d, answer field bytes %d, return_to bytes %d",
			maxAnswers, maxInputFields, maxStartFieldBytes, maxAnswerFieldBytes, maxReturnToBytes)
	}
}

// newTestMetrics builds a manual-reader meter provider a case collects from.
func newTestMetrics(t *testing.T) (*sdkmetric.ManualReader, *sdkmetric.MeterProvider) {
	t.Helper()
	reader := sdkmetric.NewManualReader()
	mp := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
	t.Cleanup(func() { _ = mp.Shutdown(context.Background()) })
	return reader, mp
}

// loginMetricPairs collects the completions counter and returns its data points
// keyed by "method/outcome". It fails when a data point is missing either
// attribute, which is the closed set the instrument carries.
func loginMetricPairs(t *testing.T, reader *sdkmetric.ManualReader) map[string]int {
	t.Helper()
	var rm metricdata.ResourceMetrics
	if err := reader.Collect(context.Background(), &rm); err != nil {
		t.Fatalf("collecting metrics: %v", err)
	}
	out := map[string]int{}
	for _, sm := range rm.ScopeMetrics {
		for _, md := range sm.Metrics {
			if md.Name != completionsInstrument {
				continue
			}
			sum, ok := md.Data.(metricdata.Sum[int64])
			if !ok {
				t.Fatalf("the instrument %s is not an int64 sum: %T", md.Name, md.Data)
			}
			for _, dp := range sum.DataPoints {
				method, okM := dp.Attributes.Value(methodAttribute)
				outcome, okO := dp.Attributes.Value(outcomeAttribute)
				if !okM || !okO {
					t.Errorf("a %s data point lacks method or outcome: %v", completionsInstrument, dp.Attributes.ToSlice())
					continue
				}
				out[method.AsString()+"/"+outcome.AsString()] += int(dp.Value)
			}
		}
	}
	return out
}

// TestTheLoginCompletionsInstrumentCarriesMethodAndOutcome holds the instrument's
// attribute set and that it counts a failed login, not only a completed one.
func TestTheLoginCompletionsInstrumentCarriesMethodAndOutcome(t *testing.T) {
	reader, mp := newTestMetrics(t)

	// A login that completes counts success.
	success := &fakeMethod{name: "password"}
	success.begin = func(authn.Start) (authn.Step, error) {
		return authn.StepProven(authn.Proof{Account: "acct1", Methods: []authn.Method{authn.MethodPassword}}), nil
	}
	rs := newRig(t, success)
	rs.allow("acct1")
	var err error
	if rs.p.meters, err = newMeters(mp); err != nil {
		t.Fatalf("creating the instrument: %v", err)
	}
	rec := rs.send(http.MethodPost, "/api/v1/auth/flows", `{"method":"password"}`)
	cookie := flowCookie(t, rec)
	rs.send(http.MethodPost, "/api/v1/auth/flows/answer", `{"seq":0,"step":"complete"}`, "Cookie", cookie)

	// A login whose first factor is refused counts failed.
	refused := &fakeMethod{name: "password"}
	refused.begin = func(authn.Start) (authn.Step, error) { return authn.StepRefused(errBoom), nil }
	rr := newRig(t, refused)
	if rr.p.meters, err = newMeters(mp); err != nil {
		t.Fatalf("creating the instrument: %v", err)
	}
	rr.send(http.MethodPost, "/api/v1/auth/flows", `{"method":"password"}`)

	pairs := loginMetricPairs(t, reader)
	if pairs["password/success"] != 1 {
		t.Errorf("the completed login is not counted: %v", pairs)
	}
	if pairs["password/failed"] != 1 {
		t.Errorf("the refused login is not counted: %v", pairs)
	}
}

// TestAnAnswerAfterTheCallBudgetIsUnavailable holds the engine's call budget: a
// method that answers only after the budget has ended is answered as an outage,
// not with its late answer.
func TestAnAnswerAfterTheCallBudgetIsUnavailable(t *testing.T) {
	r := newRigMethod(t, &redirectMethod{})
	r.p.settings = readerWith(map[string]string{ModuleName + "." + callBudgetKey: "1s"})

	got, ok := r.p.invoke(context.Background(), "password", func(cctx context.Context) (authn.Step, error) {
		<-cctx.Done()
		return authn.StepProven(authn.Proof{Account: "acct1", Methods: []authn.Method{authn.MethodPassword}}), nil
	})
	if !ok {
		t.Fatal("invoke reported that the method gave no answer")
	}
	if step.KindOf(got) != step.KindUnavailable {
		t.Errorf("an answer after the call budget is %s, want an outage", step.KindOf(got))
	}
}

// TestAMethodGivingOversizedPrivateStateIsAnOutage holds the surface bound the
// model states on a method's private state: a step whose private bytes are over
// it is answered as the method's outage rather than stored in the flow record.
func TestAMethodGivingOversizedPrivateStateIsAnOutage(t *testing.T) {
	m := &fakeMethod{name: "password"}
	m.begin = func(authn.Start) (authn.Step, error) {
		return authn.StepRedirect("idp", make([]byte, maxPrivateBytes+1), "https://idp.example.org/auth"), nil
	}
	r := newRig(t, m)

	rec := r.send(http.MethodPost, "/api/v1/auth/flows", `{"method":"password"}`)
	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("a method giving oversized private state answered %d, want 503: %s", rec.Code, rec.Body.String())
	}
}

// TestAResolverErrorFailsTheFlowClosed holds the resolver's failure path: a proof
// the resolver refuses to resolve leaves the flow a failed tombstone, answers the
// request and issues no session, rather than being read as a zero resolution.
func TestAResolverErrorFailsTheFlowClosed(t *testing.T) {
	m := &redirectMethod{}
	m.answer = func(authn.FlowState, authn.StepInput) (authn.Step, error) {
		// A proof that names no resolution source is the resolver's own error.
		return authn.StepProven(authn.Proof{}), nil
	}
	r := newRigMethod(t, m)
	r.allow("acct1")
	cookie := startRedirect(t, r, "")

	out := r.send(http.MethodGet, "/api/v1/auth/callback/password", "", "Cookie", cookie)
	if out.Code == http.StatusSeeOther {
		t.Fatalf("a resolver error made a session and redirected: %s", out.Body.String())
	}
	if len(r.sessions.created) != 0 {
		t.Errorf("a resolver error made %d sessions", len(r.sessions.created))
	}
	if st := flowRecordOf(t, r, cookie).State; st != stateFailed {
		t.Errorf("the flow state is %q, want the failed tombstone", st)
	}
}

// TestALinkIsRefusedForAnUnverifiedAccount holds the signup gate of the
// authenticated start: a pending self-registration cannot begin a link, the
// answer is 403 contact-required, and no flow record is written. Step-up and
// enroll are not gated, and an account that never self-registered or has proved
// its address links as before.
func TestALinkIsRefusedForAnUnverifiedAccount(t *testing.T) {
	cases := []struct {
		name  string
		state authn.SignupStatus
		code  int
		code_ string
	}{
		{"a pending registration", authn.SignupStatus{State: authn.SignupPending}, http.StatusForbidden, mount.CodeContactRequired},
		{"a never-registered account", authn.SignupStatus{State: authn.SignupNone}, http.StatusOK, ""},
		{"a verified registration", authn.SignupStatus{State: authn.SignupVerified}, http.StatusOK, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			m := &fakeMethod{name: "password"}
			m.begin = func(authn.Start) (authn.Step, error) { return authn.StepPrompt("credentials", nil), nil }
			r := newRig(t, m)
			r.loginAsAuthenticated("acct1", "sid")
			r.verification.state = c.state

			rec := r.send(http.MethodPost, "/api/v1/auth/flows/authenticated", `{"purpose":"link","method":"password"}`)
			if rec.Code != c.code {
				t.Fatalf("a link answered %d, want %d: %s", rec.Code, c.code, rec.Body.String())
			}
			if c.code_ != "" && !strings.Contains(rec.Body.String(), c.code_) {
				t.Errorf("the answer carries no %q code: %s", c.code_, rec.Body.String())
			}
			r.kv.mu.Lock()
			records := len(r.kv.m)
			r.kv.mu.Unlock()
			if c.code == http.StatusForbidden && records != 0 {
				t.Errorf("a refused link wrote %d flow record(s), want none", records)
			}
			if c.code == http.StatusOK && records == 0 {
				t.Error("an allowed link wrote no flow record")
			}
			if got := r.verification.readCount(); got != 1 {
				t.Errorf("the signup state was read %d times, want once", got)
			}
		})
	}
}

// TestStepUpAndEnrollAreNotGatedByTheSignupState holds the other two purposes:
// an unverified account may still strengthen its session and register a factor.
func TestStepUpAndEnrollAreNotGatedByTheSignupState(t *testing.T) {
	for _, body := range []string{
		`{"purpose":"step-up","method":"password"}`,
		`{"purpose":"enroll","input":{"kind":"totp"}}`,
	} {
		m := &fakeMethod{name: "password"}
		m.begin = func(authn.Start) (authn.Step, error) { return authn.StepPrompt("credentials", nil), nil }
		r := newRig(t, m)
		r.loginAsAuthenticated("acct1", "sid")
		r.allowWithPassword("acct1")
		r.verification.state = authn.SignupStatus{State: authn.SignupPending}

		rec := r.send(http.MethodPost, "/api/v1/auth/flows/authenticated", body)
		if rec.Code != http.StatusOK {
			t.Errorf("%s answered %d, want 200: %s", body, rec.Code, rec.Body.String())
		}
		if got := r.verification.readCount(); got != 0 {
			t.Errorf("%s read the signup state %d times, want none", body, got)
		}
	}
}

// TestALinkOnAnUnreadableSignupStateIs503 holds the failure direction: a state
// that cannot be read is unavailable, never a pass.
func TestALinkOnAnUnreadableSignupStateIs503(t *testing.T) {
	m := &fakeMethod{name: "password"}
	r := newRig(t, m)
	r.loginAsAuthenticated("acct1", "sid")
	r.verification.err = errors.New("boom")

	rec := r.send(http.MethodPost, "/api/v1/auth/flows/authenticated", `{"purpose":"link","method":"password"}`)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("a link with an unreadable signup state answered %d, want 503: %s", rec.Code, rec.Body.String())
	}
	r.kv.mu.Lock()
	records := len(r.kv.m)
	r.kv.mu.Unlock()
	if records != 0 {
		t.Errorf("the failed start wrote %d flow record(s), want none", records)
	}
}

// A registration step that refuses the input as malformed answers 400, not the
// 401 a credential that did not hold gets: a label outside its domain never
// reached a factor to check.
func TestARegistrationRefusedForAMalformedInputIsABadRequest(t *testing.T) {
	m := &fakeMethod{name: "password"}
	r := newRig(t, m)
	r.loginAsAuthenticated("acct1", "sid")
	r.mfa.enroll = func(authn.FactorKind, authn.StepInput) authn.Step {
		return authn.StepRefused(authn.ErrInvalidArgument)
	}

	w := r.send(http.MethodPost, "/api/v1/auth/flows/authenticated",
		`{"purpose":"enroll","input":{"kind":"totp","label":"work\nphone"}}`)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("POST flows/authenticated = %d (%s), want 400", w.Code, w.Body.String())
	}
	var problem struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &problem); err != nil {
		t.Fatalf("the body %q is no problem document: %v", w.Body.String(), err)
	}
	if problem.Type != "urn:speed:authn:bad-request" {
		t.Errorf("the problem type is %q, want the bad-request code", problem.Type)
	}
}
