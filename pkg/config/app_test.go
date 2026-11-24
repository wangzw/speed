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

func readerFrom(t *testing.T, reg *core.Registry) Reader {
	t.Helper()
	found, err := reg.Resolve((*Reader)(nil))
	if err != nil {
		t.Fatalf("the reader was not supplied to the registry: %v", err)
	}
	return found.(Reader)
}

func decodeAddr(t *testing.T, r Reader, module string) string {
	t.Helper()
	var got treeAddr
	if err := r.Decode(module, &got); err != nil {
		t.Fatalf("decoding %q failed: %v", module, err)
	}
	return got.Addr
}

func executesWithoutPanicking(t *testing.T, a *App) error {
	t.Helper()
	defer func() {
		if raised := recover(); raised != nil {
			t.Fatalf("Execute panicked: %v", raised)
		}
	}()
	return a.Execute(t.Context())
}

// TestExecuteDrivesTheSelectedCommand checks that execute drives the selected command.
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

// TestExecuteStopsAtTheDeclaredDepth checks that execute stops at the declared depth.
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

// TestCommandTakingNothingInDrivesWithNoModule checks that command taking nothing in drives with no module.
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

// TestADeclarationLevelModuleIsReachedButNotRun checks that a declaration level module is reached but not run.
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

// TestADeclarationLevelModulePutsNoArgumentOnTheCommandLine checks that a declaration level module puts no argument on the command line.
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

// TestWithFlagsPutsADeclarationLevelModulesArgumentOnTheCommandLine checks that with flags puts a declaration level modules argument on the command line.
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

// TestAReportingCommandCollectsEveryArgumentWithoutRunningAnything checks that a reporting command collects every argument without running anything.
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

// TestAnArgumentOfADeclarationLevelModuleSaysWhereItBelongs checks that an argument of a declaration level module says where it belongs.
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

// TestIncludeAllDeclarationsReachesEveryRegisteredModule checks that include all declarations reaches every registered module.
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

// TestAModuleTakenInAtBothLevelsIsAParticipant checks that a module taken in at both levels is a participant.
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

// TestADeclarationNameNoModuleRegisteredIsRefused checks that a declaration name no module registered is refused.
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

// TestADeclarationLevelInclusionHoldsForEveryCommandUnderIt checks that a declaration level inclusion holds for every command under it.
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

// TestAllResourcesReachesTheDrive checks that all resources reaches the drive.
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

// TestLocatorArgumentIsAcceptedOnEitherSideOfTheSubcommand checks that locator argument is accepted on either side of the subcommand.
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

// TestPrimarySourceCarriesKeysOfModulesOutsideTheCommand checks that primary source carries keys of modules outside the command.
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

// TestPrimarySourceKeyNoModuleDeclaresIsRejected checks that primary source key no module declares is rejected.
func TestPrimarySourceKeyNoModuleDeclaresIsRejected(t *testing.T) {
	content := map[string]any{"cache": map[string]any{"adrr": "typo"}}
	a, _, _ := treeApp(treeRegistry(t, &treeRun{}, content), "--config=file:///app.yaml", "version")
	a.Root().Sub("version").Do(core.StagePrepare, nil)

	if err := a.Execute(t.Context()); !errors.Is(err, ErrUnknownKey) {
		t.Fatalf("a key no module declares returned %v, want ErrUnknownKey", err)
	}
}

// TestArgumentOfAModuleOutsideTheCommandIsRejected checks that argument of a module outside the command is rejected.
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

// TestSameArgumentIsAcceptedOnTheCommandThatTakesTheModuleIn checks that same argument is accepted on the command that takes the module in.
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

