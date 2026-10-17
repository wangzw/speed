package impersonation

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/vislake/speed/pkg/audit"
	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/authn/authntest"
	"github.com/vislake/speed/pkg/authn/gate"
	authnmount "github.com/vislake/speed/pkg/authn/mount"
	"github.com/vislake/speed/pkg/authn/session"
	"github.com/vislake/speed/pkg/config"
	"github.com/vislake/speed/pkg/core"
	"github.com/vislake/speed/pkg/db/sqlite"
	eventmemory "github.com/vislake/speed/pkg/event/memory"
	speedhttp "github.com/vislake/speed/pkg/http"
	kvmemory "github.com/vislake/speed/pkg/kv/memory"
	"github.com/vislake/speed/pkg/log"
	"github.com/vislake/speed/pkg/settings"
)

// The accounts the HTTP cases act as and impersonate.
const (
	httpActorID  = authn.AccountID("acct-http-admin")
	httpTargetID = authn.AccountID("acct-http-user")
	httpTenant   = "tenant-http"
)

// The names of the stand-in modules that provide what this module requires.
const (
	httpAccountsModule    = "stand-in.http-accounts"
	httpMembershipsModule = "stand-in.http-memberships"
	httpMFAModule         = "stand-in.http-mfa"
	httpAuditModule       = "stand-in.http-audit"
	httpScopeModule       = "stand-in.http-scope"
	httpDeciderModule     = "stand-in.http-decider"
	httpLoggingModule     = "stand-in.http-logging"
)

// listenerRecord is what an entry point writes once an endpoint's address is
// bound: the port the operating system picked for an endpoint configured on 0 is
// named nowhere else.
const listenerRecord = "an HTTP listener is listening"

// boundAddresses keeps the log records an entry point wrote, so that a case can
// read back where it bound itself.
type boundAddresses struct {
	mu      sync.Mutex
	records []slog.Record
}

func (b *boundAddresses) Enabled(context.Context, slog.Level) bool { return true }
func (b *boundAddresses) Handle(_ context.Context, r slog.Record) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.records = append(b.records, r.Clone())
	return nil
}
func (b *boundAddresses) WithAttrs([]slog.Attr) slog.Handler { return b }
func (b *boundAddresses) WithGroup(string) slog.Handler      { return b }

func (b *boundAddresses) addressOf(endpoint string) string {
	b.mu.Lock()
	defer b.mu.Unlock()
	for _, r := range b.records {
		if r.Message != listenerRecord {
			continue
		}
		var name, address string
		r.Attrs(func(a slog.Attr) bool {
			switch a.Key {
			case "listener":
				name = a.Value.String()
			case "address":
				address = a.Value.String()
			}
			return true
		})
		if name == endpoint {
			return address
		}
	}
	return ""
}

// httpLogging is the logging capability over the capture.
type httpLogging struct{ logger *slog.Logger }

func (l httpLogging) Named(string) *slog.Logger { return l.logger }
func (httpLogging) Redaction() log.Redaction    { return noRedaction{} }

type noRedaction struct{}

func (noRedaction) AddKeys(...string)              {}
func (noRedaction) AddPattern(string, log.Matcher) {}

// provide declares a module that provides instance under token. A stand-in has
// no configuration of its own and no lifecycle beyond handing the instance over.
func provide(name string, instance any, token core.Token) core.Module {
	return core.Module{
		Name:     name,
		Provides: []core.Provision{{Token: token, Exclusive: true}},
		New:      func(context.Context, *core.Registry) (any, error) { return instance, nil },
	}
}

// httpReply is what a client got back.
type httpReply struct {
	status int
	body   string
}

// sessionView is the part of GET /auth/session these cases read.
type sessionView struct {
	Authenticated bool   `json:"authenticated"`
	AccountID     string `json:"account_id"`
	ActorID       string `json:"actor_id"`
	Impersonating bool   `json:"impersonating"`
}

// httpHarness is one running assembly: the bound address, a cookie-jarred client
// and the capabilities a case uses to set its scene up.
type httpHarness struct {
	t           *testing.T
	ctx         context.Context
	base        string
	sessions    authn.Sessions
	accounts    *fakeAccounts
	memberships *fakeMemberships
	jar         http.CookieJar
	client      *http.Client
	bare        *http.Client
}

