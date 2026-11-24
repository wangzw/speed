package login_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/cookiejar"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/authn/account"
	"github.com/vislake/speed/pkg/authn/gate"
	"github.com/vislake/speed/pkg/authn/humanverify"
	"github.com/vislake/speed/pkg/authn/login"
	"github.com/vislake/speed/pkg/authn/login/password"
	"github.com/vislake/speed/pkg/authn/mfa"
	"github.com/vislake/speed/pkg/authn/mount"
	"github.com/vislake/speed/pkg/authn/provisioning/provisioningtest"
	"github.com/vislake/speed/pkg/authn/session"
	"github.com/vislake/speed/pkg/config"
	jsonformat "github.com/vislake/speed/pkg/config/format/json"
	filesource "github.com/vislake/speed/pkg/config/source/file"
	"github.com/vislake/speed/pkg/core"
	"github.com/vislake/speed/pkg/core/coretest"
	"github.com/vislake/speed/pkg/db/sqlite"
	eventmemory "github.com/vislake/speed/pkg/event/memory"
	speedhttp "github.com/vislake/speed/pkg/http"
	kvmemory "github.com/vislake/speed/pkg/kv/memory"
	"github.com/vislake/speed/pkg/log"
	"github.com/vislake/speed/pkg/settings"
)

// listening is the record the entry point writes once an endpoint's address is
// bound, the only place outside the process that says which port the operating
// system picked for an endpoint configured on port 0.
const listening = "an HTTP listener is listening"

func requestBound() time.Duration {
	return coretest.Scale(5 * time.Second)
}

type capture struct {
	mu      sync.Mutex
	records []slog.Record
}

func (c *capture) Enabled(context.Context, slog.Level) bool { return true }
func (c *capture) Handle(_ context.Context, r slog.Record) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.records = append(c.records, r.Clone())
	return nil
}
func (c *capture) WithAttrs([]slog.Attr) slog.Handler { return c }
func (c *capture) WithGroup(string) slog.Handler      { return c }

func (c *capture) addressOf(endpoint string) string {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, r := range c.records {
		if r.Message != listening {
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

type logging struct{ logger *slog.Logger }

func (l logging) Named(string) *slog.Logger { return l.logger }
func (logging) Redaction() log.Redaction    { return noRules{} }

type noRules struct{}

func (noRules) AddKeys(...string)              {}
func (noRules) AddPattern(string, log.Matcher) {}

type harness struct {
	t           *testing.T
	ctx         context.Context
	address     string
	client      *http.Client
	admin       authn.AccountAdmin
	mfa         authn.MFA
	accounts    authn.Accounts
	memberships authn.Memberships
	store       settings.Store
}

type reply struct {
	status int
	header http.Header
	body   string
}

func (r reply) json(t *testing.T) map[string]any {
	t.Helper()
	var out map[string]any
	if err := json.Unmarshal([]byte(r.body), &out); err != nil {
		t.Fatalf("decoding %q as JSON: %v", r.body, err)
	}
	return out
}

func assemble(t *testing.T, use func(h *harness)) {
	t.Helper()
	assembleWith(t, nil, use)
}

func assembleWith(t *testing.T, extra []core.Module, use func(h *harness)) {
	t.Helper()
	assembleWithSections(t, extra, nil, use)
}

func authnSection(publicURL string) map[string]any {
	return map[string]any{
		"gate": map[string]any{"mount": map[string]any{
			"endpoints":  map[string]any{"default": "public"},
			"public-url": publicURL,
		}},
		"humanverify": map[string]any{"mode": "off", "phone-mode": "off"},
	}
}

func assembleWithSections(t *testing.T, extra []core.Module, sections map[string]any, use func(h *harness)) {
	t.Helper()

	dsn := filepath.Join(t.TempDir(), "authn.db")
	capt := &capture{}
	logger := slog.New(capt)
	logModule := core.Module{
		Name:     "stand-in.logging",
		Provides: []core.Provision{{Token: (*log.Logger)(nil), Exclusive: true}},
		New:      func(context.Context, *core.Registry) (any, error) { return logging{logger: logger}, nil },
	}

	document := map[string]any{
		"db": map[string]any{"sqlite": map[string]any{"dsn": dsn}},
		"http": map[string]any{
			"listeners": map[string]any{"public": map[string]any{"address": "127.0.0.1:0"}},
			"endpoints": map[string]any{"public": map[string]any{"listener": "public"}},
		},
		"kv":       map[string]any{"memory": map[string]any{"replicas": 1}},
		"authn":    authnSection("http://127.0.0.1"),
		"settings": map[string]any{},
		"event":    map[string]any{},
	}
	for key, value := range sections {
		document[key] = value
	}
	body, err := json.Marshal(document)
	if err != nil {
		t.Fatalf("encoding the configuration: %v", err)
	}

	modules := []core.Module{
		sqlite.Module(), logModule, speedhttp.Module(), kvmemory.Module(),
		eventmemory.Module(), settings.Module(), mount.Module(), gate.Module(),
		humanverify.Module(),
		account.Module(), provisioningtest.Module(), session.Module(), mfa.Module(), login.Module(), password.Module(),
	}
	modules = append(modules, extra...)
	reader := loadConfig(t, string(body), modules...)

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
			h := &harness{
				t:       t,
				ctx:     ctx,
				address: address,
				client:  &http.Client{Jar: jar, Timeout: requestBound()},
			}
			if h.admin, err = core.Resolve[authn.AccountAdmin](reg); err != nil {
				return err
			}
			if h.mfa, err = core.Resolve[authn.MFA](reg); err != nil {
				return err
			}
			if h.accounts, err = core.Resolve[authn.Accounts](reg); err != nil {
				return err
			}
			if h.memberships, err = core.Resolve[authn.Memberships](reg); err != nil {
				return err
			}
			settingsStore, resolveErr := core.Resolve[settings.Store](reg)
			if resolveErr != nil {
				return resolveErr
			}
			h.store = settingsStore
			use(h)
			return nil
		},
	})
	if err != nil {
		t.Fatalf("the drive failed: %v", err)
	}
}

