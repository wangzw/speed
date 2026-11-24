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

type stubDatabase struct {
	handle  *gorm.DB
	dialect db.Dialect
}

func (d stubDatabase) DB() *gorm.DB                                   { return d.handle }
func (d stubDatabase) Dialect() db.Dialect                            { return d.dialect }
func (d stubDatabase) BlindIndex(string) string                       { return "" }
func (d stubDatabase) Open(context.Context, string) (*gorm.DB, error) { return nil, nil }

type recordingBus struct {
	mu        sync.Mutex
	published []event.Event
	handlers  map[string][]event.Handler
	failWith  error
}

func newRecordingBus() *recordingBus {
	return &recordingBus{handlers: make(map[string][]event.Handler)}
}

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

func (b *recordingBus) events() []event.Event {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]event.Event(nil), b.published...)
}

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

func discardLogger() *slog.Logger { return slog.New(slog.DiscardHandler) }

func quiet() *gorm.Config {
	return &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)}
}

func open(t *testing.T) *gorm.DB {
	t.Helper()
	handle := openMemory(t)
	applyMigration(t, handle, "sqlite/0001_settings.sql")
	return handle
}

func openMemory(t *testing.T) *gorm.DB {
	t.Helper()
	return quietly(dbtest.SQLite(t, filepath.Join(t.TempDir(), "settings.db"), nil).DB())
}

func quietly(handle *gorm.DB) *gorm.DB {
	return handle.Session(&gorm.Session{Logger: quiet().Logger})
}

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

func registryWith(t *testing.T, mods ...core.Module) *core.Registry {
	t.Helper()
	reg := core.New()
	for _, m := range mods {
		reg.Register(m)
	}
	return reg
}

func tableOf(t *testing.T, mods ...core.Module) *keyTable {
	t.Helper()
	keys, err := buildKeyTable(registryWith(t, mods...))
	if err != nil {
		t.Fatalf("building the key table: %v", err)
	}
	return keys
}

func sortedKeys(entries map[string]keyEntry) []string {
	out := make([]string, 0, len(entries))
	for key := range entries {
		out = append(out, key)
	}
	slices.Sort(out)
	return out
}

func declaring(name string, s Schema) core.Module {
	return core.Module{Name: name, Resources: []any{s}}
}

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

func storeOn(t *testing.T, handle *gorm.DB, dialect db.Dialect, keys *keyTable, bus event.Bus) *settingsStore {
	t.Helper()
	s := newSettingsStore(keys, stubDatabase{handle: handle, dialect: dialect}, bus, moduleSettings{})
	s.logger = discardLogger()
	if err := s.load(context.Background()); err != nil {
		t.Fatalf("loading the snapshot: %v", err)
	}
	return s
}

type dialectUnderTest struct {
	name    string
	dialect db.Dialect
	open    func(t *testing.T) *gorm.DB
}

var dialectsUnderTest = []dialectUnderTest{
	{name: "sqlite", dialect: db.SQLite, open: open},
}
