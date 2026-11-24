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

type noConfig struct{}

func (noConfig) Decode(string, any) error { return nil }

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

type stubKV struct{ kv.KV }

type closingKV struct {
	kv.KV
	closed *int
}

func (c closingKV) Close(context.Context) error {
	*c.closed++
	return nil
}

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

// TestTheInMemoryImplementationStandsAloneWhenNothingElseRuns checks that the in-memory
// implementation is enabled when it is the only provider.
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

// TestTheInMemoryImplementationStandsDownForAConfiguredBackend pins the other half.
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

// TestTheInMemoryImplementationStandsDownForASuppliedBackend pins the other way a backend gets
// into the assembly: the host supplies its instance instead of pointing the command at it.
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

// TestASuppliedBackendIsClosedWhenItHasSomethingToClose checks that a supplied backend implementing
// Close is closed exactly once, whether the drive stops at Prepare or at New.
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

// TestASuppliedBackendWithNothingToCloseIsLeftAlone pins the other shape of a supplied instance.
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

// TestAnImplementationWithoutItsInputFailsTheStartup checks that a named implementation with no
// configuration fails the startup, naming its addr key.
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

// TestTwoConfiguredBackendsFailTheStartup checks that two explicitly enabled providers fail as
// ErrExclusiveViolated.
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
