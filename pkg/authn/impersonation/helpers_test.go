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
	"github.com/vislake/speed/pkg/settings"
)

// base is the instant the cases that need a fixed time start from.
var base = time.Date(2026, time.March, 14, 12, 0, 0, 0, time.UTC)

// sqliteDialect is the engine every case of this build runs on.
const sqliteDialect = "sqlite"

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
	errs     map[authn.AccountID]error
	reads    int
}

func newAccounts() *fakeAccounts { return &fakeAccounts{accounts: map[authn.AccountID]authn.Account{}} }

// failFor makes the read of one account fail, so that a case can fail the
// target's read while the actor's still answers.
func (a *fakeAccounts) failFor(id authn.AccountID, err error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.errs == nil {
		a.errs = map[authn.AccountID]error{}
	}
	a.errs[id] = err
}

// readCount is how many account reads the store has answered.
func (a *fakeAccounts) readCount() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.reads
}

// resetReads clears the read count, so that a case can count one call alone.
func (a *fakeAccounts) resetReads() {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.reads = 0
}

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
	a.reads++
	if a.err != nil {
		return authn.Account{}, a.err
	}
	if err := a.errs[id]; err != nil {
		return authn.Account{}, err
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
	reads   int
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
	f.reads++
	if f.err != nil {
		return false, f.err
	}
	return f.members[memberKey(id, tenant)], nil
}

// readCount is how many membership reads the store has answered.
func (f *fakeMemberships) readCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.reads
}

// resetReads clears the read count, so that a case can count one call alone.
func (f *fakeMemberships) resetReads() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.reads = 0
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
	// onStatus, when set, runs after a status read and before it returns, so a
	// case can park concurrent readers at the same point.
	onStatus func()

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
	return s.readStatus()
}

// StatusFresh reads the same store as Status: this fake holds no cache, so the
// two answer alike. A case that needs the cache's staleness serves Status from
// cachingSessions instead.
func (s *fakeSessions) StatusFresh(_ context.Context, _ authn.SessionID) (authn.SessionStatus, error) {
	return s.readStatus()
}

// readStatus takes the store's answer under the lock and hands it back, running
// the case's hook outside the lock.
func (s *fakeSessions) readStatus() (authn.SessionStatus, error) {
	s.mu.Lock()
	status, err := s.session, s.statusErr
	onStatus := s.onStatus
	s.mu.Unlock()
	if onStatus != nil {
		onStatus()
	}
	if err != nil {
		return authn.SessionStatus{}, err
	}
	return status, nil
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

// cachingSessions serves Status from a per-session entry until check-ttl runs
// out, as the session module does, and reads the store for StatusFresh. A case
// uses it to show that a path does not judge through the cache: it warms an
// entry with Status, changes the store, and expects the path to see the change.
type cachingSessions struct {
	*fakeSessions
	mu      sync.Mutex
	ttl     time.Duration
	now     func() time.Time
	entries map[authn.SessionID]cachedStatus
}

// cachedStatus is one cached answer and the instant it stops being served.
type cachedStatus struct {
	status  authn.SessionStatus
	expires time.Time
}

func newCachingSessions(s *fakeSessions, ttl time.Duration, now func() time.Time) *cachingSessions {
	return &cachingSessions{fakeSessions: s, ttl: ttl, now: now, entries: map[authn.SessionID]cachedStatus{}}
}

func (c *cachingSessions) Status(ctx context.Context, id authn.SessionID) (authn.SessionStatus, error) {
	c.mu.Lock()
	if entry, ok := c.entries[id]; ok && c.now().Before(entry.expires) {
		c.mu.Unlock()
		return entry.status, nil
	}
	c.mu.Unlock()

	status, err := c.fakeSessions.Status(ctx, id)
	if err != nil {
		return authn.SessionStatus{}, err
	}
	c.mu.Lock()
	c.entries[id] = cachedStatus{status: status, expires: c.now().Add(c.ttl)}
	c.mu.Unlock()
	return status, nil
}

// StatusFresh is the store's answer, as the session module's is.
func (c *cachingSessions) StatusFresh(ctx context.Context, id authn.SessionID) (authn.SessionStatus, error) {
	return c.fakeSessions.StatusFresh(ctx, id)
}

// perSessionSessions answers only for the sessions a case names. Any other
// identifier is ErrSessionNotFound, as the store answers for a session that is
// not there, so a record judged on the wrong session is caught rather than
// answered from a single shared status.
type perSessionSessions struct {
	authn.Sessions
	mu    sync.Mutex
	alive map[authn.SessionID]bool
	asked []authn.SessionID
}

// newPerSessionSessions builds a store in which each named session is alive.
func newPerSessionSessions(ids ...authn.SessionID) *perSessionSessions {
	s := &perSessionSessions{alive: map[authn.SessionID]bool{}}
	for _, id := range ids {
		s.alive[id] = true
	}
	return s
}

// setActive changes whether one named session is alive.
func (s *perSessionSessions) setActive(id authn.SessionID, active bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.alive[id] = active
}

func (s *perSessionSessions) Status(_ context.Context, id authn.SessionID) (authn.SessionStatus, error) {
	return s.answer(id)
}

func (s *perSessionSessions) StatusFresh(_ context.Context, id authn.SessionID) (authn.SessionStatus, error) {
	return s.answer(id)
}

// askedIDs is every identifier a read named, in the order it was read.
func (s *perSessionSessions) askedIDs() []authn.SessionID {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]authn.SessionID(nil), s.asked...)
}

