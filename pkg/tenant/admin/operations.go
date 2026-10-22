package admin

import (
	"net/http"
	"reflect"

	speedhttp "github.com/vislake/speed/pkg/http"
)

// The fragment's info section. The title and the version are the ones the
// hand-written fragment published before the declaration generated it.
const (
	fragmentTitle   = "tenant.admin"
	fragmentVersion = "1.0.0"
)

// The routes as the fragment writes them: relative to the API root the module's
// Spec declares. The mounting resolves them to the absolute paths the routes are
// registered at, so a path is written once.
const (
	pathTenantsFragment      = "/tenants"
	pathTenantFragment       = "/tenants/:tenant"
	pathTenantStatusFragment = "/tenants/:tenant/status"
)

// The success bodies. Each is an alias of an anonymous struct, so the fragment
// publishes the wrapper inline, as the hand-written fragment did, rather than as
// a component of its own.
type (
	// tenantListBody is the 200 answer of the listing.
	tenantListBody = struct {
		_       struct{}            `description:"The whole roster in one answer, because the listing is not paged."`
		Tenants []TenantAdminTenant `json:"tenants" required:"true" description:"Every tenant the roster holds, ordered by identifier."`
	}

	// tenantCreatedBody is the 201 answer of a registration.
	tenantCreatedBody = struct {
		_      struct{}          `description:"The tenant as registered, in the same view the read routes render."`
		Tenant TenantAdminTenant `json:"tenant" required:"true" description:"The registered tenant, its state active."`
	}

	// tenantReadBody is the 200 answer of a single-tenant read.
	tenantReadBody = struct {
		_      struct{}          `description:"The tenant the identifier named."`
		Tenant TenantAdminTenant `json:"tenant" required:"true" description:"The tenant, with its identifier, alias and state."`
	}
)

// createBody is the request of a registration: the tenant's identifier and the
// alias the request surface will address it by. The status is not here: a
// registration starts the tenant active. The type name is unexported, and the
// declaration publishes the body under the name the hand-written fragment used.
type createBody struct {
	_     struct{} `description:"The identifier and alias of a tenant to register. The state is not part of it, because a registered tenant starts active."`
	ID    string   `json:"id" required:"true" description:"The identifier to register; the roster refuses one it already holds."`
	Alias string   `json:"alias" required:"true" description:"The alias to give the tenant; the roster refuses one another tenant already holds."`
}

// getTenantInput is the request of a single-tenant read: the identifier in the
// path.
type getTenantInput struct {
	ID string `path:"tenant" description:"The tenant's identifier, the natural key the roster keys a tenant by: non-empty and free of whitespace and control characters."`
}

// setAliasInput is the request of a rename: the identifier in the path and the
// new alias in the body.
type setAliasInput struct {
	ID    string `path:"tenant" description:"The tenant's identifier, the natural key the roster keys a tenant by: non-empty and free of whitespace and control characters."`
	Alias string `json:"alias" required:"true" description:"The new alias; an alias another tenant holds is refused with a 409."`
}

// setStatusInput is the request of a status change: the identifier in the path
// and the target state in the body.
type setStatusInput struct {
	ID     string `path:"tenant" description:"The tenant's identifier, the natural key the roster keys a tenant by: non-empty and free of whitespace and control characters."`
	Status string `json:"status" required:"true" enum:"active,disabled" description:"active serves the tenant; disabled stops serving it."`
}

// listOperation declares GET /tenants: every tenant the roster
// holds, in the roster's own order.
var listOperation = speedhttp.NewOperation[struct{}, tenantListBody](
	"tenant.admin.tenants.list",
	http.MethodGet,
	pathTenantsFragment,
	"List every tenant the roster holds",
	"Reads every tenant in the roster, ordered by identifier. The roster is the set of tenants this deployment "+
		"serves and the read is not paged, so the answer always carries the whole list. It is a platform-domain "+
		"read: the caller needs tenant.read in the platform domain, and no cross-tenant context or permission is "+
		"involved. Reads write no audit row.",
	nil,
	[]speedhttp.Response{
		{
			Status:      http.StatusOK,
			Description: "Every tenant, in the roster's order by identifier",
			Body:        &speedhttp.Body{MediaType: "application/json"},
		},
		speedhttp.ProblemResponse(http.StatusUnauthorized, "The request carries no identity"),
		speedhttp.ProblemResponse(http.StatusForbidden,
			"The caller holds no tenant.read in the platform domain"),
		speedhttp.ProblemResponse(http.StatusServiceUnavailable, "A dependency could not answer"),
	},
)

// createOperation declares POST /tenants: it registers a tenant
// whose identifier the roster does not hold yet.
var createOperation = speedhttp.NewOperation[createBody, tenantCreatedBody](
	"tenant.admin.tenants.create",
	http.MethodPost,
	pathTenantsFragment,
	"Register an active tenant",
	"Registers a tenant the roster does not hold yet. The body gives the identifier and the alias; the new "+
		"tenant starts active. Registration never overwrites: an identifier or an alias already taken is "+
		"answered 409 and nothing is written. A mandatory audit row must land before the roster is called; if "+
		"it cannot, the registration does not run and the request is answered 503.",
	&speedhttp.RequestBody{
		MediaType: "application/json",
		Description: "The registration: the identifier to insert and the alias the request surface will address " +
			"the tenant by. The state is not part of it, because a registered tenant starts active.",
		Required: true,
		Name:     "TenantAdminTenantRegistration",
	},
	[]speedhttp.Response{
		{
			Status:      http.StatusCreated,
			Description: "The registered tenant",
			Body:        &speedhttp.Body{MediaType: "application/json"},
		},
		speedhttp.ProblemResponse(http.StatusBadRequest,
			"The identifier or the alias is outside the identifier domain"),
		speedhttp.ProblemResponse(http.StatusUnauthorized, "The request carries no identity"),
		speedhttp.ProblemResponse(http.StatusForbidden,
			"The caller holds no tenant.manage in the platform domain, or the caller has not proved an email address (contact-required)"),
		speedhttp.ProblemResponse(http.StatusConflict,
			"The identifier or the alias is already taken"),
		speedhttp.ProblemResponse(http.StatusServiceUnavailable, "A dependency could not answer"),
	},
)

