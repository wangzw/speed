package sms

import (
	"bytes"
	"context"
	"errors"
	"io/fs"
	"log/slog"
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
	"github.com/vislake/speed/pkg/db"
	"github.com/vislake/speed/pkg/event"
	"github.com/vislake/speed/pkg/internal/dbtest"
	"github.com/vislake/speed/pkg/internal/pgpool/pgtest"
	"github.com/vislake/speed/pkg/settings"
	"github.com/vislake/speed/pkg/sms"
)

// base is the instant the cases that need a fixed time start from.
var base = time.Date(2026, time.March, 14, 12, 0, 0, 0, time.UTC)

// rootKey is the database root key a rig configures, so that the blind index has
// a key to digest under. It is long enough for the database module's floor.
const rootKey = "sms-tests-root-key-0123456789abcdef"

// fakeClock is a clock a case moves. It reads in the zone it was built with, so a
// case can hand the module a time that is not UTC, which is what the module has
// to write and bind in UTC whatever it is given.
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

// logBuffer collects what the module logs, as text lines, so a case can read the
// level and the fields of a line and look for what must not be in any of them.
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

// lines returns the lines that carry every one of the substrings.
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

// fakeSender is the carrier a case scripts. It records every resolved message it
// is handed and answers with the result it was told to.
type fakeSender struct {
	mu     sync.Mutex
	sent   []sms.Resolved
	result error
	onSend func(sms.Resolved)
}

func newSender() *fakeSender { return &fakeSender{} }

func (f *fakeSender) Name() string { return "fake" }

func (f *fakeSender) Send(_ context.Context, r sms.Resolved) (sms.Receipt, error) {
	f.mu.Lock()
	f.sent = append(f.sent, r)
	res := f.result
	cb := f.onSend
	f.mu.Unlock()
	if cb != nil {
		cb(r)
	}
	return sms.Receipt{ProviderID: "provider-1"}, res
}

func (f *fakeSender) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.sent)
}

// last returns the most recent message the carrier was handed, and whether it
// was handed one at all.
func (f *fakeSender) last() (sms.Resolved, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.sent) == 0 {
		return sms.Resolved{}, false
	}
	return f.sent[len(f.sent)-1], true
}

func (f *fakeSender) setResult(err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.result = err
}

// fakeRegistry is the template registry a case scripts. It reports the templates
// it holds, resolves a message against them the way a registry backed by the
// configuration does, and answers a scripted error for a message a case wants
// the registry to refuse.
type fakeRegistry struct {
	mu         sync.Mutex
	declared   []sms.Template
	resolveErr error
}

func newRegistry() *fakeRegistry { return &fakeRegistry{declared: declaredTemplates()} }

// Declared reports the templates the registry holds. It starts as the full set
// this module needs, so an assembly drives to Start; a case that drives the
// consumer-side check replaces it with a set missing a combination.
func (f *fakeRegistry) Declared() []sms.Template {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.declared
}

// setDeclared replaces the templates the registry holds.
func (f *fakeRegistry) setDeclared(declared []sms.Template) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.declared = declared
}

// setResolveErr makes Resolve answer err, for a case that drives a message the
// registry will not build.
func (f *fakeRegistry) setResolveErr(err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.resolveErr = err
}

// Resolve resolves a message against the declared templates, or answers the
// scripted error.
func (f *fakeRegistry) Resolve(m sms.Message) (sms.Resolved, error) {
	f.mu.Lock()
	err := f.resolveErr
	declared := append([]sms.Template(nil), f.declared...)
	f.mu.Unlock()
	if err != nil {
		return sms.Resolved{}, err
	}
	d, derr := declarationFrom(declared)
	if derr != nil {
		return sms.Resolved{}, derr
	}
	return d.Resolve(m)
}

// The management surface is the consumer's not to call; a stand-in that reaches
// it fails loudly rather than answer a plausible zero value.
func (f *fakeRegistry) List(context.Context) ([]sms.Record, error) {
	return nil, errUnusedRegistry
}

func (f *fakeRegistry) Register(context.Context, string, sms.TemplateSpec) (sms.Record, error) {
	return sms.Record{}, errUnusedRegistry
}

func (f *fakeRegistry) Offer(context.Context, sms.Submission) (sms.Record, error) {
	return sms.Record{}, errUnusedRegistry
}

func (f *fakeRegistry) SetEnabled(context.Context, string, bool) error { return errUnusedRegistry }

