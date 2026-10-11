package login

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"

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
// set, runs inside Answer for a case that needs to hold it.
type redirectMethod struct {
	authn.LoginMethod
	calls  atomic.Int32
	answer func(authn.FlowState, authn.StepInput) (authn.Step, error)
}

func (*redirectMethod) Name() string { return "password" }

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
// returns the flow cookie.
func startRedirect(t *testing.T, r *rig, extra string) string {
	t.Helper()
	body := `{"method":"password"` + extra + `}`
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

// TestCallbackMethodSelectsProviderThenRecorded holds the method a callback path
// serves: a name the provider segment matches wins, and the method the flow
// recorded is the fallback for an enterprise provider's identifier.
func TestCallbackMethodSelectsProviderThenRecorded(t *testing.T) {
	m := &redirectMethod{}
	r := newRigMethod(t, m)
	mount := &routes{product: r.p, endpoint: r.p.mounter.Endpoint()}

	if got, ok := mount.callbackMethod("password", "other"); !ok || got.Name() != "password" {
		t.Errorf("callbackMethod(password, other) = %v, %v; want the password method", got, ok)
	}
	if got, ok := mount.callbackMethod("ent", "password"); !ok || got.Name() != "password" {
		t.Errorf("callbackMethod(ent, password) = %v, %v; want the recorded method", got, ok)
	}
	if _, ok := mount.callbackMethod("ent", ""); ok {
		t.Error("callbackMethod(ent, empty) found a method with no recorded name and no provider match")
	}
	if _, ok := mount.callbackMethod("ent", "missing"); ok {
		t.Error("callbackMethod found a method that is not taken in")
	}
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
