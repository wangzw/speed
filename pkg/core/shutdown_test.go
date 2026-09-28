package core_test

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/vislake/speed/pkg/core"
)

// Intake is the capability an entry point in these tests delivers: the seat
// its registrants put a handler into, the way a router takes routes.
type Intake interface {
	Handle(h func(context.Context) error)
}

// errNotAccepting is what a worker answers once its Stop has run.
var errNotAccepting = errors.New("the worker is not accepting work")

// entryPoint admits one request in Serve and keeps it in flight until its Stop
// begins; Stop then drains, returning only once that request's handler has.
// It is the shape an HTTP module's Stop has: close the listener, then wait out
// what is already inside. An entry point whose Serve never ran admitted
// nothing, and its Stop has nothing to wait for.
type entryPoint struct {
	mu      sync.Mutex
	handler func(context.Context) error
	served  bool
	release chan struct{}
	drained chan error
}

func (e *entryPoint) Handle(h func(context.Context) error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.handler = h
}

// worker refuses work once its Stop has run, the way a module that stops
// taking records or publications does.
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

// entryModule builds an entry point that records how the request it held in
// flight at shutdown came out.
func entryModule(p *probe, name string) core.Module {
	return core.Module{
		Name:     name,
		Provides: []core.Provision{{Token: (*Intake)(nil)}},
		New: func(context.Context, *core.Registry) (any, error) {
			return &entryPoint{release: make(chan struct{}), drained: make(chan error, 1)}, nil
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

// workerModule builds a module whose work an in-flight request reaches. It
// puts its handler into the entry point in Init, the way a registrant takes a
// router up, and refuses work from its Stop on.
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

// TestAnEntryPointDrainsBeforeAnyOtherModuleStops pins the order the shutdown
// owes a request in flight: the module its handler reaches is still taking
// work until the entry point has drained, whichever side of the entry point
// that module was constructed on.
//
// A worker that requires the entry point's capability is constructed after it,
// and so is one ordered by nothing whose name sorts later; reverse construction
// order alone would stop either of them first, and the request in flight would
// have its work refused. The worker whose name sorts earlier is constructed
// before the entry point, and is there to show that the side does not matter.
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
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			p := &probe{}
			reg := core.New()
			register(reg, entryModule(p, "entry"), workerModule(p, c.worker, c.requires))

			if err := runUntilServed(t, reg, p, "serve entry"); err != nil {
				t.Fatalf("Run returned %v", err)
			}
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
}

// TestEveryWayOutOfADriveStopsTheEntryPointsFirst pins that the shutdown order
// belongs to the drive and not to its ordinary end alone. A startup that fails
// part-way and an action that fails leave through the same shutdown as a
// cancelled run: the entry points are stopped before every other module, and a
// request an entry point admitted before the failure still has its work taken
// by the module it reaches.
//
// The worker requires the entry point's capability, so it is constructed after
// it, and reverse construction order alone would stop it first. The startup
// that fails in Start has served nothing, so there the order is all there is
// to see; it is there to show that the failing stage does not change it.
func TestEveryWayOutOfADriveStopsTheEntryPointsFirst(t *testing.T) {
	failure := errors.New("the drive could not go on")
	afterEntry := []core.Requirement{{Token: (*Intake)(nil)}}

	cases := []struct {
		name string
		// admitted says whether the entry point served before the failure,
		// which puts a request in flight when the shutdown begins.
		admitted bool
		drive    func(t *testing.T, p *probe) error
	}{
		{
			name:     "a later entry point's Serve fails",
			admitted: true,
			drive: func(t *testing.T, p *probe) error {
				reg := core.New()
				register(reg, entryModule(p, "entry"), workerModule(p, "worker", afterEntry), core.Module{
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
			drive: func(t *testing.T, p *probe) error {
				reg := core.New()
				register(reg, entryModule(p, "entry"), workerModule(p, "worker", afterEntry))
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
			drive: func(t *testing.T, p *probe) error {
				reg := core.New()
				register(reg, entryModule(p, "entry"), workerModule(p, "worker", afterEntry), core.Module{
					Name: "starter",
					Start: func(context.Context, *core.Registry, any) error {
						return failure
					},
				})
				return runToFailure(t, reg)
			},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			p := &probe{}
			if err := c.drive(t, p); !errors.Is(err, failure) {
				t.Fatalf("the drive returned %v, want the failure that ended it", err)
			}
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
}