func (s *perSessionSessions) answer(id authn.SessionID) (authn.SessionStatus, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.asked = append(s.asked, id)
	active, ok := s.alive[id]
	if !ok {
		return authn.SessionStatus{}, authn.ErrSessionNotFound
	}
	return authn.SessionStatus{
		Active: active, Account: actorID, Kind: authn.SessionBrowser,
		Methods:   []authn.Method{authn.MethodPassword, authn.MethodTOTP},
		Assurance: authn.AssuranceMulti, AuthTime: base, ExpiresAt: base.Add(2 * time.Hour),
	}, nil
}

// replica builds a second product over the same dependencies: another replica of
// the module over one store, for a case that races two of them.
func (r *rig) replica() *product {
	p := build(r.h, r.accounts, r.memberships, r.mfa, r.sessions, r.audit, r.guard, r.mounter)
	p.logger = r.logs.logger()
	p.settings = r.p.settings
	p.bus = r.bus
	p.meters = r.p.meters
	p.clock = r.clock.read
	return p
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
	mu    sync.Mutex
	allow bool
	err   error
	panic bool
	calls []authn.Decision
}

func (d *fakeDecider) Decide(_ context.Context, dec authn.Decision) (bool, error) {
	d.mu.Lock()
	d.calls = append(d.calls, dec)
	allow, err, panics := d.allow, d.err, d.panic
	d.mu.Unlock()
	if panics {
		panic("the decider panicked")
	}
	return allow, err
}

// fakeTokens signs a token a case reads back.
type fakeTokens struct {
	authn.Tokens
	mu      sync.Mutex
	err     error
	issued  []authn.IssueToken
	expires time.Time
}

func (f *fakeTokens) Issue(_ context.Context, in authn.IssueToken) (authn.AccessToken, error) {
	f.mu.Lock()
	f.issued = append(f.issued, in)
	err, expires := f.err, f.expires
	f.mu.Unlock()
	if err != nil {
		return authn.AccessToken{}, err
	}
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
	target2ID = authn.AccountID("acct-user-2")
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

// reasonText renders a stored end reason for a failure message. The column is
// scanned as a pointer, and %v on it prints the address rather than the value.
func reasonText(p *string) string {
	if p == nil {
		return "<nil>"
	}
	return *p
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

// dialect is one engine a case iterates: its name, and how to reach a database of
// the case's own on it.
type dialect struct {
	name string
	open func(t *testing.T) *gorm.DB
}

// dialects lists the engines a case iterates, SQLite first.
//
// Only the engines whose fixture needs nothing installed belong here. The
// integration build appends the ones that reach a server, so an untagged run
// cannot name a dialect whose opener would fail for want of one.
var dialects = []dialect{{
	name: sqliteDialect,
	open: func(t *testing.T) *gorm.DB {
		return dbtest.SQLite(t, filepath.Join(t.TempDir(), "impersonation.db"), nil).DB()
	},
}}

// openDialect delivers the database of the dialect with the module's table built.
//
// A dialect no engine of this build answers to is a programming error, since
// dialects never names one.
func openDialect(t *testing.T, dialect string) *gorm.DB {
	t.Helper()
	for _, d := range dialects {
		if d.name == dialect {
			h := d.open(t).Session(&gorm.Session{Logger: logger.Discard})
			applyMigrations(t, h, dialect)
			return h
		}
	}
	t.Fatalf("no dialect %q", dialect)
	return nil
}

// forEachDialect runs a case on every engine this build's cases run on.
func forEachDialect(t *testing.T, run func(t *testing.T, h *gorm.DB, dialect string)) {
	t.Helper()
	for _, d := range dialects {
		t.Run(d.name, func(t *testing.T) {
			run(t, openDialect(t, d.name), d.name)
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

// blockEndWrites makes the conditional end update fail, as a database that reads
// but cannot write would. The trigger is written for SQLite; another dialect
// skips the case.
func (r *rig) blockEndWrites(t *testing.T, dialect string) {
	t.Helper()
	if dialect != sqliteDialect {
		t.Skipf("the trigger that blocks the end update is written for %s, not %s", sqliteDialect, dialect)
	}
	err := r.h.Exec("CREATE TRIGGER block_end BEFORE UPDATE ON " + grantTable +
		" BEGIN SELECT RAISE(ABORT, 'write blocked'); END").Error
	if err != nil {
		t.Fatalf("blocking the end update: %v", err)
	}
}

// withinASecond reports whether two instants name the same second, which is the
// tolerance a stored time is compared with after a round trip.
func withinASecond(a, b time.Time) bool {
	d := a.Sub(b)
	return d <= time.Second && d >= -time.Second
}
