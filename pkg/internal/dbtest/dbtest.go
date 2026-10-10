// Package dbtest delivers the database capability to a test the way an assembly
// delivers it: the engine's own module, driven through its New stage in a
// registry of its own over a configuration loaded by the real loader, with the
// handle it opened kept open until the test ends.
//
// A handle a test opens with gorm.Open is not one the database module opened,
// and it is not a stand-in for one: it carries none of what the module installs
// on the handles it opens — the engine marker, the constraint classification,
// the field encryption — and none of the pool shape the module gives a locator,
// such as the one connection of an in-memory database. A module is exercised on
// a handle from here where what it meets is what an assembly hands it.
//
// A handle delivered here comes out of an assembly of the engine's module and
// the config module only, with no log module in it, so its statement records go
// through the process default logger, to standard output at the bootstrap level,
// stamped with the engine module's name as the module attribute.
//
// No migration is applied: only the engine's module takes part, beside the
// config module the reader is supplied to, and a test builds the tables it
// needs.
package dbtest

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vislake/speed/pkg/config"
	jsonformat "github.com/vislake/speed/pkg/config/format/json"
	filesource "github.com/vislake/speed/pkg/config/source/file"
	"github.com/vislake/speed/pkg/core"
	"github.com/vislake/speed/pkg/db"
	"github.com/vislake/speed/pkg/db/postgres"
	"github.com/vislake/speed/pkg/db/sqlite"
)

// hostPrefix names the environment variables the configuration would be read
// from. No machine carries variables under it, so the section a test writes is
// the whole of what configures the engine.
const hostPrefix = "SPEEDDBTESTFIXTURE"

// Items are input items of the engine's section beside its locator, by key:
// "max-open-conns", "max-idle-conns", "conn-max-lifetime" and the rest the
// engine declares. A nil Items configures the locator alone.
type Items map[string]any

// SQLite delivers the capability of the SQLite engine over the locator dsn,
// with the items configured beside it.
func SQLite(t testing.TB, dsn string, items Items) db.Database {
	t.Helper()
	return deliver(t, sqlite.Module(), sqlite.ConfigNamespace, dsn, items)
}

// Postgres delivers the capability of the PostgreSQL engine over the database
// the locator dsn names — one the test took from
// pkg/internal/pgpool/pgtest — with the items configured beside it. Each call
// is an assembly of its own: two of them on one locator are two processes' pools
// on one database, and neither is a connection built with Open from the other.
func Postgres(t testing.TB, dsn string, items Items) db.Database {
	t.Helper()
	return deliver(t, postgres.Module(), postgres.ConfigNamespace, dsn, items)
}

// deliver drives the engine's module through New and hands back what it
// delivered. The drive's action holds the lifecycle there until the test ends,
// and the module's Close then releases the pool.
func deliver(t testing.TB, module core.Module, namespace, dsn string, items Items) db.Database {
	t.Helper()
	reader := load(t, module, namespace, dsn, items)
	reg := core.New()
	reg.Register(config.Module())
	reg.Register(module)
	reg.Supply(reader, config.ModuleName)

	delivered := make(chan db.Database, 1)
	release := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		done <- reg.Drive(context.Background(), core.Drive{
			Depth:        core.StageNew,
			Participants: []string{module.Name},
			Action: func(_ context.Context, reg *core.Registry) error {
				database, err := core.Resolve[db.Database](reg)
				if err != nil {
					return err
				}
				delivered <- database
				<-release
				return nil
			},
		})
	}()
	select {
	case database := <-delivered:
		t.Cleanup(func() {
			close(release)
			if err := <-done; err != nil {
				t.Errorf("shutting the %s assembly down: %v", module.Name, err)
			}
		})
		return database
	case err := <-done:
		if err == nil {
			err = errors.New("the drive ended without delivering the capability")
		}
		t.Fatalf("delivering the %s capability: %v", module.Name, err)
		return nil
	}
}

// load reads the engine's section the way a host's run reads it: through the
// configuration loader, from a JSON primary source holding the locator and the
// items under the engine's namespace, with an empty command line.
func load(t testing.TB, module core.Module, namespace, dsn string, items Items) config.Reader {
	t.Helper()
	section := map[string]any{"dsn": dsn}
	for key, value := range items {
		section[key] = value
	}
	var body any = section
	parts := strings.Split(namespace, ".")
	for i := len(parts) - 1; i >= 0; i-- {
		body = map[string]any{parts[i]: body}
	}
	encoded, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("encoding the %s section: %v", namespace, err)
	}
	locator := filepath.Join(t.TempDir(), "config.json")
	if err = os.WriteFile(locator, encoded, 0o600); err != nil {
		t.Fatalf("writing the primary config source: %v", err)
	}
	loading := core.New()
	loading.Register(jsonformat.Module())
	loading.Register(filesource.Module())
	loading.Register(core.Module{
		Name:      "host",
		Resources: []any{config.HostIdentity{Prefix: hostPrefix, DefaultLocator: "file://" + locator}},
	})
	loading.Register(module)
	reader, err := config.Load(context.Background(), loading, nil)
	if err != nil {
		t.Fatalf("loading the %s section: %v", namespace, err)
	}
	return reader
}
