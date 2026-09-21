// Command orders-host is a host that assembles every module of this repository
// into one running program: an HTTP entry point, the database engine, the
// authentication gateway with one credential backend, the tenant release unit —
// resolution and roster — the audit journal, the runtime settings surface, and
// a key-value and an event backend, together with two application modules of
// its own. It writes the command tree that says what each run may be, and lets
// that tree drive the lifecycle.
//
// Importing a module's package is what puts it in the available set: each
// registers itself in init, so the import is the whole of the wiring. Nothing
// else about the program comes from the framework.
//
// What the run demonstrates, each with a case in e2e_test.go — or in
// wiring_test.go, where the property leaves no trace in a run:
//
//   - Every command has its own help, and help is answered before any
//     configuration source is read: the locator may name a source that cannot
//     be reached and the question is still answered. A word that selects no
//     command is rejected by the command-line library, and the rejection
//     appears on the stream exactly once.
//   - The command chosen decides which modules the run assembles and how far the
//     lifecycle advances. `version` and `hash` assemble nothing at all; `serve`
//     runs the whole of it; `migrate` runs the engine and stops.
//   - `migrate` reaches every module for its declarations alone. The two
//     application modules are not constructed there, their input items are not on
//     that command's line, and their tables and the journal's are built
//     nevertheless — which is what a command that runs one module and needs
//     everyone's declarations is for. A module left out of it has its tables
//     quietly not built, so the whole available set is taken rather than a list.
//   - A module taken in takes in the providers of what it requires. `serve` names
//     neither key-value nor event backend, and both run: the inventory module
//     requires the key-value capability and the settings module requires the
//     event bus, so the providers arrive through the closure rather than from
//     anything written in this file.
//   - A config file and an environment variable reach the same input item, and
//     the environment wins. The argument naming the file belongs to the root, so
//     it may be written before the command or after it and the run is the same
//     one.
//   - Cancelling the context stops and closes every module in reverse
//     construction order, and a record a module writes as it closes still
//     reaches the configured destinations: the logger it holds is closed after
//     it, which is what declaring that requirement buys.
//   - A record says which module wrote it, without the module putting its own
//     name into the call.
//   - A guarded route answers 401 without a credential and 200 with one, and the
//     handler behind it reads the caller out of the request context rather than
//     out of the request. The identity is owner-qualified — local/alice, not
//     alice — with one backend running and no second one in sight.
//   - An exemption is registered by the module that owns the route, through the
//     gateway's own surface, and is frozen at startup: the startup diagnostics
//     list the frozen table and the backends that turned up. The exemption
//     covers the credential alone — resolution has no per-route exception, so
//     the exempted route still needs a tenant — which is why the route that
//     needs neither sits on the endpoint neither guard covers.
//   - A configuration that names no endpoint to guard stops the startup rather
//     than serving the guarded routes unguarded.
//   - An unauthenticated request for a route nobody registered is refused as
//     unauthenticated rather than as not found: the gateway runs outside the
//     routing engine, so a caller who never authenticated learns nothing about
//     which paths exist.
//   - A request naming a tenant that is on the roster reaches a handler with that
//     tenant in its context, and the handler issues a query with no condition and
//     gets back only that tenant's rows.
//   - A request naming a tenant that is disabled, and one naming a tenant that
//     never existed, are answered the same way. Telling them apart would let a
//     caller enumerate the roster.
//   - A request that names no tenant at all is refused before any handler runs,
//     and the resolving layer sits inside authentication: a request without a
//     credential is refused by authentication without a roster lookup ever
//     happening.
//   - A setting declared by a module is read back through three layers: the row
//     written for the caller's tenant, the global row, and the declared default.
//     One declaration here is written at both layers and another at neither, so
//     both a written answer and a declared one are observable.
//   - A placement writes its row and publishes a fact under a type name. Nothing
//     in the writing module names a subscriber, and the subscriber counts what it
//     is handed without knowing the publisher's type.
//   - The audit journal captures the same write four ways: the statement, the
//     request that carried it, the placement event, and a crossing of tenants
//     that no capture path can explain — a seed — which states its purpose
//     through the seam, the one path whose caller knows why.
package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/vislake/speed/pkg/audit"
	"github.com/vislake/speed/pkg/audit/journal"
	"github.com/vislake/speed/pkg/authn/gate"
	"github.com/vislake/speed/pkg/authn/static"
	"github.com/vislake/speed/pkg/config"
	"github.com/vislake/speed/pkg/core"
	"github.com/vislake/speed/pkg/db"
	"github.com/vislake/speed/pkg/db/sqlite"
	"github.com/vislake/speed/pkg/http/stdmux"
	"github.com/vislake/speed/pkg/log"
	"github.com/vislake/speed/pkg/settings"
	"github.com/vislake/speed/pkg/tenant"
	"github.com/vislake/speed/pkg/tenant/resolve"

	_ "github.com/vislake/speed/pkg/config/format/yaml"
	_ "github.com/vislake/speed/pkg/config/source/file"
	_ "github.com/vislake/speed/pkg/event/memory"
	_ "github.com/vislake/speed/pkg/kv/memory"

	// The roster module registers itself in init, so importing the package is
	// what puts it in the available set. Nothing here names it: the resolving
	// module declares a construction-time dependency on the capability it
	// delivers, and that declaration is what draws it into the participating
	// set. Writing its name into the command tree as well would say the same
	// thing twice.
	_ "github.com/vislake/speed/pkg/tenant/catalog"
)

