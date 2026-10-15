package settings

import (
	"context"
	"io/fs"
	"log/slog"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"

	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"github.com/vislake/speed/pkg/core"
	"github.com/vislake/speed/pkg/db"
	"github.com/vislake/speed/pkg/event"
	"github.com/vislake/speed/pkg/internal/dbtest"
)

// stubDatabase is the database capability, backed by a real handle.
//
// The module only ever calls DB and Dialect, so the rest of the interface is
// there to satisfy it and nothing more.
type stubDatabase struct {
	handle  *gorm.DB
	dialect db.Dialect
}

func (d stubDatabase) DB() *gorm.DB                                   { return d.handle }
func (d stubDatabase) Dialect() db.Dialect                            { return d.dialect }
func (d stubDatabase) BlindIndex(string) string                       { return "" }
func (d stubDatabase) Open(context.Context, string) (*gorm.DB, error) { return nil, nil }

// recordingBus is an event bus that keeps what it was given and lets a test
// drive the handlers directly.
//
// It exists rather than the memory module because the memory bus's constructor
// is unexported: a test outside that package cannot build one, and assembling
// the module through a registry would make every case here an integration
// test.
type recordingBus struct {
	mu        sync.Mutex
	published []event.Event
	handlers  map[string][]event.Handler
	failWith  error
}

func newRecordingBus() *recordingBus {
	return &recordingBus{handlers: make(map[string][]event.Handler)}
}

// Publish refuses a context that is already done, which is what the bus
// contract holds every backend to, and records the event otherwise.
func (b *recordingBus) Publish(ctx context.Context, evt event.Event) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.failWith != nil {
		return b.failWith
	}
	b.published = append(b.published, evt)
	return nil
}

func (b *recordingBus) Subscribe(eventType string, _ string, h event.Handler) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.handlers[eventType] = append(b.handlers[eventType], h)
}

// events returns what has been published so far.
func (b *recordingBus) events() []event.Event {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]event.Event(nil), b.published...)
}

// deliver runs every handler subscribed to a type, the way a backend would.
func (b *recordingBus) deliver(t *testing.T, eventType string, evt event.Event) {
	t.Helper()
	b.mu.Lock()
	handlers := append([]event.Handler(nil), b.handlers[eventType]...)
	b.mu.Unlock()
	for _, h := range handlers {
		if err := h(context.Background(), evt); err != nil {
			t.Fatalf("the subscription handler failed: %v", err)
		}
	}
}

// discardLogger swallows the records the module writes when a reload or a
// publish fails, several of which the cases here provoke on purpose.
func discardLogger() *slog.Logger { return slog.New(slog.DiscardHandler) }

// quiet is the configuration every handle in these tests opens with: GORM
// prints a statement that failed, and several cases here fail one on purpose.
func quiet() *gorm.Config {
	return &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)}
}

// open builds a handle with the settings table created from this module's own
// migration file, so the table the cases write into is the one the module
// ships rather than a copy written out here.
func open(t *testing.T) *gorm.DB {
	t.Helper()
	handle := openMemory(t)
	applyMigration(t, handle, "sqlite/0001_settings.sql")
	return handle
}

// openMemory builds a handle on a database of the case's own, with no table in
// it, which every connection of the handle's pool shares.
//
// It is the handle the database module delivers over a SQLite file, rather than
// one opened here, so that the store runs over what an assembly hands it: the
// constraint classification the module installs, and the pool shape it gives
// the locator. The session handed back writes no record of the statements a
// case fails on purpose.
func openMemory(t *testing.T) *gorm.DB {
	t.Helper()
	return quietly(dbtest.SQLite(t, filepath.Join(t.TempDir(), "settings.db"), nil).DB())
}

// quietly is a session of a delivered handle that writes no record of a failed
// statement.
func quietly(handle *gorm.DB) *gorm.DB {
	return handle.Session(&gorm.Session{Logger: quiet().Logger})
}

// applyMigration runs one of the module's own migration files.
func applyMigration(t *testing.T, handle *gorm.DB, name string) {
	t.Helper()
	sql, err := fs.ReadFile(migrationsFS, name)
	if err != nil {
		t.Fatalf("reading the migration %s: %v", name, err)
	}
	for statement := range strings.SplitSeq(string(sql), ";") {
		if strings.TrimSpace(statement) == "" {
			continue
		}
		if err := handle.Exec(statement).Error; err != nil {
			t.Fatalf("applying %s: %v", name, err)
		}
	}
}

// registryWith registers the given modules in a registry of its own.
func registryWith(t *testing.T, mods ...core.Module) *core.Registry {
	t.Helper()
	reg := core.New()
	for _, m := range mods {
		reg.Register(m)
	}
	return reg
}

// tableOf builds a key table from hand-written declarations, through the same
// collection path the module uses.
func tableOf(t *testing.T, mods ...core.Module) *keyTable {
	t.Helper()
	keys, err := buildKeyTable(registryWith(t, mods...))
	if err != nil {
		t.Fatalf("building the key table: %v", err)
	}
	return keys
}

// sortedKeys lists a table's keys in a fixed order, for a failure message that
// reads the same every run.
func sortedKeys(entries map[string]keyEntry) []string {
	out := make([]string, 0, len(entries))
	for key := range entries {
		out = append(out, key)
	}
	slices.Sort(out)
	return out
}

// declaring wraps one declaration as a registered module.
func declaring(name string, s Schema) core.Module {
	return core.Module{Name: name, Resources: []any{s}}
}

// storeOf builds a store over an open handle and a key table, with the
// snapshot already loaded. It is the state Init leaves behind.
func storeOf(t *testing.T, keys *keyTable, bus event.Bus) (*settingsStore, *gorm.DB) {
	t.Helper()
	handle := open(t)
	s := newSettingsStore(keys, stubDatabase{handle: handle, dialect: db.SQLite}, bus, moduleSettings{})
	s.logger = discardLogger()
	if err := s.load(context.Background()); err != nil {
		t.Fatalf("loading the snapshot: %v", err)
	}
	return s, handle
}
