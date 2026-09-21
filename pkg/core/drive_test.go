package core_test

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/vislake/speed/pkg/core"
)

// preparing builds a recording module whose stance is recorded too, so a
// drive that stops at the stance stage still leaves a trace.
func preparing(p *probe, name string) core.Module {
	m := p.module(name)
	m.Prepare = func(context.Context, *core.Registry) (core.Enablement, error) {
		p.record("prepare %s", name)
		return core.Enablement{}, nil
	}
	return m
}

// noAction is an action that does nothing, which is how a drive is kept from
// blocking at StageServe while something else is being pinned.
func noAction(context.Context, *core.Registry) error { return nil }

func TestStageStringNamesTheCallbackItDrives(t *testing.T) {
	for stage, want := range map[core.Stage]string{
		core.StagePrepare: "Prepare",
		core.StageNew:     "New",
		core.StageMigrate: "Migrate",
		core.StageInit:    "Init",
		core.StageStart:   "Start",
		core.StageServe:   "Serve",
	} {
		if got := stage.String(); got != want {
			t.Errorf("stage %d renders as %q, want %q: a stage failure reads as the field "+
				"the module wrote", uint8(stage), got, want)
		}
	}
}

// TestDepthStopsEveryCallbackBeyondIt pins the whole of what a depth means:
// the stages up to it run as they always do, the ones past it do not run at
// all, and whatever was constructed on the way is released in reverse order
// regardless.
func TestDepthStopsEveryCallbackBeyondIt(t *testing.T) {
	cases := []struct {
		depth core.Stage
		want  []string
	}{
		{core.StagePrepare, []string{"prepare only"}},
		{core.StageNew, []string{"prepare only", "new only", "stop only", "close only"}},
		{core.StageMigrate, []string{
			"prepare only", "new only", "migrate only", "stop only", "close only",
		}},
		{core.StageInit, []string{
			"prepare only", "new only", "migrate only", "init only", "stop only", "close only",
		}},
		{core.StageStart, []string{
			"prepare only", "new only", "migrate only", "init only", "start only",
			"stop only", "close only",
		}},
		{core.StageServe, []string{
			"prepare only", "new only", "migrate only", "init only", "start only", "serve only",
			"stop only", "close only",
		}},
	}
	for _, c := range cases {
		t.Run(c.depth.String(), func(t *testing.T) {
			p := &probe{}
			reg := core.New()
			register(reg, preparing(p, "only"))

			err := driveToEnd(t, reg, core.Drive{
				Depth:        c.depth,
				Participants: []string{"only"},
				Action:       noAction,
			})
			if err != nil {
				t.Fatalf("Drive returned %v", err)
			}
			if got := p.snapshot(); !slices.Equal(got, c.want) {
				t.Fatalf("a drive to %s ran %v, want %v", c.depth, got, c.want)
			}
		})
	}
}

// TestDepthPrepareSkipsDependencyResolution pins that resolution is the
// preliminary to construction rather than a step of its own. A command that
// only collects stances builds nothing, so it must not fail over a provider
// it was never going to build.
func TestDepthPrepareSkipsDependencyResolution(t *testing.T) {
	p := &probe{}
	reg := core.New()
	consumer := preparing(p, "consumer")
	consumer.Requires = []core.Requirement{{Token: (*Cache)(nil)}}
	register(reg, consumer)

	err := driveToEnd(t, reg, core.Drive{
		Depth:        core.StagePrepare,
		Participants: []string{"consumer"},
	})
	if err != nil {
		t.Fatalf("a stance-only drive returned %v, want nil: it constructs nothing, so an "+
			"unmet construction-time requirement has nothing to prevent", err)
	}
	if !p.has("prepare consumer") {
		t.Fatalf("the stance stage did not run; recorded %v", p.snapshot())
	}
}

