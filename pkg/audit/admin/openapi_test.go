package admin

import (
	"testing"

	"github.com/vislake/speed/pkg/audit/journal"
	"github.com/vislake/speed/pkg/authz/guard"
	"github.com/vislake/speed/pkg/core"
	"github.com/vislake/speed/pkg/internal/routecontract"
)

// seams satisfies the two capabilities this module holds without implementing
// either: Init resolves each and stores it, and no method is called on one
// before a request arrives.
type seams struct {
	journal.Reader
	guard.Guard
}

// TestTheDeclaredOperationsAreTheRoutesTheModuleRegisters holds the one thing
// the OpenAPI fragment and the registrations have to agree on: the fragment is
// the only source a generated client is built from, and the design leaves the
// two free to drift with no failure signal at run time. The check drives the
// module's own Init and compares what it registered there with what it
// declared.
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
	})
}
