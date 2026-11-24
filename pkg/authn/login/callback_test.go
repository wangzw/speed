package login

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/vislake/speed/pkg/audit"
	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/authn/internal/mount/mounttest"
	speedhttp "github.com/vislake/speed/pkg/http"
)

type fakeSSO struct {
	authn.SSOProviders
	row authn.SSOProvider
	err error
}

func (s *fakeSSO) Get(context.Context, string) (authn.SSOProvider, error) { return s.row, s.err }

type redirectMethod struct {
	authn.LoginMethod
	name   string
	calls  atomic.Int32
	answer func(authn.FlowState, authn.StepInput) (authn.Step, error)
}

func (m *redirectMethod) Name() string {
	if m.name == "" {
		return "password"
	}
	return m.name
}

func (m *redirectMethod) Describe() authn.MethodDescriptor {
	return authn.MethodDescriptor{Name: m.Name(), Kind: authn.MethodKindRedirect, Protocol: authn.MethodProtocolOAuth2}
}

func (*redirectMethod) Begin(context.Context, authn.Start) (authn.Step, error) {
	return authn.StepRedirect("idp", nil, "https://idp.example.org/auth"), nil
}

func (m *redirectMethod) Answer(_ context.Context, f authn.FlowState, in authn.StepInput) (authn.Step, error) {
	m.calls.Add(1)
	if m.answer != nil {
		return m.answer(f, in)
	}
	return authn.StepProven(authn.Proof{Account: "acct1", Methods: []authn.Method{authn.MethodPassword}}), nil
}

func startRedirect(t *testing.T, r *rig, extra string) string {
	t.Helper()
	body := `{"method":"` + r.p.methodNames[0] + `"` + extra + `}`
	rec := r.send(http.MethodPost, "/api/v1/auth/flows", body)
	if rec.Code != http.StatusOK {
		t.Fatalf("POST flows answered %d: %s", rec.Code, rec.Body.String())
	}
	return flowCookie(t, rec)
}

// TestACallbackCompletesACookieLoginOnce holds that the callback makes a session and tombstones the flow, so a later complete is refused.
func TestACallbackCompletesACookieLoginOnce(t *testing.T) {
	m := &redirectMethod{}
	r := newRigMethod(t, m)
	r.allow("acct1")

	cookie := startRedirect(t, r, "")

	cb := r.send(http.MethodGet, "/api/v1/auth/callback/password", "", "Cookie", cookie)
	if cb.Code != http.StatusSeeOther {
		t.Fatalf("the callback answered %d, want 303: %s", cb.Code, cb.Body.String())
	}
	if len(r.sessions.created) != 1 {
		t.Fatalf("the callback made %d sessions, want one", len(r.sessions.created))
	}

	again := r.send(http.MethodPost, "/api/v1/auth/flows/answer", `{"seq":1,"step":"complete"}`, "Cookie", cookie)
	if again.Code != http.StatusUnauthorized {
		t.Errorf("a complete against a callback-completed flow answered %d, want 401: %s", again.Code, again.Body.String())
	}
	if len(r.sessions.created) != 1 {
		t.Errorf("the flow's terminal action ran twice: %d sessions", len(r.sessions.created))
	}
}

// TestAConcurrentSecondCallbackIsRefused holds that a second callback on the same record loses the processing swap, so the code is exchanged once.
func TestAConcurrentSecondCallbackIsRefused(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	var once sync.Once
	m := &redirectMethod{}
	m.answer = func(authn.FlowState, authn.StepInput) (authn.Step, error) {
		once.Do(func() { close(started) })
		<-release
		return authn.StepProven(authn.Proof{Account: "acct1", Methods: []authn.Method{authn.MethodPassword}}), nil
	}
	r := newRigMethod(t, m)
	r.allow("acct1")
	cookie := startRedirect(t, r, "")

	first := make(chan *httptest.ResponseRecorder, 1)
	go func() { first <- r.send(http.MethodGet, "/api/v1/auth/callback/password", "", "Cookie", cookie) }()
	<-started
	second := r.send(http.MethodGet, "/api/v1/auth/callback/password", "", "Cookie", cookie)
	close(release)
	got := <-first

	if n := m.calls.Load(); n != 1 {
		t.Errorf("the method's answer ran %d times, want once: a replayed callback was handled", n)
	}
	if second.Code != http.StatusUnauthorized {
		t.Errorf("the concurrent second callback answered %d, want 401: %s", second.Code, second.Body.String())
	}
	if got.Code != http.StatusSeeOther {
		t.Errorf("the first callback answered %d, want 303: %s", got.Code, got.Body.String())
	}
}

// TestANonLoginCallbackLandsOnTheReturnTarget holds that a non-login callback answers 303 with no body and no session, leaving the flow at ready.
func TestANonLoginCallbackLandsOnTheReturnTarget(t *testing.T) {
	m := &redirectMethod{}
	m.answer = func(authn.FlowState, authn.StepInput) (authn.Step, error) {
		return authn.StepProven(authn.Proof{External: &authn.External{
			Provider: "google", Subject: "g1", Email: "ann@x.org", EmailVerified: true,
		}}), nil
	}
	r := newRigMethod(t, m)
	r.loginAsAuthenticated("acct1", "sid")
	r.allow("acct1")

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
	if loc := cb.Header().Get("Location"); loc != "/account/callback" {
		t.Errorf("the link callback went to %q, want the flow's return_to", loc)
	}
	if cb.Body.Len() != 0 {
		t.Errorf("the link callback wrote a body: %q", cb.Body.String())
	}
	if cookies := cb.Result().Cookies(); len(cookies) != 0 {
		t.Errorf("the link callback set cookies %v, want none", cookies)
	}
	if n := len(r.sessions.created); n != 0 {
		t.Errorf("the link callback made %d sessions, want none", n)
	}

	current := r.send(http.MethodGet, "/api/v1/auth/flows/current", "", "Cookie", cookie)
	if current.Code != http.StatusOK {
		t.Fatalf("GET flows/current after the callback answered %d: %s", current.Code, current.Body.String())
	}
	flow := decode(t, current.Body.String())
	if flow["state"] != "ready" {
		t.Errorf("the flow state after the callback is %v, want ready", flow["state"])
	}
	if k := flow["next"].(map[string]any)["kind"]; k != "complete" {
		t.Errorf("the flow's next kind is %v, want complete", k)
	}

	again := r.send(http.MethodGet, "/api/v1/auth/callback/password", "", "Cookie", cookie)
	if again.Code != http.StatusUnauthorized {
		t.Fatalf("a second callback answered %d, want 401: %s", again.Code, again.Body.String())
	}
	if got := decode(t, again.Body.String())["type"]; got != "urn:speed:authn:flow-invalid" {
		t.Errorf("the second callback's problem type is %v, want flow-invalid", got)
	}

	done := r.send(http.MethodPost, "/api/v1/auth/flows/answer", `{"seq":1,"step":"complete"}`, "Cookie", cookie)
	if done.Code != http.StatusOK {
		t.Fatalf("the completing answer answered %d, want 200: %s", done.Code, done.Body.String())
	}
	if got := decode(t, done.Body.String())["state"]; got != "linked" {
		t.Errorf("the completing answer reports state %v, want linked", got)
	}
}

