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
	"github.com/vislake/speed/pkg/internal/pgpool/pgtest"
)

// base is the instant the cases that need a fixed time start from.
var base = time.Date(2026, time.March, 14, 12, 0, 0, 0, time.UTC)

// rootKey is the database root key a rig configures, so the encrypted column has
// a key. It is long enough for the database module's floor.
const rootKey = "sso-tests-root-key-0123456789abcdef"

// fakeClock is a clock a case moves. It reads in the zone it was built with, so a
// case can hand the module a time that is not UTC.
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

// recordingBus is a bus that keeps what was published, for a case to read.
type recordingBus struct {
	mu       sync.Mutex
	events   []event.Event
	handlers map[string][]event.Handler
}

func newBus() *recordingBus {
	return &recordingBus{handlers: map[string][]event.Handler{}}
}

func (b *recordingBus) Subscribe(eventType string, h event.Handler) {
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

// changes returns the change values published for one provider.
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

// rig is the module over a real database with every dependency a case drives.
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

	// txt is the TXT record set the fake resolver answers with, and txtErr the
	// names whose lookup fails.
	txt    map[string][]string
	txtErr map[string]error
}

// newRig builds the module over the handle with a root key configured.
func newRig(t *testing.T, database db.Database) *rig {
	t.Helper()
	reader := authntest.ConfigReaderAll(t, "{}", Module())
	mounter := mounttest.New(t)
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
	return r
}

// lookup is the resolver a case scripts. It answers the named set, or the error
// the case put on the name.
func (r *rig) lookup(_ context.Context, name string) ([]string, error) {
	if err := r.txtErr[name]; err != nil {
		return nil, err
	}
	return r.txt[name], nil
}

// publishTXT makes a name answer the given records.
func (r *rig) publishTXT(domain, token string) {
	r.txt[txtName(domain)] = []string{"speed-authn=" + token}
}

func (r *rig) failTXT(domain string, err error) {
	r.txtErr[txtName(domain)] = err
}

// newRigSQLite is newRig over a fresh SQLite database with the tables built.
func newRigSQLite(t *testing.T) *rig {
	t.Helper()
	return newRig(t, openSQLite(t))
}

// count counts the rows of one of the module's tables.
func (r *rig) count(table string) int64 {
	r.t.Helper()
	var n int64
	if err := r.h.Table(table).Count(&n).Error; err != nil {
		r.t.Fatalf("counting %s: %v", table, err)
	}
	return n
}

// The dialects the module runs on.
const (
	sqliteDialect   = "sqlite"
	postgresDialect = "postgres"
)

// applyMigrations builds the module's tables from the files it ships.
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

// openSQLite delivers the SQLite database with the module's tables built and a
// root key configured.
func openSQLite(t *testing.T) db.Database {
	t.Helper()
	database := dbtest.SQLite(t, filepath.Join(t.TempDir(), "sso.db"), dbtest.Items{"encryption-key": rootKey})
	handle := database.DB().Session(&gorm.Session{Logger: logger.Discard})
	applyMigrations(t, handle, sqliteDialect)
	return database
}

// openDialect delivers the database of the dialect with the module's tables
// built.
func openDialect(t *testing.T, dialect string) db.Database {
	t.Helper()
	var database db.Database
	switch dialect {
	case sqliteDialect:
		database = dbtest.SQLite(t, filepath.Join(t.TempDir(), "sso.db"), dbtest.Items{"encryption-key": rootKey})
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
// skipped on a machine without docker.
func forEachDialect(t *testing.T, run func(t *testing.T, database db.Database, dialect string)) {
	t.Helper()
	for _, dialect := range []string{sqliteDialect, postgresDialect} {
		t.Run(dialect, func(t *testing.T) {
			run(t, openDialect(t, dialect), dialect)
		})
	}
}

// oidcConfig is a minimal valid OIDC config.
func oidcConfig() json.RawMessage {
	return json.RawMessage(`{"issuer":"https://idp.example.com","client_id":"client-1"}`)
}

// samlConfig is a minimal valid SAML config.
func samlConfig() json.RawMessage {
	return json.RawMessage(`{"idp_entity_id":"https://idp.example.com/entity","idp_sso_url":"https://idp.example.com/sso","idp_certs":["-----BEGIN CERTIFICATE-----\nMIIB\n-----END CERTIFICATE-----"]}`)
}

// putOIDC creates an enabled OIDC provider and returns it.
func (r *rig) putOIDC(tenant, name string) authn.SSOProvider {
	r.t.Helper()
	secret := "shh-" + name
	p, err := r.p.Put(context.Background(), authn.SSOProvider{
		Tenant:       tenant,
		Protocol:     "oidc",
		Name:         name,
		Enabled:      true,
		Provisioning: "jit",
		Config:       oidcConfig(),
	}, &secret)
	if err != nil {
		r.t.Fatalf("Put (create oidc): %v", err)
	}
	return p
}
