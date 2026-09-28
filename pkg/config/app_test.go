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

// TestADeclarationLevelModuleIsReachedButNotRun pins both halves of what the
// second level does.
//
// The module is within reach of a collection -- the action asks the registry
// and it answers -- and it is not run: it states no stance and is never
// constructed. Those are two different questions about one module, and a
// command whose work is applying migrations needs them answered differently:
// it wants the tables of modules it must not build.
func TestADeclarationLevelModuleIsReachedButNotRun(t *testing.T) {
	run := &treeRun{}
	a, _, _ := treeApp(treeRegistry(t, run, nil), "migrate")
	declaring := false
	migrate := a.Root().Sub("migrate")
	migrate.Include("cache")
	migrate.IncludeDeclarations("greeter")
	migrate.Do(core.StageMigrate, func(_ context.Context, reg *core.Registry) error {
		declaring = reg.Declaring("greeter")
		return nil
	})

	if err := a.Execute(t.Context()); err != nil {
		t.Fatalf("the run failed: %v", err)
	}
	if !declaring {
		t.Error("the declaring set did not reach a module this command took in at that level, " +
			"so a command of this shape collects nothing")
	}
	if got := run.at("prepare"); !slices.Equal(got, []string{"cache"}) {
		t.Errorf("%v were asked for a stance, want only the module the command runs", got)
	}
	if got := run.at("new"); !slices.Equal(got, []string{"cache"}) {
		t.Errorf("%v were constructed, want only the module the command runs", got)
	}
}

// TestADeclarationLevelModulePutsNoArgumentOnTheCommandLine pins the half that
// makes the level usable.
//
// A command whose work is applying migrations runs the engine and collects the
// tables of modules it has no use for running. If taking those in put their
// arguments on the command line, the command would be made to answer for a
// listener address it never opens -- which is why taking them in at run level
// was never a workable answer in the first place.
func TestADeclarationLevelModulePutsNoArgumentOnTheCommandLine(t *testing.T) {
	a, stdout, _ := treeApp(treeRegistry(t, &treeRun{}, nil), "help", "migrate")
	migrate := a.Root().Sub("migrate")
	migrate.Include("cache")
	migrate.IncludeDeclarations("greeter")
	migrate.Do(core.StageMigrate, nil)

	if err := a.Execute(t.Context()); err != nil {
		t.Fatalf("asking for help returned %v", err)
	}
	help := stdout.String()
	if !strings.Contains(help, "--cache-addr") {
		t.Errorf("the help reads %q, which omits an argument of a module the command runs", help)
	}
	if strings.Contains(help, "--greeter-addr") {
		t.Errorf("the help reads %q, which carries an argument of a module the command only "+
			"collects declarations from", help)
	}
}

// TestWithFlagsPutsADeclarationLevelModulesArgumentOnTheCommandLine pins the
// adjustment that reverses the half above.
//
// It does not make the module run and it does not make its required items be
// checked: a module that is never constructed never decodes its configuration,
// so the argument is one a run may give rather than one it must.
func TestWithFlagsPutsADeclarationLevelModulesArgumentOnTheCommandLine(t *testing.T) {
	a, stdout, _ := treeApp(treeRegistry(t, &treeRun{}, nil), "help", "migrate")
	migrate := a.Root().Sub("migrate")
	migrate.Include("cache")
	migrate.IncludeDeclarations("greeter").WithFlags()
	migrate.Do(core.StageMigrate, nil)

	if err := a.Execute(t.Context()); err != nil {
		t.Fatalf("asking for help returned %v", err)
	}
	if help := stdout.String(); !strings.Contains(help, "--greeter-addr") {
		t.Errorf("the help reads %q, which omits the argument a host asked to be reachable", help)
	}
}