// TestANoFactorStepUpThroughARedirectMethodLandsOnTheReturnTarget holds that such a step-up lands 303 and leaves the ready flow for the client.
func TestANoFactorStepUpThroughARedirectMethodLandsOnTheReturnTarget(t *testing.T) {
	m := &redirectMethod{}
	r := newRigMethod(t, m)
	r.loginAsAuthenticated("acct1", "sid")
	r.allowWithPassword("acct1")

	start := r.send(http.MethodPost, "/api/v1/auth/flows/authenticated",
		`{"purpose":"step-up","method":"password","return_to":"/account/security"}`)
	if start.Code != http.StatusOK {
		t.Fatalf("POST flows/authenticated answered %d: %s", start.Code, start.Body.String())
	}
	cookie := flowCookie(t, start)

	cb := r.send(http.MethodGet, "/api/v1/auth/callback/password", "", "Cookie", cookie)
	if cb.Code != http.StatusSeeOther {
		t.Fatalf("the step-up callback answered %d, want 303: %s", cb.Code, cb.Body.String())
	}
	if loc := cb.Header().Get("Location"); loc != "/account/security" {
		t.Errorf("the step-up callback went to %q, want the flow's return_to", loc)
	}
	if cb.Body.Len() != 0 {
		t.Errorf("the step-up callback wrote a body: %q", cb.Body.String())
	}
	if n := len(r.sessions.created); n != 0 {
		t.Errorf("the step-up callback made %d sessions, want none", n)
	}

	current := r.send(http.MethodGet, "/api/v1/auth/flows/current", "", "Cookie", cookie)
	if current.Code != http.StatusOK {
		t.Fatalf("GET flows/current after the callback answered %d: %s", current.Code, current.Body.String())
	}
	flow := decode(t, current.Body.String())
	if flow["state"] != "ready" {
		t.Errorf("the flow state after the step-up callback is %v, want ready", flow["state"])
	}
	if k := flow["next"].(map[string]any)["kind"]; k != "complete" {
		t.Errorf("the flow's next kind is %v, want complete", k)
	}
}

// TestACallbackWithNoReturnToLandsOnTheDefault holds that a flow with no return_to lands on the configured default-return-to.
func TestACallbackWithNoReturnToLandsOnTheDefault(t *testing.T) {
	m := &redirectMethod{}
	m.answer = func(authn.FlowState, authn.StepInput) (authn.Step, error) {
		return authn.StepProven(authn.Proof{External: &authn.External{
			Provider: "google", Subject: "g1", Email: "ann@x.org", EmailVerified: true,
		}}), nil
	}
	r := newRigMethod(t, m)
	r.loginAsAuthenticated("acct1", "sid")
	r.allow("acct1")
	r.p.settings = readerWith(map[string]string{ModuleName + "." + defaultReturnToKey: "/account"})

	start := r.send(http.MethodPost, "/api/v1/auth/flows/authenticated", `{"purpose":"link","method":"password"}`)
	if start.Code != http.StatusOK {
		t.Fatalf("POST flows/authenticated answered %d: %s", start.Code, start.Body.String())
	}
	cookie := flowCookie(t, start)

	cb := r.send(http.MethodGet, "/api/v1/auth/callback/password", "", "Cookie", cookie)
	if cb.Code != http.StatusSeeOther {
		t.Fatalf("the callback answered %d, want 303: %s", cb.Code, cb.Body.String())
	}
	if loc := cb.Header().Get("Location"); loc != "/account" {
		t.Errorf("the callback with no return_to went to %q, want the configured default", loc)
	}
}

// TestACallbackWithAnUnacceptableDefaultLandsOnRoot holds that an untrusted configured default lands on "/".
func TestACallbackWithAnUnacceptableDefaultLandsOnRoot(t *testing.T) {
	m := &redirectMethod{}
	m.answer = func(authn.FlowState, authn.StepInput) (authn.Step, error) {
		return authn.StepProven(authn.Proof{External: &authn.External{
			Provider: "google", Subject: "g1", Email: "ann@x.org", EmailVerified: true,
		}}), nil
	}
	r := newRigMethod(t, m)
	r.loginAsAuthenticated("acct1", "sid")
	r.allow("acct1")
	r.p.settings = readerWith(map[string]string{
		ModuleName + "." + defaultReturnToKey: "https://evil.example.org/account",
	})

	start := r.send(http.MethodPost, "/api/v1/auth/flows/authenticated", `{"purpose":"link","method":"password"}`)
	if start.Code != http.StatusOK {
		t.Fatalf("POST flows/authenticated answered %d: %s", start.Code, start.Body.String())
	}
	cookie := flowCookie(t, start)

	cb := r.send(http.MethodGet, "/api/v1/auth/callback/password", "", "Cookie", cookie)
	if cb.Code != http.StatusSeeOther {
		t.Fatalf("the callback answered %d, want 303: %s", cb.Code, cb.Body.String())
	}
	if loc := cb.Header().Get("Location"); loc != "/" {
		t.Errorf("the callback with an untrusted default went to %q, want /", loc)
	}
}

// TestACookieLoginCallbackWithNoReturnToLandsOnTheDefault holds that a login without return_to also lands on the configured default.
func TestACookieLoginCallbackWithNoReturnToLandsOnTheDefault(t *testing.T) {
	m := &redirectMethod{}
	r := newRigMethod(t, m)
	r.allow("acct1")
	r.p.settings = readerWith(map[string]string{ModuleName + "." + defaultReturnToKey: "/account"})

	loc := func(t *testing.T) string {
		t.Helper()
		cookie := startRedirect(t, r, "")
		cb := r.send(http.MethodGet, "/api/v1/auth/callback/password", "", "Cookie", cookie)
		if cb.Code != http.StatusSeeOther {
			t.Fatalf("the callback answered %d, want 303: %s", cb.Code, cb.Body.String())
		}
		return cb.Header().Get("Location")
	}

	t.Run("a completed login", func(t *testing.T) {
		if got := loc(t); got != "/account" {
			t.Errorf("a completed login with no return_to went to %q, want the configured default", got)
		}
	})

	t.Run("a login needing a second factor", func(t *testing.T) {
		r.mfa.outcome = authn.MFAFactor
		r.mfa.offered = []authn.FactorRecord{{ID: "f1", Kind: authn.FactorTOTP}}
		if got := loc(t); got != "/account" {
			t.Errorf("a login needing a second factor with no return_to went to %q, want the configured default", got)
		}
	})
}

// TestATokenLoginNavigationCallbackLandsOnTheReturnTarget holds that a token login's navigation callback lands 303 with no session, leaving the ready flow for a later complete.
func TestATokenLoginNavigationCallbackLandsOnTheReturnTarget(t *testing.T) {
	m := &redirectMethod{}
	r := newRigMethod(t, m)
	r.allow("acct1")
	r.p.tokens = &fakeTokens{}

	cookie := startRedirect(t, r, `,"delivery":"token","return_to":"/account/token"`)
	cb := r.send(http.MethodGet, "/api/v1/auth/callback/password", "", "Cookie", cookie, "Sec-Fetch-Mode", "navigate")
	if cb.Code != http.StatusSeeOther {
		t.Fatalf("a token login's navigation callback answered %d, want 303: %s", cb.Code, cb.Body.String())
	}
	if loc := cb.Header().Get("Location"); loc != "/account/token" {
		t.Errorf("the navigation callback went to %q, want the flow's return_to", loc)
	}
	if cb.Body.Len() != 0 {
		t.Errorf("the navigation callback wrote a body: %q", cb.Body.String())
	}
	if n := len(r.sessions.created); n != 0 {
		t.Errorf("the navigation callback made %d sessions, want none", n)
	}

	current := r.send(http.MethodGet, "/api/v1/auth/flows/current", "", "Cookie", cookie)
	if current.Code != http.StatusOK {
		t.Fatalf("GET flows/current after the callback answered %d: %s", current.Code, current.Body.String())
	}
	flow := decode(t, current.Body.String())
	if flow["state"] != "ready" {
		t.Errorf("the flow state after the callback is %v, want ready", flow["state"])
	}
	if k := flow["next"].(map[string]any)["kind"]; k != "complete" {
		t.Errorf("the flow's next kind is %v, want complete", k)
	}

	done := r.send(http.MethodPost, "/api/v1/auth/flows/answer", `{"seq":1,"step":"complete"}`, "Cookie", cookie)
	if done.Code != http.StatusOK {
		t.Fatalf("the token completion after a navigation callback answered %d, want 200: %s", done.Code, done.Body.String())
	}
	if body := decode(t, done.Body.String()); body["access_token"] != "access-1" {
		t.Errorf("the token answer carries %v, want the issued access token", body)
	}
}

