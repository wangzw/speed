package preferences

import (
	"testing"

	"github.com/vislake/speed/pkg/core"
	"github.com/vislake/speed/pkg/internal/routecontract"
	"github.com/vislake/speed/pkg/tenant"
)

// TestTheDeclaredOperationsAreTheRoutesTheModuleRegisters holds the one
// thing the OpenAPI fragment and the registrations have to agree on.
func TestTheDeclaredOperationsAreTheRoutesTheModuleRegisters(t *testing.T) {
	database, dbConfig := routecontract.SQLite(t)
	routecontract.Check(t, routecontract.Case{
		Module:    Module(),
		Endpoints: []string{"api"},
		Stubs: []core.Module{
			database,
			routecontract.StandIn("stand-in.catalog", &struct{ tenant.Catalog }{}, (*tenant.Catalog)(nil)),
		},
		Config: routecontract.Config(dbConfig, map[string]any{
			ConfigNamespace: map[string]any{"endpoints": []string{"api"}},
		}),
	})
}
