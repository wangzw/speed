package policy_test

import (
	"context"
	"errors"
	"io/fs"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"gorm.io/gorm"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/authz"
	"github.com/vislake/speed/pkg/authz/authztest"
	"github.com/vislake/speed/pkg/authz/policy"
	"github.com/vislake/speed/pkg/config"
	"github.com/vislake/speed/pkg/core"
	"github.com/vislake/speed/pkg/db"
	"github.com/vislake/speed/pkg/event"
	"github.com/vislake/speed/pkg/internal/dbtest"
	"github.com/vislake/speed/pkg/settings"
)

func policyAssembly(t *testing.T, body string) (*core.Registry, db.Database, []string) {
	t.Helper()
	database := dbtest.SQLite(t, filepath.Join(t.TempDir(), "authz.db"), nil)
	createTables(t, database.DB())

	module := policy.Module()
	reader := authztest.ConfigReaderAll(t, body, module)
	stand := dbStandIn{name: "stand-in.database", base: database}

	reg := core.New()
	reg.Register(config.Module())
	reg.Supply(reader, config.ModuleName)
	reg.Register(module)
	reg.Register(stand.module())
	return reg, database, []string{module.Name, stand.name}
}

// TestTheConfiguredRefreshIntervalDrivesThePeriodicReload checks that the configured refresh interval drives the periodic reload.
func TestTheConfiguredRefreshIntervalDrivesThePeriodicReload(t *testing.T) {
	reg, database, names := policyAssembly(t, `{"authz":{"policy":{"refresh-interval":"5ms"}}}`)

	err := reg.Drive(t.Context(), core.Drive{
		Depth:        core.StageStart,
		Participants: names,
		Action: func(ctx context.Context, reg *core.Registry) error {
			enforcer, err := core.Resolve[authz.Enforcer](reg)
			if err != nil {
				return err
			}
			query := authz.Query{Subject: "acc1", Domain: authz.Domain("acme"), Object: "account", Action: "read"}
			allowed, err := enforcer.Allowed(ctx, query)
			if err != nil {
				return err
			}
			if allowed {
				return errors.New("an empty policy allowed a query before any row was written")
			}

			plantPolicy(t, database.DB(), "acme", "roleA", "acc1", "account", "read")
			deadline := time.Now().Add(3 * time.Second)
			for time.Now().Before(deadline) {
				allowed, err = enforcer.Allowed(ctx, query)
				if err != nil {
					return err
				}
				if allowed {
					return nil
				}
				time.Sleep(2 * time.Millisecond)
			}
			return errors.New("the configured refresh interval never reloaded the snapshot")
		},
	})
	if err != nil {
		t.Fatalf("the assembly with a configured refresh interval did not run: %v", err)
	}
}

// TestANegativeConfiguredRefreshIntervalIsRefusedAtStartup checks that a negative configured refresh interval is refused at startup.
func TestANegativeConfiguredRefreshIntervalIsRefusedAtStartup(t *testing.T) {
	reg, _, names := policyAssembly(t, `{"authz":{"policy":{"refresh-interval":"-1s"}}}`)

	err := reg.Drive(t.Context(), core.Drive{
		Depth:        core.StageStart,
		Participants: names,
	})
	if !errors.Is(err, authn.ErrInvalidConfig) {
		t.Fatalf("a negative refresh interval ended the startup with %v, want ErrInvalidConfig", err)
	}
	if !strings.Contains(err.Error(), "authz.policy.refresh-interval") {
		t.Errorf("the refusal does not name the item: %v", err)
	}
}

// TestTheSettingsStoreNoLongerDeclaresTheRefreshInterval checks that the settings store no longer declares the refresh interval.
func TestTheSettingsStoreNoLongerDeclaresTheRefreshInterval(t *testing.T) {
	store := settingsStoreOver(t, policy.Module())
	err := store.Set(context.Background(), settings.Write{
		Layer: settings.Global(),
		Key:   "authz.policy.refresh-interval",
		Value: time.Second,
		Actor: "test",
	})
	if !errors.Is(err, settings.ErrUnknownKey) {
		t.Fatalf("writing authz.policy.refresh-interval through the settings store returned %v, want ErrUnknownKey", err)
	}
}

func plantPolicy(t *testing.T, handle *gorm.DB, tenant, role, account, object, action string) {
	t.Helper()
	now := time.Now().UTC()
	for _, statement := range []struct {
		sql  string
		args []any
	}{
		{
			"INSERT INTO authz_roles (tenant_id, role, builtin, description_key, created_at) VALUES (?,?,?,?,?)",
			[]any{tenant, role, false, "k", now},
		},
		{
			"INSERT INTO authz_grants (tenant_id, role, resource, action, created_at) VALUES (?,?,?,?,?)",
			[]any{tenant, role, object, action, now},
		},
		{
			"INSERT INTO authz_bindings (tenant_id, account_id, role, granted_at) VALUES (?,?,?,?)",
			[]any{tenant, account, role, now},
		},
	} {
		if err := handle.Exec(statement.sql, statement.args...).Error; err != nil {
			t.Fatalf("planting the policy: %v", err)
		}
	}
}

func settingsStoreOver(t *testing.T, declaring ...core.Module) settings.Store {
	t.Helper()
	database := dbtest.SQLite(t, filepath.Join(t.TempDir(), "settings.db"), nil)
	applySettingsMigrations(t, database.DB())

	stand := dbStandIn{name: "stand-in.database", base: database}
	reg := core.New()
	reg.Register(config.Module())
	reg.Supply(decodesNothing{}, config.ModuleName)
	reg.Register(stand.module())
	supply(reg, "stand-in.event", (*event.Bus)(nil), acceptingBus{})
	reg.Register(settings.Module())
	for _, module := range declaring {
		reg.Register(module)
	}

	var store settings.Store
	err := reg.Drive(context.Background(), core.Drive{
		Depth:        core.StageInit,
		Participants: []string{stand.name, "stand-in.event", settings.ModuleName},
		Action: func(_ context.Context, reg *core.Registry) error {
			var err error
			store, err = core.Resolve[settings.Store](reg)
			return err
		},
	})
	if err != nil {
		t.Fatalf("driving the settings module: %v", err)
	}
	return store
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

type acceptingBus struct{}

func (acceptingBus) Publish(context.Context, event.Event) error { return nil }
func (acceptingBus) Subscribe(string, string, event.Handler)    {}
