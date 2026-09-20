package config

import (
	"context"
	"errors"
	"io"
	"slices"
	"strings"
	"testing"

	"github.com/vislake/speed/pkg/core"
)

// readerFrom takes the run's configuration up the way a module takes it up:
// it is a supplied product, so it answers a lookup for the capability and
// needs no declaration.
func readerFrom(t *testing.T, reg *core.Registry) Reader {
	t.Helper()
	found, err := reg.Resolve((*Reader)(nil))
	if err != nil {
		t.Fatalf("the reader was not supplied to the registry: %v", err)
	}
	return found.(Reader)
}

// decodeAddr reads one fixture module's input item back.
func decodeAddr(t *testing.T, r Reader, module string) string {
	t.Helper()
	var got treeAddr
	if err := r.Decode(module, &got); err != nil {
		t.Fatalf("decoding %q failed: %v", module, err)
	}
	return got.Addr
}

// TestExecuteDrivesTheSelectedCommand pins the whole of what a run comes to:
// the command line picks the command, the command's participating set is who
// takes part, its depth is how far they are carried, and its action is what
// happens on arrival.
func TestExecuteDrivesTheSelectedCommand(t *testing.T) {
	run := &treeRun{}
	a, _, _ := treeApp(treeRegistry(t, run, nil), "greet")
	acted := false
	greet := a.Root().Sub("greet")
	greet.Include("greeter")
	greet.Do(core.StageNew, func(_ context.Context, reg *core.Registry) error {
		acted = true
		if got := decodeAddr(t, readerFrom(t, reg), "greeter"); got != "greeter:default" {
			t.Errorf("greeter.addr reads %q, want the declared default", got)
		}
		return nil
	})

	if err := a.Execute(t.Context()); err != nil {
		t.Fatalf("the run failed: %v", err)
	}
	if !acted {
		t.Fatal("the drive reached its depth without running the command's action")
	}
	want := []string{"greeter", "logging"}
	if got := run.at("prepare"); !slices.Equal(got, want) {
		t.Fatalf("%v were asked for a stance, want the command's participating set %v", got, want)
	}
	if got := run.at("new"); !slices.Equal(got, want) {
		t.Fatalf("%v were constructed, want %v", got, want)
	}
}

// TestExecuteStopsAtTheDeclaredDepth pins that the depth is the command's and
// not the lifecycle's: a command that only collects the stances never reaches
// construction, and the modules outside its participating set are not asked
// for a stance at all.
func TestExecuteStopsAtTheDeclaredDepth(t *testing.T) {
	run := &treeRun{}
	a, _, _ := treeApp(treeRegistry(t, run, nil), "check")
	check := a.Root().Sub("check")
	check.Include("cache")
	check.Do(core.StagePrepare, nil)

	if err := a.Execute(t.Context()); err != nil {
		t.Fatalf("the run failed: %v", err)
	}
	if got := run.at("prepare"); !slices.Equal(got, []string{"cache"}) {
		t.Fatalf("%v were asked for a stance, want the one module the command takes in", got)
	}
	if got := run.at("new"); len(got) != 0 {
		t.Fatalf("%v were constructed by a command that drives no further than the stances", got)
	}
}

// TestCommandTakingNothingInDrivesWithNoModule pins the end of the range a
// participating set covers. A command that assembles nothing still drives:
// no module states anything, and the empty set is not read as every module.
func TestCommandTakingNothingInDrivesWithNoModule(t *testing.T) {
	run := &treeRun{}
	a, _, _ := treeApp(treeRegistry(t, run, nil), "version")
	acted := false
	version := a.Root().Sub("version")
	version.Do(core.StagePrepare, func(context.Context, *core.Registry) error {
		acted = true
		return nil
	})

	if err := a.Execute(t.Context()); err != nil {
		t.Fatalf("the run failed: %v", err)
	}
	if !acted {
		t.Fatal("a command assembling nothing did not run its action")
	}
	if len(run.steps) != 0 {
		t.Fatalf("%v took part in a command that takes no module in", run.steps)
	}
}

// TestLocatorArgumentIsAcceptedOnEitherSideOfTheSubcommand pins the reserved
// argument as the root's own: it holds for every command, so the run reaches
// the same primary source whether it is written before the subcommand or
// after it.
func TestLocatorArgumentIsAcceptedOnEitherSideOfTheSubcommand(t *testing.T) {
	content := map[string]any{"cache": map[string]any{"addr": "from-file:1"}}
	for _, args := range [][]string{
		{"--config=file:///app.yaml", "serve"},
		{"serve", "--config=file:///app.yaml"},
	} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			a, _, _ := treeApp(treeRegistry(t, &treeRun{}, content), args...)
			serve := a.Root().Sub("serve")
			serve.Include("cache")
			serve.Do(core.StagePrepare, func(_ context.Context, reg *core.Registry) error {
				if got := decodeAddr(t, readerFrom(t, reg), "cache"); got != "from-file:1" {
					t.Errorf("cache.addr reads %q, want the file the argument pointed at", got)
				}
				return nil
			})
			if err := a.Execute(t.Context()); err != nil {
				t.Fatalf("the run failed: %v", err)
			}
		})
	}
}

