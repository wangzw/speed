package stream

import (
	"testing"

	"github.com/vislake/speed/pkg/core"
	eventmemory "github.com/vislake/speed/pkg/event/memory"
	"github.com/vislake/speed/pkg/internal/routecontract"
)

// TestTheDeclaredOperationsAreTheRoutesTheModuleRegisters holds the one
// thing the OpenAPI fragment and the registrations have to agree on.
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
