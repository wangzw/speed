package admission

import (
	"context"
	"errors"
	"io/fs"
	"log/slog"
	"path/filepath"
	"strings"
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

// The cases here run the declaration through the settings module that builds
// the key table from it, over a SQLite database the database module opened: the
// assembly is refused if an item's key and its field's tag disagree, a write on
// either side of the lifetime's domain is judged as the code acts on it, a
// switch's effective value is the global row OR the tenant's row (the declared
// default applies only where neither holds one), and the layer reads what the
// store holds.

func TestTheSettingsAreDeclaredToTheStoreWithTheirDomainsAndDefaults(t *testing.T) {
	withDeliveredStore(t, func(ctx context.Context, store settings.Store) {
		declared := map[string]settings.DeclaredItem{}
		for _, item := range store.Declared() {
			declared[item.Key] = item
		}
		for _, key := range []string{requireMFAKey, requireSSOKey, checkTTLKey} {
			if declared[key].Module != ModuleName {
				t.Errorf("%s is declared by %q, want %q", key, declared[key].Module, ModuleName)
			}
		}
		for _, key := range []string{requireMFAKey, requireSSOKey} {
			if got, err := settings.Get[bool](store, settings.ForTenant(acme), key); err != nil || got {
				t.Errorf("%s reads %v (%v) with no row anywhere, want the declared default false", key, got, err)
			}
		}
		if got, err := settings.Get[time.Duration](store, settings.Global(), checkTTLKey); err != nil || got != 5*time.Second {
			t.Errorf("%s reads %v (%v), want the declared 5s", checkTTLKey, got, err)
		}

		for _, v := range []time.Duration{0, time.Second, maxCheckTTL} {
			if err := store.Set(ctx, settings.Write{Layer: settings.Global(), Key: checkTTLKey, Value: v, Actor: "test"}); err != nil {
				t.Errorf("writing %v failed: %v, want it stored", v, err)
			}
		}
		for _, v := range []time.Duration{-time.Nanosecond, maxCheckTTL + time.Nanosecond} {
			err := store.Set(ctx, settings.Write{Layer: settings.Global(), Key: checkTTLKey, Value: v, Actor: "test"})
			if !errors.Is(err, settings.ErrInvalidValue) || !strings.Contains(err.Error(), checkTTLKey) {
				t.Errorf("writing %v answered %v, want ErrInvalidValue naming the key", v, err)
			}
		}
	})
}

func TestTheLayerReadsTheSwitchesTheStoreHoldsPerTenant(t *testing.T) {
	withDeliveredStore(t, func(ctx context.Context, store settings.Store) {
		r := newRig(t)
		r.members.add(alice, globex)
		r.l.store = store

		// Nothing is written: both tenants are open to a single-factor member.
		for _, tenantID := range []string{acme, globex} {
			if rp := r.serve(r.as(tenantID)); !rp.reached() {
				t.Fatalf("with no row, %s answered %d, want it admitted", tenantID, rp.status)
			}
		}

		// A row for acme binds acme alone.
		write(t, ctx, store, settings.ForTenant(acme), requireMFAKey, true)
		stepUp(t, "acme with its own row", r.serve(r.as(acme)), acrMultiFactor)
		if rp := r.serve(r.as(globex)); !rp.reached() {
			t.Fatalf("globex answered %d, want it admitted: only acme has a row", rp.status)
		}

		// A global row binds every tenant, and a tenant's own row of false does
		// not lower that floor: the effective value is the OR of the two.
		write(t, ctx, store, settings.ForTenant(acme), requireMFAKey, false)
		write(t, ctx, store, settings.Global(), requireMFAKey, true)
		stepUp(t, "globex under the global row", r.serve(r.as(globex)), acrMultiFactor)
		stepUp(t, "acme under the global row despite its own false row", r.serve(r.as(acme)), acrMultiFactor)
	})
}

func write(t *testing.T, ctx context.Context, store settings.Store, layer settings.Layer, key string, value any) {
	t.Helper()
	if err := store.Set(ctx, settings.Write{Layer: layer, Key: key, Value: value, Actor: "test"}); err != nil {
		t.Fatalf("writing %s: %v", key, err)
	}
}

func TestTheLifetimeIsReadFromTheGlobalRowAlone(t *testing.T) {
	withDeliveredStore(t, func(ctx context.Context, store settings.Store) {
		r := newRig(t)
		r.l.store = store
		if got := r.l.checkTTL(); got != 5*time.Second {
			t.Fatalf("the lifetime is %v with no row, want the declared 5s", got)
		}
		write(t, ctx, store, settings.Global(), checkTTLKey, 12*time.Second)
		write(t, ctx, store, settings.ForTenant(acme), checkTTLKey, 40*time.Second)
		if got := r.l.checkTTL(); got != 12*time.Second {
			t.Fatalf("the lifetime is %v, want the global row's 12s and not a tenant's", got)
		}
	})
}

// withDeliveredStore runs body against the settings store the settings module
// delivers in an assembly that holds this module's declaration. This module is
// registered for its declaration alone, the way the key table reads every
// registered module's: it does not take part in the drive.
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
		New:      func(context.Context, *core.Registry) (any, error) { return acceptingBus{}, nil },
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

// applySettingsMigrations runs the settings module's own SQLite migrations, read
// out of the declaration it registers.
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

// decodesNothing is the configuration reader of an assembly that configures
// nothing, which leaves every declared default in place.
type decodesNothing struct{}

func (decodesNothing) Decode(string, any) error { return nil }

// discardingLogger is the logging capability of an assembly whose records nobody
// reads.
type discardingLogger struct{}

func (discardingLogger) Named(string) *slog.Logger { return slog.New(slog.DiscardHandler) }
func (discardingLogger) Redaction() log.Redaction  { return nil }

// acceptingBus takes every event and delivers none: the store publishes a change
// after each write, and nothing here subscribes to it.
type acceptingBus struct{}

func (acceptingBus) Publish(context.Context, event.Event) error { return nil }
func (acceptingBus) Subscribe(string, string, event.Handler)    {}
