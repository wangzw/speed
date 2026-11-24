package self

import (
	"net/http"

	speedhttp "github.com/vislake/speed/pkg/http"
)

const (
	fragmentTitle   = "tenant.self"
	fragmentVersion = "1.0.0"
)

const pathTenantsFragment = "/auth/me/tenants"

type tenantSelfTenant = struct {
	// Name is the name the request surface addresses the tenant by.
	Name string `json:"name" required:"true" description:"The tenant's name, which the request surface addresses it by."`
	// DisplayName is the display name, empty when the profile names none.
	DisplayName string `json:"display_name" required:"true" description:"The organization's display name, empty when its profile names none. A surface falls back to the name then."`
	// Kind is "business" or "personal".
	Kind string `json:"kind" required:"true" enum:"business,personal" description:"The tenant's kind: business for an ordinary tenant, personal for the single-person space a host derives for an account. The value is a closed set; a client branches on it rather than on the name."`
}

type tenantsBody = struct {
	_ struct{} `description:"The tenants the calling account holds an active membership in."`

	// Tenants lists the caller's active memberships in membership-read order; never null.
	Tenants []tenantSelfTenant `json:"tenants" required:"true" description:"The tenants whose membership is active and whose roster row is active, in the membership read's order. Empty when the caller holds none."`
}

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

var operations = speedhttp.Operations{
	tenantsOperation,
}
