package budget

import (
	"context"
	"embed"
	"errors"
	"fmt"
	"io/fs"

	"github.com/vislake/speed/pkg/core"
	"github.com/vislake/speed/pkg/db"
	"github.com/vislake/speed/pkg/log"
	"github.com/vislake/speed/pkg/settings"
)

// ModuleName is this module's name in the registry.
const ModuleName = "sms.budget"

const (
	ceilingNamespace  = "sms"
	ceilingHourlyItem = "ceiling-hourly"
)

const (
	// DefaultCeilingHourly is the deployment default of the cross-channel hourly
	// ceiling.
	DefaultCeilingHourly = 5000
	// MinCeilingHourly is the lowest ceiling the domain admits.
	MinCeilingHourly = 1
	// MaxCeilingHourly is the highest ceiling the domain admits.
	MaxCeilingHourly = 1000000
)

var ceilingDefaultValues = ceilingDefaults{CeilingHourly: DefaultCeilingHourly}

type ceilingDefaults struct {
	CeilingHourly int `config:"ceiling-hourly"`
}

func ceilingHourlyInDomain(n int) bool { return n >= MinCeilingHourly && n <= MaxCeilingHourly }

func ceilingSchema() settings.Schema {
	return settings.Schema{
		Namespace: ceilingNamespace,
		Mounts:    []settings.Mount{{Value: &ceilingDefaultValues}},
		Items: map[string]settings.Item{
			ceilingHourlyItem: {
				Description: "the most SMS messages the deployment may send in an hour across the " +
					"authentication and notification channels together, whatever the purpose or number; " +
					"consulted before every other send budget",
				Domain: settings.DomainOf("whole numbers from 1 to 1000000", ceilingHourlyInDomain),
			},
		},
	}
}

//go:embed migrations
var migrationRoot embed.FS

var migrationsFS = func() fs.FS {
	sub, err := fs.Sub(migrationRoot, "migrations")
	if err != nil {
		panic(ModuleName + ": the embedded migration tree: " + err.Error())
	}
	return sub
}()

func init() { core.ProcessRegistry.Register(Module()) }

// Module is the descriptor a host registers. It requires the database, delivers
// Store non-exclusively, declares the cross-channel ceiling and owns the retention
// sweep.
func Module() core.Module {
	return core.Module{
		Name: ModuleName,
		Requires: []core.Requirement{
			{Token: (*db.Database)(nil)},
			{Token: (*log.Logger)(nil), Optional: true},
		},
		Provides: []core.Provision{{Token: (*Store)(nil)}},
		Resources: []any{
			db.Migrations{FS: migrationsFS},
			ceilingSchema(),
		},

		New: func(_ context.Context, reg *core.Registry) (any, error) {
			database, err := core.Resolve[db.Database](reg)
			if err != nil {
				return nil, fmt.Errorf("%s: taking up the database: %w", ModuleName, err)
			}
			product := newLedger(New(database.DB()))
			if delivered, err := core.Resolve[log.Logger](reg); err == nil {
				product.logger = delivered.Named(ModuleName)
			} else if !errors.Is(err, core.ErrMissingProvider) {
				return nil, fmt.Errorf("%s: taking up the logging capability: %w", ModuleName, err)
			}
			return product, nil
		},

		Start: func(ctx context.Context, _ *core.Registry, instance any) error {
			if product, ok := instance.(*ledger); ok && product != nil {
				product.start(ctx)
			}
			return nil
		},

		Stop: func(_ context.Context, _ *core.Registry, instance any) error {
			if product, ok := instance.(*ledger); ok && product != nil {
				return product.stop()
			}
			return nil
		},
	}
}
