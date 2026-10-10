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
// all, and whatever was constructed on the way is stopped and then closed
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

// TestTheSetsAreFinalFromTheFirstPrepareCallback pins when the participating and
// declaring sets can be asked about: from the first Prepare callback on, with the
// answer the action gets later, and not only once the stances are in and
// resolution has run. A module's Prepare reads them to tell what its drive is
// made of before it states anything.
//
// Four modules stand for the four things a module can be to a drive: one taken
// in at run level, which does the asking in its Prepare; one the host supplied an
// instance for and no command names; one reached for its declarations alone; and
// one nothing takes in. The verdict is not among the answers that are final, since
// it is resolution's and resolution follows the last Prepare.
func TestTheSetsAreFinalFromTheFirstPrepareCallback(t *testing.T) {
	type answers struct{ participating, declaring, verdict bool }
	names := []string{"aa-asking", "bb-supplied", "cc-declared", "dd-left-out"}
	ask := func(r *core.Registry) map[string]answers {
		got := make(map[string]answers, len(names))
		for _, name := range names {
			_, verdict := r.Enablement(name)
			got[name] = answers{r.Participating(name), r.Declaring(name), verdict}
		}
		return got
	}

	var inPrepare, inAction map[string]answers
	prepares := 0
	reg := core.New()
	register(reg,
		core.Module{
			Name: "aa-asking",
			Prepare: func(_ context.Context, r *core.Registry) (core.Enablement, error) {
				prepares++
				inPrepare = ask(r)
				return core.Enablement{}, nil
			},
		},
		core.Module{Name: "bb-supplied"},
		core.Module{Name: "cc-declared"},
		core.Module{Name: "dd-left-out"},
	)
	reg.Supply(&box{id: "host"}, "bb-supplied")

	err := driveToEnd(t, reg, core.Drive{
		Depth:         core.StagePrepare,
		Participants:  []string{"aa-asking"},
		DeclaringOnly: []core.Declaration{{Name: "cc-declared"}},
		Action: func(_ context.Context, r *core.Registry) error {
			inAction = ask(r)
			return nil
		},
	})
	if err != nil {
		t.Fatalf("Drive returned %v", err)
	}
	if prepares != 1 {
		t.Fatalf("the asking module's Prepare ran %d times, want once", prepares)
	}

	for _, c := range []struct {
		name string
		// The sets, as they stand in Prepare and in the action alike.
		participating, declaring bool
	}{
		{"aa-asking", true, true},
		{"bb-supplied", true, true},
		{"cc-declared", false, true},
		{"dd-left-out", false, false},
	} {
		// No stance has been resolved when Prepare runs, and every one has by the
		// action, a module left out of the drive included: it is reported as
		// disabled there.
		if got, want := inPrepare[c.name], (answers{c.participating, c.declaring, false}); got != want {
			t.Errorf("in Prepare, %s is answered %+v, want %+v", c.name, got, want)
		}
		if got, want := inAction[c.name], (answers{c.participating, c.declaring, true}); got != want {
			t.Errorf("in the action, %s is answered %+v, want %+v", c.name, got, want)
		}
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

// TestAParticipantStatingDisabledFailsTheDrive pins the rule through the
// driver rather than through resolution alone: a module taken in at run level
// has to start, so one that states StateDisabled stops the startup with that
// named instead of quietly leaving the command one module short.
func TestAParticipantStatingDisabledFailsTheDrive(t *testing.T) {
	p := &probe{}
	reg := core.New()
	off := preparing(p, "switched-off")
	off.Prepare = func(context.Context, *core.Registry) (core.Enablement, error) {
		return core.Enablement{State: core.StateDisabled, Reason: "no connection address configured"}, nil
	}
	register(reg, preparing(p, "running"), off)

	err := driveToEnd(t, reg, core.Drive{
		Depth:        core.StageServe,
		Participants: []string{"running", "switched-off"},
		Action:       noAction,
	})
	if err == nil {
		t.Fatal("a drive accepted a participant that stated StateDisabled")
	}
	for _, want := range []string{"switched-off", "stated StateDisabled"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("Drive returned %q, which does not mention %q", err, want)
		}
	}
	if p.has("new switched-off") {
		t.Errorf("the module that could not start was constructed anyway: %v", p.snapshot())
	}
}

// TestDisabledParticipantIsDiagnosed and the test below it pin the two sides
// of what the startup diagnostics are for: a module that was included and is
// not running is the surprise worth reporting, while one the command never
// listed is the host's own writing.
//
// Standing down is the only way a participant misses the enabled set, now that
// a module taken in at run level has to start. The pair below is that case: an
// exclusive default that states StateAuto, and an exclusive implementation
// beside it that states StateEnabled, so propagation stands the default down.
func TestDisabledParticipantIsDiagnosed(t *testing.T) {
	p := &probe{}
	reg := core.New()
	def := preparing(p, "switched-off")
	def.Provides = []core.Provision{{Token: (*Cache)(nil), Exclusive: true}}
	ground := preparing(p, "running")
	ground.Provides = []core.Provision{{Token: (*Cache)(nil), Exclusive: true}}
	ground.Prepare = func(context.Context, *core.Registry) (core.Enablement, error) {
		p.record("prepare running")
		return core.Enablement{State: core.StateEnabled}, nil
	}
	register(reg, ground, def)

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
	for _, want := range []string{"core: ", "switched-off", "explicitly enabled"} {
		if !strings.Contains(text, want) {
			t.Errorf("stderr %q does not contain %q", text, want)
		}
	}
	if strings.Contains(text, `module "running"`) {
		t.Errorf("stderr %q reports a module that is enabled", text)
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

// TestClosureLeavesOutAnExclusiveProvider pins the exception to expansion. Two
// implementations may each claim a capability alone, and hauling one of them in
// on the requiring module's behalf would be the expansion rule making a
// decision. It makes none: the host names which implementation runs, and only
// the named one joins the command.
func TestClosureLeavesOutAnExclusiveProvider(t *testing.T) {
	reg := core.New()
	register(reg,
		core.Module{Name: "consumer", Requires: []core.Requirement{{Token: (*Cache)(nil)}}},
		core.Module{
			Name:     "memory",
			Provides: []core.Provision{{Token: (*Cache)(nil), Exclusive: true}},
		},
		core.Module{
			Name:     "redis",
			Provides: []core.Provision{{Token: (*Cache)(nil), Exclusive: true}},
		},
	)

	got, err := reg.Closure([]string{"consumer", "redis"})
	if err != nil {
		t.Fatalf("Closure returned %v", err)
	}
	if want := []string{"consumer", "redis"}; !slices.Equal(got, want) {
		t.Fatalf("Closure gave %v, want %v: a provider claiming the capability exclusively is "+
			"not pulled into a command that never named it", got, want)
	}
}

// TestClosureLeavesOutOnlyTheExclusiveProviders pins where the exception stops.
// A provider that does not claim the capability alone is added exactly as
// before, so a set of interchangeable backends behind one interface keeps
// coming along without the host listing each of them.
func TestClosureLeavesOutOnlyTheExclusiveProviders(t *testing.T) {
	reg := core.New()
	register(reg,
		core.Module{Name: "consumer", Requires: []core.Requirement{{Token: (*Cache)(nil)}}},
		core.Module{
			Name:     "memory",
			Provides: []core.Provision{{Token: (*Cache)(nil), Exclusive: true}},
		},
		core.Module{Name: "redis", Provides: []core.Provision{{Token: (*Cache)(nil)}}},
	)

	got, err := reg.Closure([]string{"consumer"})
	if err != nil {
		t.Fatalf("Closure returned %v", err)
	}
	if want := []string{"consumer", "redis"}; !slices.Equal(got, want) {
		t.Fatalf("Closure gave %v, want %v: only the exclusive provider is withheld", got, want)
	}
}

// TestClosureOfAConsumerOfAnExclusiveCapabilityIsItself pins what the exception
// leaves behind: with no implementation named, the command has no provider for a
// required capability at all, and dependency resolution is where that is
// reported, together with the fix.
func TestClosureOfAConsumerOfAnExclusiveCapabilityIsItself(t *testing.T) {
	reg := core.New()
	register(reg,
		core.Module{Name: "consumer", Requires: []core.Requirement{{Token: (*Cache)(nil)}}},
		core.Module{
			Name:     "memory",
			Provides: []core.Provision{{Token: (*Cache)(nil), Exclusive: true}},
		},
	)

	got, err := reg.Closure([]string{"consumer"})
	if err != nil {
		t.Fatalf("Closure returned %v", err)
	}
	if want := []string{"consumer"}; !slices.Equal(got, want) {
		t.Fatalf("Closure gave %v, want %v", got, want)
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

// providing declares that m delivers the capabilities the tokens designate.
func providing(m core.Module, tokens ...core.Token) core.Module {
	for _, token := range tokens {
		m.Provides = append(m.Provides, core.Provision{Token: token})
	}
	return m
}

// receiving wraps the callbacks of m that take an instance so that each one
// fails the test unless it is handed want.
func receiving(t *testing.T, m core.Module, want any) core.Module {
	t.Helper()
	wrap := func(stage string, next func(context.Context, *core.Registry, any) error) func(context.Context, *core.Registry, any) error {
		return func(ctx context.Context, r *core.Registry, got any) error {
			if got != want {
				t.Errorf("%s of %s was handed %#v, want the instance the host supplied", stage, m.Name, got)
			}
			return next(ctx, r, got)
		}
	}
	m.Migrate = wrap("Migrate", m.Migrate)
	m.Init = wrap("Init", m.Init)
	m.Start = wrap("Start", m.Start)
	m.Serve = wrap("Serve", m.Serve)
	m.Stop = wrap("Stop", m.Stop)
	m.Close = wrap("Close", m.Close)
	return m
}

// rival builds a recording module that delivers Cache, claims it exclusively
// or not, and states the given stance.
func rival(p *probe, name string, exclusive bool, state core.EnablementState) core.Module {
	m := preparing(p, name)
	m.Provides = []core.Provision{{Token: (*Cache)(nil), Exclusive: exclusive}}
	m.Prepare = func(context.Context, *core.Registry) (core.Enablement, error) {
		p.record("prepare %s", name)
		return core.Enablement{State: state}, nil
	}
	return m
}

// TestASuppliedModuleTakesPartWithoutBeingNamed pins the way a supplied module
// joins a drive: it is in the participating set from the start, whatever the
// list of participants says, and takes the stages from Migrate on with Prepare
// and New left out. A module that was neither named nor supplied an instance
// takes no part.
func TestASuppliedModuleTakesPartWithoutBeingNamed(t *testing.T) {
	want := []string{"migrate only", "init only", "start only", "serve only", "stop only", "close only"}
	for _, c := range []struct {
		name         string
		participants []string
	}{
		{"no participants", nil},
		{"an empty list of participants", []string{}},
		{"another module named", []string{"named"}},
		{"the supplied module named as well", []string{"only"}},
	} {
		t.Run(c.name, func(t *testing.T) {
			p := &probe{}
			reg := core.New()
			register(reg, preparing(p, "only"), preparing(p, "named"), preparing(p, "bystander"))
			reg.Supply(&box{id: "host"}, "only")

			err := driveToEnd(t, reg, core.Drive{
				Depth:        core.StageServe,
				Participants: c.participants,
				Action:       noAction,
			})
			if err != nil {
				t.Fatalf("Drive returned %v", err)
			}
			if got := p.eventsOf("only"); !slices.Equal(got, want) {
				t.Errorf("the supplied module ran %v, want %v", got, want)
			}
			if got := p.eventsOf("bystander"); len(got) != 0 {
				t.Errorf("a module that was neither named nor supplied ran %v", got)
			}
		})
	}
}

// TestADriveLeavesTheListOfParticipantsAsTheCallerWroteIt pins that adding the
// supplied modules to the participants does not write into the list the caller
// gave. The list is the host's, and one with room to spare would otherwise come
// back carrying the name of a module the host never listed.
func TestADriveLeavesTheListOfParticipantsAsTheCallerWroteIt(t *testing.T) {
	p := &probe{}
	reg := core.New()
	register(reg, preparing(p, "named"), preparing(p, "supplied"))
	reg.Supply(&box{id: "host"}, "supplied")
	listed := make([]string, 1, 4)
	listed[0] = "named"

	err := driveToEnd(t, reg, core.Drive{Depth: core.StageNew, Participants: listed, Action: noAction})
	if err != nil {
		t.Fatalf("Drive returned %v", err)
	}

	if got, want := listed[:cap(listed)], []string{"named", "", "", ""}; !slices.Equal(got, want) {
		t.Fatalf("the drive left the list it was given as %q, want %q", got, want)
	}
	if !reg.Participating("supplied") {
		t.Fatal("the supplied module did not take part, so the drive never had a name to add")
	}
}

// TestASuppliedModuleNeverRunsItsPrepareOrNew pins that the instance the host
// built stands in for what the module's Prepare and New produce: a Prepare
// that would fail or state StateDisabled and a New that would fail leave the
// drive untouched, because neither runs.
func TestASuppliedModuleNeverRunsItsPrepareOrNew(t *testing.T) {
	ran := errors.New("the module's own Prepare or New ran")
	for _, c := range []struct {
		name    string
		prepare func(context.Context, *core.Registry) (core.Enablement, error)
	}{
		{"a Prepare that fails", func(context.Context, *core.Registry) (core.Enablement, error) {
			return core.Enablement{}, ran
		}},
		{"a Prepare that states it is disabled", func(context.Context, *core.Registry) (core.Enablement, error) {
			return core.Enablement{State: core.StateDisabled}, nil
		}},
	} {
		t.Run(c.name, func(t *testing.T) {
			p := &probe{}
			reg := core.New()
			m := preparing(p, "only")
			m.Prepare = c.prepare
			m.New = func(context.Context, *core.Registry) (any, error) { return nil, ran }
			register(reg, m)
			reg.Supply(&box{id: "host"}, "only")

			err := driveToEnd(t, reg, core.Drive{Depth: core.StageServe, Action: noAction})
			if err != nil {
				t.Fatalf("Drive returned %v, want the module's Prepare and New not to run", err)
			}
			if got, ok := reg.Enablement("only"); !ok || got.State != core.StateEnabled {
				t.Errorf("Enablement reported %+v/%v, want the fixed stance StateEnabled", got, ok)
			}
		})
	}
}

// TestEveryCallbackOfASuppliedModuleReceivesTheSuppliedInstance pins what the
// callbacks that take an instance are handed: the instance the host supplied,
// at every stage the module declares, Stop and Close included. The sequence
// asserted at the end shows that each callback ran and so was checked.
func TestEveryCallbackOfASuppliedModuleReceivesTheSuppliedInstance(t *testing.T) {
	p := &probe{}
	reg := core.New()
	handed := &box{id: "host"}
	register(reg, receiving(t, preparing(p, "only"), handed))
	reg.Supply(handed, "only")

	if err := driveToEnd(t, reg, core.Drive{Depth: core.StageServe, Action: noAction}); err != nil {
		t.Fatalf("Drive returned %v", err)
	}
	want := []string{"migrate only", "init only", "start only", "serve only", "stop only", "close only"}
	if got := p.eventsOf("only"); !slices.Equal(got, want) {
		t.Fatalf("the supplied module ran %v, want %v", got, want)
	}
}

// TestADepthStopsASuppliedModuleLikeAnyOther pins what a depth means for a
// module whose instance already exists: the stages past it do not run, and the
// instance is stopped and then closed at every depth. StagePrepare ends before
// construction reaches the module and StageNew ends right after it, so at those
// two depths Stop and Close are the only callbacks of the module that run.
func TestADepthStopsASuppliedModuleLikeAnyOther(t *testing.T) {
	cases := []struct {
		depth core.Stage
		want  []string
	}{
		{core.StagePrepare, []string{"stop only", "close only"}},
		{core.StageNew, []string{"stop only", "close only"}},
		{core.StageMigrate, []string{"migrate only", "stop only", "close only"}},
		{core.StageInit, []string{"migrate only", "init only", "stop only", "close only"}},
		{core.StageStart, []string{"migrate only", "init only", "start only", "stop only", "close only"}},
		{core.StageServe, []string{
			"migrate only", "init only", "start only", "serve only", "stop only", "close only",
		}},
	}
	for _, c := range cases {
		t.Run(c.depth.String(), func(t *testing.T) {
			p := &probe{}
			reg := core.New()
			register(reg, preparing(p, "only"))
			reg.Supply(&box{id: "host"}, "only")

			err := driveToEnd(t, reg, core.Drive{Depth: c.depth, Action: noAction})
			if err != nil {
				t.Fatalf("Drive returned %v", err)
			}
			if got := p.snapshot(); !slices.Equal(got, c.want) {
				t.Fatalf("a drive to %s ran %v, want %v", c.depth, got, c.want)
			}
		})
	}
}

// TestASuppliedModuleIsInEverySetAndStatesEnabled pins what the queries say of
// a supplied module. Nothing is settled before a drive, so every answer is
// negative then, supplied or not; once the drive has settled the sets the module
// is a participant and in the declaring set, its stance is the fixed
// StateEnabled, and the host's adjustment does not apply to it.
func TestASuppliedModuleIsInEverySetAndStatesEnabled(t *testing.T) {
	p := &probe{}
	reg := core.New()
	register(reg, preparing(p, "only"), preparing(p, "bystander"))
	reg.Supply(&box{id: "host"}, "only")

	if _, ok := reg.Enablement("only"); ok {
		t.Error("Enablement answered for a supplied module before a drive resolved anything")
	}
	if reg.Participating("only") || reg.Declaring("only") {
		t.Error("a supplied module counted as a participant before a drive settled the sets")
	}

	if err := driveToEnd(t, reg, core.Drive{Depth: core.StagePrepare}); err != nil {
		t.Fatalf("Drive returned %v", err)
	}
	if got, ok := reg.Enablement("only"); !ok || got.State != core.StateEnabled {
		t.Errorf("Enablement reported %+v/%v, want StateEnabled", got, ok)
	}
	if !reg.Participating("only") {
		t.Error("Participating denied a supplied module")
	}
	if !reg.Declaring("only") {
		t.Error("Declaring denied a supplied module")
	}
	if reg.AllResourcesInForce("only") {
		t.Error("AllResourcesInForce answered true for a module that is a participant")
	}
	if got, ok := reg.Enablement("bystander"); !ok || got.State != core.StateDisabled {
		t.Errorf("Enablement reported %+v/%v for a module that was neither named nor supplied, "+
			"want StateDisabled", got, ok)
	}
	if reg.Participating("bystander") || reg.Declaring("bystander") {
		t.Error("a module that was neither named nor supplied counted as taking part")
	}
}

// TestANameAtDeclarationLevelDoesNotMakeASuppliedModuleDeclarationOnly pins
// that a supplied module is always a participant: the command tree names it at
// the static level knowing nothing of the supply, and the registry treats it
// at run level, where the host's adjustment lapses.
func TestANameAtDeclarationLevelDoesNotMakeASuppliedModuleDeclarationOnly(t *testing.T) {
	want := []string{"migrate only", "init only", "start only", "serve only", "stop only", "close only"}
	for _, c := range []struct {
		name         string
		participants []string
	}{
		{"at declaration level alone", nil},
		{"at both levels", []string{"only"}},
	} {
		t.Run(c.name, func(t *testing.T) {
			p := &probe{}
			reg := core.New()
			register(reg, preparing(p, "only"))
			reg.Supply(&box{id: "host"}, "only")

			err := driveToEnd(t, reg, core.Drive{
				Depth:         core.StageServe,
				Participants:  c.participants,
				DeclaringOnly: []core.Declaration{{Name: "only", AllResources: true}},
				Action:        noAction,
			})
			if err != nil {
				t.Fatalf("Drive returned %v", err)
			}
			if !reg.Participating("only") {
				t.Error("Participating denied a supplied module the command tree named at declaration level")
			}
			if !reg.Declaring("only") {
				t.Error("Declaring denied a supplied module the command tree named at declaration level")
			}
			if reg.AllResourcesInForce("only") {
				t.Error("AllResourcesInForce answered true for a participant, whose adjustment lapses")
			}
			if got := p.eventsOf("only"); !slices.Equal(got, want) {
				t.Errorf("the supplied module ran %v, want %v", got, want)
			}
		})
	}
}

// TestASuppliedProviderMeetsARivalUnderTheOrdinaryResolutionRules pins that a
// supplied module has no rules of its own in resolution beyond its fixed
// stance. Its stance is StateEnabled, so an exclusive rival that states
// StateAuto stands down, and any other meeting that leaves two providers
// enabled with an exclusive claim among them fails the startup.
func TestASuppliedProviderMeetsARivalUnderTheOrdinaryResolutionRules(t *testing.T) {
	type outcome int
	const (
		standsDown outcome = iota
		violated
		coexist
	)
	cases := []struct {
		name              string
		rivalExclusive    bool
		rivalState        core.EnablementState
		suppliedExclusive bool
		want              outcome
	}{
		{"an exclusive default that states auto", true, core.StateAuto, false, standsDown},
		{"an exclusive default that states auto, against an exclusive supplied provider", true, core.StateAuto, true, standsDown},
		{"an exclusive rival that states enabled", true, core.StateEnabled, false, violated},
		{"an exclusive rival that states enabled, against an exclusive supplied provider", true, core.StateEnabled, true, violated},
		{"a rival that claims nothing, against an exclusive supplied provider", false, core.StateAuto, true, violated},
		{"an enabled rival that claims nothing, against an exclusive supplied provider", false, core.StateEnabled, true, violated},
		{"neither claims the capability", false, core.StateAuto, false, coexist},
		{"neither claims it and the rival states enabled", false, core.StateEnabled, false, coexist},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			p := &probe{}
			reg := core.New()
			fake := preparing(p, "fake")
			fake.Provides = []core.Provision{{Token: (*Cache)(nil), Exclusive: c.suppliedExclusive}}
			register(reg, rival(p, "backend", c.rivalExclusive, c.rivalState), fake)
			reg.Supply(&box{id: "fake"}, "fake")

			var served string
			var lookup error
			var all []string
			var err error
			text := captureStderr(t, func() {
				err = driveToEnd(t, reg, core.Drive{
					Depth:        core.StageInit,
					Participants: []string{"backend"},
					Action: func(_ context.Context, r *core.Registry) error {
						if cache, resolveErr := core.Resolve[Cache](r); resolveErr == nil {
							served = cache.CacheID()
						} else {
							lookup = resolveErr
						}
						for _, cache := range core.ResolveAll[Cache](r) {
							all = append(all, cache.CacheID())
						}
						return nil
					},
				})
			})

			switch c.want {
			case violated:
				if !errors.Is(err, core.ErrExclusiveViolated) {
					t.Fatalf("Drive returned %v, want ErrExclusiveViolated", err)
				}
				for _, name := range []string{"backend", "fake"} {
					if !strings.Contains(err.Error(), name) {
						t.Errorf("error %q does not name the conflicting module %q", err, name)
					}
				}
				// The supplied provider cannot be disabled from the command tree
				// and does not stand down, so the failure says which party it is
				// and that taking its instance away is the way out; the rival has
				// no instance of this kind and is not described as having one.
				for _, want := range []string{`"fake" has a supplied instance`, "stop supplying an instance"} {
					if !strings.Contains(err.Error(), want) {
						t.Errorf("error %q does not say %q", err, want)
					}
				}
				if strings.Contains(err.Error(), `"backend" has`) {
					t.Errorf("error %q says the rival has a supplied instance, which nobody supplied for it", err)
				}
				if p.has("new backend") {
					t.Errorf("a module was constructed although the startup failed in resolution: %v", p.snapshot())
				}
			case standsDown:
				if err != nil {
					t.Fatalf("Drive returned %v", err)
				}
				if served != "fake" {
					t.Errorf("the capability was served by %q, want the supplied instance", served)
				}
				if got, ok := reg.Enablement("backend"); !ok || got.State != core.StateDisabled {
					t.Errorf("Enablement reported %+v/%v for the rival that stood down", got, ok)
				}
				if p.has("new backend") {
					t.Errorf("a rival that stood down was constructed: %v", p.snapshot())
				}
				if !strings.Contains(text, "backend") {
					t.Errorf("stderr %q does not report the module that stood down", text)
				}
			case coexist:
				if err != nil {
					t.Fatalf("Drive returned %v", err)
				}
				if !errors.Is(lookup, core.ErrAmbiguousProvider) {
					t.Errorf("a single-valued lookup returned %v, want ErrAmbiguousProvider once both exist", lookup)
				}
				if want := []string{"backend", "fake"}; !slices.Equal(all, want) {
					t.Errorf("ResolveAll returned %v, want both providers in module-name order %v", all, want)
				}
			}
			if got, ok := reg.Enablement("fake"); err == nil && (!ok || got.State != core.StateEnabled) {
				t.Errorf("Enablement reported %+v/%v for the supplied module", got, ok)
			}
		})
	}
}

// TestAnInstanceSuppliedToOneProviderIsTheOnlyOneUntilTheOthersAreConstructed
// pins how a single-valued lookup changes as the drive builds the other
// providers of a capability that is not exclusive. A Prepare sees the supplied
// instance alone, since nothing else exists yet, and succeeds; once the other
// provider is constructed the same lookup has two instances to choose from and
// fails as any lookup does with two.
func TestAnInstanceSuppliedToOneProviderIsTheOnlyOneUntilTheOthersAreConstructed(t *testing.T) {
	p := &probe{}
	reg := core.New()
	var inPrepare string
	var afterwards error
	watcher := preparing(p, "watcher")
	watcher.Prepare = func(_ context.Context, r *core.Registry) (core.Enablement, error) {
		cache, err := core.Resolve[Cache](r)
		if err != nil {
			return core.Enablement{}, err
		}
		inPrepare = cache.CacheID()
		return core.Enablement{}, nil
	}
	register(reg, watcher, rival(p, "backend", false, core.StateAuto), providing(preparing(p, "fake"), (*Cache)(nil)))
	reg.Supply(&box{id: "fake"}, "fake")

	err := driveToEnd(t, reg, core.Drive{
		Depth:        core.StageInit,
		Participants: []string{"watcher", "backend"},
		Action: func(_ context.Context, r *core.Registry) error {
			_, afterwards = core.Resolve[Cache](r)
			return nil
		},
	})
	if err != nil {
		t.Fatalf("Drive returned %v", err)
	}
	if inPrepare != "fake" {
		t.Errorf("the lookup in Prepare returned %q, want the supplied instance", inPrepare)
	}
	if !errors.Is(afterwards, core.ErrAmbiguousProvider) {
		t.Errorf("the lookup after construction returned %v, want ErrAmbiguousProvider", afterwards)
	}
}

// TestARequirerFollowsASuppliedModuleAndWhatItRequiresPrecedesIt pins that a
// supplied module takes its place in the dependency order like any other: after
// what it requires, before what requires it, at every stage that is ordered and
// in the reverse at shutdown. The names run against the order the
// requirements give, and so does the order of registration, so neither can pass
// for it.
func TestARequirerFollowsASuppliedModuleAndWhatItRequiresPrecedesIt(t *testing.T) {
	p := &probe{}
	reg := core.New()
	top := preparing(p, "aa-top")
	top.Requires = []core.Requirement{{Token: (*Cache)(nil)}}
	middle := providing(preparing(p, "mm-supplied"), (*Cache)(nil))
	middle.Requires = []core.Requirement{{Token: (*Store)(nil)}}
	base := providing(preparing(p, "zz-base"), (*Store)(nil))
	register(reg, top, middle, base)
	reg.Supply(&box{id: "host"}, "mm-supplied")

	err := driveToEnd(t, reg, core.Drive{
		Depth:        core.StageServe,
		Participants: []string{"zz-base", "aa-top"},
		Action:       noAction,
	})
	if err != nil {
		t.Fatalf("Drive returned %v", err)
	}
	for _, stage := range []string{"migrate", "init", "start", "serve"} {
		atBase := p.indexOf(t, stage+" zz-base")
		atMiddle := p.indexOf(t, stage+" mm-supplied")
		atTop := p.indexOf(t, stage+" aa-top")
		if atBase > atMiddle || atMiddle > atTop {
			t.Errorf("%s ran out of dependency order: %v", stage, p.snapshot())
		}
	}
	for _, stage := range []string{"stop", "close"} {
		atBase := p.indexOf(t, stage+" zz-base")
		atMiddle := p.indexOf(t, stage+" mm-supplied")
		atTop := p.indexOf(t, stage+" aa-top")
		if atTop > atMiddle || atMiddle > atBase {
			t.Errorf("%s ran out of reverse dependency order: %v", stage, p.snapshot())
		}
	}
}

// TestSuppliedModulesAreOrderedByWhatTheyRequireOfEachOther pins the
// dependency order for a drive in which no module is constructed at all: every
// instance is supplied, and the requirement alone orders them at each stage and
// at shutdown. The names run against that order.
func TestSuppliedModulesAreOrderedByWhatTheyRequireOfEachOther(t *testing.T) {
	p := &probe{}
	reg := core.New()
	user := preparing(p, "aa-user")
	user.Requires = []core.Requirement{{Token: (*Cache)(nil)}}
	register(reg, user, providing(preparing(p, "zz-base"), (*Cache)(nil)))
	reg.Supply(&box{id: "user"}, "aa-user")
	reg.Supply(&box{id: "base"}, "zz-base")

	err := driveToEnd(t, reg, core.Drive{Depth: core.StageServe, Action: noAction})
	if err != nil {
		t.Fatalf("Drive returned %v", err)
	}
	for _, stage := range []string{"migrate", "init", "start", "serve"} {
		if p.indexOf(t, stage+" zz-base") > p.indexOf(t, stage+" aa-user") {
			t.Errorf("%s ran the module that requires before the one it requires: %v", stage, p.snapshot())
		}
	}
	for _, stage := range []string{"stop", "close"} {
		if p.indexOf(t, stage+" aa-user") > p.indexOf(t, stage+" zz-base") {
			t.Errorf("%s released the module that is required before the one that requires it: %v",
				stage, p.snapshot())
		}
	}
}

// TestASuppliedModulesRequirementsAreNotPulledIn pins that the registry adds
// the supplied module to the participating set and nothing more. What it
// requires has to be in the set already, by being named or by being supplied,
// or the dependency resolution fails naming the module; a requirement marked
// Optional is skipped when nothing provides it and orders the module as usual
// when something does.
func TestASuppliedModulesRequirementsAreNotPulledIn(t *testing.T) {
	cases := []struct {
		name         string
		optional     bool
		participants []string
		wantErr      error
		keeperRuns   bool
	}{
		{"a required provider nobody named", false, nil, core.ErrMissingProvider, false},
		{"the same provider, named", false, []string{"keeper"}, nil, true},
		{"an optional provider nobody named", true, nil, nil, false},
		{"an optional provider, named", true, []string{"keeper"}, nil, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			p := &probe{}
			reg := core.New()
			needy := providing(preparing(p, "needy"), (*Reader)(nil))
			needy.Requires = []core.Requirement{{Token: (*Cache)(nil), Optional: c.optional}}
			register(reg, needy, providing(preparing(p, "keeper"), (*Cache)(nil)))
			reg.Supply(reader{}, "needy")

			err := driveToEnd(t, reg, core.Drive{
				Depth:        core.StageInit,
				Participants: c.participants,
				Action:       noAction,
			})

			if c.wantErr != nil {
				if !errors.Is(err, c.wantErr) {
					t.Fatalf("Drive returned %v, want %v", err, c.wantErr)
				}
				for _, want := range []string{"needy", "Cache"} {
					if !strings.Contains(err.Error(), want) {
						t.Errorf("error %q does not mention %q", err, want)
					}
				}
				if !strings.Contains(strings.ToLower(err.Error()), "suppl") {
					t.Errorf("error %q does not say that the module has a supplied instance, which "+
						"changes how it is taken out of the command", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("Drive returned %v", err)
			}
			if got := p.has("migrate keeper"); got != c.keeperRuns {
				t.Errorf("the provider ran: %v, want %v; recorded %v", got, c.keeperRuns, p.snapshot())
			}
			if c.keeperRuns && p.indexOf(t, "migrate keeper") > p.indexOf(t, "migrate needy") {
				t.Errorf("the provider was not ordered ahead of the module that requires it: %v", p.snapshot())
			}
		})
	}
}

// TestClosureTreatsASuppliedModuleLikeAnyOther pins that expansion knows
// nothing of supplies: a module that requires a capability brings in every
// provider of it, the supplied one among them, and a supplied module that
// itself requires something is expanded as any module is.
func TestClosureTreatsASuppliedModuleLikeAnyOther(t *testing.T) {
	reg := core.New()
	register(reg,
		core.Module{Name: "consumer", Requires: []core.Requirement{{Token: (*Cache)(nil)}}},
		core.Module{Name: "alpha", Provides: []core.Provision{{Token: (*Cache)(nil)}}},
		core.Module{Name: "bravo", Provides: []core.Provision{{Token: (*Cache)(nil)}}},
		core.Module{
			Name:     "needy",
			Requires: []core.Requirement{{Token: (*Store)(nil)}},
			Provides: []core.Provision{{Token: (*Reader)(nil)}},
		},
		core.Module{Name: "keeper", Provides: []core.Provision{{Token: (*Store)(nil)}}},
	)
	reg.Supply(&box{id: "host"}, "alpha")
	reg.Supply(reader{}, "needy")

	got, err := reg.Closure([]string{"consumer", "needy"})
	if err != nil {
		t.Fatalf("Closure returned %v", err)
	}
	if want := []string{"alpha", "bravo", "consumer", "keeper", "needy"}; !slices.Equal(got, want) {
		t.Fatalf("Closure gave %v, want %v", got, want)
	}
}

// TestASuppliedExclusiveProviderMeetsAConsumerThatNamesNoImplementation pins
// the supplied way out of a capability whose providers all claim it
// exclusively. The closure brings in none of them, and the module that has an
// instance supplied joins the participating set by itself, so the requirement
// has its provider and the other implementation is left out.
func TestASuppliedExclusiveProviderMeetsAConsumerThatNamesNoImplementation(t *testing.T) {
	p := &probe{}
	reg := core.New()
	app := p.module("app")
	app.Requires = []core.Requirement{{Token: (*Cache)(nil)}}
	memory := p.module("memory")
	memory.Provides = []core.Provision{{Token: (*Cache)(nil), Exclusive: true}}
	redis := p.module("redis")
	redis.Provides = []core.Provision{{Token: (*Cache)(nil), Exclusive: true}}
	register(reg, app, memory, redis)
	reg.Supply(&box{id: "memory"}, "memory")

	participants, err := reg.Closure([]string{"app"})
	if err != nil {
		t.Fatalf("Closure returned %v", err)
	}
	if want := []string{"app"}; !slices.Equal(participants, want) {
		t.Fatalf("Closure gave %v, want %v: no exclusive provider is brought in", participants, want)
	}
	err = driveToEnd(t, reg, core.Drive{
		Depth:        core.StageInit,
		Participants: participants,
		Action:       noAction,
	})
	if err != nil {
		t.Fatalf("Drive returned %v", err)
	}
	if p.indexOf(t, "migrate memory") > p.indexOf(t, "migrate app") {
		t.Errorf("the supplied provider was not ordered ahead of its consumer: %v", p.snapshot())
	}
	if got := p.eventsOf("redis"); len(got) != 0 {
		t.Errorf("the implementation nobody named or supplied ran %v", got)
	}
}

// TestRunTakesInEveryRegisteredModuleAtRunLevel pins the sets Run settles:
// every registered module is a participant and so in the declaring set, and the
// host's adjustment has nothing to apply to, because no module is taken in at
// the static level.
func TestRunTakesInEveryRegisteredModuleAtRunLevel(t *testing.T) {
	p := &probe{}
	reg := core.New()
	register(reg, preparing(p, "alpha"), preparing(p, "bravo"))

	if err := runUntilServed(t, reg, p, "serve alpha", "serve bravo"); err != nil {
		t.Fatalf("Run returned %v", err)
	}
	for _, name := range []string{"alpha", "bravo"} {
		if !reg.Participating(name) || !reg.Declaring(name) {
			t.Errorf("%s is not in the participating and declaring sets after Run", name)
		}
		if reg.AllResourcesInForce(name) {
			t.Errorf("AllResourcesInForce answered true for %s under Run", name)
		}
	}
}
