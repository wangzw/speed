package self

import (
	"net/http"

	speedhttp "github.com/vislake/speed/pkg/http"
)

// The fragment's info section.
const (
	fragmentTitle   = "authz.self"
	fragmentVersion = "1.0.0"
)

// pathPermissionsFragment is the route relative to the module's API root.
const pathPermissionsFragment = "/authz/me/permissions"

// permissionsBody is the 200 answer: the permission names the caller holds in
// each domain. Both lists are always present, sorted, and empty rather than
// absent when the caller holds none.
type permissionsBody = struct {
	_ struct{} `description:"The permissions the calling principal holds in the two policy domains."`

	Platform []string `json:"platform" required:"true" description:"The permission names the principal holds in the platform domain, sorted ascending. Empty when it holds none there."`
	Tenant   []string `json:"tenant" required:"true" description:"The permission names the principal holds in the tenant the request resolved to, sorted ascending. Empty when the request resolved no tenant, or the principal holds none there."`
}

// permissionsOperation declares GET /authz/me/permissions.
var permissionsOperation = speedhttp.NewOperation[struct{}, permissionsBody](
	"authz.self.permissions",
	http.MethodGet,
	pathPermissionsFragment,
	"List the permissions the caller holds",
	"Answers the permission names the calling principal effectively holds, as the union over the roles "+
		"it is bound to in each domain: platform for the platform domain, and tenant for the tenant the "+
		"request resolved to. A request that resolved no tenant answers an empty tenant list. A held "+
		"(object, action) pair that no declared permission translates to is left out, and a permission "+
		"reached through several roles appears once. A policy that cannot be read is a 503, never an "+
		"empty list.",
	nil,
	[]speedhttp.Response{
		{
			Status:      http.StatusOK,
			Description: "The permissions the caller holds in the platform domain and in the request's tenant",
			Body:        &speedhttp.Body{MediaType: "application/json"},
		},
		speedhttp.ProblemResponse(http.StatusUnauthorized, "The request carries no identity"),
		speedhttp.ProblemResponse(http.StatusServiceUnavailable, "The policy snapshot or its store could not answer"),
	},
)

// operations is this module's declaration of the API it serves.
var operations = speedhttp.Operations{
	permissionsOperation,
}
