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
// Execute runs it.
type App struct {
	reg      *core.Registry
	root     *Command
	args     []string
	stdout   io.Writer
	stderr   io.Writer
	executed bool
}

// NewApp starts a command tree over a registry. The tree is empty: the host
// writes it on Root, and the root command is named after the program.
//
// The registry has to hold the config module, which Execute supplies the reader
// to; NewApp panics when it is not there.
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
// Help, and a command line, primary source or validation failure, end the run
// before the drive begins and return nil or the error. The lifecycle is driven
// once, so a tree is run once.
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
	root.SetFlagErrorFunc(func(cmd *cobra.Command, err error) error {
		return l.ClassifyParseError(err, p.collectedAt(cmd))
	})

	// Every command's arguments are registered before any of them is located: a
	// layer still unregistered at that moment has the subcommand name after its
	// boolean swallowed as that argument's value.
	for _, node := range p.allNodes() {
		if err := l.BindLayer(node.cmd.PersistentFlags(), node.added(), node.participants); err != nil {
			return a.reportEarly(err)
		}
	}
	root.InitDefaultHelpCmd()
	return root.ExecuteContext(ctx)
}

// reportEarly writes a failure that happened before the command-line library
// took over, and hands it back.
func (a *App) reportEarly(err error) error {
	//nolint:errcheck // a.stderr is where a failure is reported; a failed
	// write of the report has nowhere left to be reported to, and the error
	// itself is returned to the host either way.
	fmt.Fprintln(a.stderr, "Error:", err)
	return err
}

// run is what a command does once the library has parsed the whole of its path:
// load this command's configuration, supply it to the config module, drive.
func (a *App) run(ctx context.Context, l *loader, p *plan, node *planNode) error {
	locator := locate(l.Manifest(), l.env, p.locator.Value.String(), p.locator.Changed)
	if err := l.ReadPrimary(ctx, locator); err != nil {
		return err
	}
	l.ReportUnclaimed()
	l.ReportSensitive()

	// The module is named, so the owner of the reader does not depend on what
	// else the registry holds.
	a.reg.Supply(l.ReaderFor(node.participants), ModuleName)

	return a.reg.Drive(ctx, core.Drive{
		Depth:         node.decl.depth,
		Participants:  node.participants,
		DeclaringOnly: node.declarationsForTheDrive(),
		Action:        node.decl.action,
	})
}

// withArgs and withStreams put a run's inputs in place of the process's own.
// The environment has no such pair: every layer reads one snapshot taken when
// Execute begins, and a case that steers a variable writes it with t.Setenv
// before Execute.
func (a *App) withArgs(args ...string) *App {
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
// invoked under.
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
