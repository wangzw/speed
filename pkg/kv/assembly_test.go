package kv_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/vislake/speed/pkg/config"
	"github.com/vislake/speed/pkg/core"
	"github.com/vislake/speed/pkg/kv"
	"github.com/vislake/speed/pkg/kv/memory"
	"github.com/vislake/speed/pkg/kv/redis"
)

// noConfig is the reading face for an assembly whose configuration declares
// nothing. Every implementation then reads the defaults its prototype carries.
type noConfig struct{}

func (noConfig) Decode(string, any) error { return nil }

// enabledProvider stands in for a backend a host pointed at: a second provider
// of the capability that states StateEnabled. Its constructor is never reached
// at the depth these cases drive to.
type enabledProvider struct{ name string }

func (p enabledProvider) Module() core.Module {
	cfg := struct{}{}
	return kv.NewModule(kv.Spec[struct{}]{
		ModuleName:      p.name,
		ConfigNamespace: p.name,
		Config:          &cfg,
		Stance: func(*struct{}) (core.Enablement, error) {
			return core.Enablement{State: core.StateEnabled}, nil
		},
		Open: func(context.Context, *struct{}) (kv.KV, error) {
			return nil, errors.New("this provider is never constructed at the depth under test")
		},
	})
}

// suppliedProvider stands in for a backend whose instance the host supplies. The
// instance takes the place of what its stance would state and its constructor
// would build, so both fail on purpose: a drive that took the stance or ran the
// constructor stops on that failure.
type suppliedProvider struct{ name string }

func (p suppliedProvider) Module() core.Module {
	cfg := struct{}{}
	return kv.NewModule(kv.Spec[struct{}]{
		ModuleName:      p.name,
		ConfigNamespace: p.name,
		Config:          &cfg,
		Stance: func(*struct{}) (core.Enablement, error) {
			return core.Enablement{}, errors.New("the stance of a module whose instance the host supplied was taken")
		},
		Open: func(context.Context, *struct{}) (kv.KV, error) {
			return nil, errors.New("a module whose instance the host supplied constructed its own")
		},
	})
}

// stubKV is the instance a host supplies for a backend's module. It embeds the
// interface to deliver the capability without implementing an operation: no case
// here calls one. It has no Close, so it holds nothing this package can release.
type stubKV struct{ kv.KV }

// closingKV is a supplied instance with a Close. It counts each Close it is asked
// for into closed, which is how a case sees the shutdown release it.
type closingKV struct {
	kv.KV
	closed *int
}

func (c closingKV) Close(context.Context) error {
	*c.closed++
	return nil
}

// prepare drives a fresh assembly to its stances and hands the registry back.
func prepare(t *testing.T, participants ...string) (*core.Registry, error) {
	t.Helper()
	reg := core.New()
	reg.Register(config.Module())
	reg.Supply(noConfig{}, config.ModuleName)
	reg.Register(memory.Module())
	reg.Register(enabledProvider{name: "kv.other-backend"}.Module())
	return reg, reg.Drive(context.Background(), core.Drive{
		Depth:        core.StagePrepare,
		Participants: participants,
	})
}

// TestTheInMemoryImplementationStandsAloneWhenNothingElseRuns pins the half of
// its stance that makes it a usable default: StateAuto is enabled unless
// something contests it, so a host that imports it and configures nothing gets
// a working capability.
func TestTheInMemoryImplementationStandsAloneWhenNothingElseRuns(t *testing.T) {
	reg, err := prepare(t, "kv.memory")
	if err != nil {
		t.Fatalf("drive: %v", err)
	}
	got, ok := reg.Enablement("kv.memory")
	if !ok {
		t.Fatal("the registry has no verdict for kv.memory")
	}
	if got.State != core.StateEnabled {
		t.Fatalf("kv.memory is %v, want enabled: nothing contested the capability", got.State)
	}
}

// TestTheInMemoryImplementationStandsDownForAConfiguredBackend pins the other
// half. This is the whole reason it claims the capability exclusively while
// stating StateAuto: a host that imports it as a fallback and then configures a
// real backend must get that backend, not a startup failure. Naming that
// backend in the command tree is part of the premise rather than a detail of
// the case: an exclusive provider the command tree leaves out, with no instance
// supplied for it, enters no participant set, so this case would have nothing to
// stand down for.
func TestTheInMemoryImplementationStandsDownForAConfiguredBackend(t *testing.T) {
	reg, err := prepare(t, "kv.memory", "kv.other-backend")
	if err != nil {
		t.Fatalf("drive: %v", err)
	}
	stood, _ := reg.Enablement("kv.memory")
	if stood.State != core.StateDisabled {
		t.Fatalf("kv.memory is %v beside an explicitly enabled provider, want disabled", stood.State)
	}
	if stood.Reason == "" {
		t.Fatal("the stand-down carries no reason, and the reason reaches the startup diagnostics")
	}
	won, _ := reg.Enablement("kv.other-backend")
	if won.State != core.StateEnabled {
		t.Fatalf("kv.other-backend is %v, want enabled", won.State)
	}
}

