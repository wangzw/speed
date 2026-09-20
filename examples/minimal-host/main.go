// Command minimal-host is the smallest host that assembles a run out of the
// module mechanism: it writes a main, imports the modules it wants, writes the
// command tree that says what each run may be, and lets that tree drive the
// lifecycle. Nothing else about the program comes from the framework.
//
// What the host itself contributes is the command tree below and one module
// holding its identity resource, plus the imports. Everything else in this
// directory is an ordinary module: two implementations of one capability, and
// an application module that takes the capability up.
//
// The two blank imports are how a transport and a format reach the
// configuration module. Each registers itself in init and delivers its
// implementation as a resource, so importing the package is the whole of the
// wiring, and a host that configures itself in JSON never imports the YAML
// parser or pays for its dependency.
//
// What the run demonstrates, each with a case in e2e_test.go — or in
// wiring_test.go, where the property leaves no trace in a run:
//
//   - Every command has its own help. The command-line library renders it,
//     out of the arguments the modules that command assembles declared, and
//     the library is what answers: the exit status of a run that asked for
//     help is its verdict, and so is the rejection of a word that selects no
//     command at all.
//   - The command chosen on the command line decides which modules the run
//     assembles: serve takes in every available one, greet takes in the two
//     greeters, and version takes in none at all. An empty set is empty: it
//     is not read as a request for everything.
//   - The command also decides how far the lifecycle advances. greet stops
//     as soon as construction is done, and what it constructed is stopped
//     and closed before the process leaves all the same: one advance in, one
//     advance out, whatever depth it reached.
//   - Taking a module in takes in the providers of what it requires. greet
//     names the two greeters and never names logging, and its records still
//     reach the configured destination and its help still offers the logging
//     arguments.
//   - A config file and an environment variable reach the same input item,
//     and the environment wins. The argument that names the file belongs to
//     the root, so it may be written before the command or after it and the
//     run is the same one.
//   - Two providers deliver one capability; the built-in one stands down as
//     soon as the other is configured, and the startup diagnostics say which
//     module is not running and why.
//   - Cancelling the context stops and closes every module in reverse
//     dependency order.
//   - A record says which module wrote it, without the module putting its own
//     name into the call: a module that declares the logging dependency takes
//     its logger by name, and the same declaration is what puts logging ahead
//     of it at construction and behind it at shutdown, so a record written
//     from New or from Close still reaches the destination.
//   - The configuration reading surface is not a capability a module requires
//     but a product the host supplies, in place before the first module is
//     constructed. No module declares a requirement for it, and the modules
//     that read it are ordered by what they do require.
//   - A path that carries a context takes its logger from the step that
//     created the context, and every call below it only asks the context. The
//     injection carries the attributes of the unit of work along with it.
//   - The configuration decides where records go: by default one text record
//     per line on standard output, and configured to a JSON file they leave
//     standard output altogether. The process default logger is not part of
//     that: it writes to standard output whatever the configuration says,
//     which is how a path that never had a logger injected is spotted.
//   - Redaction masks the keys a module registered as sensitive, in every
//     destination at once: a credential given on the command line reaches no
//     stream and no file, while the record still says a credential was given.
package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/vislake/speed/pkg/config"
	"github.com/vislake/speed/pkg/core"
	"github.com/vislake/speed/pkg/log"

	_ "github.com/vislake/speed/pkg/config/format/yaml"
	_ "github.com/vislake/speed/pkg/config/source/file"
)

// stoppedMsg is the record the serving command writes once its run is over.
const stoppedMsg = "stopped"

// hostVersion is what the version command answers with.
const hostVersion = "minimal-host 0.1.0"

func main() { os.Exit(run()) }

// run builds the command tree, runs it, and turns its outcome into an exit
// status. It is a function of its own so that the deferred stop runs before
// the process leaves: os.Exit skips deferred calls.
func run() int {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := commandTree(core.ProcessRegistry).Execute(ctx); err != nil {
		// A failure has been written to standard error by the time it
		// arrives here: the command-line library reports what a run failed
		// on, and the steps ahead of it report where they happen. What is
		// left for the host is the status, and printing the error a second
		// time would put the same line on the stream twice.
		return 1
	}
	return 0
}

// commandTree writes this host's command line: which commands there are, what
// each one assembles, and how far each one drives.
//
// The tree is the host's alone. No module declares a command and none can
// learn which command took it in, so what one run does is settled here and
// nowhere else.
func commandTree(reg *core.Registry) *config.App {
	app := config.NewApp(reg)

	// The root deliberately takes in nothing and does nothing. What a level
	// takes in holds for every command under it, so a module named here
	// would be assembled by every command — including the one whose whole
	// point is to assemble nothing. Leaving the root without an action of
	// its own is what makes the program's name on its own print the list of
	// commands: a root that served instead would hide that list from the
	// reader most in need of it.
	//
	// The reserved --config argument is the exception, and the command tree
	// registers it here rather than the host: it sits on the root, so
	// --config=x serve and serve --config=x are both the same run.

	// serve is the whole program: every available module, the whole of the
	// lifecycle, and a run that lasts until the context is cancelled.
	serve := app.Root().Sub("serve")
	serve.IncludeAll()
	serve.Do(core.StageServe, serveUntilCancelled)

	// version assembles nothing. Its participating set is empty — the root
	// takes in nothing and this command names nothing — and with nothing
	// assembled no depth is observable, so the shallowest is chosen: the
	// call has to carry a depth, and this is a value rather than a
	// statement about the command.
	version := app.Root().Sub("version")
	version.Do(core.StagePrepare, printVersion)

	// greet drives as far as construction and stops there. It names the two
	// greeters alone; logging is assembled with them because taking a module
	// in takes in the providers of what it requires, and nobody had to write
	// that down here.
	greet := app.Root().Sub("greet")
	greet.Include(localModuleName, remoteModuleName)
	greet.Do(core.StageNew, greetOnce)

	return app
}

// serveUntilCancelled is what serve does once every module has started:
// nothing, until the run is asked to stop.
//
// The waiting is the action's own. A serving command that declares no action
// is held at the same point by the driver; declaring one is what gives this
// host a place to write the closing record, and the record belongs to the
// command that ran rather than to the program, since a command that assembles
// nothing has no run whose end is worth reporting.
func serveUntilCancelled(ctx context.Context, _ *core.Registry) error {
	<-ctx.Done()
	// This line belongs to the host rather than to a module: it holds no
	// product of the registry and the context it was handed carries no
	// logger, so it goes through the process default logger. That one
	// writes to standard output over the bootstrap chain whatever
	// destinations the run was configured with, which is how a path that
	// never had a logger injected is spotted.
	log.Default().Info(stoppedMsg)
	return nil
}

// printVersion answers with the version and is the whole of what the version
// command does. It reads nothing out of the registry, which is what lets the
// command assemble nothing.
//
// The answer goes to standard output directly rather than through
// log.Default(): it is what the caller asked for, not a record of what the
// program is doing, and sending it through logging would let the logging
// configuration decide whether the caller sees it at all.
func printVersion(_ context.Context, _ *core.Registry) error {
	fmt.Println(hostVersion)
	return nil
}

// greetOnce produces one greeting and is the whole of what the greet command
// does.
//
// It resolves the capability and not an implementation, exactly as the
// application module does: which of the two greeters answers is settled by the
// configuration of the run, and this function reads the same either way.
func greetOnce(_ context.Context, reg *core.Registry) error {
	greeter, err := core.Resolve[Greeter](reg)
	if err != nil {
		return err
	}
	fmt.Println(greeter.Greet("world"))
	return nil
}