// TestATokenLoginAPICallbackKeepsTheCompletionBody holds that a non-navigation token callback answers 200 with the completion body and makes no session.
func TestATokenLoginAPICallbackKeepsTheCompletionBody(t *testing.T) {
	m := &redirectMethod{}
	r := newRigMethod(t, m)
	r.allow("acct1")
	r.p.tokens = &fakeTokens{}

	cookie := startRedirect(t, r, `,"delivery":"token","return_to":"/account/token"`)
	cb := r.send(http.MethodGet, "/api/v1/auth/callback/password", "", "Cookie", cookie)
	if cb.Code != http.StatusOK {
		t.Fatalf("a token login's API callback answered %d, want 200: %s", cb.Code, cb.Body.String())
	}
	body := decode(t, cb.Body.String())
	if body["state"] != "ready" {
		t.Errorf("the API callback reports state %v, want ready", body["state"])
	}
	if k := body["next"].(map[string]any)["kind"]; k != "complete" {
		t.Errorf("the API callback's next kind is %v, want complete", k)
	}
	if n := len(r.sessions.created); n != 0 {
		t.Errorf("the API callback made %d sessions, want none", n)
	}
}

// TestACompletedLinkWritesItsAuditRow holds the best-effort audit row of a completed link.
func TestACompletedLinkWritesItsAuditRow(t *testing.T) {
	m := &redirectMethod{}
	m.answer = func(authn.FlowState, authn.StepInput) (authn.Step, error) {
		return authn.StepProven(authn.Proof{External: &authn.External{
			Provider: "google", Subject: "g1", Email: "ann@x.org", EmailVerified: true,
		}}), nil
	}
	r := newRigMethod(t, m)
	r.loginAsAuthenticated("acct1", "sid")
	r.allow("acct1")
	sink := &relaxAudit{}
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

	var linkRows []audit.Entry
	for _, row := range sink.rows() {
		if row.Action == "authn.binding.link" {
			linkRows = append(linkRows, row)
		}
	}
	if len(linkRows) != 1 {
		t.Fatalf("the audit holds %d rows of authn.binding.link, want 1: %+v", len(linkRows), sink.rows())
	}
	if linkRows[0].Object != "account:acct1" || linkRows[0].Outcome != "success" {
		t.Errorf("the audit row = %+v, want object acct1 and outcome success", linkRows[0])
	}
	if got := linkRows[0].Detail["provider"]; got != "google" {
		t.Errorf("the row's provider detail = %q, want google", got)
	}
}

// TestACallbackNeedingASecondFactorRedirects holds that such a callback answers 303 to return_to and leaves the flow in the cookie.
func TestACallbackNeedingASecondFactorRedirects(t *testing.T) {
	m := &redirectMethod{}
	r := newRigMethod(t, m)
	r.allow("acct1")
	r.mfa.outcome = authn.MFAFactor
	r.mfa.offered = []authn.FactorRecord{{ID: "f1", Kind: authn.FactorTOTP}}

	cookie := startRedirect(t, r, `,"return_to":"https://app.example.org/after"`)

	cb := r.send(http.MethodGet, "/api/v1/auth/callback/password", "", "Cookie", cookie)
	if cb.Code != http.StatusSeeOther {
		t.Fatalf("a callback needing a second factor answered %d, want 303: %s", cb.Code, cb.Body.String())
	}
	if loc := cb.Header().Get("Location"); loc != "https://app.example.org/after" {
		t.Errorf("the redirect went to %q, want the flow's return_to", loc)
	}
	current := r.send(http.MethodGet, "/api/v1/auth/flows/current", "", "Cookie", cookie)
	if current.Code != http.StatusOK {
		t.Fatalf("GET flows/current after the callback answered %d: %s", current.Code, current.Body.String())
	}
	if st := decode(t, current.Body.String())["state"]; st != "second" {
		t.Errorf("the flow state after the callback is %v, want second", st)
	}
}

// TestCallbackIDPInitiatedIsGated holds that only a resumed POST from an enabled, allowing provider is accepted, and an untrusted default falls back to "/".
func TestCallbackIDPInitiatedIsGated(t *testing.T) {
	body := func() *rig {
		m := &redirectMethod{}
		m.answer = func(authn.FlowState, authn.StepInput) (authn.Step, error) {
			return authn.StepProven(authn.Proof{Account: "acct1", Methods: []authn.Method{authn.MethodPassword}}), nil
		}
		r := newRigMethod(t, m)
		r.allow("acct1")
		return r
	}

	t.Run("a GET with no cookie is refused", func(t *testing.T) {
		r := body()
		if rec := r.send(http.MethodGet, "/api/v1/auth/callback/ent", ""); rec.Code != http.StatusUnauthorized {
			t.Errorf("a GET IdP-initiated callback answered %d, want 401: %s", rec.Code, rec.Body.String())
		}
	})

	t.Run("no provider register refuses", func(t *testing.T) {
		r := body()
		if rec := r.send(http.MethodPost, "/api/v1/auth/callback/ent/resume", ""); rec.Code != http.StatusUnauthorized {
			t.Errorf("a POST with no provider register answered %d, want 401: %s", rec.Code, rec.Body.String())
		}
	})

	t.Run("a disabled provider refuses", func(t *testing.T) {
		r := body()
		r.p.sso = &fakeSSO{row: authn.SSOProvider{ID: "ent", Protocol: "password", Enabled: false, AllowIDPInitiated: true}}
		if rec := r.send(http.MethodPost, "/api/v1/auth/callback/ent/resume", ""); rec.Code != http.StatusUnauthorized {
			t.Errorf("a disabled provider answered %d, want 401: %s", rec.Code, rec.Body.String())
		}
	})

	t.Run("a provider that forbids IdP-initiated refuses", func(t *testing.T) {
		r := body()
		r.p.sso = &fakeSSO{row: authn.SSOProvider{ID: "ent", Protocol: "password", Enabled: true, AllowIDPInitiated: false}}
		if rec := r.send(http.MethodPost, "/api/v1/auth/callback/ent/resume", ""); rec.Code != http.StatusUnauthorized {
			t.Errorf("a provider that forbids IdP-initiated answered %d, want 401: %s", rec.Code, rec.Body.String())
		}
	})

	t.Run("a provider whose protocol is not a registered method refuses", func(t *testing.T) {
		r := body()
		r.p.sso = &fakeSSO{row: authn.SSOProvider{ID: "ent", Protocol: "oidc", Enabled: true, AllowIDPInitiated: true}}
		if rec := r.send(http.MethodPost, "/api/v1/auth/callback/ent/resume", ""); rec.Code != http.StatusUnauthorized {
			t.Errorf("a provider whose protocol is not a registered method answered %d, want 401: %s", rec.Code, rec.Body.String())
		}
	})

	t.Run("an enabled provider that allows it proceeds", func(t *testing.T) {
		r := body()
		r.p.sso = &fakeSSO{row: authn.SSOProvider{ID: "ent", Protocol: "password", Enabled: true, AllowIDPInitiated: true}}
		rec := r.send(http.MethodPost, "/api/v1/auth/callback/ent/resume", "")
		if rec.Code != http.StatusSeeOther {
			t.Fatalf("an accepted IdP-initiated sign-in answered %d, want 303: %s", rec.Code, rec.Body.String())
		}
		if loc := rec.Header().Get("Location"); loc != "/" {
			t.Errorf("the redirect went to %q, want the default return target", loc)
		}
	})
}

