package kv_test

import (
	"context"
	"errors"
	"testing"

	"github.com/vislake/speed/pkg/config"
	"github.com/vislake/speed/pkg/core"
	"github.com/vislake/speed/pkg/kv"
	"github.com/vislake/speed/pkg/kv/memory"
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
		Stance: func(*struct{}) core.Enablement {
			return core.Enablement{State: core.StateEnabled}
		},
		Open: func(context.Context, *struct{}) (kv.KV, error) {
			return nil, errors.New("this provider is never constructed at the depth under test")
		},
	})
}

// prepare drives a fresh assembly to its stances and hands the registry back.
func prepare(t *testing.T, participants ...string) (*core.Registry, error) {
	t.Helper()
	reg := core.New()
	reg.Supply((*config.Reader)(nil), noConfig{})
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
// real backend must get that backend, not a startup failure.
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

// TestTwoConfiguredBackendsFailTheStartup pins that the capability is delivered
// exclusively: two backends a host pointed at once cannot be resolved to one,
// and discovering that at the first write would be too late to tell the host
// that half its data went somewhere it did not name.
func TestTwoConfiguredBackendsFailTheStartup(t *testing.T) {
	reg := core.New()
	reg.Supply((*config.Reader)(nil), noConfig{})
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