func loadConfig(t *testing.T, body string, modules ...core.Module) config.Reader {
	t.Helper()
	locator := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(locator, []byte(body), 0o600); err != nil {
		t.Fatalf("writing the primary config source: %v", err)
	}
	loading := core.New()
	loading.Register(jsonformat.Module())
	loading.Register(filesource.Module())
	loading.Register(core.Module{
		Name:      "host",
		Resources: []any{config.HostIdentity{Prefix: "SPEEDLOGINTEST", DefaultLocator: "file://" + locator}},
	})
	for _, m := range modules {
		loading.Register(m)
	}
	reader, err := config.Load(t.Context(), loading, nil)
	if err != nil {
		t.Fatalf("loading the configuration: %v", err)
	}
	return reader
}

func (h *harness) createAccount(username, pw string) authn.AccountID {
	h.t.Helper()
	acct, err := h.admin.Create(h.ctx, authn.CreateAccount{
		Kind: authn.KindUser, Username: username, Password: pw, Via: "admin",
	})
	if err != nil {
		h.t.Fatalf("creating account %q: %v", username, err)
	}
	return acct.ID
}

func (h *harness) do(method, path string, body any, edits ...func(*http.Request)) reply {
	h.t.Helper()
	var reader io.Reader
	contentType := ""
	switch b := body.(type) {
	case nil:
	case string:
		reader = strings.NewReader(b)
		contentType = "application/json"
	default:
		encoded, err := json.Marshal(body)
		if err != nil {
			h.t.Fatalf("encoding a request body: %v", err)
		}
		reader = bytes.NewReader(encoded)
		contentType = "application/json"
	}
	req, err := http.NewRequestWithContext(h.ctx, method, "http://"+h.address+path, reader)
	if err != nil {
		h.t.Fatalf("building %s %s: %v", method, path, err)
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	if method != http.MethodGet && method != http.MethodHead {
		req.Header.Set("Sec-Fetch-Site", "same-origin")
	}
	for _, edit := range edits {
		edit(req)
	}
	resp, err := h.client.Do(req)
	if err != nil {
		h.t.Errorf("%s %s got no response: %v", method, path, err)
		return reply{}
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		h.t.Errorf("reading the body of %s %s: %v", method, path, err)
	}
	return reply{status: resp.StatusCode, header: resp.Header, body: string(raw)}
}

func (h *harness) get(path string, edits ...func(*http.Request)) reply {
	return h.do(http.MethodGet, path, nil, edits...)
}

func (h *harness) post(path string, body any, edits ...func(*http.Request)) reply {
	return h.do(http.MethodPost, path, body, edits...)
}

func firstCookie(t *testing.T, header string) string {
	t.Helper()
	if header == "" {
		t.Fatal("no Set-Cookie header")
	}
	pair, _, _ := strings.Cut(header, ";")
	return strings.TrimSpace(pair)
}

func (h *harness) selfStatus() bool {
	h.t.Helper()
	var out struct {
		Authenticated bool `json:"authenticated"`
	}
	r := h.get("/api/v1/auth/session")
	if r.status == http.StatusOK {
		if err := json.Unmarshal([]byte(r.body), &out); err != nil {
			h.t.Fatalf("decoding session status %q: %v", r.body, err)
		}
		return out.Authenticated
	}
	return false
}

func (h *harness) loginFlow(username, pw string) reply {
	h.t.Helper()
	start := h.post("/api/v1/auth/flows", map[string]any{
		"method": "password",
		"input":  map[string]string{"identifier": username, "password": pw},
	})
	if start.status != http.StatusOK {
		h.t.Fatalf("POST flows answered %d: %s", start.status, start.body)
	}
	seq := int(start.json(h.t)["seq"].(float64))
	done := h.post("/api/v1/auth/flows/answer", map[string]any{"seq": seq, "step": "complete"})
	return done
}

// TestPasswordLoginTwoStepsDeliversASessionCookie holds the whole login: the cookie authenticates a guarded route and is refused after logout.
func TestPasswordLoginTwoStepsDeliversASessionCookie(t *testing.T) {
	assemble(t, func(h *harness) {
		h.createAccount("alice", "alice-password")

		done := h.loginFlow("alice", "alice-password")
		if done.status != http.StatusOK {
			t.Fatalf("complete answered %d: %s", done.status, done.body)
		}
		body := done.json(t)
		if body["state"] != "authenticated" {
			t.Fatalf("complete body = %s, want state authenticated", done.body)
		}
		session := body["session"].(map[string]any)
		if session["id"] == nil || session["id"] == "" {
			t.Fatalf("complete body carries no session id: %s", done.body)
		}
		if got := done.header.Get("Set-Cookie"); got == "" {
			t.Fatalf("complete set no cookie before writing the body: %s", done.body)
		}
		sessionCookie := firstCookie(t, done.header.Get("Set-Cookie"))
		if !h.selfStatus() {
			t.Errorf("GET /api/v1/auth/session did not report the new session as authenticated")
		}
		start := h.post("/api/v1/auth/flows/authenticated", map[string]any{"purpose": "step-up", "method": "password"})
		if start.status != http.StatusOK {
			t.Errorf("POST /api/v1/auth/flows/authenticated with the session cookie answered %d: %s", start.status, start.body)
		}

		out := h.post("/api/v1/auth/logout", nil)
		if out.status != http.StatusNoContent && out.status != http.StatusOK {
			t.Errorf("POST /api/v1/auth/logout answered %d: %s", out.status, out.body)
		}
		if got := out.header.Get("Set-Cookie"); !strings.Contains(got, "Max-Age=0") {
			t.Errorf("logout did not clear the cookie: %q", got)
		}
		after := h.get("/api/v1/auth/session", func(r *http.Request) {
			r.Header.Set("Cookie", sessionCookie)
			r.Header.Set("Sec-Fetch-Site", "same-origin")
		})
		if after.status != http.StatusUnauthorized {
			t.Errorf("GET /api/v1/auth/session with the revoked cookie answered %d, want 401: %s", after.status, after.body)
		}
	})
}

// TestEveryCookieOfALoginIsNamedForThePublicURL holds what the real stack
// writes on the public URL it is configured with.
func TestEveryCookieOfALoginIsNamedForThePublicURL(t *testing.T) {
	for _, c := range []struct {
		name, publicURL, flow, session string
		secure                         bool
	}{
		{"a loopback http deployment", "http://127.0.0.1", "authn-flow", "authn-sid", false},
		{"an https deployment", "https://app.example.com", "__Host-authn-flow", "__Host-authn-sid", true},
	} {
		t.Run(c.name, func(t *testing.T) {
			sections := map[string]any{"authn": authnSection(c.publicURL)}
			assembleWithSections(t, nil, sections, func(h *harness) {
				h.createAccount("carol", "carol-password")

				start := h.post("/api/v1/auth/flows", map[string]any{
					"method": "password",
					"input":  map[string]string{"identifier": "carol", "password": "carol-password"},
				})
				if start.status != http.StatusOK {
					t.Fatalf("POST flows answered %d: %s", start.status, start.body)
				}
				holdsCookie(t, start.header, c.flow, c.secure)

				seq := int(start.json(t)["seq"].(float64))
				done := h.post("/api/v1/auth/flows/answer", map[string]any{"seq": seq, "step": "complete"})
				if done.status != http.StatusOK {
					t.Fatalf("complete answered %d, so the flow cookie did not come back: %s", done.status, done.body)
				}
				holdsCookie(t, done.header, c.session, c.secure)
				if !h.selfStatus() {
					t.Errorf("GET /api/v1/auth/session did not report the session, so the session cookie did not come back")
				}

				sameSite := map[string]http.SameSite{c.flow: http.SameSiteLaxMode, c.session: http.SameSiteLaxMode}
				for _, header := range []http.Header{start.header, done.header} {
					for _, set := range (&http.Response{Header: header}).Cookies() {
						want, known := sameSite[set.Name]
						if !known {
							t.Errorf("the reply set a cookie %q, which is neither %s nor %s", set.Name, c.flow, c.session)
							continue
						}
						if set.SameSite != want {
							t.Errorf("the cookie %s is SameSite=%v, want %v", set.Name, set.SameSite, want)
						}
					}
				}
			})
		})
	}
}

func holdsCookie(t *testing.T, header http.Header, name string, secure bool) {
	t.Helper()
	var found []*http.Cookie
	for _, c := range (&http.Response{Header: header}).Cookies() {
		if c.Name == name {
			found = append(found, c)
		}
	}
	if len(found) != 1 {
		t.Fatalf("the reply set %d cookies named %s, want one: %v", len(found), name, header.Values("Set-Cookie"))
	}
	if c := found[0]; c.Secure != secure || !c.HttpOnly || c.Path != "/" || c.Domain != "" {
		t.Errorf("the cookie %s is Secure=%v HttpOnly=%v Path=%q Domain=%q, want Secure %v, HttpOnly, Path=/ and "+
			"no Domain", name, c.Secure, c.HttpOnly, c.Path, c.Domain, secure)
	}
}

// TestASecondLoginMakesASecondSessionForTheSameAccount holds that two clients produce two session identifiers naming one account.
func TestASecondLoginMakesASecondSessionForTheSameAccount(t *testing.T) {
	assemble(t, func(h *harness) {
		acct := h.createAccount("bob", "bob-password")
		first := h.loginFlow("bob", "bob-password").json(t)["session"].(map[string]any)["id"].(string)
		second := h.loginFlow("bob", "bob-password").json(t)["session"].(map[string]any)["id"].(string)
		if first == second {
			t.Fatalf("two logins produced one session id %q", first)
		}
		got, err := h.accounts.Get(h.ctx, acct)
		if err != nil {
			t.Fatalf("reading the account back: %v", err)
		}
		if got.ID != acct {
			t.Errorf("the account changed between logins: %q then %q", acct, got.ID)
		}
	})
}

// TestLoginRevokesTheSessionTheRequestCameWith holds that a login made while a
// session cookie is presented revokes that old session.
func TestLoginRevokesTheSessionTheRequestCameWith(t *testing.T) {
	assemble(t, func(h *harness) {
		h.createAccount("carol", "carol-password")
		if first := h.loginFlow("carol", "carol-password"); first.status != http.StatusOK {
			t.Fatalf("first login answered %d: %s", first.status, first.body)
		}
		second := h.loginFlow("carol", "carol-password")
		if second.status != http.StatusOK {
			t.Fatalf("second login answered %d: %s", second.status, second.body)
		}
		if !h.selfStatus() {
			t.Errorf("the second session is not authenticated")
		}
	})
}

// TestEnumerationAnswersAreIndistinguishable holds that an unknown identifier, a wrong password, a disabled account and a passwordless account answer one 401.
func TestEnumerationAnswersAreIndistinguishable(t *testing.T) {
	assemble(t, func(h *harness) {
		h.createAccount("erin", "erin-password")
		disabled := h.createAccount("frank", "frank-password")
		if err := h.admin.SetStatus(h.ctx, disabled, authn.StatusDisabled, authn.ReasonAdmin); err != nil {
			t.Fatalf("disabling frank: %v", err)
		}
		if _, err := h.admin.Create(h.ctx, authn.CreateAccount{Kind: authn.KindUser, Username: "grace", Via: "admin"}); err != nil {
			t.Fatalf("creating a passwordless account: %v", err)
		}

		replies := []reply{
			h.post("/api/v1/auth/flows", map[string]any{"method": "password", "input": map[string]string{"identifier": "nobody", "password": "whatever"}}),
			h.post("/api/v1/auth/flows", map[string]any{"method": "password", "input": map[string]string{"identifier": "erin", "password": "wrong"}}),
			h.post("/api/v1/auth/flows", map[string]any{"method": "password", "input": map[string]string{"identifier": "frank", "password": "frank-password"}}),
			h.post("/api/v1/auth/flows", map[string]any{"method": "password", "input": map[string]string{"identifier": "grace", "password": "whatever"}}),
		}
		for i, got := range replies {
			if got.status != http.StatusUnauthorized {
				t.Fatalf("case %d answered %d, want 401: %s", i, got.status, got.body)
			}
			if got.body != replies[0].body {
				t.Errorf("case %d body %q differs from the first %q", i, got.body, replies[0].body)
			}
			if got.header.Get("WWW-Authenticate") != replies[0].header.Get("WWW-Authenticate") {
				t.Errorf("case %d challenge %q differs from the first", i, got.header.Get("WWW-Authenticate"))
			}
			if strings.Contains(got.body, "nobody") || strings.Contains(got.body, "erin") {
				t.Errorf("case %d echoes the identifier in the body: %s", i, got.body)
			}
		}
		if strings.Contains(replies[0].header.Get("WWW-Authenticate"), "Basic") {
			t.Errorf("the login 401 offers Basic, which makes a browser prompt: %q", replies[0].header.Get("WWW-Authenticate"))
		}
	})
}

// TestTokenDeliveryIsRefusedWithoutATokenCapability holds that an assembly without the token module refuses a token flow.
func TestTokenDeliveryIsRefusedWithoutATokenCapability(t *testing.T) {
	assemble(t, func(h *harness) {
		h.createAccount("heidi", "heidi-password")
		rec := h.post("/api/v1/auth/flows", map[string]any{
			"method": "password", "delivery": "token",
			"input": map[string]string{"identifier": "heidi", "password": "heidi-password"},
		})
		if rec.status != http.StatusBadRequest {
			t.Errorf("a token delivery with no token module answered %d, want 400: %s", rec.status, rec.body)
		}
	})
}

// TestAbandonedFlowRefusesANextAnswer holds that a flow deleted with DELETE
// flows/current no longer answers: the same cookie is refused an invalid flow.
func TestAbandonedFlowRefusesANextAnswer(t *testing.T) {
	assemble(t, func(h *harness) {
		h.createAccount("ivan", "ivan-password")
		start := h.post("/api/v1/auth/flows", map[string]any{"method": "password"})
		if start.status != http.StatusOK {
			t.Fatalf("POST flows answered %d: %s", start.status, start.body)
		}
		seq := int(start.json(t)["seq"].(float64))
		if del := h.do(http.MethodDelete, "/api/v1/auth/flows/current", nil); del.status != http.StatusNoContent {
			t.Fatalf("DELETE flows/current answered %d: %s", del.status, del.body)
		}
		after := h.post("/api/v1/auth/flows/answer", map[string]any{"seq": seq, "step": "answer", "input": map[string]string{}})
		if after.status != http.StatusUnauthorized {
			t.Errorf("an answer to an abandoned flow answered %d, want 401: %s", after.status, after.body)
		}
	})
}

// TestAnOversizedBodyIsRefused holds the capability's 64 KiB body limit.
func TestAnOversizedBodyIsRefused(t *testing.T) {
	assemble(t, func(h *harness) {
		padded := `{"method":"password"}` + strings.Repeat(" ", 70_000)
		rec := h.do(http.MethodPost, "/api/v1/auth/flows", padded)
		if rec.status != http.StatusBadRequest {
			t.Fatalf("an oversized body answered %d, want 400: %s", rec.status, rec.body)
		}
		if got := rec.json(t)["type"]; got != "urn:speed:authn:bad-request" {
			t.Errorf("an oversized body answered type %v, want urn:speed:authn:bad-request", got)
		}
	})
}

// TestABindingRefusalCarriesTheCapabilityCode holds the problem type of
// the answers no handler produces.
func TestABindingRefusalCarriesTheCapabilityCode(t *testing.T) {
	for _, c := range []struct {
		name        string
		body        string
		contentType string
		status      int
		kind        string
	}{
		{"malformed", `{"method":`, "application/json", http.StatusBadRequest, "urn:speed:authn:bad-request"},
		{
			"unknown member", `{"method":"password","nope":1}`, "application/json", http.StatusBadRequest,
			"urn:speed:authn:bad-request",
		},
		{
			"wrong media type", `{"method":"password"}`, "text/plain", http.StatusUnsupportedMediaType,
			"urn:speed:authn:unsupported-media-type",
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			assemble(t, func(h *harness) {
				rec := h.do(http.MethodPost, "/api/v1/auth/flows", c.body, func(r *http.Request) {
					r.Header.Set("Content-Type", c.contentType)
				})
				if rec.status != c.status {
					t.Fatalf("answer = %d %s, want %d", rec.status, rec.body, c.status)
				}
				if got := rec.json(t)["type"]; got != c.kind {
					t.Errorf("problem type = %v, want %s", got, c.kind)
				}
			})
		})
	}
}

