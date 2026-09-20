package config

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/vislake/speed/pkg/core"
)

// Action is what a command does once the drive has reached its depth. It is
// the host's, runs before shutdown begins, and its failure is the failure of
// the run.
//
// A command that leaves it out states that arriving at the depth was the whole
// of its work. At StageServe that is serving traffic, and the drive blocks
// until the context is cancelled.
type Action func(ctx context.Context, reg *core.Registry) error

// Command is one command of the tree: which modules this level takes in, which
// commands sit under it, and how far the run drives once it is selected.
//
// A host writes the tree in main and nothing else contributes to it: a module
// does not declare commands and cannot learn which ones take it in.
//
// The declarations are recorded as written and nothing is checked against the
// registry here. A tree is usually written before the last package's init has
// registered anything, so a name is looked up when Execute assembles the tree,
// not while it is being declared.
type Command struct {
	// use is the word that selects this command. The root's is the
	// program's own name.
	use      string
	parent   *Command
	children []*Command
	// include names the modules this level takes in, in declaration order.
	include []string
	// includeAll records that this level takes in every available module,
	// whatever the registry turns out to hold.
	includeAll bool
	depth      core.Stage
	action     Action
	// hasDo tells a command that drives to StagePrepare from one that never
	// said how far it goes. The depth's zero value is a legal answer, so the
	// field cannot carry the question.
	hasDo bool
}

// Sub adds a command under this one and returns it, so that the level's own
// declarations read as a block.
//
// use is the single word that selects the command. It panics on a word that
// cannot select one -- empty, carrying a space, or beginning with a dash --
// and on one a sibling already uses, which would leave two commands that no
// argument can tell apart.
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
// providers of what they require to be constructed.
//
// A module is taken in by name, which is its identity: the host imported the
// package that registers it, and that package exports the name as a constant
// to be written here. What a level takes in holds for every command under it,
// so the root's modules are the ones every command has.
//
// Naming a module twice takes it in once. So does naming it on two levels of
// one path; the arguments it contributes are registered on the shallower one.
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
// is what the host's imports reach, which is wider than what the host lists
// itself: a module an indirect dependency registers is in it too.
//
// The set is expanded into names while the tree is assembled, so the drive is
// handed the same literal list an explicit Include would have produced.
func (c *Command) IncludeAll() { c.includeAll = true }

// Do states how far this command drives and what it does on arrival.
//
// depth is where the lifecycle stops: StageServe runs the whole of it, and a
// shallower one is a command whose work is done earlier -- applying migrations,
// printing what the stances came to. Whatever the depth, the drive releases
// what it constructed; the host has no second call to remember.
//
// A second call panics, as does a depth that is not one of the stages. Both are
// decided by this one call against what the type admits.
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
// first. It is how a diagnostic names the command it is about, since the words
// on their own repeat across levels.
func (c *Command) path() string {
	words := []string{c.use}
	for at := c.parent; at != nil; at = at.parent {
		words = append(words, at.use)
	}
	slices.Reverse(words)
	return strings.Join(words, " ")
}
