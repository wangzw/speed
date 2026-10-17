package board

import (
	"testing"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/core"
	"github.com/vislake/speed/pkg/internal/routecontract"
)

// seam satisfies the capability this module takes up in Init without
// implementing it: Init resolves the gateway and registers one route on it, and
// nothing here needs the registrations afterwards.
type seam struct{}

func (seam) Exempt(string, string, string)       {}
func (seam) Optional(string, string, string)     {}
func (seam) Impersonable(string, string, string) {}

// TestTheDeclaredOperationsAreTheRoutesTheModuleRegisters holds the one thing
// the fragment and the registrations have to agree on. The fragment is the only
// source a generated client is built from, and the design leaves the two free to
// drift with no failure signal at run time, so the check drives the module's own
// Init and compares the route it registered with the operation it declared.
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
