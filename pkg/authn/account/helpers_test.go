package account

import (
	"bytes"
	"context"
	"fmt"
	"io/fs"
	"log/slog"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"go.opentelemetry.io/otel/attribute"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"

	"github.com/vislake/speed/pkg/audit"
	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/authn/internal/password"
	"github.com/vislake/speed/pkg/db"
	"github.com/vislake/speed/pkg/event"
	"github.com/vislake/speed/pkg/internal/dbtest"
	"github.com/vislake/speed/pkg/settings"
)

type stubDatabase struct {
	handle  *gorm.DB
	dialect db.Dialect
}

func (d stubDatabase) DB() *gorm.DB                                   { return d.handle }
func (d stubDatabase) Dialect() db.Dialect                            { return d.dialect }
func (d stubDatabase) BlindIndex(string) string                       { return "" }
func (d stubDatabase) Open(context.Context, string) (*gorm.DB, error) { return nil, nil }

func quietly(handle *gorm.DB) *gorm.DB {
	return handle.Session(&gorm.Session{Logger: logger.Default.LogMode(logger.Silent)})
}

func applyMigrations(t testing.TB, handle *gorm.DB, dialect db.Dialect) {
	t.Helper()
	entries, err := fs.ReadDir(migrationsFS, string(dialect))
	if err != nil {
		t.Fatalf("listing the %s migrations: %v", dialect, err)
	}
	for _, entry := range entries {
		text, err := fs.ReadFile(migrationsFS, string(dialect)+"/"+entry.Name())
		if err != nil {
			t.Fatalf("reading %s: %v", entry.Name(), err)
		}
		for statement := range strings.SplitSeq(string(text), ";") {
			if strings.TrimSpace(statement) == "" {
				continue
			}
			if err := handle.Exec(statement).Error; err != nil {
				t.Fatalf("applying %s: %v", entry.Name(), err)
			}
		}
	}
}

func applyMigrationFile(t testing.TB, handle *gorm.DB, dialect db.Dialect, name string) {
	t.Helper()
	text, err := fs.ReadFile(migrationsFS, string(dialect)+"/"+name)
	if err != nil {
		t.Fatalf("reading %s: %v", name, err)
	}
	for statement := range strings.SplitSeq(string(text), ";") {
		if strings.TrimSpace(statement) == "" {
			continue
		}
		if err := handle.Exec(statement).Error; err != nil {
			t.Fatalf("applying %s: %v", name, err)
		}
	}
}

var dialectsUnderTest = []db.Dialect{db.SQLite}

var databaseOpeners = map[db.Dialect]func(*testing.T) db.Database{
	db.SQLite: func(t *testing.T) db.Database {
		return dbtest.SQLite(t, filepath.Join(t.TempDir(), "account.db"), nil)
	},
}

func openDatabase(t *testing.T, dialect db.Dialect) *gorm.DB {
	t.Helper()
	open, ok := databaseOpeners[dialect]
	if !ok {
		t.Fatalf("account: this build runs no case on the %s engine", dialect)
	}
	handle := quietly(open(t).DB())
	applyMigrations(t, handle, dialect)
	return handle
}

func forEachDialect(t *testing.T, run func(t *testing.T, h *harness)) {
	t.Helper()
	for _, dialect := range dialectsUnderTest {
		t.Run(string(dialect), func(t *testing.T) {
			run(t, newHarness(t, dialect))
		})
	}
}

type testClock struct {
	mu  sync.Mutex
	now time.Time
}

// The instant is far from the real one, so a statement that read the real clock disagrees with the rows.
func newTestClock() *testClock {
	return &testClock{now: time.Date(2031, 3, 4, 12, 0, 0, 0, time.UTC)}
}

func (c *testClock) read() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *testClock) advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

type logBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (l *logBuffer) Write(b []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.buf.Write(b)
}

func (l *logBuffer) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.buf.String()
}