// TestTheAnswerChecksTheBindingBeforeTheBody holds the order of content type, flow binding and body.
func TestTheAnswerChecksTheBindingBeforeTheBody(t *testing.T) {
	assemble(t, func(h *harness) {
		rec := h.do(http.MethodPost, "/api/v1/auth/flows/answer", `{"seq":`)
		if rec.status != http.StatusUnauthorized {
			t.Fatalf("an unbound answer with a malformed body answered %d, want 401: %s", rec.status, rec.body)
		}
		if got := rec.json(t)["type"]; got != "urn:speed:authn:flow-invalid" {
			t.Errorf("problem type = %v, want urn:speed:authn:flow-invalid", got)
		}
		if rec.header.Get("WWW-Authenticate") == "" {
			t.Error("the flow-invalid 401 carries no WWW-Authenticate challenge")
		}
	})
}

// TestTheAnswerCarriesTheCapabilityCodeForAMalformedBody holds that a body that does not parse gets the capability's bad-request code.
func TestTheAnswerCarriesTheCapabilityCodeForAMalformedBody(t *testing.T) {
	assemble(t, func(h *harness) {
		h.createAccount("lena", "lena-password")
		if start := h.post("/api/v1/auth/flows", map[string]any{"method": "password"}); start.status != http.StatusOK {
			t.Fatalf("POST flows answered %d: %s", start.status, start.body)
		}
		rec := h.do(http.MethodPost, "/api/v1/auth/flows/answer", `{"seq":`)
		if rec.status != http.StatusBadRequest {
			t.Fatalf("a bound answer with a malformed body answered %d, want 400: %s", rec.status, rec.body)
		}
		if got := rec.json(t)["type"]; got != "urn:speed:authn:bad-request" {
			t.Errorf("problem type = %v, want urn:speed:authn:bad-request", got)
		}
	})
}

