package admin

import (
	"testing"

	"github.com/vislake/speed/pkg/audit"
	"github.com/vislake/speed/pkg/authz/guard"
	"github.com/vislake/speed/pkg/core"
	"github.com/vislake/speed/pkg/internal/routecontract"
	"github.com/vislake/speed/pkg/sms"
)

type seams struct {
	sms.Registry
	guard.Guard
	audit.Audit
}

type providerSeam struct{ sms.Provider }

// TestTheDeclaredOperationsAreTheRoutesTheModuleRegisters checks that the fragment and the
// mounted routes agree by driving Init and comparing what it registered.
func TestTheDeclaredOperationsAreTheRoutesTheModuleRegisters(t *testing.T) {
	routecontract.Check(t, routecontract.Case{
		Module:    Module(),
		Endpoints: []string{"admin"},
		Stubs: []core.Module{
			routecontract.StandIn("stand-in.seams", &seams{},
				(*sms.Registry)(nil), (*guard.Guard)(nil), (*audit.Audit)(nil)),
			routecontract.StandIn("stand-in.provider", &providerSeam{}, (*sms.Provider)(nil)),
		},
		Config: map[string]any{
			ConfigNamespace: map[string]any{"endpoints": []string{"admin"}},
		},
	})
}
