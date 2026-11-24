package kv_test

import (
	"context"
	"testing"

	"github.com/vislake/speed/pkg/core"
	"github.com/vislake/speed/pkg/kv"
	"github.com/vislake/speed/pkg/log"
)

// TestTheModuleRequiresTheLogCapabilityOptionally holds the descriptor the factory builds to one
// optional requirement, the log capability.
func TestTheModuleRequiresTheLogCapabilityOptionally(t *testing.T) {
	cfg := struct{}{}
	m := kv.NewModule(kv.Spec[struct{}]{
		ModuleName:      "kv.secrecy",
		ConfigNamespace: "kv.secrecy",
		Config:          &cfg,
		Stance:          func(*struct{}) (core.Enablement, error) { return core.Enablement{}, nil },
		Open:            func(context.Context, *struct{}) (kv.KV, error) { return nil, nil },
	})
	if len(m.Requires) != 1 {
		t.Fatalf("Requires = %+v, want the logging capability alone", m.Requires)
	}
	if r := m.Requires[0]; r.Token != core.Token((*log.Logger)(nil)) || !r.Optional {
		t.Errorf("Requires[0] = %+v, want the optional logging capability", r)
	}
}
