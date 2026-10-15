package impersonation

import (
	"bytes"
	"context"
	"errors"
	"io/fs"
	"log/slog"
	"net/http"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"go.opentelemetry.io/otel/attribute"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"github.com/vislake/speed/pkg/audit"
	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/authn/authntest"
	"github.com/vislake/speed/pkg/authn/internal/mount/mounttest"
	"github.com/vislake/speed/pkg/event"
	"github.com/vislake/speed/pkg/internal/dbtest"
	"github.com/vislake/speed/pkg/internal/pgpool/pgtest"
	"github.com/vislake/speed/pkg/settings"
)

// base is the instant the cases that need a fixed time start from.
var base = time.Date(2026, time.March, 14, 12, 0, 0, 0, time.UTC)

// The dialects the module runs on.
const (
	sqliteDialect   = "sqlite"
	postgresDialect = "postgres"
)

// fakeClock is a clock a case moves.
type fakeClock struct {
	mu  sync.Mutex
	now time.Time
}

func newClock(at time.Time) *fakeClock { return &fakeClock{now: at} }

func (c *fakeClock) read() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *fakeClock) advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

// logBuffer collects what the module logs, as text lines.
type logBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *logBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *logBuffer) logger() *slog.Logger {
	return slog.New(slog.NewTextHandler(b, &slog.HandlerOptions{Level: slog.LevelDebug}))
}

func (b *logBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// fakeAccounts is the account store as far as this module reads it.
type fakeAccounts struct {
	authn.Accounts
	mu       sync.Mutex
	accounts map[authn.AccountID]authn.Account
	err      error
}

func newAccounts() *fakeAccounts { return &fakeAccounts{accounts: map[authn.AccountID]authn.Account{}} }

func (a *fakeAccounts) put(id authn.AccountID, acct authn.Account) {
	a.mu.Lock()
	defer a.mu.Unlock()
	acct.ID = id
	a.accounts[id] = acct
}

// remove drops an account, so that Get answers ErrAccountNotFound as a deleted
// account's row would.
func (a *fakeAccounts) remove(id authn.AccountID) {
	a.mu.Lock()
	defer a.mu.Unlock()
	delete(a.accounts, id)
}

func (a *fakeAccounts) Get(_ context.Context, id authn.AccountID) (authn.Account, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.err != nil {
		return authn.Account{}, a.err
	}
	acct, ok := a.accounts[id]
	if !ok {
		return authn.Account{}, authn.ErrAccountNotFound
	}
	return acct, nil
}

// fakeMemberships is the membership store as far as this module reads it.
type fakeMemberships struct {
	authn.Memberships
	mu      sync.Mutex
	members map[string]bool
	err     error
}

func newMemberships() *fakeMemberships { return &fakeMemberships{members: map[string]bool{}} }

func memberKey(id authn.AccountID, tenant string) string { return tenant + "/" + string(id) }

func (f *fakeMemberships) set(id authn.AccountID, tenant string, member bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.members[memberKey(id, tenant)] = member
}

func (f *fakeMemberships) IsMember(_ context.Context, id authn.AccountID, tenant string) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return false, f.err
	}
	return f.members[memberKey(id, tenant)], nil
}

// fakeMFA is the multi-factor composition as far as this module reads it: the
// seasoned answer alone.
type fakeMFA struct {
	authn.MFA
	seasoned bool
	err      error
}

func (f *fakeMFA) Seasoned(_ context.Context, _ authn.AccountID) (bool, error) {
	if f.err != nil {
		return false, f.err
	}
	return f.seasoned, nil
}

// fakeSessions is the session store as far as this module reads it: the status of
// a session, the marker it sets, and the cookie it writes.
type fakeSessions struct {
	authn.Sessions
	mu        sync.Mutex
	session   authn.SessionStatus
	statusErr error

	setErr    error
	setCalls  []string
	issued    authn.Issued
	cookie    bool
	cookieVal authn.Issued
}

func newSessions(active bool, expiresAt time.Time) *fakeSessions {
	return &fakeSessions{
		session: authn.SessionStatus{
			Active: active, Account: actorID, Kind: authn.SessionBrowser,
			Methods:   []authn.Method{authn.MethodPassword, authn.MethodTOTP},
			Assurance: authn.AssuranceMulti, AuthTime: base,
			ExpiresAt: expiresAt,
		},
		issued: authn.Issued{ID: sessionID, Token: "rotated-token", ExpiresAt: expiresAt},
	}
}

func (s *fakeSessions) Status(_ context.Context, _ authn.SessionID) (authn.SessionStatus, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.statusErr != nil {
		return authn.SessionStatus{}, s.statusErr
	}
	return s.session, nil
}

