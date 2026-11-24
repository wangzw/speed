package admin

import (
	"testing"

	"github.com/vislake/speed/pkg/audit/journal"
	"github.com/vislake/speed/pkg/authz/guard"
	"github.com/vislake/speed/pkg/core"
	"github.com/vislake/speed/pkg/internal/routecontract"
)

type seams struct {
	journal.Reader
	guard.Guard
}

// TestTheDeclaredOperationsAreTheRoutesTheModuleRegisters holds the one
// thing the OpenAPI fragment and the registrations have to agree on.
func TestTheDeclaredOperationsAreTheRoutesTheModuleRegisters(t *testing.T) {
	routecontract.Check(t, routecontract.Case{
		Module:    Module(),
		Endpoints: []string{"admin"},
		Stubs: []core.Module{
			routecontract.StandIn("stand-in.seams", &seams{},
				(*journal.Reader)(nil), (*guard.Guard)(nil)),
		},
		Config: map[string]any{
			ConfigNamespace: map[string]any{"endpoints": []string{"admin"}},
		},
		QueryParams: map[routecontract.Route]map[string]bool{
			{Method: "GET", Path: "/api/v1/system/audit/records"}: listParams,
			{Method: "GET", Path: "/api/v1/system/audit/export"}:  exportParams,
		},
	})
}
