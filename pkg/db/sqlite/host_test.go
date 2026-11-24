package sqlite_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"testing/fstest"

	"github.com/vislake/speed/pkg/config"
	jsonformat "github.com/vislake/speed/pkg/config/format/json"
	filesource "github.com/vislake/speed/pkg/config/source/file"
	"github.com/vislake/speed/pkg/core"
	"github.com/vislake/speed/pkg/db"
	"github.com/vislake/speed/pkg/db/sqlite"
)

const hostIdentityPrefix = "SPEEDDBSQLITETEST"

const createWidgets = `CREATE TABLE widgets (id INTEGER NOT NULL, PRIMARY KEY (id))`

// TestTheSQLiteModuleDeliversTheCapability starts a real assembly around this subpackage's module
// and looks at what it delivered.
func TestTheSQLiteModuleDeliversTheCapability(t *testing.T) {
	dsn := filepath.Join(t.TempDir(), "delivered.db")
	body := fmt.Sprintf(`{"db":{"sqlite":{"dsn":%q}}}`, dsn)

	probe := newProbe()
	reg := assembly(t, body,
		sqlite.Module(),
		core.Module{Name: "catalog", Resources: []any{db.Migrations{FS: fstest.MapFS{
			"sqlite/0001_create_widgets.sql": &fstest.MapFile{Data: []byte(createWidgets)},
		}}}},
		probe.module(),
	)

	// The command line is the process's and a test binary always carries
	// arguments of its own, so they are taken away while the tree reads them.
	// The cases here therefore do not run in parallel.
	previous := os.Args
	os.Args = []string{"sqlite.test"}

	app := config.NewApp(reg)
	app.Root().IncludeAll()
	app.Root().Do(core.StageServe, nil)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- app.Execute(ctx) }()

	var instance db.Database
	select {
	case instance = <-probe.delivered:
	case err := <-done:
		os.Args = previous
		t.Fatalf("the assembly did not deliver the database capability: %v", err)
	}
	select {
	case err := <-done:
		os.Args = previous
		t.Fatalf("the assembly shut down before its Init stage: %v", err)
	case <-probe.migrated:
	}
	os.Args = previous

	if got := instance.Dialect(); got != db.SQLite {
		t.Errorf("the delivered capability reports the dialect %q, want %q", got, db.SQLite)
	}
	if _, err := os.Stat(dsn); err != nil {
		t.Errorf("no database was opened at the configured locator: %v", err)
	}
	if !instance.DB().Migrator().HasTable("widgets") {
		t.Error("the migration this assembly declared under the sqlite subdirectory was not applied, " +
			"so either the migration stage is not wired to this module or its dialect does not name " +
			"that subdirectory")
	}

	close(probe.gate)
	cancel()
	if err := <-done; err != nil {
		t.Errorf("the assembly shut down with an error: %v", err)
	}
}

func load(t *testing.T, body string, modules ...core.Module) (config.Reader, error) {
	t.Helper()
	return config.Load(t.Context(), assembly(t, body, modules...), nil)
}

func assembly(t *testing.T, body string, modules ...core.Module) *core.Registry {
	t.Helper()
	locator := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(locator, []byte(body), 0o600); err != nil {
		t.Fatalf("writing the primary config source: %v", err)
	}

	reg := core.New()
	reg.Register(config.Module())
	reg.Register(jsonformat.Module())
	reg.Register(filesource.Module())
	reg.Register(core.Module{
		Name:      "host",
		Resources: []any{config.HostIdentity{Prefix: hostIdentityPrefix, DefaultLocator: "file://" + locator}},
	})
	for _, module := range modules {
		reg.Register(module)
	}
	return reg
}

type probe struct {
	delivered chan db.Database
	migrated  chan struct{}
	gate      chan struct{}
}

func newProbe() *probe {
	return &probe{
		delivered: make(chan db.Database, 1),
		migrated:  make(chan struct{}),
		gate:      make(chan struct{}),
	}
}

func (p *probe) module() core.Module {
	return core.Module{
		Name:     "probe",
		Requires: []core.Requirement{{Token: (*db.Database)(nil)}},
		New: func(_ context.Context, reg *core.Registry) (any, error) {
			instance, err := core.Resolve[db.Database](reg)
			if err != nil {
				return nil, err
			}
			p.delivered <- instance
			return nil, nil
		},
		Init: func(context.Context, *core.Registry, any) error {
			close(p.migrated)
			<-p.gate
			return nil
		},
	}
}