func (l *logBuffer) lines(fragments ...string) []string {
	var out []string
	for _, line := range strings.Split(l.String(), "\n") {
		matches := line != ""
		for _, f := range fragments {
			matches = matches && strings.Contains(line, f)
		}
		if matches {
			out = append(out, line)
		}
	}
	return out
}

type recordingBus struct {
	mu       sync.Mutex
	events   []event.Event
	handlers map[string][]event.Handler
	failWith error
}

func (b *recordingBus) Publish(_ context.Context, evt event.Event) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.failWith != nil {
		return b.failWith
	}
	b.events = append(b.events, evt)
	return nil
}

func (b *recordingBus) Subscribe(eventType string, _ string, h event.Handler) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.handlers == nil {
		b.handlers = map[string][]event.Handler{}
	}
	b.handlers[eventType] = append(b.handlers[eventType], h)
}

func (b *recordingBus) of(eventType string) []event.Event {
	b.mu.Lock()
	defer b.mu.Unlock()
	var out []event.Event
	for _, e := range b.events {
		if e.Type == eventType {
			out = append(out, e)
		}
	}
	return out
}

type stagingOutbox struct {
	fail error
}

func (o *stagingOutbox) Stage(tx *gorm.DB, evt event.Event) error {
	if _, inTransaction := tx.Statement.ConnPool.(gorm.TxCommitter); !inTransaction {
		return fmt.Errorf("the session is not in a transaction")
	}
	if o.fail != nil {
		return o.fail
	}
	return tx.Exec("INSERT INTO test_staged (type, tenant_id, payload) VALUES (?, ?, ?)",
		evt.Type, evt.TenantID, fmt.Sprintf("%+v", evt.Payload)).Error
}

type stagedRow struct {
	Type, TenantID, Payload string
}

func (h *harness) staged() []stagedRow {
	h.t.Helper()
	var rows []stagedRow
	if err := h.handle.Raw("SELECT type, tenant_id, payload FROM test_staged ORDER BY id").Scan(&rows).Error; err != nil {
		h.t.Fatalf("reading the staged events: %v", err)
	}
	return rows
}

type recordingHook struct {
	mu       sync.Mutex
	calls    []string
	failWith map[string]error
}

func (h *recordingHook) record(call string) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.calls = append(h.calls, call)
	return h.failWith[call]
}

func (h *recordingHook) AccountCreated(_ context.Context, id authn.AccountID) error {
	return h.record("created:" + id.String())
}

func (h *recordingHook) AccountDisabled(_ context.Context, id authn.AccountID) error {
	return h.record("disabled:" + id.String())
}

func (h *recordingHook) AccountEnabled(_ context.Context, id authn.AccountID) error {
	return h.record("enabled:" + id.String())
}

func (h *recordingHook) AccountDeleted(_ context.Context, id authn.AccountID) error {
	return h.record("deleted:" + id.String())
}

func (h *recordingHook) ServiceSecretRevoked(_ context.Context, id authn.AccountID, credential string) error {
	return h.record("revoked:" + id.String() + ":" + credential)
}

func (h *recordingHook) reset() {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.calls = nil
}

func (h *recordingHook) seen() []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]string(nil), h.calls...)
}

func (h *recordingHook) fail(call string, err error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.failWith == nil {
		h.failWith = map[string]error{}
	}
	if err == nil {
		delete(h.failWith, call)
		return
	}
	h.failWith[call] = err
}

type fakeSettings struct {
	mu     sync.Mutex
	values map[string]any
	errs   map[string]error
}

func (s *fakeSettings) set(key string, value any) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.values == nil {
		s.values = map[string]any{}
	}
	s.values[key] = value
}

func (s *fakeSettings) failOn(key string, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.errs == nil {
		s.errs = map[string]error{}
	}
	s.errs[key] = err
}

func (s *fakeSettings) Get(_ settings.Layer, key string, target any) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.errs[key]; err != nil {
		return err
	}
	value, ok := s.values[key]
	if !ok {
		return settings.ErrUnknownKey
	}
	reflect.ValueOf(target).Elem().Set(reflect.ValueOf(value))
	return nil
}

