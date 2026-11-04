package authn_test

// The cross-module summary of the failure design: this file assembles the real
// modules of the capability directory, drives them to Serve, and holds the four
// properties design-authn-failure.md states over an assembly larger than one
// module: the sentinel closure of the root capability interfaces, the direction
// each path takes under a dependency outage, and the shape of a 503 that a
// dependency fault produces. Each module's own assembly_test and direction test
// holds its own slice of this; this is the one place many modules stand together
// and the properties are asserted across all of them.

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
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/authn/account"
	"github.com/vislake/speed/pkg/authn/gate"
	"github.com/vislake/speed/pkg/authn/humanverify"
	"github.com/vislake/speed/pkg/authn/login"
	passmethod "github.com/vislake/speed/pkg/authn/login/password"
	"github.com/vislake/speed/pkg/authn/mfa"
	"github.com/vislake/speed/pkg/authn/mount"
	"github.com/vislake/speed/pkg/authn/session"
	"github.com/vislake/speed/pkg/authn/token"
	"github.com/vislake/speed/pkg/config"
	jsonformat "github.com/vislake/speed/pkg/config/format/json"
	filesource "github.com/vislake/speed/pkg/config/source/file"
	"github.com/vislake/speed/pkg/core"
	"github.com/vislake/speed/pkg/db"
	"github.com/vislake/speed/pkg/db/sqlite"
	eventmemory "github.com/vislake/speed/pkg/event/memory"
	speedhttp "github.com/vislake/speed/pkg/http"
	"github.com/vislake/speed/pkg/kv"
	"github.com/vislake/speed/pkg/log"
	"github.com/vislake/speed/pkg/settings"
)

// summaryListening is the record the entry point writes once an endpoint's
// address is bound, the only place outside the process that says which port the
// operating system picked for an endpoint configured on port 0.
const summaryListening = "an HTTP listener is listening"

// summaryRequestBound bounds each request a case sends.
const summaryRequestBound = 5 * time.Second

// unknownSessionToken is a value shaped like a session token that no session
// carries. It is short enough that the session backend looks it up rather than
// refusing it without a read, so a request that presents it reaches the store.
const unknownSessionToken = "summary-unknown-session-token-value"

// summaryCapture is a log handler that keeps what it is handed, so a case can
// read back the address the entry point bound itself to.
type summaryCapture struct {
	mu      sync.Mutex
	records []slog.Record
}

func (c *summaryCapture) Enabled(context.Context, slog.Level) bool { return true }
func (c *summaryCapture) Handle(_ context.Context, r slog.Record) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.records = append(c.records, r.Clone())
	return nil
}
func (c *summaryCapture) WithAttrs([]slog.Attr) slog.Handler { return c }
func (c *summaryCapture) WithGroup(string) slog.Handler      { return c }

