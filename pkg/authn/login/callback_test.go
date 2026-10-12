package login

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/vislake/speed/pkg/authn"
)

// fakeSSO is an enterprise provider register a case scripts. Only Get is
// reached on the paths these cases drive.
type fakeSSO struct {
	authn.SSOProviders
	row authn.SSOProvider
	err error
}

func (s *fakeSSO) Get(context.Context, string) (authn.SSOProvider, error) { return s.row, s.err }

// redirectMethod is a method whose Begin sends the user to a provider and whose
// Answer a case scripts. calls counts the answers atomically, and answer, when
// set, runs inside Answer for a case that needs to hold it. name defaults to
// "password"; a case that stands in for an enterprise SSO protocol method names
// the method "oidc" or "saml", whose callback path carries a provider id.
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

// startRedirect begins a login flow whose first factor returns a redirect and
// returns the flow cookie. The start request names the rig's one method.
func startRedirect(t *testing.T, r *rig, extra string) string {
	t.Helper()
	body := `{"method":"` + r.p.methodNames[0] + `"` + extra + `}`
	rec := r.send(http.MethodPost, "/auth/flows", body)
	if rec.Code != http.StatusOK {
		t.Fatalf("POST flows answered %d: %s", rec.Code, rec.Body.String())
	}
	return flowCookie(t, rec)
}

// TestACallbackCompletesACookieLoginOnce holds that the callback's terminal
// action runs at most once: the callback makes a session and swaps the record to
// the done tombstone, so a POST complete against the same flow is refused and
// makes no second session.
func TestACallbackCompletesACookieLoginOnce(t *testing.T) {
	m := &redirectMethod{}
	r := newRigMethod(t, m)
	r.allow("acct1")

	cookie := startRedirect(t, r, "")

	cb := r.send(http.MethodGet, "/auth/callback/password", "", "Cookie", cookie)
	if cb.Code != http.StatusSeeOther {
		t.Fatalf("the callback answered %d, want 303: %s", cb.Code, cb.Body.String())
	}
	if len(r.sessions.created) != 1 {
		t.Fatalf("the callback made %d sessions, want one", len(r.sessions.created))
	}

	again := r.send(http.MethodPost, "/auth/flows/answer", `{"seq":1,"step":"complete"}`, "Cookie", cookie)
	if again.Code != http.StatusUnauthorized {
		t.Errorf("a complete against a callback-completed flow answered %d, want 401: %s", again.Code, again.Body.String())
	}
	if len(r.sessions.created) != 1 {
		t.Errorf("the flow's terminal action ran twice: %d sessions", len(r.sessions.created))
	}
}

// TestAConcurrentSecondCallbackIsRefused holds the replay guard: the flow is
// moved to processing before the method is asked, so a second callback that read
// the same record loses the swap and the authorization code is exchanged once.
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
	go func() { first <- r.send(http.MethodGet, "/auth/callback/password", "", "Cookie", cookie) }()
	<-started // the first callback is inside the method's answer
	second := r.send(http.MethodGet, "/auth/callback/password", "", "Cookie", cookie)
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

// TestACallbackOfAnyPurposeIsAccepted holds that the callback only needs the flow
// to exist and be at the first factor: a link flow started on the authenticated
// route continues through its provider's callback.
func TestACallbackOfAnyPurposeIsAccepted(t *testing.T) {
	m := &redirectMethod{}
	m.answer = func(authn.FlowState, authn.StepInput) (authn.Step, error) {
		return authn.StepProven(authn.Proof{External: &authn.External{
			Provider: "google", Subject: "g1", Email: "ann@x.org", EmailVerified: true,
		}}), nil
	}
	r := newRigMethod(t, m)
	r.loginAsAuthenticated("acct1", "sid")
	r.allow("acct1")

	start := r.send(http.MethodPost, "/auth/flows/authenticated", `{"purpose":"link","method":"password"}`)
	if start.Code != http.StatusOK {
		t.Fatalf("POST flows/authenticated answered %d: %s", start.Code, start.Body.String())
	}
	cookie := flowCookie(t, start)

	cb := r.send(http.MethodGet, "/auth/callback/password", "", "Cookie", cookie)
	if cb.Code != http.StatusOK {
		t.Fatalf("the link callback answered %d, want 200: %s", cb.Code, cb.Body.String())
	}
	if k := decode(t, cb.Body.String())["next"].(map[string]any)["kind"]; k != "complete" {
		t.Errorf("the link callback answered next kind %v, want complete", k)
	}
}