func (s *fakeSettings) Set(context.Context, settings.Write) error { return nil }

func (s *fakeSettings) Delete(context.Context, settings.Layer, string, string) error { return nil }

func (s *fakeSettings) List(context.Context, settings.Layer) ([]settings.Entry, error) {
	return nil, nil
}

func (s *fakeSettings) Declared() []settings.DeclaredItem { return nil }

func (s *fakeSettings) WrittenAt(settings.Layer, string) (time.Time, bool, error) {
	return time.Time{}, false, nil
}

func (s *fakeSettings) Read(key string, reads ...settings.LayerRead) ([]settings.LayerReadResult, error) {
	results := make([]settings.LayerReadResult, len(reads))
	for i, read := range reads {
		if err := s.Get(read.Layer, key, read.Target); err != nil {
			return nil, err
		}
		results[i] = settings.LayerReadResult{}
	}
	return results, nil
}

type harness struct {
	t       *testing.T
	p       *product
	handle  *gorm.DB
	dialect db.Dialect
	clock   *testClock
	logs    *logBuffer
	bus     *recordingBus
	hook    *recordingHook
	set     *fakeSettings
}

func newHarness(t *testing.T, dialect db.Dialect) *harness {
	t.Helper()
	handle := openDatabase(t, dialect)
	cfg, err := configDefaults.resolve()
	if err != nil {
		t.Fatalf("resolving the default configuration: %v", err)
	}
	p := newProduct(stubDatabase{handle: handle, dialect: dialect}, cfg)
	h := &harness{
		t: t, p: p, handle: handle, dialect: dialect,
		clock: newTestClock(), logs: &logBuffer{}, bus: &recordingBus{}, hook: &recordingHook{}, set: &fakeSettings{},
	}
	p.now = h.clock.read
	p.logger = slog.New(slog.NewTextHandler(h.logs, &slog.HandlerOptions{Level: slog.LevelDebug}))
	p.bus = h.bus
	p.hooks = []authn.AccountLifecycle{h.hook}
	t.Cleanup(func() { p.stop(context.Background()) })
	return h
}

func (h *harness) withOutbox() *stagingOutbox {
	h.t.Helper()
	if err := h.handle.Exec("CREATE TABLE test_staged (id " + h.autoID() + ", type TEXT NOT NULL, tenant_id TEXT NOT NULL, payload TEXT NOT NULL)").Error; err != nil {
		h.t.Fatalf("creating the staging table: %v", err)
	}
	o := &stagingOutbox{}
	h.p.outbox = o
	return o
}

func (h *harness) autoID() string {
	if h.dialect == db.Postgres {
		return "BIGSERIAL PRIMARY KEY"
	}
	return "INTEGER PRIMARY KEY AUTOINCREMENT"
}

func (h *harness) withSettings() *fakeSettings {
	h.p.store = h.set
	return h.set
}

func cheapHash(t testing.TB, plaintext string) string {
	t.Helper()
	params := password.Params{Algorithm: password.Argon2id, MemoryKiB: 8, Iterations: 1, Parallelism: 1}
	phc, err := params.Hash(password.Normalize(plaintext))
	if err != nil {
		t.Fatalf("hashing: %v", err)
	}
	return phc
}

func (h *harness) user(name, plaintext string) authn.AccountID {
	h.t.Helper()
	in := authn.CreateAccount{
		Username: name, Email: name + "@example.com", EmailVerifiedVia: authn.Verification{Via: authn.VerifiedViaAdmin},
		Via: "admin",
	}
	if plaintext != "" {
		in.PasswordHash = cheapHash(h.t, plaintext)
	}
	account, err := h.p.Create(context.Background(), in)
	if err != nil {
		h.t.Fatalf("creating %s: %v", name, err)
	}
	h.hook.reset()
	return account.ID
}

