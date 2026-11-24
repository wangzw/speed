// Package dbtest delivers the database capability to a test the way an assembly does: the engine's own module driven through its New stage.
package dbtest

import (
	"context"
	"encoding/json"
	"errors"
	"net/url"
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

const hostPrefix = "SPEEDDBTESTFIXTURE"

// Items are input items of the engine's section beside its locator, by key: "max-open-conns",
// "max-idle-conns", "conn-max-lifetime" and the rest the engine declares.
// A nil Items configures the locator alone.
type Items map[string]any

// SQLite delivers the capability of the SQLite engine over the locator dsn, with the items
// configured beside it.
func SQLite(t testing.TB, dsn string, items Items) db.Database {
	t.Helper()
	return deliver(t, sqlite.Module(), sqlite.ConfigNamespace, dsn, items)
}

// Postgres delivers the capability of the PostgreSQL engine over the database the locator dsn
// names — one the test took from pkg/internal/pgpool/pgtest — with the items configured beside it.
func Postgres(t testing.TB, dsn string, items Items) db.Database {
	t.Helper()
	return deliver(t, postgres.Module(), postgres.ConfigNamespace, dsn, items)
}

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
		Resources: []any{config.HostIdentity{Prefix: hostPrefix, DefaultLocator: defaultLocator(locator)}},
	})
	loading.Register(module)
	reader, err := config.Load(context.Background(), loading, nil)
	if err != nil {
		t.Fatalf("loading the %s section: %v", namespace, err)
	}
	return reader
}

func defaultLocator(path string) string {
	return (&url.URL{Scheme: "file", Path: path}).String()
}