func (c *summaryCapture) addressOf(endpoint string) string {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, r := range c.records {
		if r.Message != summaryListening {
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

// summaryLogging delivers the logging capability over the capture.
type summaryLogging struct{ logger *slog.Logger }

func (l summaryLogging) Named(string) *slog.Logger { return l.logger }
func (summaryLogging) Redaction() log.Redaction    { return summaryNoRules{} }

type summaryNoRules struct{}

func (summaryNoRules) AddKeys(...string)              {}
func (summaryNoRules) AddPattern(string, log.Matcher) {}

// faultKV is the key-value capability with a switch: while it is whole it serves
// the login flow store as the in-process implementation does, and once breakIt
// is called every operation reports the store unreachable, which is how a case
// puts the key-value dependency in an outage.
type faultKV struct {
	mu      sync.Mutex
	entries map[string][]byte
	broken  atomic.Bool
}

var errKVUnreachable = errors.New("the key-value store is unreachable")

func newFaultKV() *faultKV { return &faultKV{entries: make(map[string][]byte)} }

// breakIt puts the store into its outage.
func (k *faultKV) breakIt() { k.broken.Store(true) }

func (k *faultKV) down() error {
	if k.broken.Load() {
		return errKVUnreachable
	}
	return nil
}

func (k *faultKV) Get(ctx context.Context, key string) ([]byte, bool, error) {
	if err := ctx.Err(); err != nil {
		return nil, false, err
	}
	if err := k.down(); err != nil {
		return nil, false, err
	}
	k.mu.Lock()
	defer k.mu.Unlock()
	v, ok := k.entries[key]
	if !ok {
		return nil, false, nil
	}
	return bytes.Clone(v), true, nil
}

func (k *faultKV) Set(ctx context.Context, key string, value []byte, _ time.Duration) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := k.down(); err != nil {
		return err
	}
	k.mu.Lock()
	defer k.mu.Unlock()
	k.entries[key] = bytes.Clone(value)
	return nil
}

func (k *faultKV) Delete(ctx context.Context, key string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := k.down(); err != nil {
		return err
	}
	k.mu.Lock()
	defer k.mu.Unlock()
	delete(k.entries, key)
	return nil
}

func (k *faultKV) IncrByFloat(ctx context.Context, key string, delta float64) (float64, error) {
	return k.increment(ctx, key, delta)
}

func (k *faultKV) IncrByFloatWithTTL(ctx context.Context, key string, delta float64, _ time.Duration) (float64, error) {
	return k.increment(ctx, key, delta)
}

func (k *faultKV) increment(ctx context.Context, key string, delta float64) (float64, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	if err := k.down(); err != nil {
		return 0, err
	}
	k.mu.Lock()
	defer k.mu.Unlock()
	var current float64
	if raw, ok := k.entries[key]; ok {
		if _, err := fmt.Sscanf(string(raw), "%g", &current); err != nil {
			return 0, err
		}
	}
	next := current + delta
	k.entries[key] = []byte(fmt.Sprintf("%g", next))
	return next, nil
}

func (k *faultKV) CompareAndSwap(ctx context.Context, key string, old, newVal []byte) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	if err := k.down(); err != nil {
		return false, err
	}
	k.mu.Lock()
	defer k.mu.Unlock()
	if !bytes.Equal(k.entries[key], old) {
		return false, nil
	}
	k.entries[key] = bytes.Clone(newVal)
	return true, nil
}

func (k *faultKV) SetIfAbsent(ctx context.Context, key string, value []byte, _ time.Duration) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	if err := k.down(); err != nil {
		return false, err
	}
	k.mu.Lock()
	defer k.mu.Unlock()
	if _, ok := k.entries[key]; ok {
		return false, nil
	}
	k.entries[key] = bytes.Clone(value)
	return true, nil
}

// summaryHost is one running assembly: the bound address, two clients (one that
// carries cookies from the jar it is given and one that carries none, for a case
// that hands a cookie by hand) and the capabilities a case reaches to set its
// scene up.
type summaryHost struct {
	t        *testing.T
	ctx      context.Context
	address  string
	client   *http.Client
	bare     *http.Client
	reg      *core.Registry
	kv       *faultKV
	accounts authn.Accounts
	admin    authn.AccountAdmin
	sessions authn.Sessions
	mfa      authn.MFA
	tokens   authn.Tokens
	pass     authn.Passwords
}

// summaryReply is what a client got back.
type summaryReply struct {
	status int
	header http.Header
	body   string
}

func (r summaryReply) json(t *testing.T) map[string]any {
	t.Helper()
	var out map[string]any
	if err := json.Unmarshal([]byte(r.body), &out); err != nil {
		t.Fatalf("decoding %q as JSON: %v", r.body, err)
	}
	return out
}

