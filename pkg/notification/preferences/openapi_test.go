package preferences

import (
	"testing"

	"github.com/vislake/speed/pkg/core"
	"github.com/vislake/speed/pkg/internal/routecontract"
	"github.com/vislake/speed/pkg/tenant"
)

// TestTheDeclaredOperationsAreTheRoutesTheModuleRegisters holds the one thing
// the OpenAPI fragment and the registrations have to agree on: the fragment is
// the only source a generated client is built from, and the design leaves the
// two free to drift with no failure signal at run time. The check drives the
// module's own Init and compares what it registered there with what it
// declared.
//
// The database is real, because Init takes its handle up; the tenant roster is
// a stand-in, because the module declares the requirement without resolving it
// and no request is served.
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
