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

// seams satisfies the capabilities this module holds without implementing any
// of them: Init resolves each and stores it, and no method is called on one
// before a request arrives.
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

// TestTheDeclaredOperationsAreTheRoutesTheModuleRegisters holds the one thing
// the OpenAPI fragment and the registrations have to agree on: the fragment is
// the only source a generated client is built from, and the design leaves the
// two free to drift with no failure signal at run time. The check drives the
// module's own Init and compares what it registered there with what it
// declared.
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