// stoppedMsg is the record the serving command writes once its run is over.
const stoppedMsg = "stopped"

// readyMsg is the record it writes once every module has started and the seed is
// in place. It is what a case waits for: the listening sockets are open before
// the seed runs, so a case that waited on a port alone would be free to
// interrupt a run that is still putting its data in place — which is not what
// interrupting is meant to test.
const readyMsg = "ready"

// hostVersion is what the version command answers with.
const hostVersion = "orders-host 0.1.0"

// The tenants this host puts on the roster when it serves, and the orders it
// writes for them.
//
// Two are active and one is disabled. The disabled one is here so that a case
// can see the two refusals being one answer, and the order written for it is
// here so that the refusal is observably not about the rows being absent.
const (
	acmeTenant    = "acme"
	globexTenant  = "globex"
	initechTenant = "initech"
)

// The page sizes the seed writes: one for a single tenant and one for everyone.
//
// The tenant's is larger than the rows any one tenant owns, so a listing that
// reads it returns every row that tenant has and nothing else — which is what
// makes the tenant filter observable rather than merely plausible. The global
// one is smaller than globex's rows, so that listing returns a prefix of them,
// which is what makes the limit observable.
const (
	globalPageSize = 2
	acmePageSize   = 10
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
		// time it arrives here; printing it again would put the same line
		// on the stream twice.
		return 1
	}
	return 0
}

// commandTree writes this host's command line.
//
// The root takes in nothing, so that the program's name on its own prints the
// list of commands and the two commands that assemble nothing can go on
// assembling nothing.
func commandTree(reg *core.Registry) *config.App {
	app := config.NewApp(reg)

	// serve names every module it means to run, one by one.
	//
	// IncludeAll() would be shorter and is the one thing a command carrying
	// tenant scoping must not do: it makes the participating set a function of
	// the import graph, and on this path that means a module nobody wrote down
	// here could carry a statement filter into the run.
	//
	// Neither backend is named. They are in this run because the modules here
	// require the capabilities they deliver, which is the declaration that
	// stays right when a module is added — and the backends are the part of
	// this run that is a consequence rather than a choice.
	serve := app.Root().Sub("serve")
	serve.Include(
		stdmux.ModuleName,
		gate.ModuleName,
		static.ModuleName,
		resolve.ModuleName,
		sqlite.ModuleName,
		journal.ModuleName,
		settings.ModuleName,
		ordersModuleName,
		inventoryModuleName,
		log.ModuleName,
	)
	serve.Do(core.StageServe, serveUntilCancelled)

	// migrate runs the engine and collects everyone's declarations.
	//
	// The application modules and the journal are not run here: taking them in
	// would put their input items on this command's line and make them answer
	// for configuration they do not need, while building their tables is the
	// only thing this command wants from them.
	migrate := app.Root().Sub("migrate")
	migrate.Include(sqlite.ModuleName)
	migrate.IncludeAllDeclarations()
	migrate.Do(core.StageMigrate, nil)

	// hash turns a password into what the subject table holds. It assembles
	// nothing: deriving a hash reads no configuration and needs no module, and
	// an operator writing the first account into a file has nothing running yet
	// to ask.
	hash := app.Root().Sub("hash")
	hash.Do(core.StagePrepare, hashPassword)

	version := app.Root().Sub("version")
	version.Do(core.StagePrepare, printVersion)

	return app
}

// serveUntilCancelled puts a roster, some rows and two settings in place, then
// does nothing until the run is asked to stop.
//
// A real host would not seed on every run; it does here so that a case can be
// about resolution rather than about how the data got there.
func serveUntilCancelled(ctx context.Context, reg *core.Registry) error {
	if err := seed(ctx, reg); err != nil {
		return err
	}
	log.Default().Info(readyMsg)
	<-ctx.Done()
	log.Default().Info(stoppedMsg)
	return nil
}