// TestPrimarySourceCarriesKeysOfModulesOutsideTheCommand pins the first of the
// two directions the unknown-key check runs in, and the one that is easiest to
// get backwards. One primary source is shared by every command, so judging it
// by a command's participating set would make it wrong under every command.
func TestPrimarySourceCarriesKeysOfModulesOutsideTheCommand(t *testing.T) {
	content := map[string]any{
		"cache":   map[string]any{"addr": "from-file:1"},
		"logging": map[string]any{"addr": "from-file:2"},
	}
	a, _, _ := treeApp(treeRegistry(t, &treeRun{}, content), "--config=file:///app.yaml", "serve")
	serve := a.Root().Sub("serve")
	serve.Include("cache")
	serve.Do(core.StagePrepare, func(_ context.Context, reg *core.Registry) error {
		if got := decodeAddr(t, readerFrom(t, reg), "cache"); got != "from-file:1" {
			t.Errorf("cache.addr reads %q, want the value the file gave", got)
		}
		return nil
	})

	if err := a.Execute(t.Context()); err != nil {
		t.Fatalf("a file carrying a key of a module this command does not take in failed the "+
			"run: %v", err)
	}
}

// TestPrimarySourceKeyNoModuleDeclaresIsRejected pins the other half of that
// direction: judging by the whole manifest is not judging by nothing. A key no
// module anywhere declares is a defect under every command.
func TestPrimarySourceKeyNoModuleDeclaresIsRejected(t *testing.T) {
	content := map[string]any{"cache": map[string]any{"adrr": "typo"}}
	a, _, _ := treeApp(treeRegistry(t, &treeRun{}, content), "--config=file:///app.yaml", "version")
	a.Root().Sub("version").Do(core.StagePrepare, nil)

	if err := a.Execute(t.Context()); !errors.Is(err, ErrUnknownKey) {
		t.Fatalf("a key no module declares returned %v, want ErrUnknownKey", err)
	}
}

// TestArgumentOfAModuleOutsideTheCommandIsRejected pins the second direction:
// the command line is judged by the participating set. The argument exists in
// the manifest and this command never registered it, and the fix is a
// different command rather than a different spelling.
func TestArgumentOfAModuleOutsideTheCommandIsRejected(t *testing.T) {
	a, _, _ := treeApp(treeRegistry(t, &treeRun{}, nil), "version", "--cache-addr=x")
	serve := a.Root().Sub("serve")
	serve.Include("cache")
	serve.Do(core.StagePrepare, nil)
	a.Root().Sub("version").Do(core.StagePrepare, nil)

	err := a.Execute(t.Context())
	if !errors.Is(err, ErrUnknownKey) {
		t.Fatalf("an argument of a module outside the command returned %v, want ErrUnknownKey", err)
	}
	if !strings.Contains(err.Error(), "does not take that module in") {
		t.Fatalf("the rejection reads %q, which does not say the module is outside this "+
			"command", err)
	}
}

// TestSameArgumentIsAcceptedOnTheCommandThatTakesTheModuleIn is the case that
// makes the one above say something: the same word on the sibling command
// reaches the item.
func TestSameArgumentIsAcceptedOnTheCommandThatTakesTheModuleIn(t *testing.T) {
	a, _, _ := treeApp(treeRegistry(t, &treeRun{}, nil), "serve", "--cache-addr=given")
	serve := a.Root().Sub("serve")
	serve.Include("cache")
	serve.Do(core.StagePrepare, func(_ context.Context, reg *core.Registry) error {
		if got := decodeAddr(t, readerFrom(t, reg), "cache"); got != "given" {
			t.Errorf("cache.addr reads %q, want the command line's value", got)
		}
		return nil
	})
	a.Root().Sub("version").Do(core.StagePrepare, nil)

	if err := a.Execute(t.Context()); err != nil {
		t.Fatalf("the run failed: %v", err)
	}
}

