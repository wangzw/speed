package registry

import (
	"bytes"
	"context"
	"encoding/json"
	"io/fs"
	"log/slog"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"github.com/vislake/speed/pkg/db"
	"github.com/vislake/speed/pkg/event"
	"github.com/vislake/speed/pkg/internal/dbtest"
	"github.com/vislake/speed/pkg/internal/pgpool/pgtest"
	"github.com/vislake/speed/pkg/sms"
)

// base is the instant injected writes are stamped from.
var base = time.Date(2026, time.March, 14, 12, 0, 0, 0, time.UTC)

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

func (b *logBuffer) lines(with ...string) []string {
	b.mu.Lock()
	whole := b.buf.String()
	b.mu.Unlock()
	var out []string
	for _, line := range strings.Split(whole, "\n") {
		found := line != ""
		for _, s := range with {
			found = found && strings.Contains(line, s)
		}
		if found {
			out = append(out, line)
		}
	}
	return out
}

// fakeProvider is the template-management seam a case scripts. It answers the
// approval state a case set for each vendor code, records every code it was
// asked about and every submission it was handed, and fails the calls a case
// told it to.
type fakeProvider struct {
	mu            sync.Mutex
	offerCode     string
	offerErr      error
	offered       []sms.Submission
	approvals     map[string]sms.Approval
	approvalErr   map[string]error
	approvalCalls []string
}

func newProvider() *fakeProvider {
	return &fakeProvider{
		offerCode:   "vendor-code",
		approvals:   map[string]sms.Approval{},
		approvalErr: map[string]error{},
	}
}

func (p *fakeProvider) Name() string { return "fake" }

func (p *fakeProvider) Offer(_ context.Context, s sms.Submission) (string, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.offered = append(p.offered, s)
	if p.offerErr != nil {
		return "", p.offerErr
	}
	return p.offerCode, nil
}

func (p *fakeProvider) Approval(_ context.Context, code string) (sms.Approval, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.approvalCalls = append(p.approvalCalls, code)
	if err := p.approvalErr[code]; err != nil {
		return sms.Approval{}, err
	}
	if ap, ok := p.approvals[code]; ok {
		return ap, nil
	}
	return sms.Approval{State: sms.ApprovalPending}, nil
}

func (p *fakeProvider) Retire(context.Context, string) error { return nil }

func (p *fakeProvider) setApproval(code string, ap sms.Approval) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.approvals[code] = ap
}

func (p *fakeProvider) failApproval(code string, err error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.approvalErr[code] = err
}

func (p *fakeProvider) calls() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]string(nil), p.approvalCalls...)
}

func (p *fakeProvider) submissions() []sms.Submission {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]sms.Submission(nil), p.offered...)
}

// recordingBus is a bus that keeps what it was given and delivers to the
// handlers subscribed, which is what a process-local bus does.
type recordingBus struct {
	mu        sync.Mutex
	published []event.Event
	handlers  map[string][]event.Handler
}

func newBus() *recordingBus {
	return &recordingBus{handlers: map[string][]event.Handler{}}
}

func (b *recordingBus) Subscribe(eventType string, _ string, h event.Handler) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.handlers[eventType] = append(b.handlers[eventType], h)
}

func (b *recordingBus) Publish(_ context.Context, evt event.Event) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.published = append(b.published, evt)
	return nil
}

func (b *recordingBus) events() []event.Event {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]event.Event(nil), b.published...)
}

func (b *recordingBus) deliver(ctx context.Context, evt event.Event) {
	b.mu.Lock()
	hs := append([]event.Handler(nil), b.handlers[evt.Type]...)
	b.mu.Unlock()
	for _, h := range hs {
		_ = h(ctx, evt)
	}
}

func (b *recordingBus) subscribed(eventType string) int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return len(b.handlers[eventType])
}

// settingsOf builds the module settings from a configuration template set and a
// poll interval, through the same resolve the module uses.
func settingsOf(t *testing.T, templates map[string]sms.TemplateSpec, poll time.Duration) moduleSettings {
	t.Helper()
	s, err := moduleConfig{Templates: templates, PollInterval: poll}.resolve()
	if err != nil {
		t.Fatalf("resolving the configuration: %v", err)
	}
	return s
}

// spec builds a template spec from an ordered parameter list and a code table.
func spec(params []string, codes map[string]string) sms.TemplateSpec {
	return sms.TemplateSpec{Params: params, Codes: codes}
}

// rig is the module over a real database with the dependencies a case drives.
type rig struct {
	t        *testing.T
	s        *registryStore
	h        *gorm.DB
	database db.Database
	provider *fakeProvider
	bus      *recordingBus
	logs     *logBuffer
}

