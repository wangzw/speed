package config

import (
	"errors"
	"fmt"
	"slices"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"github.com/vislake/speed/pkg/core"
)

// locatorUsage is what the reserved argument says about itself; its back-quoted word is the placeholder the command-line library prints.
const locatorUsage = "the `LOCATOR` of the primary config source, written as a URI"

type planNode struct {
	decl   *Command
	cmd    *cobra.Command
	parent *planNode
	// participants is the drive's set, deduplicated and in name order; an empty set is an empty slice, never nil.
	participants  []string
	declaringOnly []collectedModule
}

type plan struct {
	root      *planNode
	byCommand map[*cobra.Command]*planNode
	locator   *pflag.Flag
}

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
		Use:          decl.use,
		Args:         cobra.NoArgs,
		SilenceUsage: true,
	}
	switch {
	case decl.hasDo:
		node.cmd.RunE = func(cmd *cobra.Command, _ []string) error {
			return a.run(cmd.Context(), l, p, node)
		}
	case len(decl.children) > 0:
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

func withoutRunning(declared []collectedModule, running map[string]bool) []collectedModule {
	out := make([]collectedModule, 0, len(declared))
	for _, d := range declared {
		if !running[d.name] {
			out = append(out, d)
		}
	}
	return out
}

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

func (n *planNode) declarationsForTheDrive() []core.Declaration {
	out := make([]core.Declaration, 0, len(n.declaringOnly))
	for _, d := range n.declaringOnly {
		out = append(out, core.Declaration{Name: d.name, AllResources: d.allResources})
	}
	return out
}

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
