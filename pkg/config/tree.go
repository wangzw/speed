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
// output of every command. The back-quoted word is the value placeholder the
// command-line library prints after the argument.
const locatorUsage = "the `LOCATOR` of the primary config source, written as a URI"

// planNode is one command once the tree has been assembled.
type planNode struct {
	decl   *Command
	cmd    *cobra.Command
	parent *planNode
	// participants is every module the path to this command takes in and runs,
	// deduplicated and in name order.
	participants []string
	// declaringOnly accumulates what the path to this command reaches for its
	// declarations alone, carrying the adjustments the host made to each.
	declaringOnly []collectedModule
}

// plan is the assembled tree: the root, and the way back from a command the
// library selected to the node that declared it.
type plan struct {
	root      *planNode
	byCommand map[*cobra.Command]*planNode
	// locator is the reserved --config argument, settled as soon as the
	// bootstrap layer is parsed.
	locator *pflag.Flag
}

// compile turns the declared tree into the one the command-line library runs,
// and settles each command's participating set. It happens before anything is
// parsed, so a run cannot add a command or an argument.
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
		// No command takes positional arguments.
		Args: cobra.NoArgs,
		// A failure past parsing is not a syntax misuse, and printing the whole
		// usage over it buries the one line that says what went wrong.
		SilenceUsage: true,
	}
	switch {
	case decl.hasDo:
		node.cmd.RunE = func(cmd *cobra.Command, _ []string) error {
			return a.run(cmd.Context(), l, p, node)
		}
	case len(decl.children) > 0:
		// Answering at all makes the word that follows a grouping command an
		// argument the library checks, rather than a misspelled subcommand met
		// with the help text and a success.
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

// setsOf settles what one command reaches: the modules it runs, and the ones it
// reaches for their declarations alone.
//
// The first group is this level's run-level modules, every module the levels
// above took in, and the providers of what all of those require. The expansion
// is the registry's, so the matching rule is read in one place. The second group
// is not expanded: a declaration-level module is not constructed. A module named
// at both levels is a participant and not in the second group.
func (a *App) setsOf(decl *Command, parent *planNode, mods []core.Module) (
	participants []string, declaringOnly []collectedModule, err error,
) {
	var runSeeds []string
	if parent != nil {
		runSeeds = slices.Clone(parent.participants)
	}
	runSeeds = append(runSeeds, includedAt(decl, mods)...)

	participants, err = a.reg.Closure(runSeeds)
	if err != nil {
		if errors.Is(err, core.ErrUnknownParticipant) {
			return nil, nil, fmt.Errorf("config: command %q includes a module that is "+
				"not registered: %w", decl.path(), err)
		}
		return nil, nil, err
	}

	mine := declarationsAt(decl, mods)
	for _, d := range mine {
		if _, registered := a.reg.Lookup(d.name); !registered {
			return nil, nil, fmt.Errorf("config: command %q includes a module that is "+
				"not registered: %w: %q", decl.path(), core.ErrUnknownParticipant, d.name)
		}
	}

	running := make(map[string]bool, len(participants))
	for _, name := range participants {
		running[name] = true
	}

	if len(mine) == 0 && parent != nil {
		declaringOnly = withoutRunning(parent.declaringOnly, running)
	} else {
		var seeds []collectedModule
		if parent != nil {
			seeds = slices.Clone(parent.declaringOnly)
		}
		declaringOnly = withoutRunning(append(seeds, mine...), running)
	}

	return participants, declaringOnly, nil
}

// withoutRunning drops the declarations of modules this command runs, keeping
// the order it was given.
func withoutRunning(declared []collectedModule, running map[string]bool) []collectedModule {
	out := make([]collectedModule, 0, len(declared))
	for _, d := range declared {
		if !running[d.name] {
			out = append(out, d)
		}
	}
	return out
}

// declarationsAt lists what one level takes in for its declarations alone, with
// "every available module" expanded into names. The expansion carries the
// per-inclusion adjustments along.
func declarationsAt(c *Command, mods []core.Module) []collectedModule {
	var out []collectedModule
	for _, inc := range c.declIncludes {
		names := inc.names
		if inc.all {
			names = make([]string, len(mods))
			for i, m := range mods {
				names[i] = m.Name
			}
		}
		for _, name := range names {
			out = append(out, collectedModule{
				name:         name,
				withFlags:    inc.withFlags,
				allResources: inc.allResources,
			})
		}
	}
	return out
}

// includedAt lists the modules one level takes in, with "every available module"
// expanded into the names the registry holds.
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

// allNodes lists every node of the tree, shallowest first. Every command's
// arguments are registered, not only the selected command's, because locating
// the selected command reads the command line.
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

// added lists the modules whose arguments this command shows in a way no
// command above it already does, deciding where each argument's face is
// registered. It reads the flagged set, not the participating set.
func (n *planNode) added() []string {
	if n.parent == nil {
		return n.flagModules()
	}
	above := make(map[string]bool)
	aboveRuns := make(map[string]bool, len(n.parent.participants))
	for _, name := range n.parent.participants {
		aboveRuns[name] = true
	}
	runs := make(map[string]bool, len(n.participants))
	for _, name := range n.participants {
		runs[name] = true
	}
	for _, name := range n.parent.flagModules() {
		if aboveRuns[name] || !runs[name] {
			above[name] = true
		}
	}
	out := make([]string, 0, len(n.flagModules()))
	for _, name := range n.flagModules() {
		if !above[name] {
			out = append(out, name)
		}
	}
	return out
}

// declarationsForTheDrive renders the accumulated list in the form a drive
// takes: the module names, and the one adjustment the registry acts on.
func (n *planNode) declarationsForTheDrive() []core.Declaration {
	out := make([]core.Declaration, 0, len(n.declaringOnly))
	for _, d := range n.declaringOnly {
		out = append(out, core.Declaration{Name: d.name, AllResources: d.allResources})
	}
	return out
}

// collectedAt lists the modules the given command reached for their
// declarations alone.
func (p *plan) collectedAt(cmd *cobra.Command) []string {
	node, declared := p.byCommand[cmd]
	if !declared {
		return nil
	}
	out := make([]string, 0, len(node.declaringOnly))
	for _, d := range node.declaringOnly {
		out = append(out, d.name)
	}
	return out
}

// flagModules lists the modules whose input items this command puts on its
// command line: the ones it runs, plus the declaration-level ones a host asked
// for by name.
func (n *planNode) flagModules() []string {
	out := slices.Clone(n.participants)
	for _, d := range n.declaringOnly {
		if d.withFlags {
			out = append(out, d.name)
		}
	}
	slices.Sort(out)
	return out
}
