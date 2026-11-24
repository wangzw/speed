package mfa

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"net/http"
	"path/filepath"
	"reflect"
	"slices"
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
	"github.com/vislake/speed/pkg/authn/internal/mount/mounttest"
	"github.com/vislake/speed/pkg/event"
	"github.com/vislake/speed/pkg/internal/dbtest"
	"github.com/vislake/speed/pkg/settings"
)

var base = time.Date(2026, time.March, 14, 12, 0, 0, 0, time.UTC)

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

type logBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *logBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *logBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

func (b *logBuffer) logger() *slog.Logger {
	return slog.New(slog.NewTextHandler(b, &slog.HandlerOptions{Level: slog.LevelDebug}))
}

func (b *logBuffer) lines(substrings ...string) []string {
	var out []string
	for _, line := range strings.Split(b.String(), "\n") {
		ok := line != ""
		for _, s := range substrings {
			ok = ok && strings.Contains(line, s)
		}
		if ok {
			out = append(out, line)
		}
	}
	return out
}

type fakeThrottle struct {
	mu           sync.Mutex
	waits        map[string]time.Duration
	failures     map[string]int
	freeAttempts int
	checkErr     error
	failErr      error
	succeedErr   error
	calls        []string
}

func newThrottle() *fakeThrottle {
	return &fakeThrottle{waits: map[string]time.Duration{}, failures: map[string]int{}, freeAttempts: 3}
}

func (f *fakeThrottle) Check(_ context.Context, scope authn.ThrottleScope, key string) (time.Duration, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, "check:"+string(scope)+":"+key)
	if f.checkErr != nil {
		return 0, f.checkErr
	}
	return f.waits[key], nil
}

func (f *fakeThrottle) Fail(_ context.Context, scope authn.ThrottleScope, key string) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, "fail:"+string(scope)+":"+key)
	if f.failErr != nil {
		return false, f.failErr
	}
	f.failures[key]++
	if f.failures[key] == f.freeAttempts {
		f.waits[key] = time.Minute
		return true, nil
	}
	return false, nil
}

func (f *fakeThrottle) Succeed(_ context.Context, scope authn.ThrottleScope, key string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, "succeed:"+string(scope)+":"+key)
	if f.succeedErr != nil {
		return f.succeedErr
	}
	delete(f.failures, key)
	delete(f.waits, key)
	return nil
}

func (f *fakeThrottle) callList() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.calls...)
}

func (f *fakeThrottle) count(prefix string) int {
	n := 0
	for _, c := range f.callList() {
		if strings.HasPrefix(c, prefix) {
			n++
		}
	}
	return n
}

type fakeAccounts struct {
	authn.Accounts
	mu        sync.Mutex
	required  map[authn.AccountID]bool
	passwords map[authn.AccountID]bool
	getErr    error
	gets      int
}

func (a *fakeAccounts) Get(_ context.Context, id authn.AccountID) (authn.Account, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.gets++
	if a.getErr != nil {
		return authn.Account{}, a.getErr
	}
	return authn.Account{ID: id, Status: authn.StatusActive, MFARequired: a.required[id], PasswordSet: a.passwords[id]}, nil
}

type fakeAdmin struct {
	authn.AccountAdmin
	accounts *fakeAccounts
	mu       sync.Mutex
	err      error
	calls    []string
	onSet    func()
}

func (a *fakeAdmin) SetMFARequired(_ context.Context, id authn.AccountID, required bool) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.calls = append(a.calls, fmt.Sprintf("set-mfa-required:%s:%v", id, required))
	if a.onSet != nil {
		a.onSet()
	}
	if a.err != nil {
		return a.err
	}
	a.accounts.mu.Lock()
	a.accounts.required[id] = required
	a.accounts.mu.Unlock()
	return nil
}

type fakeSessions struct {
	authn.Sessions
	mu        sync.Mutex
	err       error
	rotateErr error
	revoked   []string
	rotated   []authn.SessionID
	live      map[authn.SessionID]authn.AccountID
	tokens    map[authn.SessionID]string
	onRevoke  func()
}

func newFakeSessions() *fakeSessions {
	return &fakeSessions{
		live:   map[authn.SessionID]authn.AccountID{},
		tokens: map[authn.SessionID]string{},
	}
}

func (s *fakeSessions) add(id authn.SessionID, account authn.AccountID) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.live[id] = account
	s.tokens[id] = "original-token-of-" + string(id)
}

