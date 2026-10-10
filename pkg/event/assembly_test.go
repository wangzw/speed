package event_test

import (
	"context"
	"strings"
	"testing"

	"github.com/vislake/speed/pkg/config"
	"github.com/vislake/speed/pkg/core"
	"github.com/vislake/speed/pkg/event/memory"
	"github.com/vislake/speed/pkg/event/redis"
)

// declared is the reading face for an assembly whose configuration says one
// thing: how many replicas share the assembly's state. Each backend reads its own
// section through it, and a section this face does not answer for keeps the
// defaults the module's prototype carries.
type declared struct{ replicas int }

func (d declared) Decode(_ string, target any) error {
	if cfg, ok := target.(*memory.Config); ok {
		cfg.Replicas = d.replicas
	}
	return nil
}

// prepare drives an assembly of the named participants to its stances, which is
// as far as the stages need to go for a premise to be judged: Prepare is where a
// module that cannot start reports it, and nothing is constructed before it
// returns. The reading face is supplied as the instance of the config module,
// which takes part in the drive beside the named participants.
func prepare(t *testing.T, decl declared, participants ...string) (*core.Registry, error) {
	t.Helper()
	reg := core.New()
	reg.Register(config.Module())
	reg.Supply(decl, config.ModuleName)
	reg.Register(memory.Module())
	reg.Register(redis.Module())
	return reg, reg.Drive(context.Background(), core.Drive{
		Depth:        core.StagePrepare,
		Participants: participants,
	})
}

// TestAnUnmetDeploymentPreconditionFailsTheStartup pins the enablement rule for
// the premise this module carries: a module taken in at run level either starts
// or stops the startup with an error naming what was missing. Three replicas is a
// deployment the in-process bus cannot serve — a subscriber on one replica would
// never see an event published on another, while every publish still returned
// success — so reading it as a reason to run without an event bus is the silent
// loss the premise exists to prevent.
func TestAnUnmetDeploymentPreconditionFailsTheStartup(t *testing.T) {
	_, err := prepare(t, declared{replicas: 3}, memory.ModuleName)
	if err == nil {
		t.Fatal("an assembly of three replicas whose only event bus is the in-process one started")
	}
	if want := memory.ConfigNamespace + ".replicas"; !strings.Contains(err.Error(), want) {
		t.Fatalf("the startup failure does not name %s, so an operator cannot tell which input "+
			"stopped it: %v", want, err)
	}
}

// TestAnUnpointedBackendFailsTheStartup is the same rule at a backend whose
// premise is being pointed at a server. A missing address is not how an
// assembly says it does not want this backend: it stops the startup, and the
// way to say it is to leave the implementation out of the command tree.
func TestAnUnpointedBackendFailsTheStartup(t *testing.T) {
	_, err := prepare(t, declared{}, redis.ModuleName)
	if err == nil {
		t.Fatal("an assembly that named the Redis backend without an address started")
	}
	if want := redis.ConfigNamespace + ".addr"; !strings.Contains(err.Error(), want) {
		t.Fatalf("the startup failure does not name %s, so an operator cannot tell which input "+
			"stopped it: %v", want, err)
	}
}

// TestAPremiseThatHoldsStarts is the positive control: the same driver over the
// same reading face starts once the deployment suits the implementation. Without
// it the two cases above would also pass on a driver that fails everything.
func TestAPremiseThatHoldsStarts(t *testing.T) {
	reg, err := prepare(t, declared{replicas: 1}, memory.ModuleName)
	if err != nil {
		t.Fatalf("an assembly of one replica with the in-process bus failed the startup: %v", err)
	}
	got, ok := reg.Enablement(memory.ModuleName)
	if !ok {
		t.Fatal("the registry has no verdict for event.memory")
	}
	if got.State != core.StateEnabled {
		t.Fatalf("event.memory is %v, want enabled: it states StateAuto and nothing contested the "+
			"capability", got.State)
	}
}