func (h *harness) count(table, where string, args ...any) int64 {
	h.t.Helper()
	var n int64
	query := "SELECT COUNT(*) FROM " + table
	if where != "" {
		query += " WHERE " + where
	}
	if err := h.handle.Raw(query, args...).Scan(&n).Error; err != nil {
		h.t.Fatalf("counting %s: %v", table, err)
	}
	return n
}

type sqlRecorder struct {
	mu         sync.Mutex
	statements []string
}

func (r *sqlRecorder) LogMode(logger.LogLevel) logger.Interface { return r }
func (r *sqlRecorder) Info(context.Context, string, ...any)     {}
func (r *sqlRecorder) Warn(context.Context, string, ...any)     {}
func (r *sqlRecorder) Error(context.Context, string, ...any)    {}

func (r *sqlRecorder) Trace(_ context.Context, _ time.Time, fc func() (string, int64), _ error) {
	sql, _ := fc()
	r.mu.Lock()
	defer r.mu.Unlock()
	r.statements = append(r.statements, sql)
}

func (r *sqlRecorder) take() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := r.statements
	r.statements = nil
	return out
}

func (h *harness) recordSQL() *sqlRecorder {
	rec := &sqlRecorder{}
	h.p.handle = h.handle.Session(&gorm.Session{Logger: rec})
	return rec
}

type recordingAudit struct {
	mu      sync.Mutex
	entries []audit.Entry
	fail    error
}

func (a *recordingAudit) Record(_ context.Context, e audit.Entry) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.fail != nil {
		return a.fail
	}
	a.entries = append(a.entries, e)
	return nil
}

func (a *recordingAudit) written() []audit.Entry {
	a.mu.Lock()
	defer a.mu.Unlock()
	return append([]audit.Entry(nil), a.entries...)
}

func (h *harness) withAudit() *recordingAudit {
	h.t.Helper()
	a := &recordingAudit{}
	h.p.audit = a
	return a
}

func (h *harness) withMeters() *metricsReader {
	h.t.Helper()
	reader := sdkmetric.NewManualReader()
	provider := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
	m, err := newMeters(provider)
	if err != nil {
		h.t.Fatalf("creating the instruments: %v", err)
	}
	h.p.meters = m
	return &metricsReader{t: h.t, reader: reader}
}

type metricsReader struct {
	t      *testing.T
	reader *sdkmetric.ManualReader
}

func (r *metricsReader) collect() map[string]metricdata.Metrics {
	r.t.Helper()
	var rm metricdata.ResourceMetrics
	if err := r.reader.Collect(context.Background(), &rm); err != nil {
		r.t.Fatalf("collecting the instruments: %v", err)
	}
	out := map[string]metricdata.Metrics{}
	for _, sm := range rm.ScopeMetrics {
		for _, m := range sm.Metrics {
			out[m.Name] = m
		}
	}
	return out
}

func (r *metricsReader) counter(name string, want map[string]string) int64 {
	r.t.Helper()
	m, ok := r.collect()[name]
	if !ok {
		return 0
	}
	data, ok := m.Data.(metricdata.Sum[int64])
	if !ok {
		r.t.Fatalf("%s is not an int64 sum", name)
	}
	var total int64
	for _, dp := range data.DataPoints {
		if !attributesCover(dp.Attributes, want) {
			continue
		}
		total += dp.Value
	}
	return total
}

func (r *metricsReader) histogramCount(name string, want map[string]string) uint64 {
	r.t.Helper()
	m, ok := r.collect()[name]
	if !ok {
		return 0
	}
	data, ok := m.Data.(metricdata.Histogram[float64])
	if !ok {
		r.t.Fatalf("%s is not a float64 histogram", name)
	}
	var total uint64
	for _, dp := range data.DataPoints {
		if !attributesCover(dp.Attributes, want) {
			continue
		}
		total += dp.Count
	}
	return total
}

func attributesCover(set attribute.Set, want map[string]string) bool {
	for k, v := range want {
		got, ok := set.Value(attribute.Key(k))
		if !ok || got.AsString() != v {
			return false
		}
	}
	return true
}
