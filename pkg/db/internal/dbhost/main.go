// Command dbhost is the test host the end-to-end cases in this directory drive as a child process; it is not part of the product. Its tree has two commands: the root drives the whole lifecycle, and `migrate` stops at the migration stage.
package main

import (
	"context"
	"errors"
	"fmt"
	"os"

	"github.com/vislake/speed/pkg/config"
	"github.com/vislake/speed/pkg/core"
	"github.com/vislake/speed/pkg/db/postgres"
	"github.com/vislake/speed/pkg/db/sqlite"

	_ "github.com/vislake/speed/pkg/config/format/json"
	_ "github.com/vislake/speed/pkg/config/source/file"
)

// The markers a case looks for when the run is expected to fail: errors.Is does
// not survive a process boundary, so the host judges the error itself and prints
// a fixed word the case can look for.
const (
	exclusiveViolatedMarker = "dbhost: exclusive-violated"
	missingProviderMarker   = "dbhost: missing-provider"
)

var finish context.CancelFunc

func main() { os.Exit(run()) }

// run drives the lifecycle and turns its outcome into an exit status; it is a function of its own so the deferred cancel runs before os.Exit skips it.
func run() int {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	finish = cancel

	app := config.NewApp(core.ProcessRegistry)

	root := app.Root()
	root.Include(namedModules()...)
	root.Do(core.StageServe, nil)

	migrate := root.Sub("migrate")
	migrate.Do(core.StageMigrate, nil)

	if err := app.Execute(ctx); err != nil {
		switch {
		case errors.Is(err, core.ErrExclusiveViolated):
			say(exclusiveViolatedMarker)
		case errors.Is(err, core.ErrMissingProvider):
			say(missingProviderMarker)
		}
		return 1
	}
	return 0
}

// namedModules is what the command tree takes in: the engine this scenario configures and the dependant beside it. The engine is named because the capability is delivered exclusively, so the dependency closure pulls no provider in on its own.
func namedModules() []string {
	switch scenario() {
	case caseBothEnginesConfigured:
		return []string{postgres.ModuleName, sqlite.ModuleName, probeModuleName}
	case caseNoEngineConfigured:
		return []string{probeModuleName}
	case caseFirstMigration, caseConnectRefused:
		return []string{postgres.ModuleName, probeModuleName}
	case caseSerializerRoundTrip, caseOneEngineConfigured, caseMigrateSubcommand:
		return []string{sqlite.ModuleName, probeModuleName}
	default:
		// Unreachable in the suite; leaving the engine out makes it fail on
		// the dependant's requirement.
		return []string{probeModuleName}
	}
}

func say(line string) {
	//nolint:errcheck // dropping the error is what this function is for; the
	// doc comment above says why there is nothing to do with it.
	fmt.Fprintln(os.Stdout, line)
}
