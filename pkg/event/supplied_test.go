package event_test

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/vislake/speed/pkg/config"
	"github.com/vislake/speed/pkg/core"
	"github.com/vislake/speed/pkg/event"
	"github.com/vislake/speed/pkg/event/memory"
	eventnats "github.com/vislake/speed/pkg/event/nats"
	eventpostgres "github.com/vislake/speed/pkg/event/postgres"
	eventredis "github.com/vislake/speed/pkg/event/redis"
)

// hostBus is a bus the host built itself, standing where the in-process
// implementation's own product would. It keeps the order its Stop and Close were
// asked for, which is how a case sees the drive reach it.
type hostBus struct {
	mu    sync.Mutex
	calls []string
}

func (*hostBus) Subscribe(string, event.Handler)            {}
func (*hostBus) Publish(context.Context, event.Event) error { return nil }

func (b *hostBus) Stop(context.Context) error  { b.record("stop"); return nil }
func (b *hostBus) Close(context.Context) error { b.record("close"); return nil }

func (b *hostBus) record(call string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.calls = append(b.calls, call)
}

func (b *hostBus) asked() []string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return slices.Clone(b.calls)
}

// bareHostBus is a bus the host built with no stop stage and nothing to close.
type bareHostBus struct{}

func (bareHostBus) Subscribe(string, event.Handler)            {}
func (bareHostBus) Publish(context.Context, event.Event) error { return nil }

// implementations are the modules that deliver the event capability. Each has a
// start function of its own, so a case about what the start stage does with a bus
// the host supplied is run over every one of them.
var implementations = []struct {
	name   string
	module func() core.Module
}{
	{memory.ModuleName, memory.Module},
	{eventredis.ModuleName, eventredis.Module},
	{eventpostgres.ModuleName, eventpostgres.Module},
	{eventnats.ModuleName, eventnats.Module},
}

// TestASuppliedBusIsRefusedAtTheStartStage pins where a bus the host supplied for
// an implementation stops being usable: the start stage freezes and starts the
// product its own Open builds, and a bus built elsewhere has none of that state,
// so a drive that carries it there fails while a drive that stops at Init does
// not. The refusal says what was refused and what to supply instead.
//
// Every implementation is held to it, and none needs its server: the supplied bus
// stands where the one Open would have connected, so nothing connects.
//
// No reading face is supplied. The module's Prepare and New, which read one, do
// not run for a supplied instance, so the shallow drive passing is also what shows
// they did not.
func TestASuppliedBusIsRefusedAtTheStartStage(t *testing.T) {
	depths := []struct {
		name  string
		depth core.Stage
		fails bool
	}{
		{"a drive that stops at init takes it", core.StageInit, false},
		{"a drive that reaches start refuses it", core.StageStart, true},
	}
	for _, impl := range implementations {
		t.Run(impl.name, func(t *testing.T) {
			for _, c := range depths {
				t.Run(c.name, func(t *testing.T) {
					reg := core.New()
					reg.Register(impl.module())
					reg.Supply(&hostBus{}, impl.name)
					err := reg.Drive(t.Context(), core.Drive{Depth: c.depth})
					if !c.fails {
						if err != nil {
							t.Fatalf("a drive to %v over a bus the host supplied for %s failed: %v",
								c.depth, impl.name, err)
						}
						return
					}
					if err == nil {
						t.Fatalf("a drive to %v over a bus the host supplied for %s succeeded, and a bus "+
							"that was never opened here was started as if it had been", c.depth, impl.name)
					}
					for _, want := range []string{impl.name, core.StageStart.String(), "hostBus"} {
						if !strings.Contains(err.Error(), want) {
							t.Errorf("the refusal does not name %q, so an operator cannot tell which module "+
								"refused which bus at which stage: %v", want, err)
						}
					}
					for _, want := range []string{fmt.Sprintf("the New of module %q", impl.name), "placeholder module"} {
						if !strings.Contains(err.Error(), want) {
							t.Errorf("the refusal does not say %q, so an operator is told what was refused "+
								"and not what to supply instead: %v", want, err)
						}
					}
				})
			}
		})
	}
}