// TestActionRunsAtTheDepthAndReachesTheRegistry pins where the action sits:
// after the last stage the depth includes, before the shutdown, with every
// instance that stage left behind still reachable.
func TestActionRunsAtTheDepthAndReachesTheRegistry(t *testing.T) {
	p := &probe{}
	reg := core.New()
	provider := preparing(p, "provider")
	provider.Provides = []core.Provision{{Token: (*Cache)(nil)}}
	register(reg, provider)

	err := driveToEnd(t, reg, core.Drive{
		Depth:        core.StageInit,
		Participants: []string{"provider"},
		Action: func(_ context.Context, r *core.Registry) error {
			cache, err := core.Resolve[Cache](r)
			if err != nil {
				return err
			}
			p.record("action saw %s", cache.CacheID())
			return nil
		},
	})
	if err != nil {
		t.Fatalf("Drive returned %v", err)
	}
	if !p.has("action saw provider") {
		t.Fatalf("the action did not reach the registry; recorded %v", p.snapshot())
	}
	if p.indexOf(t, "init provider") > p.indexOf(t, "action saw provider") {
		t.Fatalf("the action ran before the depth was reached: %v", p.snapshot())
	}
	if p.indexOf(t, "action saw provider") > p.indexOf(t, "stop provider") {
		t.Fatalf("the action ran after the shutdown began: %v", p.snapshot())
	}
	if p.has("start provider") {
		t.Fatalf("a stage past the depth ran: %v", p.snapshot())
	}
}

// TestActionFailureIsPrimaryAndStillRollsBack pins that the release of what a
// drive built does not depend on the action succeeding, and that the action's
// own failure is what comes back rather than something the cleanup added.
func TestActionFailureIsPrimaryAndStillRollsBack(t *testing.T) {
	p := &probe{}
	reg := core.New()
	register(reg, preparing(p, "only"))
	failure := errors.New("the command could not do its work")

	err := driveToEnd(t, reg, core.Drive{
		Depth:        core.StageInit,
		Participants: []string{"only"},
		Action: func(context.Context, *core.Registry) error {
			p.record("action only")
			return failure
		},
	})
	if !errors.Is(err, failure) {
		t.Fatalf("Drive returned %v, want the action's own failure", err)
	}
	if !strings.Contains(err.Error(), "action") {
		t.Errorf("error %q does not read as an action failure rather than a stage failure", err)
	}
	for _, want := range []string{"stop only", "close only"} {
		if !p.has(want) {
			t.Errorf("a failed action skipped %q; recorded %v", want, p.snapshot())
		}
	}
}

// TestShallowDriveWithoutAnActionDoesNotBlock pins that arriving is the whole
// of a shallow command's work. Blocking there, as serving does, would hang a
// command whose end point is to exit.
func TestShallowDriveWithoutAnActionDoesNotBlock(t *testing.T) {
	p := &probe{}
	reg := core.New()
	register(reg, preparing(p, "only"))

	err := driveToEnd(t, reg, core.Drive{
		Depth:        core.StageStart,
		Participants: []string{"only"},
	})
	if err != nil {
		t.Fatalf("Drive returned %v", err)
	}
	if !p.has("close only") {
		t.Fatalf("the drive did not shut down; recorded %v", p.snapshot())
	}
}

// TestServeWithoutAnActionBlocksUntilCancelled pins the other side of the
// same rule: at the deepest stage a missing action is the shape of serving
// traffic, and cancellation is the normal way out of it.
func TestServeWithoutAnActionBlocksUntilCancelled(t *testing.T) {
	p := &probe{}
	reg := core.New()
	register(reg, preparing(p, "only"))

	err := driveUntilServed(t, reg, p, core.Drive{
		Depth:        core.StageServe,
		Participants: []string{"only"},
	}, "serve only")
	if err != nil {
		t.Fatalf("cancelling a serving drive returned %v, want nil", err)
	}
}

func TestServeWithAnActionDoesNotBlock(t *testing.T) {
	p := &probe{}
	reg := core.New()
	register(reg, preparing(p, "only"))

	err := driveToEnd(t, reg, core.Drive{
		Depth:        core.StageServe,
		Participants: []string{"only"},
		Action: func(context.Context, *core.Registry) error {
			p.record("action only")
			return nil
		},
	})
	if err != nil {
		t.Fatalf("Drive returned %v", err)
	}
	if p.indexOf(t, "serve only") > p.indexOf(t, "action only") {
		t.Fatalf("the action ran before the deepest stage: %v", p.snapshot())
	}
}

