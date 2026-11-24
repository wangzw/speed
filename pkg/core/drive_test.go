package core_test

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/vislake/speed/pkg/core"
)

func preparing(p *probe, name string) core.Module {
	m := p.module(name)
	m.Prepare = func(context.Context, *core.Registry) (core.Enablement, error) {
		p.record("prepare %s", name)
		return core.Enablement{}, nil
	}
	return m
}

func noAction(context.Context, *core.Registry) error { return nil }

// TestStageStringNamesTheCallbackItDrives checks that each stage renders as the name of the callback
// a stage failure blames.
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

// TestDepthStopsEveryCallbackBeyondIt checks that a drive stops at its depth, with Stop and Close
// still running for what was constructed.
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

// TestDepthPrepareSkipsDependencyResolution pins that resolution is the preliminary to
// construction rather than a step of its own.
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

// TestActionRunsAtTheDepthAndReachesTheRegistry checks that the action runs after the depth's last
// stage, can resolve constructed instances, and runs before shutdown begins.
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

// TestActionFailureIsPrimaryAndStillRollsBack checks that an action error is returned unchanged and
// the constructed modules are still stopped and closed.
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

// TestShallowDriveWithoutAnActionDoesNotBlock pins that arriving is the whole of a shallow
// command's work.
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

// TestServeWithoutAnActionBlocksUntilCancelled checks that a drive to Serve without an action
// blocks until its context is cancelled, and then returns nil.
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

// TestServeWithAnActionDoesNotBlock checks that an action after Serve runs and the drive returns
// without waiting for a cancellation.
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

// TestZeroDriveDoesNothing pins the zero value of the whole struct: no depth past the stances,
// nobody taking part, nothing to do on arrival.
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

// TestEmptyParticipantSetIncludesNothing pins the most dangerous reading a zero value could be
// given.
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

// TestModulesOutsideTheParticipantSetTakeNoPart checks that a module the command left out runs no
// callback and is reported disabled with a participation reason.
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

// TestADeclarationLevelModuleContributesDeclarationsAndNothingElse pins what the second level of
// inclusion reaches and what it leaves alone.
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

// TestANameNoModuleRegisteredIsRefusedAtEitherLevel pins that the second level is checked the way
// the first is.
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

// TestTheSetsAreUnsettledUntilADriveSettlesThem pins that neither query invents an answer before
// there is a command to answer about.
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

// TestTheSetsAreFinalFromTheFirstPrepareCallback checks that participation and declaration are
// settled before the first Prepare and the verdicts fill in by the action.
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
		name                     string
		participating, declaring bool
	}{
		{"aa-asking", true, true},
		{"bb-supplied", true, true},
		{"cc-declared", false, true},
		{"dd-left-out", false, false},
	} {
		if got, want := inPrepare[c.name], (answers{c.participating, c.declaring, false}); got != want {
			t.Errorf("in Prepare, %s is answered %+v, want %+v", c.name, got, want)
		}
		if got, want := inAction[c.name], (answers{c.participating, c.declaring, true}); got != want {
			t.Errorf("in the action, %s is answered %+v, want %+v", c.name, got, want)
		}
	}
}

// TestAllResourcesInForceFollowsTheHostsAdjustment pins the override the host can set, and that it
// is off unless set.
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

// TestRepeatedParticipantCountsOnce pins what a module included at two levels of one command path
// produces: the same name twice, and one run of its stages.
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

// TestUnknownParticipantIsAnError checks that naming an unregistered participant fails as
// ErrUnknownParticipant naming it.
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

// TestAParticipantStatingDisabledSitsOutTheDrive checks that a module that states disabled is
// reported on stderr with its reason and is never constructed, while its neighbour runs.
func TestAParticipantStatingDisabledSitsOutTheDrive(t *testing.T) {
	const reason = "GitHub app credentials not configured"
	p := &probe{}
	reg := core.New()
	off := preparing(p, "switched-off")
	off.Prepare = func(context.Context, *core.Registry) (core.Enablement, error) {
		return core.Enablement{State: core.StateDisabled, Reason: reason}, nil
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
		t.Fatalf("Drive returned %v, want the module to sit out the run", err)
	}
	if !strings.Contains(text, "switched-off") || !strings.Contains(text, reason) {
		t.Errorf("stderr %q does not report the disabled module and the reason it gave", text)
	}
	if p.has("new switched-off") {
		t.Errorf("the module that stood itself down was constructed: %v", p.snapshot())
	}
	if !p.has("new running") {
		t.Errorf("the module beside it did not start: %v", p.snapshot())
	}
	if got, ok := reg.Enablement("switched-off"); !ok || got.State != core.StateDisabled || got.Reason != reason {
		t.Errorf("Enablement reported %+v/%v, want the module's own disabled verdict and reason", got, ok)
	}
}

