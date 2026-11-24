package admin

import (
	"net/http"

	speedhttp "github.com/vislake/speed/pkg/http"
)

// The fragment's info section.
const (
	fragmentTitle   = "authz.admin"
	fragmentVersion = "1.0.0"
)

// The routes as the fragment writes them, relative to the API root the module's
// Spec declares.
const (
	pathPermissionsFragment = "/permissions"

	pathRolesFragment     = "/tenants/:tenant/roles"
	pathRoleFragment      = "/tenants/:tenant/roles/:role"
	pathGrantsFragment    = "/tenants/:tenant/roles/:role/grants"
	pathGrantFragment     = "/tenants/:tenant/roles/:role/grants/:permission"
	pathBindingsFragment  = "/tenants/:tenant/bindings"
	pathBindingOfFragment = "/tenants/:tenant/bindings/:account"
	pathBindingFragment   = "/tenants/:tenant/bindings/:account/:role"
)

// tenantInput is the request of a route that acts on one tenant's policy data.
type tenantInput struct {
	Tenant string `path:"tenant" description:"The tenant's name whose policy data the route acts on. The platform name layer resolves it to the internal identifier before the handler runs; it names the rows, not the domain the access decision is made in, which is the platform domain."`
}

// roleInput is the request of a route that acts on one role of a tenant.
type roleInput struct {
	Tenant string `path:"tenant" description:"The tenant's name whose policy data the route acts on. The platform name layer resolves it to the internal identifier before the handler runs; it names the rows, not the domain the access decision is made in, which is the platform domain."`
	Role   string `path:"role" description:"The role name within the tenant. Roles are addressed by this natural key; no opaque row identifier is exposed."`
}

// grantInput is the request of a route that acts on one grant.
type grantInput struct {
	Tenant     string `path:"tenant" description:"The tenant's name whose policy data the route acts on. The platform name layer resolves it to the internal identifier before the handler runs; it names the rows, not the domain the access decision is made in, which is the platform domain."`
	Role       string `path:"role" description:"The role name within the tenant. Roles are addressed by this natural key; no opaque row identifier is exposed."`
	Permission string `path:"permission" description:"The permission name as the catalog declares it, for example authz.role.read. It is translated to its (resource, action) pair before the write."`
}

// tenantAccountInput is the request of the read that lists one account's
// bindings within a tenant.
type tenantAccountInput struct {
	Tenant  string `path:"tenant" description:"The tenant's name whose policy data the route acts on. The platform name layer resolves it to the internal identifier before the handler runs; it names the rows, not the domain the access decision is made in, which is the platform domain."`
	Account string `path:"account" description:"The account identifier the binding is addressed by."`
}

// bindingInput is the request of a route that acts on one binding: an account
// holding a role within a tenant.
type bindingInput struct {
	Tenant  string `path:"tenant" description:"The tenant's name whose policy data the route acts on. The platform name layer resolves it to the internal identifier before the handler runs; it names the rows, not the domain the access decision is made in, which is the platform domain."`
	Account string `path:"account" description:"The account identifier the binding is addressed by."`
	Role    string `path:"role" description:"The role name within the tenant. Roles are addressed by this natural key; no opaque row identifier is exposed."`
}

// roleWriteInput is the request of a role write: the role named in the path and
// its description in the body. The builtin mark is not writable here; a member
// sent under that name reaches nothing.
//
// The blank field opens the object: the route's body prescreen drops members the
// declaration does not describe, so the schema must not claim a refusal the
// route does not make.
type roleWriteInput struct {
	_ struct{} `additionalProperties:"true" description:"The body of a role write: the role's description. The builtin mark is not part of the body; a member sent under that name reaches nothing."`

	Tenant string `path:"tenant" description:"The tenant's name whose policy data the route acts on. The platform name layer resolves it to the internal identifier before the handler runs; it names the rows, not the domain the access decision is made in, which is the platform domain."`
	Role   string `path:"role" description:"The role name within the tenant. Roles are addressed by this natural key; no opaque row identifier is exposed."`

	Description *string `json:"description" description:"The role's free-text description, at most 500 characters. Absent leaves the stored text as it is; an explicit empty string clears it; an over-long value is a 400."`
}

