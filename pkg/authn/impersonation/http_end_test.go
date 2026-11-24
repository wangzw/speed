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
	"slices"
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

const (
	httpActorID  = authn.AccountID("acct-http-admin")
	httpTargetID = authn.AccountID("acct-http-user")
	httpTenant   = "tenant-http"
)

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

type httpLogging struct{ logger *slog.Logger }

func (l httpLogging) Named(string) *slog.Logger { return l.logger }
func (httpLogging) Redaction() log.Redaction    { return noRedaction{} }

type noRedaction struct{}

func (noRedaction) AddKeys(...string)              {}
func (noRedaction) AddPattern(string, log.Matcher) {}

func provide(name string, instance any, token core.Token) core.Module {
	return core.Module{
		Name:     name,
		Provides: []core.Provision{{Token: token, Exclusive: true}},
		New:      func(context.Context, *core.Registry) (any, error) { return instance, nil },
	}
}

const httpDirectoryModule = "http-tenant-directory"

type httpDirectory struct{}

func (httpDirectory) TenantIDOf(_ context.Context, name string) (string, bool, error) {
	return name, true, nil
}

type httpReply struct {
	status     int
	body       string
	setCookies []string
	header     http.Header
}

type sessionView struct {
	Authenticated bool   `json:"authenticated"`
	AccountID     string `json:"account_id"`
	ActorID       string `json:"actor_id"`
	Impersonating bool   `json:"impersonating"`
}

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
		provide("http-signup-state", &fakeSignupVerification{}, (*authn.SignupVerification)(nil)),
		provide(httpMembershipsModule, memberships, (*authn.Memberships)(nil)),
		provide(httpMFAModule, mfa, (*authn.MFA)(nil)),
		provide(httpAuditModule, auditor, (*audit.Audit)(nil)),
		provide(httpScopeModule, guard, (*authn.ScopeGuard)(nil)),
		provide(httpDeciderModule, decider, (*authn.Decider)(nil)),
		provide(httpDirectoryModule, httpDirectory{}, (*authn.TenantDirectory)(nil)),
		session.Module(), Module(),
	}

	document := map[string]any{
		"db": map[string]any{"sqlite": map[string]any{"dsn": dsn}},
		"http": map[string]any{
			"listeners": map[string]any{"public": map[string]any{"address": "127.0.0.1:0"}},
			"endpoints": map[string]any{"public": map[string]any{"listener": "public"}},
		},
		"kv":       map[string]any{"memory": map[string]any{"replicas": 1}},
		"authn":    map[string]any{"gate": map[string]any{"mount": map[string]any{"endpoints": map[string]any{"default": "public"}, "public-url": "http://127.0.0.1"}}},
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
	return httpReply{
		status:     resp.StatusCode,
		body:       string(raw),
		setCookies: resp.Header.Values("Set-Cookie"),
		header:     resp.Header.Clone(),
	}
}

func (h *httpHarness) state() sessionView {
	h.t.Helper()
	r := h.do(h.client, http.MethodGet, "/api/v1/auth/session", nil, "", "")
	if r.status != http.StatusOK {
		h.t.Fatalf("GET /api/v1/auth/session over the jar answered %d: %s", r.status, r.body)
	}
	var v sessionView
	if err := json.Unmarshal([]byte(r.body), &v); err != nil {
		h.t.Fatalf("decoding %q: %v", r.body, err)
	}
	return v
}

func (h *httpHarness) startImpersonation() {
	h.t.Helper()
	r := h.do(h.client, http.MethodPost, "/api/v1/auth/impersonation", map[string]any{
		"account":     string(httpTargetID),
		"tenant":      httpTenant,
		"reason":      "a support case",
		"ttl_seconds": 600,
	}, "", "")
	if r.status != http.StatusOK {
		h.t.Fatalf("POST /api/v1/auth/impersonation answered %d: %s", r.status, r.body)
	}
}

