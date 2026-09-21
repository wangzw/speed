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
	// participants is every module the path to this command takes in and
	// runs, dependencies included, deduplicated and in name order. It is
	// what the drive is handed as its participating set, and an empty one
	// is an empty list rather than nil: a command that assembles nothing
	// takes in nothing.
	participants []string
	// declaringOnly accumulates what the path to this command reaches for
	// its declarations alone, carrying the adjustments the host made to
	// each.
	//
	// It is an accumulation and not a folded set: a module named at two
	// levels appears once per naming, and a module this command runs is
	// not filtered out here. Folding is the registry's, because the
	// drive is a contract a host can build by hand and what a Drive
	// means cannot depend on whether a command tree built it -- see
	// recordDeclarationSets. Everything this side reads it for is
	// indifferent to repetition, and a module named twice costs one
	// entry.
	declaringOnly []core.Declaration
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
	participants, declaringOnly, err := a.setsOf(decl, parent, mods)
	if err != nil {
		return nil, err
	}
	node := &planNode{
		decl:          decl,
		parent:        parent,
		participants:  participants,
		declaringOnly: declaringOnly,
	}
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

// setsOf settles what one command reaches: the modules it runs, and the ones
// it reaches for their declarations alone.
//
// The first group is this level's run-level modules, every module the levels
// above took in, and the providers of what all of those require. The
// dependency closure is the registry's to expand. The rule that matches a
// requirement to a delivery lives there, and a second expansion here would be
// a second reading of that rule: the two would divide only on the runs where
// they disagreed, and nothing on a normal path would show it.
//
// The second group is not expanded. A module reached for its declarations
// alone is not constructed, so it has no construction-time requirements for a
// closure to follow, and expanding one would build providers on behalf of a
// module that never asked for them.
//
// A module named at both levels is a participant here as it is in the
// registry, so it appears in the first group and not the second. The two
// levels are settled together rather than in sequence for that reason: which
// one a name belongs to is a fact about the whole of what the host wrote.
//
// The third thing a command needs -- which modules put input items on its
// command line -- is not settled here but derived, by flagModules, so that it
// cannot come to disagree with the two groups it is made of.
func (a *App) setsOf(decl *Command, parent *planNode, mods []core.Module) (
	participants []string, declaringOnly []core.Declaration, err error,
) {
	var runSeeds []string
	if parent != nil {
		runSeeds = slices.Clone(parent.participants)
	}
	runSeeds = append(runSeeds, includedAt(decl, mods)...)

	participants, err = a.reg.Closure(runSeeds)
	if err != nil {
		if errors.Is(err, core.ErrUnknownParticipant) {
			// Which command wrote the name is the one thing the registry
			// cannot say; what to do about it, it already said.
			return nil, nil, fmt.Errorf("config: command %q includes a module that is "+
				"not registered: %w", decl.path(), err)
		}
		return nil, nil, err
	}

	mine := declarationsAt(decl, mods)
	for _, d := range mine {
		// The name is looked up rather than expanded: a module reached
		// this way is not constructed, so it has no requirements for the
		// registry to follow, and asking it for the closure would pull in
		// providers this command never asked to build.
		if _, registered := a.reg.Lookup(d.Name); !registered {
			return nil, nil, fmt.Errorf("config: command %q includes a module that is "+
				"not registered: %w: %q", decl.path(), core.ErrUnknownParticipant, d.Name)
		}
	}

	running := make(map[string]bool, len(participants))
	for _, name := range participants {
		running[name] = true
	}

	if len(mine) == 0 && parent != nil {
		// This level adds none of its own, which is the ordinary case: what
		// the levels above reached carries over as it stands, minus
		// whatever this level has since taken in at run level. Their names
		// were looked up where they were written, so there is nothing to
		// check here either.
		declaringOnly = withoutRunning(parent.declaringOnly, running)
	} else {
		var seeds []core.Declaration
		if parent != nil {
			seeds = slices.Clone(parent.declaringOnly)
		}
		declaringOnly = withoutRunning(append(seeds, mine...), running)
	}

	return participants, declaringOnly, nil
}