// TestRequiringADisabledModuleNamesItAndItsReason checks that a requirement whose only provider
// disabled fails as ErrMissingProvider naming both and the reason.
func TestRequiringADisabledModuleNamesItAndItsReason(t *testing.T) {
	const reason = "no connection address configured"
	p := &probe{}
	reg := core.New()
	provider := preparing(p, "provider")
	provider.Provides = []core.Provision{{Token: (*Cache)(nil)}}
	provider.Prepare = func(context.Context, *core.Registry) (core.Enablement, error) {
		return core.Enablement{State: core.StateDisabled, Reason: reason}, nil
	}
	consumer := preparing(p, "consumer")
	consumer.Requires = []core.Requirement{{Token: (*Cache)(nil)}}
	register(reg, provider, consumer)

	err := driveToEnd(t, reg, core.Drive{
		Depth:        core.StageServe,
		Participants: []string{"provider", "consumer"},
		Action:       noAction,
	})
	if !errors.Is(err, core.ErrMissingProvider) {
		t.Fatalf("Drive returned %v, want ErrMissingProvider", err)
	}
	for _, want := range []string{`"provider"`, reason, `"consumer"`} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not name %q", err, want)
		}
	}
	if p.has("new consumer") || p.has("new provider") {
		t.Errorf("a module was constructed before the unmet requirement was reported: %v", p.snapshot())
	}
}

// TestDisabledParticipantIsDiagnosed checks that a module stood down for an exclusive provider
// writes a diagnostic naming it and the cause, while the enabled one writes none.
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

// TestModuleOutsideTheParticipantSetIsNotDiagnosed checks that a module the command never listed
// writes no diagnostic line.
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

// TestDriveRejectsADepthBeyondServe pins the depth as a Stage constant.
func TestDriveRejectsADepthBeyondServe(t *testing.T) {
	reg := core.New()

	msg := recoverMessage(t, func() {
		_ = reg.Drive(t.Context(), core.Drive{Depth: core.StageServe + 1})
	})
	if !strings.Contains(msg, "StageServe") {
		t.Fatalf("panic %q does not say which values a depth may take", msg)
	}
}

// TestSecondDrivePanics pins that a shallow drive spends the one use a registry has: it went in
// and came out, and the instances it built cannot be reused.
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

// TestRunAfterAShallowDrivePanics checks that Run after a drive that did not go all the way panics,
// explaining that Run and Drive share the one drive.
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

// TestRunIsDriveOverEverythingToServe pins Run as one value of Drive rather than a second driver:
// the same modules yield the same sequence either way.
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

// TestClosureExpandsRequirementsTransitively pins what including a module means: including what it
// takes to build it, all the way down.
func TestClosureExpandsRequirementsTransitively(t *testing.T) {
	got, err := closureRegistry().Closure([]string{"charlie"})
	if err != nil {
		t.Fatalf("Closure returned %v", err)
	}
	if want := []string{"alpha", "bravo", "charlie"}; !slices.Equal(got, want) {
		t.Fatalf("Closure gave %v, want %v", got, want)
	}
}

// TestClosureDeduplicatesAndSortsByName checks that Closure returns the transitive set once each,
// in name order.
func TestClosureDeduplicatesAndSortsByName(t *testing.T) {
	got, err := closureRegistry().Closure([]string{"charlie", "lone", "bravo", "charlie"})
	if err != nil {
		t.Fatalf("Closure returned %v", err)
	}
	if want := []string{"alpha", "bravo", "charlie", "lone"}; !slices.Equal(got, want) {
		t.Fatalf("Closure gave %v, want %v", got, want)
	}
}