func (f *fakeRegistry) Remove(context.Context, string) error { return errUnusedRegistry }

// errUnusedRegistry is what the fake registry's management methods answer: this
// module is a consumer of templates and never manages one.
var errUnusedRegistry = errors.New("fakeRegistry: the management surface is not used by this module")

// declarationFrom builds a real Declaration from a reported template set, so the
// fake resolves a message the way a registry backed by the configuration does.
func declarationFrom(declared []sms.Template) (sms.Declaration, error) {
	specs := make(map[string]sms.TemplateSpec, len(declared))
	for _, t := range declared {
		codes := make(map[string]string, len(t.Locales))
		for _, locale := range t.Locales {
			codes[locale] = t.Name + "/" + locale
		}
		specs[t.Name] = sms.TemplateSpec{Params: append([]string(nil), t.Params...), Codes: codes}
	}
	return sms.NewDeclaration(specs)
}

// declaredTemplates is the set of templates this module needs, as a registry that
// covers them would report it: the three template names, each with the two
// parameters and the two languages.
func declaredTemplates() []sms.Template { return declaredTemplatesFor(testLocales) }

// declaredTemplatesFor is declaredTemplates over a registry's own language set, so
// a case can drive a registry that declares languages other than the two the
// module used to hardcode.
func declaredTemplatesFor(locales []string) []sms.Template {
	out := make([]sms.Template, 0, len(requiredTemplates))
	for _, name := range requiredTemplates {
		out = append(out, sms.Template{
			Name:    name,
			Params:  append([]string(nil), templateParams...),
			Locales: append([]string(nil), locales...),
		})
	}
	return out
}

// testDefaultLocale is the default language the rig's product runs on, matching
// the i18n capability's fallback.
const testDefaultLocale = "en-US"

// testLocales is the language set the rig's carrier declares by default. The
// default language is last on purpose: a case that truncates a template's
// languages to the first one leaves a template that misses the configured
// default, which is the startup failure checkSender names.
var testLocales = []string{"zh-CN", "en-US"}

// recordingBus is a bus that keeps what was published and delivers it to the
// handlers subscribed, which is what a process-local bus does.
type recordingBus struct {
	mu       sync.Mutex
	handlers map[string][]event.Handler
}

func newBus() *recordingBus { return &recordingBus{handlers: map[string][]event.Handler{}} }

func (b *recordingBus) Subscribe(eventType string, h event.Handler) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.handlers[eventType] = append(b.handlers[eventType], h)
}

func (b *recordingBus) Publish(context.Context, event.Event) error { return nil }

// deliver hands an event to the handlers of its type, as the bus would.
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

func (b *recordingBus) subscribedTypes() []string {
	b.mu.Lock()
	defer b.mu.Unlock()
	out := make([]string, 0, len(b.handlers))
	for typ := range b.handlers {
		out = append(out, typ)
	}
	return out
}

// recordingAudit keeps the rows it was asked to write.
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

// fakeStore is the settings store, holding the values a case wrote and failing
// the reads it was told to.
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
	// A key nobody wrote reads as its declared default, which the real store does
	// for the module.
	if v, ok := declaredValue(key); ok {
		reflect.ValueOf(target).Elem().Set(reflect.ValueOf(v))
	}
	return nil
}

// declaredValue is the declared default of one of this module's settings, as a
// read that finds no row answers with it.
func declaredValue(key string) (any, bool) {
	switch key {
	case allowedPrefixesPath:
		return defaults.AllowedPrefixes, true
	case codeLengthPath:
		return defaults.CodeLength, true
	case codeTTLPath:
		return defaults.CodeTTL, true
	case codeAttemptsPath:
		return defaults.CodeAttempts, true
	case resendIntervalPath:
		return defaults.ResendInterval, true
	case perPhoneHourlyPath:
		return defaults.PerPhoneHourly, true
	case perPhoneDailyPath:
		return defaults.PerPhoneDaily, true
	case globalLoginPath:
		return defaults.GlobalLogin, true
	case globalMFAPath:
		return defaults.GlobalMFA, true
	case globalEnrollPath:
		return defaults.GlobalEnroll, true
	case perFlowSendsPath:
		return defaults.PerFlowSends, true
	case perAccountHourlyPath:
		return defaults.PerAccountHourly, true
	}
	return nil, false
}

