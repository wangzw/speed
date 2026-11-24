package ratelimit

import (
	"context"
	"errors"
	"io/fs"
	"log/slog"
	"path/filepath"
	"strconv"
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

type thresholdValue struct {
	name   string
	key    string
	value  any
	inside bool
}

var thresholdValues = []thresholdValue{
	{"a limit of one", limitPath, 1, true},
	{"the default limit", limitPath, 100, true},
	{"a limit of zero", limitPath, 0, false},
	{"a negative limit", limitPath, -5, false},
	{"a limit of zero as text", limitPath, "0", false},
	{"a negative limit as text", limitPath, "-5", false},
	{"a limit of zero decoded from JSON", limitPath, 0.0, false},
	{"a limit decoded from JSON", limitPath, 3.0, true},
	{"a window of one nanosecond", windowPath, time.Nanosecond, true},
	{"a window of a minute as text", windowPath, "1m", true},
	{"the longest window", windowPath, maxWindow, true},
	{"a window of zero", windowPath, time.Duration(0), false},
	{"a window of zero as text", windowPath, "0s", false},
	{"a negative window", windowPath, "-1m", false},
	{"a window one past the longest", windowPath, maxWindow + 1, false},
}

// TestAThresholdOutsideItsDomainIsRefusedWhereItIsWritten checks that the store
// refuses and the layer stands aside on the same out-of-domain values.
func TestAThresholdOutsideItsDomainIsRefusedWhereItIsWritten(t *testing.T) {
	withDeliveredStore(t, func(ctx context.Context, store settings.Store) {
		layer := settings.ForTenant("acme")
		for _, v := range thresholdValues {
			err := store.Set(ctx, settings.Write{Layer: layer, Key: v.key, Value: v.value, Actor: "test"})
			switch {
			case v.inside && err != nil:
				t.Errorf("%s: writing %v to %s failed: %v, want it stored", v.name, v.value, v.key, err)
			case !v.inside && !errors.Is(err, settings.ErrInvalidValue):
				t.Errorf("%s: writing %v to %s answered %v, want ErrInvalidValue", v.name, v.value, v.key, err)
			case !v.inside && !strings.Contains(err.Error(), v.key):
				t.Errorf("%s: the refusal %q does not name the key %s", v.name, err, v.key)
			}

			tripped := standsAsideOn(v)
			if tripped == v.inside {
				t.Errorf("%s: the layer stands aside on %v: %v, while the store takes it: %v",
					v.name, v.value, tripped, v.inside)
			}
		}
	})
}

// TestARefusedLimitLeavesTheStoredOneInForce checks that a refused write leaves
// the stored limit and the layer keeps limiting by it.
func TestARefusedLimitLeavesTheStoredOneInForce(t *testing.T) {
	withDeliveredStore(t, func(ctx context.Context, store settings.Store) {
		layer := settings.ForTenant("acme")
		if err := store.Set(ctx, settings.Write{Layer: layer, Key: limitPath, Value: 3}); err != nil {
			t.Fatalf("writing the limit: %v", err)
		}
		for _, refused := range []any{0, -5} {
			if err := store.Set(ctx, settings.Write{Layer: layer, Key: limitPath, Value: refused}); !errors.Is(err, settings.ErrInvalidValue) {
				t.Errorf("writing a limit of %v answered %v, want ErrInvalidValue", refused, err)
			}
		}
		rows, err := store.List(ctx, layer)
		if err != nil {
			t.Fatalf("listing the tenant's rows: %v", err)
		}
		if len(rows) != 1 || rows[0].Key != limitPath || rows[0].Value != "3" || rows[0].Version != 1 {
			t.Errorf("the tenant's rows after the refused writes are %+v, want the one limit of 3 at version 1", rows)
		}

		l := pinned()
		l.store = store
		var d decision
		window, limit := l.thresholds(layer, &d)
		if d.tripped != "" || limit != 3 || window != defaults.Window {
			t.Errorf("the layer reads window %v and limit %d (tripped %q), want %v and 3",
				window, limit, d.tripped, defaults.Window)
		}
	})
}

func standsAsideOn(v thresholdValue) bool {
	values := map[string]any{windowPath: defaults.Window, limitPath: defaults.Limit}
	held, ok := heldAsDeclared(v)
	if !ok {
		return false
	}
	values[v.key] = held
	l := newLimiter(nil)
	l.store = &fakeStore{values: values}
	var d decision
	l.thresholds(settings.Global(), &d)
	return d.tripped == reasonThresholdDomain
}

func heldAsDeclared(v thresholdValue) (any, bool) {
	switch value := v.value.(type) {
	case int:
		return value, v.key == limitPath
	case float64:
		return int(value), v.key == limitPath
	case time.Duration:
		return value, v.key == windowPath
	case string:
		if v.key == windowPath {
			parsed, err := time.ParseDuration(value)
			return parsed, err == nil
		}
		parsed, err := strconv.Atoi(value)
		return parsed, err == nil
	}
	return nil, false
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
func (discardingLogger) Redaction() log.Redaction  { return nil }

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