// TestAReportingCommandCollectsEveryArgumentWithoutRunningAnything pins the
// one shape the flag adjustment exists for.
//
// A command that reports or checks the configuration has to offer every
// module's arguments and construct none of them: it reads the values itself
// rather than asking each module to, so nothing should be built and no
// required item should be enforced. Both halves are observed here, and the
// first is the load-bearing one -- take the arguments away and there is no
// way for the operator to give the command the value it is being asked to
// report on.
func TestAReportingCommandCollectsEveryArgumentWithoutRunningAnything(t *testing.T) {
	run := &treeRun{}
	a, _, _ := treeApp(treeRegistry(t, run, nil), "check", "--greeter-addr=:1")
	read := ""
	check := a.Root().Sub("check")
	check.IncludeAllDeclarations().WithFlags()
	check.Do(core.StagePrepare, func(_ context.Context, reg *core.Registry) error {
		read = decodeAddr(t, readerFrom(t, reg), "greeter")
		return nil
	})

	if err := a.Execute(t.Context()); err != nil {
		t.Fatalf("a command collecting every module's arguments reported %v", err)
	}
	if read != ":1" {
		t.Errorf("the command read greeter.addr as %q, want the value given on its own command "+
			"line: an argument it cannot accept is an argument the operator cannot give it", read)
	}
	if len(run.steps) != 0 {
		t.Errorf("%v ran under a command whose whole work is reading configuration", run.steps)
	}
}

// TestAnArgumentOfADeclarationLevelModuleSaysWhereItBelongs pins the diagnosis
// for an argument that is absent because the module behind it is only
// collected from.
//
// The module is taken in, so telling a host this command "does not take that
// module in" is false of it, and the fix that sentence suggests -- add it to
// the command's Include -- constructs a module the command was written not to
// build. The adjustment that would accept the argument is the thing to name,
// and this is the only place a host can be told about it: the argument is
// registered nowhere, so the library knows only that it is unknown.
func TestAnArgumentOfADeclarationLevelModuleSaysWhereItBelongs(t *testing.T) {
	a, _, _ := treeApp(treeRegistry(t, &treeRun{}, nil), "migrate", "--greeter-addr=:1")
	migrate := a.Root().Sub("migrate")
	migrate.Include("cache")
	migrate.IncludeDeclarations("greeter")
	migrate.Do(core.StageMigrate, nil)

	err := a.Execute(t.Context())
	if !errors.Is(err, ErrUnknownKey) {
		t.Fatalf("running with an argument this command does not carry returned %v, want %v",
			err, ErrUnknownKey)
	}
	if !strings.Contains(err.Error(), "WithFlags") {
		t.Errorf("the failure reads %q, which does not name the adjustment that would accept "+
			"the argument", err)
	}
	if strings.Contains(err.Error(), "does not take that module in") {
		t.Errorf("the failure reads %q, and this command does take that module in", err)
	}
}

// TestIncludeAllDeclarationsReachesEveryRegisteredModule pins the form that
// does not need a list kept in step.
//
// A command collecting declarations would otherwise name every module with
// tables, and a name left off that list is a table that never gets created --
// silently, since being left out is not among the things that reach the
// startup diagnostics.
func TestIncludeAllDeclarationsReachesEveryRegisteredModule(t *testing.T) {
	a, _, _ := treeApp(treeRegistry(t, &treeRun{}, nil), "version")
	var missed []string
	version := a.Root().Sub("version")
	version.IncludeAllDeclarations()
	version.Do(core.StagePrepare, func(_ context.Context, reg *core.Registry) error {
		for _, m := range reg.Modules() {
			if !reg.Declaring(m.Name) {
				missed = append(missed, m.Name)
			}
		}
		return nil
	})

	if err := a.Execute(t.Context()); err != nil {
		t.Fatalf("the run failed: %v", err)
	}
	if len(missed) != 0 {
		t.Errorf("%v are registered and were not reached by taking every declaration in",
			missed)
	}
}

// TestAModuleTakenInAtBothLevelsIsAParticipant pins which level wins.
//
// The two lists are two things the host wrote, and there is only one reading
// of a module named in both: it runs. Reporting it as declaring alone would
// leave a module that is constructed and serving out of the set that describes
// what this command runs.
func TestAModuleTakenInAtBothLevelsIsAParticipant(t *testing.T) {
	run := &treeRun{}
	a, _, _ := treeApp(treeRegistry(t, run, nil), "migrate")
	participating := false
	migrate := a.Root().Sub("migrate")
	migrate.Include("cache")
	migrate.IncludeDeclarations("cache")
	migrate.Do(core.StageMigrate, func(_ context.Context, reg *core.Registry) error {
		participating = reg.Participating("cache")
		return nil
	})

	if err := a.Execute(t.Context()); err != nil {
		t.Fatalf("the run failed: %v", err)
	}
	if got := run.at("new"); !slices.Equal(got, []string{"cache"}) {
		t.Fatalf("%v were constructed, want the module both levels name", got)
	}
	if !participating {
		t.Error("a module named at both levels was not counted as taking part")
	}
}