// TestACallbackNeedingASecondFactorRedirects holds that a callback whose login
// still needs a second factor answers 303 to the flow's return_to, not a JSON
// step body, and leaves the flow in the cookie.
func TestACallbackNeedingASecondFactorRedirects(t *testing.T) {
	m := &redirectMethod{}
	r := newRigMethod(t, m)
	r.allow("acct1")
	r.mfa.outcome = authn.MFAFactor
	r.mfa.offered = []authn.FactorRecord{{ID: "f1", Kind: authn.FactorTOTP}}

	cookie := startRedirect(t, r, `,"return_to":"https://app.example.org/after"`)

	cb := r.send(http.MethodGet, "/auth/callback/password", "", "Cookie", cookie)
	if cb.Code != http.StatusSeeOther {
		t.Fatalf("a callback needing a second factor answered %d, want 303: %s", cb.Code, cb.Body.String())
	}
	if loc := cb.Header().Get("Location"); loc != "https://app.example.org/after" {
		t.Errorf("the redirect went to %q, want the flow's return_to", loc)
	}
	// The flow is still in the cookie: GET flows/current reports the second state.
	current := r.send(http.MethodGet, "/auth/flows/current", "", "Cookie", cookie)
	if current.Code != http.StatusOK {
		t.Fatalf("GET flows/current after the callback answered %d: %s", current.Code, current.Body.String())
	}
	if st := decode(t, current.Body.String())["state"]; st != "second" {
		t.Errorf("the flow state after the callback is %v, want second", st)
	}
}

// TestCallbackIDPInitiatedIsGated holds the guards of an IdP-initiated sign-in:
// only a POST from an enabled provider that allows it is accepted, and the
// default return target falls back to "/" when it is not trusted.
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
		if rec := r.send(http.MethodGet, "/auth/callback/ent", ""); rec.Code != http.StatusUnauthorized {
			t.Errorf("a GET IdP-initiated callback answered %d, want 401: %s", rec.Code, rec.Body.String())
		}
	})

	t.Run("no provider register refuses", func(t *testing.T) {
		r := body()
		if rec := r.send(http.MethodPost, "/auth/callback/ent", ""); rec.Code != http.StatusUnauthorized {
			t.Errorf("a POST with no provider register answered %d, want 401: %s", rec.Code, rec.Body.String())
		}
	})

	t.Run("a disabled provider refuses", func(t *testing.T) {
		r := body()
		r.p.sso = &fakeSSO{row: authn.SSOProvider{ID: "ent", Protocol: "password", Enabled: false, AllowIDPInitiated: true}}
		if rec := r.send(http.MethodPost, "/auth/callback/ent", ""); rec.Code != http.StatusUnauthorized {
			t.Errorf("a disabled provider answered %d, want 401: %s", rec.Code, rec.Body.String())
		}
	})

	t.Run("a provider that forbids IdP-initiated refuses", func(t *testing.T) {
		r := body()
		r.p.sso = &fakeSSO{row: authn.SSOProvider{ID: "ent", Protocol: "password", Enabled: true, AllowIDPInitiated: false}}
		if rec := r.send(http.MethodPost, "/auth/callback/ent", ""); rec.Code != http.StatusUnauthorized {
			t.Errorf("a provider that forbids IdP-initiated answered %d, want 401: %s", rec.Code, rec.Body.String())
		}
	})

	t.Run("a provider whose protocol is not a registered method refuses", func(t *testing.T) {
		r := body()
		r.p.sso = &fakeSSO{row: authn.SSOProvider{ID: "ent", Protocol: "oidc", Enabled: true, AllowIDPInitiated: true}}
		if rec := r.send(http.MethodPost, "/auth/callback/ent", ""); rec.Code != http.StatusUnauthorized {
			t.Errorf("a provider whose protocol is not a registered method answered %d, want 401: %s", rec.Code, rec.Body.String())
		}
	})

	t.Run("an enabled provider that allows it proceeds", func(t *testing.T) {
		r := body()
		r.p.sso = &fakeSSO{row: authn.SSOProvider{ID: "ent", Protocol: "password", Enabled: true, AllowIDPInitiated: true}}
		rec := r.send(http.MethodPost, "/auth/callback/ent", "")
		if rec.Code != http.StatusSeeOther {
			t.Fatalf("an accepted IdP-initiated sign-in answered %d, want 303: %s", rec.Code, rec.Body.String())
		}
		if loc := rec.Header().Get("Location"); loc != "/" {
			t.Errorf("the redirect went to %q, want the default return target", loc)
		}
	})
}