// The success bodies. Each is an alias of an anonymous struct, so the fragment
// publishes the wrapper inline rather than as a component of its own.
type (
	// permissionListBody is the 200 answer of the catalog read.
	permissionListBody = struct {
		_ struct{} `description:"The catalog's declared permissions."`

		Permissions []AuthzAdminPermissionView `json:"permissions" required:"true" description:"Every declared permission, one view each; not sorted."`
	}

	// roleListBody is the 200 answer of a role list.
	roleListBody = struct {
		_ struct{} `description:"The tenant's roles."`

		Roles []AuthzAdminRoleView `json:"roles" required:"true" description:"Every role of the tenant, ordered by role name."`
	}

	// grantListBody is the 200 answer of a grant list.
	grantListBody = struct {
		_ struct{} `description:"The role's grants."`

		Grants []AuthzAdminGrantView `json:"grants" required:"true" description:"Every grant the role holds, ordered by resource and then action."`
	}

	// bindingListBody is the 200 answer of a tenant's binding list.
	bindingListBody = struct {
		_ struct{} `description:"The tenant's bindings."`

		Bindings []AuthzAdminBindingView `json:"bindings" required:"true" description:"Every binding of the tenant, ordered by account and then role."`
	}

	// accountBindingListBody is the 200 answer of the read that lists one
	// account's bindings within a tenant.
	accountBindingListBody = struct {
		_ struct{} `description:"The account's bindings within the tenant."`

		Bindings []AuthzAdminBindingView `json:"bindings" required:"true" description:"Every binding the account holds within the tenant, ordered by role name."`
	}
)

// permissionListOperation declares GET /permissions.
var permissionListOperation = speedhttp.NewOperation[struct{}, permissionListBody](
	"authz.admin.permissions.list",
	http.MethodGet,
	pathPermissionsFragment,
	"List every permission the catalog declares",
	"Lists every permission the process's catalog declares, one view each: the union of the permission "+
		"lists the registered modules declared. The order is the catalog's collection order and is not "+
		"sorted. The list is not filtered by which modules this process runs, so a permission may be listed "+
		"that no mounted surface serves.",
	nil,
	[]speedhttp.Response{
		{
			Status:      http.StatusOK,
			Description: "Every declared permission",
			Body:        &speedhttp.Body{MediaType: "application/json"},
		},
		speedhttp.ProblemResponse(http.StatusUnauthorized, "The request carries no identity"),
		speedhttp.ProblemResponse(http.StatusForbidden,
			"The caller holds no role.read in the platform domain"),
		speedhttp.ProblemResponse(http.StatusServiceUnavailable, "A dependency could not answer"),
	},
)

// roleListOperation declares GET /tenants/:tenant/roles.
var roleListOperation = speedhttp.NewOperation[tenantInput, roleListBody](
	"authz.admin.roles.list",
	http.MethodGet,
	pathRolesFragment,
	"List a tenant's roles",
	"Lists every role stored for the tenant, one view each, ordered by role name ascending. Each view carries "+
		"the role's description, how many grants it holds, how many active members hold it (null when the "+
		"assembly delivers no account-identity reader) and whether the caller may manage roles. The tenant is "+
		"the policy domain the rows belong to and is not looked up in a roster: a tenant with no role yields "+
		"an empty list. The list is not paginated.",
	nil,
	[]speedhttp.Response{
		{
			Status:      http.StatusOK,
			Description: "Every role of the tenant",
			Body:        &speedhttp.Body{MediaType: "application/json"},
		},
		speedhttp.ProblemResponse(http.StatusBadRequest, "The tenant is empty"),
		speedhttp.ProblemResponse(http.StatusUnauthorized, "The request carries no identity"),
		speedhttp.ProblemResponse(http.StatusForbidden,
			"The caller holds no role.read in the platform domain"),
		speedhttp.ProblemResponse(http.StatusServiceUnavailable, "A dependency could not answer"),
	},
)