// TestZeroDriveDoesNothing pins the zero value of the whole struct: no depth
// past the stances, nobody taking part, nothing to do on arrival.
func TestZeroDriveDoesNothing(t *testing.T) {
	p := &probe{}
	reg := core.New()
	register(reg, preparing(p, "registered"))

	err := driveToEnd(t, reg, core.Drive{})
	if err != nil {
		t.Fatalf("the zero Drive returned %v, want nil", err)
	}
	if got := p.snapshot(); len(got) != 0 {
		t.Fatalf("the zero Drive ran %v, want nothing", got)
	}
}

// TestEmptyParticipantSetIncludesNothing pins the most dangerous reading a
// zero value could be given. A command that assembles nothing is an ordinary
// command; reading its empty list as "everything" would turn it into the one
// that assembles the most, and it would do so silently.
//
// The registry holds modules the drive did not list, so a correct
// implementation and one that inverted the empty list are told apart here.
func TestEmptyParticipantSetIncludesNothing(t *testing.T) {
	p := &probe{}
	reg := core.New()
	register(reg, preparing(p, "alpha"), preparing(p, "bravo"))

	err := driveToEnd(t, reg, core.Drive{
		Depth:        core.StageServe,
		Participants: nil,
		Action:       noAction,
	})
	if err != nil {
		t.Fatalf("a drive with no participants returned %v, want nil", err)
	}
	if got := p.snapshot(); len(got) != 0 {
		t.Fatalf("a drive with no participants ran %v, want nothing: the list is literal", got)
	}
	if len(reg.Modules()) != 2 {
		t.Fatal("the registry lost its registrations, so the test could not tell an empty " +
			"participating set from an empty registry")
	}
}

// TestModulesOutsideTheParticipantSetTakeNoPart pins that the enabled set is a
// subset of the participating one: a registered module the command left out
// states nothing, is constructed never, and reports the exclusion as its
// verdict so that a mechanism collecting declarations across the registry
// drops it.
func TestModulesOutsideTheParticipantSetTakeNoPart(t *testing.T) {
	p := &probe{}
	reg := core.New()
	register(reg, preparing(p, "included"), preparing(p, "left-out"))

	err := driveToEnd(t, reg, core.Drive{
		Depth:        core.StageServe,
		Participants: []string{"included"},
		Action:       noAction,
	})
	if err != nil {
		t.Fatalf("Drive returned %v", err)
	}
	for _, event := range p.snapshot() {
		if strings.HasSuffix(event, " left-out") {
			t.Fatalf("a module outside the participating set took part: %v", p.snapshot())
		}
	}
	if !p.has("prepare included") {
		t.Fatalf("the listed module did not take part; recorded %v", p.snapshot())
	}
	got, ok := reg.Enablement("left-out")
	if !ok {
		t.Fatal("Enablement gave no verdict for a registered module this command left out")
	}
	if got.State != core.StateDisabled {
		t.Errorf("Enablement reported %+v for a module that is not running, want disabled", got)
	}
	if !strings.Contains(got.Reason, "participating set") {
		t.Errorf("the verdict reads %q, which does not tell being left out of this command "+
			"apart from having switched itself off", got.Reason)
	}
	if !reg.Participating("included") {
		t.Error("Participating denied a module this command listed")
	}
	if reg.Participating("left-out") {
		t.Error("Participating counted a module this command left out, which is the whole " +
			"thing a mechanism filtering by participation has to be able to ask")
	}
	if !reg.Declaring("included") {
		t.Error("Declaring denied a participant, so the declaring set does not contain the " +
			"participating one")
	}
	if reg.Declaring("left-out") {
		t.Error("Declaring counted a registered module this command took in at neither level")
	}
}

