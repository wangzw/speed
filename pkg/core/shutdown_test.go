package core_test

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/vislake/speed/pkg/core"
)

// Intake is the capability an entry point in these tests delivers: the seat its registrants put a
// handler into, the way a router takes routes.
type Intake interface {
	// Handle records the instance it is handed.
	Handle(h func(context.Context) error)
}

var errNotAccepting = errors.New("the worker is not accepting work")

type entryPoint struct {
	mu      sync.Mutex
	handler func(context.Context) error
	served  bool
	release chan struct{}
	drained chan error
}

func newEntryPoint() *entryPoint {
	return &entryPoint{release: make(chan struct{}), drained: make(chan error, 1)}
}

func (e *entryPoint) Handle(h func(context.Context) error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.handler = h
}

type worker struct {
	mu      sync.Mutex
	stopped bool
}

func (w *worker) do() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.stopped {
		return errNotAccepting
	}
	return nil
}

func (w *worker) stop() {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.stopped = true
}

func entryModule(p *probe, name string) core.Module {
	return core.Module{
		Name:     name,
		Provides: []core.Provision{{Token: (*Intake)(nil)}},
		New: func(context.Context, *core.Registry) (any, error) {
			p.record("new %s", name)
			return newEntryPoint(), nil
		},
		Serve: func(ctx context.Context, _ *core.Registry, instance any) error {
			e := instance.(*entryPoint)
			e.mu.Lock()
			handler := e.handler
			e.served = true
			e.mu.Unlock()
			go func() {
				<-e.release
				e.drained <- handler(context.WithoutCancel(ctx))
			}()
			p.record("serve %s", name)
			return nil
		},
		Stop: func(_ context.Context, _ *core.Registry, instance any) error {
			e := instance.(*entryPoint)
			p.record("stop %s", name)
			e.mu.Lock()
			served := e.served
			e.mu.Unlock()
			if !served {
				return nil
			}
			close(e.release)
			if err := <-e.drained; err != nil {
				p.record("in-flight request failed: %v", err)
				return nil
			}
			p.record("in-flight request completed")
			return nil
		},
	}
}

func registerEntry(t *testing.T, reg *core.Registry, p *probe, name string, supplied bool) {
	t.Helper()
	register(reg, entryModule(p, name))
	if supplied {
		reg.Supply(newEntryPoint(), name)
	}
}

func assertEntryOrigin(t *testing.T, p *probe, name string, supplied bool) {
	t.Helper()
	built := p.has("new " + name)
	if supplied && built {
		t.Fatalf("New of %s ran for an entry point whose instance the host supplied", name)
	}
	if !supplied && !built {
		t.Fatalf("New of %s did not run for an entry point the drive constructs", name)
	}
}

func workerModule(p *probe, name string, requires []core.Requirement) core.Module {
	return core.Module{
		Name:     name,
		Requires: requires,
		New: func(context.Context, *core.Registry) (any, error) {
			return &worker{}, nil
		},
		Init: func(_ context.Context, reg *core.Registry, instance any) error {
			w := instance.(*worker)
			intake, err := core.Resolve[Intake](reg)
			if err != nil {
				return err
			}
			intake.Handle(func(context.Context) error { return w.do() })
			return nil
		},
		Stop: func(_ context.Context, _ *core.Registry, instance any) error {
			instance.(*worker).stop()
			p.record("stop %s", name)
			return nil
		},
	}
}

// TestAnEntryPointDrainsBeforeAnyOtherModuleStops checks that an in-flight request completes before
// a worker stops, for a constructed or supplied entry point and any worker placement.
func TestAnEntryPointDrainsBeforeAnyOtherModuleStops(t *testing.T) {
	cases := []struct {
		name     string
		worker   string
		requires []core.Requirement
	}{
		{"constructed after it by requiring it", "worker", []core.Requirement{{Token: (*Intake)(nil)}}},
		{"constructed after it by nothing but the name", "worker", nil},
		{"constructed before it by nothing but the name", "a-worker", nil},
	}
	for _, supplied := range []bool{false, true} {
		origin := "an entry point the drive constructs"
		if supplied {
			origin = "an entry point the host supplies"
		}
		t.Run(origin, func(t *testing.T) {
			for _, c := range cases {
				t.Run(c.name, func(t *testing.T) {
					p := &probe{}
					reg := core.New()
					registerEntry(t, reg, p, "entry", supplied)
					register(reg, workerModule(p, c.worker, c.requires))

					if err := runUntilServed(t, reg, p, "serve entry"); err != nil {
						t.Fatalf("Run returned %v", err)
					}
					assertEntryOrigin(t, p, "entry", supplied)
					if !p.has("in-flight request completed") {
						t.Fatalf("the request in flight at shutdown did not complete; recorded %v",
							p.snapshot())
					}
					if p.indexOf(t, "stop "+c.worker) < p.indexOf(t, "in-flight request completed") {
						t.Fatalf("the worker was stopped before the entry point drained; recorded %v",
							p.snapshot())
					}
				})
			}
		})
	}
}

