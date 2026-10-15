package selfservice_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/authn/authntest"
	"github.com/vislake/speed/pkg/authn/internal/mount"
	"github.com/vislake/speed/pkg/authn/internal/mount/mounttest"
	"github.com/vislake/speed/pkg/authn/login/internal/selfservice"
	"github.com/vislake/speed/pkg/log"
)

// The instant every test's clock reads, and the account and session its
// identity carries.
var (
	now = time.Date(2026, time.October, 11, 12, 0, 0, 0, time.UTC)
)

const (
	account  authn.AccountID = "acct-1"
	session  authn.SessionID = "sess-1"
	basePath                 = "/auth"
)

// harness is a Service on a mounttest Mounter with a fake for every dependency.
type harness struct {
	t        *testing.T
	log      *callLog
	m        *mounttest.Mounter
	svc      *selfservice.Service
	accounts *fakeAccounts
	admin    *fakeAdmin
	pw       *fakePasswords
	sessions *fakeSessions
	mfa      *fakeMFA
	throttle *fakeThrottle
	sms      *fakeSMS
	kv       *fakeKV
	bus      *fakeBus
	audit    *fakeAudit
	settings *fakeSettings
	engaged  []authn.ThrottleScope
	logs     *logCapture
}

type config struct {
	noSMS         bool
	impersonable  bool // every route is registered as impersonable, as a wrong registration would
	mutateDeps    func(*selfservice.Deps)
	withoutOptins bool // no settings, bus or audit
}

func newHarness(t *testing.T, cfgs ...func(*config)) *harness {
	t.Helper()
	var cfg config
	for _, f := range cfgs {
		f(&cfg)
	}
	log := &callLog{}
	h := &harness{
		t:   t,
		log: log,
		m:   mounttest.New(t),
		accounts: &fakeAccounts{log: log, account: authn.Account{
			ID: account, Kind: authn.KindUser, DisplayName: "Ada", PasswordSet: true,
			CreatedAt: now.Add(-48 * time.Hour),
		}},
		admin:    &fakeAdmin{log: log},
		pw:       &fakePasswords{log: log},
		sessions: &fakeSessions{log: log, issued: authn.Issued{ID: session, Token: "rotated-token", ExpiresAt: now.Add(time.Hour)}},
		mfa:      &fakeMFA{log: log},
		throttle: &fakeThrottle{log: log},
		sms:      &fakeSMS{log: log},
		kv:       newFakeKV(log),
		bus:      &fakeBus{},
		audit:    &fakeAudit{},
		settings: &fakeSettings{values: map[string]time.Duration{}, errs: map[string]error{}},
		logs:     &logCapture{},
	}
	deps := selfservice.Deps{
		Accounts:        h.accounts,
		Admin:           h.admin,
		Passwords:       h.pw,
		Sessions:        h.sessions,
		MFA:             h.mfa,
		Clock:           func() time.Time { return now },
		ThrottleEngaged: func(_ context.Context, scope authn.ThrottleScope) { h.engaged = append(h.engaged, scope) },
	}
	if !cfg.noSMS {
		deps.SMSCodes = h.sms
		deps.KV = h.kv
		deps.Throttle = h.throttle
	}
	if !cfg.withoutOptins {
		deps.Settings = h.settings
		deps.Bus = h.bus
		deps.Audit = h.audit
	}
	if cfg.mutateDeps != nil {
		cfg.mutateDeps(&deps)
	}
	svc, err := selfservice.New(deps)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	h.svc = svc
	var mounter mount.Mounter = h.m
	if cfg.impersonable {
		mounter = impersonableMounter{h.m}
	}
	svc.Mount(mounter)
	h.as()
	return h
}

// impersonableMounter registers every guarded route as one an impersonating
// identity may pass, which is the registration mistake the handlers' own check
// is the fallback for.
type impersonableMounter struct{ *mounttest.Mounter }

func (m impersonableMounter) Guarded(method, rel string, h gin.HandlerFunc, opts ...mount.Option) {
	m.Mounter.Guarded(method, rel, h, append(opts, mount.Impersonable())...)
}

// as sets the identity of the requests that follow: the account and session of
// the harness, a first factor proven a minute ago, and what opts change.
func (h *harness) as(opts ...authntest.Option) {
	h.t.Helper()
	base := []authntest.Option{
		authntest.WithAccount(account),
		authntest.WithSession(session),
		authntest.WithAuthTime(now.Add(-time.Minute)),
	}
	h.m.As(append(base, opts...)...)
}