// TestClosureOfNothingIsNothing pins the same reading of an empty list that the participating set
// has, one step earlier.
func TestClosureOfNothingIsNothing(t *testing.T) {
	got, err := closureRegistry().Closure(nil)
	if err != nil {
		t.Fatalf("Closure returned %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("Closure of an empty list gave %v, want nothing", got)
	}
}

// TestClosureRejectsAnUnregisteredName checks that Closure fails as ErrUnknownParticipant naming
// the unregistered module.
func TestClosureRejectsAnUnregisteredName(t *testing.T) {
	_, err := closureRegistry().Closure([]string{"ghost"})
	if !errors.Is(err, core.ErrUnknownParticipant) {
		t.Fatalf("Closure returned %v, want ErrUnknownParticipant", err)
	}
	if !strings.Contains(err.Error(), "ghost") {
		t.Errorf("error %q does not name the unregistered module", err)
	}
}

// TestClosureLeavesAnUndeliveredRequirementToTheDrive pins where a missing provider is reported.
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

// TestClosureFeedsTheParticipantSet pins the division of labour: the command tree expands the
// dependencies and hands the drive the literal result.
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

// TestClosureLeavesOutAnOptionalRequirementsProviders pins that an optional requirement does not
// pull its providers into the participating set.
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

// TestClosureLeavesOutAnExclusiveProvider pins the exception to expansion.
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

// TestClosureOfAConsumerOfAnExclusiveCapabilityIsItself checks that an exclusive provider is not
// pulled into the closure of its consumer.
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

func providing(m core.Module, tokens ...core.Token) core.Module {
	for _, token := range tokens {
		m.Provides = append(m.Provides, core.Provision{Token: token})
	}
	return m
}

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

func rival(p *probe, name string, exclusive bool, state core.EnablementState) core.Module {
	m := preparing(p, name)
	m.Provides = []core.Provision{{Token: (*Cache)(nil), Exclusive: exclusive}}
	m.Prepare = func(context.Context, *core.Registry) (core.Enablement, error) {
		p.record("prepare %s", name)
		return core.Enablement{State: state}, nil
	}
	return m
}

// TestASuppliedModuleTakesPartWithoutBeingNamed checks that a supplied module runs whatever the
// participant list says, while an unlisted unsupplied module runs nothing.
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

// TestADriveLeavesTheListOfParticipantsAsTheCallerWroteIt pins that adding the supplied modules to
// the participants does not write into the list the caller gave.
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

// TestASuppliedModuleNeverRunsItsPrepareOrNew checks that a supplied module's failing or disabling
// Prepare and its New never run, and its stance is StateEnabled.
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

// TestEveryCallbackOfASuppliedModuleReceivesTheSuppliedInstance checks that every stage callback of
// a supplied module is handed the supplied instance.
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

// TestADepthStopsASuppliedModuleLikeAnyOther checks that a depth stops a supplied module after the
// same stages as a constructed one, with Stop and Close always running.
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

// TestASuppliedModuleIsInEverySetAndStatesEnabled pins what the queries say of a supplied module.
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

// TestANameAtDeclarationLevelDoesNotMakeASuppliedModuleDeclarationOnly checks that a supplied module
// named for declarations alone still participates, and AllResourcesInForce does not apply to it.
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

// TestASuppliedProviderMeetsARivalUnderTheOrdinaryResolutionRules pins that a supplied module has
// no rules of its own in resolution beyond its fixed stance.
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

// TestAnInstanceSuppliedToOneProviderIsTheOnlyOneUntilTheOthersAreConstructed checks that a supplied
// instance answers alone before construction and the lookup turns ambiguous after.
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

// TestARequirerFollowsASuppliedModuleAndWhatItRequiresPrecedesIt checks that stages run in
// dependency order and Stop and Close in reverse, with a supplied module in the middle.
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

// TestSuppliedModulesAreOrderedByWhatTheyRequireOfEachOther checks that supplied modules still run in
// dependency order and are released in reverse.
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

// TestASuppliedModulesRequirementsAreNotPulledIn pins that the registry adds the supplied module
// to the participating set and nothing more.
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

// TestClosureTreatsASuppliedModuleLikeAnyOther checks that the closure expands a supplied provider
// and a supplied requirer exactly as it does constructed ones.
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

// TestASuppliedExclusiveProviderMeetsAConsumerThatNamesNoImplementation pins the supplied way out
// of a capability whose providers all claim it exclusively.
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

// TestRunTakesInEveryRegisteredModuleAtRunLevel checks that Run counts every registered module as
// both participating and declaring, with no declaration-level adjustment in force.
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
