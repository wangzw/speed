package kv_test

import (
	"context"
	"testing"

	"github.com/vislake/speed/pkg/core"
	"github.com/vislake/speed/pkg/kv"
	"github.com/vislake/speed/pkg/log"
)

// TestTheModuleRequiresTheLogCapabilityOptionally holds the descriptor the
// factory builds to one optional requirement, the log capability.
//
// Optional, because a backend has to work in an assembly that never took logging
// in; declared at all, because an assembly that did has to have it constructed
// first, so that New can register the backend's credentials as literal redaction
// rules before any logger binds an attribute. A different capability, or a
// requirement that is not optional, are the other two ways the declaration goes
// wrong.
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
