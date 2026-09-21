// Command tenanted-host is the smallest host that puts statements behind a
// tenant: it imports an HTTP entry point, the database engine, the
// authentication gateway with one backend, the tenant release unit, and an
// application module whose model embeds the scoping convention. It writes the
// command tree that says what each run may be, and lets that tree drive the
// lifecycle.
//
// Importing a module's package is what puts it in the available set: each
// registers itself in init, so the import is the whole of the wiring. One of
// the imports below is blank for a reason worth reading — the roster module's
// name never appears in the command tree, and it is nevertheless in the run.
//
// What the run demonstrates, each with a case in e2e_test.go:
//
//   - A request naming a tenant that is on the roster reaches a handler with
//     that tenant in its context; the handler issues a query with no condition
//     and gets back only that tenant's rows.
//   - A request naming a tenant that is disabled, and one naming a tenant that
//     never existed, are answered the same way. Telling them apart would let a
//     caller enumerate the roster.
//   - A request that names no tenant at all is refused before any handler runs.
//     Resolution has no per-route exception, so a route that needs no tenant
//     belongs on an endpoint resolution does not guard — which is where the
//     health check is.
//   - The resolving layer sits inside authentication: a request without a
//     credential is refused by authentication without a roster lookup ever
//     happening.
//   - The migrate command builds both tables while running only the database
//     engine. The application module is not constructed there, its input items
//     are not on that command's line, and its table is built anyway — which is
//     what reaching a module for its declarations alone is for.
//   - The roster module's name is written nowhere, and it runs: the resolving
//     module declares a construction-time dependency on it.
package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/vislake/speed/pkg/authn/gate"
	"github.com/vislake/speed/pkg/authn/static"
	"github.com/vislake/speed/pkg/config"
	"github.com/vislake/speed/pkg/core"
	"github.com/vislake/speed/pkg/db"
	"github.com/vislake/speed/pkg/db/sqlite"
	"github.com/vislake/speed/pkg/http/stdmux"
	"github.com/vislake/speed/pkg/log"
	"github.com/vislake/speed/pkg/tenant"
	"github.com/vislake/speed/pkg/tenant/resolve"

	// The roster module registers itself in init, so importing the package
	// is what puts it in the available set. Nothing here names it: the
	// resolving module declares a construction-time dependency on the
	// capability it delivers, and that declaration is what draws it into
	// the participating set. Writing its name into the command tree as well
	// would say the same thing twice.
	_ "github.com/vislake/speed/pkg/tenant/catalog"

	_ "github.com/vislake/speed/pkg/config/format/yaml"
	_ "github.com/vislake/speed/pkg/config/source/file"
)

// stoppedMsg is the record the serving command writes once its run is over.
const stoppedMsg = "stopped"

// hostVersion is what the version command answers with.
const hostVersion = "tenanted-host 0.1.0"

// The tenants this host puts on the roster when it serves, and the orders it
// writes for them.
//
// The disabled one is here so that a case can see the two refusals being one
// answer, and the order written for it is here so that the refusal is
// observably not about the rows being absent.
const (
	acmeTenant   = "acme"
	globexTenant = "globex"
)

func main() { os.Exit(run()) }

// run builds the command tree, runs it, and turns its outcome into an exit
// status. It is a function of its own so that the deferred stop runs before the
// process leaves: os.Exit skips deferred calls.
func run() int {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := commandTree(core.ProcessRegistry).Execute(ctx); err != nil {
		// A failure has already been written to standard error by the
		// time it arrives here; printing it again would put the same
		// line on the stream twice.
		return 1
	}
	return 0
}

// commandTree writes this host's command line.
//
// The root takes in nothing, so that the program's name on its own prints the
// list of commands and the command that assembles nothing can go on assembling
// nothing.
func commandTree(reg *core.Registry) *config.App {
	app := config.NewApp(reg)

	// serve takes in the resolving module and every other module it needs.
	//
	// IncludeAll() would be shorter and is the one thing a command
	// carrying tenant scoping must not do: it makes the participating set a
	// function of the import graph, and on this path that means a module
	// nobody wrote down here could carry a statement filter into the run.
	serve := app.Root().Sub("serve")
	serve.Include(
		stdmux.ModuleName,
		gate.ModuleName,
		static.ModuleName,
		resolve.ModuleName,
		sqlite.ModuleName,
		ordersModuleName,
		log.ModuleName,
	)
	serve.Do(core.StageServe, serveUntilCancelled)

	// migrate runs the engine and collects everyone's declarations.
	//
	// The application module is not run here: taking it in would put its
	// input items on this command's line and make it answer for
	// configuration it does not need, while building the tables is the only
	// thing this command wants from it. Reaching every module for its
	// declarations alone is the form that says both. It is also the form
	// with a silent failure of its own — a module left out of it has its
	// tables quietly not built — which is why the whole available set is
	// taken rather than a list.
	migrate := app.Root().Sub("migrate")
	migrate.Include(sqlite.ModuleName)
	migrate.IncludeAllDeclarations()
	migrate.Do(core.StageMigrate, nil)

	version := app.Root().Sub("version")
	version.Do(core.StagePrepare, printVersion)

	return app
}

// serveUntilCancelled puts a roster and some rows in place, then does nothing
// until the run is asked to stop.
//
// A real host would not seed on every run; it does here so that a case can be
// about resolution rather than about how the data got there.
func serveUntilCancelled(ctx context.Context, reg *core.Registry) error {
	if err := seed(ctx, reg); err != nil {
		return err
	}
	<-ctx.Done()
	log.Default().Info(stoppedMsg)
	return nil
}

// seed registers two tenants and writes one order for each.
//
// The roster needs no tenant, and that is a property rather than a
// convenience: its table does not embed the scoping convention, so its
// statements take the same path through the filter as any global table and are
// neither conditioned nor refused.
//
// The orders are a different matter. This runs outside any request, so there
// is no resolving layer to establish a tenant, and the writing side of the
// context is closed to everything outside the release unit. A path like this
// one therefore states what it is doing: it crosses tenants, and it writes the
// tenant column itself.
func seed(ctx context.Context, reg *core.Registry) error {
	roster, err := core.Resolve[tenant.Catalog](reg)
	if err != nil {
		return err
	}
	for _, t := range []tenant.Tenant{
		{ID: acmeTenant, Alias: acmeTenant, Status: tenant.StatusActive},
		{ID: globexTenant, Alias: globexTenant, Status: tenant.StatusDisabled},
	} {
		// Named rather than shadowing err: the resolution above already
		// declared one, and a second err in the loop body would be a
		// different variable wearing the same name.
		if putErr := roster.Put(ctx, t); putErr != nil {
			return fmt.Errorf("registering %s: %w", t.ID, putErr)
		}
	}

	handle, err := core.Resolve[db.Database](reg)
	if err != nil {
		return err
	}
	cross := tenant.CrossTenant(ctx)
	for _, o := range []Order{
		{Model: tenant.Model{TenantID: acmeTenant}, ID: "a1", Item: "a widget for acme"},
		{Model: tenant.Model{TenantID: globexTenant}, ID: "g1", Item: "a widget for globex"},
	} {
		if writeErr := handle.DB().WithContext(cross).Create(&o).Error; writeErr != nil {
			return fmt.Errorf("writing %s: %w", o.ID, writeErr)
		}
	}
	return nil
}

// printVersion answers with the version. It goes to standard output directly:
// it is what the caller asked for, not a record of what the program is doing.
func printVersion(_ context.Context, _ *core.Registry) error {
	fmt.Println(hostVersion)
	return nil
}
