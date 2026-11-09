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

// tenantSelfTenant is one entry of the answer: the name the request surface
// addresses the tenant by, the display name the organization carries, and its
// kind. It carries no identifier: the identifier is internal, and a client
// addresses a tenant by the name alone. The kind is the host's answer, passed
// through from tenant.KindResolver; the roster records no kind and this module
// derives none. The display name is empty for a tenant whose profile names none
// — a personal tenant ordinarily does — and a host falls back to the name.
// There is no status, because a row reaches the answer only when its tenant is
// served.
type tenantSelfTenant = struct {
	Name        string `json:"name" required:"true" description:"The tenant's name, which the request surface addresses it by."`
	DisplayName string `json:"displayName" required:"true" description:"The organization's display name, empty when its profile names none. A surface falls back to the name then."`
	Kind        string `json:"kind" required:"true" enum:"business,personal" description:"The tenant's kind: business for an ordinary tenant, personal for the single-person space a host derives for an account. The value is a closed set; a client branches on it rather than on the name."`
}

// tenantsBody is the 200 answer: the tenants the caller holds an active
// membership in. The list is always present, in the membership read's order,
// and empty rather than absent when the caller holds none.
type tenantsBody = struct {
	_ struct{} `description:"The tenants the calling account holds an active membership in."`

	Tenants []tenantSelfTenant `json:"tenants" required:"true" description:"The tenants whose membership is active and whose roster row is active, in the membership read's order. Empty when the caller holds none."`
}

// tenantsOperation declares GET /auth/me/tenants: the tenants the caller holds
// an active membership in.
var tenantsOperation = speedhttp.NewOperation[struct{}, tenantsBody](
	"tenant.self.tenants.list",
	http.MethodGet,
	pathTenantsFragment,
	"List the tenants the caller holds an active membership in",
	"Answers the tenants the calling account holds an active membership in, with each tenant's "+
		"name, display name and kind. No identifier is exposed: the name is what a client addresses the "+
		"tenant by. The display name is the organization's, empty when its profile names "+
		"none; a surface falls back to the name then. The kind is business or personal, a closed set the "+
		"host's resolver supplies; an assembly without a resolver reads every tenant as business. A "+
		"membership that is suspended is left "+
		"out, as is a tenant whose roster state is not active and an identifier the roster does not carry. "+
		"The list is in the membership read's order, a stable enumeration over tenants the view exposes by name alone, and is empty rather than absent when the caller holds none. The "+
		"route resolves no tenant and requires no permission: every authenticated caller reads its own "+
		"memberships. A membership, roster or kind read that cannot answer is a 503, never an empty list.",
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
