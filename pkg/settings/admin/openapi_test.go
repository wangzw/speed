package admin

import (
	"testing"

	"github.com/vislake/speed/pkg/audit"
	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/authz/guard"
	"github.com/vislake/speed/pkg/core"
	"github.com/vislake/speed/pkg/internal/routecontract"
	"github.com/vislake/speed/pkg/settings"
)

type seams struct {
	settings.Store
	guard.Guard
	audit.Audit
	authn.SignupVerification
}

// TestTheDeclaredOperationsAreTheRoutesTheModuleRegisters checks that the
// fragment and the mounted routes agree, by driving Init and comparing.
func TestTheDeclaredOperationsAreTheRoutesTheModuleRegisters(t *testing.T) {
	routecontract.Check(t, routecontract.Case{
		Module:    Module(),
		Endpoints: []string{"admin"},
		Stubs: []core.Module{
			routecontract.StandIn("stand-in.seams", &seams{},
				(*settings.Store)(nil), (*guard.Guard)(nil), (*audit.Audit)(nil),
				(*authn.SignupVerification)(nil)),
		},
		Config: map[string]any{
			ConfigNamespace: map[string]any{"endpoints": []string{"admin"}},
		},
	})
}
