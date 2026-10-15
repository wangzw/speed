package stream

import (
	"testing"

	"github.com/vislake/speed/pkg/core"
	eventmemory "github.com/vislake/speed/pkg/event/memory"
	"github.com/vislake/speed/pkg/internal/routecontract"
)

// TestTheDeclaredOperationsAreTheRoutesTheModuleRegisters holds the one thing
// the OpenAPI fragment and the registrations have to agree on: the fragment is
// the only source a generated client is built from, and the design leaves the
// two free to drift with no failure signal at run time. The check drives the
// module's own Init and compares what it registered there with what it
// declared.
//
// The bus is the real in-process one, not a stand-in: Init subscribes the
// module's own handler to it.
func TestTheDeclaredOperationsAreTheRoutesTheModuleRegisters(t *testing.T) {
	routecontract.Check(t, routecontract.Case{
		Module:    Module(),
		Endpoints: []string{"api"},
		Stubs:     []core.Module{eventmemory.Module()},
		Config: map[string]any{
			ConfigNamespace: map[string]any{"endpoints": []string{"api"}},
		},
	})
}
