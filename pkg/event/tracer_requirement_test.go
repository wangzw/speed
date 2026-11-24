package event_test

import (
	"testing"

	"github.com/vislake/speed/pkg/core"
	"github.com/vislake/speed/pkg/event/memory"
	"github.com/vislake/speed/pkg/event/nats"
	"github.com/vislake/speed/pkg/event/postgres"
	"github.com/vislake/speed/pkg/event/redis"
	"github.com/vislake/speed/pkg/observability"
)

// TestEveryImplementationDeclaresTheOptionalTracerRequirement checks that each
// implementation's descriptor requires the optional tracer provider.
func TestEveryImplementationDeclaresTheOptionalTracerRequirement(t *testing.T) {
	want := core.Token((*observability.TracerProvider)(nil))
	for _, m := range []core.Module{
		memory.Module(), redis.Module(), postgres.Module(), nats.Module(),
	} {
		if m.Name == "" {
			t.Fatal("an implementation's descriptor carries no name, so what is checked below is not a module")
		}
		var optional, found bool
		for _, r := range m.Requires {
			if r.Token == want {
				found, optional = true, r.Optional
			}
		}
		switch {
		case !found:
			t.Errorf("%s declares no requirement on the telemetry provider: without that edge the "+
				"assembler is not ordered ahead of the bus, and Trace falls back to the provider that "+
				"exports nothing while the bus runs as if tracing worked", m.Name)
		case !optional:
			t.Errorf("%s requires the telemetry provider rather than taking it optionally: an assembly "+
				"with no telemetry is legal and delivers no provider, so the requirement has to be optional", m.Name)
		}
	}
}