// TestADeclarationLevelModuleContributesDeclarationsAndNothingElse pins what
// the second level of inclusion reaches and what it leaves alone.
//
// The module is in the declaring set, so a collection reading that set finds
// it. It is not a participant, so it states no stance and is not constructed.
// The two queries have to answer differently for that to be expressible at
// all: one verdict reported the same thing either way, and a command whose
// work is applying migrations had no way to collect the tables of modules it
// must not build.
func TestADeclarationLevelModuleContributesDeclarationsAndNothingElse(t *testing.T) {
	p := &probe{}
	constructed := false
	reg := core.New()
	register(reg, preparing(p, "runs"))
	register(reg, core.Module{
		Name:      "declares",
		Resources: []any{"a declaration"},
		New: func(context.Context, *core.Registry) (any, error) {
			constructed = true
			return nil, nil
		},
	})

	err := driveToEnd(t, reg, core.Drive{
		Depth:         core.StageServe,
		Participants:  []string{"runs"},
		DeclaringOnly: []core.Declaration{{Name: "declares"}},
		Action:        noAction,
	})
	if err != nil {
		t.Fatalf("Drive returned %v", err)
	}
	if !reg.Declaring("declares") {
		t.Error("Declaring denied a module this drive reached for its declarations")
	}
	if reg.Participating("declares") {
		t.Error("Participating counted a module this drive took in at declaration level only")
	}
	if constructed {
		t.Error("a module reached for its declarations alone was constructed, so the level " +
			"does not spare it the configuration a command of this shape has no use for")
	}
	if !p.has("prepare runs") {
		t.Errorf("the participating module did not run; recorded %v", p.snapshot())
	}
}

// TestANameNoModuleRegisteredIsRefusedAtEitherLevel pins that the second level
// is checked the way the first is.
//
// A declaration-level name that matches nothing leaves declarations out with
// nobody the wiser, and that is the failure the level exists to make
// preventable: the command runs, collects less than it was written to collect,
// and reports success.
func TestANameNoModuleRegisteredIsRefusedAtEitherLevel(t *testing.T) {
	reg := core.New()
	register(reg, core.Module{Name: "registered"})

	err := driveToEnd(t, reg, core.Drive{
		Participants:  []string{"registered"},
		DeclaringOnly: []core.Declaration{{Name: "misspelt"}},
	})
	if !errors.Is(err, core.ErrUnknownParticipant) {
		t.Fatalf("a declaration-level name that matches nothing returned %v, want %v",
			err, core.ErrUnknownParticipant)
	}
}

// TestTheSetsAreUnsettledUntilADriveSettlesThem pins that neither query
// invents an answer before there is a command to answer about.
//
// A registry holds registrations until it is driven, and until then no command
// has taken anything in. Answering "yes" for a registered module would be the
// reading that treats the available set as if it were a decision.
func TestTheSetsAreUnsettledUntilADriveSettlesThem(t *testing.T) {
	reg := core.New()
	register(reg, core.Module{Name: "registered"})

	if reg.Participating("registered") {
		t.Error("Participating answered before a drive had settled the set")
	}
	if reg.Declaring("registered") {
		t.Error("Declaring answered before a drive had settled the set")
	}
	if reg.Participating("never-registered") || reg.Declaring("never-registered") {
		t.Error("a query answered for a name no module registered")
	}
	if reg.AllResourcesInForce("registered") {
		t.Error("AllResourcesInForce answered before a drive had settled the adjustment")
	}
}

// TestAllResourcesInForceFollowsTheHostsAdjustment pins the override the host
// can set, and that it is off unless set.
//
// It says a module's declarations are in force whether or not it runs, which
// is the one thing a consumption point cannot work out on its own: its own
// domain is a decision of its own, and this is the host answering for it.
func TestAllResourcesInForceFollowsTheHostsAdjustment(t *testing.T) {
	for name, declared := range map[string]core.Declaration{
		"without the adjustment": {Name: "declares"},
		"with the adjustment":    {Name: "declares", AllResources: true},
	} {
		t.Run(name, func(t *testing.T) {
			reg := core.New()
			register(reg, core.Module{Name: "declares"})
			if err := driveToEnd(t, reg, core.Drive{DeclaringOnly: []core.Declaration{declared}}); err != nil {
				t.Fatalf("Drive returned %v", err)
			}
			want := declared.AllResources
			if got := reg.AllResourcesInForce("declares"); got != want {
				t.Errorf("AllResourcesInForce reported %v, want %v", got, want)
			}
		})
	}
}

