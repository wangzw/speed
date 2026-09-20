package config

import (
	"errors"
	"fmt"
	"slices"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"github.com/vislake/speed/pkg/core"
)

// locatorUsage is what the reserved argument says about itself in the help
// output of every command.
//
// The back-quoted word is the value placeholder the command-line library
// prints after the argument. An input item seats its declared placeholder in
// the Value it hands over, which this argument has none of: it carries no
// declaration, and the usage text is the seat the library leaves for one.
// Without it the library prints the name of the Go type it holds the value
// in, and the one argument this module reserves for itself would be the one
// argument in the help output that names a type rather than a value.
const locatorUsage = "the `LOCATOR` of the primary config source, written as a URI"

// planNode is one command once the tree has been assembled: what the host
// declared, the command the library runs, and the participating set reaching
// this command comes to.
type planNode struct {
	decl   *Command
	cmd    *cobra.Command
	parent *planNode
	// participants is every module the path to this command takes in,
	// dependencies included, deduplicated and in name order. It is what the
	// drive is handed, and an empty one is an empty list rather than nil:
	// a command that assembles nothing takes in nothing.
	participants []string
}

// plan is the assembled tree: the root, and the way back from a command the
// library selected to the node that declared it.
type plan struct {
	root      *planNode
	byCommand map[*cobra.Command]*planNode
	// locator is the reserved --config argument. It sits on the root, so
	// both --config=x serve and serve --config=x give it, and it is settled
	// as soon as the bootstrap layer is parsed.
	locator *pflag.Flag
}

// compile turns the declared tree into the one the command-line library runs,
// and settles each command's participating set.
//
// It happens before anything is parsed, so the whole tree -- which commands
// exist, which arguments each carries, how far each drives -- is fixed before
// the first argument is read. Nothing about a run can add a command or an
// argument.
func (a *App) compile(l *loader) (*plan, error) {
	mods := a.reg.Modules()
	p := &plan{byCommand: make(map[*cobra.Command]*planNode)}

	root, err := a.assemble(l, p, a.root, nil, mods)
	if err != nil {
		return nil, err
	}
	p.root = root

	root.cmd.PersistentFlags().String(locatorFlag, "", locatorUsage)
	p.locator = root.cmd.PersistentFlags().Lookup(locatorFlag)
	return p, nil
}

// assemble builds one command and everything under it.
func (a *App) assemble(l *loader, p *plan, decl *Command, parent *planNode, mods []core.Module) (*planNode, error) {
	participants, err := a.participantsOf(decl, parent, mods)
	if err != nil {
		return nil, err
	}
	node := &planNode{decl: decl, parent: parent, participants: participants}
	node.cmd = &cobra.Command{
		Use: decl.use,
		// No command takes positional arguments: an action receives the
		// context and the registry, and has nowhere to put them.
		Args: cobra.NoArgs,
		// A failure past the parsing of the command line is not a
		// misuse of the syntax, and printing the whole usage over it
		// buries the one line that says what went wrong. Errors
		// themselves are left to the library to print.
		SilenceUsage: true,
	}
	switch {
	case decl.hasDo:
		node.cmd.RunE = func(cmd *cobra.Command, _ []string) error {
			return a.run(cmd.Context(), l, p, node)
		}
	case len(decl.children) > 0:
		// A command that only groups others answers with its own help,
		// which is the list of what can be written after it. Answering
		// at all is what makes the word that follows it an argument the
		// library checks: a command that runs nothing is skipped before
		// the check, and a misspelled subcommand would be met with the
		// help text and a success.
		node.cmd.RunE = func(cmd *cobra.Command, _ []string) error {
			return cmd.Help()
		}
	default:
		return nil, fmt.Errorf("config: command %q says nothing about what it does. A command "+
			"with no subcommands under it is one a run can select, so it states how far it "+
			"drives and what it does on arrival, with Do", decl.path())
	}
	p.byCommand[node.cmd] = node

	for _, child := range decl.children {
		childNode, err := a.assemble(l, p, child, node, mods)
		if err != nil {
			return nil, err
		}
		node.cmd.AddCommand(childNode.cmd)
	}
	return node, nil
}

// participantsOf settles what one command assembles: the modules its own
// level takes in, the ones every level above it takes in, and the providers of
// what all of those require.
//
// The dependency closure is the registry's to expand. The rule that matches a
// requirement to a delivery lives there, and a second expansion here would be
// a second reading of that rule: the two would divide only on the runs where
// they disagreed, and nothing on a normal path would show it.
func (a *App) participantsOf(decl *Command, parent *planNode, mods []core.Module) ([]string, error) {
	var seeds []string
	if parent != nil {
		seeds = slices.Clone(parent.participants)
	}
	seeds = append(seeds, includedAt(decl, mods)...)

	participants, err := a.reg.Closure(seeds)
	if err != nil {
		if errors.Is(err, core.ErrUnknownParticipant) {
			// Which command wrote the name is the one thing the registry
			// cannot say; what to do about it, it already said.
			return nil, fmt.Errorf("config: command %q includes a module that is not "+
				"registered: %w", decl.path(), err)
		}
		return nil, err
	}
	return participants, nil
}

// includedAt lists the modules one level takes in, with "every available
// module" expanded into the names the registry holds.
//
// The expansion belongs here, where the tree is written: what the drive
// receives is then always a literal list, and core never has to answer a
// question about which command took what in. Modules carrying resources alone
// are in it -- available means available -- and they bring no arguments with
// them, so nothing the user has not heard of appears on the command line.
//
// Naming modules and taking all of them in are not in conflict: everything
// available is a superset of anything listed.
func includedAt(c *Command, mods []core.Module) []string {
	if !c.includeAll {
		return slices.Clone(c.include)
	}
	names := make([]string, len(mods))
	for i, m := range mods {
		names[i] = m.Name
	}
	return names
}

// allNodes lists every node of the tree, shallowest first.
//
// Every command's arguments are registered, not only those of the command a
// run selects, because locating that command reads the command line: an
// argument written without a value -- which a boolean may be -- is told apart
// from one whose value follows it only by the registration, so a command
// whose arguments are still unregistered has the subcommand name after its
// boolean swallowed as that argument's value. Registering the whole tree
// leaves nothing to locate.
func (p *plan) allNodes() []*planNode {
	out := []*planNode{p.root}
	for at := 0; at < len(out); at++ {
		for _, child := range out[at].cmd.Commands() {
			if node, declared := p.byCommand[child]; declared {
				out = append(out, node)
			}
		}
	}
	return out
}

// added lists the modules this command takes in that no command above it
// takes in. It is what decides where an argument is registered: on the
// shallowest command of a path whose participating set holds the module,
// which every command below then inherits. A second definition deeper down
// would shadow the one the layering tool is bound to, and the value would be
// given on the command line and never read.
//
// Commands on separate branches stand above none of each other, so each of
// them registers what it takes in, and the argument they register is one and
// the same.
func (n *planNode) added() []string {
	if n.parent == nil {
		return n.participants
	}
	above := make(map[string]bool, len(n.parent.participants))
	for _, name := range n.parent.participants {
		above[name] = true
	}
	out := make([]string, 0, len(n.participants))
	for _, name := range n.participants {
		if !above[name] {
			out = append(out, name)
		}
	}
	return out
}