// rolePutOperation declares PUT /tenants/:tenant/roles/:role.
var rolePutOperation = speedhttp.NewOperation[roleWriteInput, struct{}](
	"authz.admin.roles.put",
	http.MethodPut,
	pathRoleFragment,
	"Create or rewrite a role",
	"The request carries the role's free-text description; absent leaves the stored text as it is, and an "+
		"explicit empty string clears it. The builtin mark is not writable here: it is set by the host "+
		"that seeds the policy data, and once set it cannot be cleared through this surface.",
	&speedhttp.RequestBody{
		MediaType:   "application/json",
		Description: "The role's description.",
		Required:    true,
		Name:        "AuthzAdminRoleWrite",
	},
	[]speedhttp.Response{
		{
			Status:      http.StatusNoContent,
			Description: "The role was written",
		},
		speedhttp.ProblemResponse(http.StatusBadRequest,
			"The body is not one object, the tenant is empty, or the description is longer than 500 characters"),
		speedhttp.ProblemResponse(http.StatusUnauthorized, "The request carries no identity"),
		speedhttp.ProblemResponse(http.StatusForbidden,
			"The caller holds no role.manage in the platform domain, or the caller has not proved an email address (contact-required)"),
		speedhttp.ProblemResponse(http.StatusConflict, "The role is builtin and cannot be changed"),
		speedhttp.ProblemResponse(http.StatusServiceUnavailable, "A dependency could not answer"),
	},
)

// roleDeleteOperation declares DELETE /tenants/:tenant/roles/:role.
var roleDeleteOperation = speedhttp.NewOperation[roleInput, struct{}](
	"authz.admin.roles.delete",
	http.MethodDelete,
	pathRoleFragment,
	"Remove a role, revoking its grants and bindings with it",
	"Removes the role and, in one transaction, its permission grants and its account bindings, so no "+
		"grant or binding outlives the role. Removing a role that does not exist succeeds with 204 and "+
		"changes nothing. A builtin role cannot be removed. A mandatory audit row is written before the "+
		"action; if it cannot be written the action is not carried out and the answer is 503.",
	nil,
	[]speedhttp.Response{
		{
			Status:      http.StatusNoContent,
			Description: "The role was removed",
		},
		speedhttp.ProblemResponse(http.StatusBadRequest, "The tenant is empty"),
		speedhttp.ProblemResponse(http.StatusUnauthorized, "The request carries no identity"),
		speedhttp.ProblemResponse(http.StatusForbidden,
			"The caller holds no role.manage in the platform domain, or the caller has not proved an email address (contact-required)"),
		speedhttp.ProblemResponse(http.StatusConflict, "The role is builtin and cannot be removed"),
		speedhttp.ProblemResponse(http.StatusServiceUnavailable, "A dependency could not answer"),
	},
)

// grantListOperation declares GET /tenants/:tenant/roles/:role/grants.
var grantListOperation = speedhttp.NewOperation[roleInput, grantListBody](
	"authz.admin.grants.list",
	http.MethodGet,
	pathGrantsFragment,
	"List every permission grant a role holds",
	"Lists every permission grant the role holds as the (resource, action) pairs the policy engine matches "+
		"on, ordered by resource and then action. A role that holds no grant, or does not exist, yields an "+
		"empty list; the role is not looked up. The list is not paginated.",
	nil,
	[]speedhttp.Response{
		{
			Status:      http.StatusOK,
			Description: "The role's grants",
			Body:        &speedhttp.Body{MediaType: "application/json"},
		},
		speedhttp.ProblemResponse(http.StatusBadRequest, "The tenant is empty"),
		speedhttp.ProblemResponse(http.StatusUnauthorized, "The request carries no identity"),
		speedhttp.ProblemResponse(http.StatusForbidden,
			"The caller holds no role.read in the platform domain"),
		speedhttp.ProblemResponse(http.StatusServiceUnavailable, "A dependency could not answer"),
	},
)

