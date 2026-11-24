package board

import (
	"testing"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/core"
	"github.com/vislake/speed/pkg/internal/routecontract"
)

type seam struct{}

func (seam) Exempt(string, string, string)       {}
func (seam) Optional(string, string, string)     {}
func (seam) Impersonable(string, string, string) {}

// TestTheDeclaredOperationsAreTheRoutesTheModuleRegisters checks that the route
// the module registers matches the operation it declares.
func TestTheDeclaredOperationsAreTheRoutesTheModuleRegisters(t *testing.T) {
	routecontract.Check(t, routecontract.Case{
		Module:    Module(),
		Endpoints: []string{fragmentEndpoint},
		Stubs: []core.Module{
			routecontract.StandIn("stand-in.gate", seam{}, (*authn.Gate)(nil)),
		},
		Config: map[string]any{
			"feature": map[string]any{
				"board": map[string]any{"endpoint": fragmentEndpoint},
			},
		},
	})
}