// TestRepeatedParticipantCountsOnce pins what a module included at two levels
// of one command path produces: the same name twice, and one run of its
// stages.
func TestRepeatedParticipantCountsOnce(t *testing.T) {
	p := &probe{}
	reg := core.New()
	register(reg, preparing(p, "twice"))

	err := driveToEnd(t, reg, core.Drive{
		Depth:        core.StageNew,
		Participants: []string{"twice", "twice"},
		Action:       noAction,
	})
	if err != nil {
		t.Fatalf("Drive returned %v", err)
	}
	want := []string{"prepare twice", "new twice", "stop twice", "close twice"}
	if got := p.snapshot(); !slices.Equal(got, want) {
		t.Fatalf("a repeated name ran %v, want %v", got, want)
	}
}

func TestUnknownParticipantIsAnError(t *testing.T) {
	reg := core.New()
	register(reg, core.Module{Name: "known"})

	err := driveToEnd(t, reg, core.Drive{
		Depth:        core.StageNew,
		Participants: []string{"known", "ghost"},
	})
	if !errors.Is(err, core.ErrUnknownParticipant) {
		t.Fatalf("Drive returned %v, want ErrUnknownParticipant: writing a module-name "+
			"constant without importing the package that registers it compiles, so the "+
			"mismatch has to come out as a diagnosable startup failure", err)
	}
	if !strings.Contains(err.Error(), "ghost") {
		t.Errorf("error %q does not name the participant nobody registered", err)
	}
}

// TestDisabledParticipantIsDiagnosed and the test below it pin the two sides
// of what the startup diagnostics are for: a module that was included and is
// not running is the surprise worth reporting, while one the command never
// listed is the host's own writing.
func TestDisabledParticipantIsDiagnosed(t *testing.T) {
	p := &probe{}
	reg := core.New()
	off := preparing(p, "switched-off")
	off.Prepare = func(context.Context, *core.Registry) (core.Enablement, error) {
		return core.Enablement{State: core.StateDisabled, Reason: "no connection address configured"}, nil
	}
	register(reg, preparing(p, "running"), off)

	var err error
	text := captureStderr(t, func() {
		err = driveToEnd(t, reg, core.Drive{
			Depth:        core.StageServe,
			Participants: []string{"running", "switched-off"},
			Action:       noAction,
		})
	})
	if err != nil {
		t.Fatalf("Drive returned %v", err)
	}
	for _, want := range []string{"core: ", "switched-off", "no connection address configured"} {
		if !strings.Contains(text, want) {
			t.Errorf("stderr %q does not contain %q", text, want)
		}
	}
}

func TestModuleOutsideTheParticipantSetIsNotDiagnosed(t *testing.T) {
	p := &probe{}
	reg := core.New()
	register(reg, preparing(p, "included"), preparing(p, "left-out"))

	var err error
	text := captureStderr(t, func() {
		err = driveToEnd(t, reg, core.Drive{
			Depth:        core.StageServe,
			Participants: []string{"included"},
			Action:       noAction,
		})
	})
	if err != nil {
		t.Fatalf("Drive returned %v", err)
	}
	if strings.Contains(text, "left-out") {
		t.Errorf("stderr %q reports a module the command never listed; every command short "+
			"of the serving one would fill the screen with them", text)
	}
}

// TestDriveRejectsADepthBeyondServe pins the depth as a Stage constant. The
// call alone decides it, which is the criterion the registration-time panics
// use.
func TestDriveRejectsADepthBeyondServe(t *testing.T) {
	reg := core.New()

	msg := recoverMessage(t, func() {
		_ = reg.Drive(t.Context(), core.Drive{Depth: core.StageServe + 1})
	})
	if !strings.Contains(msg, "StageServe") {
		t.Fatalf("panic %q does not say which values a depth may take", msg)
	}
}

