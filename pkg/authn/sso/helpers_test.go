package sso

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

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/authn/authntest"
	"github.com/vislake/speed/pkg/authn/internal/mount/mounttest"
	"github.com/vislake/speed/pkg/config"
	"github.com/vislake/speed/pkg/db"
	"github.com/vislake/speed/pkg/event"
	"github.com/vislake/speed/pkg/internal/dbtest"
)

var base = time.Date(2026, time.March, 14, 12, 0, 0, 0, time.UTC)

const rootKey = "sso-tests-root-key-0123456789abcdef"

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

type recordingBus struct {
	mu       sync.Mutex
	events   []event.Event
	handlers map[string][]event.Handler
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
	b.events = append(b.events, evt)
	return nil
}

func (b *recordingBus) published() []event.Event {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]event.Event(nil), b.events...)
}

func (b *recordingBus) changes(idpID string) []string {
	var out []string
	for _, evt := range b.published() {
		if evt.Type != authn.EventIDPChanged {
			continue
		}
		var payload authn.IDPChanged
		if err := event.DecodePayload(evt.Payload, &payload); err != nil {
			continue
		}
		if payload.IDPID == idpID {
			out = append(out, payload.Change)
		}
	}
	return out
}

type rig struct {
	t        *testing.T
	p        *product
	h        *gorm.DB
	database db.Database
	bus      *recordingBus
	logs     *logBuffer
	clock    *fakeClock
	mounter  *mounttest.Mounter
	reader   config.Reader

	txt    map[string][]string
	txtErr map[string]error
}

func newRig(t *testing.T, database db.Database) *rig {
	t.Helper()
	return newRigAtBase(t, database, mounttest.DefaultBase)
}

func newRigAtBase(t *testing.T, database db.Database, basePath string) *rig {
	t.Helper()
	reader := authntest.ConfigReaderAll(t, "{}", Module())
	mounter := mounttest.New(t, mounttest.WithBase(basePath))
	p := build(database, mounter, resolved{}, reader)
	r := &rig{
		t: t, p: p, h: database.DB(), database: database,
		bus: newBus(), logs: &logBuffer{}, clock: newClock(base),
		mounter: mounter, reader: reader,
		txt: map[string][]string{}, txtErr: map[string]error{},
	}
	p.logger = r.logs.logger()
	p.bus = r.bus
	p.clock = r.clock.read
	p.lookup = r.lookup
	p.delivered = map[string]bool{authn.SSOProtocolOIDC: true, authn.SSOProtocolSAML: true}
	return r
}

func (r *rig) deliverProtocols(names ...string) {
	r.p.delivered = make(map[string]bool, len(names))
	for _, name := range names {
		r.p.delivered[name] = true
	}
}

func (r *rig) lookup(_ context.Context, name string) ([]string, error) {
	if err := r.txtErr[name]; err != nil {
		return nil, err
	}
	return r.txt[name], nil
}

func (r *rig) publishTXT(domain, token string) {
	r.txt[txtName(domain)] = []string{"speed-authn=" + token}
}

type standInSwitch struct {
	mu       sync.Mutex
	disabled []string
	err      error
	reads    int
}

// DisabledMethods reports what the case set, counting the read.
func (s *standInSwitch) DisabledMethods() ([]string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.reads++
	return append([]string(nil), s.disabled...), s.err
}

func (s *standInSwitch) calls() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.reads
}

func (s *standInSwitch) set(disabled []string, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.disabled = append([]string(nil), disabled...)
	s.err = err
}

func (r *rig) failTXT(domain string, err error) {
	r.txtErr[txtName(domain)] = err
}

func newRigSQLite(t *testing.T) *rig {
	t.Helper()
	return newRig(t, openSQLite(t))
}

func (r *rig) count(table string) int64 {
	r.t.Helper()
	var n int64
	if err := r.h.Table(table).Count(&n).Error; err != nil {
		r.t.Fatalf("counting %s: %v", table, err)
	}
	return n
}

const sqliteDialect = "sqlite"

type dialect struct {
	name string
	open func(t *testing.T) db.Database
}

var dialects = []dialect{{
	name: sqliteDialect,
	open: func(t *testing.T) db.Database {
		return dbtest.SQLite(t, filepath.Join(t.TempDir(), "sso.db"), dbtest.Items{"encryption-key": rootKey})
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

func openSQLite(t *testing.T) db.Database {
	t.Helper()
	database := dbtest.SQLite(t, filepath.Join(t.TempDir(), "sso.db"), dbtest.Items{"encryption-key": rootKey})
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

func oidcConfig() json.RawMessage {
	return json.RawMessage(`{"issuer":"https://idp.example.com","client_id":"client-1"}`)
}

func samlConfig() json.RawMessage {
	return json.RawMessage(`{"idp_entity_id":"https://idp.example.com/entity","idp_sso_url":"https://idp.example.com/sso","idp_certs":["-----BEGIN CERTIFICATE-----\nMIIB\n-----END CERTIFICATE-----"]}`)
}

func (r *rig) putSAML(tenant, name string) authn.SSOProvider {
	r.t.Helper()
	p, err := r.p.Put(context.Background(), authn.SSOProvider{
		Tenant:           tenant,
		Protocol:         authn.SSOProtocolSAML,
		Name:             name,
		Enabled:          true,
		Provisioning:     "jit",
		UsernameStrategy: "user-confirm",
		Config:           samlConfig(),
	}, nil)
	if err != nil {
		r.t.Fatalf("Put (create saml): %v", err)
	}
	return p
}

func (r *rig) putOIDC(tenant, name string) authn.SSOProvider {
	r.t.Helper()
	secret := "shh-" + name
	p, err := r.p.Put(context.Background(), authn.SSOProvider{
		Tenant:           tenant,
		Protocol:         "oidc",
		Name:             name,
		Enabled:          true,
		Provisioning:     "jit",
		UsernameStrategy: "user-confirm",
		Config:           oidcConfig(),
	}, &secret)
	if err != nil {
		r.t.Fatalf("Put (create oidc): %v", err)
	}
	return p
}
