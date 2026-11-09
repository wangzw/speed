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

	// tenantOwnerFragmentTitle is the title of the tenant-surface fragment.
	tenantOwnerFragmentTitle = "tenant.owner"
)

// The routes as the fragment writes them: relative to the API root the module's
// Spec declares. The mounting resolves them to the absolute paths the routes are
// registered at, so a path is written once.
const (
	pathTenantsFragment       = "/tenants"
	pathTenantFragment        = "/tenants/:tenant"
	pathTenantStatusFragment  = "/tenants/:tenant/status"
	pathTenantProfileFragment = "/tenants/:tenant/profile"
	pathTenantOwnerFragment   = "/tenants/:tenant/owner"
)

// The tenant-surface routes, relative to the tenants-scope API root. The
// absolute paths are /api/v1/tenants/:tenant/owner and /api/v1/tenants/:tenant.
const (
	pathOwnerTransferFragment = "/:tenant/owner"
	pathOrganizationFragment  = "/:tenant"
)

// The success bodies. Each is an alias of an anonymous struct, so the fragment
// publishes the wrapper inline, as the hand-written fragment did, rather than as
// a component of its own.
type (
	// tenantListBody is the 200 answer of the listing.
	tenantListBody = struct {
		_       struct{}            `description:"The whole roster in one answer, because the listing is not paged."`
		Tenants []TenantAdminTenant `json:"tenants" required:"true" description:"Every tenant the roster holds, ordered by name."`
	}

	// tenantCreatedBody is the 201 answer of a registration.
	tenantCreatedBody = struct {
		_      struct{}          `description:"The tenant as registered, in the same view the read routes render."`
		Tenant TenantAdminTenant `json:"tenant" required:"true" description:"The registered tenant, its state active."`
	}

	// tenantReadBody is the 200 answer of a single-tenant read.
	tenantReadBody = struct {
		_      struct{}          `description:"The tenant the name named."`
		Tenant TenantAdminTenant `json:"tenant" required:"true" description:"The tenant, with its name, kind, owner account and state."`
	}
)

// createBody is the request of a registration: the name the request surface
// will address the tenant by and its optional profile. The identifier is
// generated, so it is not part of the body and a body that carries one is
// refused. The status is not here either: a registration starts the tenant
// active. The type name is unexported, and the declaration publishes the body
// under the name the hand-written fragment used.
type createBody struct {
	_    struct{} `description:"The name and optional profile of a tenant to register. The identifier is generated and the state starts active, so neither is part of the body."`
	Name string   `json:"name" required:"true" description:"The name to give the tenant; the roster refuses one another tenant already holds."`

	// The profile fields are optional here: the platform form requires a
	// display name, but the API leaves a registration without a profile valid.
	DisplayName  string `json:"display_name" description:"The organization's display name, at most 100 runes. Empty when the registration names none."`
	Description  string `json:"description" description:"A longer account of the organization, at most 500 runes."`
	ContactEmail string `json:"contact_email" description:"The organization's contact address, one bare email address of at most 254 bytes."`
	Website      string `json:"website" description:"The organization's absolute http or https address, at most 2048 bytes, with a host and no userinfo."`
}

// getTenantInput is the request of a single-tenant read: the name in the path.
type getTenantInput struct {
	ID string `path:"tenant" description:"The tenant's name. The platform name layer resolves it to the internal identifier before the handler runs; a name no tenant carries is a 404."`
}

// transferOwnerInput is the request of an owner transfer: the tenant in the
// path and the account that becomes its owner in the body.
type transferOwnerInput struct {
	ID      string `path:"tenant" description:"The tenant's name. The platform name layer resolves it to the internal identifier before the handler runs; a name no tenant carries is a 404."`
	Account string `json:"account" required:"true" description:"The account identifier of the active member that becomes the organization's owner."`
}