// TestEveryWayOutOfADriveStopsTheEntryPointsFirst pins that the shutdown order belongs to the
// drive and not to its ordinary end alone.
func TestEveryWayOutOfADriveStopsTheEntryPointsFirst(t *testing.T) {
	failure := errors.New("the drive could not go on")
	afterEntry := []core.Requirement{{Token: (*Intake)(nil)}}

	cases := []struct {
		name     string
		admitted bool
		drive    func(t *testing.T, p *probe, supplied bool) error
	}{
		{
			name:     "a later entry point's Serve fails",
			admitted: true,
			drive: func(t *testing.T, p *probe, supplied bool) error {
				reg := core.New()
				registerEntry(t, reg, p, "entry", supplied)
				register(reg, workerModule(p, "worker", afterEntry), core.Module{
					Name:     "late-entry",
					Requires: afterEntry,
					Serve: func(context.Context, *core.Registry, any) error {
						return failure
					},
				})
				return runToFailure(t, reg)
			},
		},
		{
			name:     "the action fails",
			admitted: true,
			drive: func(t *testing.T, p *probe, supplied bool) error {
				reg := core.New()
				registerEntry(t, reg, p, "entry", supplied)
				register(reg, workerModule(p, "worker", afterEntry))
				return driveToEnd(t, reg, core.Drive{
					Depth:        core.StageServe,
					Participants: []string{"entry", "worker"},
					Action: func(context.Context, *core.Registry) error {
						return failure
					},
				})
			},
		},
		{
			name: "a Start fails before anything is served",
			drive: func(t *testing.T, p *probe, supplied bool) error {
				reg := core.New()
				registerEntry(t, reg, p, "entry", supplied)
				register(reg, workerModule(p, "worker", afterEntry), core.Module{
					Name: "starter",
					Start: func(context.Context, *core.Registry, any) error {
						return failure
					},
				})
				return runToFailure(t, reg)
			},
		},
	}
	for _, supplied := range []bool{false, true} {
		origin := "an entry point the drive constructs"
		if supplied {
			origin = "an entry point the host supplies"
		}
		t.Run(origin, func(t *testing.T) {
			for _, c := range cases {
				t.Run(c.name, func(t *testing.T) {
					p := &probe{}
					if err := c.drive(t, p, supplied); !errors.Is(err, failure) {
						t.Fatalf("the drive returned %v, want the failure that ended it", err)
					}
					assertEntryOrigin(t, p, "entry", supplied)
					if p.indexOf(t, "stop worker") < p.indexOf(t, "stop entry") {
						t.Fatalf("the worker was stopped before the entry point; recorded %v", p.snapshot())
					}
					if !c.admitted {
						return
					}
					if !p.has("in-flight request completed") {
						t.Fatalf("the request in flight when the drive failed did not complete; recorded %v",
							p.snapshot())
					}
					if p.indexOf(t, "stop worker") < p.indexOf(t, "in-flight request completed") {
						t.Fatalf("the worker was stopped before the entry point drained; recorded %v",
							p.snapshot())
					}
				})
			}
		})
	}
}

// TestSuppliedInstancesNobodyConstructedStillStopEntryPointsFirst pins the order for a drive that
// ends before it constructs anything.
func TestSuppliedInstancesNobodyConstructedStillStopEntryPointsFirst(t *testing.T) {
	failure := errors.New("a stance could not be stated")
	p := &probe{}
	reg := core.New()
	registerEntry(t, reg, p, "entry", true)
	register(reg,
		workerModule(p, "worker", []core.Requirement{{Token: (*Intake)(nil)}}),
		core.Module{
			Name: "failing",
			Prepare: func(context.Context, *core.Registry) (core.Enablement, error) {
				return core.Enablement{}, failure
			},
		},
	)
	reg.Supply(&worker{}, "worker")

	err := driveToEnd(t, reg, core.Drive{
		Depth:        core.StageServe,
		Participants: []string{"failing"},
		Action:       noAction,
	})
	if !errors.Is(err, failure) {
		t.Fatalf("Drive returned %v, want the failure of the stance", err)
	}
	if p.indexOf(t, "stop worker") < p.indexOf(t, "stop entry") {
		t.Fatalf("the worker was stopped before the entry point; recorded %v", p.snapshot())
	}
}

