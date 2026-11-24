package admin

import (
	"testing"

	"github.com/vislake/speed/pkg/audit"
	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/authz"
	"github.com/vislake/speed/pkg/authz/guard"
	"github.com/vislake/speed/pkg/core"
	"github.com/vislake/speed/pkg/internal/routecontract"
)

type seams struct {
	authz.Manager
	authz.Catalog
	authz.SubjectPermissions
	guard.Guard
	audit.Audit
	authn.SignupVerification
}

// TestTheDeclaredOperationsAreTheRoutesTheModuleRegisters checks that the declared operations are the routes the module registers.
func TestTheDeclaredOperationsAreTheRoutesTheModuleRegisters(t *testing.T) {
	routecontract.Check(t, routecontract.Case{
		Module:    Module(),
		Endpoints: []string{"admin"},
		Stubs: []core.Module{
			routecontract.StandIn("stand-in.seams", &seams{},
				(*authz.Manager)(nil), (*authz.Catalog)(nil), (*authz.SubjectPermissions)(nil),
				(*guard.Guard)(nil), (*audit.Audit)(nil), (*authn.SignupVerification)(nil)),
		},
		Config: map[string]any{
			ConfigNamespace: map[string]any{"endpoints": []string{"admin"}},
		},
	})
}
