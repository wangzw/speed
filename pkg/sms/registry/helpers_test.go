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
	"github.com/vislake/speed/pkg/sms"
)

var base = time.Date(2026, time.March, 14, 12, 0, 0, 0, time.UTC)

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

type fakeProvider struct {
	mu            sync.Mutex
	name          string
	model         sms.CarrierModel
	offerCode     string
	offerErr      error
	offered       []sms.Submission
	approvals     map[string]sms.Approval
	approvalErr   map[string]error
	approvalCalls []string
}

func codeCarrier() sms.CarrierModel {
	return sms.CarrierModel{
		Content: sms.ContentSupport{TemplateCode: true},
		Review:  sms.ReviewSupport{PerTemplate: true},
	}
}

func textCarrier() sms.CarrierModel {
	return sms.CarrierModel{
		Content:       sms.ContentSupport{TextBody: true},
		Review:        sms.ReviewSupport{PerTemplate: false, Originator: true},
		AttributeKeys: []string{"IN_TEMPLATE_ID"},
		MaxBodyRunes:  1600,
		AsyncReceipt:  true,
	}
}

func contentCarrier() sms.CarrierModel {
	return sms.CarrierModel{
		Content:      sms.ContentSupport{TemplateCode: true},
		Review:       sms.ReviewSupport{PerTemplate: false, Originator: true},
		AsyncReceipt: true,
	}
}

func newProvider() *fakeProvider {
	return &fakeProvider{
		name:        "fake",
		model:       codeCarrier(),
		offerCode:   "vendor-code",
		approvals:   map[string]sms.Approval{},
		approvalErr: map[string]error{},
	}
}

func newNamedProvider(name string) *fakeProvider {
	p := newProvider()
	p.name = name
	return p
}

func newModelProvider(model sms.CarrierModel) *fakeProvider {
	p := newProvider()
	p.model = model
	return p
}

func (p *fakeProvider) Name() string { return p.name }

func (p *fakeProvider) Model() sms.CarrierModel { return p.model }

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

func settingsOf(t *testing.T, templates map[string]sms.TemplateSpec, poll time.Duration) moduleSettings {
	t.Helper()
	s, err := moduleConfig{Templates: templates, PollInterval: poll}.resolve()
	if err != nil {
		t.Fatalf("resolving the configuration: %v", err)
	}
	return s
}

func spec(params []string, codes map[string]string) sms.TemplateSpec {
	return sms.TemplateSpec{Params: params, Codes: codes}
}

type rig struct {
	t        *testing.T
	s        *registryStore
	h        *gorm.DB
	provider *fakeProvider
	bus      *recordingBus
	logs     *logBuffer
}

func newRig(t *testing.T, database db.Database, provider *fakeProvider, cfg moduleSettings) *rig {
	t.Helper()
	h := database.DB().Session(&gorm.Session{Logger: logger.Discard})
	applyMigrations(t, h, string(database.Dialect()))
	return newRigOn(t, h, provider, cfg)
}

func newRigOn(t *testing.T, h *gorm.DB, provider *fakeProvider, cfg moduleSettings) *rig {
	t.Helper()
	logs := &logBuffer{}
	bus := newBus()
	s := &registryStore{
		handle:   h,
		provider: provider,
		bus:      bus,
		logger:   logs.logger(),
		config:   cfg,
		tuned:    newSettingsReader(nil, logs.logger()),
	}
	return &rig{t: t, s: s, h: h, provider: provider, bus: bus, logs: logs}
}

func (r *rig) load() {
	r.t.Helper()
	if err := r.s.load(context.Background()); err != nil {
		r.t.Fatalf("loading the snapshot: %v", err)
	}
}

func (r *rig) rowCount() int64 { return r.rowCountFor(r.provider.Name()) }

func (r *rig) rowCountFor(carrier string) int64 {
	r.t.Helper()
	var n int64
	if err := r.h.Table(tableName).Where("carrier = ?", carrier).Count(&n).Error; err != nil {
		r.t.Fatalf("counting the runtime rows: %v", err)
	}
	return n
}

func (r *rig) plant(name string, params []string, codes map[string]string, approvals map[string]sms.Approval, enabled bool, at time.Time) {
	r.t.Helper()
	r.plantRow(r.provider.Name(), name, sms.KindNotification, params, codes, nil, nil, approvals, enabled, at)
}

