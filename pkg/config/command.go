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
	// declIncludes records the declaration-level inclusions, in declaration
	// order, each carrying whatever the host adjusted on the value it
	// returned. They are kept apart from include because the two levels
	// differ in what they do, not in how much of it there is: a module
	// named in both is a participant, and the drive settles that.
	declIncludes []declarationInclusion
	depth        core.Stage
	action       Action
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

// declarationInclusion is one declaration-level inclusion as written, with
// the adjustments the host made on the value it returned.
type declarationInclusion struct {
	names        []string
	all          bool
	withFlags    bool
	allResources bool
}

// collectedModule is one module a command reaches for its declarations alone,
// with the adjustments the inclusions that named it asked for.
//
// It is this module's own type rather than core.Declaration because one of the
// two adjustments never crosses that boundary. WithFlags decides whether the
// module's input items reach this command's command line; the registry neither
// writes a command line nor recognises one, so a field for it on the type a
// drive takes would be read by no one there and ignored without a word by a
// Drive built by hand. What the drive acts on is converted on the way in.
type collectedModule struct {
	name         string
	withFlags    bool
	allResources bool
}

// Inclusion is what a declaration-level inclusion returns, so that a host can
// adjust it where it was written.
//
// It is not the command. One command can carry several declaration-level
// inclusions and a host may want them treated differently, so an adjustment
// made here says nothing about any other one: the value carries which
// inclusion it is, not only which command.
//
// Its methods are on this type alone, and it is returned by the
// declaration-level entries alone. A module that runs contributes its
// arguments and has its required items checked with no adjustment available,
// and asking for one where it would have no effect is a compile error rather
// than a startup failure.
type Inclusion struct {
	cmd *Command
	at  int
}

// IncludeDeclarations takes the named modules into this command for their
// declarations alone.
//
// Such a module is not constructed: it runs no callback, takes no stance, and
// its requirements are not expanded, since expanding them is what makes its
// providers be constructed beside it. What it contributes is its resource
// declarations, and only to a consumption point that reads the declaring set
// or wider. Its input items do not reach the command line unless WithFlags
// says so, and its required items are never checked.
//
// This is how a command collects the declarations of modules it does not run.
// A command applying migrations includes the engine and reaches its tables
// this way, which is what keeps the engine's own arguments from being the
// only ones on a command that must not be made to answer for a listener
// address it never opens.
//
// Naming a module twice takes it in once, and an adjustment made on one of
// them holds for both. That reading belongs to the registry rather than to
// this tree: see core.Drive, where the list is folded once for every Drive
// whatever wrote it. This level accumulates what each naming asked for and
// hands the accumulation over.
//
// A name no module registered is core.ErrUnknownParticipant, the same as for
// Include: a declaration-level name that goes unmatched leaves declarations
// out silently, which is the failure this whole level exists to make
// preventable.
func (c *Command) IncludeDeclarations(names ...string) *Inclusion {
	for _, name := range names {
		if name == "" {
			panic("config: " + c.path() + " includes a module under an empty name. A module " +
				"is taken in by the name it registered, which the package that registers " +
				"it exports as a constant")
		}
	}
	// The slice is copied rather than kept. A variadic call passes the
	// caller's own slice when it spreads one, and a tree is written ahead
	// of the run that reads it: a host that builds a list of modules, hands
	// it over and goes on using it would otherwise be writing into this
	// command's declarations from outside them. Include copies for the same
	// reason, and the two have to answer a caller's aliasing the same way.
	c.declIncludes = append(c.declIncludes, declarationInclusion{names: slices.Clone(names)})
	return &Inclusion{cmd: c, at: len(c.declIncludes) - 1}
}

// IncludeAllDeclarations takes every available module into this command for
// its declarations alone. The available set is what the host's imports reach,
// the same set IncludeAll spans.
//
// It is the way to write a command that collects everything declared and runs
// almost none of it, and it is what makes that command's collection complete
// without a list the host has to keep in step. IncludeAll carries a cost this
// one does not: the participating set decides the help output, the available
// arguments and the required range, all of them things a person reads. The
// declaring set decides none of those, so taking every module's declarations
// in adds nothing to the command line and nothing to a run's obligations.
//
// What it does add is reach: a module the host never heard of, registered by
// a package some dependency imported, has its declarations collected too.
// That is the point, and it is also the price, since the host cannot see the
// list it is accepting.
func (c *Command) IncludeAllDeclarations() *Inclusion {
	c.declIncludes = append(c.declIncludes, declarationInclusion{all: true})
	return &Inclusion{cmd: c, at: len(c.declIncludes) - 1}
}

// WithFlags lets the modules of this inclusion reach the command line of the
// command that took them in, and of every command beneath it.
//
// It does not make them required: a module reached for its declarations alone
// is never constructed, so it never decodes its configuration and there is no
// reading of it that could come up short. The flag is an argument a run may
// give, not one it must.
//
// It serves one shape: a command that runs no module at all and reports or
// checks the configuration. Such a command has to collect every module's
// input items and let the operator give an override on its own command line,
// without constructing anything or enforcing any required item. Neither
// existing way gets there. Taking the modules in at declaration level alone
// gives the command no arguments to offer; taking them in at run level builds
// them and turns their required items on, which is what a command that only
// reports must not do.
//
// No command of that shape exists today, and none has to: this module defines
// no subcommands. So the adjustment is here to make the shape writable, not
// because something needs it now, and the price is already paid -- a module's
// required items are never enforced at this level, so the argument is one no
// module will read.
func (i *Inclusion) WithFlags() *Inclusion {
	i.cmd.declIncludes[i.at].withFlags = true
	return i
}

// AllResources puts every declaration the modules of this inclusion make in
// force, overriding the set the consumption point would otherwise read.
//
// Which set a declaration is collected from is the consumption point's
// decision, and this overrides it. A point reading the enabled set then
// collects these modules' declarations as though the modules were running,
// and they are not.
//
// The price is concrete and it lands on the consumption point that has the
// most to lose by it: a database plugin is installed onto the handle for the
// statements this run issues, and this installs the plugins of modules that
// are not part of the run at all. Nothing will say which of them rewrote a
// statement. The host is choosing that knowingly, and it is not the default.
func (i *Inclusion) AllResources() *Inclusion {
	i.cmd.declIncludes[i.at].allResources = true
	return i
}

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