// setProfileInput is the request of a profile update: the identifier in the
// path and the four descriptive fields in the body, each of which must be
// present. A field the body carries empty clears the stored value.
type setProfileInput struct {
	ID string `path:"tenant" description:"The tenant's name. The platform name layer resolves it to the internal identifier before the handler runs; a name no tenant carries is a 404."`

	DisplayName  string `json:"display_name" required:"true" description:"The organization's display name, at most 100 runes. Empty clears it; a surface then falls back to the name."`
	Description  string `json:"description" required:"true" description:"A longer account of the organization, at most 500 runes. Empty clears it."`
	ContactEmail string `json:"contact_email" required:"true" description:"The organization's contact address, one bare email address of at most 254 bytes. Empty clears it."`
	Website      string `json:"website" required:"true" description:"The organization's absolute http or https address, at most 2048 bytes, with a host and no userinfo. Empty clears it."`
}

// profileWriteFields are the body members a profile update must carry. The
// declaration marks them required, and the entry point's binding enforces body
// presence, so the declaration carries them the way the tenant profile module
// carries its own write body: the same snake_case names the registration body
// uses.
var profileWriteFields = []string{"display_name", "description", "contact_email", "website"}

// registrationForbiddenFields are the body members a registration may not
// carry. The identifier is generated on the server; a client that sends one is
// refused rather than allowed to choose an internal key.
var registrationForbiddenFields = []string{"id"}

// setStatusInput is the request of a status change: the identifier in the path
// and the target state in the body.
type setStatusInput struct {
	ID     string `path:"tenant" description:"The tenant's name. The platform name layer resolves it to the internal identifier before the handler runs; a name no tenant carries is a 404."`
	Status string `json:"status" required:"true" enum:"active,disabled" description:"active serves the tenant; disabled stops serving it."`
}

// listOperation declares GET /tenants: every tenant the roster
// holds, in the roster's own order.
var listOperation = speedhttp.NewOperation[struct{}, tenantListBody](
	"tenant.admin.tenants.list",
	http.MethodGet,
	pathTenantsFragment,
	"List every tenant the roster holds",
	"Reads every tenant in the roster, ordered by name. The roster is the set of tenants this deployment "+
		"serves and the read is not paged, so the answer always carries the whole list. It is a platform-domain "+
		"read: the caller needs tenant.read in the platform domain, and no cross-tenant context or permission is "+
		"involved. Reads write no audit row.",
	nil,
	[]speedhttp.Response{
		{
			Status:      http.StatusOK,
			Description: "Every tenant, in the roster's order by name",
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
	"Registers a tenant the roster does not hold yet, together with its first tenant-admin, which is the "+
		"acting account. The body gives the name and optionally the profile's four descriptive fields; the "+
		"identifier is generated by the server and never appears in a request or a view, and the new tenant starts "+
		"active. A body that carries an identifier is refused as a bad request. Registration "+
		"never overwrites: an identifier or a name already taken is answered 409 and nothing is written. A "+
		"mandatory audit row must land before the provisioner is called; if it cannot, the registration does not "+
		"run and the request is answered 503.",
	&speedhttp.RequestBody{
		MediaType: "application/json",
		Description: "The registration: the name the request surface will address " +
			"the tenant by, and the optional profile fields. The identifier is generated, and the state is not " +
			"part of it, because a registered tenant starts active.",
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
			"The body carries an identifier, or the name is outside the slug domain"),
		speedhttp.ProblemResponse(http.StatusUnauthorized, "The request carries no identity"),
		speedhttp.ProblemResponse(http.StatusForbidden,
			"The caller holds no tenant.manage in the platform domain, or the caller has not proved an email address (contact-required)"),
		speedhttp.ProblemResponse(http.StatusConflict,
			"The name is already taken"),
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
	"Reads one tenant by its name, which the request surface addresses it by. A name the roster does "+
		"not hold is a 404. Reads write no audit row.",
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
			Description: "No tenant carries the name",
		},
		speedhttp.ProblemResponse(http.StatusServiceUnavailable, "A dependency could not answer"),
	},
)