// TestACallbackMustNameTheFlowsProvider holds the callback's provider check: the
// engine compares the path's :provider with the provider the flow recorded — the
// method's own name for a social method, the start input's identifier for an
// enterprise provider — and asks no method when they differ.
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
		if rec := r.send(http.MethodGet, "/auth/callback/other", "", "Cookie", cookie); rec.Code != http.StatusUnauthorized {
			t.Errorf("a social callback of another name answered %d, want 401: %s", rec.Code, rec.Body.String())
		}
		if n := m.calls.Load(); n != 0 {
			t.Errorf("the method was asked %d times for a mismatched social callback, want none", n)
		}
	})

	// A social method's own recorded name is fixed to the method, so a start
	// input cannot aim its callback elsewhere.
	t.Run("a social start input cannot change the recorded provider", func(t *testing.T) {
		r, m := newSocial()
		r.allow("acct1")
		cookie := startRedirect(t, r, `,"input":{"provider":"ent"}`)
		if got := flowProviderOf(t, r, cookie); got != "password" {
			t.Errorf("a social flow with input.provider recorded provider %q, want the method name", got)
		}
		if rec := r.send(http.MethodGet, "/auth/callback/ent", "", "Cookie", cookie); rec.Code != http.StatusUnauthorized {
			t.Errorf("a social callback aimed at the start input's provider answered %d, want 401: %s", rec.Code, rec.Body.String())
		}
		if n := m.calls.Load(); n != 0 {
			t.Errorf("the method was asked %d times for a social callback of another name, want none", n)
		}
	})

	t.Run("an enterprise callback of another provider is refused", func(t *testing.T) {
		r, m := newEnterprise()
		r.allow("acct1")
		// The flow records the input's provider identifier, not the method name.
		cookie := startRedirect(t, r, `,"input":{"provider":"ent"}`)
		if rec := r.send(http.MethodGet, "/auth/callback/other", "", "Cookie", cookie); rec.Code != http.StatusUnauthorized {
			t.Errorf("an enterprise callback of another provider answered %d, want 401: %s", rec.Code, rec.Body.String())
		}
		if n := m.calls.Load(); n != 0 {
			t.Errorf("the method was asked %d times for a mismatched enterprise callback, want none", n)
		}
	})

	// The compare is verbatim: a provider of the same length but different
	// content, and one differing only in case, are both refused.
	t.Run("a same-length different provider is refused", func(t *testing.T) {
		r, m := newEnterprise()
		r.allow("acct1")
		cookie := startRedirect(t, r, `,"input":{"provider":"entA"}`)
		if rec := r.send(http.MethodGet, "/auth/callback/entB", "", "Cookie", cookie); rec.Code != http.StatusUnauthorized {
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
		if rec := r.send(http.MethodGet, "/auth/callback/ENT", "", "Cookie", cookie); rec.Code != http.StatusUnauthorized {
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
		if rec := r.send(http.MethodGet, "/auth/callback/ent", "", "Cookie", cookie); rec.Code != http.StatusSeeOther {
			t.Errorf("an enterprise callback of the recorded provider answered %d, want 303: %s", rec.Code, rec.Body.String())
		}
		if n := m.calls.Load(); n != 1 {
			t.Errorf("the method was asked %d times for a matching enterprise callback, want once", n)
		}
	})
}

// TestACrossSiteCallbackPostIsAllowed holds that the callback route bypasses the
// cross-origin check while another flow route keeps it.
func TestACrossSiteCallbackPostIsAllowed(t *testing.T) {
	m := &redirectMethod{}
	r := newRigMethod(t, m)
	r.allow("acct1")
	cookie := startRedirect(t, r, "")

	cross := []string{"Sec-Fetch-Site", "cross-site", "Origin", "https://evil.example.org"}
	cb := r.send(http.MethodPost, "/auth/callback/password", "", append([]string{"Cookie", cookie}, cross...)...)
	if cb.Code != http.StatusSeeOther {
		t.Errorf("a cross-site callback POST answered %d, want 303: the callback must bypass the check", cb.Code)
	}
}

// flowRecordOf reads and decodes the flow record a cookie's token keys.
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

// editFlowRecord rewrites the flow record a cookie names, as a competing writer
// would.
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

// TestBeginProcessingLosingTheRecordIsRefused holds the flow's atomicity guard:
// when a competing writer moved the record so the compare-and-swap from the
// bytes just read cannot match, beginProcessing refuses and the method is never
// asked. It is the branch a lost compare-and-swap reaches, which the concurrent
// callback case stops short of.
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
	// A competing writer replaces the record, so the bytes just read no longer
	// match and the swap must lose.
	editFlowRecord(t, r, cookie, func(rec *record) { rec.Seq = 99 })

	rt := &routes{product: r.p}
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/auth/callback/password", nil)
	if rt.beginProcessing(c, token, rec, raw) {
		t.Error("beginProcessing accepted a record that had moved")
	}
	if w.Code != http.StatusUnauthorized {
		t.Errorf("beginProcessing losing the swap answered %d, want 401", w.Code)
	}
	if n := m.calls.Load(); n != 0 {
		t.Errorf("the method was asked %d times, want none", n)
	}
}