// TestEndingAnImpersonationKeepsTheSessionToken checks that a start rotates the session token and an end clears the marker without rotating it.
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

		// The end leaves the token in place, so a read racing it succeeds whichever
		// order it lands in.
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
			ended = h.do(h.client, http.MethodDelete, "/api/v1/auth/impersonation", nil, "", "")
		}()
		go func() {
			defer wg.Done()
			<-begin
			read = h.do(h.bare, http.MethodGet, "/api/v1/auth/session", nil, cookie, preEnd)
		}()
		close(begin)
		wg.Wait()

		if ended.status != http.StatusNoContent {
			t.Fatalf("DELETE /api/v1/auth/impersonation answered %d: %s", ended.status, ended.body)
		}
		if read.status != http.StatusOK {
			t.Errorf("a read carrying the token in force when the end settled answered %d, want 200: %s",
				read.status, read.body)
		} else {
			var v sessionView
			if err := json.Unmarshal([]byte(read.body), &v); err != nil {
				t.Fatalf("decoding %q: %v", read.body, err)
			}
			if !v.Authenticated || v.AccountID == "" {
				t.Errorf("the read during the end reads %+v, want an authenticated session", v)
			}
		}

		after := h.do(h.bare, http.MethodGet, "/api/v1/auth/session", nil, cookie, preEnd)
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

		if v := h.state(); !v.Authenticated || v.Impersonating || v.AccountID != string(httpActorID) {
			t.Errorf("the jar's session after the end reads %+v, want the actor and not impersonating", v)
		}
	})
}

// TestTheStartRotatesTheCookieAndTheEndLeavesIt checks that a start writes exactly one new session cookie and an end writes none.
func TestTheStartRotatesTheCookieAndTheEndLeavesIt(t *testing.T) {
	assembleOverHTTP(t, func(h *httpHarness) {
		h.accounts.put(httpActorID, authn.Account{Kind: authn.KindUser, Status: authn.StatusActive})
		h.accounts.put(httpTargetID, authn.Account{Kind: authn.KindUser, Status: authn.StatusActive})
		h.memberships.set(httpTargetID, httpTenant, true)

		h.createSession(httpActorID)
		started := h.do(h.client, http.MethodPost, "/api/v1/auth/impersonation", map[string]any{
			"account":     string(httpTargetID),
			"tenant":      httpTenant,
			"reason":      "a support case",
			"ttl_seconds": 600,
		}, "", "")
		if started.status != http.StatusOK {
			t.Fatalf("POST /api/v1/auth/impersonation answered %d: %s", started.status, started.body)
		}
		if len(started.setCookies) != 1 {
			t.Errorf("the start write wrote %d Set-Cookie header(s), want exactly 1: %v",
				len(started.setCookies), started.setCookies)
		}
		cacheControl := started.header.Values("Cache-Control")
		if !slices.Contains(cacheControl, "no-store") || !slices.Contains(cacheControl, cacheControlSetCookie) {
			t.Errorf("the start write's Cache-Control reads %v, want the mounter's no-store and %q beside the cookie it issues",
				cacheControl, cacheControlSetCookie)
		}

		ended := h.do(h.client, http.MethodDelete, "/api/v1/auth/impersonation", nil, "", "")
		if ended.status != http.StatusNoContent {
			t.Fatalf("DELETE /api/v1/auth/impersonation answered %d: %s", ended.status, ended.body)
		}
		if len(ended.setCookies) != 0 {
			t.Errorf("the end write wrote %d Set-Cookie header(s), want none: %v",
				len(ended.setCookies), ended.setCookies)
		}

		again := h.do(h.client, http.MethodDelete, "/api/v1/auth/impersonation", nil, "", "")
		if again.status != http.StatusNotFound {
			t.Fatalf("a second DELETE /api/v1/auth/impersonation answered %d, want 404: %s", again.status, again.body)
		}
		if len(again.setCookies) != 0 {
			t.Errorf("the second end wrote %d Set-Cookie header(s), want none: %v",
				len(again.setCookies), again.setCookies)
		}
	})
}