func (s *fakeSessions) SetImpersonation(_ context.Context, _ authn.SessionID, grantID string) (authn.Issued, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.setCalls = append(s.setCalls, grantID)
	if s.setErr != nil {
		return authn.Issued{}, s.setErr
	}
	return s.issued, nil
}

func (s *fakeSessions) SetCookie(_ http.ResponseWriter, in authn.Issued) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.cookie = true
	s.cookieVal = in
}

// recordingAudit keeps the rows it was asked to write, and can be told to fail.
type recordingAudit struct {
	mu     sync.Mutex
	rows   []audit.Entry
	err    error
	onCall func()
}

func (a *recordingAudit) Record(_ context.Context, e audit.Entry) error {
	a.mu.Lock()
	a.rows = append(a.rows, e)
	onCall := a.onCall
	err := a.err
	a.mu.Unlock()
	if onCall != nil {
		onCall()
	}
	return err
}

func (a *recordingAudit) entries() []audit.Entry {
	a.mu.Lock()
	defer a.mu.Unlock()
	return append([]audit.Entry(nil), a.rows...)
}

// fakeDecider answers a decision a case scripts.
type fakeDecider struct {
	allow bool
	err   error
	panic bool
	calls []authn.Decision
}

func (d *fakeDecider) Decide(_ context.Context, dec authn.Decision) (bool, error) {
	d.calls = append(d.calls, dec)
	if d.panic {
		panic("the decider panicked")
	}
	return d.allow, d.err
}

// fakeTokens signs a token a case reads back.
type fakeTokens struct {
	authn.Tokens
	err     error
	issued  []authn.IssueToken
	expires time.Time
}

func (f *fakeTokens) Issue(_ context.Context, in authn.IssueToken) (authn.AccessToken, error) {
	f.issued = append(f.issued, in)
	if f.err != nil {
		return authn.AccessToken{}, f.err
	}
	expires := f.expires
	if expires.IsZero() {
		expires = base.Add(in.TTL)
	}
	return authn.AccessToken{Value: "imp-token", ExpiresAt: expires}, nil
}

// recordingBus keeps what was published and delivers it to the handlers
// subscribed, as a process-local bus does.
type recordingBus struct {
	mu        sync.Mutex
	handlers  map[string][]event.Handler
	published []event.Event
	err       error
}

func newBus() *recordingBus { return &recordingBus{handlers: map[string][]event.Handler{}} }

func (b *recordingBus) Subscribe(eventType string, _ string, h event.Handler) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.handlers[eventType] = append(b.handlers[eventType], h)
}

func (b *recordingBus) Publish(_ context.Context, evt event.Event) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.published = append(b.published, evt)
	return b.err
}

func (b *recordingBus) ofType(typ string) []event.Event {
	b.mu.Lock()
	defer b.mu.Unlock()
	var out []event.Event
	for _, e := range b.published {
		if e.Type == typ {
			out = append(out, e)
		}
	}
	return out
}

// endedPayload decodes the ended event a case expects, failing when there is not
// exactly one.
func (r *rig) endedPayload() authn.ImpersonationEnded {
	r.t.Helper()
	events := r.bus.ofType(authn.EventImpersonationEnded)
	if len(events) != 1 {
		r.t.Fatalf("%d ended events were published, want 1", len(events))
	}
	var p authn.ImpersonationEnded
	if err := event.DecodePayload(events[0].Payload, &p); err != nil {
		r.t.Fatalf("decoding the ended payload: %v", err)
	}
	return p
}

// startedPayload decodes the started event a case expects, failing when there is
// not exactly one.
func (r *rig) startedPayload() authn.ImpersonationStarted {
	r.t.Helper()
	events := r.bus.ofType(authn.EventImpersonationStarted)
	if len(events) != 1 {
		r.t.Fatalf("%d started events were published, want 1", len(events))
	}
	var p authn.ImpersonationStarted
	if err := event.DecodePayload(events[0].Payload, &p); err != nil {
		r.t.Fatalf("decoding the started payload: %v", err)
	}
	return p
}

// fakeStore is the settings store, holding the values a case wrote.
type fakeStore struct {
	settings.Store
	mu     sync.Mutex
	values map[string]any
	errs   map[string]error
}

func newStore() *fakeStore {
	return &fakeStore{values: map[string]any{}, errs: map[string]error{}}
}

func (s *fakeStore) set(path string, v any) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.values[path] = v
}

func (s *fakeStore) fail(path string, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.errs[path] = err
}