// TestBothSiblingsThatTakeOneModuleInCarryItsArguments checks that both siblings that take one module in carry its arguments.
func TestBothSiblingsThatTakeOneModuleInCarryItsArguments(t *testing.T) {
	a, _, _ := treeApp(treeRegistry(t, &treeRun{}, nil), "serve", "--cache-addr=given")
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

// TestArgumentOfAModuleTakenInTwiceReachesTheReader checks that argument of a module taken in twice reaches the reader.
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

// TestUnknownArgumentIsAnUnknownKey checks that unknown argument is an unknown key.
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

// TestUnknownSubcommandIsNotAConfigurationFailure checks that unknown subcommand is not a configuration failure.
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

// TestAskingForHelpIsNotAFailure checks that asking for help is not a failure.
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

// TestHelpForACommandListsItsOwnArguments checks that help for a command lists its own arguments.
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

// TestFailureBeforeTheLibraryTakesOverIsReported checks that failure before the library takes over is reported.
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

// TestExecuteTwiceIsAProgrammingError checks that execute twice is a programming error.
func TestExecuteTwiceIsAProgrammingError(t *testing.T) {
	a, _, _ := treeApp(treeRegistry(t, &treeRun{}, nil), "version")
	a.Root().Sub("version").Do(core.StagePrepare, nil)
	if err := a.Execute(t.Context()); err != nil {
		t.Fatalf("the first run failed: %v", err)
	}
	panicsWith(t, "a second Execute", func() { _ = a.Execute(t.Context()) })
}

// TestNewAppWithoutARegistryIsAProgrammingError checks that new app without a registry is a programming error.
func TestNewAppWithoutARegistryIsAProgrammingError(t *testing.T) {
	panicsWith(t, "NewApp(nil)", func() { NewApp(nil) })
}

// TestNewAppWithoutTheConfigModuleIsAProgrammingError checks that new app without the config module is a programming error.
func TestNewAppWithoutTheConfigModuleIsAProgrammingError(t *testing.T) {
	standIn := core.New()
	standIn.Register(core.Module{
		Name:     "mirror",
		Provides: []core.Provision{{Token: (*Reader)(nil)}},
	})
	for _, c := range []struct {
		name string
		reg  *core.Registry
	}{
		{"an empty registry", core.New()},
		{"a registry whose only reader provider is another module", standIn},
	} {
		t.Run(c.name, func(t *testing.T) {
			message := panicsWith(t, "NewApp", func() { NewApp(c.reg) })
			if !strings.Contains(message, "reg.Register(config.Module())") {
				t.Errorf("the panic reads %q, which does not say how to register the module", message)
			}
		})
	}

	cured := core.New()
	cured.Register(Module())
	NewApp(cured)
}

// TestExecuteSuppliesTheReaderToTheConfigModuleByName checks that execute supplies the reader to the config module by name.
func TestExecuteSuppliesTheReaderToTheConfigModuleByName(t *testing.T) {
	reg := treeRegistry(t, &treeRun{}, nil)
	reg.Register(core.Module{
		Name:     "mirror",
		Provides: []core.Provision{{Token: (*Reader)(nil)}},
	})
	a, _, _ := treeApp(reg, "serve")
	acted := false
	serve := a.Root().Sub("serve")
	serve.Include("cache")
	serve.Do(core.StagePrepare, func(_ context.Context, reg *core.Registry) error {
		acted = true
		if !reg.Participating(ModuleName) {
			t.Errorf("the reader was supplied and module %q does not take part", ModuleName)
		}
		if reg.Participating("mirror") {
			t.Error("module \"mirror\" takes part, so the reader was supplied to the module that " +
				"merely declares the same capability")
		}
		if got := decodeAddr(t, readerFrom(t, reg), "cache"); got != "cache:default" {
			t.Errorf("cache.addr reads %q through the supplied reader, want the declared default", got)
		}
		return nil
	})

	if err := executesWithoutPanicking(t, a); err != nil {
		t.Fatalf("the run failed: %v", err)
	}
	if !acted {
		t.Fatal("the drive reached its depth without running the command's action")
	}
}

// TestExecuteSuppliesTheReaderOnlyAfterTheRunHasLoaded checks that execute supplies the reader only after the run has loaded.
func TestExecuteSuppliesTheReaderOnlyAfterTheRunHasLoaded(t *testing.T) {
	holdingAReader := func(t *testing.T) (*core.Registry, Reader) {
		t.Helper()
		reg := treeRegistry(t, &treeRun{}, nil)
		reader, err := Load(t.Context(), reg, nil)
		if err != nil {
			t.Fatalf("loading the host's own reader failed: %v", err)
		}
		reg.Supply(reader, ModuleName)
		return reg, reader
	}
	versionApp := func(reg *core.Registry, args ...string) *App {
		a, _, _ := treeApp(reg, args...)
		a.Root().Sub("version").Do(core.StagePrepare, nil)
		return a
	}

	t.Run("a command line the library rejects comes back as that error", func(t *testing.T) {
		reg, _ := holdingAReader(t)
		a := versionApp(reg, "version", "--no-such-argument")
		if err := executesWithoutPanicking(t, a); !errors.Is(err, ErrUnknownKey) {
			t.Fatalf("an argument no item declares returned %v, want %v", err, ErrUnknownKey)
		}
	})

	t.Run("a locator no transport takes comes back as that error", func(t *testing.T) {
		reg, _ := holdingAReader(t)
		a := versionApp(reg, "version", "--config=nosuchscheme://x")
		if err := executesWithoutPanicking(t, a); !errors.Is(err, ErrUnknownScheme) {
			t.Fatalf("a locator whose scheme no transport takes returned %v, want %v",
				err, ErrUnknownScheme)
		}
	})

	t.Run("a module that already has an instance refuses the reader", func(t *testing.T) {
		reg, hosts := holdingAReader(t)
		a := versionApp(reg, "version")
		panicsWith(t, "Execute", func() { _ = a.Execute(t.Context()) })

		found, err := reg.Resolve((*Reader)(nil))
		if err != nil || found != hosts {
			t.Errorf("after the refusal the registry hands out %v (%v), want the reader the host supplied",
				found, err)
		}
	})

	t.Run("a registry that was driven already refuses the reader", func(t *testing.T) {
		reg := treeRegistry(t, &treeRun{}, nil)
		drive := core.Drive{Depth: core.StagePrepare, Participants: []string{}}
		if err := reg.Drive(t.Context(), drive); err != nil {
			t.Fatalf("driving the registry ahead of Execute failed: %v", err)
		}
		a := versionApp(reg, "version")
		message := panicsWith(t, "Execute", func() { _ = a.Execute(t.Context()) })
		if !strings.Contains(strings.ToLower(message), "drive") {
			t.Errorf("the panic reads %q, which does not say the drive had begun", message)
		}
		if _, err := reg.Resolve((*Reader)(nil)); !errors.Is(err, core.ErrMissingProvider) {
			t.Errorf("looking the reader up after the refusal returned %v, want %v: nothing was supplied",
				err, core.ErrMissingProvider)
		}
	})

	t.Run("a module of that name the reader does not satisfy refuses it", func(t *testing.T) {
		reg := core.New()
		reg.Register(identifying("host", HostIdentity{Prefix: "MYAPP"}))
		reg.Register(core.Module{
			Name:     ModuleName,
			Provides: []core.Provision{{Token: (*treeSink)(nil)}},
		})
		a := versionApp(reg, "version")
		panicsWith(t, "Execute", func() { _ = a.Execute(t.Context()) })
	})
}

// TestTheConfigModuleTakesPartInEveryDrive checks that the config module takes part in every drive.
func TestTheConfigModuleTakesPartInEveryDrive(t *testing.T) {
	a, _, _ := treeApp(treeRegistry(t, &treeRun{}, nil), "version")
	acted := false
	a.Root().Sub("version").Do(core.StagePrepare, func(_ context.Context, reg *core.Registry) error {
		acted = true
		if !reg.Participating(ModuleName) {
			t.Errorf("module %q does not take part in a drive of a command that takes nothing in",
				ModuleName)
		}
		enablement, known := reg.Enablement(ModuleName)
		if !known || enablement.State != core.StateEnabled {
			t.Errorf("the verdict on module %q is %+v (known: %v), want StateEnabled: its stance is "+
				"fixed, so resolution cannot stand it down", ModuleName, enablement, known)
		}
		return nil
	})

	if err := a.Execute(t.Context()); err != nil {
		t.Fatalf("the run failed: %v", err)
	}
	if !acted {
		t.Fatal("the drive reached its depth without running the command's action")
	}
}

// TestTakingTheConfigModuleInIsHarmless checks that taking the config module in is harmless.
func TestTakingTheConfigModuleInIsHarmless(t *testing.T) {
	for _, c := range []struct {
		name  string
		write func(*Command)
	}{
		{"by name", func(c *Command) { c.Include(ModuleName) }},
		{"with every module", func(c *Command) { c.IncludeAll() }},
	} {
		t.Run(c.name, func(t *testing.T) {
			a, _, _ := treeApp(treeRegistry(t, &treeRun{}, nil), "serve")
			acted := false
			serve := a.Root().Sub("serve")
			c.write(serve)
			serve.Do(core.StagePrepare, func(_ context.Context, reg *core.Registry) error {
				acted = true
				if !reg.Participating(ModuleName) {
					t.Errorf("module %q does not take part", ModuleName)
				}
				return nil
			})

			if err := a.Execute(t.Context()); err != nil {
				t.Fatalf("the run failed: %v", err)
			}
			if !acted {
				t.Fatal("the drive reached its depth without running the command's action")
			}
		})
	}
}

func suppliedOnlyApp(t *testing.T, args ...string) (*App, *strings.Builder) {
	t.Helper()
	reg := core.New()
	reg.Register(Module())
	reg.Register(identifying("host", HostIdentity{Prefix: "MYAPP"}))
	reg.Register(declaring("greeter", Schema{
		Namespace: "greeter",
		Mounts:    []Mount{{Value: &treeAddr{}}},
		Items: map[string]Item{"addr": {
			Origins:     OriginPrimary | OriginEnv | OriginFlag,
			FlagName:    "greeter-addr",
			Placeholder: "HOST:PORT",
			Description: "the address of the greeting service",
			Required:    true,
		}},
	}))
	reg.Supply(&treeProduct{name: "greeter"}, "greeter")
	a, stdout, _ := treeApp(reg, args...)
	return a, stdout
}

// TestAModuleTakingPartOnlyBySupplyIsOutsideTheConfigurationInterface checks that a module taking part only by supply is outside the configuration interface.
func TestAModuleTakingPartOnlyBySupplyIsOutsideTheConfigurationInterface(t *testing.T) {
	t.Run("help does not list its argument", func(t *testing.T) {
		a, stdout := suppliedOnlyApp(t, "--help")
		a.Root().Sub("version").Do(core.StagePrepare, nil)
		if err := a.Execute(t.Context()); err != nil {
			t.Fatalf("asking for help returned %v", err)
		}
		if help := stdout.String(); strings.Contains(help, "--greeter-addr") {
			t.Errorf("the help reads %q, which lists the argument of a module no command takes in", help)
		}
	})

	t.Run("the command line has no argument for it", func(t *testing.T) {
		a, _ := suppliedOnlyApp(t, "version", "--greeter-addr=:1")
		a.Root().Sub("version").Do(core.StagePrepare, nil)
		err := a.Execute(t.Context())
		if !errors.Is(err, ErrUnknownKey) {
			t.Fatalf("an argument of a module no command takes in returned %v, want %v", err, ErrUnknownKey)
		}
		if !strings.Contains(err.Error(), "does not take that module in") {
			t.Errorf("the rejection reads %q, which does not say the module is outside this command", err)
		}
	})

	t.Run("its required item is not judged", func(t *testing.T) {
		a, _ := suppliedOnlyApp(t, "version")
		reached := false
		var decoded error
		a.Root().Sub("version").Do(core.StagePrepare, func(_ context.Context, reg *core.Registry) error {
			reached = true
			if !reg.Participating("greeter") {
				t.Error("the module whose instance was supplied does not take part")
			}
			var got treeAddr
			decoded = readerFrom(t, reg).Decode("greeter", &got)
			return nil
		})
		if err := a.Execute(t.Context()); err != nil {
			t.Fatalf("the run failed: %v", err)
		}
		if !reached {
			t.Fatal("the drive reached its depth without running the command's action")
		}
		if decoded != nil {
			t.Errorf("decoding the module's path returned %v, and a module that takes part only by "+
				"supply has no required item judged", decoded)
		}
	})
}

// TestASuppliedModuleTakenInForItsDeclarationsIsStaticToTheTree checks that a supplied module taken in for its declarations is static to the tree.
func TestASuppliedModuleTakenInForItsDeclarationsIsStaticToTheTree(t *testing.T) {
	declared := func(c *Command) { c.IncludeDeclarations("greeter").WithFlags().AllResources() }
	for _, c := range []struct {
		name        string
		write       func(*Command)
		args        []string
		wantAddr    string
		wantMissing bool
	}{
		{
			name:     "declaration level, the argument given",
			write:    declared,
			args:     []string{"version", "--greeter-addr=:1"},
			wantAddr: ":1",
		},
		{
			name:  "declaration level, the argument left out",
			write: declared,
			args:  []string{"version"},
		},
		{
			name:        "run level, the argument left out",
			write:       func(c *Command) { c.Include("greeter") },
			args:        []string{"version"},
			wantMissing: true,
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			a, _ := suppliedOnlyApp(t, c.args...)
			version := a.Root().Sub("version")
			c.write(version)
			reached := false
			version.Do(core.StagePrepare, func(_ context.Context, reg *core.Registry) error {
				reached = true
				if !reg.Participating("greeter") {
					t.Error("the module whose instance was supplied does not take part")
				}
				if reg.AllResourcesInForce("greeter") {
					t.Error("AllResources is in force for a module that takes part: the registry " +
						"handles it as a participant, and the adjustment lapses")
				}

				var got treeAddr
				err := readerFrom(t, reg).Decode("greeter", &got)
				if c.wantMissing {
					if !errors.Is(err, ErrMissingRequired) {
						t.Errorf("decoding the path of a module the tree runs returned %v, want %v",
							err, ErrMissingRequired)
					}
					return nil
				}
				if err != nil {
					t.Errorf("decoding the path of a module the tree reaches for its declarations "+
						"alone returned %v, and its required item is not judged", err)
				}
				if got.Addr != c.wantAddr {
					t.Errorf("greeter.addr reads %q, want %q", got.Addr, c.wantAddr)
				}
				return nil
			})

			if err := a.Execute(t.Context()); err != nil {
				t.Fatalf("the run failed: %v", err)
			}
			if !reached {
				t.Fatal("the drive reached its depth without running the command's action")
			}
		})
	}
}