// TestSecondDrivePanics pins that a shallow drive spends the one use a
// registry has: it went in and came out, and the instances it built cannot be
// reused.
func TestSecondDrivePanics(t *testing.T) {
	p := &probe{}
	reg := core.New()
	register(reg, preparing(p, "only"))

	shallow := core.Drive{Depth: core.StageNew, Participants: []string{"only"}}
	if err := driveToEnd(t, reg, shallow); err != nil {
		t.Fatalf("the first drive returned %v", err)
	}
	msg := recoverMessage(t, func() {
		_ = reg.Drive(t.Context(), shallow)
	})
	if !strings.Contains(msg, "more than once") {
		t.Fatalf("panic %q does not explain that a registry is driven once", msg)
	}
}

func TestRunAfterAShallowDrivePanics(t *testing.T) {
	p := &probe{}
	reg := core.New()
	register(reg, preparing(p, "only"))

	err := driveToEnd(t, reg, core.Drive{Depth: core.StagePrepare, Participants: []string{"only"}})
	if err != nil {
		t.Fatalf("the shallow drive returned %v", err)
	}
	msg := recoverMessage(t, func() {
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		_ = reg.Run(ctx)
	})
	if !strings.Contains(msg, "more than once") {
		t.Fatalf("panic %q does not explain that Run and Drive share the one drive", msg)
	}
}

// TestRunIsDriveOverEverythingToServe pins Run as one value of Drive rather
// than a second driver: the same modules yield the same sequence either way.
func TestRunIsDriveOverEverythingToServe(t *testing.T) {
	viaRun := &probe{}
	runReg := core.New()
	register(runReg, preparing(viaRun, "alpha"), preparing(viaRun, "bravo"))
	if err := runUntilServed(t, runReg, viaRun, "serve alpha", "serve bravo"); err != nil {
		t.Fatalf("Run returned %v", err)
	}

	viaDrive := &probe{}
	driveReg := core.New()
	register(driveReg, preparing(viaDrive, "alpha"), preparing(viaDrive, "bravo"))
	err := driveUntilServed(t, driveReg, viaDrive, core.Drive{
		Depth:        core.StageServe,
		Participants: []string{"alpha", "bravo"},
	}, "serve alpha", "serve bravo")
	if err != nil {
		t.Fatalf("Drive returned %v", err)
	}

	if got, want := viaDrive.snapshot(), viaRun.snapshot(); !slices.Equal(got, want) {
		t.Fatalf("Drive over everything ran %v, while Run ran %v", got, want)
	}
}

// closureRegistry builds the dependency chain the closure tests expand:
// charlie needs what bravo delivers, bravo needs what alpha delivers, and
// lone stands apart.
func closureRegistry() *core.Registry {
	reg := core.New()
	register(reg,
		core.Module{Name: "alpha", Provides: []core.Provision{{Token: (*Cache)(nil)}}},
		core.Module{
			Name:     "bravo",
			Requires: []core.Requirement{{Token: (*Cache)(nil)}},
			Provides: []core.Provision{{Token: (*Store)(nil)}},
		},
		core.Module{Name: "charlie", Requires: []core.Requirement{{Token: (*Store)(nil)}}},
		core.Module{Name: "lone"},
	)
	return reg
}

// TestClosureExpandsRequirementsTransitively pins what including a module
// means: including what it takes to build it, all the way down.
func TestClosureExpandsRequirementsTransitively(t *testing.T) {
	got, err := closureRegistry().Closure([]string{"charlie"})
	if err != nil {
		t.Fatalf("Closure returned %v", err)
	}
	if want := []string{"alpha", "bravo", "charlie"}; !slices.Equal(got, want) {
		t.Fatalf("Closure gave %v, want %v", got, want)
	}
}

func TestClosureDeduplicatesAndSortsByName(t *testing.T) {
	got, err := closureRegistry().Closure([]string{"charlie", "lone", "bravo", "charlie"})
	if err != nil {
		t.Fatalf("Closure returned %v", err)
	}
	if want := []string{"alpha", "bravo", "charlie", "lone"}; !slices.Equal(got, want) {
		t.Fatalf("Closure gave %v, want %v", got, want)
	}
}