// newRig builds the module over the handle, with the bus and the log buffer a
// case can inspect. The snapshot is not loaded: a case calls load for the state
// Init leaves, or leaves it unloaded to drive the before-load behaviour.
func newRig(t *testing.T, database db.Database, provider *fakeProvider, cfg moduleSettings) *rig {
	t.Helper()
	h := database.DB().Session(&gorm.Session{Logger: logger.Discard})
	applyMigrations(t, h, string(database.Dialect()))
	logs := &logBuffer{}
	bus := newBus()
	s := &registryStore{
		handle:   h,
		provider: provider,
		bus:      bus,
		logger:   logs.logger(),
		config:   cfg,
	}
	return &rig{t: t, s: s, h: h, database: database, provider: provider, bus: bus, logs: logs}
}

// load replaces the snapshot, which is the state Init leaves.
func (r *rig) load() {
	r.t.Helper()
	if err := r.s.load(context.Background()); err != nil {
		r.t.Fatalf("loading the snapshot: %v", err)
	}
}

// rowCount counts the rows in the runtime table.
func (r *rig) rowCount() int64 {
	r.t.Helper()
	var n int64
	if err := r.h.Table(tableName).Count(&n).Error; err != nil {
		r.t.Fatalf("counting the runtime rows: %v", err)
	}
	return n
}

// plant writes a runtime row directly, the way another replica would. The
// approvals are keyed by language, as the column holds them.
func (r *rig) plant(name string, params []string, codes map[string]string, approvals map[string]sms.Approval, enabled bool, at time.Time) {
	r.t.Helper()
	pj, _ := json.Marshal(params)
	cj, _ := json.Marshal(codes)
	err := r.h.Exec("INSERT INTO "+tableName+" (name, params, codes, approvals, enabled, "+
		"version, created_at, updated_at) VALUES (?, ?, ?, ?, ?, 1, ?, ?)",
		name, string(pj), string(cj), encodeApprovals(approvals), enabled, at, at).Error
	if err != nil {
		r.t.Fatalf("planting the row %q: %v", name, err)
	}
}

// approvalsOf reads one row's review table, keyed by language.
func (r *rig) approvalsOf(name string) map[string]sms.Approval {
	r.t.Helper()
	var rows []struct {
		Approvals string
	}
	if err := r.h.Table(tableName).Select("approvals").Where("name = ?", name).Scan(&rows).Error; err != nil {
		r.t.Fatalf("reading the row %q: %v", name, err)
	}
	if len(rows) == 0 {
		r.t.Fatalf("no row named %q", name)
	}
	approvals, err := decodeApprovals(rows[0].Approvals)
	if err != nil {
		r.t.Fatalf("decoding the approvals of %q: %v", name, err)
	}
	return approvals
}

// approvalOf reads one row's review state for one language.
func (r *rig) approvalOf(name, locale string) string {
	r.t.Helper()
	return string(r.approvalsOf(name)[locale].State)
}

// versionOf reads one row's version.
func (r *rig) versionOf(name string) int64 {
	r.t.Helper()
	var rows []struct {
		Version int64
	}
	if err := r.h.Table(tableName).Select("version").Where("name = ?", name).Scan(&rows).Error; err != nil {
		r.t.Fatalf("reading the row %q: %v", name, err)
	}
	if len(rows) == 0 {
		r.t.Fatalf("no row named %q", name)
	}
	return rows[0].Version
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
	for _, part := range strings.Split(sql, ";") {
		if onlyCommentsOrBlank(part) {
			continue
		}
		out = append(out, part)
	}
	return out
}

func onlyCommentsOrBlank(text string) bool {
	for _, line := range strings.Split(text, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "--") {
			continue
		}
		return false
	}
	return true
}

// openDialect delivers the database of the dialect with the module's table
// built.
func openDialect(t *testing.T, dialect string) db.Database {
	t.Helper()
	return openDialectItems(t, dialect, nil)
}

// openDialectItems is openDialect with input items configured beside the
// locator, so a concurrency case can hold the pool to one connection and let
// the writes serialise at the pool rather than at the engine's own lock.
func openDialectItems(t *testing.T, dialect string, items dbtest.Items) db.Database {
	t.Helper()
	var database db.Database
	switch dialect {
	case sqliteDialect:
		database = dbtest.SQLite(t, filepath.Join(t.TempDir(), "sms_registry.db"), items)
	case postgresDialect:
		database = dbtest.Postgres(t, pgtest.Acquire(t), items)
	default:
		t.Fatalf("no dialect %q", dialect)
	}
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

// newSQLiteRig builds a loaded rig over a fresh SQLite database with no
// configuration templates, which is the common starting point.
func newSQLiteRig(t *testing.T) *rig {
	t.Helper()
	r := newRig(t, openDialect(t, sqliteDialect), newProvider(), settingsOf(t, nil, 0))
	r.load()
	return r
}