// TestAModuleReadsItsValuesInPrepareAndInNew checks that a module reads its values in prepare and in new.
func TestAModuleReadsItsValuesInPrepareAndInNew(t *testing.T) {
	var inPrepare, inNew string
	read := func(reg *core.Registry) string {
		r, err := core.Resolve[Reader](reg)
		if err != nil {
			t.Errorf("the reader could not be taken up: %v", err)
			return ""
		}
		return decodeAddr(t, r, "reading")
	}
	reading := treeModule(&treeRun{}, "reading", core.Module{})
	reading.Prepare = func(_ context.Context, reg *core.Registry) (core.Enablement, error) {
		inPrepare = read(reg)
		return core.Enablement{State: core.StateEnabled}, nil
	}
	reading.New = func(_ context.Context, reg *core.Registry) (any, error) {
		inNew = read(reg)
		return treeProduct{name: "reading"}, nil
	}
	reg := treeRegistry(t, &treeRun{}, nil)
	reg.Register(reading)

	a, _, _ := treeApp(reg, "serve", "--reading-addr=given")
	serve := a.Root().Sub("serve")
	serve.Include("reading")
	serve.Do(core.StageNew, nil)

	if err := a.Execute(t.Context()); err != nil {
		t.Fatalf("the run failed: %v", err)
	}
	if inPrepare != "given" {
		t.Errorf("Prepare read reading.addr as %q, want the command line's value", inPrepare)
	}
	if inNew != "given" {
		t.Errorf("New read reading.addr as %q, want the command line's value", inNew)
	}
}