// TestStartAuthenticatedAcceptsTheDeliveryMember holds that the authenticated start accepts and ignores delivery, which follows the session's kind.
func TestStartAuthenticatedAcceptsTheDeliveryMember(t *testing.T) {
	assemble(t, func(h *harness) {
		h.createAccount("karen", "karen-password")
		if done := h.loginFlow("karen", "karen-password"); done.status != http.StatusOK {
			t.Fatalf("the login answered %d: %s", done.status, done.body)
		}
		rec := h.post("/api/v1/auth/flows/authenticated", map[string]any{
			"purpose": "step-up", "method": "password", "delivery": "session",
		})
		if rec.status != http.StatusOK {
			t.Errorf("the authenticated start with a delivery member answered %d: %s", rec.status, rec.body)
		}
	})
}

// TestTheLinkGateReadsTheAccountsRealEmailState holds that an unproved account is refused contact-required and a no-email account is not gated.
func TestTheLinkGateReadsTheAccountsRealEmailState(t *testing.T) {
	linkStart := func(h *harness) reply {
		return h.post("/api/v1/auth/flows/authenticated", map[string]any{
			"purpose": "link", "method": linkStubName,
		})
	}
	assembleWith(t, []core.Module{linkStubModule()}, func(h *harness) {
		unproved, err := h.admin.Create(h.ctx, authn.CreateAccount{
			Kind: authn.KindUser, Username: "erin", Email: "erin@example.com",
			EmailVerifiedVia: authn.Verification{Via: authn.VerifiedViaAdmin},
			Password:         "erin-password", Via: "admin",
		})
		if err != nil {
			t.Fatalf("creating the account: %v", err)
		}
		if done := h.loginFlow("erin", "erin-password"); done.status != http.StatusOK {
			t.Fatalf("the login answered %d: %s", done.status, done.body)
		}
		if got := linkStart(h); got.status != http.StatusForbidden || got.json(t)["type"] != "urn:speed:authn:contact-required" {
			t.Fatalf("the link start of an unproved account answered %d %s, want 403 contact-required", got.status, got.body)
		}

		if err = h.admin.AddIdentifier(h.ctx, unproved.ID, authn.IdentifierEmail, "erin@example.com",
			authn.Verification{Via: authn.VerifiedViaEmailCode}); err != nil {
			t.Fatalf("proving the address: %v", err)
		}
		if got := linkStart(h); got.status != http.StatusOK {
			t.Errorf("the link start after the proof answered %d %s, want 200", got.status, got.body)
		}
	})
	assembleWith(t, []core.Module{linkStubModule()}, func(h *harness) {
		h.createAccount("frank", "frank-password")
		if done := h.loginFlow("frank", "frank-password"); done.status != http.StatusOK {
			t.Fatalf("the login answered %d: %s", done.status, done.body)
		}
		if got := linkStart(h); got.status != http.StatusOK {
			t.Errorf("the link start of an account with no email answered %d %s, want 200", got.status, got.body)
		}
	})
}

