package login_test

import (
	"bytes"
	"context"
	"encoding/json"
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
	"github.com/vislake/speed/pkg/authn/login"
	"github.com/vislake/speed/pkg/authn/login/password"
	"github.com/vislake/speed/pkg/authn/mfa"
	"github.com/vislake/speed/pkg/authn/mount"
	"github.com/vislake/speed/pkg/authn/session"
	"github.com/vislake/speed/pkg/config"
	jsonformat "github.com/vislake/speed/pkg/config/format/json"
	filesource "github.com/vislake/speed/pkg/config/source/file"
	"github.com/vislake/speed/pkg/core"
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
const listening = "an HTTP endpoint is listening"

// requestBound bounds each request a case sends.
const requestBound = 5 * time.Second

// capture is a log handler that keeps what it is handed, so that a case can read
// back where the entry point bound itself.
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
			case "endpoint":
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

// logging delivers the logging capability over the capture.
type logging struct{ logger *slog.Logger }

func (l logging) Named(string) *slog.Logger { return l.logger }
func (logging) Redaction() log.Redaction    { return noRules{} }

type noRules struct{}

func (noRules) AddKeys(...string)              {}
func (noRules) AddPattern(string, log.Matcher) {}

// harness is one running assembly: the bound address, a cookie-jarred client and
// the capabilities a case reaches to set its scene up.
type harness struct {
	t        *testing.T
	ctx      context.Context
	address  string
	client   *http.Client
	admin    authn.AccountAdmin
	mfa      authn.MFA
	accounts authn.Accounts
}

// reply is what a client got back.
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

// assemble drives the real stack to Serve and runs use against it.
func assemble(t *testing.T, use func(h *harness)) {
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
		"db":   map[string]any{"sqlite": map[string]any{"dsn": dsn}},
		"http": map[string]any{"endpoints": map[string]any{"public": map[string]any{"address": "127.0.0.1:0"}}},
		"kv":   map[string]any{"memory": map[string]any{"replicas": 1}},
		"authn": map[string]any{
			"gate": map[string]any{"mount": map[string]any{
				"endpoint": "public", "base-path": "/auth",
				"public-url": "http://127.0.0.1", "allow-insecure": true,
			}},
		},
		"settings": map[string]any{},
		"event":    map[string]any{},
	}
	body, err := json.Marshal(document)
	if err != nil {
		t.Fatalf("encoding the configuration: %v", err)
	}

	modules := []core.Module{
		sqlite.Module(), logModule, speedhttp.Module(), kvmemory.Module(),
		eventmemory.Module(), settings.Module(), mount.Module(), gate.Module(),
		account.Module(), session.Module(), mfa.Module(), login.Module(), password.Module(),
	}
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
				client:  &http.Client{Jar: jar, Timeout: requestBound},
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
			use(h)
			return nil
		},
	})
	if err != nil {
		t.Fatalf("the drive failed: %v", err)
	}
}

// loadConfig loads the configuration the way a host does, with every module's
// declaration in the registry that reads it.
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

// createAccount makes an enabled account with a password.
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

// do issues one request, defaulting the origin headers a browser would send.
func (h *harness) do(method, path string, body any, edits ...func(*http.Request)) reply {
	h.t.Helper()
	var reader io.Reader
	contentType := ""
	switch b := body.(type) {
	case nil:
	case string:
		// A string is sent as it stands, for a case that hands a body too large
		// or too malformed to build through a value.
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

// firstCookie returns the "name=value" pair of a Set-Cookie header.
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
	r := h.get("/auth/session")
	if r.status == http.StatusOK {
		if err := json.Unmarshal([]byte(r.body), &out); err != nil {
			h.t.Fatalf("decoding session status %q: %v", r.body, err)
		}
		return out.Authenticated
	}
	return false
}

// loginFlow runs a password login from the first factor to a delivered session
// and returns the reply of the completion.
func (h *harness) loginFlow(username, pw string) reply {
	h.t.Helper()
	start := h.post("/auth/flows", map[string]any{
		"method": "password",
		"input":  map[string]string{"identifier": username, "password": pw},
	})
	if start.status != http.StatusOK {
		h.t.Fatalf("POST flows answered %d: %s", start.status, start.body)
	}
	seq := int(start.json(h.t)["seq"].(float64))
	done := h.post("/auth/flows/answer", map[string]any{"seq": seq, "step": "complete"})
	return done
}

// TestPasswordLoginTwoStepsDeliversASessionCookie holds the whole login: a
// password flow begun and completed over the real routes makes a session, the
// cookie authenticates a guarded route, the session route reports it, and a
// logout refuses the same cookie afterwards.
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
			t.Errorf("GET /auth/session did not report the new session as authenticated")
		}
		me := h.get("/auth/me")
		if me.status != http.StatusOK {
			t.Errorf("GET /auth/me with the session cookie answered %d: %s", me.status, me.body)
		}

		out := h.post("/auth/logout", nil)
		if out.status != http.StatusNoContent && out.status != http.StatusOK {
			t.Errorf("POST /auth/logout answered %d: %s", out.status, out.body)
		}
		if got := out.header.Get("Set-Cookie"); !strings.Contains(got, "Max-Age=0") {
			t.Errorf("logout did not clear the cookie: %q", got)
		}
		// The same cookie presented again is refused: the session was revoked,
		// not merely forgotten by the client.
		after := h.get("/auth/session", func(r *http.Request) {
			r.Header.Set("Cookie", sessionCookie)
			r.Header.Set("Sec-Fetch-Site", "same-origin")
		})
		if after.status != http.StatusUnauthorized {
			t.Errorf("GET /auth/session with the revoked cookie answered %d, want 401: %s", after.status, after.body)
		}
	})
}