// TestBothSiblingsThatTakeOneModuleInCarryItsArguments pins that a command's
// arguments are its own participating set's, and another command's
// registration does not stand in for them. Two commands neither of which is
// under the other can both take one module in, and a run selects either; an
// argument defined on whichever of them is reached first would be unknown on
// the other, which takes the module in just as much.
func TestBothSiblingsThatTakeOneModuleInCarryItsArguments(t *testing.T) {
	a, _, _ := treeApp(treeRegistry(t, &treeRun{}, nil), "serve", "--cache-addr=given")
	// check is reached before serve, whatever order the two are declared
	// in: the library holds its commands by name.
	check := a.Root().Sub("check")
	check.Include("cache")
	check.Do(core.StagePrepare, nil)
	serve := a.Root().Sub("serve")
	serve.Include("cache")
	serve.Do(core.StagePrepare, func(_ context.Context, reg *core.Registry) error {
		if got := decodeAddr(t, readerFrom(t, reg), "cache"); got != "given" {
			t.Errorf("cache.addr reads %q, want the command line's value", got)
		}
		return nil
	})

	if err := a.Execute(t.Context()); err != nil {
		t.Fatalf("the run failed: %v", err)
	}
}

// TestArgumentOfAModuleTakenInTwiceReachesTheReader pins what registering on
// the shallowest level comes to from outside. The value is written after the
// subcommand, and the argument it lands in is the root's; a second definition
// on the subcommand would shadow the one the layering tool is bound to and the
// value would be read as the default.
func TestArgumentOfAModuleTakenInTwiceReachesTheReader(t *testing.T) {
	a, _, _ := treeApp(treeRegistry(t, &treeRun{}, nil), "serve", "--cache-addr=given")
	a.Root().Include("cache")
	serve := a.Root().Sub("serve")
	serve.Include("cache")
	serve.Do(core.StagePrepare, func(_ context.Context, reg *core.Registry) error {
		if got := decodeAddr(t, readerFrom(t, reg), "cache"); got != "given" {
			t.Errorf("cache.addr reads %q, want the command line's value", got)
		}
		return nil
	})

	if err := a.Execute(t.Context()); err != nil {
		t.Fatalf("the run failed: %v", err)
	}
}

// TestUnknownArgumentIsAnUnknownKey pins that a name nothing declares is
// classified by this module rather than left as the command-line library's
// own error: the three ways an argument can fail to reach an item lead to
// three different fixes, and only the manifest can tell them apart.
func TestUnknownArgumentIsAnUnknownKey(t *testing.T) {
	a, _, _ := treeApp(treeRegistry(t, &treeRun{}, nil), "serve", "--nonesuch=x")
	serve := a.Root().Sub("serve")
	serve.Include("cache")
	serve.Do(core.StagePrepare, nil)

	err := a.Execute(t.Context())
	if !errors.Is(err, ErrUnknownKey) {
		t.Fatalf("an argument no item declares returned %v, want ErrUnknownKey", err)
	}
	if !strings.Contains(err.Error(), "--nonesuch") {
		t.Fatalf("the rejection reads %q, which does not quote the argument", err)
	}
}

// TestUnknownSubcommandIsNotAConfigurationFailure pins the boundary of what
// this module classifies. A word that is no command of the tree is the
// command-line library's finding and stays its own: putting it under a
// sentinel of this module's would claim a failure that is not about
// configuration.
func TestUnknownSubcommandIsNotAConfigurationFailure(t *testing.T) {
	a, _, _ := treeApp(treeRegistry(t, &treeRun{}, nil), "nonesuch")
	a.Root().Sub("serve").Do(core.StagePrepare, nil)

	err := a.Execute(t.Context())
	if err == nil {
		t.Fatal("a word that is no command of the tree ran to success")
	}
	for _, sentinel := range []error{ErrUnknownKey, ErrMalformedCommandLine} {
		if errors.Is(err, sentinel) {
			t.Fatalf("an unknown command was classified as %v", sentinel)
		}
	}
	if !strings.Contains(err.Error(), "nonesuch") {
		t.Fatalf("the failure reads %q, which does not quote the word", err)
	}
}

// TestAskingForHelpIsNotAFailure pins that help is the library's to answer and
// produces no special return value of this module's: no command runs and the
// result is nil.
func TestAskingForHelpIsNotAFailure(t *testing.T) {
	a, stdout, _ := treeApp(treeRegistry(t, &treeRun{}, nil), "--help")
	serve := a.Root().Sub("serve")
	serve.Include("cache")
	serve.Do(core.StagePrepare, func(context.Context, *core.Registry) error {
		t.Error("the command ran although the run only asked for help")
		return nil
	})

	if err := a.Execute(t.Context()); err != nil {
		t.Fatalf("asking for help returned %v", err)
	}
	if !strings.Contains(stdout.String(), "serve") {
		t.Fatalf("the help reads %q, which does not list the commands that can follow", stdout)
	}
}

