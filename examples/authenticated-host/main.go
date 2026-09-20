// Command authenticated-host is the smallest host that puts an HTTP entry
// point behind authentication: it imports an entry point implementation, the
// authentication gateway and one credential-verifying backend, writes the
// command tree that says what each run may be, and lets that tree drive the
// lifecycle.
//
// Importing a module's package is what puts it in the available set: each
// registers itself in init, so the import is the whole of the wiring. The two
// blank imports are a transport and a format, which reach the configuration
// module as resources and have no module name to write down.
//
// What the run demonstrates, each with a case in e2e_test.go:
//
//   - A guarded route answers 401 without a credential and 200 with one, and
//     the handler behind it reads the caller out of the request context rather
//     than out of the request.
//   - The identity a handler receives is owner-qualified — local/alice, not
//     alice — with one backend running and no second one in sight. The
//     qualification is never dropped, so a policy written against it stays
//     right the day a second backend arrives.
//   - An exempt route answers without a credential and reaches no backend at
//     all: the exemption is judged before the Authorization header is read,
//     which is what lets a health check answer while the credential store is
//     down.
//   - The exemption is registered by the module that owns the route, through
//     the gateway's own surface, and is frozen at startup: the startup
//     diagnostics list the frozen table and the backends that turned up.
//   - A wrong password is a refusal and not an outage. The two are different
//     status codes on purpose, because every lockout and alert rule is keyed
//     off which one it is.
//   - The hash command produces what the subject table holds. Passwords are
//     never written into configuration in the clear, and the command that
//     writes a hash assembles no module at all.
//   - The serving command names every module it assembles. IncludeAll() would
//     make the set a function of the import graph, and on an authenticated path
//     that means a backend nobody wrote down could issue identities.
//   - A configuration naming no endpoint to guard stops the startup instead of
//     serving the guarded routes unguarded. The gateway states itself disabled
//     with a reason naming the key to write, and the module that registered the
//     exemption finds no gateway to register with and ends the run.
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
	"github.com/vislake/speed/pkg/http/stdmux"
	"github.com/vislake/speed/pkg/log"

	_ "github.com/vislake/speed/pkg/config/format/yaml"
	_ "github.com/vislake/speed/pkg/config/source/file"
)

// stoppedMsg is the record the serving command writes once its run is over.
const stoppedMsg = "stopped"

// hostVersion is what the version command answers with.
const hostVersion = "authenticated-host 0.1.0"

func main() { os.Exit(run()) }

// run builds the command tree, runs it, and turns its outcome into an exit
// status. It is a function of its own so that the deferred stop runs before
// the process leaves: os.Exit skips deferred calls.
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
// list of commands and the one command that assembles nothing can go on
// assembling nothing.
func commandTree(reg *core.Registry) *config.App {
	app := config.NewApp(reg)

	// serve names every module it assembles, one by one.
	//
	// IncludeAll() would be shorter and is the one thing a command carrying
	// authentication must not do: it makes the participating set a function
	// of the import graph, and on this path that means a credential-verifying
	// backend nobody wrote down here could issue identities. Naming them is
	// what keeps "which backends can authenticate a caller" a question this
	// file answers.
	serve := app.Root().Sub("serve")
	serve.Include(
		stdmux.ModuleName,
		gate.ModuleName,
		static.ModuleName,
		ordersModuleName,
		log.ModuleName,
	)
	serve.Do(core.StageServe, serveUntilCancelled)

	// hash turns a password into what the subject table holds. It assembles
	// nothing: deriving a hash reads no configuration and needs no module,
	// and an operator writing the first account into a file has nothing
	// running yet to ask.
	hash := app.Root().Sub("hash")
	hash.Do(core.StagePrepare, hashPassword)

	// version assembles nothing either.
	version := app.Root().Sub("version")
	version.Do(core.StagePrepare, printVersion)

	return app
}

// serveUntilCancelled is what serve does once every module has started:
// nothing, until the run is asked to stop.
func serveUntilCancelled(ctx context.Context, _ *core.Registry) error {
	<-ctx.Done()
	log.Default().Info(stoppedMsg)
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