func (s *fakeSessions) tokenOf(id authn.SessionID) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.tokens[id]
}

func (s *fakeSessions) authenticates(token string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	for id, held := range s.tokens {
		if held != token {
			continue
		}
		_, live := s.live[id]
		return live
	}
	return false
}

func (s *fakeSessions) rotationCalls() []authn.SessionID {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]authn.SessionID(nil), s.rotated...)
}

// Rotate gives a live session a new token, as the store's conditional update
// does, and records the call. A session that is not live is ErrSessionNotFound.
func (s *fakeSessions) Rotate(_ context.Context, id authn.SessionID) (authn.Issued, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.rotated = append(s.rotated, id)
	if s.rotateErr != nil {
		return authn.Issued{}, s.rotateErr
	}
	if _, ok := s.live[id]; !ok {
		return authn.Issued{}, authn.ErrSessionNotFound
	}
	token := "rotated-token-of-" + string(id)
	s.tokens[id] = token
	return authn.Issued{ID: id, Token: token}, nil
}

// SetCookie writes the cookie of an issued token the way the session module
// does, so a case can read it back from the response.
func (s *fakeSessions) SetCookie(w http.ResponseWriter, in authn.Issued) {
	if in.Token == "" {
		return
	}
	http.SetCookie(w, &http.Cookie{Name: "sid", Value: in.Token, Path: "/"})
}

func (s *fakeSessions) isLive(id authn.SessionID) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, ok := s.live[id]
	return ok
}

func (s *fakeSessions) revocations() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.revoked...)
}

func (s *fakeSessions) RevokeAccount(_ context.Context, account authn.AccountID, except authn.SessionID, reason string) ([]authn.SessionID, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.revoked = append(s.revoked, fmt.Sprintf("revoke-account:%s:except=%q:%s", account, except, reason))
	if s.onRevoke != nil {
		s.onRevoke()
	}
	if s.err != nil {
		return nil, s.err
	}
	var ended []authn.SessionID
	for id, acct := range s.live {
		if acct != account || id == except {
			continue
		}
		ended = append(ended, id)
		delete(s.live, id)
	}
	return ended, nil
}

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
	b.published = append(b.published, evt)
	err := b.err
	b.mu.Unlock()
	return err
}

func (b *recordingBus) deliver(ctx context.Context, evt event.Event) error {
	b.mu.Lock()
	hs := append([]event.Handler(nil), b.handlers[evt.Type]...)
	b.mu.Unlock()
	var errs []error
	for _, h := range hs {
		errs = append(errs, h(ctx, evt))
	}
	return errors.Join(errs...)
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

func (b *recordingBus) payload(t *testing.T, typ string, dst any) {
	t.Helper()
	got := b.ofType(typ)
	if len(got) != 1 {
		t.Fatalf("%d events of type %s were published, want 1", len(got), typ)
	}
	if err := event.DecodePayload(got[0].Payload, dst); err != nil {
		t.Fatalf("decoding the %s payload: %v", typ, err)
	}
}

type recordingAudit struct {
	mu   sync.Mutex
	rows []audit.Entry
	err  error
}

func (a *recordingAudit) Record(_ context.Context, e audit.Entry) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.rows = append(a.rows, e)
	return a.err
}

func (a *recordingAudit) entries() []audit.Entry {
	a.mu.Lock()
	defer a.mu.Unlock()
	return append([]audit.Entry(nil), a.rows...)
}

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
	case *bool:
		*p = defaults.GlobalRequired
	case *time.Duration:
		switch key {
		case recentWindowPath:
			*p = defaults.RecentWindow
		case pendingTTLPath:
			*p = defaults.PendingTTL
		case sensitiveMinAgePath:
			*p = defaults.SensitiveMinAge
		}
	case *int:
		*p = defaults.MaxPerKind
	}
	return nil
}

type fakeFactor struct {
	kind authn.FactorKind

	mu          sync.Mutex
	enrollFn    func(ctx context.Context, f authn.FlowState, in authn.StepInput) (authn.Step, error)
	challengeFn func(ctx context.Context, f authn.FlowState, rec authn.FactorRecord) (authn.Step, error)
	verifyFn    func(ctx context.Context, f authn.FlowState, rec authn.FactorRecord, in authn.StepInput) (authn.Step, error)
	removeFn    func(ctx context.Context, account authn.AccountID, rec authn.FactorRecord) error

	calls   []string
	removed []string
	seen    []enrollSight
}

