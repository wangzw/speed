package config

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/vislake/speed/pkg/core"
)

// panicsWith runs fn and returns what it panicked with. A declaration that is
// wrong in itself is caught where it is written, so these cases read the
// message rather than an error value.
func panicsWith(t *testing.T, what string, fn func()) string {
	t.Helper()
	panicked := false
	message := ""
	func() {
		defer func() {
			if raised := recover(); raised != nil {
				panicked = true
				message = fmt.Sprint(raised)
			}
		}()
		fn()
	}()
	if !panicked {
		t.Fatalf("%s returned, want a panic", what)
	}
	return message
}

// declaredTree is a root written by hand rather than by NewApp, so that the
// command paths a diagnostic renders do not carry the test binary's name.
func declaredTree() *Command { return &Command{use: "app"} }

func TestSubBuildsThePathTheCommandLineReads(t *testing.T) {
	root := declaredTree()
	deep := root.Sub("cluster").Sub("member").Sub("add")
	if got := deep.path(); got != "app cluster member add" {
		t.Fatalf("the command renders as %q, want the words a run writes to reach it", got)
	}
	if got := root.path(); got != "app" {
		t.Fatalf("the root renders as %q", got)
	}
}

// TestSubRejectsANameThatSelectsNothing pins the names that leave a command no
// run can reach: one that is empty, one carrying a space, one beginning with a
// dash, and one a sibling already answers to.
func TestSubRejectsANameThatSelectsNothing(t *testing.T) {
	t.Run("empty", func(t *testing.T) {
		root := declaredTree()
		message := panicsWith(t, `Sub("")`, func() { root.Sub("") })
		if !strings.Contains(message, "app") {
			t.Fatalf("the message reads %q, which does not name the command it is about", message)
		}
	})
	t.Run("carries a space", func(t *testing.T) {
		root := declaredTree()
		message := panicsWith(t, `Sub("member add")`, func() { root.Sub("member add") })
		if !strings.Contains(message, "member add") {
			t.Fatalf("the message reads %q, which does not quote the name", message)
		}
	})
	t.Run("begins with a dash", func(t *testing.T) {
		// The library reads a word beginning with a dash as an argument
		// and never searches the commands for it, so the command this
		// would declare has no command line that reaches it. Left to
		// stand it is worse than unreachable: a leaf must also declare
		// Do, so the host writes an action that is never run and hears
		// nothing about it.
		root := declaredTree()
		message := panicsWith(t, `Sub("-x")`, func() { root.Sub("-x") })
		if !strings.Contains(message, "-x") {
			t.Fatalf("the message reads %q, which does not quote the name", message)
		}
	})
	t.Run("already taken by a sibling", func(t *testing.T) {
		root := declaredTree()
		root.Sub("serve")
		message := panicsWith(t, `a second Sub("serve")`, func() { root.Sub("serve") })
		if !strings.Contains(message, "serve") {
			t.Fatalf("the message reads %q, which does not quote the name", message)
		}
	})
}

// TestIncludeTakesAModuleInOnce pins that naming a module twice is not naming
// two of them: the drive is handed one list, and a repeated name in it would
// be a module asked for a stance twice.
func TestIncludeTakesAModuleInOnce(t *testing.T) {
	root := declaredTree()
	root.Include("cache", "cache")
	root.Include("cache", "logging")
	if !slices.Equal(root.include, []string{"cache", "logging"}) {
		t.Fatalf("the level takes in %v, want each module once and in declaration order", root.include)
	}
}

func TestIncludeRejectsAnEmptyName(t *testing.T) {
	root := declaredTree()
	message := panicsWith(t, `Include("")`, func() { root.Include("") })
	if !strings.Contains(message, "app") {
		t.Fatalf("the message reads %q, which does not name the command it is about", message)
	}
}

// TestDoTwiceIsAProgrammingError pins the one depth and one action a command
// has. A second call would leave two answers to how far the run drives, and
// only the writer of the tree can say which was meant.
func TestDoTwiceIsAProgrammingError(t *testing.T) {
	serve := declaredTree().Sub("serve")
	serve.Do(core.StageServe, nil)
	message := panicsWith(t, "a second Do", func() {
		serve.Do(core.StagePrepare, func(context.Context, *core.Registry) error { return nil })
	})
	if !strings.Contains(message, "app serve") {
		t.Fatalf("the message reads %q, which does not name the command", message)
	}
}

// TestDoRejectsADepthThatIsNotAStage pins the check this one call can make on
// its own: the type admits any small number, and the stages are six of them.
func TestDoRejectsADepthThatIsNotAStage(t *testing.T) {
	serve := declaredTree().Sub("serve")
	message := panicsWith(t, "Do past the deepest stage", func() {
		serve.Do(core.StageServe+1, nil)
	})
	if !strings.Contains(message, "app serve") {
		t.Fatalf("the message reads %q, which does not name the command", message)
	}
}

// TestDoAtTheZeroDepthIsADeclaration pins that the shallowest stage is a legal
// answer rather than a missing one. A command that only asks the modules for a
// stance drives to it, and nothing about the value says whether Do was called.
func TestDoAtTheZeroDepthIsADeclaration(t *testing.T) {
	serve := declaredTree().Sub("serve")
	if serve.hasDo {
		t.Fatal("a command that has not declared Do reports that it has")
	}
	serve.Do(core.StagePrepare, nil)
	if !serve.hasDo {
		t.Fatal("Do at the zero depth left the command indistinguishable from one without it")
	}
}