// TestClosureOfNothingIsNothing pins the same reading of an empty list that
// the participating set has, one step earlier.
func TestClosureOfNothingIsNothing(t *testing.T) {
	got, err := closureRegistry().Closure(nil)
	if err != nil {
		t.Fatalf("Closure returned %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("Closure of an empty list gave %v, want nothing", got)
	}
}

func TestClosureRejectsAnUnregisteredName(t *testing.T) {
	_, err := closureRegistry().Closure([]string{"ghost"})
	if !errors.Is(err, core.ErrUnknownParticipant) {
		t.Fatalf("Closure returned %v, want ErrUnknownParticipant", err)
	}
	if !strings.Contains(err.Error(), "ghost") {
		t.Errorf("error %q does not name the unregistered module", err)
	}
}

// TestClosureLeavesAnUndeliveredRequirementToTheDrive pins where a missing
// provider is reported. The closure has nothing to add for it, and the drive
// is where the failure can name the module that required it.
func TestClosureLeavesAnUndeliveredRequirementToTheDrive(t *testing.T) {
	reg := core.New()
	register(reg, core.Module{
		Name:     "consumer",
		Requires: []core.Requirement{{Token: (*Greeter)(nil)}},
	})

	got, err := reg.Closure([]string{"consumer"})
	if err != nil {
		t.Fatalf("Closure returned %v, want a requirement nobody delivers to add nothing", err)
	}
	if want := []string{"consumer"}; !slices.Equal(got, want) {
		t.Fatalf("Closure gave %v, want %v", got, want)
	}
}

// TestClosureFeedsTheParticipantSet pins the division of labour: the command
// tree expands the dependencies and hands the drive the literal result.
func TestClosureFeedsTheParticipantSet(t *testing.T) {
	p := &probe{}
	reg := core.New()
	provider := preparing(p, "provider")
	provider.Provides = []core.Provision{{Token: (*Cache)(nil)}}
	consumer := preparing(p, "consumer")
	consumer.Requires = []core.Requirement{{Token: (*Cache)(nil)}}
	register(reg, provider, consumer)

	participants, err := reg.Closure([]string{"consumer"})
	if err != nil {
		t.Fatalf("Closure returned %v", err)
	}
	err = driveToEnd(t, reg, core.Drive{
		Depth:        core.StageNew,
		Participants: participants,
		Action:       noAction,
	})
	if err != nil {
		t.Fatalf("driving over the closure returned %v", err)
	}
	for _, want := range []string{"new provider", "new consumer"} {
		if !p.has(want) {
			t.Errorf("the closure left out %q; recorded %v", want, p.snapshot())
		}
	}
}

// TestClosureLeavesOutAnOptionalRequirementsProviders pins that an optional
// requirement does not pull its providers into the participating set. Going
// without it is what Optional means, and a provider the host never listed,
// added here on the module's behalf, would leave the requirement no way to be
// absent — the command tree would then include a module the host cannot see
// in its own code.
func TestClosureLeavesOutAnOptionalRequirementsProviders(t *testing.T) {
	reg := core.New()
	reg.Register(core.Module{
		Name: "gate",
		Requires: []core.Requirement{
			{Token: (*closureRouter)(nil)},
			{Token: (*closureTracer)(nil), Optional: true},
		},
	})
	reg.Register(core.Module{
		Name:     "router",
		Provides: []core.Provision{{Token: (*closureRouter)(nil)}},
		New:      func(context.Context, *core.Registry) (any, error) { return closureRouterImpl{}, nil },
	})
	reg.Register(core.Module{
		Name:     "tracer",
		Provides: []core.Provision{{Token: (*closureTracer)(nil)}},
		New:      func(context.Context, *core.Registry) (any, error) { return closureTracerImpl{}, nil },
	})
	got, err := reg.Closure([]string{"gate"})
	if err != nil {
		t.Fatalf("the closure failed: %v", err)
	}
	want := []string{"gate", "router"}
	if !slices.Equal(got, want) {
		t.Fatalf("the closure is %v, want %v: a required capability brings its providers in and "+
			"an optional one does not", got, want)
	}
}

type (
	closureRouter     interface{ route() }
	closureTracer     interface{ trace() }
	closureRouterImpl struct{}
	closureTracerImpl struct{}
)

func (closureRouterImpl) route() {}

func (closureTracerImpl) trace() {}