// seed registers three tenants, writes orders for each of them, and writes the
// two page sizes.
//
// The roster needs no tenant, and that is a property rather than a convenience:
// its table does not embed the scoping convention, so its statements take the
// same path through the filter as any global table and are neither conditioned
// nor refused.
//
// The orders are a different matter. This runs outside any request, so there is
// no resolving layer to establish a tenant and the writing side of the context
// is closed to everything outside the release unit. A path like this one
// therefore states what it is doing: it crosses tenants, and it writes the
// tenant column itself. That crossing is also the one fact no capture path of
// the audit journal can explain — the statement sees a write, the request path
// is not on it, and no event was published — so this is where the seam is used
// and the purpose is stated.
func seed(ctx context.Context, reg *core.Registry) error {
	roster, err := core.Resolve[tenant.Catalog](reg)
	if err != nil {
		return err
	}
	for _, t := range []tenant.Tenant{
		{ID: acmeTenant, Alias: acmeTenant, Status: tenant.StatusActive},
		{ID: globexTenant, Alias: globexTenant, Status: tenant.StatusActive},
		{ID: initechTenant, Alias: initechTenant, Status: tenant.StatusDisabled},
	} {
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
		{Model: tenant.Model{TenantID: acmeTenant}, ID: "a2", Item: "a second widget for acme"},
		{Model: tenant.Model{TenantID: acmeTenant}, ID: "a3", Item: "a third widget for acme"},
		{Model: tenant.Model{TenantID: globexTenant}, ID: "g1", Item: "a widget for globex"},
		{Model: tenant.Model{TenantID: globexTenant}, ID: "g2", Item: "a second widget for globex"},
		{Model: tenant.Model{TenantID: globexTenant}, ID: "g3", Item: "a third widget for globex"},
		// The disabled tenant gets a row as well, so that a case can see its
		// refusal being about the roster rather than about there being
		// nothing to read.
		{Model: tenant.Model{TenantID: initechTenant}, ID: "i1", Item: "a widget for initech"},
	} {
		if writeErr := handle.DB().WithContext(cross).Create(&o).Error; writeErr != nil {
			return fmt.Errorf("writing %s: %w", o.ID, writeErr)
		}
	}

	// The two page sizes land at the two written layers. Nothing is written
	// for the retention setting, so a read of that one answers at the
	// declaration — which is the only way the third layer is observable at
	// all.
	set, err := core.Resolve[settings.Store](reg)
	if err != nil {
		return err
	}
	for _, w := range []settings.Write{
		{Tenant: "", Key: pageSizeSetting, Value: globalPageSize, Actor: "seed"},
		{Tenant: acmeTenant, Key: pageSizeSetting, Value: acmePageSize, Actor: "seed"},
	} {
		if writeErr := set.Set(ctx, w); writeErr != nil {
			return fmt.Errorf("writing %s for %q: %w", w.Key, w.Tenant, writeErr)
		}
	}

	// The seam, and only the seam. A record written here is accepted even
	// though this context carries no identity and no tenant, because a
	// system-level operation is as much in need of a record as a request is.
	recorder, err := core.Resolve[audit.Audit](reg)
	if err != nil {
		return err
	}
	if err := recorder.Record(ctx, audit.Entry{
		Action:  "seed",
		Object:  orderTable,
		Outcome: "succeeded",
		Purpose: "seeding rows for three tenants outside any request, so that a case can be about resolution rather than about how the data got there",
	}); err != nil {
		return fmt.Errorf("recording the seed: %w", err)
	}
	return nil
}

// hashPassword reads a password from standard input and writes its hash to
// standard output, for pasting into the subject table.
//
// It is read from standard input rather than taken as an argument, so that the
// password does not land in the shell history or in the process list of every
// other user on the machine.
func hashPassword(_ context.Context, _ *core.Registry) error {
	var password string
	if _, err := fmt.Scanln(&password); err != nil {
		return fmt.Errorf("reading the password from standard input: %w", err)
	}
	encoded, err := static.Hash(password)
	if err != nil {
		return err
	}
	fmt.Println(encoded)
	return nil
}

// printVersion answers with the version. It goes to standard output directly:
// it is what the caller asked for, not a record of what the program is doing.
func printVersion(_ context.Context, _ *core.Registry) error {
	fmt.Println(hostVersion)
	return nil
}