// assembleOverHTTP drives the real stack to Serve and runs use against it. The
// session store, the gateway and the mounter are the real modules: the token
// carried by the cookie is verified against the database by the same code a
// deployment runs. The account, membership, factor, audit, scope and decision
// capabilities are stand-ins, so a case needs no registered factor to impersonate.
func assembleOverHTTP(t *testing.T, use func(h *httpHarness)) {
	t.Helper()

	dsn := filepath.Join(t.TempDir(), "impersonation.db")
	capt := &boundAddresses{}
	logger := slog.New(capt)
	logModule := core.Module{
		Name:     httpLoggingModule,
		Provides: []core.Provision{{Token: (*log.Logger)(nil), Exclusive: true}},
		New:      func(context.Context, *core.Registry) (any, error) { return httpLogging{logger: logger}, nil },
	}

	accounts := newAccounts()
	memberships := newMemberships()
	mfa := &fakeMFA{seasoned: true}
	auditor := &recordingAudit{}
	guard := fakeScopeGuard{covers: map[string]bool{}}
	decider := &fakeDecider{allow: true}

	modules := []core.Module{
		sqlite.Module(), logModule, speedhttp.Module(), kvmemory.Module(),
		eventmemory.Module(), settings.Module(), gate.Module(), authnmount.Module(),
		provide(httpAccountsModule, accounts, (*authn.Accounts)(nil)),
		provide(httpMembershipsModule, memberships, (*authn.Memberships)(nil)),
		provide(httpMFAModule, mfa, (*authn.MFA)(nil)),
		provide(httpAuditModule, auditor, (*audit.Audit)(nil)),
		provide(httpScopeModule, guard, (*authn.ScopeGuard)(nil)),
		provide(httpDeciderModule, decider, (*authn.Decider)(nil)),
		session.Module(), Module(),
	}

	document := map[string]any{
		"db": map[string]any{"sqlite": map[string]any{"dsn": dsn}},
		"http": map[string]any{
			"listeners": map[string]any{"public": map[string]any{"address": "127.0.0.1:0"}},
			"endpoints": map[string]any{"public": map[string]any{"listener": "public"}},
		},
		"kv":       map[string]any{"memory": map[string]any{"replicas": 1}},
		"authn":    map[string]any{"gate": map[string]any{"mount": map[string]any{"endpoint": "public", "base-path": "/auth", "public-url": "http://127.0.0.1", "allow-insecure": true}}},
		"settings": map[string]any{},
		"event":    map[string]any{},
	}
	body, err := json.Marshal(document)
	if err != nil {
		t.Fatalf("encoding the configuration: %v", err)
	}
	reader := authntest.ConfigReaderAll(t, string(body), modules...)

	reg := core.New()
	names := make([]string, 0, len(modules))
	for _, m := range modules {
		reg.Register(m)
		names = append(names, m.Name)
	}
	reg.Register(config.Module())
	reg.Supply(reader, config.ModuleName)

	err = reg.Drive(t.Context(), core.Drive{
		Depth:        core.StageServe,
		Participants: names,
		Action: func(ctx context.Context, reg *core.Registry) error {
			address := capt.addressOf("public")
			if address == "" {
				return fmt.Errorf("the entry point wrote no record of binding the public endpoint")
			}
			jar, jerr := cookiejar.New(nil)
			if jerr != nil {
				return jerr
			}
			sessions, rerr := core.Resolve[authn.Sessions](reg)
			if rerr != nil {
				return rerr
			}
			base := "http://" + address
			h := &httpHarness{
				t:           t,
				ctx:         ctx,
				base:        base,
				sessions:    sessions,
				accounts:    accounts,
				memberships: memberships,
				jar:         jar,
				client:      &http.Client{Jar: jar, Timeout: 5 * time.Second},
				bare:        &http.Client{Timeout: 5 * time.Second},
			}
			use(h)
			return nil
		},
	})
	if err != nil {
		t.Fatalf("the drive failed: %v", err)
	}
}

// createSession makes a browser session for account, authenticated with a
// password and a one-time code at the multi-factor level, and puts its cookie in
// the jar as a login response would. It returns the cookie's name, which the
// session backend keeps for the lifetime of the process.
func (h *httpHarness) createSession(account authn.AccountID) (name string) {
	h.t.Helper()
	issued, err := h.sessions.Create(h.ctx, authn.NewSession{
		Account:   account,
		Kind:      authn.SessionBrowser,
		Methods:   []authn.Method{authn.MethodPassword, authn.MethodTOTP},
		Assurance: authn.AssuranceMulti,
		AuthTime:  time.Now(),
	})
	if err != nil {
		h.t.Fatalf("creating a session: %v", err)
	}
	recorder := httptest.NewRecorder()
	h.sessions.SetCookie(recorder, issued)
	cookies := recorder.Result().Cookies()
	if len(cookies) != 1 {
		h.t.Fatalf("SetCookie wrote %d cookies, want 1", len(cookies))
	}
	base, err := url.Parse(h.base)
	if err != nil {
		h.t.Fatalf("parsing the bound address: %v", err)
	}
	h.jar.SetCookies(base, cookies)
	return cookies[0].Name
}