type enrollSight struct {
	record  authn.FactorRecord
	hadRow  bool
	private []byte
}

func newFactor(kind authn.FactorKind) *fakeFactor { return &fakeFactor{kind: kind} }

func (f *fakeFactor) Kind() authn.FactorKind { return f.kind }

func (f *fakeFactor) record(call string) {
	f.mu.Lock()
	f.calls = append(f.calls, call)
	f.mu.Unlock()
}

func (f *fakeFactor) callList() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.calls...)
}

func (f *fakeFactor) count(call string) int {
	n := 0
	for _, c := range f.callList() {
		if c == call {
			n++
		}
	}
	return n
}

func (f *fakeFactor) Enroll(ctx context.Context, fs authn.FlowState, in authn.StepInput) (authn.Step, error) {
	f.record("enroll")
	rec, ok := PendingFactor(ctx)
	f.mu.Lock()
	f.seen = append(f.seen, enrollSight{record: rec, hadRow: ok, private: append([]byte(nil), fs.Private...)})
	fn := f.enrollFn
	f.mu.Unlock()
	if fn != nil {
		return fn(ctx, fs, in)
	}
	return authn.StepEnrolled(rec), nil
}

func (f *fakeFactor) Challenge(ctx context.Context, fs authn.FlowState, rec authn.FactorRecord) (authn.Step, error) {
	f.record("challenge")
	f.mu.Lock()
	fn := f.challengeFn
	f.mu.Unlock()
	if fn != nil {
		return fn(ctx, fs, rec)
	}
	return authn.StepPrompt("code", nil, authn.Field{Name: "code", Kind: authn.FieldCode, Required: true}), nil
}

func (f *fakeFactor) Verify(ctx context.Context, fs authn.FlowState, rec authn.FactorRecord, in authn.StepInput) (authn.Step, error) {
	f.record("verify")
	f.mu.Lock()
	fn := f.verifyFn
	f.mu.Unlock()
	if fn != nil {
		return fn(ctx, fs, rec, in)
	}
	return authn.StepPassed(authn.Pass{Methods: methodsOf(f.kind)}), nil
}

func (f *fakeFactor) Remove(ctx context.Context, account authn.AccountID, rec authn.FactorRecord) error {
	f.record("remove")
	f.mu.Lock()
	f.removed = append(f.removed, rec.ID)
	fn := f.removeFn
	f.mu.Unlock()
	if fn != nil {
		return fn(ctx, account, rec)
	}
	return nil
}

type rig struct {
	t        *testing.T
	p        *product
	h        *gorm.DB
	accounts *fakeAccounts
	admin    *fakeAdmin
	throttle *fakeThrottle
	sessions *fakeSessions
	bus      *recordingBus
	audit    *recordingAudit
	store    *fakeStore
	logs     *logBuffer
	clock    *fakeClock
	metrics  *sdkmetric.ManualReader
	mounter  *mounttest.Mounter
}

func newRig(t *testing.T, h *gorm.DB, factors ...authn.Factor) *rig {
	t.Helper()
	accounts := &fakeAccounts{required: map[authn.AccountID]bool{}, passwords: map[authn.AccountID]bool{}}
	admin := &fakeAdmin{accounts: accounts}
	throttle := newThrottle()
	mounter := mounttest.New(t)
	p := build(h, accounts, admin, throttle, mounter)
	r := &rig{
		t: t, p: p, h: h, accounts: accounts, admin: admin, throttle: throttle,
		sessions: newFakeSessions(), bus: newBus(), audit: &recordingAudit{}, store: newStore(),
		logs: &logBuffer{}, clock: newClock(base), mounter: mounter,
	}
	p.logger = r.logs.logger()
	p.settings = newReader(r.store, p.logger)
	p.sessions = r.sessions
	p.audit = r.audit
	p.bus = r.bus
	p.clock = r.clock.read
	p.subscribe(r.bus)

	r.metrics = sdkmetric.NewManualReader()
	provider := sdkmetric.NewMeterProvider(sdkmetric.WithReader(r.metrics))
	t.Cleanup(func() { _ = provider.Shutdown(context.Background()) })
	meters, err := newMeters(provider)
	if err != nil {
		t.Fatalf("creating the instruments: %v", err)
	}
	p.meters = meters

	all := make([]authn.Factor, len(factors))
	copy(all, factors)
	if err := p.takeFactors(all); err != nil {
		t.Fatalf("taking in the factors: %v", err)
	}
	return r
}