// TestTheInMemoryImplementationStandsDownForASuppliedBackend pins the other way
// a backend gets into the assembly: the host supplies its instance instead of
// pointing the command at it. The command tree names only the in-memory
// implementation, and the supplied backend takes part all the same with its
// stance fixed at StateEnabled, so the in-memory implementation yields to it
// exactly as it does to a named one.
//
// The supplied backend's own stance and constructor both fail, and the drive goes
// as far as construction: one that took the stance, or ran the constructor
// instead of using the supplied instance, stops on that error.
func TestTheInMemoryImplementationStandsDownForASuppliedBackend(t *testing.T) {
	const supplied = "kv.supplied-backend"
	reg := core.New()
	reg.Register(config.Module())
	reg.Supply(noConfig{}, config.ModuleName)
	reg.Register(memory.Module())
	reg.Register(suppliedProvider{name: supplied}.Module())
	reg.Supply(stubKV{}, supplied)
	err := reg.Drive(context.Background(), core.Drive{
		Depth:        core.StageNew,
		Participants: []string{memory.ModuleName},
	})
	if err != nil {
		t.Fatalf("drive: %v", err)
	}
	if !reg.Participating(supplied) {
		t.Fatalf("%s is outside the participating set, though the host supplied its instance and "+
			"the command tree did not need to name it", supplied)
	}
	won, _ := reg.Enablement(supplied)
	if won.State != core.StateEnabled {
		t.Fatalf("%s is %v, want enabled: a supplied instance states no stance, it is enabled", supplied, won.State)
	}
	stood, _ := reg.Enablement(memory.ModuleName)
	if stood.State != core.StateDisabled {
		t.Fatalf("kv.memory is %v beside a supplied backend, want disabled", stood.State)
	}
	if stood.Reason == "" {
		t.Fatal("the stand-down carries no reason, and the reason reaches the startup diagnostics")
	}
}

// TestASuppliedBackendIsClosedWhenItHasSomethingToClose pins what the shutdown
// does with the instance a host supplied for an implementation: the module's
// Close runs on it as it does on one the constructor built, and closes it. It
// runs at two depths. A drive to Prepare constructs nothing, so the shutdown
// releases the instance as though it had been built last; a drive to New reaches
// the module in construction order and releases it there.
//
// No reading face is supplied and none is needed: a supplied backend runs
// neither the Prepare nor the New that read configuration, so a drive that
// reached either would stop on the missing reader.
func TestASuppliedBackendIsClosedWhenItHasSomethingToClose(t *testing.T) {
	for _, depth := range []core.Stage{core.StagePrepare, core.StageNew} {
		t.Run(depth.String(), func(t *testing.T) {
			closed := 0
			reg := core.New()
			reg.Register(memory.Module())
			reg.Supply(closingKV{closed: &closed}, memory.ModuleName)
			if err := reg.Drive(context.Background(), core.Drive{Depth: depth}); err != nil {
				t.Fatalf("drive: %v", err)
			}
			if closed != 1 {
				t.Fatalf("the supplied instance was closed %d times, want once: the module's Close is "+
					"what releases it", closed)
			}
		})
	}
}

// TestASuppliedBackendWithNothingToCloseIsLeftAlone pins the other shape of a
// supplied instance. One without a Close(ctx) holds nothing this package can
// release, so the shutdown reports no error for it: what it holds is the
// supplying host's to release.
func TestASuppliedBackendWithNothingToCloseIsLeftAlone(t *testing.T) {
	for _, depth := range []core.Stage{core.StagePrepare, core.StageNew} {
		t.Run(depth.String(), func(t *testing.T) {
			reg := core.New()
			reg.Register(memory.Module())
			reg.Supply(stubKV{}, memory.ModuleName)
			if err := reg.Drive(context.Background(), core.Drive{Depth: depth}); err != nil {
				t.Fatalf("drive: %v", err)
			}
		})
	}
}

// TestAnImplementationWithoutItsInputFailsTheStartup pins the rule a module
// named in the command tree lives under: the name is a commitment to run it, so
// one that was pointed at nothing stops the startup and says which input was
// missing, rather than standing itself down and leaving the process up with the
// capability silently absent.
//
// It drives a real implementation rather than a test double, because what is
// being pinned is that an implementation of this capability carries the rule
// through: the constructor an implementation hands this package is where its
// stance is read, and the error has to survive that wiring.
func TestAnImplementationWithoutItsInputFailsTheStartup(t *testing.T) {
	reg := core.New()
	reg.Register(config.Module())
	reg.Supply(noConfig{}, config.ModuleName)
	reg.Register(redis.Module())
	err := reg.Drive(context.Background(), core.Drive{
		Depth:        core.StagePrepare,
		Participants: []string{redis.ModuleName},
	})
	if err == nil {
		t.Fatal("an implementation named in the command tree and configured with nothing started " +
			"anyway, so the command tree is not a reliable statement of what this run has")
	}
	if want := redis.ConfigNamespace + ".addr"; !strings.Contains(err.Error(), want) {
		t.Fatalf("the startup stopped without naming the input it stopped on, want %q: %v", want, err)
	}
}

// TestTwoConfiguredBackendsFailTheStartup pins that the capability is delivered
// exclusively: two backends a host pointed at once cannot be resolved to one,
// and discovering that at the first write would be too late to tell the host
// that half its data went somewhere it did not name.
func TestTwoConfiguredBackendsFailTheStartup(t *testing.T) {
	reg := core.New()
	reg.Register(config.Module())
	reg.Supply(noConfig{}, config.ModuleName)
	reg.Register(enabledProvider{name: "kv.backend-a"}.Module())
	reg.Register(enabledProvider{name: "kv.backend-b"}.Module())
	err := reg.Drive(context.Background(), core.Drive{
		Depth:        core.StagePrepare,
		Participants: []string{"kv.backend-a", "kv.backend-b"},
	})
	if !errors.Is(err, core.ErrExclusiveViolated) {
		t.Fatalf("driving two configured providers: %v, want ErrExclusiveViolated", err)
	}
}