// TestHelpForACommandListsItsOwnArguments pins that the two ways of asking
// agree. Both are answered after the command path is located, so both list the
// arguments of the command's participating set rather than the root's.
func TestHelpForACommandListsItsOwnArguments(t *testing.T) {
	for _, args := range [][]string{{"help", "serve"}, {"serve", "--help"}} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			a, stdout, _ := treeApp(treeRegistry(t, &treeRun{}, nil), args...)
			serve := a.Root().Sub("serve")
			serve.Include("cache")
			serve.Do(core.StagePrepare, nil)
			version := a.Root().Sub("version")
			version.Include("logging")
			version.Do(core.StagePrepare, nil)

			if err := a.Execute(t.Context()); err != nil {
				t.Fatalf("asking for help returned %v", err)
			}
			help := stdout.String()
			if !strings.Contains(help, "--cache-addr") {
				t.Fatalf("the help reads %q, which omits an argument of the command's own "+
					"participating set", help)
			}
			if strings.Contains(help, "--logging-addr") {
				t.Fatalf("the help reads %q, which carries an argument only a sibling "+
					"command takes in", help)
			}
		})
	}
}

// TestFailureBeforeTheLibraryTakesOverIsReported pins the one report this
// module writes itself. Assembling the tree happens before the library has
// anything to print with, so a failure there would otherwise reach the host as
// a value nobody had shown anybody.
func TestFailureBeforeTheLibraryTakesOverIsReported(t *testing.T) {
	a, _, stderr := treeApp(treeRegistry(t, &treeRun{}, nil), "serve")
	a.Root().Sub("serve").Include("cache")

	err := a.Execute(t.Context())
	if err == nil {
		t.Fatal("a command with no Do ran to success")
	}
	if !strings.Contains(stderr.String(), err.Error()) {
		t.Fatalf("the diagnostics read %q, and the failure %v was never shown to anybody",
			stderr, err)
	}
}

// TestExecuteTwiceIsAProgrammingError pins the one run a tree has. The
// registry behind it is driven once, and catching the second call here names
// the tree rather than the registry.
func TestExecuteTwiceIsAProgrammingError(t *testing.T) {
	a, _, _ := treeApp(treeRegistry(t, &treeRun{}, nil), "version")
	a.Root().Sub("version").Do(core.StagePrepare, nil)
	if err := a.Execute(t.Context()); err != nil {
		t.Fatalf("the first run failed: %v", err)
	}
	panicsWith(t, "a second Execute", func() { _ = a.Execute(t.Context()) })
}

func TestNewAppWithoutARegistryIsAProgrammingError(t *testing.T) {
	panicsWith(t, "NewApp(nil)", func() { NewApp(nil) })
}

// TestBooleanOnAMiddleLayerDoesNotHideTheSubcommand pins that a boolean
// argument introduced by a middle command does not swallow the subcommand
// name that follows it.
//
// Locating the selected command reads the command line, and an argument
// written without a value is told apart from one whose value follows it only
// by its registration. A layer registered after the search would have its
// boolean take the next word -- the name of its own subcommand -- as a value,
// and the command found would be the parent. What surfaced then was a
// diagnostic saying the deeper command does not take in a module it does take
// in, because that command's layer was never registered.
func TestBooleanOnAMiddleLayerDoesNotHideTheSubcommand(t *testing.T) {
	for _, order := range [][]string{
		{"db", "migrate", "--dry-run", "--verbose"},
		{"db", "--verbose", "migrate", "--dry-run"},
		{"db", "--verbose", "migrate"},
	} {
		t.Run(strings.Join(order, " "), func(t *testing.T) {
			var dbOpts struct {
				Verbose bool `config:"verbose"`
			}
			var migOpts struct {
				DryRun bool `config:"dry-run"`
			}
			reg := core.New()
			reg.Register(identifying("host", HostIdentity{Prefix: "TL"}))
			reg.Register(declaring("dbmod", Schema{
				Namespace: "dbmod",
				Mounts:    []Mount{{Value: &dbOpts}},
				Items:     map[string]Item{"verbose": {Origins: OriginFlag, FlagName: "verbose"}},
			}))
			reg.Register(declaring("migmod", Schema{
				Namespace: "migmod",
				Mounts:    []Mount{{Value: &migOpts}},
				Items:     map[string]Item{"dry-run": {Origins: OriginFlag, FlagName: "dry-run"}},
			}))

			reached := false
			app := NewApp(reg)
			db := app.Root().Sub("db")
			db.Include("dbmod")
			mig := db.Sub("migrate")
			mig.Include("migmod")
			mig.Do(core.StagePrepare, func(context.Context, *core.Registry) error {
				reached = true
				return nil
			})
			app.withArgs(order...)
			app.withStreams(io.Discard, io.Discard)
			if err := app.Execute(t.Context()); err != nil {
				t.Fatalf("%v failed: %v", order, err)
			}
			if !reached {
				t.Fatalf("%v did not reach the deepest command", order)
			}
		})
	}
}
