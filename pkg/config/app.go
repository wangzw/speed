package config

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/vislake/speed/pkg/core"
)

// App is one program's command tree. The host writes the tree on Root, then
// Execute runs it: the command line decides which command this run is, and the
// command decides which modules it assembles, how far the lifecycle advances
// and what happens on arrival.
//
// Everything App does happens before assembly begins, so it belongs to no
// lifecycle stage. The modules it assembles know nothing about it.
type App struct {
	reg  *core.Registry
	root *Command
	// args, stdout and stderr are the process's own. Holding them rather
	// than reaching for them is what lets a test run a tree without a
	// process of its own.
	args   []string
	stdout io.Writer
	stderr io.Writer
	// executed records that this tree has been run, so that a second run is
	// caught where it is asked for.
	executed bool
}

// NewApp starts a command tree over a registry. The tree is empty: the host
// writes it on Root, and the root command is named after the program.
//
// The registry is the one the run assembles. core.ProcessRegistry is the one
// every module registers itself with, and the one a host passes. It has to hold
// the config module, which Execute supplies the reader to: the process registry
// has it from this package's init, and a registry made with core.New registers
// Module by hand first. NewApp panics when it is not there, so the omission shows
// where the tree is started rather than after the command line has been read.
func NewApp(reg *core.Registry) *App {
	if reg == nil {
		panic("config: NewApp was given no registry. The tree assembles the modules of a " +
			"registry, which for a program is core.ProcessRegistry")
	}
	if _, registered := reg.Lookup(ModuleName); !registered {
		panic(fmt.Sprintf("config: NewApp was given a registry with no module named %q. Execute "+
			"supplies the reader it loads to that module, so the module is registered first: "+
			"reg.Register(config.Module()). core.ProcessRegistry has it from this package's "+
			"init, and a registry made with core.New does not inherit that", ModuleName))
	}
	return &App{
		reg:    reg,
		root:   &Command{use: programName()},
		args:   processArgs(),
		stdout: os.Stdout,
		stderr: os.Stderr,
	}
}

// Root is the command a run selects when it names no subcommand. What it
// takes in, every command under it takes in too, so its arguments are the
// program's global ones.
func (a *App) Root() *Command { return a.root }

// Execute runs the tree: it assembles the commands, reads the command line,
// loads the configuration of the selected command, supplies the reader to the
// config module and drives the lifecycle as that command asked.
//
// The reader is the config module's instance. It is supplied under that
// module's name, so which module it belongs to does not depend on what else the
// registry holds, and it exists before the first module's Prepare runs: a
// module takes it up with core.Resolve in Prepare as in New, and declares no
// requirement to be ordered after it. The command tree does not have to take
// the module in, since a module whose instance was supplied takes part in every
// drive.
//
// The supply is made after the command line and the primary source have been
// read, so a failure of either comes back as the error it is. A registry that
// cannot take the reader makes the supply panic: one that has been driven
// already, one whose config module already has an instance, and one where the
// module registered under that name declares what the reader does not
// implement.
//
// Help is the command-line library's, and asking for it is not a failure: the
// library answers, no command runs, and the result is nil. That answer, and a
// command line, a primary source or a validation that failed, end the run before
// the drive begins: Execute has supplied no reader and driven nothing, yet it
// returns as it does when a drive ended. An instance the host supplied before
// Execute is then still the host's to release, and Participating on the module
// it belongs to tells the cases apart: it is true once the drive has begun.
//
// The lifecycle is driven once, so a tree is run once.
func (a *App) Execute(ctx context.Context) error {
	if a.executed {
		panic("config: Execute was called twice on one App. A run has one command line and " +
			"drives its registry once; a second command needs a second registry")
	}
	a.executed = true

	l, err := newLoader(a.reg, a.stderr)
	if err != nil {
		return a.reportEarly(err)
	}
	p, err := a.compile(l)
	if err != nil {
		return a.reportEarly(err)
	}

	root := p.root.cmd
	root.SetArgs(a.args)
	root.SetOut(a.stdout)
	root.SetErr(a.stderr)
	// A command line the library rejects is classified where its own error
	// types are still in hand. The seam is the library's own, so nothing
	// here reads the text of a message that is the library's to reword. The
	// command it hands over is the one it was parsing, which is the one the
	// run selected, and what that command took in is what the diagnosis of
	// an unknown argument is about.
	root.SetFlagErrorFunc(func(cmd *cobra.Command, err error) error {
		return l.ClassifyParseError(err, p.collectedAt(cmd))
	})

	// Every command's arguments are registered before any of them is
	// located. Locating reads the command line, and an argument written
	// without a value -- which a boolean may be -- is told apart from one
	// whose value follows it only by the registration: a layer still
	// unregistered at that moment has the subcommand name after its boolean
	// swallowed as that argument's value, and the command found is the
	// wrong one. The tree is settled before anything is parsed, so there is
	// nothing to wait for.
	//
	// Each command registers what it shows differently from the commands
	// above it, so the argument of a module two levels of one path take in
	// sits on the shallower and the deeper inherits it, unless the deeper
	// one runs a module the shallower only collects from and marks its
	// required items where the shallower does not.
	for _, node := range p.allNodes() {
		if err := l.BindLayer(node.cmd.PersistentFlags(), node.added(), node.participants); err != nil {
			return a.reportEarly(err)
		}
	}
	// The library adds a command of its own that reports on the others, and
	// it reports on the arguments that are registered when it runs. Adding
	// it here rather than leaving it to the run is what makes `help serve`
	// and `serve --help` say the same thing.
	root.InitDefaultHelpCmd()
	return root.ExecuteContext(ctx)
}