// jarToken is the value the jar holds of the session cookie named cookie.
func (h *httpHarness) jarToken(cookie string) string {
	h.t.Helper()
	base, err := url.Parse(h.base)
	if err != nil {
		h.t.Fatalf("parsing the bound address: %v", err)
	}
	for _, c := range h.jar.Cookies(base) {
		if c.Name == cookie {
			return c.Value
		}
	}
	h.t.Fatal("the jar holds no session cookie")
	return ""
}

// do issues one request, carrying an explicit session cookie when token is not
// empty. A non-GET request declares itself same-origin, as a page of the
// deployment would.
func (h *httpHarness) do(client *http.Client, method, path string, body any, cookie, token string) httpReply {
	h.t.Helper()
	var reader io.Reader
	contentType := ""
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			h.t.Fatalf("encoding a request body: %v", err)
		}
		reader = bytes.NewReader(encoded)
		contentType = "application/json"
	}
	req, err := http.NewRequestWithContext(h.ctx, method, h.base+path, reader)
	if err != nil {
		h.t.Fatalf("building %s %s: %v", method, path, err)
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	if method != http.MethodGet && method != http.MethodHead {
		req.Header.Set("Sec-Fetch-Site", "same-origin")
	}
	if token != "" {
		req.Header.Set("Cookie", cookie+"="+token)
	}
	resp, err := client.Do(req)
	if err != nil {
		h.t.Errorf("%s %s got no response: %v", method, path, err)
		return httpReply{}
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		h.t.Errorf("reading the body of %s %s: %v", method, path, err)
	}
	return httpReply{status: resp.StatusCode, body: string(raw)}
}

// state reads GET /auth/session over the jar and decodes the answer.
func (h *httpHarness) state() sessionView {
	h.t.Helper()
	r := h.do(h.client, http.MethodGet, "/auth/session", nil, "", "")
	if r.status != http.StatusOK {
		h.t.Fatalf("GET /auth/session over the jar answered %d: %s", r.status, r.body)
	}
	var v sessionView
	if err := json.Unmarshal([]byte(r.body), &v); err != nil {
		h.t.Fatalf("decoding %q: %v", r.body, err)
	}
	return v
}

// startImpersonation begins an impersonation over the jar and fails the case
// when the route does not answer 200.
func (h *httpHarness) startImpersonation() {
	h.t.Helper()
	r := h.do(h.client, http.MethodPost, "/auth/impersonation", map[string]any{
		"account":     string(httpTargetID),
		"tenant":      httpTenant,
		"reason":      "a support case",
		"ttl_seconds": 600,
	}, "", "")
	if r.status != http.StatusOK {
		h.t.Fatalf("POST /auth/impersonation answered %d: %s", r.status, r.body)
	}
}