func failingAt(p *probe, name string, stage core.Stage, failure error) core.Module {
	m := preparing(p, name)
	switch stage {
	case core.StagePrepare:
		m.Prepare = func(context.Context, *core.Registry) (core.Enablement, error) {
			return core.Enablement{}, failure
		}
	case core.StageNew:
		m.New = func(context.Context, *core.Registry) (any, error) { return nil, failure }
	case core.StageMigrate:
		m.Migrate = func(context.Context, *core.Registry, any) error { return failure }
	case core.StageInit:
		m.Init = func(context.Context, *core.Registry, any) error { return failure }
	case core.StageStart:
		m.Start = func(context.Context, *core.Registry, any) error { return failure }
	case core.StageServe:
		m.Serve = func(context.Context, *core.Registry, any) error { return failure }
	}
	return m
}

// TestEveryWayOutOfADriveReleasesASuppliedInstanceOnce pins that a supplied instance is released
// whichever way the drive ends.
func TestEveryWayOutOfADriveReleasesASuppliedInstanceOnce(t *testing.T) {
	failure := errors.New("the drive could not go on")
	type scenario struct {
		name    string
		wantErr error
		refuses bool
		drive   func(t *testing.T, p *probe, reg *core.Registry) error
	}
	cases := []scenario{
		{name: "a run that is cancelled", drive: func(t *testing.T, p *probe, reg *core.Registry) error {
			return runUntilServed(t, reg, p, "serve held")
		}},
		{name: "a drive that stops at the stances", drive: func(t *testing.T, _ *probe, reg *core.Registry) error {
			return driveToEnd(t, reg, core.Drive{})
		}},
		{
			name:    "an exclusive conflict",
			wantErr: core.ErrExclusiveViolated,
			drive: func(t *testing.T, p *probe, reg *core.Registry) error {
				register(reg, rival(p, "backend", true, core.StateEnabled))
				return driveToEnd(t, reg, core.Drive{
					Depth: core.StageServe, Participants: []string{"backend"}, Action: noAction,
				})
			},
		},
		{
			name:    "a requirement nothing provides",
			wantErr: core.ErrMissingProvider,
			drive: func(t *testing.T, p *probe, reg *core.Registry) error {
				consumer := preparing(p, "consumer")
				consumer.Requires = []core.Requirement{{Token: (*Greeter)(nil)}}
				register(reg, consumer)
				return driveToEnd(t, reg, core.Drive{
					Depth: core.StageServe, Participants: []string{"consumer"}, Action: noAction,
				})
			},
		},
		{
			name:    "a dependency cycle",
			wantErr: core.ErrDependencyCycle,
			drive: func(t *testing.T, p *probe, reg *core.Registry) error {
				left := providing(preparing(p, "loop-a"), (*Store)(nil))
				left.Requires = []core.Requirement{{Token: (*Greeter)(nil)}}
				right := providing(preparing(p, "loop-b"), (*Greeter)(nil))
				right.Requires = []core.Requirement{{Token: (*Store)(nil)}}
				register(reg, left, right)
				return driveToEnd(t, reg, core.Drive{
					Depth: core.StageServe, Participants: []string{"loop-a", "loop-b"}, Action: noAction,
				})
			},
		},
		{
			name:    "a participant no module registered",
			wantErr: core.ErrUnknownParticipant,
			drive: func(t *testing.T, _ *probe, reg *core.Registry) error {
				return driveToEnd(t, reg, core.Drive{
					Depth: core.StageServe, Participants: []string{"ghost"}, Action: noAction,
				})
			},
		},
		{
			name:    "a declaration-level name no module registered",
			wantErr: core.ErrUnknownParticipant,
			drive: func(t *testing.T, _ *probe, reg *core.Registry) error {
				return driveToEnd(t, reg, core.Drive{
					Depth: core.StageServe, DeclaringOnly: []core.Declaration{{Name: "ghost"}}, Action: noAction,
				})
			},
		},
		{
			name:    "an action that fails",
			wantErr: failure,
			drive: func(t *testing.T, _ *probe, reg *core.Registry) error {
				return driveToEnd(t, reg, core.Drive{
					Depth:  core.StageInit,
					Action: func(context.Context, *core.Registry) error { return failure },
				})
			},
		},
		{
			name:    "the supplied module's own Migrate refusing the instance",
			wantErr: failure,
			refuses: true,
			drive: func(t *testing.T, _ *probe, reg *core.Registry) error {
				return driveToEnd(t, reg, core.Drive{Depth: core.StageServe, Action: noAction})
			},
		},
	}
	for _, stage := range []core.Stage{
		core.StagePrepare, core.StageNew, core.StageMigrate, core.StageInit, core.StageStart, core.StageServe,
	} {
		cases = append(cases, scenario{
			name:    "a " + stage.String() + " that fails",
			wantErr: failure,
			drive: func(t *testing.T, p *probe, reg *core.Registry) error {
				register(reg, failingAt(p, "broken", stage, failure))
				return driveToEnd(t, reg, core.Drive{
					Depth: core.StageServe, Participants: []string{"broken"}, Action: noAction,
				})
			},
		})
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			p := &probe{}
			reg := core.New()
			handed := &box{id: "held"}
			held := providing(preparing(p, "held"), (*Cache)(nil))
			if c.refuses {
				held.Migrate = func(context.Context, *core.Registry, any) error { return failure }
			}
			register(reg, receiving(t, held, handed))
			reg.Supply(handed, "held")

			err := c.drive(t, p, reg)

			if c.wantErr == nil && err != nil {
				t.Fatalf("the drive returned %v, want nil", err)
			}
			if c.wantErr != nil && !errors.Is(err, c.wantErr) {
				t.Fatalf("the drive returned %v, want %v", err, c.wantErr)
			}
			for _, want := range []string{"stop held", "close held"} {
				if got := p.count(want); got != 1 {
					t.Fatalf("%q was recorded %d times, want once; recorded %v", want, got, p.snapshot())
				}
			}
			if p.indexOf(t, "stop held") > p.indexOf(t, "close held") {
				t.Fatalf("the supplied instance was closed before it was stopped: %v", p.snapshot())
			}
		})
	}
}