// grantPutOperation declares PUT
// /tenants/:tenant/roles/:role/grants/:permission.
var grantPutOperation = speedhttp.NewOperation[grantInput, struct{}](
	"authz.admin.grants.put",
	http.MethodPut,
	pathGrantFragment,
	"Grant a permission to a role",
	"Grants one permission to a role. The permission in the path is checked against the catalog and "+
		"translated to its (resource, action) pair before anything is written; a permission the catalog "+
		"does not declare, or one with no translation row, is a 400 and no write happens. The role must "+
		"exist in the tenant and must not be builtin: an absent role is a 400, and a builtin one is a 409, "+
		"since its grants cannot be changed. Granting a permission the role already holds succeeds and "+
		"leaves the existing row unchanged. A mandatory audit row is written before the action; if it "+
		"cannot be written the action is not carried out and the answer is 503.",
	nil,
	[]speedhttp.Response{
		{
			Status:      http.StatusNoContent,
			Description: "The permission was granted",
		},
		speedhttp.ProblemResponse(http.StatusBadRequest,
			"The tenant is empty, the named permission is not in the catalog, or the translation has no row for it, or the named role belongs to another tenant"),
		speedhttp.ProblemResponse(http.StatusUnauthorized, "The request carries no identity"),
		speedhttp.ProblemResponse(http.StatusForbidden,
			"The caller holds no role.manage in the platform domain, or the caller has not proved an email address (contact-required)"),
		speedhttp.ProblemResponse(http.StatusConflict,
			"The role is builtin and its grants cannot be changed"),
		speedhttp.ProblemResponse(http.StatusServiceUnavailable, "A dependency could not answer"),
	},
)

// grantDeleteOperation declares DELETE
// /tenants/:tenant/roles/:role/grants/:permission.
var grantDeleteOperation = speedhttp.NewOperation[grantInput, struct{}](
	"authz.admin.grants.delete",
	http.MethodDelete,
	pathGrantFragment,
	"Revoke a permission from a role",
	"Revokes one permission from a role. The permission in the path is translated to its (resource, "+
		"action) pair; a permission with no translation row is a 400. Revoking a grant the role does not "+
		"hold succeeds with 204 and changes nothing, and a role that is not in the tenant is not refused: "+
		"no grant row matches and the answer is also 204. A builtin role's grants cannot be revoked: that "+
		"is a 409. A mandatory audit row is written before the action; if it cannot be written the action "+
		"is not carried out and the answer is 503.",
	nil,
	[]speedhttp.Response{
		{
			Status:      http.StatusNoContent,
			Description: "The permission was revoked",
		},
		speedhttp.ProblemResponse(http.StatusBadRequest,
			"The tenant is empty, or the translation has no row for the named permission"),
		speedhttp.ProblemResponse(http.StatusUnauthorized, "The request carries no identity"),
		speedhttp.ProblemResponse(http.StatusForbidden,
			"The caller holds no role.manage in the platform domain, or the caller has not proved an email address (contact-required)"),
		speedhttp.ProblemResponse(http.StatusConflict,
			"The role is builtin and its grants cannot be revoked"),
		speedhttp.ProblemResponse(http.StatusServiceUnavailable, "A dependency could not answer"),
	},
)

// bindingListOperation declares GET /tenants/:tenant/bindings.
var bindingListOperation = speedhttp.NewOperation[tenantInput, bindingListBody](
	"authz.admin.bindings.list",
	http.MethodGet,
	pathBindingsFragment,
	"List every role binding of a tenant",
	"Lists every role binding of the tenant, ordered by account identifier and then role name, one view "+
		"each. A tenant with no binding yields an empty list. The list is not paginated.",
	nil,
	[]speedhttp.Response{
		{
			Status:      http.StatusOK,
			Description: "The tenant's bindings",
			Body:        &speedhttp.Body{MediaType: "application/json"},
		},
		speedhttp.ProblemResponse(http.StatusBadRequest, "The tenant is empty"),
		speedhttp.ProblemResponse(http.StatusUnauthorized, "The request carries no identity"),
		speedhttp.ProblemResponse(http.StatusForbidden,
			"The caller holds no role.read in the platform domain"),
		speedhttp.ProblemResponse(http.StatusServiceUnavailable, "A dependency could not answer"),
	},
)

