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
	"github.com/vislake/speed/pkg/settings"
	"github.com/vislake/speed/pkg/sms"
	"github.com/vislake/speed/pkg/sms/budget"
)

var base = time.Date(2026, time.March, 14, 12, 0, 0, 0, time.UTC)

const rootKey = "sms-tests-root-key-0123456789abcdef"

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

type fakeRegistry struct {
	mu                sync.Mutex
	declared          []sms.Template
	resolveErr        error
	declaration       sms.Declaration
	heldAsDeclaration bool
	onResolve         func()
}

func newRegistry() *fakeRegistry { return &fakeRegistry{declared: declaredTemplates()} }

func (f *fakeRegistry) Declared() []sms.Template {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.heldAsDeclaration {
		return f.declaration.Declared()
	}
	return f.declared
}

func (f *fakeRegistry) setDeclaration(declaration sms.Declaration) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.declaration = declaration
	f.heldAsDeclaration = true
}

func (f *fakeRegistry) setDeclared(declared []sms.Template) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.declared = declared
	f.heldAsDeclaration = false
}

func (f *fakeRegistry) setResolveErr(err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.resolveErr = err
}

func (f *fakeRegistry) setOnResolve(fn func()) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.onResolve = fn
}

// Resolve resolves a message against the declared templates, or answers the
// scripted error.
func (f *fakeRegistry) Resolve(m sms.Message) (sms.Resolved, error) {
	f.mu.Lock()
	err := f.resolveErr
	declared := append([]sms.Template(nil), f.declared...)
	heldAsDeclaration := f.heldAsDeclaration
	declaration := f.declaration
	hook := f.onResolve
	f.mu.Unlock()
	if hook != nil {
		hook()
	}
	if err != nil {
		return sms.Resolved{}, err
	}
	if heldAsDeclaration {
		return declaration.Resolve(m)
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

var errUnusedRegistry = errors.New("fakeRegistry: the management surface is not used by this module")

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

func declaredTemplates() []sms.Template { return declaredTemplatesFor(testLocales) }

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

const testDefaultLocale = "en-US"

var testLocales = []string{"zh-CN", "en-US"}

type recordingBus struct {
	mu       sync.Mutex
	handlers map[string][]event.Handler
}

func newBus() *recordingBus { return &recordingBus{handlers: map[string][]event.Handler{}} }

func (b *recordingBus) Subscribe(eventType string, _ string, h event.Handler) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.handlers[eventType] = append(b.handlers[eventType], h)
}

func (b *recordingBus) Publish(context.Context, event.Event) error { return nil }

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
	if v, ok := declaredValue(key); ok {
		reflect.ValueOf(target).Elem().Set(reflect.ValueOf(v))
	}
	return nil
}

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
	case perSourceHourlyPath:
		return defaults.PerSourceHourly, true
	case perSourceDailyPath:
		return defaults.PerSourceDaily, true
	case perSourceDistinctHourlyPath:
		return defaults.PerSourceDistinctHourly, true
	case perSourceDistinctDailyPath:
		return defaults.PerSourceDistinctDaily, true
	case perSourceAttemptHourlyPath:
		return defaults.PerSourceAttemptHourly, true
	case globalLoginPath:
		return defaults.GlobalLogin, true
	case globalMFAPath:
		return defaults.GlobalMFA, true
	case globalEnrollPath:
		return defaults.GlobalEnroll, true
	case globalResetPath:
		return defaults.GlobalReset, true
	case globalAttemptLoginPath:
		return defaults.GlobalAttemptLogin, true
	case globalAttemptMFAPath:
		return defaults.GlobalAttemptMFA, true
	case globalAttemptEnrollPath:
		return defaults.GlobalAttemptEnroll, true
	case globalAttemptResetPath:
		return defaults.GlobalAttemptReset, true
	case perFlowSendsPath:
		return defaults.PerFlowSends, true
	case perAccountHourlyPath:
		return defaults.PerAccountHourly, true
	case ceilingHourlyPath:
		return budget.DefaultCeilingHourly, true
	}
	return nil, false
}

type rig struct {
	t        *testing.T
	p        *product
	h        *gorm.DB
	database db.Database
	ledger   budget.Store
	registry *fakeRegistry
	sender   *fakeSender
	bus      *recordingBus
	audit    *recordingAudit
	store    *fakeStore
	logs     *logBuffer
	clock    *fakeClock
	metrics  *sdkmetric.ManualReader
}

func newRig(t *testing.T, database db.Database, sender *fakeSender) *rig {
	t.Helper()
	return newRigOver(t, database, newRegistry(), sender, testDefaultLocale)
}