type treeLedger interface{ record() }

type treeLedgerBook struct{ entries int }

func (b *treeLedgerBook) record() { b.entries++ }

// TestParticipatingTellsWhetherTheDriveBegan checks that participating tells whether the drive began.
func TestParticipatingTellsWhetherTheDriveBegan(t *testing.T) {
	for _, c := range []struct {
		name  string
		args  []string
		began bool
	}{
		{"help is answered and no command runs", []string{"--help"}, false},
		{"a command runs", []string{"version"}, true},
	} {
		t.Run(c.name, func(t *testing.T) {
			closes := 0
			reg := treeRegistry(t, &treeRun{}, nil)
			reg.Register(core.Module{
				Name:     "ledger",
				Provides: []core.Provision{{Token: (*treeLedger)(nil)}},
				Close: func(context.Context, *core.Registry, any) error {
					closes++
					return nil
				},
			})
			reg.Supply(&treeLedgerBook{}, "ledger")

			a, _, _ := treeApp(reg, c.args...)
			a.Root().Sub("version").Do(core.StagePrepare, nil)
			if err := a.Execute(t.Context()); err != nil {
				t.Fatalf("the run failed: %v", err)
			}

			for _, name := range []string{ModuleName, "ledger"} {
				if got := reg.Participating(name); got != c.began {
					t.Errorf("Participating(%q) is %v, want %v", name, got, c.began)
				}
			}
			wantCloses := 0
			if c.began {
				wantCloses = 1
			}
			if closes != wantCloses {
				t.Errorf("the supplied instance was closed %d times, want %d", closes, wantCloses)
			}
			if !c.began {
				if _, err := reg.Resolve((*Reader)(nil)); !errors.Is(err, core.ErrMissingProvider) {
					t.Errorf("the reader was supplied (%v) although no drive began: nothing in the "+
						"registry is the driver's to release before then", err)
				}
			}
		})
	}
}