// TestCallbackIDPInitiatedRecordsProviderAndResumableBinding holds that the
// record an IdP-initiated sign-in creates carries the path's provider id — which
// the method reads as FlowState.Provider — and a binding the issued cookie
// resumes, so a login that still needs a second factor continues rather than
// being lost.
func TestCallbackIDPInitiatedRecordsProviderAndResumableBinding(t *testing.T) {
	m := &redirectMethod{}
	m.answer = func(f authn.FlowState, in authn.StepInput) (authn.Step, error) {
		if f.Provider != "ent" {
			return authn.Step{}, errors.New("the flow carried no provider")
		}
		return authn.StepProven(authn.Proof{Account: "acct1", Methods: []authn.Method{authn.MethodPassword}}), nil
	}
	r := newRigMethod(t, m)
	r.allow("acct1")
	r.mfa.outcome = authn.MFAFactor
	r.mfa.offered = []authn.FactorRecord{{ID: "f1", Kind: authn.FactorTOTP}}
	r.p.sso = &fakeSSO{row: authn.SSOProvider{ID: "ent", Protocol: "password", Enabled: true, AllowIDPInitiated: true}}

	out := r.send(http.MethodPost, "/auth/callback/ent", "")
	if out.Code != http.StatusSeeOther {
		t.Fatalf("an IdP-initiated sign-in needing a second factor answered %d, want 303: %s", out.Code, out.Body.String())
	}
	cookie := flowCookie(t, out)
	if got := flowRecordOf(t, r, cookie).Provider; got != "ent" {
		t.Errorf("the IdP-initiated record carries provider %q, want the path id", got)
	}
	current := r.send(http.MethodGet, "/auth/flows/current", "", "Cookie", cookie)
	if current.Code != http.StatusOK {
		t.Fatalf("resuming the IdP-initiated flow answered %d, want 200: %s", current.Code, current.Body.String())
	}
	if st := decode(t, current.Body.String())["state"]; st != "second" {
		t.Errorf("the resumed flow state is %v, want second", st)
	}
}