// runSummary drives the real stack to Serve and runs use against it. The modules
// are the ones the capability directory delivers and a host would name together,
// with the key-value capability supplied by faultKV so a case can put it into an
// outage.
func runSummary(t *testing.T, use func(h *summaryHost)) {
	t.Helper()

	dsn := filepath.Join(t.TempDir(), "authn.db")
	capt := &summaryCapture{}
	logger := slog.New(capt)
	logModule := core.Module{
		Name:     "stand-in.logging",
		Provides: []core.Provision{{Token: (*log.Logger)(nil), Exclusive: true}},
		New:      func(context.Context, *core.Registry) (any, error) { return summaryLogging{logger: logger}, nil },
	}

	kvStore := newFaultKV()
	kvModule := core.Module{
		Name:     "stand-in.kv",
		Provides: []core.Provision{{Token: (*kv.KV)(nil), Exclusive: true}},
		New:      func(context.Context, *core.Registry) (any, error) { return kvStore, nil },
	}

	keyPath := summarySigningKey(t)

	document := map[string]any{
		"db": map[string]any{"sqlite": map[string]any{"dsn": dsn}},
		"http": map[string]any{
			"listeners": map[string]any{"public": map[string]any{"address": "127.0.0.1:0"}},
			"endpoints": map[string]any{"public": map[string]any{"listener": "public"}},
		},
		"settings": map[string]any{},
		"event":    map[string]any{},
		"authn": map[string]any{
			"gate": map[string]any{"mount": map[string]any{
				"endpoints":  map[string]any{"default": "public"},
				"public-url": "http://127.0.0.1", "allow-insecure": true,
			}},
			"token": map[string]any{
				"issuer":           "https://auth.example.com",
				"signing-key-file": keyPath,
			},
			// The gate is a required dependency of the login engine, and these cases
			// present no proof: both surfaces run the off mechanism.
			"humanverify": map[string]any{"mode": "off", "phone-mode": "off"},
		},
	}
	body, err := json.Marshal(document)
	if err != nil {
		t.Fatalf("encoding the configuration: %v", err)
	}

	modules := []core.Module{
		sqlite.Module(), logModule, kvModule, speedhttp.Module(),
		eventmemory.Module(), settings.Module(), mount.Module(), gate.Module(),
		humanverify.Module(),
		account.Module(), session.Module(), mfa.Module(), login.Module(),
		passmethod.Module(), token.Module(),
	}
	reader := summaryLoadConfig(t, string(body), modules...)

	reg := core.New()
	names := make([]string, 0, len(modules))
	for _, m := range modules {
		reg.Register(m)
		names = append(names, m.Name)
	}
	reg.Register(config.Module())
	reg.Supply(reader, config.ModuleName)

	ran := false
	driveErr := reg.Drive(t.Context(), core.Drive{
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
			h := &summaryHost{
				t:       t,
				ctx:     ctx,
				address: address,
				client:  &http.Client{Jar: jar, Timeout: summaryRequestBound},
				bare:    &http.Client{Timeout: summaryRequestBound},
				reg:     reg,
				kv:      kvStore,
			}
			if h.accounts, err = core.Resolve[authn.Accounts](reg); err != nil {
				return err
			}
			if h.admin, err = core.Resolve[authn.AccountAdmin](reg); err != nil {
				return err
			}
			if h.sessions, err = core.Resolve[authn.Sessions](reg); err != nil {
				return err
			}
			if h.mfa, err = core.Resolve[authn.MFA](reg); err != nil {
				return err
			}
			if h.tokens, err = core.Resolve[authn.Tokens](reg); err != nil {
				return err
			}
			if h.pass, err = core.Resolve[authn.Passwords](reg); err != nil {
				return err
			}
			ran = true
			use(h)
			return nil
		},
	})
	if !ran {
		t.Fatalf("the drive did not reach Serve: %v", driveErr)
	}
	// A case that put a dependency into an outage may leave a module whose Stop
	// or Close reports against the broken dependency; that is the fault the case
	// made, not a failure of the drive. An action error is still fatal.
	if driveErr != nil && strings.Contains(driveErr.Error(), "core: action:") {
		t.Fatalf("the action failed: %v", driveErr)
	}
}