// getOperation declares GET /tenants/:tenant: one tenant the roster
// holds. Its handler builds the answer because a tenant the roster does not hold
// is a bare 404, which the problem-document writer cannot produce.
var getOperation = speedhttp.NewReplyOperation[getTenantInput](
	"tenant.admin.tenants.get",
	http.MethodGet,
	pathTenantFragment,
	"Read one tenant the roster holds",
	"Reads one tenant by its identifier, the natural key the roster keys a row by, so no opaque row "+
		"identifier appears in a path. An identifier the roster does not hold is a 404. Reads write no audit row.",
	nil,
	[]speedhttp.Response{
		{
			Status:      http.StatusOK,
			Description: "The tenant",
			Body: &speedhttp.Body{
				MediaType: "application/json",
				Structure: reflect.TypeOf(tenantReadBody{}),
			},
		},
		speedhttp.ProblemResponse(http.StatusUnauthorized, "The request carries no identity"),
		speedhttp.ProblemResponse(http.StatusForbidden,
			"The caller holds no tenant.read in the platform domain"),
		{
			Status:      http.StatusNotFound,
			Description: "No tenant carries the identifier",
		},
		speedhttp.ProblemResponse(http.StatusServiceUnavailable, "A dependency could not answer"),
	},
)

// setAliasOperation declares PATCH /tenants/:tenant: it changes one
// tenant's alias and nothing else. Its handler builds the answer because the
// success is a 204 and an unknown tenant is a bare 404.
var setAliasOperation = speedhttp.NewReplyOperation[setAliasInput](
	"tenant.admin.tenants.setAlias",
	http.MethodPatch,
	pathTenantFragment,
	"Change one tenant's alias",
	"Changes one tenant's alias and nothing else; the tenant's state is left as it stands. The path "+
		"identifier and the new alias must both be identifiers. A tenant the roster does not hold is a 404, "+
		"and an alias another tenant holds is a 409. A mandatory audit row lands before the write; if it "+
		"cannot, the alias is unchanged and the request is answered 503.",
	&speedhttp.RequestBody{
		MediaType:   "application/json",
		Description: "The new alias, which must be an identifier.",
		Required:    true,
	},
	[]speedhttp.Response{
		{
			Status:      http.StatusNoContent,
			Description: "The alias was changed",
		},
		speedhttp.ProblemResponse(http.StatusBadRequest,
			"The identifier or the alias is outside the identifier domain"),
		speedhttp.ProblemResponse(http.StatusUnauthorized, "The request carries no identity"),
		speedhttp.ProblemResponse(http.StatusForbidden,
			"The caller holds no tenant.manage in the platform domain, or the caller has not proved an email address (contact-required)"),
		{
			Status:      http.StatusNotFound,
			Description: "No tenant carries the identifier",
		},
		speedhttp.ProblemResponse(http.StatusConflict, "The alias is already taken"),
		speedhttp.ProblemResponse(http.StatusServiceUnavailable, "A dependency could not answer"),
	},
)

// setStatusOperation declares POST /tenants/:tenant/status: it changes
// one tenant's state. Its handler builds the answer because the success is a 204
// and an unknown tenant is a bare 404.
var setStatusOperation = speedhttp.NewReplyOperation[setStatusInput](
	"tenant.admin.tenants.setStatus",
	http.MethodPost,
	pathTenantStatusFragment,
	"Change one tenant's state",
	"Changes one tenant's state, which is one of the closed set active and disabled. The roster has no "+
		"delete, so disabling is the only way to stop serving a tenant. An identifier the roster does not hold "+
		"is a 404. A mandatory audit row lands before the write; if it cannot, the state is unchanged and the "+
		"request is answered 503.",
	&speedhttp.RequestBody{
		MediaType:   "application/json",
		Description: "The target state, one of the closed set active and disabled.",
		Required:    true,
	},
	[]speedhttp.Response{
		{
			Status:      http.StatusNoContent,
			Description: "The state was changed",
		},
		speedhttp.ProblemResponse(http.StatusBadRequest,
			"The identifier is outside the identifier domain or the status is outside the closed set active/disabled"),
		speedhttp.ProblemResponse(http.StatusUnauthorized, "The request carries no identity"),
		speedhttp.ProblemResponse(http.StatusForbidden,
			"The caller holds no tenant.manage in the platform domain, or the caller has not proved an email address (contact-required)"),
		{
			Status:      http.StatusNotFound,
			Description: "No tenant carries the identifier",
		},
		speedhttp.ProblemResponse(http.StatusServiceUnavailable, "A dependency could not answer"),
	},
)

// operations is this module's declaration of the API it serves. The fragment is
// generated from it; the routes are mounted from the same declarations.
var operations = speedhttp.Operations{
	listOperation,
	createOperation,
	getOperation,
	setAliasOperation,
	setStatusOperation,
}
