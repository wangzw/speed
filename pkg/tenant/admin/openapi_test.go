package admin

import (
	"testing"

	"github.com/vislake/speed/pkg/audit"
	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/authz/guard"
	"github.com/vislake/speed/pkg/core"
	"github.com/vislake/speed/pkg/internal/routecontract"
	"github.com/vislake/speed/pkg/tenant"
)

type seams struct {
	tenant.Catalog
	tenant.WriteGuard
	tenant.Provisioner
	tenant.OrganizationWriter
	tenant.OwnershipTarget
	guard.Guard
	audit.Audit
	authn.SignupVerification
}

// TestTheDeclaredOperationsAreTheRoutesTheModuleRegisters checks that the OpenAPI
// fragment and the module's registrations declare the same operations.
func TestTheDeclaredOperationsAreTheRoutesTheModuleRegisters(t *testing.T) {
	routecontract.Check(t, routecontract.Case{
		Module:    Module(),
		Endpoints: []string{"admin", "api"},
		Stubs: []core.Module{
			routecontract.StandIn("stand-in.seams", &seams{},
				(*tenant.Catalog)(nil), (*tenant.WriteGuard)(nil), (*tenant.Provisioner)(nil),
				(*tenant.OrganizationWriter)(nil), (*tenant.OwnershipTarget)(nil),
				(*guard.Guard)(nil), (*audit.Audit)(nil),
				(*authn.SignupVerification)(nil)),
		},
		Config: map[string]any{
			ConfigNamespace: map[string]any{
				"endpoints":        []string{"admin"},
				"tenant-endpoints": []string{"api"},
				"self-endpoints":   []string{"api"},
			},
		},
	})
}