// TestACallbackReadsOnlyTheCookie holds that a callback carrying the flow token
// only in the Authn-Flow header is treated as carrying no binding: a user agent
// navigation cannot set a header, so the callback reads the cookie alone and
// falls to the IdP-initiated path.
func TestACallbackReadsOnlyTheCookie(t *testing.T) {
	m := &redirectMethod{}
	r := newRigMethod(t, m)
	r.allow("acct1")
	cookie := startRedirect(t, r, "")
	_, token, _ := strings.Cut(cookie, "=")

	out := r.send(http.MethodPost, "/auth/callback/password", "", flowHeader, token)
	if out.Code != http.StatusUnauthorized {
		t.Errorf("a callback carrying only the header answered %d, want 401: a callback reads only the cookie", out.Code)
	}
	if n := m.calls.Load(); n != 0 {
		t.Errorf("the method was asked %d times for a header-only callback, want none", n)
	}
}

// TestACallbackAnsweringRefusedOrUnavailableLeavesATombstone holds that a method
// that refuses or reports an outage on its callback leaves the flow a failed
// tombstone and issues no session.
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
			cookie := startRedirect(t, r, "")

			out := r.send(http.MethodGet, "/auth/callback/password", "", "Cookie", cookie)
			if out.Code != tc.want {
				t.Fatalf("the callback answered %d, want %d: %s", out.Code, tc.want, out.Body.String())
			}
			if len(r.sessions.created) != 0 {
				t.Errorf("a failed callback made %d sessions", len(r.sessions.created))
			}
			if st := flowRecordOf(t, r, cookie).State; st != stateFailed {
				t.Errorf("the flow state is %q, want the failed tombstone", st)
			}
		})
	}
}

// TestACallbackOfAnUnregisteredMethodIsRefused holds that a flow record naming a
// method the assembly does not hold cannot be walked into a proof: the callback
// is refused and no method is dispatched.
func TestACallbackOfAnUnregisteredMethodIsRefused(t *testing.T) {
	m := &redirectMethod{}
	r := newRigMethod(t, m)
	r.allow("acct1")
	cookie := startRedirect(t, r, "")
	editFlowRecord(t, r, cookie, func(rec *record) { rec.Method = "ghost" })

	out := r.send(http.MethodGet, "/auth/callback/password", "", "Cookie", cookie)
	if out.Code != http.StatusUnauthorized {
		t.Errorf("a callback of an unregistered method answered %d, want 401: %s", out.Code, out.Body.String())
	}
	if n := m.calls.Load(); n != 0 {
		t.Errorf("the method was asked %d times for an unregistered method, want none", n)
	}
}

// TestACallbackBodyOverTheLimitIsRefused holds the callback's posted-body bound
// (the design's 512 KiB for SAML's ACS): a body over it is refused before any
// method reads it, and a body within it is dispatched.
func TestACallbackBodyOverTheLimitIsRefused(t *testing.T) {
	m := &redirectMethod{}
	r := newRigMethod(t, m)
	r.allow("acct1")

	cookie := startRedirect(t, r, "")
	over := "SAMLResponse=" + strings.Repeat("x", maxCallbackFormBytes+1024)
	out := r.postForm("/auth/callback/password", over, cookie)
	if out.Code != http.StatusUnauthorized {
		t.Errorf("a callback body over the limit answered %d, want 401: %s", out.Code, out.Body.String())
	}
	if n := m.calls.Load(); n != 0 {
		t.Errorf("the method was asked %d times for an oversized callback body, want none", n)
	}

	cookie = startRedirect(t, r, "")
	within := r.postForm("/auth/callback/password", "SAMLResponse=ok", cookie)
	if within.Code != http.StatusSeeOther {
		t.Errorf("a callback body within the limit answered %d, want 303: %s", within.Code, within.Body.String())
	}
}