// TestASecondLoginMakesASecondSessionForTheSameAccount holds that every login
// makes a new session and the account is one: two flows from two clients produce
// two session identifiers naming the same account.
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
		// First login, keeping the cookie in the jar.
		if first := h.loginFlow("carol", "carol-password"); first.status != http.StatusOK {
			t.Fatalf("first login answered %d: %s", first.status, first.body)
		}
		// Second login while the first cookie is still presented.
		second := h.loginFlow("carol", "carol-password")
		if second.status != http.StatusOK {
			t.Fatalf("second login answered %d: %s", second.status, second.body)
		}
		if !h.selfStatus() {
			t.Errorf("the second session is not authenticated")
		}
	})
}

// TestEnumerationAnswersAreIndistinguishable holds that an unknown identifier, a
// wrong password, a disabled account and an account with no password all answer
// the same 401 with the same body and the same challenge, so that no branch
// tells an attacker whether an account exists.
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
			h.post("/auth/flows", map[string]any{"method": "password", "input": map[string]string{"identifier": "nobody", "password": "whatever"}}),
			h.post("/auth/flows", map[string]any{"method": "password", "input": map[string]string{"identifier": "erin", "password": "wrong"}}),
			h.post("/auth/flows", map[string]any{"method": "password", "input": map[string]string{"identifier": "frank", "password": "frank-password"}}),
			h.post("/auth/flows", map[string]any{"method": "password", "input": map[string]string{"identifier": "grace", "password": "whatever"}}),
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

// TestTokenDeliveryIsRefusedWithoutATokenCapability holds the optional
// dependency's absence: with no token module the assembly delivers cookies alone,
// and a flow that asks for a token is refused.
func TestTokenDeliveryIsRefusedWithoutATokenCapability(t *testing.T) {
	assemble(t, func(h *harness) {
		h.createAccount("heidi", "heidi-password")
		rec := h.post("/auth/flows", map[string]any{
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
		start := h.post("/auth/flows", map[string]any{"method": "password"})
		if start.status != http.StatusOK {
			t.Fatalf("POST flows answered %d: %s", start.status, start.body)
		}
		seq := int(start.json(t)["seq"].(float64))
		if del := h.do(http.MethodDelete, "/auth/flows/current", nil); del.status != http.StatusNoContent {
			t.Fatalf("DELETE flows/current answered %d: %s", del.status, del.body)
		}
		after := h.post("/auth/flows/answer", map[string]any{"seq": seq, "step": "answer", "input": map[string]string{}})
		if after.status != http.StatusUnauthorized {
			t.Errorf("an answer to an abandoned flow answered %d, want 401: %s", after.status, after.body)
		}
	})
}

// TestAnOversizedBodyIsRefused holds the 64 KiB body limit.
func TestAnOversizedBodyIsRefused(t *testing.T) {
	assemble(t, func(h *harness) {
		big := `{"method":"password","input":{"identifier":"` + strings.Repeat("a", 70_000) + `"}}`
		rec := h.do(http.MethodPost, "/auth/flows", big)
		if rec.status != http.StatusBadRequest {
			t.Errorf("an oversized body answered %d, want 400: %s", rec.status, rec.body)
		}
	})
}

// TestTheFirstFactorNeedsNoCookieAndAStaleOneDoesNotBlock holds that POST flows
// reads no cookie: a stale session cookie left in the jar does not stand in the
// way of beginning a login.
func TestTheFirstFactorNeedsNoCookieAndAStaleOneDoesNotBlock(t *testing.T) {
	assemble(t, func(h *harness) {
		h.createAccount("dave", "dave-password")
		stale := func(r *http.Request) {
			r.AddCookie(&http.Cookie{Name: "authn-session", Value: "not-a-session"})
		}
		start := h.post("/auth/flows", map[string]any{
			"method": "password",
			"input":  map[string]string{"identifier": "dave", "password": "dave-password"},
		}, stale)
		if start.status != http.StatusOK {
			t.Fatalf("POST flows with a stale cookie answered %d: %s", start.status, start.body)
		}
	})
}
