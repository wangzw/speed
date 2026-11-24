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

type declared struct{ replicas int }

func (d declared) Decode(_ string, target any) error {
	if cfg, ok := target.(*memory.Config); ok {
		cfg.Replicas = d.replicas
	}
	return nil
}

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

// TestAnUnmetDeploymentPreconditionFailsTheStartup pins the enablement
// rule for the premise this module carries.
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

// TestAnUnpointedBackendFailsTheStartup is the same rule at a backend
// whose premise is being pointed at a server.
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

// TestAPremiseThatHoldsStarts is the positive control.
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