// TestSuppliedModulesThatRequireEachOtherAreStillReleased pins the instance on a dependency cycle.
func TestSuppliedModulesThatRequireEachOtherAreStillReleased(t *testing.T) {
	for _, c := range []struct {
		name    string
		depth   core.Stage
		wantErr error
	}{
		{"a drive that plans its dependencies", core.StageServe, core.ErrDependencyCycle},
		{"a drive that ends at the stances", core.StagePrepare, nil},
	} {
		t.Run(c.name, func(t *testing.T) {
			p := &probe{}
			reg := core.New()
			left := providing(preparing(p, "left"), (*Cache)(nil))
			left.Requires = []core.Requirement{{Token: (*Store)(nil)}}
			right := providing(preparing(p, "right"), (*Store)(nil))
			right.Requires = []core.Requirement{{Token: (*Cache)(nil)}}
			register(reg, left, right)
			reg.Supply(&box{id: "left"}, "left")
			reg.Supply(&box{id: "right"}, "right")

			err := driveToEnd(t, reg, core.Drive{Depth: c.depth, Action: noAction})

			if c.wantErr == nil && err != nil {
				t.Fatalf("Drive returned %v, want nil", err)
			}
			if c.wantErr != nil && !errors.Is(err, c.wantErr) {
				t.Fatalf("Drive returned %v, want %v", err, c.wantErr)
			}
			for _, want := range []string{"stop left", "close left", "stop right", "close right"} {
				if got := p.count(want); got != 1 {
					t.Errorf("%q was recorded %d times, want once; recorded %v", want, got, p.snapshot())
				}
			}
		})
	}
}

// TestSuppliedInstancesNobodyConstructedAreReleasedInDependencyOrder checks that a required supplied
// instance is stopped and closed before the instance that requires it.
func TestSuppliedInstancesNobodyConstructedAreReleasedInDependencyOrder(t *testing.T) {
	failure := errors.New("a stance could not be stated")
	for _, c := range []struct {
		name    string
		depth   core.Stage
		failing bool
	}{
		{"a stance that fails", core.StageServe, true},
		{"a drive that ends at the stances", core.StagePrepare, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			p := &probe{}
			reg := core.New()
			user := preparing(p, "aa-user")
			user.Requires = []core.Requirement{{Token: (*Cache)(nil)}}
			register(reg, providing(preparing(p, "zz-base"), (*Cache)(nil)), user)
			reg.Supply(&box{id: "base"}, "zz-base")
			reg.Supply(&box{id: "user"}, "aa-user")
			d := core.Drive{Depth: c.depth, Action: noAction}
			if c.failing {
				register(reg, core.Module{
					Name: "failing",
					Prepare: func(context.Context, *core.Registry) (core.Enablement, error) {
						return core.Enablement{}, failure
					},
				})
				d.Participants = []string{"failing"}
			}

			err := driveToEnd(t, reg, d)

			if c.failing && !errors.Is(err, failure) {
				t.Fatalf("Drive returned %v, want the failure of the stance", err)
			}
			if !c.failing && err != nil {
				t.Fatalf("Drive returned %v, want nil", err)
			}
			for _, stage := range []string{"stop", "close"} {
				if p.indexOf(t, stage+" aa-user") > p.indexOf(t, stage+" zz-base") {
					t.Errorf("%s released the module that is required before the one that requires it: %v",
						stage, p.snapshot())
				}
			}
		})
	}
}
