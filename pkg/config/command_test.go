package config

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/vislake/speed/pkg/core"
)

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

func declaredTree() *Command { return &Command{use: "app"} }

// TestSubBuildsThePathTheCommandLineReads checks that sub builds the path the command line reads.
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

// TestSubRejectsANameThatSelectsNothing checks that sub rejects a name that selects nothing.
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

// TestIncludeTakesAModuleInOnce checks that include takes a module in once.
func TestIncludeTakesAModuleInOnce(t *testing.T) {
	root := declaredTree()
	root.Include("cache", "cache")
	root.Include("cache", "logging")
	if !slices.Equal(root.include, []string{"cache", "logging"}) {
		t.Fatalf("the level takes in %v, want each module once and in declaration order", root.include)
	}
}

// TestIncludeRejectsAnEmptyName checks that include rejects an empty name.
func TestIncludeRejectsAnEmptyName(t *testing.T) {
	root := declaredTree()
	message := panicsWith(t, `Include("")`, func() { root.Include("") })
	if !strings.Contains(message, "app") {
		t.Fatalf("the message reads %q, which does not name the command it is about", message)
	}
}

// TestDoTwiceIsAProgrammingError checks that do twice is a programming error.
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

// TestDoRejectsADepthThatIsNotAStage checks that do rejects a depth that is not a stage.
func TestDoRejectsADepthThatIsNotAStage(t *testing.T) {
	serve := declaredTree().Sub("serve")
	message := panicsWith(t, "Do past the deepest stage", func() {
		serve.Do(core.StageServe+1, nil)
	})
	if !strings.Contains(message, "app serve") {
		t.Fatalf("the message reads %q, which does not name the command", message)
	}
}

// TestDoAtTheZeroDepthIsADeclaration checks that do at the zero depth is a declaration.
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