func newRigOver(t *testing.T, database db.Database, registry *fakeRegistry, sender *fakeSender, defaultLocale string) *rig {
	t.Helper()
	cfg := resolved{Workers: 1, QueueSize: 64}
	ledger := budget.New(database.DB())
	p := build(database, registry, sender, ledger, cfg, defaultLocale, templateLocales(registry.Declared()))
	r := &rig{
		t: t, p: p, h: database.DB(), database: database, ledger: ledger, registry: registry, sender: sender,
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

func (r *rig) setClock(at time.Time) {
	r.clock = newClock(at)
	r.p.clock = r.clock.read
}

func (r *rig) startPool() {
	r.p.startPool(context.Background())
}

func (r *rig) stopPool() {
	if err := r.p.stopPool(); err != nil {
		r.t.Fatalf("stopping the pool: %v", err)
	}
}

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

func (r *rig) rowCount() int64 {
	r.t.Helper()
	var n int64
	if err := r.h.Table(challengeTable).Count(&n).Error; err != nil {
		r.t.Fatalf("counting the challenge rows: %v", err)
	}
	return n
}

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

type dialect struct {
	name string
	open func(t *testing.T) db.Database
}

var dialects = []dialect{{
	name: sqliteDialect,
	open: func(t *testing.T) db.Database {
		return dbtest.SQLite(t, filepath.Join(t.TempDir(), "sms.db"), dbtest.Items{"encryption-key": rootKey})
	},
}}

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
	applyLedgerMigrations(t, h, dialect)
}

func applyLedgerMigrations(t *testing.T, h *gorm.DB, dialect string) {
	t.Helper()
	for _, resource := range budget.Module().Resources {
		migrations, ok := resource.(db.Migrations)
		if !ok {
			continue
		}
		entries, err := fs.ReadDir(migrations.FS, dialect)
		if err != nil {
			t.Fatalf("listing the ledger's %s migrations: %v", dialect, err)
		}
		for _, entry := range entries {
			body, err := fs.ReadFile(migrations.FS, dialect+"/"+entry.Name())
			if err != nil {
				t.Fatalf("reading the ledger's %s: %v", entry.Name(), err)
			}
			for _, statement := range splitLedgerStatements(string(body)) {
				if err := h.Exec(statement).Error; err != nil {
					t.Fatalf("applying the ledger's %s: %v", entry.Name(), err)
				}
			}
		}
	}
}

func splitLedgerStatements(sql string) []string {
	var body strings.Builder
	for _, line := range strings.Split(sql, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "--") {
			continue
		}
		body.WriteString(line)
		body.WriteByte('\n')
	}
	var out []string
	for _, part := range strings.Split(body.String(), ";") {
		if strings.TrimSpace(part) == "" {
			continue
		}
		out = append(out, part)
	}
	return out
}

// TestSplitStatementsDropsCommentLinesBeforeSplitting checks that a semicolon
// inside a whole-line comment does not end the statement the comment annotates.
func TestSplitStatementsDropsCommentLinesBeforeSplitting(t *testing.T) {
	sql := "-- the challenges; the guard is idempotent\n" +
		"CREATE TABLE alpha (id INTEGER);\n" +
		"-- the budget event; the guard is idempotent\n" +
		"CREATE TABLE beta (id INTEGER);\n"
	want := []string{
		"CREATE TABLE alpha (id INTEGER)",
		"CREATE TABLE beta (id INTEGER)",
	}
	got := splitStatements(sql)
	if len(got) != len(want) {
		t.Fatalf("split into %d statements, want %d: %q", len(got), len(want), got)
	}
	for i := range want {
		if trimmed := strings.TrimSpace(got[i]); trimmed != want[i] {
			t.Errorf("statement %d is %q, want %q", i, trimmed, want[i])
		}
	}
}

// splitStatements drops whole-line comments before splitting on semicolons, so
// a semicolon inside a comment cannot end the statement the comment annotates.
func splitStatements(sql string) []string {
	var body strings.Builder
	for _, line := range strings.Split(sql, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "--") {
			continue
		}
		body.WriteString(line)
		body.WriteByte('\n')
	}
	var out []string
	for _, part := range strings.Split(body.String(), ";") {
		if strings.TrimSpace(part) == "" {
			continue
		}
		out = append(out, part)
	}
	return out
}

func openSQLite(t *testing.T) db.Database {
	t.Helper()
	database := dbtest.SQLite(t, filepath.Join(t.TempDir(), "sms.db"), dbtest.Items{"encryption-key": rootKey})
	handle := database.DB().Session(&gorm.Session{Logger: logger.Discard})
	applyMigrations(t, handle, sqliteDialect)
	return database
}

func openDialect(t *testing.T, dialect string) db.Database {
	t.Helper()
	for _, d := range dialects {
		if d.name == dialect {
			database := d.open(t)
			handle := database.DB().Session(&gorm.Session{Logger: logger.Discard})
			applyMigrations(t, handle, dialect)
			return database
		}
	}
	t.Fatalf("no dialect %q", dialect)
	return nil
}

func forEachDialect(t *testing.T, run func(t *testing.T, database db.Database, dialect string)) {
	t.Helper()
	for _, d := range dialects {
		t.Run(d.name, func(t *testing.T) {
			run(t, openDialect(t, d.name), d.name)
		})
	}
}

func newRigSQLite(t *testing.T) *rig {
	t.Helper()
	return newRig(t, openSQLite(t), newSender())
}

const (
	acct  = authn.AccountID("acct-1")
	phone = "+8613800138000"
)

func (r *rig) allow() {
	r.store.set(allowedPrefixesPath, `["+86"]`)
}

func (r *rig) sendLogin() (string, error) {
	return r.p.Send(context.Background(), authn.SendCode{
		Purpose: authn.SMSPurposeLogin,
		Phone:   phone,
		Deliver: true,
	})
}