// reportEarly writes a failure that happened before the command-line library
// took over, and hands it back.
//
// Once the library is running it prints what a run failed on, and the host
// prints nothing a second time. The steps ahead of it -- collecting the
// declarations, assembling the tree, registering the arguments -- have no such
// reporter, so their failures would reach the host as a value nothing had
// shown anybody. The line is written the way the library writes its own, since
// a reader cannot tell which side of that boundary a failure came from.
func (a *App) reportEarly(err error) error {
	//nolint:errcheck // a.stderr is where a failure is reported; a failed
	// write of the report has nowhere left to be reported to, and the error
	// itself is returned to the host either way.
	fmt.Fprintln(a.stderr, "Error:", err)
	return err
}

// run is what a command does once the library has parsed the whole of its
// path: load this command's configuration, supply it to the config module,
// drive.
//
// The primary source is read here and nowhere else. Its locator comes from the
// host's default, the environment and the reserved argument, and the argument
// sits on the root, so the locator is settled by the bootstrap layer whatever
// command the run turns out to be.
func (a *App) run(ctx context.Context, l *loader, p *plan, node *planNode) error {
	locator := locate(l.Manifest(), l.env, p.locator.Value.String(), p.locator.Changed)
	if err := l.ReadPrimary(ctx, locator); err != nil {
		return err
	}
	l.ReportUnclaimed()
	l.ReportSensitive()

	// The module is named, so the owner of the reader does not depend on what
	// else the registry holds: a registry with another module delivering
	// Reader takes the call as well. From the start of the drive the instance
	// counts as constructed and the drive runs the module's Stop and Close on
	// it. The module declares neither, because the reader holds the values of
	// the run and nothing to release; a reader that held something would need
	// the module to declare the Close that releases it.
	a.reg.Supply(l.ReaderFor(node.participants), ModuleName)

	return a.reg.Drive(ctx, core.Drive{
		Depth:         node.decl.depth,
		Participants:  node.participants,
		DeclaringOnly: node.declarationsForTheDrive(),
		Action:        node.decl.action,
	})
}

// withArgs and withStreams put a run's inputs in place of the process's own.
// They are unexported because a host has no use for them: the process is
// where a program's arguments and streams come from, and a second way in
// would be a second answer to where they come from.
//
// The environment has no such pair. Every layer that reads one -- the values,
// the unclaimed-variable diagnostic, the locator -- reads the one snapshot of
// the process's own taken when Execute begins, and a second way in would make a
// run's locator come from one environment while its values came from another.
// A case that steers a variable writes it with t.Setenv before Execute.
func (a *App) withArgs(args ...string) *App {
	// A nil list would send the command-line library back to the process's
	// own arguments, which is the one thing a test asking for none means to
	// avoid.
	if args == nil {
		args = []string{}
	}
	a.args = args
	return a
}

func (a *App) withStreams(stdout, stderr io.Writer) *App {
	a.stdout = stdout
	a.stderr = stderr
	return a
}

// programName is what the root command is called: the name the program was
// invoked under, which is the word a reader sees in the help output. A process
// that was given no name at all falls back to a neutral one, since the help
// output has to call the program something.
func programName() string {
	if len(os.Args) == 0 || os.Args[0] == "" {
		return "app"
	}
	return filepath.Base(os.Args[0])
}

// processArgs is the command line without the program name.
func processArgs() []string {
	if len(os.Args) < 2 {
		return []string{}
	}
	return os.Args[1:]
}
