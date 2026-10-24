package self

import (
	"net/http"

	speedhttp "github.com/vislake/speed/pkg/http"
)

// The fragment's info section: the title this module's fragment carries and the
// version of the fragment's shape.
const (
	fragmentTitle   = "tenant.self"
	fragmentVersion = "1.0.0"
)

// pathTenantsFragment is the route as the fragment writes it: relative to the
// module's API root, so the absolute path is /api/v1/auth/me/tenants. The
// mounting resolves the same declaration, so the path is written once.
const pathTenantsFragment = "/auth/me/tenants"

// tenantSelfTenant is one entry of the answer: the tenant's identifier and the
// alias the request surface addresses it by. It carries no kind — the personal
// tenant is a host policy the roster does not record — and no status, because a
// row reaches the answer only when its tenant is served.
type tenantSelfTenant = struct {
	ID    string `json:"id" required:"true" description:"The tenant's immutable identifier."`
	Alias string `json:"alias" required:"true" description:"The tenant's alias, the name the request surface addresses it by."`
}

// tenantsBody is the 200 answer: the tenants the caller holds an active
// membership in. The list is always present, ordered by identifier, and empty
// rather than absent when the caller holds none.
type tenantsBody = struct {
	_ struct{} `description:"The tenants the calling account holds an active membership in."`

	Tenants []tenantSelfTenant `json:"tenants" required:"true" description:"The tenants whose membership is active and whose roster row is active, ordered by identifier. Empty when the caller holds none."`
}

// tenantsOperation declares GET /auth/me/tenants: the tenants the caller holds
// an active membership in.
var tenantsOperation = speedhttp.NewOperation[struct{}, tenantsBody](
	"tenant.self.tenants.list",
	http.MethodGet,
	pathTenantsFragment,
	"List the tenants the caller holds an active membership in",
	"Answers the tenants the calling account holds an active membership in, with each tenant's identifier "+
		"and alias. A membership that is suspended is left out, as is a tenant whose roster state is not "+
		"active and an identifier the roster does not carry. The list is ordered by identifier and is empty "+
		"rather than absent when the caller holds none. The route resolves no tenant and requires no "+
		"permission: every authenticated caller reads its own memberships. A membership or roster read that "+
		"cannot answer is a 503, never an empty list.",
	nil,
	[]speedhttp.Response{
		{
			Status:      http.StatusOK,
			Description: "The tenants the caller holds an active membership in",
			Body:        &speedhttp.Body{MediaType: "application/json"},
		},
		speedhttp.ProblemResponse(http.StatusUnauthorized, "The request carries no identity"),
		speedhttp.ProblemResponse(http.StatusServiceUnavailable,
			"The membership table or the roster snapshot could not answer"),
	},
)

// operations is this module's declaration of the API it serves. The fragment is
// generated from it; the route is mounted from the same declaration.
var operations = speedhttp.Operations{
	tenantsOperation,
}