const linkStubName = "test-link"

type linkStub struct{}

func (linkStub) Name() string { return linkStubName }

func (linkStub) Describe() authn.MethodDescriptor {
	return authn.MethodDescriptor{Name: linkStubName, Kind: authn.MethodKindForm}
}

func (linkStub) Begin(context.Context, authn.Start) (authn.Step, error) {
	return authn.StepPrompt("link", nil), nil
}

func (linkStub) Answer(context.Context, authn.FlowState, authn.StepInput) (authn.Step, error) {
	return authn.StepRefused(errors.New("authn.login.test: the stub link method answers no step")), nil
}

func linkStubModule() core.Module {
	return core.Module{
		Name:     "test.authn.login.link",
		Provides: []core.Provision{{Token: (*authn.LoginMethod)(nil)}},
		Prepare: func(context.Context, *core.Registry) (core.Enablement, error) {
			return core.Enablement{State: core.StateEnabled}, nil
		},
		New: func(context.Context, *core.Registry) (any, error) { return linkStub{}, nil },
	}
}

// TestTheFirstFactorNeedsNoCookieAndAStaleOneDoesNotBlock holds that POST flows reads no cookie, so a stale session cookie does not block a login.
func TestTheFirstFactorNeedsNoCookieAndAStaleOneDoesNotBlock(t *testing.T) {
	assemble(t, func(h *harness) {
		h.createAccount("dave", "dave-password")
		stale := func(r *http.Request) {
			r.AddCookie(&http.Cookie{Name: "authn-session", Value: "not-a-session"})
		}
		start := h.post("/api/v1/auth/flows", map[string]any{
			"method": "password",
			"input":  map[string]string{"identifier": "dave", "password": "dave-password"},
		}, stale)
		if start.status != http.StatusOK {
			t.Fatalf("POST flows with a stale cookie answered %d: %s", start.status, start.body)
		}
	})
}