// transferOwnerOperation declares PUT /tenants/:tenant/owner: it hands one
// tenant's ownership to an active member. Its handler builds the answer because
// an unknown tenant is a bare 404.
var transferOwnerOperation = speedhttp.NewReplyOperation[transferOwnerInput](
	"tenant.admin.tenants.transferOwner",
	http.MethodPut,
	pathTenantOwnerFragment,
	"Transfer one tenant's owner",
	"Hands one tenant's ownership to an active member. The target must already be an active member of the "+
		"organization; the transfer grants it tenant-admin if it does not hold it, and the previous owner "+
		"loses every binding in the organization and is removed from it. The transfer and those writes are "+
		"one transaction. A mandatory audit row lands before the write; if it cannot, nothing is written and "+
		"the request is answered 503.",
	&speedhttp.RequestBody{
		MediaType:   "application/json",
		Description: "The account identifier of the active member that becomes the owner.",
		Required:    true,
		Name:        "TenantAdminTenantOwnerTransfer",
	},
	[]speedhttp.Response{
		{
			Status:      http.StatusNoContent,
			Description: "The ownership was transferred",
		},
		speedhttp.ProblemResponse(http.StatusBadRequest,
			"The name is outside the slug domain, the request body is not one JSON object, or the account member is absent"),
		speedhttp.ProblemResponse(http.StatusUnauthorized, "The request carries no identity"),
		speedhttp.ProblemResponse(http.StatusForbidden,
			"The caller holds no tenant.manage in the platform domain, or the caller has not proved an email address (contact-required)"),
		{
			Status:      http.StatusNotFound,
			Description: "No tenant carries the name",
		},
		speedhttp.ProblemResponse(http.StatusConflict,
			"The target is not an active member of the organization, the target's account is not active, or the tenant is a tombstone"),
		speedhttp.ProblemResponse(http.StatusServiceUnavailable, "A dependency could not answer"),
	},
)

// deleteOperation declares DELETE /tenants/:tenant: it writes the tenant's
// tombstone and cleans its core relations. Its handler builds the answer because
// an unknown tenant is a bare 404.
//
// This is the platform surface: it requires tenant.manage in the platform
// domain. An organization's owner deletes through the tenant surface
// (tenantDeleteOperation) and is refused here unless they also hold that
// permission; the two surfaces share the write and its locks.
var deleteOperation = speedhttp.NewReplyOperation[getTenantInput](
	"tenant.admin.tenants.delete",
	http.MethodDelete,
	pathTenantFragment,
	"Delete one tenant",
	"Deletes one tenant platform staff names. The write is a tombstone: the name and the internal "+
		"identifier stay and are never reusable, and the organization's memberships, bindings, roles and grants are "+
		"removed in the same transaction. Other modules' tenant data is cleaned under the tombstone and is not "+
		"part of this write. A mandatory audit row lands before the write; if it cannot, nothing is written "+
		"and the request is answered 503. A tombstone is a 409: deletion is terminal. An organization's owner "+
		"reaches the tenant surface, not this route.",
	nil,
	[]speedhttp.Response{
		{
			Status:      http.StatusNoContent,
			Description: "The tenant was deleted",
		},
		speedhttp.ProblemResponse(http.StatusBadRequest,
			"The name is outside the slug domain"),
		speedhttp.ProblemResponse(http.StatusUnauthorized, "The request carries no identity"),
		speedhttp.ProblemResponse(http.StatusForbidden,
			"The caller holds no tenant.manage in the platform domain, the caller has not proved an "+
				"email address (contact-required), or the tenant is protected (urn:speed:tenant:protected)"),
		{
			Status:      http.StatusNotFound,
			Description: "No tenant carries the name",
		},
		speedhttp.ProblemResponse(http.StatusConflict,
			"The tenant is a tombstone"),
		speedhttp.ProblemResponse(http.StatusServiceUnavailable, "A dependency could not answer"),
	},
)