// TestACallbackMustNameTheFlowsProvider holds that a path provider differing from the recorded one is refused before any method runs.
func TestACallbackMustNameTheFlowsProvider(t *testing.T) {
	newSocial := func() (*rig, *redirectMethod) {
		m := &redirectMethod{}
		return newRigMethod(t, m), m
	}
	newEnterprise := func() (*rig, *redirectMethod) {
		m := &redirectMethod{name: "oidc"}
		return newRigMethod(t, m), m
	}

	t.Run("a social callback of another name is refused", func(t *testing.T) {
		r, m := newSocial()
		r.allow("acct1")
		cookie := startRedirect(t, r, "")
		if rec := r.send(http.MethodGet, "/api/v1/auth/callback/other", "", "Cookie", cookie); rec.Code != http.StatusUnauthorized {
			t.Errorf("a social callback of another name answered %d, want 401: %s", rec.Code, rec.Body.String())
		}
		if n := m.calls.Load(); n != 0 {
			t.Errorf("the method was asked %d times for a mismatched social callback, want none", n)
		}
	})

	t.Run("a social start input cannot change the recorded provider", func(t *testing.T) {
		r, m := newSocial()
		r.allow("acct1")
		cookie := startRedirect(t, r, `,"input":{"provider":"ent"}`)
		if got := flowProviderOf(t, r, cookie); got != "password" {
			t.Errorf("a social flow with input.provider recorded provider %q, want the method name", got)
		}
		if rec := r.send(http.MethodGet, "/api/v1/auth/callback/ent", "", "Cookie", cookie); rec.Code != http.StatusUnauthorized {
			t.Errorf("a social callback aimed at the start input's provider answered %d, want 401: %s", rec.Code, rec.Body.String())
		}
		if n := m.calls.Load(); n != 0 {
			t.Errorf("the method was asked %d times for a social callback of another name, want none", n)
		}
	})

	t.Run("an enterprise callback of another provider is refused", func(t *testing.T) {
		r, m := newEnterprise()
		r.allow("acct1")
		cookie := startRedirect(t, r, `,"input":{"provider":"ent"}`)
		if rec := r.send(http.MethodGet, "/api/v1/auth/callback/other", "", "Cookie", cookie); rec.Code != http.StatusUnauthorized {
			t.Errorf("an enterprise callback of another provider answered %d, want 401: %s", rec.Code, rec.Body.String())
		}
		if n := m.calls.Load(); n != 0 {
			t.Errorf("the method was asked %d times for a mismatched enterprise callback, want none", n)
		}
	})

	t.Run("a same-length different provider is refused", func(t *testing.T) {
		r, m := newEnterprise()
		r.allow("acct1")
		cookie := startRedirect(t, r, `,"input":{"provider":"entA"}`)
		if rec := r.send(http.MethodGet, "/api/v1/auth/callback/entB", "", "Cookie", cookie); rec.Code != http.StatusUnauthorized {
			t.Errorf("a callback naming a same-length different provider answered %d, want 401: %s", rec.Code, rec.Body.String())
		}
		if n := m.calls.Load(); n != 0 {
			t.Errorf("the method was asked %d times for a same-length different provider, want none", n)
		}
	})

	t.Run("a provider differing only in case is refused", func(t *testing.T) {
		r, m := newEnterprise()
		r.allow("acct1")
		cookie := startRedirect(t, r, `,"input":{"provider":"ent"}`)
		if rec := r.send(http.MethodGet, "/api/v1/auth/callback/ENT", "", "Cookie", cookie); rec.Code != http.StatusUnauthorized {
			t.Errorf("a callback naming a case variant answered %d, want 401: %s", rec.Code, rec.Body.String())
		}
		if n := m.calls.Load(); n != 0 {
			t.Errorf("the method was asked %d times for a case variant, want none", n)
		}
	})

	t.Run("an enterprise callback of the recorded provider is dispatched", func(t *testing.T) {
		r, m := newEnterprise()
		r.allow("acct1")
		cookie := startRedirect(t, r, `,"input":{"provider":"ent"}`)
		if rec := r.send(http.MethodGet, "/api/v1/auth/callback/ent", "", "Cookie", cookie); rec.Code != http.StatusSeeOther {
			t.Errorf("an enterprise callback of the recorded provider answered %d, want 303: %s", rec.Code, rec.Body.String())
		}
		if n := m.calls.Load(); n != 1 {
			t.Errorf("the method was asked %d times for a matching enterprise callback, want once", n)
		}
	})
}

// TestACrossSiteCallbackPostIsAllowed holds that the callback bypasses the cross-origin check and the resume route does not.
func TestACrossSiteCallbackPostIsAllowed(t *testing.T) {
	m := &redirectMethod{}
	r := newRigMethod(t, m)
	r.allow("acct1")
	cookie := startRedirect(t, r, "")

	cross := []string{"Sec-Fetch-Site", "cross-site", "Origin", "https://evil.example.org"}
	cb := r.send(http.MethodPost, "/api/v1/auth/callback/password", "", append([]string{"Cookie", cookie}, cross...)...)
	if cb.Code != http.StatusOK || !strings.HasPrefix(cb.Header().Get("Content-Type"), "text/html") {
		t.Errorf("a cross-site callback POST answered %d %q, want the 200 bounce page: the callback must bypass the check",
			cb.Code, cb.Header().Get("Content-Type"))
	}
	if n := m.calls.Load(); n != 0 {
		t.Errorf("the method was asked %d times by the callback route, want none", n)
	}

	check := func(path string) error {
		req := httptest.NewRequest(http.MethodPost, path, nil)
		req.Header.Set("Sec-Fetch-Site", "cross-site")
		req.Header.Set("Origin", "https://evil.example.org")
		return r.m.CSRF().Check(req)
	}
	if err := check("/api/v1/auth/callback/password"); err != nil {
		t.Errorf("the cross-origin check refused a cross-site POST to the callback route: %v", err)
	}
	if err := check("/api/v1/auth/callback/password/resume"); err == nil {
		t.Error("the cross-origin check let a cross-site POST to the resume route through: the callback bypass covers it")
	}
}

func flowRecordOf(t *testing.T, r *rig, cookie string) record {
	t.Helper()
	_, token, ok := strings.Cut(cookie, "=")
	if !ok {
		t.Fatalf("the cookie %q carries no token", cookie)
	}
	raw, present, err := r.kv.Get(context.Background(), flowKey(token))
	if err != nil || !present {
		t.Fatalf("reading the flow record: present=%v err=%v", present, err)
	}
	rec, err := decodeRecord(raw)
	if err != nil {
		t.Fatalf("decoding the flow record: %v", err)
	}
	return rec
}

func editFlowRecord(t *testing.T, r *rig, cookie string, edit func(*record)) {
	t.Helper()
	_, token, _ := strings.Cut(cookie, "=")
	rec := flowRecordOf(t, r, cookie)
	edit(&rec)
	raw, err := json.Marshal(rec)
	if err != nil {
		t.Fatalf("encoding the flow record: %v", err)
	}
	if err := r.kv.Set(context.Background(), flowKey(token), raw, 0); err != nil {
		t.Fatalf("writing the flow record: %v", err)
	}
}