// TestBooleanOnAMiddleLayerDoesNotHideTheSubcommand checks that boolean on a middle layer does not hide the subcommand.
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
			reg.Register(Module())
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

func requiredArgumentApp(t *testing.T, write func(*App), args ...string) (string, error) {
	t.Helper()
	defaults := &treeAddr{}
	reg := core.New()
	reg.Register(Module())
	reg.Register(identifying("host", HostIdentity{Prefix: "MYAPP"}))
	reg.Register(declaring("greeter", Schema{
		Namespace: "greeter",
		Mounts:    []Mount{{Value: defaults}},
		Items: map[string]Item{"addr": {
			Origins:     OriginPrimary | OriginEnv | OriginFlag,
			FlagName:    "greeter-addr",
			Placeholder: "HOST:PORT",
			Description: "the address of the greeting service",
			Required:    true,
		}},
	}))
	a, stdout, _ := treeApp(reg, args...)
	write(a)
	err := a.Execute(t.Context())
	return stdout.String(), err
}

func greeterLine(t *testing.T, help string) string {
	t.Helper()
	for line := range strings.SplitSeq(help, "\n") {
		if strings.Contains(line, "--greeter-addr") {
			return strings.TrimSpace(line)
		}
	}
	t.Fatalf("the help reads\n%s\nand carries no --greeter-addr line", help)
	return ""
}

