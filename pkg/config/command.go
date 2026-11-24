package config

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/vislake/speed/pkg/core"
)

// Action is what a command does once the drive has reached its depth. It runs
// before shutdown begins, and its failure is the failure of the run.
type Action func(ctx context.Context, reg *core.Registry) error

// Command is one command of the tree: which modules this level takes in, which
// commands sit under it, and how far the run drives once it is selected.
//
// The declarations are recorded as written and looked up when Execute assembles
// the tree.
type Command struct {
	use          string
	parent       *Command
	children     []*Command
	include      []string
	includeAll   bool
	declIncludes []declarationInclusion
	depth        core.Stage
	action       Action
	hasDo        bool
}

// Sub adds a command under this one and returns it. use is the single word that
// selects the command; Sub panics on a word that cannot select one or that a
// sibling already uses.
func (c *Command) Sub(use string) *Command {
	if use == "" {
		panic("config: a subcommand of " + c.path() + " was declared with an empty name. The " +
			"name is the word that selects the command on the command line")
	}
	if strings.ContainsFunc(use, func(r rune) bool { return r == ' ' || r == '\t' || r == '\n' }) {
		panic(fmt.Sprintf("config: subcommand %q of %s has a space in its name. One command is "+
			"selected by one word; a command under another is written with Sub on it",
			use, c.path()))
	}
	if strings.HasPrefix(use, "-") {
		panic(fmt.Sprintf("config: subcommand %q of %s begins with a dash. A word beginning with "+
			"one is read as an argument and never as a command, so nothing a run could write "+
			"would select this command",
			use, c.path()))
	}
	for _, existing := range c.children {
		if existing.use == use {
			panic(fmt.Sprintf("config: %s declares two subcommands named %q. The name is what "+
				"selects the command, so the second one could never be reached",
				c.path(), use))
		}
	}
	child := &Command{use: use, parent: c}
	c.children = append(c.children, child)
	return child
}

// Include takes the named modules into this command, and with them the
// providers of what they require to be constructed. A provider that declares an
// exclusive delivery is not among them. What a level takes in holds for every
// command under it. Naming a module twice takes it in once.
func (c *Command) Include(names ...string) {
	for _, name := range names {
		if name == "" {
			panic("config: " + c.path() + " includes a module under an empty name. A module " +
				"is taken in by the name it registered, which the package that registers " +
				"it exports as a constant")
		}
		if !slices.Contains(c.include, name) {
			c.include = append(c.include, name)
		}
	}
}

// IncludeAll takes every available module into this command. The available set
// is what the host's imports reach, including modules an indirect dependency
// registers. Every name it expands to joins at run level and has to start; a
// command that wants the declarations alone uses IncludeAllDeclarations.
func (c *Command) IncludeAll() { c.includeAll = true }

// declarationInclusion is one declaration-level inclusion as written, with the
// adjustments the host made on the value it returned.
type declarationInclusion struct {
	names        []string
	all          bool
	withFlags    bool
	allResources bool
}

// collectedModule is one module a command reaches for its declarations alone,
// with the adjustments the inclusions that named it asked for.
type collectedModule struct {
	name         string
	withFlags    bool
	allResources bool
}

// Inclusion is what a declaration-level inclusion returns, so that a host can
// adjust it where it was written.
type Inclusion struct {
	cmd *Command
	at  int
}

// IncludeDeclarations takes the named modules into this command for their
// declarations alone.
//
// Such a module is not constructed and its requirements are not expanded. Its
// input items do not reach the command line unless WithFlags says so, and its
// required items are never checked. A name no module registered is
// core.ErrUnknownParticipant.
func (c *Command) IncludeDeclarations(names ...string) *Inclusion {
	for _, name := range names {
		if name == "" {
			panic("config: " + c.path() + " includes a module under an empty name. A module " +
				"is taken in by the name it registered, which the package that registers " +
				"it exports as a constant")
		}
	}
	c.declIncludes = append(c.declIncludes, declarationInclusion{names: slices.Clone(names)})
	return &Inclusion{cmd: c, at: len(c.declIncludes) - 1}
}

// IncludeAllDeclarations takes every available module into this command for its
// declarations alone. It adds nothing to the command line or to a run's
// obligations.
func (c *Command) IncludeAllDeclarations() *Inclusion {
	c.declIncludes = append(c.declIncludes, declarationInclusion{all: true})
	return &Inclusion{cmd: c, at: len(c.declIncludes) - 1}
}

// WithFlags lets the modules of this inclusion reach the command line of the
// command that took them in, and of every command beneath it. It does not make
// them required.
func (i *Inclusion) WithFlags() *Inclusion {
	i.cmd.declIncludes[i.at].withFlags = true
	return i
}

// AllResources puts every declaration the modules of this inclusion make in
// force, overriding the set the consumption point would otherwise read.
func (i *Inclusion) AllResources() *Inclusion {
	i.cmd.declIncludes[i.at].allResources = true
	return i
}

// Do states how far this command drives and what it does on arrival. depth is
// where the lifecycle stops. A second call panics, as does a depth that is not
// one of the stages.
func (c *Command) Do(depth core.Stage, action Action) {
	if c.hasDo {
		panic("config: " + c.path() + " declares Do twice. A command drives to one depth and " +
			"does one thing on arrival")
	}
	if depth > core.StageServe {
		panic(fmt.Sprintf("config: %s drives to stage %d, which is not one of them. The depth "+
			"is a core.Stage constant, StagePrepare through StageServe",
			c.path(), uint8(depth)))
	}
	c.hasDo = true
	c.depth = depth
	c.action = action
}

// path renders the command as it is written on the command line, root word
// first.
func (c *Command) path() string {
	words := []string{c.use}
	for at := c.parent; at != nil; at = at.parent {
		words = append(words, at.use)
	}
	slices.Reverse(words)
	return strings.Join(words, " ")
}