// rig is the module over a real database with every dependency a case drives.
type rig struct {
	t        *testing.T
	p        *product
	h        *gorm.DB
	database db.Database
	registry *fakeRegistry
	sender   *fakeSender
	bus      *recordingBus
	audit    *recordingAudit
	store    *fakeStore
	logs     *logBuffer
	clock    *fakeClock
	metrics  *sdkmetric.ManualReader
}

// newRig builds the module over the handle with a root key configured. The bus,
// the audit, the settings store and the metric capability are present; a case
// that wants one absent sets the field to nil on the product. The worker pool is
// not started: a case that drives the worker path starts it, and one that drives
// Send alone leaves the queue untouched.
func newRig(t *testing.T, database db.Database, sender *fakeSender) *rig {
	t.Helper()
	return newRigOver(t, database, newRegistry(), sender, testDefaultLocale)
}

// newRigOver is newRig over a chosen registry and default language, so a case can
// drive the module's read of a configured default and a registry that declares a
// set the module has to check. The templates' languages are derived from what the
// registry declares, the way newProduct derives them.
func newRigOver(t *testing.T, database db.Database, registry *fakeRegistry, sender *fakeSender, defaultLocale string) *rig {
	t.Helper()
	cfg := resolved{Workers: 1, QueueSize: 64}
	p := build(database, registry, sender, cfg, defaultLocale, templateLocales(registry.Declared()))
	r := &rig{
		t: t, p: p, h: database.DB(), database: database, registry: registry, sender: sender,
		bus: newBus(), audit: &recordingAudit{}, store: newStore(),
		logs: &logBuffer{}, clock: newClock(base),
	}
	p.logger = r.logs.logger()
	p.settings = newReader(r.store, p.logger)
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
	return r
}

// setClock replaces the module's clock, for a case that reads it in another zone.
func (r *rig) setClock(at time.Time) {
	r.clock = newClock(at)
	r.p.clock = r.clock.read
}

// startPool starts the worker pool, which a case does when it drives the send off
// the request path.
func (r *rig) startPool() {
	r.p.startPool(context.Background())
}

// stopPool ends the worker pool and waits for it.
func (r *rig) stopPool() {
	if err := r.p.stopPool(); err != nil {
		r.t.Fatalf("stopping the pool: %v", err)
	}
}

// insert writes a challenge row directly, the way another replica would, in UTC,
// and returns its identifier.
func (r *rig) insert(id, purpose, phoneMac string, account *string, suppressed bool, codeMac string, attempts, maxAttempts int, created, expires time.Time, consumed *time.Time) {
	r.t.Helper()
	var accountVal any
	if account != nil {
		accountVal = *account
	}
	var consumedVal any
	if consumed != nil {
		u := consumed.UTC()
		consumedVal = u
	}
	err := r.h.Exec("INSERT INTO authn_challenges "+
		"(id, purpose, phone_mac, account_id, suppressed, code_mac, attempts, max_attempts, created_at, expires_at, consumed_at) "+
		"VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)",
		id, purpose, phoneMac, accountVal, suppressed, codeMac, attempts, maxAttempts,
		created.UTC(), expires.UTC(), consumedVal).Error
	if err != nil {
		r.t.Fatalf("inserting a challenge row: %v", err)
	}
}

// rowCount counts the rows in the challenge table.
func (r *rig) rowCount() int64 {
	r.t.Helper()
	var n int64
	if err := r.h.Table(challengeTable).Count(&n).Error; err != nil {
		r.t.Fatalf("counting the challenge rows: %v", err)
	}
	return n
}

// column reads one column of one row, and reports whether the row is there.
func (r *rig) column(id, name string) (any, bool) {
	r.t.Helper()
	var rows []map[string]any
	if err := r.h.Table(challengeTable).Where("id = ?", id).Limit(1).Find(&rows).Error; err != nil {
		r.t.Fatalf("reading a challenge row: %v", err)
	}
	if len(rows) == 0 {
		return nil, false
	}
	return rows[0][name], true
}

// boolColumn reads a boolean column of one row, as SQLite returns it as an
// integer and PostgreSQL as a bool.
func (r *rig) boolColumn(id, name string) (bool, bool) {
	r.t.Helper()
	v, ok := r.column(id, name)
	if !ok {
		return false, false
	}
	switch x := v.(type) {
	case bool:
		return x, true
	case int64:
		return x != 0, true
	case int:
		return x != 0, true
	}
	return false, true
}