// TestEndingAnImpersonationKeepsTheSessionToken holds the whole browser flow over
// the real routes and a real database: starting rotates the token, ending clears
// the marker without rotating, and the token in force when the end request
// settles stays valid as the initiator's own identity. A request still in flight
// with that token when the end settles is therefore not refused.
func TestEndingAnImpersonationKeepsTheSessionToken(t *testing.T) {
	assembleOverHTTP(t, func(h *httpHarness) {
		h.accounts.put(httpActorID, authn.Account{Kind: authn.KindUser, Status: authn.StatusActive})
		h.accounts.put(httpTargetID, authn.Account{Kind: authn.KindUser, Status: authn.StatusActive})
		h.memberships.set(httpTargetID, httpTenant, true)

		cookie := h.createSession(httpActorID)

		h.startImpersonation()
		if v := h.state(); !v.Authenticated || !v.Impersonating || v.AccountID != string(httpTargetID) || v.ActorID != string(httpActorID) {
			t.Fatalf("after the start the session reads %+v, want the target acted for by the actor", v)
		}

		preEnd := h.jarToken(cookie)

		// The end and a read still carrying the token both leave at the same
		// time. Neither order refuses the read: the end leaves the token in
		// place.
		var (
			ended httpReply
			read  httpReply
			wg    sync.WaitGroup
		)
		begin := make(chan struct{})
		wg.Add(2)
		go func() {
			defer wg.Done()
			<-begin
			ended = h.do(h.client, http.MethodDelete, "/auth/impersonation", nil, "", "")
		}()
		go func() {
			defer wg.Done()
			<-begin
			read = h.do(h.bare, http.MethodGet, "/auth/session", nil, cookie, preEnd)
		}()
		close(begin)
		wg.Wait()

		if ended.status != http.StatusNoContent {
			t.Fatalf("DELETE /auth/impersonation answered %d: %s", ended.status, ended.body)
		}
		if read.status != http.StatusOK {
			t.Errorf("a read carrying the token in force when the end settled answered %d, want 200: %s",
				read.status, read.body)
		} else {
			// Whichever side of the record's end the read lands on, it is
			// authenticated: before the end as the target acted for by the actor,
			// after it as the actor alone. The identity is pinned by the plain
			// read below, which always lands after the end.
			var v sessionView
			if err := json.Unmarshal([]byte(read.body), &v); err != nil {
				t.Fatalf("decoding %q: %v", read.body, err)
			}
			if !v.Authenticated || v.AccountID == "" {
				t.Errorf("the read during the end reads %+v, want an authenticated session", v)
			}
		}

		// The token is usable for a plain read after the end, and it resolves to
		// the initiator's own identity.
		after := h.do(h.bare, http.MethodGet, "/auth/session", nil, cookie, preEnd)
		if after.status != http.StatusOK {
			t.Fatalf("a read carrying the token in force before the end answered %d after it, want 200: %s",
				after.status, after.body)
		}
		var plain sessionView
		if err := json.Unmarshal([]byte(after.body), &plain); err != nil {
			t.Fatalf("decoding %q: %v", after.body, err)
		}
		if !plain.Authenticated || plain.Impersonating || plain.AccountID != string(httpActorID) || plain.ActorID != "" {
			t.Errorf("the plain read after the end reads %+v, want the actor alone", plain)
		}

		// The jar never received a replacement cookie, and it does not need one.
		if v := h.state(); !v.Authenticated || v.Impersonating || v.AccountID != string(httpActorID) {
			t.Errorf("the jar's session after the end reads %+v, want the actor and not impersonating", v)
		}
	})
}

// TestAnEndOnASlowNetworkKeepsInFlightReadsAuthenticated holds the shape a slow
// network produces: reads already in flight carry the token from before the end,
// and the browser is still presenting it while the end request is on its way.
// Ending leaves the token in place, so those reads stay authenticated, and the
// jar authenticates as the initiator afterwards as well. The session on the
// server is never in question: a refusal of a request already sent is not the
// end of the session.
func TestAnEndOnASlowNetworkKeepsInFlightReadsAuthenticated(t *testing.T) {
	assembleOverHTTP(t, func(h *httpHarness) {
		h.accounts.put(httpActorID, authn.Account{Kind: authn.KindUser, Status: authn.StatusActive})
		h.accounts.put(httpTargetID, authn.Account{Kind: authn.KindUser, Status: authn.StatusActive})
		h.memberships.set(httpTargetID, httpTenant, true)

		cookie := h.createSession(httpActorID)
		h.startImpersonation()
		preEnd := h.jarToken(cookie)

		// The end leaves over the jar, as a browser sends it, and three reads
		// already carry the token the jar holds at this instant. They all stay
		// authenticated: the end clears the marker and leaves the token alone.
		var (
			ended  httpReply
			inRead [3]httpReply
			wg     sync.WaitGroup
		)
		begin := make(chan struct{})
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-begin
			ended = h.do(h.client, http.MethodDelete, "/auth/impersonation", nil, "", "")
		}()
		for i := range inRead {
			wg.Add(1)
			go func() {
				defer wg.Done()
				<-begin
				inRead[i] = h.do(h.bare, http.MethodGet, "/auth/session", nil, cookie, preEnd)
			}()
		}
		close(begin)
		wg.Wait()

		if ended.status != http.StatusNoContent {
			t.Fatalf("DELETE /auth/impersonation answered %d: %s", ended.status, ended.body)
		}
		for i, r := range inRead {
			if r.status != http.StatusOK {
				t.Errorf("in-flight read %d answered %d, want 200: %s", i, r.status, r.body)
			}
		}

		// The session on the server is intact: the jar's cookie authenticates as
		// the initiator after the end, whatever the end answered. A client that
		// ends up anonymous did so on its own reading of the requests it had
		// already sent, not because the session ended.
		if v := h.state(); !v.Authenticated || v.Impersonating || v.AccountID != string(httpActorID) {
			t.Errorf("the jar's session after the end reads %+v, want the actor and not impersonating", v)
		}
	})
}
