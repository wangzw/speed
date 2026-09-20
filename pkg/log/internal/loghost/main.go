// Command loghost is the test host the end-to-end cases in this directory
// drive as a child process. It is not part of the product: it lives under
// internal/ so no consumer can import it, and nothing but this package's own
// tests builds it.
//
// A child process is what the cases need rather than a preference. The command
// tree takes the process's own arguments and nothing exported puts another
// list in their place, while a test binary is always given arguments of its
// own (-test.paniconexit0 among them), which the tree rejects as arguments no
// command declares; a real assembly therefore cannot be started inside a test
// binary. Running the host as a process also gives the cases the two output
// streams and the exit status, and it lets each case have a registry of its
// own, since a registry is driven once.
//
// Two implementation constraints come from the same direction. The scenario is
// chosen by an environment variable that does not start with this host's
// prefix: positional arguments are rejected by every command, and a variable
// under the prefix that no input item reads is reported in the startup
// diagnostics, which the cases read. The configuration document is JSON
// because the cases marshal it from Go values rather than writing text, and
// encoding/json does that in the standard library.
package main

import (
	"context"
	"errors"
	"fmt"
	"os"

	"github.com/vislake/speed/pkg/config"
	"github.com/vislake/speed/pkg/core"

	_ "github.com/vislake/speed/pkg/config/format/json"
	_ "github.com/vislake/speed/pkg/config/source/file"
	_ "github.com/vislake/speed/pkg/log"
)

// exclusiveMarker is printed when the startup failed over two enabled
// providers of the logging capability. errors.Is does not survive a process
// boundary, so the host judges the error itself and prints a fixed word the
// case can look for.
const exclusiveMarker = "loghost: exclusive-violated"

// finish ends the run. The host's whole job is one scenario, so the module
// that ran it cancels the context and lets the drive shut every module down in
// order, the way an interrupt would.
var finish context.CancelFunc

func main() { os.Exit(run()) }

// run assembles the command tree, runs it, and turns its outcome into an exit
// status. It is a function of its own so the deferred cancel runs before the
// process leaves: os.Exit skips deferred calls.
//
// The tree is one command. The cases invoke the host with no word but the
// reserved argument that names the config source, so the root is what every
// run selects, and the root is what carries the work.
//
// The failure is written by the tree, which reports both the failures of its
// own steps and the one the drive returns, so nothing is printed here a second
// time. What this function adds is the fixed word the cases look for, since
// errors.Is does not survive a process boundary.
func run() int {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	finish = cancel

	if scenario() == caseRivalProvider {
		core.ProcessRegistry.Register(rivalLoggerModule())
	}

	app := config.NewApp(core.ProcessRegistry)
	root := app.Root()
	// Every registered module is taken in. A written-out list could not
	// name the rival: it is registered above, after the imports that
	// register the rest, and only in the one scenario that wants it.
	root.IncludeAll()
	// Serving is the whole depth, and arriving is the whole of the
	// command's work: the scenario runs in the application module's Serve
	// and cancels the context when it is done.
	root.Do(core.StageServe, nil)

	if err := app.Execute(ctx); err != nil {
		if errors.Is(err, core.ErrExclusiveViolated) {
			say(exclusiveMarker)
		}
		return 1
	}
	return 0
}

// say writes one line of this host's own narration, as opposed to a log
// record.
//
// The dropped write error is stated once here rather than at each call site:
// stdout is the output channel itself, so a failed write to it has nowhere
// left to be reported.
func say(line string) {
	//nolint:errcheck // dropping the error is what this function is for; the
	// doc comment above says why there is nothing to do with it.
	fmt.Fprintln(os.Stdout, line)
}