// TestBeginProcessingLosingTheRecordIsRefused holds that a lost compare-and-swap refuses the callback and never asks the method.
func TestBeginProcessingLosingTheRecordIsRefused(t *testing.T) {
	m := &redirectMethod{}
	r := newRigMethod(t, m)
	r.allow("acct1")
	cookie := startRedirect(t, r, "")
	_, token, _ := strings.Cut(cookie, "=")
	ctx := context.Background()
	rec, raw, err := r.p.loadFlow(ctx, token)
	if err != nil {
		t.Fatalf("loading the flow: %v", err)
	}
	editFlowRecord(t, r, cookie, func(rec *record) { rec.Seq = 99 })

	rt := &routes{product: r.p}
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/auth/callback/password", nil)
	c := &call{ctx: req.Context(), req: req, w: w, endpoint: mounttest.DefaultEndpoint}
	if rt.beginProcessing(c, token, rec, raw) {
		t.Error("beginProcessing accepted a record that had moved")
	}
	var fault speedhttp.Fault
	if !errors.As(c.err, &fault) || fault.Status != http.StatusUnauthorized {
		t.Errorf("beginProcessing losing the swap answered %v, want a 401 fault", c.err)
	}
	if n := m.calls.Load(); n != 0 {
		t.Errorf("the method was asked %d times, want none", n)
	}
}

// TestCallbackIDPInitiatedRecordsProviderAndResumableBinding holds that the bounce turns a cookie-less cross-site post into a same-origin resume that creates the record.
func TestCallbackIDPInitiatedRecordsProviderAndResumableBinding(t *testing.T) {
	build := func(allow bool) (*rig, *redirectMethod) {
		m := &redirectMethod{}
		m.answer = func(f authn.FlowState, in authn.StepInput) (authn.Step, error) {
			if f.Provider != "ent" {
				return authn.Step{}, errors.New("the flow carried no provider")
			}
			if in.Form.Get("SAMLResponse") != "assertion" {
				return authn.StepRefused(authn.ErrInvalidCredentials), nil
			}
			return authn.StepProven(authn.Proof{Account: "acct1", Methods: []authn.Method{authn.MethodPassword}}), nil
		}
		r := newRigMethod(t, m)
		r.allow("acct1")
		r.mfa.outcome = authn.MFAFactor
		r.mfa.offered = []authn.FactorRecord{{ID: "f1", Kind: authn.FactorTOTP}}
		r.p.sso = &fakeSSO{row: authn.SSOProvider{ID: "ent", Protocol: "password", Enabled: true, AllowIDPInitiated: allow}}
		return r, m
	}

	t.Run("a provider that allows it", func(t *testing.T) {
		r, m := build(true)
		page := r.bounceThrough(t, "ent", "SAMLResponse=assertion")
		out := r.resume(t, page, "")
		if out.Code != http.StatusSeeOther {
			t.Fatalf("an IdP-initiated sign-in needing a second factor answered %d, want 303: %s", out.Code, out.Body.String())
		}
		if n := m.calls.Load(); n != 1 {
			t.Errorf("the method was asked %d times, want once", n)
		}
		cookie := flowCookie(t, out)
		if got := flowRecordOf(t, r, cookie).Provider; got != "ent" {
			t.Errorf("the IdP-initiated record carries provider %q, want the path id", got)
		}
		current := r.send(http.MethodGet, "/api/v1/auth/flows/current", "", "Cookie", cookie)
		if current.Code != http.StatusOK {
			t.Fatalf("resuming the IdP-initiated flow answered %d, want 200: %s", current.Code, current.Body.String())
		}
		if st := decode(t, current.Body.String())["state"]; st != "second" {
			t.Errorf("the resumed flow state is %v, want second", st)
		}
	})

	t.Run("a provider that forbids it", func(t *testing.T) {
		r, m := build(false)
		page := r.bounceThrough(t, "ent", "SAMLResponse=assertion")
		out := r.resume(t, page, "")
		if out.Code != http.StatusUnauthorized {
			t.Fatalf("a resumed IdP-initiated post for a provider that forbids it answered %d, want 401: %s",
				out.Code, out.Body.String())
		}
		if n := m.calls.Load(); n != 0 {
			t.Errorf("the method was asked %d times, want none", n)
		}
		if len(r.kv.keys()) != 0 {
			t.Errorf("a refused IdP-initiated post left records %v, want none", r.kv.keys())
		}
	})
}

// TestACallbackReadsOnlyTheCookie holds that a header-only token is no binding on a callback, which falls to the IdP-initiated path.
func TestACallbackReadsOnlyTheCookie(t *testing.T) {
	m := &redirectMethod{}
	r := newRigMethod(t, m)
	r.allow("acct1")
	cookie := startRedirect(t, r, "")
	_, token, _ := strings.Cut(cookie, "=")

	out := r.send(http.MethodPost, "/api/v1/auth/callback/password/resume", "", flowHeader, token)
	if out.Code != http.StatusUnauthorized {
		t.Errorf("a callback carrying only the header answered %d, want 401: a callback reads only the cookie", out.Code)
	}
	if n := m.calls.Load(); n != 0 {
		t.Errorf("the method was asked %d times for a header-only callback, want none", n)
	}
}

// TestACallbackAnsweringRefusedOrUnavailableLeavesATombstone holds that a refusal or outage on a callback leaves a failed tombstone and no session.
func TestACallbackAnsweringRefusedOrUnavailableLeavesATombstone(t *testing.T) {
	cases := []struct {
		name string
		step authn.Step
		want int
	}{
		{"refused", authn.StepRefused(authn.ErrInvalidCredentials), http.StatusUnauthorized},
		{"unavailable", authn.StepUnavailable(authn.ErrUnavailable), http.StatusServiceUnavailable},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			answer := tc.step
			m := &redirectMethod{}
			m.answer = func(authn.FlowState, authn.StepInput) (authn.Step, error) { return answer, nil }
			r := newRigMethod(t, m)
			r.allow("acct1")
			sink := &auditing{}
			r.p.audit = sink
			cookie := startRedirect(t, r, "")

			out := r.send(http.MethodGet, "/api/v1/auth/callback/password", "", "Cookie", cookie)
			if out.Code != tc.want {
				t.Fatalf("the callback answered %d, want %d: %s", out.Code, tc.want, out.Body.String())
			}
			if len(r.sessions.created) != 0 {
				t.Errorf("a failed callback made %d sessions", len(r.sessions.created))
			}
			if st := flowRecordOf(t, r, cookie).State; st != stateFailed {
				t.Errorf("the flow state is %q, want the failed tombstone", st)
			}
			rows := sink.rows()
			if tc.name == "unavailable" {
				if len(rows) != 0 {
					t.Errorf("an unavailable callback wrote %d audit rows, want none: %+v", len(rows), rows)
				}
				return
			}
			if len(rows) != 1 || rows[0].Outcome != "failure" {
				t.Errorf("a refused callback wrote %+v, want one row with outcome failure", rows)
			}
		})
	}
}

