package static

import (
	"context"
	"errors"
	"io/fs"
	"log/slog"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"gorm.io/gorm"

	"github.com/vislake/speed/pkg/config"
	"github.com/vislake/speed/pkg/core"
	"github.com/vislake/speed/pkg/db"
	"github.com/vislake/speed/pkg/event"
	"github.com/vislake/speed/pkg/internal/dbtest"
	"github.com/vislake/speed/pkg/log"
	"github.com/vislake/speed/pkg/settings"
)

// TestTheSettingsAreDeclaredToTheStoreWithTheirDomains holds the key-tag agreement, the domain edges and the read value.
func TestTheSettingsAreDeclaredToTheStoreWithTheirDomains(t *testing.T) {
	withDeliveredStore(t, func(ctx context.Context, store settings.Store) {
		b := tableWith(t, map[string]string{"alice": "secret"})
		b.store = store

		for _, c := range []struct {
			path    string
			inside  []any
			outside []any
			read    func() any
		}{
			{
				cacheTTLPath,
				[]any{time.Duration(0), 5 * time.Minute, 90 * time.Second},
				[]any{-time.Nanosecond, 5*time.Minute + time.Nanosecond},
				func() any { return b.cacheTTL() },
			},
			{
				freeAttemptsPath,
				[]any{1, 20, 7},
				[]any{0, 21, -1},
				func() any { return b.throttleParams().FreeAttempts },
			},
			{
				failWindowPath,
				[]any{time.Minute, 24 * time.Hour, 30 * time.Minute},
				[]any{time.Minute - time.Nanosecond, 24*time.Hour + time.Nanosecond},
				func() any { return b.throttleParams().Window },
			},
			{
				maxBackoffPath,
				[]any{10 * time.Second, 24 * time.Hour, time.Hour},
				[]any{10*time.Second - time.Nanosecond, 24*time.Hour + time.Nanosecond},
				func() any { return b.throttleParams().MaxBackoff },
			},
		} {
			for _, v := range c.inside {
				write := settings.Write{Layer: settings.Global(), Key: c.path, Value: v, Actor: "test"}
				if err := store.Set(ctx, write); err != nil {
					t.Errorf("%s: writing %v failed: %v, want it stored", c.path, v, err)
					continue
				}
				if got := c.read(); got != v {
					t.Errorf("%s: the store holds %v and the backend reads %v", c.path, v, got)
				}
			}
			for _, v := range c.outside {
				write := settings.Write{Layer: settings.Global(), Key: c.path, Value: v, Actor: "test"}
				err := store.Set(ctx, write)
				if !errors.Is(err, settings.ErrInvalidValue) {
					t.Errorf("%s: writing %v answered %v, want ErrInvalidValue", c.path, v, err)
				} else if !strings.Contains(err.Error(), c.path) {
					t.Errorf("%s: the refusal %q does not name the key", c.path, err)
				}
			}
		}
	})
}

func withDeliveredStore(t *testing.T, body func(ctx context.Context, store settings.Store)) {
	t.Helper()
	database := dbtest.SQLite(t, filepath.Join(t.TempDir(), "settings.db"), nil)
	applySettingsMigrations(t, database.DB())

	reg := core.New()
	reg.Register(config.Module())
	reg.Supply(decodesNothing{}, config.ModuleName)
	reg.Register(log.Module())
	reg.Supply(discardingLogger{}, log.ModuleName)
	reg.Register(core.Module{
		Name:     "test.database",
		Provides: []core.Provision{{Token: (*db.Database)(nil), Exclusive: true}},
		New:      func(context.Context, *core.Registry) (any, error) { return database, nil },
	})
	reg.Register(core.Module{
		Name:     "test.event",
		Provides: []core.Provision{{Token: (*event.Bus)(nil), Exclusive: true}},
		New:      func(context.Context, *core.Registry) (any, error) { return &acceptingBus{}, nil },
	})
	reg.Register(settings.Module())
	reg.Register(Module())

	err := reg.Drive(context.Background(), core.Drive{
		Depth:        core.StageInit,
		Participants: []string{"test.database", "test.event", settings.ModuleName},
		Action: func(ctx context.Context, reg *core.Registry) error {
			store, err := core.Resolve[settings.Store](reg)
			if err != nil {
				return err
			}
			body(ctx, store)
			return nil
		},
	})
	if err != nil {
		t.Fatalf("driving the settings module: %v", err)
	}
}

func applySettingsMigrations(t *testing.T, handle *gorm.DB) {
	t.Helper()
	var tree fs.FS
	for _, resource := range settings.Module().Resources {
		if declared, ok := resource.(db.Migrations); ok {
			tree = declared.FS
		}
	}
	if tree == nil {
		t.Fatal("the settings module declares no migrations")
	}
	files, err := fs.Glob(tree, string(db.SQLite)+"/*.sql")
	if err != nil || len(files) == 0 {
		t.Fatalf("finding the settings module's SQLite migrations: %v (%d files)", err, len(files))
	}
	for _, name := range files {
		sql, err := fs.ReadFile(tree, name)
		if err != nil {
			t.Fatalf("reading %s: %v", name, err)
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
}

type decodesNothing struct{}

func (decodesNothing) Decode(string, any) error { return nil }

type discardingLogger struct{}

func (discardingLogger) Named(string) *slog.Logger { return slog.New(slog.DiscardHandler) }
func (discardingLogger) Redaction() log.Redaction  { return noopRedaction{} }

type acceptingBus struct {
	mu        sync.Mutex
	published int
}

func (b *acceptingBus) Publish(context.Context, event.Event) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.published++
	return nil
}

func (b *acceptingBus) Subscribe(string, string, event.Handler) {}