func (s *fakeStore) Get(_ settings.Layer, key string, target any) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.errs[key]; err != nil {
		return err
	}
	if v, ok := s.values[key]; ok {
		reflect.ValueOf(target).Elem().Set(reflect.ValueOf(v))
		return nil
	}
	switch p := target.(type) {
	case *time.Duration:
		switch key {
		case maxTTLPath:
			*p = defaults.MaxTTL
		case maxAuthAgePath:
			*p = defaults.MaxAuthAge
		case accessTTLPath:
			*p = defaultTokenAccessTTL
		}
	}
	return nil
}

// fakeScopeGuard declares which endpoints the admission layer guards.
type fakeScopeGuard struct {
	covers map[string]bool
}

func (g fakeScopeGuard) Covers(endpoint string) bool { return g.covers[endpoint] }

// rig is the module over a real database with every dependency a case drives.
type rig struct {
	t           *testing.T
	p           *product
	h           *gorm.DB
	accounts    *fakeAccounts
	memberships *fakeMemberships
	mfa         *fakeMFA
	sessions    *fakeSessions
	audit       *recordingAudit
	decider     *fakeDecider
	tokens      *fakeTokens
	bus         *recordingBus
	store       *fakeStore
	logs        *logBuffer
	clock       *fakeClock
	mounter     *mounttest.Mounter
	guard       fakeScopeGuard
	metrics     *sdkmetric.ManualReader
}

// The identifiers and defaults a case starts from.
const (
	actorID   = authn.AccountID("acct-admin")
	targetID  = authn.AccountID("acct-user")
	tenantA   = "tenant-a"
	tenantB   = "tenant-b"
	sessionID = authn.SessionID("0190a6f2-3b6c-7c2e-8a4d-5e6f7a8b9c0d")
)

// newRig builds the module over the handle, with every dependency present and a
// healthy account, membership, session and factor. A case that wants one absent
// or failing sets it on the rig.
func newRig(t *testing.T, h *gorm.DB) *rig {
	t.Helper()
	accounts := newAccounts()
	accounts.put(actorID, authn.Account{Kind: authn.KindUser, Status: authn.StatusActive})
	accounts.put(targetID, authn.Account{Kind: authn.KindUser, Status: authn.StatusActive})
	memberships := newMemberships()
	memberships.set(targetID, tenantA, true)
	mfa := &fakeMFA{seasoned: true}
	sessions := newSessions(true, base.Add(2*time.Hour))
	guard := fakeScopeGuard{covers: map[string]bool{}}
	mounter := mounttest.New(t)

	p := build(h, accounts, memberships, mfa, sessions, &recordingAudit{}, guard, mounter)
	r := &rig{
		t: t, p: p, h: h,
		accounts: accounts, memberships: memberships, mfa: mfa, sessions: sessions,
		audit: &recordingAudit{}, decider: &fakeDecider{allow: true}, tokens: &fakeTokens{},
		bus: newBus(), store: newStore(), logs: &logBuffer{}, clock: newClock(base), mounter: mounter,
		guard: guard,
	}
	p.logger = r.logs.logger()
	p.settings = newReader(r.store, p.logger)
	p.audit = r.audit
	p.decider = r.decider
	p.tokens = r.tokens
	p.bus = r.bus
	p.clock = r.clock.read

	r.metrics = sdkmetric.NewManualReader()
	provider := sdkmetric.NewMeterProvider(sdkmetric.WithReader(r.metrics))
	t.Cleanup(func() { _ = provider.Shutdown(context.Background()) })
	meters, err := newMeters(provider)
	if err != nil {
		t.Fatalf("creating the instruments: %v", err)
	}
	p.meters = meters
	return r
}

// counter reads the value of the sessions counter for the outcome given.
func (r *rig) counter(outcome string) int64 {
	r.t.Helper()
	var rm metricdata.ResourceMetrics
	if err := r.metrics.Collect(context.Background(), &rm); err != nil {
		r.t.Fatalf("collecting the metrics: %v", err)
	}
	var total int64
	for _, sm := range rm.ScopeMetrics {
		for _, m := range sm.Metrics {
			if m.Name != sessionsInstrument {
				continue
			}
			sum, ok := m.Data.(metricdata.Sum[int64])
			if !ok {
				r.t.Fatalf("%s is not an int64 sum", sessionsInstrument)
			}
			for _, dp := range sum.DataPoints {
				if v, ok := dp.Attributes.Value(attribute.Key(outcomeAttribute)); ok && v.AsString() == outcome {
					total += dp.Value
				}
			}
		}
	}
	return total
}

// withAudit swaps in an audit implementation, for a case that drives a real one
// or one that fails.
func (r *rig) withAudit(a audit.Audit) { r.p.audit = a }