// TestMaskEmail holds the mask shape: the first rune, three asterisks and the domain as written.
func TestMaskEmail(t *testing.T) {
	cases := []struct{ in, want string }{
		{"ann@home.org", "a***@home.org"},
		{"bob@example.test", "b***@example.test"},
		{"", ""},
		{"home.org", ""},
		{"@home.org", ""},
		{"ann@", ""},
		{"ann@home@org", ""},
	}
	for _, tc := range cases {
		if got := maskEmail(tc.in); got != tc.want {
			t.Errorf("maskEmail(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// TestAMaskedAddressTravelsOnlyWithLinkRequired holds that authn_email appears only when a link-required refusal named an address.
func TestAMaskedAddressTravelsOnlyWithLinkRequired(t *testing.T) {
	cases := []struct {
		name     string
		err      error
		email    string
		fragment string
	}{
		{
			name:     "a link-required naming an address",
			err:      fmt.Errorf("resolve: %w", &authn.LinkRequiredError{Email: "ann@home.org"}),
			email:    "a***@home.org",
			fragment: "/#authn_error=link-required&authn_email=a%2A%2A%2A%40home.org",
		},
		{
			name:     "a plain link-required",
			err:      fmt.Errorf("resolve: %w", authn.ErrLinkRequired),
			email:    "",
			fragment: "/#authn_error=link-required",
		},
		{
			name:     "another refusal",
			err:      fmt.Errorf("resolve: %w", authn.ErrInvalidCredentials),
			email:    "",
			fragment: "/#authn_error=login-failed",
		},
	}
	for _, tc := range cases {
		r := &routes{}
		c := &call{endpoint: "login"}
		r.fail(c, tc.err)
		if c.failureEmail != tc.email {
			t.Errorf("%s: failureEmail = %q, want %q", tc.name, c.failureEmail, tc.email)
		}
		target := landingTarget("/", c.failureEmail, c.err)
		if target != tc.fragment {
			t.Errorf("%s: landingTarget = %q, want %q", tc.name, target, tc.fragment)
		}
		if tc.email == "" && strings.Contains(target, landingEmailKey+"=") {
			t.Errorf("%s: the fragment %q carries an %s key, want none", tc.name, target, landingEmailKey)
		}
	}
}

// TestAFailedNavigationCallbackLandsBackInTheProduct holds that a refused navigation callback lands on the failure page with the code in the fragment.
func TestAFailedNavigationCallbackLandsBackInTheProduct(t *testing.T) {
	m := &redirectMethod{}
	m.answer = func(authn.FlowState, authn.StepInput) (authn.Step, error) {
		return authn.StepRefused(authn.ErrInvalidCredentials), nil
	}
	r := newRigMethod(t, m)
	r.allow("acct1")

	apiCookie := startRedirect(t, r, "")
	api := r.send(http.MethodGet, "/api/v1/auth/callback/password", "", "Cookie", apiCookie)
	if api.Code != http.StatusUnauthorized {
		t.Fatalf("the API callback answered %d, want the problem document: %s", api.Code, api.Body.String())
	}
	if api.Header().Get("Location") != "" {
		t.Errorf("the API callback answered a Location %q, want none", api.Header().Get("Location"))
	}
	if !strings.Contains(api.Body.String(), "urn:speed:authn:login-failed") {
		t.Errorf("the API callback answered %q, want the login-failed problem", api.Body.String())
	}

	navCookie := startRedirect(t, r, "")
	nav := r.send(http.MethodGet, "/api/v1/auth/callback/password", "",
		"Cookie", navCookie, "Sec-Fetch-Mode", "navigate")
	if nav.Code != http.StatusSeeOther {
		t.Fatalf("the navigation callback answered %d, want 303: %s", nav.Code, nav.Body.String())
	}
	if got := nav.Header().Get("Location"); got != "/#authn_error=login-failed" {
		t.Errorf("the navigation callback went to %q, want the failure landing with the code in the fragment", got)
	}
	if nav.Body.Len() != 0 {
		t.Errorf("the navigation callback wrote a body: %q", nav.Body.String())
	}
	if len(r.sessions.created) != 0 {
		t.Errorf("a failed callback made %d sessions", len(r.sessions.created))
	}
	if st := flowRecordOf(t, r, navCookie).State; st != stateFailed {
		t.Errorf("the flow state is %q, want the failed tombstone", st)
	}
}

// TestAFailedCallbackOfAnotherPurposeLandsOnItsOwnPage holds that a failed link, step-up or enrollment lands on its recorded return target with the same fragment.
func TestAFailedCallbackOfAnotherPurposeLandsOnItsOwnPage(t *testing.T) {
	m := &redirectMethod{}
	m.answer = func(authn.FlowState, authn.StepInput) (authn.Step, error) {
		return authn.StepRefused(authn.ErrInvalidCredentials), nil
	}
	r := newRigMethod(t, m)
	r.loginAsAuthenticated("acct1", "sid")
	r.allow("acct1")

	start := r.send(http.MethodPost, "/api/v1/auth/flows/authenticated",
		`{"purpose":"link","method":"password","return_to":"/account/callback"}`)
	if start.Code != http.StatusOK {
		t.Fatalf("POST flows/authenticated answered %d: %s", start.Code, start.Body.String())
	}

	cb := r.send(http.MethodGet, "/api/v1/auth/callback/password", "",
		"Cookie", flowCookie(t, start), "Sec-Fetch-Mode", "navigate")
	if cb.Code != http.StatusSeeOther {
		t.Fatalf("the refused link callback answered %d, want 303: %s", cb.Code, cb.Body.String())
	}
	if got := cb.Header().Get("Location"); got != "/account/callback#authn_error=login-failed" {
		t.Errorf("the refused link callback went to %q, want its own return target with the code", got)
	}
}

// TestACallbackOfAnUnregisteredMethodIsRefused holds that a flow naming an unassembled method is refused and no method dispatched.
func TestACallbackOfAnUnregisteredMethodIsRefused(t *testing.T) {
	m := &redirectMethod{}
	r := newRigMethod(t, m)
	r.allow("acct1")
	cookie := startRedirect(t, r, "")
	editFlowRecord(t, r, cookie, func(rec *record) { rec.Method = "ghost" })

	out := r.send(http.MethodGet, "/api/v1/auth/callback/password", "", "Cookie", cookie)
	if out.Code != http.StatusUnauthorized {
		t.Errorf("a callback of an unregistered method answered %d, want 401: %s", out.Code, out.Body.String())
	}
	if n := m.calls.Load(); n != 0 {
		t.Errorf("the method was asked %d times for an unregistered method, want none", n)
	}
}

// TestACallbackBodyOverTheLimitIsRefused holds the 512 KiB posted-body bound on both callback routes.
func TestACallbackBodyOverTheLimitIsRefused(t *testing.T) {
	m := &redirectMethod{}
	r := newRigMethod(t, m)
	r.allow("acct1")
	over := "SAMLResponse=" + strings.Repeat("x", maxCallbackFormBytes+1024)

	cookie := startRedirect(t, r, "")
	out := r.postForm("/api/v1/auth/callback/password/resume", over, cookie)
	if out.Code != http.StatusUnauthorized {
		t.Errorf("a resumed body over the limit answered %d, want 401: %s", out.Code, out.Body.String())
	}
	if n := m.calls.Load(); n != 0 {
		t.Errorf("the method was asked %d times for an oversized callback body, want none", n)
	}
	if st := flowRecordOf(t, r, cookie).State; st != stateFailed {
		t.Errorf("the flow state after an oversized resumed body is %q, want the failed tombstone", st)
	}

	cookie = startRedirect(t, r, "")
	within := r.postForm("/api/v1/auth/callback/password/resume", "SAMLResponse=ok", cookie)
	if within.Code != http.StatusSeeOther {
		t.Errorf("a resumed body within the limit answered %d, want 303: %s", within.Code, within.Body.String())
	}

	cookie = startRedirect(t, r, "")
	bounced := r.postForm("/api/v1/auth/callback/password", over, "")
	if bounced.Code != http.StatusUnauthorized || !strings.Contains(bounced.Body.String(), "login-failed") {
		t.Errorf("a callback body over the limit answered %d, want 401 login-failed: %s", bounced.Code, bounced.Body.String())
	}
	if st := flowRecordOf(t, r, cookie).State; st != stateFirst {
		t.Errorf("the flow state after an oversized callback body is %q, want it untouched at first", st)
	}
}

// TestACallbackWritesTheReturnTargetTheWayRedirectDoes holds that a local target drops dot segments and a non-ASCII byte is percent-escaped.
func TestACallbackWritesTheReturnTargetTheWayRedirectDoes(t *testing.T) {
	for _, c := range []struct {
		name     string
		returnTo string
		location string
	}{
		{"dot segments", "/a/../b", "/b"},
		{"trailing slash", "/a/b/", "/a/b/"},
		{"non-ASCII", "/café", "/caf%C3%A9"},
	} {
		t.Run(c.name, func(t *testing.T) {
			m := &redirectMethod{}
			r := newRigMethod(t, m)
			r.allow("acct1")

			cookie := startRedirect(t, r, `,"return_to":"`+c.returnTo+`"`)
			cb := r.send(http.MethodGet, "/api/v1/auth/callback/password", "", "Cookie", cookie)
			if cb.Code != http.StatusSeeOther {
				t.Fatalf("the callback answered %d, want 303: %s", cb.Code, cb.Body.String())
			}
			if got := cb.Header().Get("Location"); got != c.location {
				t.Errorf("Location = %q, want %q", got, c.location)
			}
		})
	}
}

type samlStandIn struct {
	authn.LoginMethod
	sp, idp atomic.Int32
}

func (*samlStandIn) Name() string { return "saml" }

func (*samlStandIn) Describe() authn.MethodDescriptor {
	return authn.MethodDescriptor{Name: "saml", Kind: authn.MethodKindEnterprise, Protocol: authn.MethodProtocolSAML}
}

func (*samlStandIn) Begin(context.Context, authn.Start) (authn.Step, error) {
	return authn.StepRedirect("assertion", []byte("req-1"), "https://idp.example.org/sso"), nil
}

func (m *samlStandIn) Answer(_ context.Context, f authn.FlowState, in authn.StepInput) (authn.Step, error) {
	if len(f.Private) > 0 {
		m.sp.Add(1)
		if in.Form.Get("InResponseTo") != string(f.Private) || in.Form.Get("SAMLResponse") != "valid" {
			return authn.StepRefused(authn.ErrInvalidCredentials), nil
		}
		return authn.StepProven(authn.Proof{Account: "acct-sp", Methods: []authn.Method{authn.MethodPassword}}), nil
	}
	m.idp.Add(1)
	if f.Provider != "ent" || in.Form.Get("InResponseTo") != "" || in.Form.Get("SAMLResponse") != "valid" {
		return authn.StepRefused(authn.ErrInvalidCredentials), nil
	}
	return authn.StepProven(authn.Proof{Account: "acct-idp", Methods: []authn.Method{authn.MethodPassword}}), nil
}

type providerRows struct {
	authn.SSOProviders
	rows map[string]authn.SSOProvider
}

func (s *providerRows) Get(_ context.Context, id string) (authn.SSOProvider, error) {
	row, ok := s.rows[id]
	if !ok {
		return authn.SSOProvider{}, authn.ErrProviderNotFound
	}
	return row, nil
}

func staleCookieRig(t *testing.T, allowIDP bool) (*rig, *redirectMethod, *samlStandIn) {
	t.Helper()
	social := &redirectMethod{name: "github"}
	saml := &samlStandIn{}
	r := newRigMethods(t, social, saml)
	r.allow("acct1")
	r.allow("acct-idp")
	r.allow("acct-sp")
	r.p.sso = &providerRows{rows: map[string]authn.SSOProvider{
		"ent": {ID: "ent", Protocol: "saml", Enabled: true, AllowIDPInitiated: allowIDP},
	}}
	return r, social, saml
}

func startGitHub(t *testing.T, r *rig) string {
	t.Helper()
	rec := r.send(http.MethodPost, "/api/v1/auth/flows", `{"method":"github"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("POST flows answered %d: %s", rec.Code, rec.Body.String())
	}
	return flowCookie(t, rec)
}

func postIDPInitiated(t *testing.T, r *rig, form, cookie string) *httptest.ResponseRecorder {
	t.Helper()
	page := r.bounceThrough(t, "ent", form)
	return r.resume(t, page, cookie, "Sec-Fetch-Mode", "navigate")
}

func rawFlowRecord(t *testing.T, r *rig, cookie string) []byte {
	t.Helper()
	_, token, _ := strings.Cut(cookie, "=")
	raw, present, err := r.kv.Get(context.Background(), flowKey(token))
	if err != nil {
		t.Fatalf("reading the flow record: %v", err)
	}
	if !present {
		return nil
	}
	return raw
}

func signedInFromTheIDP(t *testing.T, r *rig, out *httptest.ResponseRecorder, stale string) {
	t.Helper()
	if out.Code != http.StatusSeeOther {
		t.Fatalf("the IdP-initiated sign-in answered %d, want 303: %s", out.Code, out.Body.String())
	}
	if loc := out.Header().Get("Location"); loc != "/" {
		t.Errorf("the IdP-initiated sign-in landed on %q, want the default return target", loc)
	}
	if n := len(r.sessions.created); n != 1 {
		t.Fatalf("the IdP-initiated sign-in made %d sessions, want one", n)
	}
	if got := r.sessions.created[0].Account; got != "acct-idp" {
		t.Errorf("the session is for %q, want acct-idp", got)
	}
	if fresh := flowCookie(t, out); fresh == stale {
		t.Errorf("the IdP-initiated sign-in wrote back the stale flow cookie %q", stale)
	}
}

// TestAStaleFlowCookieDoesNotBlockAnIdPInitiatedSignIn holds that an unusable cookie binds nothing, so an allowing provider still signs the user in.
func TestAStaleFlowCookieDoesNotBlockAnIdPInitiatedSignIn(t *testing.T) {
	cases := []struct {
		name  string
		stale func(t *testing.T, r *rig, social *redirectMethod) string
	}{
		{"a done tombstone", func(t *testing.T, r *rig, _ *redirectMethod) string {
			cookie := startGitHub(t, r)
			if cb := r.send(http.MethodGet, "/api/v1/auth/callback/github", "", "Cookie", cookie); cb.Code != http.StatusSeeOther {
				t.Fatalf("the github callback answered %d: %s", cb.Code, cb.Body.String())
			}
			r.sessions.created = nil
			return cookie
		}},
		{"a failed tombstone", func(t *testing.T, r *rig, social *redirectMethod) string {
			social.answer = func(authn.FlowState, authn.StepInput) (authn.Step, error) {
				return authn.StepRefused(authn.ErrInvalidCredentials), nil
			}
			cookie := startGitHub(t, r)
			r.send(http.MethodGet, "/api/v1/auth/callback/github", "", "Cookie", cookie)
			if st := flowRecordOf(t, r, cookie).State; st != stateFailed {
				t.Fatalf("the github flow is %s, want failed", st)
			}
			return cookie
		}},
		{"a first-factor flow of another provider", func(t *testing.T, r *rig, _ *redirectMethod) string {
			return startGitHub(t, r)
		}},
		{"a record that is gone", func(t *testing.T, r *rig, _ *redirectMethod) string {
			cookie := startGitHub(t, r)
			_, token, _ := strings.Cut(cookie, "=")
			if err := r.kv.Delete(context.Background(), flowKey(token)); err != nil {
				t.Fatal(err)
			}
			return cookie
		}},
		{"a record that cannot be read", func(t *testing.T, r *rig, _ *redirectMethod) string {
			cookie := startGitHub(t, r)
			_, token, _ := strings.Cut(cookie, "=")
			if err := r.kv.Set(context.Background(), flowKey(token), []byte("not a record"), 0); err != nil {
				t.Fatal(err)
			}
			return cookie
		}},
		{"a record bound to another token", func(t *testing.T, r *rig, _ *redirectMethod) string {
			cookie := startGitHub(t, r)
			editFlowRecord(t, r, cookie, func(rec *record) { rec.Binding = tokenDigest("another-token") })
			return cookie
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r, social, _ := staleCookieRig(t, true)
			cookie := tc.stale(t, r, social)
			before := rawFlowRecord(t, r, cookie)

			signedInFromTheIDP(t, r, postIDPInitiated(t, r, "SAMLResponse=valid", cookie), cookie)
			if after := rawFlowRecord(t, r, cookie); string(after) != string(before) {
				t.Errorf("the IdP-initiated sign-in rewrote the stale flow's record from %s to %s", before, after)
			}
		})
	}
}

// TestAnUnprovenIdPInitiatedPostLeavesALiveFlowAlone holds that a refused post neither ends nor displaces another provider's live flow.
func TestAnUnprovenIdPInitiatedPostLeavesALiveFlowAlone(t *testing.T) {
	r, _, saml := staleCookieRig(t, true)
	cookie := startGitHub(t, r)
	before := rawFlowRecord(t, r, cookie)

	out := postIDPInitiated(t, r, "SAMLResponse=forged", cookie)
	if n := saml.idp.Load(); n != 1 {
		t.Fatalf("the method was asked %d times as IdP-initiated, want once", n)
	}
	if loc := out.Header().Get("Location"); !strings.Contains(loc, "authn_error=login-failed") {
		t.Errorf("the unproven post landed on %q, want the login-failed landing", loc)
	}
	if n := len(r.sessions.created); n != 0 {
		t.Fatalf("an unproven post made %d sessions", n)
	}
	for _, c := range out.Result().Cookies() {
		if strings.Contains(c.Name, "authn-flow") {
			t.Errorf("an unproven post wrote the flow cookie %s=%s over the browser's", c.Name, c.Value)
		}
	}
	if after := rawFlowRecord(t, r, cookie); string(after) != string(before) {
		t.Errorf("an unproven post rewrote the live flow's record from %s to %s", before, after)
	}
	cb := r.send(http.MethodGet, "/api/v1/auth/callback/github", "", "Cookie", cookie)
	if cb.Code != http.StatusSeeOther || strings.Contains(cb.Header().Get("Location"), "authn_error") {
		t.Errorf("the live flow could not complete after the unproven post: %d %q", cb.Code, cb.Header().Get("Location"))
	}
	if n := len(r.sessions.created); n != 1 || r.sessions.created[0].Account != "acct1" {
		t.Errorf("the live flow made sessions %+v, want one for acct1", r.sessions.created)
	}
}

// TestAStaleFlowCookieKeepsAProviderThatForbidsIdPInitiatedRefused holds that a forbidding provider still answers flow-invalid, asking no method and writing nothing.
func TestAStaleFlowCookieKeepsAProviderThatForbidsIdPInitiatedRefused(t *testing.T) {
	r, _, saml := staleCookieRig(t, false)
	cookie := startGitHub(t, r)
	keys := len(r.kv.keys())

	out := postIDPInitiated(t, r, "SAMLResponse=valid", cookie)
	if loc := out.Header().Get("Location"); !strings.Contains(loc, "authn_error=flow-invalid") {
		t.Errorf("a provider that forbids IdP-initiated landed on %q, want flow-invalid", loc)
	}
	if n := saml.idp.Load() + saml.sp.Load(); n != 0 {
		t.Errorf("the method was asked %d times, want none", n)
	}
	if got := len(r.kv.keys()); got != keys {
		t.Errorf("a refused post wrote records: %d -> %d", keys, got)
	}
	if cookies := out.Result().Cookies(); len(cookies) != 0 {
		t.Errorf("a refused post set cookies %v, want none", cookies)
	}
}

// TestAFirstFactorFlowOfTheSameProviderStaysBound holds that an IdP-initiated response is delivered to the bound flow, which refuses it, and no session is made.
func TestAFirstFactorFlowOfTheSameProviderStaysBound(t *testing.T) {
	r, _, saml := staleCookieRig(t, true)
	rec := r.send(http.MethodPost, "/api/v1/auth/flows", `{"method":"saml","input":{"provider":"ent"}}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("POST flows answered %d: %s", rec.Code, rec.Body.String())
	}
	cookie := flowCookie(t, rec)

	postIDPInitiated(t, r, "SAMLResponse=valid", cookie)
	if sp, idp := saml.sp.Load(), saml.idp.Load(); sp != 1 || idp != 0 {
		t.Errorf("the method was asked sp=%d idp=%d, want the bound flow once", sp, idp)
	}
	if n := len(r.sessions.created); n != 0 {
		t.Errorf("an IdP-initiated response against a live SP flow made %d sessions", n)
	}
}

// TestAFailedGETCallbackOfALinkFlowPastTheFirstFactorLandsOnItsReturnTo holds that such a callback answers flow-invalid and lands on the page the link started from.
func TestAFailedGETCallbackOfALinkFlowPastTheFirstFactorLandsOnItsReturnTo(t *testing.T) {
	m := &redirectMethod{}
	m.answer = func(authn.FlowState, authn.StepInput) (authn.Step, error) {
		return authn.StepProven(authn.Proof{External: &authn.External{
			Provider: "google", Subject: "g1", Email: "ann@x.org", EmailVerified: true,
		}}), nil
	}
	r := newRigMethod(t, m)
	r.loginAsAuthenticated("acct1", "sid")
	r.allow("acct1")
	start := r.send(http.MethodPost, "/api/v1/auth/flows/authenticated",
		`{"purpose":"link","method":"password","return_to":"/account/callback"}`)
	if start.Code != http.StatusOK {
		t.Fatalf("POST flows/authenticated answered %d: %s", start.Code, start.Body.String())
	}
	cookie := flowCookie(t, start)
	if cb := r.send(http.MethodGet, "/api/v1/auth/callback/password", "", "Cookie", cookie); cb.Code != http.StatusSeeOther {
		t.Fatalf("the link callback answered %d, want 303: %s", cb.Code, cb.Body.String())
	}
	if st := flowRecordOf(t, r, cookie).State; st == stateFirst {
		t.Fatalf("the link flow is still at the first factor after its callback")
	}

	again := r.send(http.MethodGet, "/api/v1/auth/callback/password", "",
		"Cookie", cookie, "Sec-Fetch-Mode", "navigate")
	if again.Code != http.StatusSeeOther {
		t.Fatalf("the second link callback answered %d, want 303: %s", again.Code, again.Body.String())
	}
	if got := again.Header().Get("Location"); got != "/account/callback#authn_error=flow-invalid" {
		t.Errorf("the second link callback went to %q, want its own return target with flow-invalid", got)
	}
	if n := m.calls.Load(); n != 1 {
		t.Errorf("the method was asked %d times, want once", n)
	}
}

// TestAStoreOutageOnAFlowCookiePostIsUnavailable holds that an unreadable flow record answers unavailable and never enters the IdP-initiated path.
func TestAStoreOutageOnAFlowCookiePostIsUnavailable(t *testing.T) {
	r, _, saml := staleCookieRig(t, true)
	cookie := startGitHub(t, r)
	page := r.bounceThrough(t, "ent", "SAMLResponse=valid")
	r.kv.failing = true

	out := r.resume(t, page, cookie, "Sec-Fetch-Mode", "navigate")
	if loc := out.Header().Get("Location"); !strings.Contains(loc, "authn_error=unavailable") {
		t.Errorf("a resume post the store could not answer landed on %q, want unavailable", loc)
	}
	if n := saml.idp.Load() + saml.sp.Load(); n != 0 {
		t.Errorf("the method was asked %d times, want none", n)
	}
	if n := len(r.sessions.created); n != 0 {
		t.Errorf("a resume post the store could not answer made %d sessions", n)
	}
}