func flow(account authn.AccountID) authn.FlowState {
	return authn.FlowState{ID: "flow-1", Purpose: authn.PurposeEnroll, Account: account}
}

func (r *rig) insertRow(account authn.AccountID, kind authn.FactorKind, status authn.FactorStatus, created time.Time, activated *time.Time) string {
	r.t.Helper()
	id := event.NewID()
	err := r.h.Exec("INSERT INTO authn_factors (id, account_id, kind, label, status, created_at, activated_at) VALUES (?, ?, ?, '', ?, ?, ?)",
		id, string(account), string(kind), int(status), created.UTC(), utcPtr(activated)).Error
	if err != nil {
		r.t.Fatalf("inserting a factor row: %v", err)
	}
	return id
}

func utcPtr(t *time.Time) any {
	if t == nil {
		return nil
	}
	u := t.UTC()
	return u
}

func (r *rig) activeFactor(account authn.AccountID, kind authn.FactorKind, activatedAgo time.Duration) string {
	r.t.Helper()
	at := r.clock.read().Add(-activatedAgo)
	return r.insertRow(account, kind, authn.FactorActive, at.Add(-time.Minute), &at)
}

func (r *rig) rows(account authn.AccountID) []authn.FactorRecord {
	r.t.Helper()
	recs, err := r.p.Factors(context.Background(), account)
	if err != nil {
		r.t.Fatalf("Factors: %v", err)
	}
	return recs
}

func (r *rig) status(id string) (authn.FactorStatus, bool) {
	r.t.Helper()
	var rows []struct{ Status int }
	if err := r.h.Raw("SELECT status FROM authn_factors WHERE id = ?", id).Scan(&rows).Error; err != nil {
		r.t.Fatalf("reading a status: %v", err)
	}
	if len(rows) == 0 {
		return 0, false
	}
	return authn.FactorStatus(rows[0].Status), true
}

func (r *rig) counter(name string, want ...attribute.KeyValue) int64 {
	r.t.Helper()
	var rm metricdata.ResourceMetrics
	if err := r.metrics.Collect(context.Background(), &rm); err != nil {
		r.t.Fatalf("collecting the metrics: %v", err)
	}
	var total int64
	for _, sm := range rm.ScopeMetrics {
		for _, m := range sm.Metrics {
			if m.Name != name {
				continue
			}
			sum, ok := m.Data.(metricdata.Sum[int64])
			if !ok {
				r.t.Fatalf("%s is not an int64 sum", name)
			}
			for _, dp := range sum.DataPoints {
				match := true
				for _, kv := range want {
					if v, ok := dp.Attributes.Value(kv.Key); !ok || v != kv.Value {
						match = false
					}
				}
				if match {
					total += dp.Value
				}
			}
		}
	}
	return total
}

const sqliteDialect = "sqlite"

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

type dialect struct {
	name string
	open func(t *testing.T) *gorm.DB
}

var dialects = []dialect{{
	name: sqliteDialect,
	open: func(t *testing.T) *gorm.DB {
		return dbtest.SQLite(t, filepath.Join(t.TempDir(), "mfa.db"), nil).DB()
	},
}}

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

func forEachDialect(t *testing.T, run func(t *testing.T, h *gorm.DB, dialect string)) {
	t.Helper()
	for _, d := range dialects {
		t.Run(d.name, func(t *testing.T) {
			run(t, openDialect(t, d.name), d.name)
		})
	}
}

func onEachDialect(t *testing.T, kinds []authn.FactorKind, run func(t *testing.T, r *rig)) {
	t.Helper()
	forEachDialect(t, func(t *testing.T, h *gorm.DB, _ string) {
		factors := make([]authn.Factor, len(kinds))
		for i, k := range kinds {
			factors[i] = newFactor(k)
		}
		run(t, newRig(t, h, factors...))
	})
}

const acct = authn.AccountID("acct-1")

func decode(payload, dst any) error { return event.DecodePayload(payload, dst) }

func (b *recordingBus) subscribedTypes() []string {
	b.mu.Lock()
	defer b.mu.Unlock()
	out := make([]string, 0, len(b.handlers))
	for typ := range b.handlers {
		out = append(out, typ)
	}
	slices.Sort(out)
	return out
}