// TestTheRequiredMarkFollowsTheCommandWhoseHelpIsShown checks that the required mark follows the command whose help is shown.
func TestTheRequiredMarkFollowsTheCommandWhoseHelpIsShown(t *testing.T) {
	for _, reporter := range []string{"check", "zcheck"} {
		tree := func(a *App) {
			report := a.Root().Sub(reporter)
			report.IncludeAllDeclarations().WithFlags()
			report.Do(core.StagePrepare, nil)
			serve := a.Root().Sub("serve")
			serve.Include("greeter")
			serve.Do(core.StagePrepare, nil)
		}
		t.Run(reporter+" beside serve", func(t *testing.T) {
			help, err := requiredArgumentApp(t, tree, "serve", "--help")
			if err != nil {
				t.Fatalf("asking serve for help returned %v", err)
			}
			if line := greeterLine(t, help); !strings.Contains(line, "(required)") {
				t.Errorf("serve's help reads %q, and serve runs the module that will not start "+
					"without this argument", line)
			}
			help, err = requiredArgumentApp(t, tree, reporter, "--help")
			if err != nil {
				t.Fatalf("asking %s for help returned %v", reporter, err)
			}
			if line := greeterLine(t, help); strings.Contains(line, "(required)") {
				t.Errorf("%s's help reads %q, and %s runs nothing that would ask for it",
					reporter, line, reporter)
			}
		})
	}
}