// allText reads every column of every row as text, for a case that looks for a
// value that must not be in the table at all.
func (r *rig) allText() string {
	r.t.Helper()
	rows, err := r.h.Table(challengeTable).Rows()
	if err != nil {
		r.t.Fatalf("reading the challenge rows: %v", err)
	}
	defer func() { _ = rows.Close() }()
	cols, err := rows.Columns()
	if err != nil {
		r.t.Fatalf("reading the columns: %v", err)
	}
	var sb bytes.Buffer
	for rows.Next() {
		holders := make([]any, len(cols))
		ptrs := make([]any, len(cols))
		for i := range holders {
			ptrs[i] = &holders[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			r.t.Fatalf("scanning a row: %v", err)
		}
		for _, v := range holders {
			sb.WriteString(stringify(v))
			sb.WriteByte(' ')
		}
	}
	return sb.String()
}

func stringify(v any) string {
	switch t := v.(type) {
	case nil:
		return ""
	case []byte:
		return string(t)
	case string:
		return t
	default:
		return "?"
	}
}

// counter reads the value of a counter from the metric reader for the data
// points whose attributes include every pair given.
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

// The dialects the module runs on.
const (
	sqliteDialect   = "sqlite"
	postgresDialect = "postgres"
)

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
		for _, statement := range splitStatements(string(sql)) {
			if err := h.Exec(statement).Error; err != nil {
				t.Fatalf("applying %s: %v", entry.Name(), err)
			}
		}
	}
}

// splitStatements splits a migration on the semicolons that end statements,
// dropping the comment-only and blank fragments.
func splitStatements(sql string) []string {
	var out []string
	for _, part := range bytes.Split([]byte(sql), []byte(";")) {
		text := string(part)
		if onlyCommentsOrBlank(text) {
			continue
		}
		out = append(out, text)
	}
	return out
}

func onlyCommentsOrBlank(text string) bool {
	for _, line := range bytes.Split([]byte(text), []byte("\n")) {
		trimmed := bytes.TrimSpace(line)
		if len(trimmed) == 0 || bytes.HasPrefix(trimmed, []byte("--")) {
			continue
		}
		return false
	}
	return true
}

// openSQLite delivers the SQLite database with the module's table built and a
// root key configured.
func openSQLite(t *testing.T) db.Database {
	t.Helper()
	database := dbtest.SQLite(t, filepath.Join(t.TempDir(), "sms.db"), dbtest.Items{"encryption-key": rootKey})
	handle := database.DB().Session(&gorm.Session{Logger: logger.Discard})
	applyMigrations(t, handle, sqliteDialect)
	return database
}

// openDialect delivers the database of the dialect with the module's table built.
func openDialect(t *testing.T, dialect string) db.Database {
	t.Helper()
	var database db.Database
	switch dialect {
	case sqliteDialect:
		database = dbtest.SQLite(t, filepath.Join(t.TempDir(), "sms.db"), dbtest.Items{"encryption-key": rootKey})
	case postgresDialect:
		database = dbtest.Postgres(t, pgtest.Acquire(t), dbtest.Items{"encryption-key": rootKey})
	default:
		t.Fatalf("no dialect %q", dialect)
	}
	handle := database.DB().Session(&gorm.Session{Logger: logger.Discard})
	applyMigrations(t, handle, dialect)
	return database
}

// forEachDialect runs a case on SQLite and on PostgreSQL. The PostgreSQL leg is
// skipped on a machine without docker, as every case on that engine is.
func forEachDialect(t *testing.T, run func(t *testing.T, database db.Database, dialect string)) {
	t.Helper()
	for _, dialect := range []string{sqliteDialect, postgresDialect} {
		t.Run(dialect, func(t *testing.T) {
			run(t, openDialect(t, dialect), dialect)
		})
	}
}

// newRigSQLite is newRig over a fresh SQLite database.
func newRigSQLite(t *testing.T) *rig {
	t.Helper()
	return newRig(t, openSQLite(t), newSender())
}

const (
	acct  = authn.AccountID("acct-1")
	phone = "+8613800138000"
)

// allow is the store with the allowed table admitting the +86 prefix and a long
// resend interval freed, so most cases start from a send that goes through.
func (r *rig) allow() {
	r.store.set(allowedPrefixesPath, `["+86"]`)
}

// sendLogin sends a login-purpose code for the phone, delivering it.
func (r *rig) sendLogin() (string, error) {
	return r.p.Send(context.Background(), authn.SendCode{
		Purpose: authn.SMSPurposeLogin,
		Phone:   phone,
		Deliver: true,
	})
}