// summaryLoadConfig loads the configuration the way a host does, with every
// module's declaration in the registry that reads it.
func summaryLoadConfig(t *testing.T, body string, modules ...core.Module) config.Reader {
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
		Resources: []any{config.HostIdentity{Prefix: "SPEEDSUMMARYTEST", DefaultLocator: "file://" + locator}},
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

// summarySigningKey writes a freshly generated ES256 signing key to a file only
// its owner can read and returns its path, so the token module can issue.
func summarySigningKey(t *testing.T) string {
	t.Helper()
	pemBytes, err := token.GenerateKey("ES256")
	if err != nil {
		t.Fatalf("generating a signing key: %v", err)
	}
	path := filepath.Join(t.TempDir(), "signing.pem")
	if err := os.WriteFile(path, pemBytes, 0o600); err != nil {
		t.Fatalf("writing the signing key: %v", err)
	}
	return path
}

// do issues one request through client, defaulting the origin header a browser
// would send and, when cookie is not empty, the Cookie header a case hands by
// hand.
func (h *summaryHost) do(client *http.Client, method, path string, body []byte, contentType, cookie string) summaryReply {
	h.t.Helper()
	var reader io.Reader
	if body != nil {
		reader = bytes.NewReader(body)
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
	if cookie != "" {
		req.Header.Set("Cookie", cookie)
	}
	resp, err := client.Do(req)
	if err != nil {
		h.t.Errorf("%s %s got no response: %v", method, path, err)
		return summaryReply{}
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		h.t.Errorf("reading the body of %s %s: %v", method, path, err)
	}
	return summaryReply{status: resp.StatusCode, header: resp.Header, body: string(raw)}
}

func (h *summaryHost) get(path string) summaryReply {
	return h.do(h.client, http.MethodGet, path, nil, "", "")
}

func (h *summaryHost) getWithCookie(path, cookie string) summaryReply {
	return h.do(h.bare, http.MethodGet, path, nil, "", cookie)
}

func (h *summaryHost) postJSON(path string, v any) summaryReply {
	body, err := json.Marshal(v)
	if err != nil {
		h.t.Fatalf("encoding the body of %s: %v", path, err)
	}
	return h.do(h.client, http.MethodPost, path, body, "application/json", "")
}

func (h *summaryHost) postForm(path string, values url.Values) summaryReply {
	return h.do(h.bare, http.MethodPost, path, []byte(values.Encode()), "application/x-www-form-urlencoded", "")
}

// closeDatabase closes the underlying connection pool of the assembly's
// database handle, so every later read against it fails the way an unreachable
// database does.
func (h *summaryHost) closeDatabase() {
	h.t.Helper()
	database, err := core.Resolve[db.Database](h.reg)
	if err != nil {
		h.t.Fatalf("resolving the database capability: %v", err)
	}
	raw, err := database.DB().DB()
	if err != nil {
		h.t.Fatalf("reaching the database's pool: %v", err)
	}
	if err := raw.Close(); err != nil {
		h.t.Fatalf("closing the database's pool: %v", err)
	}
}

// createAccount makes an enabled account with a password.
func (h *summaryHost) createAccount(username, pw string) authn.AccountID {
	h.t.Helper()
	acct, err := h.admin.Create(h.ctx, authn.CreateAccount{
		Kind: authn.KindUser, Username: username, Password: pw, Via: authn.ReasonAdmin,
	})
	if err != nil {
		h.t.Fatalf("creating account %q: %v", username, err)
	}
	return acct.ID
}

// loginFlow runs a password login from the first factor to a delivered session
// over the jar-carrying client and returns the completion's reply.
func (h *summaryHost) loginFlow(username, pw string) summaryReply {
	h.t.Helper()
	start := h.postJSON("/api/v1/auth/flows", map[string]any{
		"method": "password",
		"input":  map[string]string{"identifier": username, "password": pw},
	})
	if start.status != http.StatusOK {
		h.t.Fatalf("POST flows answered %d: %s", start.status, start.body)
	}
	seq := int(start.json(h.t)["seq"].(float64))
	done := h.postJSON("/api/v1/auth/flows/answer", map[string]any{"seq": seq, "step": "complete"})
	if done.status != http.StatusOK {
		h.t.Fatalf("complete answered %d: %s", done.status, done.body)
	}
	return done
}

// sessionCookieName reads the name of the cookie a completion set, so a case can
// present a value of its own under the name the session backend accepts.
func sessionCookieName(t *testing.T, header string) string {
	t.Helper()
	if header == "" {
		t.Fatal("the completion set no cookie")
	}
	pair, _, _ := strings.Cut(header, ";")
	name, _, ok := strings.Cut(pair, "=")
	if !ok || name == "" {
		t.Fatalf("the Set-Cookie header %q carries no name", header)
	}
	return strings.TrimSpace(name)
}

// authenticated reports whether GET /api/v1/auth/session answered that somebody is
// logged in.
func (h *summaryHost) authenticated(reply summaryReply) bool {
	h.t.Helper()
	if reply.status != http.StatusOK {
		h.t.Fatalf("GET /api/v1/auth/session answered %d: %s", reply.status, reply.body)
	}
	return reply.json(h.t)["authenticated"] == true
}

// assertNoDependencyName holds the design's "the answer does not say which
// dependency is unavailable": the body of a dependency-fault 503 carries no word
// that names the kind of store that failed.
func assertNoDependencyName(t *testing.T, body string) {
	t.Helper()
	low := strings.ToLower(body)
	for _, word := range []string{"sqlite", "postgres", "database", "kv", "key-value", "idp", "redis", "memcached"} {
		if strings.Contains(low, word) {
			t.Errorf("a dependency-fault 503 body names the dependency kind %q: %s", word, body)
		}
	}
}

// The runtime sentinels of design-authn-failure.md's runtime table, the closed
// set a runtime failure of this capability directory is classified into.
var runtimeSentinels = []error{
	authn.ErrInvalidAccountID, authn.ErrAccountNotFound, authn.ErrAccountExists, authn.ErrContactTaken,
	authn.ErrInvalidCredentials,
	authn.ErrPasswordPolicy, authn.ErrInvalidIdentifier, authn.ErrInvalidArgument, authn.ErrFactorLimit,
	authn.ErrIdentifierRequired, authn.ErrUsernameImmutable, authn.ErrAccountUnavailable, authn.ErrMethodDisabled, authn.ErrCleanupIncomplete,
	authn.ErrSessionRevoked, authn.ErrThrottled, authn.ErrFlowInvalid, authn.ErrStepOutOfOrder,
	authn.ErrLinkRequired, authn.ErrNotProvisioned, authn.ErrSignupClosed, authn.ErrDomainMismatch,
	authn.ErrProvisionRefused, authn.ErrBindingConflict, authn.ErrLastLoginMethod, authn.ErrLastFactor,
	authn.ErrLastTenantAdmin,
	authn.ErrFactorNotEnrolled, authn.ErrReplay, authn.ErrStepUpRequired, authn.ErrFactorNotSeasoned,
	authn.ErrFactorFlagged,
	authn.ErrSessionNotFound, authn.ErrCSRF, authn.ErrTokenInvalid, authn.ErrInvalidGrant,
	authn.ErrRefreshReuse, authn.ErrNoSigningKey, authn.ErrReservedClaim, authn.ErrImpersonationRestricted,
	authn.ErrImpersonationActive, authn.ErrNotImpersonating, authn.ErrImpersonationNeedsSession,
	authn.ErrForbidden, authn.ErrAuditRequired, authn.ErrUnsupportedRegion,
	authn.ErrDomainClaimed, authn.ErrAssertionReplay,
	authn.ErrProviderNotFound, authn.ErrUnavailable, authn.ErrBusy, authn.ErrUsernameReserved,
}

// startupSentinels are the startup failures, which a runtime call must never
// report: the design puts them behind a startup stage, not behind a request.
var startupSentinels = []error{
	authn.ErrNoRouter, authn.ErrNoVerifier, authn.ErrClaimConflict, authn.ErrInvalidClaim,
	authn.ErrInvalidConfig, authn.ErrUnknownEndpoint, authn.ErrExemptionUnguarded, authn.ErrAccessConflict,
	authn.ErrRoutesUnconfigured, authn.ErrRoutesUnguarded, authn.ErrInsecureConfig, authn.ErrNoEncryptionKey,
	authn.ErrInvalidKey, authn.ErrNotMigrated, authn.ErrMethodConflict, authn.ErrFactorConflict,
	authn.ErrNoLoginMethod, authn.ErrNoFactor, authn.ErrDrainTimeout,
}

func matchesAny(err error, sentinels []error) bool {
	for _, s := range sentinels {
		if errors.Is(err, s) {
			return true
		}
	}
	return false
}

// classified is the property a root capability interface's error must have: it
// is one of the runtime sentinels, or it wraps an external error. An error that
// matches a startup sentinel, or one that is bare and unnamed, is neither.
func classified(err error) bool {
	if matchesAny(err, runtimeSentinels) {
		return true
	}
	return errors.Unwrap(err) != nil && !matchesAny(err, startupSentinels)
}

// TestEveryCapabilityErrorIsARuntimeSentinelOrAWrappedExternal drives the error
// paths of the root capability interfaces in one assembled host and holds the
// design's closure: each error can be classified with errors.Is to a runtime
// sentinel, or is a wrapped external error. It is the assembly test the
// assumption table of design-authn-failure.md names.
func TestEveryCapabilityErrorIsARuntimeSentinelOrAWrappedExternal(t *testing.T) {
	runSummary(t, func(h *summaryHost) {
		acct := h.createAccount("dave", "dave-password")

		calls := []struct {
			name string
			call func() error
		}{
			{"Accounts.Get of a missing account", func() error {
				_, err := h.accounts.Get(h.ctx, authn.AccountID("summary-no-such-account"))
				return err
			}},
			{"AccountAdmin.SetDisplayName over the bound", func() error {
				return h.admin.SetDisplayName(h.ctx, acct, strings.Repeat("x", 200))
			}},
			{"AccountAdmin.SetStatus of a missing account", func() error {
				return h.admin.SetStatus(h.ctx, authn.AccountID("summary-no-such-account"), authn.StatusDisabled, authn.ReasonAdmin)
			}},
			{"Sessions.Get of a missing session", func() error {
				_, err := h.sessions.Get(h.ctx, authn.SessionID("summary-no-such-session"))
				return err
			}},
			{"Tokens.Refresh of a bogus token", func() error {
				_, _, err := h.tokens.Refresh(h.ctx, "spd_rt_summary_bogus")
				return err
			}},
			{"Passwords.Verify of an unknown identifier", func() error {
				_, _, err := h.pass.Verify(h.ctx, "summary-nobody", "whatever")
				return err
			}},
		}

		sentinels := 0
		for _, c := range calls {
			err := c.call()
			if err == nil {
				t.Errorf("%s returned no error", c.name)
				continue
			}
			if !classified(err) {
				t.Errorf("%s = %v, which is neither a runtime sentinel nor a wrapped external error", c.name, err)
			}
			if matchesAny(err, runtimeSentinels) {
				sentinels++
			}
		}
		// Positive control: the battery has to have produced runtime-sentinel
		// classifications, or the assertion would pass on a set that never held
		// one.
		if sentinels == 0 {
			t.Fatal("no call in the battery classified to a runtime sentinel, so the check read nothing")
		}

		// The same property under a dependency outage: the database goes away and
		// a capability call reports a runtime sentinel, not a bare error.
		h.closeDatabase()
		if _, err := h.sessions.Get(h.ctx, authn.SessionID("summary-no-such-session")); !classified(err) {
			t.Errorf("Sessions.Get with the database gone = %v, which is neither a runtime sentinel nor a wrapped external error", err)
		}
		if err := h.admin.SetDisplayName(h.ctx, acct, "x"); !classified(err) {
			t.Errorf("AccountAdmin.SetDisplayName with the database gone = %v, which is neither a runtime sentinel nor a wrapped external error", err)
		}
	})
}

// TestADatabaseFaultAnswers503Never200Or401 holds the shape of a dependency
// fault's answer over two entry points at once: a protected route and the token
// endpoint both answer 503, never 200 and never 401, and neither body names the
// dependency kind. The baseline while the database answers shows the two are
// otherwise a 401 and a 400, so the 503 is the fault and not the ordinary path.
func TestADatabaseFaultAnswers503Never200Or401(t *testing.T) {
	runSummary(t, func(h *summaryHost) {
		done := h.loginFlowAccount("erin", "erin-password")
		cookieName := sessionCookieName(t, done.header.Get("Set-Cookie"))
		bogus := cookieName + "=" + unknownSessionToken

		// Baseline: with the database answering, the protected route refuses the
		// unknown cookie as a 401 and the token endpoint refuses the bogus grant
		// with its own failure code, never a 503.
		if got := h.getWithCookie("/api/v1/auth/sessions", bogus); got.status != http.StatusUnauthorized {
			t.Fatalf("GET /api/v1/auth/sessions with an unknown cookie answered %d, want 401: %s", got.status, got.body)
		}
		if got := h.tokenRefresh("spd_rt_summary_bogus"); got.status != http.StatusBadRequest {
			t.Fatalf("POST /api/v1/auth/token with a bogus grant answered %d, want 400: %s", got.status, got.body)
		}

		h.closeDatabase()

		guarded := h.getWithCookie("/api/v1/auth/sessions", bogus)
		if guarded.status != http.StatusServiceUnavailable {
			t.Errorf("GET /api/v1/auth/sessions under a database fault answered %d, want 503: %s", guarded.status, guarded.body)
		}
		assertNoDependencyName(t, guarded.body)

		tok := h.tokenRefresh("spd_rt_summary_bogus")
		if tok.status != http.StatusServiceUnavailable {
			t.Errorf("POST /api/v1/auth/token under a database fault answered %d, want 503: %s", tok.status, tok.body)
		}
		if !strings.Contains(tok.body, "temporarily_unavailable") {
			t.Errorf("the token endpoint's 503 body is %q, want the temporarily_unavailable code", tok.body)
		}
		assertNoDependencyName(t, tok.body)
	})
}

// TestADatabaseFaultRelaxesOnlyTheSessionCheckCacheInBound holds the direction
// table's one leniency for the database: a session answer already cached is
// served until check-ttl runs out, and every other path is refused. A cookie
// whose answer was cached still authenticates, while an uncached cookie and the
// login path both answer 503 -- so leniency appears on the listed path and
// nowhere else.
func TestADatabaseFaultRelaxesOnlyTheSessionCheckCacheInBound(t *testing.T) {
	runSummary(t, func(h *summaryHost) {
		done := h.loginFlowAccount("frank", "frank-password")
		cookieName := sessionCookieName(t, done.header.Get("Set-Cookie"))

		// Warm the check cache for this cookie while the database answers.
		if !h.authenticated(h.get("/api/v1/auth/session")) {
			t.Fatal("the freshly delivered session did not authenticate")
		}

		h.closeDatabase()

		// The cached answer inside check-ttl is the listed leniency: the same
		// cookie still authenticates though the database is gone.
		if !h.authenticated(h.get("/api/v1/auth/session")) {
			t.Errorf("a cached session stopped authenticating on a database fault, so the bounded leniency is not in force")
		}

		// An uncached cookie has no leniency: it is refused with a 503, not
		// admitted as anonymous and not denied with a 401.
		uncached := h.getWithCookie("/api/v1/auth/session", cookieName+"="+unknownSessionToken)
		if uncached.status != http.StatusServiceUnavailable {
			t.Errorf("an uncached cookie under a database fault answered %d, want 503: %s", uncached.status, uncached.body)
		}

		// The login path is not on the leniency list: it is refused.
		start := h.postJSON("/api/v1/auth/flows", map[string]any{
			"method": "password",
			"input":  map[string]string{"identifier": "frank", "password": "frank-password"},
		})
		if start.status != http.StatusServiceUnavailable {
			t.Errorf("beginning a login under a database fault answered %d, want 503: %s", start.status, start.body)
		}
	})
}

// TestAKeyValueFaultRefusesFlowsButLeavesTheSessionAlone holds the key-value
// dependency's direction: with the flow store gone, beginning a login flow is
// refused with a 503, while an already-established session, which lives in the
// database, still authenticates. The design's "sessions and revocations are in
// the database, so the key-value store's outage leaves a logged-in user alone"
// has no other cross-module witness.
func TestAKeyValueFaultRefusesFlowsButLeavesTheSessionAlone(t *testing.T) {
	runSummary(t, func(h *summaryHost) {
		h.loginFlowAccount("grace", "grace-password")

		h.kv.breakIt()

		// The session survives: it is a database row, not a key-value entry.
		if !h.authenticated(h.get("/api/v1/auth/session")) {
			t.Error("an established session stopped authenticating on a key-value fault, so the session depends on the flow store")
		}

		// Beginning a login flow needs the store and is refused.
		start := h.postJSON("/api/v1/auth/flows", map[string]any{
			"method": "password",
			"input":  map[string]string{"identifier": "grace", "password": "grace-password"},
		})
		if start.status != http.StatusServiceUnavailable {
			t.Errorf("beginning a login under a key-value fault answered %d, want 503: %s", start.status, start.body)
		}
	})
}

// loginFlowAccount creates an account and logs it in, returning the completion's
// reply so a case can read the session cookie it set.
func (h *summaryHost) loginFlowAccount(username, pw string) summaryReply {
	h.t.Helper()
	h.createAccount(username, pw)
	return h.loginFlow(username, pw)
}

// tokenRefresh posts a refresh_token grant to the token endpoint with the
// standard client, which reads no cookie.
func (h *summaryHost) tokenRefresh(refresh string) summaryReply {
	return h.postForm("/api/v1/auth/token", url.Values{
		"grant_type":    {"refresh_token"},
		"refresh_token": {refresh},
	})
}