// setProfileOperation declares PATCH /tenants/:tenant/profile: it writes one
// tenant's organization profile and nothing else. Its handler builds the answer
// because an unknown tenant is a bare 404.
var setProfileOperation = speedhttp.NewReplyOperation[setProfileInput](
	"tenant.admin.tenants.profile",
	http.MethodPatch,
	pathTenantProfileFragment,
	"Write one tenant's organization profile",
	"Writes one tenant's organization profile and nothing else: the display name, description, contact email "+
		"and website. The identifier, name, state and creation time are not written, so a profile update "+
		"cannot undo a disable or a deletion another path committed. The body carries all four fields; "+
		"an empty value clears the stored one. A profile outside the stated bounds is a 400, an unknown tenant a "+
		"404, and a tombstone a 409. A mandatory audit row lands before the roster write, with surface=platform "+
		"in its detail; if it cannot, nothing is written and the request is answered 503.",
	&speedhttp.RequestBody{
		MediaType:   "application/json",
		Description: "The four descriptive fields, all of them present. An empty value clears the stored one.",
		Required:    true,
		Name:        "TenantAdminTenantProfileWrite",
	},
	[]speedhttp.Response{
		{
			Status:      http.StatusOK,
			Description: "The tenant with the profile as written",
			Body:        &speedhttp.Body{MediaType: "application/json", Structure: reflect.TypeOf(tenantReadBody{})},
		},
		speedhttp.ProblemResponse(http.StatusBadRequest,
			"The name is outside the slug domain, the request body is not one JSON object, a "+
				"member is absent, or the profile is outside the stated bounds"),
		speedhttp.ProblemResponse(http.StatusUnauthorized, "The request carries no identity"),
		speedhttp.ProblemResponse(http.StatusForbidden,
			"The caller holds no tenant.manage in the platform domain, or the caller has not proved an email address (contact-required)"),
		{
			Status:      http.StatusNotFound,
			Description: "No tenant carries the name",
		},
		speedhttp.ProblemResponse(http.StatusConflict, "The tenant is a tombstone; a deleted tenant's profile is terminal"),
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
	"Changes one tenant's state, which is one of the closed set active and disabled. This surface does not "+
		"write the deleted state: deletion is the roster's terminal marker, written by the host that winds an "+
		"account up, and the deleted tenant is rendered read-only here. When the write disables the tenant, "+
		"before the audit the route asks the host's write guard whether the tenant refuses the write: a "+
		"protected tenant answers 403 with the type urn:speed:tenant:protected and nothing is written. An "+
		"name the roster does not hold "+
		"is a 404; a tenant the roster holds as a tombstone is a 409, because deletion is terminal and no "+
		"status change revives it. A mandatory audit row lands before the write; if it cannot, the state is "+
		"unchanged and the request is answered 503.",
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
			"The name is outside the slug domain or the status is outside the closed set active/disabled"),
		speedhttp.ProblemResponse(http.StatusUnauthorized, "The request carries no identity"),
		speedhttp.ProblemResponse(http.StatusForbidden,
			"The caller holds no tenant.manage in the platform domain, the caller has not proved an email "+
				"address (contact-required), or the tenant is protected against disabling (urn:speed:tenant:protected)"),
		{
			Status:      http.StatusNotFound,
			Description: "No tenant carries the name",
		},
		speedhttp.ProblemResponse(http.StatusConflict, "The tenant is a tombstone; deletion is terminal"),
		speedhttp.ProblemResponse(http.StatusServiceUnavailable, "A dependency could not answer"),
	},
)

// ownerTransferInput is the request of the tenant-surface owner transfer: the
// tenant in the path and the account that becomes its owner in the body. The
// path segment is held to the resolved tenant by the tenant administration
// guard before the handler runs.
type ownerTransferInput struct {
	Tenant  string `path:"tenant" description:"The tenant's name. The path-tenant layer resolves it to the internal identifier before the handler runs, holds it to the tenant the request resolved to, and refuses the platform domain; a name no tenant carries is a 404."`
	Account string `json:"account" required:"true" description:"The account identifier of the active member that becomes the organization's owner."`
}