// bindingOfOperation declares GET /tenants/:tenant/bindings/:account.
var bindingOfOperation = speedhttp.NewOperation[tenantAccountInput, accountBindingListBody](
	"authz.admin.bindings.of",
	http.MethodGet,
	pathBindingOfFragment,
	"List every binding an account holds within a tenant",
	"Lists every binding the account holds within the tenant, ordered by role name ascending, one view "+
		"each. An account with no binding, or a tenant with none, yields an empty list; the account is not "+
		"looked up. The list is not paginated.",
	nil,
	[]speedhttp.Response{
		{
			Status:      http.StatusOK,
			Description: "The account's bindings within the tenant",
			Body:        &speedhttp.Body{MediaType: "application/json"},
		},
		speedhttp.ProblemResponse(http.StatusBadRequest, "The tenant is empty"),
		speedhttp.ProblemResponse(http.StatusUnauthorized, "The request carries no identity"),
		speedhttp.ProblemResponse(http.StatusForbidden,
			"The caller holds no role.read in the platform domain"),
		speedhttp.ProblemResponse(http.StatusServiceUnavailable, "A dependency could not answer"),
	},
)

// bindingPutOperation declares PUT /tenants/:tenant/bindings/:account/:role.
var bindingPutOperation = speedhttp.NewOperation[bindingInput, struct{}](
	"authz.admin.bindings.put",
	http.MethodPut,
	pathBindingFragment,
	"Bind a role to an account within a tenant",
	"Binds a role to an account within the tenant. The role must exist in the tenant; a builtin role may "+
		"be bound. An empty account is refused with a 400 before anything is written. Binding a role the "+
		"account already holds succeeds and leaves the existing row unchanged. A mandatory audit row is "+
		"written before the action; if it cannot be written the action is not carried out and the answer "+
		"is 503.",
	nil,
	[]speedhttp.Response{
		{
			Status:      http.StatusNoContent,
			Description: "The role was bound",
		},
		speedhttp.ProblemResponse(http.StatusBadRequest,
			"The tenant or the account is empty, no such role, or the named role belongs to another tenant"),
		speedhttp.ProblemResponse(http.StatusUnauthorized, "The request carries no identity"),
		speedhttp.ProblemResponse(http.StatusForbidden,
			"The caller holds no role.manage in the platform domain, or the caller has not proved an email address (contact-required)"),
		speedhttp.ProblemResponse(http.StatusServiceUnavailable, "A dependency could not answer"),
	},
)

// bindingDeleteOperation declares DELETE
// /tenants/:tenant/bindings/:account/:role.
var bindingDeleteOperation = speedhttp.NewOperation[bindingInput, struct{}](
	"authz.admin.bindings.delete",
	http.MethodDelete,
	pathBindingFragment,
	"Remove an account's binding to a role within a tenant",
	"Removes the account's binding to the role within the tenant. An empty account is refused with a 400 "+
		"before anything is written. A caller may not remove its own tenant-admin binding: that is refused "+
		"with a 403 before anything is written, and another tenant administrator removes it. Removing a "+
		"binding that does not exist succeeds with 204 and changes nothing. A mandatory audit row is "+
		"written before the action; if it cannot be written the action is not carried out and the answer is 503.",
	nil,
	[]speedhttp.Response{
		{
			Status:      http.StatusNoContent,
			Description: "The binding was removed",
		},
		speedhttp.ProblemResponse(http.StatusBadRequest, "The account or the tenant is empty"),
		speedhttp.ProblemResponse(http.StatusUnauthorized, "The request carries no identity"),
		speedhttp.ProblemResponse(http.StatusForbidden,
			"The caller holds no role.manage in the platform domain, has not proved an email address "+
				"(contact-required), or is removing its own tenant-admin binding"),
		speedhttp.ProblemResponse(http.StatusServiceUnavailable, "A dependency could not answer"),
	},
)

// operations is this module's declaration of the API it serves.
var operations = speedhttp.Operations{
	permissionListOperation,
	roleListOperation,
	rolePutOperation,
	roleDeleteOperation,
	grantListOperation,
	grantPutOperation,
	grantDeleteOperation,
	bindingListOperation,
	bindingOfOperation,
	bindingPutOperation,
	bindingDeleteOperation,
}
