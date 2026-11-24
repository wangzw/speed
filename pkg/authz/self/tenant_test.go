package self

import (
	"context"
	"log/slog"
	"path/filepath"
	"testing"

	"github.com/vislake/speed/pkg/config"
	"github.com/vislake/speed/pkg/core"
	"github.com/vislake/speed/pkg/db"
	"github.com/vislake/speed/pkg/internal/configtest"
	"github.com/vislake/speed/pkg/internal/dbtest"
	"github.com/vislake/speed/pkg/internal/routecontract"
	"github.com/vislake/speed/pkg/log"
	"github.com/vislake/speed/pkg/tenant"
	tenantcatalog "github.com/vislake/speed/pkg/tenant/catalog"
)

type quietLogging struct{}

func (quietLogging) Named(string) *slog.Logger { return slog.New(slog.DiscardHandler) }
func (quietLogging) Redaction() log.Redaction  { return quietRedaction{} }

type quietRedaction struct{}

func (quietRedaction) AddKeys(...string)              {}
func (quietRedaction) AddPattern(string, log.Matcher) {}

func tenantContext(t *testing.T, id tenant.ID) context.Context {
	t.Helper()
	database := dbtest.SQLite(t, filepath.Join(t.TempDir(), "roster.db"), nil)
	handle := database.DB()
	if err := handle.Exec(`CREATE TABLE tenants (
		id     TEXT    NOT NULL PRIMARY KEY,
		name   TEXT    NOT NULL UNIQUE,
		status INTEGER NOT NULL DEFAULT 0)`).Error; err != nil {
		t.Fatalf("creating the tenants table: %v", err)
	}
	if err := handle.Exec(`INSERT INTO tenants (id, name, status) VALUES (?, ?, 1)`,
		string(id), string(id)).Error; err != nil {
		t.Fatalf("registering the tenant %q: %v", id, err)
	}

	stand := routecontract.StandIn("stand-in.self.db", database, (*db.Database)(nil))
	logging := routecontract.StandIn("stand-in.self.logging", quietLogging{}, (*log.Logger)(nil))
	modules := []core.Module{tenantcatalog.Module(), stand, logging}
	reader := configtest.Reader(t, "{}", modules...)

	reg := core.New()
	reg.Register(config.Module())
	for _, module := range modules {
		reg.Register(module)
	}
	reg.Supply(reader, config.ModuleName)

	var entered context.Context
	err := reg.Drive(t.Context(), core.Drive{
		Depth:        core.StageInit,
		Participants: []string{tenantcatalog.ModuleName, stand.Name, logging.Name},
		Action: func(ctx context.Context, reg *core.Registry) error {
			roster, err := core.Resolve[tenant.Catalog](reg)
			if err != nil {
				return err
			}
			entered, err = roster.Enter(ctx, id)
			return err
		},
	})
	if err != nil {
		t.Fatalf("entering the tenant %q: %v", id, err)
	}
	return entered
}
