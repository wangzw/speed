package login

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"testing"

	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"

	"github.com/vislake/speed/pkg/authn"
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
			rec := r.send(http.MethodPost, "/auth/flows", `{"method":"password"}`)
			cookie := flowCookie(t, rec)
			for i, want := range c.want {
				out := r.send(http.MethodPost, "/auth/flows/answer",
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
	rec := r.send(http.MethodPost, "/auth/flows", `{"method":"password"}`)
	cookie := flowCookie(t, rec)
	if out := r.send(http.MethodPost, "/auth/flows/answer", `{"seq":1,"step":"answer","input":{}}`, "Cookie", cookie); out.Code != http.StatusServiceUnavailable {
		t.Fatalf("an overstated report answered %d, want 503: %s", out.Code, out.Body.String())
	}
	if after := r.send(http.MethodGet, "/auth/flows/current", "", "Cookie", cookie); after.Code != http.StatusUnauthorized {
		t.Errorf("the flow after an overstated report answered %d, want 401: the record was not tombstoned", after.Code)
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

	start := r.send(http.MethodPost, "/auth/flows/authenticated", `{"purpose":"enroll","input":{"kind":"totp"}}`)
	if start.Code != http.StatusOK {
		t.Fatalf("POST flows/authenticated answered %d: %s", start.Code, start.Body.String())
	}
	cookie := flowCookie(t, start)
	seq := int(decode(t, start.Body.String())["seq"].(float64))

	ans := r.send(http.MethodPost, "/auth/flows/answer",
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

// TestTheCompletionReviewRefusesAnotherAccountOrAnImpersonation holds the two
// guards of the completion review that the session's own status feeds: a session
// of another account and a session that is impersonating are both refused.
func TestTheCompletionReviewRefusesAnotherAccountOrAnImpersonation(t *testing.T) {
	cases := []struct {
		name string
		edit func(*rig)
	}{
		{"a session of another account", func(r *rig) { r.sessions.status.Account = "other" }},
		{"an impersonating session", func(r *rig) { r.sessions.status.ImpersonationID = "imp1" }},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			m := &fakeMethod{name: "password"}
			r := newRig(t, m)
			r.loginAsAuthenticated("acct1", "sid")
			start := r.send(http.MethodPost, "/auth/flows/authenticated", `{"purpose":"enroll","input":{"kind":"totp"}}`)
			if start.Code != http.StatusOK {
				t.Fatalf("POST flows/authenticated answered %d: %s", start.Code, start.Body.String())
			}
			cookie := flowCookie(t, start)
			seq := int(decode(t, start.Body.String())["seq"].(float64))
			c.edit(r)

			done := r.send(http.MethodPost, "/auth/flows/answer", `{"seq":`+itoa(seq)+`,"step":"complete"}`, "Cookie", cookie)
			if done.Code != http.StatusUnauthorized {
				t.Errorf("the completion answered %d, want 401: %s", done.Code, done.Body.String())
			}
		})
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

	rec := r.send(http.MethodPost, "/auth/flows", `{"method":"password"}`)
	next := decode(t, rec.Body.String())["next"].(map[string]any)
	if next["kind"] != "enroll" {
		t.Fatalf("a login the policy requires a factor of answered next %v, want enroll", next)
	}
	kinds, ok := next["kinds"].([]any)
	if !ok || len(kinds) != 1 || kinds[0] != "totp" {
		t.Errorf("the enroll next step carries kinds %v, want [totp]", next["kinds"])
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

	rec := r.send(http.MethodPost, "/auth/flows", `{"method":"password"}`)
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
			rec := r.send(http.MethodPost, "/auth/flows/authenticated", `{"purpose":"enroll","input":{"kind":"totp"}}`)
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

	rec := r.send(http.MethodPost, "/auth/flows", `{"method":"password","delivery":"token"}`)
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

	rec := r.send(http.MethodPost, "/auth/flows/authenticated", `{"purpose":"link","method":"password"}`)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("a link on a single-factor session answered %d, want 401: %s", rec.Code, rec.Body.String())
	}
}

// TestTheSourceAddressPreThrottleHolds holds the boundary of the source-address
// pre-throttle: the requests up to the limit are served and the one past it is
// throttled.
func TestTheSourceAddressPreThrottleHolds(t *testing.T) {
	m := &fakeMethod{name: "password"}
	m.begin = func(authn.Start) (authn.Step, error) { return authn.StepPrompt("credentials", nil), nil }
	r := newRig(t, m)
	r.p.settings = readerWith(map[string]string{ModuleName + "." + ipLimitKey: "2"})

	for i := 1; i <= 2; i++ {
		if rec := r.send(http.MethodPost, "/auth/flows", `{"method":"password"}`); rec.Code != http.StatusOK {
			t.Fatalf("request %d answered %d, want 200: %s", i, rec.Code, rec.Body.String())
		}
	}
	if rec := r.send(http.MethodPost, "/auth/flows", `{"method":"password"}`); rec.Code != http.StatusTooManyRequests {
		t.Errorf("the request past the limit answered %d, want 429: %s", rec.Code, rec.Body.String())
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

	start := r.send(http.MethodPost, "/auth/flows", `{"method":"password","delivery":"token"}`)
	if start.Code != http.StatusOK {
		t.Fatalf("POST flows answered %d: %s", start.Code, start.Body.String())
	}
	cookie := flowCookie(t, start)
	done := r.send(http.MethodPost, "/auth/flows/answer", `{"seq":0,"step":"complete"}`, "Cookie", cookie)
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

// TestAnAutomaticMergeCleansUpBeforeTheNewSession holds the A10 ordering of an
// automatic merge: the account's sessions are revoked, then its password is
// deleted, and only then is the login's new session created.
func TestAnAutomaticMergeCleansUpBeforeTheNewSession(t *testing.T) {
	m := &fakeMethod{name: "password"}
	m.begin = func(authn.Start) (authn.Step, error) {
		return authn.StepProven(authn.Proof{External: &authn.External{
			Provider: "google", Subject: "g1", Email: "ann@x.org", EmailVerified: true,
		}}), nil
	}
	r := newRig(t, m)
	r.allow("acct1")
	r.withAutoLink()
	r.accounts.ident["acct1"] = []authn.Identifier{{
		Kind: authn.IdentifierEmail, Value: "ann@x.org", VerifiedVia: authn.VerifiedViaIDPPrefix + "google",
	}}
	r.accounts.lookup[string(authn.IdentifierEmail)+"\x00ann@x.org"] = "acct1"

	rec := &recorder{}
	r.sessions.rec = rec
	r.p.passwords = &recordingPasswords{rec: rec}

	start := r.send(http.MethodPost, "/auth/flows", `{"method":"password"}`)
	if start.Code != http.StatusOK {
		t.Fatalf("POST flows answered %d: %s", start.Code, start.Body.String())
	}
	cookie := flowCookie(t, start)
	done := r.send(http.MethodPost, "/auth/flows/answer", `{"seq":0,"step":"complete"}`, "Cookie", cookie)
	if done.Code != http.StatusOK {
		t.Fatalf("the merged login answered %d: %s", done.Code, done.Body.String())
	}
	want := []string{"revoke:", "clear", "create"}
	got := rec.all()
	if len(got) != len(want) {
		t.Fatalf("the merge cleanup ran %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("the merge cleanup ran %v, want %v (revoke with except=\"\", then clear, then create)", got, want)
		}
	}
}

// TestMethodsAndCurrentBehave holds the two listing routes: GET methods hides a
// disabled method, and GET flows/current refuses a tombstone and echoes the flow
// token of a header-bound flow.
func TestMethodsAndCurrentBehave(t *testing.T) {
	m := &fakeMethod{name: "password"}
	m.begin = func(authn.Start) (authn.Step, error) { return authn.StepPrompt("credentials", nil), nil }
	r := newRig(t, m)

	listing := r.send(http.MethodGet, "/auth/methods", "")
	if body := decode(t, listing.Body.String())["methods"].([]any); len(body) != 1 || body[0] != "password" {
		t.Errorf("GET methods = %v, want [password]", listing.Body.String())
	}

	hidden := newRig(t, m)
	hidden.p.settings = readerWithDisabled(t, "password")
	got := hidden.send(http.MethodGet, "/auth/methods", "")
	if body := decode(t, got.Body.String())["methods"].([]any); len(body) != 0 {
		t.Errorf("GET methods with password disabled = %v, want an empty list", got.Body.String())
	}

	// A header-bound flow echoes its token in the flow reply and in a resume.
	header := r.send(http.MethodPost, "/auth/flows", `{"method":"password","binding":"header"}`)
	tok, ok := decode(t, header.Body.String())["flow_token"].(string)
	if !ok || tok == "" {
		t.Fatalf("a header-bound start echoed no flow token: %s", header.Body.String())
	}
	current := r.send(http.MethodGet, "/auth/flows/current", "", "Authn-Flow", tok)
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

	rec := r.send(http.MethodPost, "/auth/flows", `{"method":"password"}`)
	cookie := flowCookie(t, rec)

	notOffered := r.send(http.MethodPost, "/auth/flows/answer",
		`{"seq":0,"step":"factor","input":{"factor":"nope"}}`, "Cookie", cookie)
	if notOffered.Code != http.StatusUnauthorized {
		t.Errorf("a factor the flow did not offer answered %d, want 401: %s", notOffered.Code, notOffered.Body.String())
	}
	empty := r.send(http.MethodPost, "/auth/flows/answer",
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

	bogus := r.send(http.MethodPost, "/auth/flows/answer", `{"seq":1,"step":"bogus","input":{}}`, "Cookie", cookie)
	if bogus.Code != http.StatusBadRequest {
		t.Errorf("an unknown step answered %d, want 400: %s", bogus.Code, bogus.Body.String())
	}
	wrongSeq := r.send(http.MethodPost, "/auth/flows/answer", `{"seq":99,"step":"answer","input":{}}`, "Cookie", cookie)
	failed := r.send(http.MethodPost, "/auth/flows/answer", `{"seq":1,"step":"answer","input":{}}`, "Cookie", cookie)
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
	if maxAnswers != 8 || maxStartFields != 8 || maxFieldBytes != 512 || maxReturnToBytes != 2048 {
		t.Errorf("the flow bounds changed: answers %d, start fields %d, field bytes %d, return_to bytes %d",
			maxAnswers, maxStartFields, maxFieldBytes, maxReturnToBytes)
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
	rec := rs.send(http.MethodPost, "/auth/flows", `{"method":"password"}`)
	cookie := flowCookie(t, rec)
	rs.send(http.MethodPost, "/auth/flows/answer", `{"seq":0,"step":"complete"}`, "Cookie", cookie)

	// A login whose first factor is refused counts failed.
	refused := &fakeMethod{name: "password"}
	refused.begin = func(authn.Start) (authn.Step, error) { return authn.StepRefused(errBoom), nil }
	rr := newRig(t, refused)
	if rr.p.meters, err = newMeters(mp); err != nil {
		t.Fatalf("creating the instrument: %v", err)
	}
	rr.send(http.MethodPost, "/auth/flows", `{"method":"password"}`)

	pairs := loginMetricPairs(t, reader)
	if pairs["password/success"] != 1 {
		t.Errorf("the completed login is not counted: %v", pairs)
	}
	if pairs["password/failed"] != 1 {
		t.Errorf("the refused login is not counted: %v", pairs)
	}
}
