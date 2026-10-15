package event_test

import (
	"context"
	"log/slog"
	"testing"

	"github.com/vislake/speed/pkg/core"
	"github.com/vislake/speed/pkg/event"
	"github.com/vislake/speed/pkg/event/internal/tracing"
	"github.com/vislake/speed/pkg/log"
)

// TestTheModuleRequiresTheLogCapabilityOptionally holds the descriptor the
// factory builds to one optional requirement, the log capability.
//
// Optional, because a bus has to work in an assembly that never took logging
// in; declared at all, because an assembly that did has to have it constructed
// first, or the bus would be built before there was a logger to hand it, and the
// module's New would resolve ErrMissingProvider and quietly register no
// credential at all. A different capability, or a requirement that is not
// optional, are the other two ways the declaration goes wrong.
func TestTheModuleRequiresTheLogCapabilityOptionally(t *testing.T) {
	cfg := struct{}{}
	m := event.NewModule(event.Spec[struct{}]{
		ModuleName:      "event.secrecy",
		ConfigNamespace: "event.secrecy",
		Config:          &cfg,
		Stance:          func(*struct{}) (core.Enablement, error) { return core.Enablement{}, nil },
		Trace:           func(*core.Registry) (event.TracerProvider, error) { return tracing.Noop(), nil },
		Open: func(context.Context, *struct{}, *slog.Logger, event.TracerProvider) (event.Bus, error) {
			return nil, nil
		},
	})
	if len(m.Requires) != 1 {
		t.Fatalf("Requires = %+v, want the logging capability alone", m.Requires)
	}
	if r := m.Requires[0]; r.Token != core.Token((*log.Logger)(nil)) || !r.Optional {
		t.Errorf("Requires[0] = %+v, want the optional logging capability", r)
	}
}