// withoutRunning drops the declarations of modules this command runs, keeping
// the order it was given.
//
// A level that adds no declaration-level inclusion of its own takes what the
// levels above reached as it stands, and this is the one way that set can
// still have to change: taking a module in at run level here makes it a
// participant, and a participant is not reached for its declarations alone.
func withoutRunning(declared []core.Declaration, running map[string]bool) []core.Declaration {
	out := make([]core.Declaration, 0, len(declared))
	for _, d := range declared {
		if !running[d.Name] {
			out = append(out, d)
		}
	}
	return out
}

// declarationsAt lists what one level takes in for its declarations alone,
// with "every available module" expanded into names.
//
// The expansion belongs here for the same reason the participating set's
// does: what the drive receives is then always a literal list. What differs
// is that the expansion carries the adjustments along, since they are set per
// inclusion and two inclusions of one command may disagree about them.
func declarationsAt(c *Command, mods []core.Module) []core.Declaration {
	var out []core.Declaration
	for _, inc := range c.declIncludes {
		names := inc.names
		if inc.all {
			names = make([]string, len(mods))
			for i, m := range mods {
				names[i] = m.Name
			}
		}
		for _, name := range names {
			out = append(out, core.Declaration{
				Name:         name,
				WithFlags:    inc.withFlags,
				AllResources: inc.allResources,
			})
		}
	}
	return out
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

// added lists the modules whose arguments this command contributes and no
// command above it does. It is what decides where an argument is registered:
// on the shallowest command of a path whose flagged set holds the module,
// which every command below then inherits. A second definition deeper down
// would shadow the one the layering tool is bound to, and the value would be
// given on the command line and never read.
//
// It reads the flagged set rather than the participating set, and the two
// differ exactly when a host asked for a declaration-level module's arguments
// to be reachable. Reading the participating set instead would put the
// arguments of every declaration-level module on the command line, which is
// the thing taking them in at declaration level avoids.
//
// Commands on separate branches stand above none of each other, so each of
// them registers what it flags, and the argument they register is one and the
// same.
func (n *planNode) added() []string {
	if n.parent == nil {
		return n.flagModules()
	}
	return subtract(n.flagModules(), n.parent.flagModules())
}

// declaredNames lists the modules this command reached for their declarations
// alone, in the order it holds them, which is name order.
//
// It is what the argument-registering code needs to tell an argument that was
// deliberately kept off the command line from one of a module the command never
// took in; those two get different diagnoses and different fixes.
func (n *planNode) declaredNames() []string {
	out := make([]string, 0, len(n.declaringOnly))
	for _, d := range n.declaringOnly {
		out = append(out, d.Name)
	}
	return out
}

// flagModules lists the modules whose input items this command puts on its
// command line: the ones it runs, plus the declaration-level ones a host asked
// for by name.
//
// It is derived from the two sets rather than settled alongside them, so that
// the three cannot come apart. It is deliberately not the participating set:
// every module in that set contributes its arguments, and a declaration-level
// module does so only because the host said it should -- which is what keeps a
// command collecting declarations from growing arguments it never wanted.
func (n *planNode) flagModules() []string {
	out := slices.Clone(n.participants)
	for _, d := range n.declaringOnly {
		if d.WithFlags {
			out = append(out, d.Name)
		}
	}
	slices.Sort(out)
	return out
}

// subtract lists what is in names and not in others, in the order it was given.
// Both sides are sorted and free of repetition, which is what lets one pass
// over a membership map answer it.
func subtract(names, others []string) []string {
	minus := make(map[string]bool, len(others))
	for _, name := range others {
		minus[name] = true
	}
	out := make([]string, 0, len(names))
	for _, name := range names {
		if !minus[name] {
			out = append(out, name)
		}
	}
	return out
}