// TestADeclarationNameNoModuleRegisteredIsRefused pins that the second level
// is checked the way the first is.
//
// A misspelt name at this level leaves declarations out with nothing said, and
// the command goes on to report success: the failure is a table that was never
// created, met later as a query against it.
func TestADeclarationNameNoModuleRegisteredIsRefused(t *testing.T) {
	a, _, _ := treeApp(treeRegistry(t, &treeRun{}, nil), "migrate")
	migrate := a.Root().Sub("migrate")
	migrate.IncludeDeclarations("misspelt")
	migrate.Do(core.StageMigrate, nil)

	err := a.Execute(t.Context())
	if !errors.Is(err, core.ErrUnknownParticipant) {
		t.Fatalf("a declaration-level name that matches nothing returned %v, want %v",
			err, core.ErrUnknownParticipant)
	}
	if !strings.Contains(err.Error(), "migrate") {
		t.Errorf("the failure reads %q, which does not say which command wrote the name", err)
	}
}

// TestADeclarationLevelInclusionHoldsForEveryCommandUnderIt pins that the
// second level is inherited the way the first is.
//
// What a level takes in holds for every command under it. The two levels are
// no different there, so a host writes the modules its program collects
// declarations from once, where they belong, rather than repeating them under
// each command that needs them.
func TestADeclarationLevelInclusionHoldsForEveryCommandUnderIt(t *testing.T) {
	a, _, _ := treeApp(treeRegistry(t, &treeRun{}, nil), "version")
	declaring := false
	a.Root().IncludeDeclarations("greeter")
	version := a.Root().Sub("version")
	version.Do(core.StagePrepare, func(_ context.Context, reg *core.Registry) error {
		declaring = reg.Declaring("greeter")
		return nil
	})

	if err := a.Execute(t.Context()); err != nil {
		t.Fatalf("the run failed: %v", err)
	}
	if !declaring {
		t.Error("a declaration-level inclusion written on the root did not hold for a command " +
			"under it")
	}
}

// TestAllResourcesReachesTheDrive pins that what a host sets on an inclusion
// survives the trip from the command tree to the registry.
//
// They sit far apart: the adjustment is set where the tree is written, before
// anything is parsed, and it is read by whichever module asks the registry.
// A step that dropped it would leave the call looking applied and doing
// nothing -- which, for an override that installs a module's plugins onto a
// handle it does not run on, is the difference between a host's deliberate
// choice and a silent no-op.
func TestAllResourcesReachesTheDrive(t *testing.T) {
	a, _, _ := treeApp(treeRegistry(t, &treeRun{}, nil), "version")
	inForce := false
	version := a.Root().Sub("version")
	version.IncludeDeclarations("greeter").AllResources()
	version.Do(core.StagePrepare, func(_ context.Context, reg *core.Registry) error {
		inForce = reg.AllResourcesInForce("greeter")
		return nil
	})

	if err := a.Execute(t.Context()); err != nil {
		t.Fatalf("the run failed: %v", err)
	}
	if !inForce {
		t.Error("an adjustment set where the tree is written did not reach the registry")
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

// TestTheDiagnosisOfAnUnknownArgumentIsAboutTheSelectedCommand pins that the
// fix an unknown argument is met with is read off the command the run
// selected. A sibling that reaches the module for its declarations alone says
// nothing about a command that does not take it in at all, and pointing that
// command at WithFlags would send the host to change an inclusion it never
// wrote.
func TestTheDiagnosisOfAnUnknownArgumentIsAboutTheSelectedCommand(t *testing.T) {
	a, _, _ := treeApp(treeRegistry(t, &treeRun{}, nil), "version", "--greeter-addr=:1")
	check := a.Root().Sub("check")
	check.IncludeAllDeclarations().WithFlags()
	check.Do(core.StagePrepare, nil)
	version := a.Root().Sub("version")
	version.Do(core.StagePrepare, nil)

	err := a.Execute(t.Context())
	if !errors.Is(err, ErrUnknownKey) {
		t.Fatalf("an argument the selected command does not carry returned %v, want %v",
			err, ErrUnknownKey)
	}
	if strings.Contains(err.Error(), "WithFlags") || strings.Contains(err.Error(), "declarations alone") {
		t.Errorf("the failure reads %q, and version does not take the module in at any level", err)
	}
	if !strings.Contains(err.Error(), "does not take that module in") {
		t.Errorf("the failure reads %q, which does not say the module is outside this command", err)
	}
}