// tenantTransferOwnerOperation declares PUT /:tenant/owner on the tenant
// surface: the organization's own owner hands its ownership on. Its handler
// builds the answer because an unknown tenant is a bare 404.
var tenantTransferOwnerOperation = speedhttp.NewReplyOperation[ownerTransferInput](
	"tenant.admin.owner.transfer",
	http.MethodPut,
	pathOwnerTransferFragment,
	"Transfer the caller's organization to another member",
	"Hands one organization's ownership on, called by the organization's current owner. The tenant surface "+
		"decides by owner identity: the acting account must be the row's OwnerAccount, and no platform "+
		"permission is asked. The target must be an active member of the organization whose account is active; "+
		"the transfer grants it tenant-admin if it does not hold it, and the previous owner loses every binding "+
		"in the organization and is removed from it. The transfer and those writes are one transaction, under "+
		"the tenant lock the platform surface takes too. A mandatory audit row lands before the write; if it "+
		"cannot, nothing is written and the request is answered 503.",
	&speedhttp.RequestBody{
		MediaType:   "application/json",
		Description: "The account identifier of the active member that becomes the owner.",
		Required:    true,
		Name:        "TenantOwnerTransfer",
	},
	[]speedhttp.Response{
		{
			Status:      http.StatusNoContent,
			Description: "The ownership was transferred",
		},
		speedhttp.ProblemResponse(http.StatusBadRequest,
			"The name is outside the slug domain, the request body is not one JSON object, or the account member is absent"),
		speedhttp.ProblemResponse(http.StatusUnauthorized, "The request carries no identity"),
		speedhttp.ProblemResponse(http.StatusForbidden,
			"The caller is not the organization's owner, or the caller has not proved an email address (contact-required)"),
		{
			Status:      http.StatusNotFound,
			Description: "No tenant carries the name, or the path tenant is not the tenant the request resolved to or is the platform domain",
		},
		speedhttp.ProblemResponse(http.StatusConflict,
			"The target is not an active member, the target's account is not active, the tenant is a "+
				"tombstone, or the caller stopped owning the organization before the write committed"),
		speedhttp.ProblemResponse(http.StatusServiceUnavailable, "A dependency could not answer"),
	},
)

// tenantDeleteInput is the request of the tenant-surface delete: the name in
// the path, held to the resolved tenant by the tenant administration guard
// before the handler runs.
type tenantDeleteInput struct {
	ID string `path:"tenant" description:"The tenant's name. The path-tenant layer resolves it to the internal identifier before the handler runs, holds it to the tenant the request resolved to, and refuses the platform domain; a name no tenant carries is a 404."`
}

// tenantDeleteOperation declares DELETE /:tenant on the tenant surface: the
// organization's own owner ends it. Its handler builds the answer because an
// unknown tenant is a bare 404.
var tenantDeleteOperation = speedhttp.NewReplyOperation[tenantDeleteInput](
	"tenant.admin.owner.delete",
	http.MethodDelete,
	pathOrganizationFragment,
	"Delete the caller's own organization",
	"Deletes one organization, called by the organization's current owner. The tenant surface decides by "+
		"owner identity: the acting account must be the row's OwnerAccount, and no platform permission is "+
		"asked. The write is a tombstone: the identifier and the name stay and are never reusable, and the "+
		"organization's memberships, bindings, roles and grants are removed in the same transaction, under the "+
		"same tenant lock the platform surface takes. A protected tenant is refused by the host's write guard. "+
		"A mandatory audit row lands before the write; if it cannot, nothing is written and the request is "+
		"answered 503. A tombstone is a 409: deletion is terminal.",
	nil,
	[]speedhttp.Response{
		{
			Status:      http.StatusNoContent,
			Description: "The organization was deleted",
		},
		speedhttp.ProblemResponse(http.StatusBadRequest,
			"The name is outside the slug domain"),
		speedhttp.ProblemResponse(http.StatusUnauthorized, "The request carries no identity"),
		speedhttp.ProblemResponse(http.StatusForbidden,
			"The caller is not the organization's owner, the caller has not proved an email address "+
				"(contact-required), or the tenant is protected (urn:speed:tenant:protected)"),
		{
			Status:      http.StatusNotFound,
			Description: "No tenant carries the name, or the path tenant is not the tenant the request resolved to or is the platform domain",
		},
		speedhttp.ProblemResponse(http.StatusConflict,
			"The tenant is a tombstone, or the caller stopped owning the organization before the write committed"),
		speedhttp.ProblemResponse(http.StatusServiceUnavailable, "A dependency could not answer"),
	},
)

// operations is this module's declaration of the API it serves. The fragment is
// generated from it; the routes are mounted from the same declarations.
var operations = speedhttp.Operations{
	listOperation,
	createOperation,
	getOperation,
	transferOwnerOperation,
	deleteOperation,
	setProfileOperation,
	setStatusOperation,
}

// tenantOperations is this module's declaration of the tenant-surface API. The
// fragment is generated from it; the routes are mounted from the same
// declarations.
var tenantOperations = speedhttp.Operations{
	tenantTransferOwnerOperation,
	tenantDeleteOperation,
}