// builtByNew is a bus the in-process implementation's own New builds in a
// registry of the host's making, which is one of the two things the refusal tells
// a host to supply instead.
func builtByNew(t *testing.T) event.Bus {
	t.Helper()
	reg := core.New()
	reg.Register(config.Module())
	reg.Supply(declared{replicas: 1}, config.ModuleName)
	built, err := memory.Module().New(t.Context(), reg)
	if err != nil {
		t.Fatalf("building a bus with %s's own New failed: %v", memory.ModuleName, err)
	}
	bus, ok := built.(event.Bus)
	if !ok {
		t.Fatalf("%s's own New built a %T, which is not a bus", memory.ModuleName, built)
	}
	return bus
}

// TestABusBuiltByTheModulesOwnNewIsTakenUpWhenSupplied backs the first way out the
// refusal names. The bus the module's own New built is supplied for the module in
// the registry that is driven, and the start stage takes it up: the drive goes as
// deep as Start and publishes from its action, which a bus the start stage had
// not opened to publishing would refuse.
func TestABusBuiltByTheModulesOwnNewIsTakenUpWhenSupplied(t *testing.T) {
	reg := core.New()
	reg.Register(memory.Module())
	reg.Supply(builtByNew(t), memory.ModuleName)
	err := reg.Drive(t.Context(), core.Drive{
		Depth: core.StageStart,
		Action: func(ctx context.Context, reg *core.Registry) error {
			bus, err := core.Resolve[event.Bus](reg)
			if err != nil {
				return err
			}
			return bus.Publish(ctx, event.Event{Type: "case.published"})
		},
	})
	if err != nil {
		t.Fatalf("a drive to start over a bus the module's own New built failed: %v", err)
	}
}

// TestAPlaceholderModuleCarriesAStandInThroughTheStartStage backs the other way
// out. The stand-in is supplied for a module that declares only the capability,
// which has no start stage to refuse it, and the drive goes past the stage at
// which an implementation's own module would have.
func TestAPlaceholderModuleCarriesAStandInThroughTheStartStage(t *testing.T) {
	const placeholder = "case.bus"
	stand := &hostBus{}
	reg := core.New()
	reg.Register(core.Module{Name: placeholder, Provides: []core.Provision{{Token: (*event.Bus)(nil)}}})
	reg.Supply(stand, placeholder)
	var taken event.Bus
	err := reg.Drive(t.Context(), core.Drive{
		Depth: core.StageStart,
		Action: func(_ context.Context, reg *core.Registry) error {
			var err error
			taken, err = core.Resolve[event.Bus](reg)
			return err
		},
	})
	if err != nil {
		t.Fatalf("a drive to start over a placeholder module carrying the stand-in failed: %v", err)
	}
	if taken != event.Bus(stand) {
		t.Errorf("the action took up %v, want the stand-in that was supplied", taken)
	}
}

// TestASuppliedBusIsStoppedAndClosedByTheDrive pins that the stop and close
// stages treat a bus the host supplied as they treat the one Open builds: through
// the methods it has, Stop before Close.
func TestASuppliedBusIsStoppedAndClosedByTheDrive(t *testing.T) {
	supplied := &hostBus{}
	reg := core.New()
	reg.Register(memory.Module())
	reg.Supply(supplied, memory.ModuleName)
	if err := reg.Drive(t.Context(), core.Drive{Depth: core.StageInit}); err != nil {
		t.Fatalf("a drive to init over a bus the host supplied for %s failed: %v", memory.ModuleName, err)
	}
	want := []string{"stop", "close"}
	if got := supplied.asked(); !slices.Equal(got, want) {
		t.Fatalf("the drive asked the supplied bus for %v, want %v: a bus the host built is released "+
			"through the methods it has, or nothing releases it", got, want)
	}
}

// TestASuppliedBusWithNoStopOrCloseIsLeftAlone is the other half: a bus that has
// neither method is not an error at the stop and close stages, and the drive
// that carried it ends cleanly.
func TestASuppliedBusWithNoStopOrCloseIsLeftAlone(t *testing.T) {
	reg := core.New()
	reg.Register(memory.Module())
	reg.Supply(bareHostBus{}, memory.ModuleName)
	if err := reg.Drive(t.Context(), core.Drive{Depth: core.StageInit}); err != nil {
		t.Fatalf("a drive over a bus with no stop stage and nothing to close failed: %v", err)
	}
}