func (r *rig) plantLegacy(name string, params []string, codes map[string]string, approvals map[string]sms.Approval, enabled bool, at time.Time) {
	r.t.Helper()
	r.plantRow("", name, sms.KindNotification, params, codes, nil, nil, approvals, enabled, at)
}

func (r *rig) plantRow(carrier, name string, kind sms.Kind, params []string, codes, bodies,
	attributes map[string]string, approvals map[string]sms.Approval, enabled bool, at time.Time,
) {
	r.t.Helper()
	pj, _ := json.Marshal(params)
	cj, _ := json.Marshal(orEmpty(codes))
	bj, _ := json.Marshal(orEmpty(bodies))
	aj, _ := json.Marshal(orEmpty(attributes))
	err := r.h.Exec("INSERT INTO "+tableName+" (carrier, name, kind, params, codes, bodies, attributes, "+
		"approvals, enabled, version, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, 1, ?, ?)",
		carrier, name, string(normalizedKind(kind)), string(pj), string(cj), string(bj), string(aj),
		encodeApprovals(approvals), enabled, at, at).Error
	if err != nil {
		r.t.Fatalf("planting the row %q: %v", name, err)
	}
}

func (r *rig) approvalsOf(name string) map[string]sms.Approval {
	r.t.Helper()
	var rows []struct {
		Approvals string
	}
	if err := r.h.Table(tableName).Select("approvals").
		Where("carrier = ? AND name = ?", r.provider.Name(), name).Scan(&rows).Error; err != nil {
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

func (r *rig) carrierOf(name string) string {
	r.t.Helper()
	var rows []struct {
		Carrier string
	}
	if err := r.h.Table(tableName).Select("carrier").Where("name = ?", name).Scan(&rows).Error; err != nil {
		r.t.Fatalf("reading the carrier of %q: %v", name, err)
	}
	if len(rows) == 0 {
		r.t.Fatalf("no row named %q", name)
	}
	return rows[0].Carrier
}

func (r *rig) ownerOf() (string, bool) {
	r.t.Helper()
	var rows []struct {
		Carrier string
	}
	if err := r.h.Table(ownershipTable).Select("carrier").Scan(&rows).Error; err != nil {
		r.t.Fatalf("reading the ownership marker: %v", err)
	}
	if len(rows) == 0 {
		return "", false
	}
	return rows[0].Carrier, true
}

func (r *rig) approvalOf(name, locale string) string {
	r.t.Helper()
	return string(r.approvalsOf(name)[locale].State)
}

func (r *rig) versionOf(name string) int64 {
	r.t.Helper()
	var rows []struct {
		Version int64
	}
	if err := r.h.Table(tableName).Select("version").
		Where("carrier = ? AND name = ?", r.provider.Name(), name).Scan(&rows).Error; err != nil {
		r.t.Fatalf("reading the row %q: %v", name, err)
	}
	if len(rows) == 0 {
		r.t.Fatalf("no row named %q", name)
	}
	return rows[0].Version
}

const sqliteDialect = "sqlite"

type dialect struct {
	name string
	open func(t *testing.T, items dbtest.Items) db.Database
}

var dialects = []dialect{{
	name: sqliteDialect,
	open: func(t *testing.T, items dbtest.Items) db.Database {
		return dbtest.SQLite(t, filepath.Join(t.TempDir(), "sms_registry.db"), items)
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
}

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

func openDialect(t *testing.T, dialect string) db.Database {
	t.Helper()
	return openDialectItems(t, dialect, nil)
}

func openDialectItems(t *testing.T, dialect string, items dbtest.Items) db.Database {
	t.Helper()
	for _, d := range dialects {
		if d.name == dialect {
			return d.open(t, items)
		}
	}
	t.Fatalf("no dialect %q", dialect)
	return nil
}

func forEachDialect(t *testing.T, run func(t *testing.T, database db.Database, dialect string)) {
	t.Helper()
	for _, d := range dialects {
		t.Run(d.name, func(t *testing.T) {
			run(t, openDialectItems(t, d.name, nil), d.name)
		})
	}
}

func newSQLiteRig(t *testing.T) *rig {
	t.Helper()
	r := newRig(t, openDialect(t, sqliteDialect), newProvider(), settingsOf(t, nil, 0))
	r.load()
	return r
}