// activeFactor gives the account a registered, active TOTP factor.
func (h *harness) activeFactor() {
	h.mfa.factors = []authn.FactorRecord{{ID: "factor-1", Account: account, Kind: authn.FactorTOTP, Status: authn.FactorActive}}
}

// multi is an identity that proved two factors here a minute ago.
func (h *harness) multi() {
	h.as(authntest.WithAssurance(authn.AssuranceMulti), authntest.WithMethods(authn.MethodPassword, authn.MethodTOTP))
}

// request is one request to a route, written relative to the base path.
type request struct {
	method string
	path   string
	body   string
	header map[string]string
}

func (h *harness) do(r request) *httptest.ResponseRecorder {
	h.t.Helper()
	var body io.Reader
	if r.body != "" {
		body = strings.NewReader(r.body)
	}
	req := httptest.NewRequest(r.method, basePath+r.path, body)
	if r.body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	for k, v := range r.header {
		req.Header.Set(k, v)
	}
	req = req.WithContext(log.WithLogger(req.Context(), slog.New(h.logs.handler())))
	return h.m.Do(req)
}

func (h *harness) put(path, body string) *httptest.ResponseRecorder {
	return h.do(request{method: http.MethodPut, path: path, body: body})
}

func (h *harness) post(path, body string) *httptest.ResponseRecorder {
	return h.do(request{method: http.MethodPost, path: path, body: body})
}

func (h *harness) delete(path string) *httptest.ResponseRecorder {
	return h.do(request{method: http.MethodDelete, path: path})
}

// problemType is the code a problem answer carries, without the type prefix, or
// the empty string when the body is not a problem or has none (about:blank).
func problemType(t *testing.T, w *httptest.ResponseRecorder) string {
	t.Helper()
	var p struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &p); err != nil {
		return ""
	}
	if p.Type == "about:blank" {
		return ""
	}
	return strings.TrimPrefix(p.Type, "urn:speed:authn:")
}

// logCapture collects what the handlers log, so a test can read it for what must
// not be there.
type logCapture struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (c *logCapture) handler() slog.Handler {
	return slog.NewJSONHandler(&lockedWriter{c}, &slog.HandlerOptions{Level: slog.LevelDebug})
}

func (c *logCapture) String() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.buf.String()
}

type lockedWriter struct{ c *logCapture }

func (w *lockedWriter) Write(p []byte) (int, error) {
	w.c.mu.Lock()
	defer w.c.mu.Unlock()
	return w.c.buf.Write(p)
}

// mutating is the five routes that change how the account is proven, with a
// request body each of them accepts.
var mutating = []struct {
	name string
	req  request
}{
	{"PUT /password", request{method: http.MethodPut, path: "/password", body: `{"new_password":"correct horse"}`}},
	{"POST /phone", request{method: http.MethodPost, path: "/phone", body: `{"phone":"+8613800000000"}`}},
	{"POST /phone/verify", request{method: http.MethodPost, path: "/phone/verify", body: `{"code":"123456"}`}},
	{"DELETE /identifiers/:id", request{method: http.MethodDelete, path: "/identifiers/ident-1"}},
	{"DELETE /links/:id", request{method: http.MethodDelete, path: "/links/link-1"}},
}

// secretSubject is a binding's provider-side subject, which no answer may carry.
const secretSubject = "subject-9f3a-never-shown"

// richAccount gives the account two identifiers and two external bindings, one
// of each carrying values a test reads back.
func (h *harness) richAccount() {
	h.accounts.account.MFARequired = true
	h.accounts.identifiers = []authn.Identifier{
		{ID: "ident-1", Kind: authn.IdentifierUsername, Value: "ada", VerifiedAt: now.Add(-time.Hour), VerifiedVia: authn.VerifiedViaAdmin},
		{ID: "ident-2", Kind: authn.IdentifierPhone, Value: "+8613800000000", VerifiedAt: now.Add(-time.Minute), VerifiedVia: authn.VerifiedViaSMSCode},
	}
	h.accounts.externals = []authn.ExternalBinding{
		{ID: "link-1", Provider: "oidc:corp", Subject: secretSubject, Email: "ada@corp.example", EmailVerified: true, CreatedAt: now.Add(-24 * time.Hour), LastLoginAt: now.Add(-time.Hour)},
		{ID: "link-2", Provider: "github", Subject: "12345678", CreatedAt: now.Add(-48 * time.Hour)},
	}
}