// TestTheRequiredMarkReachesACommandThatRunsWhatItsParentOnlyCollects checks that the required mark reaches a command that runs what its parent only collects.
func TestTheRequiredMarkReachesACommandThatRunsWhatItsParentOnlyCollects(t *testing.T) {
	tree := func(a *App) {
		tool := a.Root().Sub("tool")
		tool.IncludeDeclarations("greeter").WithFlags()
		run := tool.Sub("run")
		run.Include("greeter")
		run.Do(core.StagePrepare, nil)
	}
	help, err := requiredArgumentApp(t, tree, "tool", "run", "--help")
	if err != nil {
		t.Fatalf("asking tool run for help returned %v", err)
	}
	if line := greeterLine(t, help); !strings.Contains(line, "(required)") {
		t.Errorf("tool run's help reads %q, and tool run runs the module that will not start "+
			"without this argument", line)
	}
	help, err = requiredArgumentApp(t, tree, "tool", "--help")
	if err != nil {
		t.Fatalf("asking tool for help returned %v", err)
	}
	if line := greeterLine(t, help); strings.Contains(line, "(required)") {
		t.Errorf("tool's help reads %q, and tool runs nothing that would ask for it", line)
	}
}

// TestAnArgumentGivenOnARunningChildSatisfiesTheRequiredItem checks that an argument given on a running child satisfies the required item.
func TestAnArgumentGivenOnARunningChildSatisfiesTheRequiredItem(t *testing.T) {
	for _, args := range [][]string{
		{"tool", "run", "--greeter-addr=:1"},
		{"tool", "--greeter-addr=:1", "run"},
	} {
		read := ""
		tree := func(a *App) {
			tool := a.Root().Sub("tool")
			tool.IncludeDeclarations("greeter").WithFlags()
			run := tool.Sub("run")
			run.Include("greeter")
			run.Do(core.StagePrepare, func(_ context.Context, reg *core.Registry) error {
				read = decodeAddr(t, readerFrom(t, reg), "greeter")
				return nil
			})
		}
		if _, err := requiredArgumentApp(t, tree, args...); err != nil {
			t.Fatalf("%v returned %v", args, err)
		}
		if read != ":1" {
			t.Errorf("%v read greeter.addr as %q, want the value it was given", args, read)
		}
	}
}

// TestAReportingActionReadsACollectedModuleWithoutItsRequiredItems checks that a reporting action reads a collected module without its required items.
func TestAReportingActionReadsACollectedModuleWithoutItsRequiredItems(t *testing.T) {
	decodeIn := func(command string, includeAtRunLevel bool) error {
		var decoded error
		tree := func(a *App) {
			c := a.Root().Sub(command)
			if includeAtRunLevel {
				c.Include("greeter")
			} else {
				c.IncludeAllDeclarations().WithFlags()
			}
			c.Do(core.StagePrepare, func(_ context.Context, reg *core.Registry) error {
				var got treeAddr
				decoded = readerFrom(t, reg).Decode("greeter", &got)
				return nil
			})
		}
		if _, err := requiredArgumentApp(t, tree, command); err != nil {
			t.Fatalf("running %s returned %v", command, err)
		}
		return decoded
	}

	if err := decodeIn("check", false); err != nil {
		t.Errorf("a command that only collects the module decoded its path with %v, and the "+
			"module's required items are never judged there", err)
	}
	if err := decodeIn("serve", true); !errors.Is(err, ErrMissingRequired) {
		t.Errorf("a command that runs the module decoded its path with %v, want %v",
			err, ErrMissingRequired)
	}
}

// TestTheDiagnosisOfAnUnknownArgumentIsAboutTheSelectedCommand checks that the diagnosis of an unknown argument is about the selected command.
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
