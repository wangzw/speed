package budget

import (
	"context"
	"embed"
	"fmt"
	"io/fs"

	"github.com/vislake/speed/pkg/core"
	"github.com/vislake/speed/pkg/db"
	"github.com/vislake/speed/pkg/settings"
)

// ModuleName is this module's name in the registry.
const ModuleName = "sms.budget"

// The cross-channel ceiling is declared under the shared sms namespace rather
// than this module's, because both sending channels read it and neither owns
// it. Its namespace is what an operator writes the value under.
const (
	// ceilingNamespace is the shared sms namespace the ceiling hangs from.
	ceilingNamespace = "sms"
	// ceilingHourlyItem is the ceiling's key within that namespace.
	ceilingHourlyItem = "ceiling-hourly"
)

// The ceiling's declared default and the bounds of its domain. Both sending
// channels read the one statement, so a deployment with no settings store
// answers the same number whichever channel consults it.
//
// It is a count of sends in an hour, so its floor is one: a zero would turn a
// security bound off.
const (
	// DefaultCeilingHourly is the deployment default of the cross-channel hourly
	// ceiling: the most SMS messages sent in an hour across both channels.
	DefaultCeilingHourly = 5000
	// MinCeilingHourly is the lowest ceiling the domain admits.
	MinCeilingHourly = 1
	// MaxCeilingHourly is the highest ceiling the domain admits.
	MaxCeilingHourly = 1000000
)

// ceilingDefaultValues is the ceiling the deployment starts from. The settings
// module reads the default off this prototype and never returns a value into it.
var ceilingDefaultValues = ceilingDefaults{CeilingHourly: DefaultCeilingHourly}

// ceilingDefaults is the carrier struct the settings module reads the ceiling's
// declared default from.
type ceilingDefaults struct {
	CeilingHourly int `config:"ceiling-hourly"`
}

// ceilingHourlyInDomain holds the ceiling to its range: whole numbers of sends
// from one to a million in an hour.
func ceilingHourlyInDomain(n int) bool { return n >= MinCeilingHourly && n <= MaxCeilingHourly }

// ceilingSchema declares the one setting this module owns. The ceiling spans
// both sending channels, so it is stated once here, under the shared sms
// namespace, where both consumers read it rather than each declaring its own.
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

// migrationsFS is migrationRoot with the wrapping directory removed, so the
// tree the database module sees has one directory per dialect at its root.
var migrationsFS = func() fs.FS {
	sub, err := fs.Sub(migrationRoot, "migrations")
	if err != nil {
		// The embed directive makes this unreachable: the directory is part of
		// the source tree or the build fails first.
		panic(ModuleName + ": the embedded migration tree: " + err.Error())
	}
	return sub
}()

// init registers this module with the process registry. Importing the package
// is what puts the ledger in a binary's available set.
func init() { core.ProcessRegistry.Register(Module()) }

// Module is the descriptor a host registers.
//
// The database is required and hard: a ledger with no table has nowhere to
// write a row. The store is delivered non-exclusively, because one process has
// exactly one ledger and every sending capability reads the same one; a
// consumer that declares a requirement on it pulls this module into its
// closure. The module declares one setting, the cross-channel hourly ceiling,
// which no single consumer can own because it spans both channels; every other
// limit belongs to the consumers. It registers no lifecycle callback beyond
// construction, because the table is written only after the database module has
// applied the migrations this descriptor declares.
func Module() core.Module {
	return core.Module{
		Name:     ModuleName,
		Requires: []core.Requirement{{Token: (*db.Database)(nil)}},
		Provides: []core.Provision{{Token: (*Store)(nil)}},
		Resources: []any{
			db.Migrations{FS: migrationsFS},
			ceilingSchema(),
		},

		// New builds the store over the handle the database module delivered.
		// It reads no table: this stage runs before the migrations are
		// applied, and the first read is the first count a consumer makes
		// after Migrate.
		New: func(_ context.Context, reg *core.Registry) (any, error) {
			database, err := core.Resolve[db.Database](reg)
			if err != nil {
				return nil, fmt.Errorf("%s: taking up the database: %w", ModuleName, err)
			}
			return New(database.DB()), nil
		},
	}
}
