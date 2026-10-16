package admin

import (
	"testing"

	"github.com/vislake/speed/pkg/audit"
	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/authn/internal/routefixture"
	"github.com/vislake/speed/pkg/internal/routecontract"
)

// TestTheDeclaredOperationsAreTheRoutesTheModuleRegisters holds the one thing
// the OpenAPI fragment and the registrations have to agree on: the fragment is
// the only source a generated client is built from, and the design leaves the
// two free to drift with no failure signal at run time. The check drives the
// module's own Init, through the real route-mounting module, and compares what
// it registered there with what it declared. Given the accepted sets of the
// three paged routes, it also holds each one's declared query parameters to what
// its handler reads.
//
// The fragment declares the whole management surface, and Init mounts one group
// of it per capability it takes up there, so the assembly carries every group's
// capability: with one absent the module mounts less than it declares, and that
// is a legal assembly rather than a disagreement.
func TestTheDeclaredOperationsAreTheRoutesTheModuleRegisters(t *testing.T) {
	stubs, config := routefixture.Assembly(t, Module())
	stubs = append(stubs, routefixture.Seams(
		(*authn.Accounts)(nil),
		(*authn.AccountAdmin)(nil),
		(*authn.Memberships)(nil),
		(*audit.Audit)(nil),
		(*authn.Decider)(nil),
		(*authn.Passwords)(nil),
		(*authn.Sessions)(nil),
		(*authn.MFA)(nil),
		(*authn.Tokens)(nil),
		(*authn.SSOProviders)(nil),
		(*authn.Impersonation)(nil),
		(*authn.SSOMetadataRefresher)(nil),
	)...)
	routecontract.Check(t, routecontract.Case{
		Module: Module(),
		Stubs:  stubs,
		Config: config,
		QueryParams: map[routecontract.Route]map[string]bool{
			{Method: "GET", Path: "/auth/admin/accounts"}:                accountListParams,
			{Method: "GET", Path: "/auth/admin/tenants/:tenant/members"}: {"limit": true, "after": true},
			{Method: "GET", Path: "/auth/admin/impersonations"}:          {"tenant": true, "limit": true, "after": true},
		},
	})
}
