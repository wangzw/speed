package main

import (
	"context"

	"github.com/vislake/speed/pkg/config"
	"github.com/vislake/speed/pkg/core"
	"github.com/vislake/speed/pkg/db"
)

// envPrefix is this host's environment variable prefix. The variables a scenario
// reads deliberately do not start with it: one that did would be reported as an
// input item nobody reads.
const envPrefix = "DBHOST"

func init() {
	core.ProcessRegistry.Register(hostModule())
	core.ProcessRegistry.Register(probeModule())
}

func hostModule() core.Module {
	return core.Module{
		Name:      "host",
		Resources: []any{config.HostIdentity{Prefix: envPrefix}},
	}
}

// probeModule is the dependant every scenario is built around: it requires the
// database capability, which is also what turns a missing provider into a
// startup failure.
func probeModule() core.Module {
	return core.Module{
		Name:      probeModuleName,
		Requires:  []core.Requirement{{Token: (*db.Database)(nil)}},
		Resources: []any{declaredMigrations()},
		New:       probeNew,
		Migrate:   probeMigrate,
		// Ending the scenario ends the run. Serve must not block, and cancelling
		// does not: the driver advances while shutdown is on its way. Reaching
		// this stage is what one scenario asserts the absence of.
		Serve: func(context.Context, *core.Registry, any) error {
			if scenario() == caseMigrateSubcommand {
				say(servedMarker)
			}
			finish()
			return nil
		},
	}
}