// adminOpts are the options of the administrator's own identity: a fresh local
// multi-factor authentication with a one-time code beside it, on their session.
func adminOpts(opts ...authntest.Option) []authntest.Option {
	return append([]authntest.Option{
		authntest.WithAccount(actorID),
		authntest.WithSession(sessionID),
		authntest.WithMethods(authn.MethodPassword, authn.MethodTOTP),
		authntest.WithAssurance(authn.AssuranceMulti),
		authntest.WithAuthTime(base),
		authntest.WithBackend(sessionBackend),
	}, opts...)
}

// adminIdentity is the identity the options above build.
func adminIdentity(t *testing.T, opts ...authntest.Option) authn.Identity {
	t.Helper()
	ctx := authntest.Context(t, context.Background(), adminOpts(opts...)...)
	id, ok := authn.FromContext(ctx)
	if !ok {
		t.Fatal("authntest built no identity")
	}
	return id
}

// impersonatingIdentity is the identity of an administrator acting as the target
// inside a tenant, as a verifying backend would stamp it.
func impersonatingIdentity(t *testing.T, backend string) authn.Identity {
	t.Helper()
	ctx := authntest.Context(t, context.Background(), impersonatingOpts(backend)...)
	id, ok := authn.FromContext(ctx)
	if !ok {
		t.Fatal("authntest built no impersonating identity")
	}
	return id
}

// request is a well-formed start request against the target in tenant A.
func request() authn.ImpersonationRequest {
	return authn.ImpersonationRequest{Target: targetID, Tenant: tenantA, Reason: "a support call"}
}

// recordCount reads how many rows of the record table a case has.
func (r *rig) recordCount() int {
	r.t.Helper()
	var n int64
	if err := r.h.Raw("SELECT count(*) FROM " + grantTable).Scan(&n).Error; err != nil {
		r.t.Fatalf("counting records: %v", err)
	}
	return int(n)
}

// stored reads one record's end columns from the table.
func (r *rig) stored(id string) (endedAt *time.Time, endedBy *string, endReason *string) {
	r.t.Helper()
	var rows []struct {
		EndedAt   *time.Time
		EndedBy   *string
		EndReason *string
	}
	if err := r.h.Raw("SELECT ended_at, ended_by, end_reason FROM "+grantTable+" WHERE id = ?", id).Scan(&rows).Error; err != nil {
		r.t.Fatalf("reading a record: %v", err)
	}
	if len(rows) == 0 {
		r.t.Fatalf("no record %s", id)
	}
	return rows[0].EndedAt, rows[0].EndedBy, rows[0].EndReason
}

// applyMigrations builds the module's table from the files it ships.
func applyMigrations(t *testing.T, h *gorm.DB, dialect string) {
	t.Helper()
	entries, err := fs.ReadDir(migrationsFS, dialect)
	if err != nil {
		t.Fatalf("listing the %s migrations: %v", dialect, err)
	}
	for _, entry := range entries {
		sql, err := fs.ReadFile(migrationsFS, dialect+"/"+entry.Name())
		if err != nil {
			t.Fatalf("reading %s: %v", entry.Name(), err)
		}
		for _, statement := range strings.Split(string(sql), ";") {
			if strings.TrimSpace(statement) == "" {
				continue
			}
			if err := h.Exec(statement).Error; err != nil {
				t.Fatalf("applying %s: %v", entry.Name(), err)
			}
		}
	}
}

// openDialect delivers the database of the dialect with the module's table built.
func openDialect(t *testing.T, dialect string) *gorm.DB {
	t.Helper()
	var h *gorm.DB
	switch dialect {
	case sqliteDialect:
		h = dbtest.SQLite(t, filepath.Join(t.TempDir(), "impersonation.db"), nil).DB()
	case postgresDialect:
		h = dbtest.Postgres(t, pgtest.Acquire(t), nil).DB()
	default:
		t.Fatalf("no dialect %q", dialect)
	}
	h = h.Session(&gorm.Session{Logger: logger.Discard})
	applyMigrations(t, h, dialect)
	return h
}

// forEachDialect runs a case on SQLite and on PostgreSQL. The PostgreSQL leg is
// skipped on a machine without docker, as every case on that engine is.
func forEachDialect(t *testing.T, run func(t *testing.T, h *gorm.DB, dialect string)) {
	t.Helper()
	for _, dialect := range []string{sqliteDialect, postgresDialect} {
		t.Run(dialect, func(t *testing.T) {
			run(t, openDialect(t, dialect), dialect)
		})
	}
}

// onEachDialect is forEachDialect with the rig built.
func onEachDialect(t *testing.T, run func(t *testing.T, r *rig)) {
	t.Helper()
	forEachDialect(t, func(t *testing.T, h *gorm.DB, _ string) {
		run(t, newRig(t, h))
	})
}

// errFromStore is a dependency failure a case injects.
var errFromStore = errors.New("stand-in store failure")
