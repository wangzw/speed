package tenantadmin

import (
	"net/http"

	speedhttp "github.com/vislake/speed/pkg/http"
)

// The fragment's info section. The title is the module's name and the version
// is the version of the fragment's shape.
const (
	fragmentTitle   = "authz.tenantadmin"
	fragmentVersion = "1.0.0"
)

// The routes as the fragment writes them: relative to the API root the module's
// Spec declares. A tenants-scope fragment names the tenant first, so every path
// begins with its segment. The mounting resolves them to the absolute paths the
// routes are registered at, so a path is written once.
const (
	pathPermissionsFragment = "/:tenant/permissions"

	pathRolesFragment        = "/:tenant/roles"
	pathRoleFragment         = "/:tenant/roles/:role"
	pathRoleBindingsFragment = "/:tenant/roles/:role/bindings"
	pathGrantsFragment       = "/:tenant/roles/:role/grants"
	pathGrantFragment        = "/:tenant/roles/:role/grants/:permission"

	pathBindingsFragment  = "/:tenant/bindings"
	pathBindingOfFragment = "/:tenant/bindings/:account"
	pathBindingFragment   = "/:tenant/bindings/:account/:role"
)

// tenantInput is the request of a route that acts on one tenant's policy data.
// The tenant is the route's policy domain, named in the path; the path-tenant
// layer the tenant capability mounts holds it to the tenant the request
// resolved to before this module's handler runs.
type tenantInput struct {
	Tenant string `path:"tenant" description:"The tenant whose policy data the route acts on. It is the route's policy domain, named in the path; the path-tenant layer holds it to the tenant the request resolved to before the handler runs."`
}

// roleInput is the request of a route that acts on one role of a tenant.
type roleInput struct {
	Tenant string `path:"tenant" description:"The tenant whose policy data the route acts on. It is the route's policy domain, named in the path; the path-tenant layer holds it to the tenant the request resolved to before the handler runs."`
	Role   string `path:"role" description:"The role name within the tenant. Roles are addressed by this natural key; no opaque row identifier is exposed."`
}

// grantInput is the request of a route that acts on one grant: a role and the
// permission it holds.
type grantInput struct {
	Tenant     string `path:"tenant" description:"The tenant whose policy data the route acts on. It is the route's policy domain, named in the path; the path-tenant layer holds it to the tenant the request resolved to before the handler runs."`
	Role       string `path:"role" description:"The role name within the tenant. Roles are addressed by this natural key; no opaque row identifier is exposed."`
	Permission string `path:"permission" description:"The permission name as the catalog declares it, for example authz.tenant.role.read. A grant write accepts only a governed permission; the name is translated to its (resource, action) pair before the write."`
}

// tenantAccountInput is the request of the read that lists one account's
// bindings within a tenant.
type tenantAccountInput struct {
	Tenant  string `path:"tenant" description:"The tenant whose policy data the route acts on. It is the route's policy domain, named in the path; the path-tenant layer holds it to the tenant the request resolved to before the handler runs."`
	Account string `path:"account" description:"The account identifier the binding is addressed by."`
}

// bindingInput is the request of a route that acts on one binding: an account
// holding a role within a tenant.
type bindingInput struct {
	Tenant  string `path:"tenant" description:"The tenant whose policy data the route acts on. It is the route's policy domain, named in the path; the path-tenant layer holds it to the tenant the request resolved to before the handler runs."`
	Account string `path:"account" description:"The account identifier the binding is addressed by."`
	Role    string `path:"role" description:"The role name within the tenant. Roles are addressed by this natural key; no opaque row identifier is exposed."`
}

// roleBindingListInput is the request of the role usage read: the role named in
// the path and the page cursor.
type roleBindingListInput struct {
	Tenant string `path:"tenant" description:"The tenant whose policy data the route acts on. It is the route's policy domain, named in the path; the path-tenant layer holds it to the tenant the request resolved to before the handler runs."`
	Role   string `path:"role" description:"The role name within the tenant. Roles are addressed by this natural key; no opaque row identifier is exposed."`
	After  string `query:"after" description:"The next_cursor of the previous page, or empty for the first page. The cursor is the account identifier of the last row on that page, and the page is read by it rather than by an offset."`
	Limit  int    `query:"limit" minimum:"0" description:"The page's maximum size. Absent is 50; a value above 200 is capped at 200; a negative value is a 400."`
}

// roleWriteInput is the request of a role write: the role named in the path and
// the role's description in the body. The builtin mark is not part of the
// request — a client cannot set it, and the field a client sends for it reaches
// nothing. The blank field publishes the body schema's description and opens the
// object: the route's body prescreen drops the members the declaration does not
// describe instead of refusing them.
//
// The free-text description is preferred over the legacy i18n key when both are
// present. The key stays accepted so a client written before the change keeps
// working. The role name is not a body member: the name is the binding's key and
// a role is never renamed.
type roleWriteInput struct {
	_ struct{} `additionalProperties:"true" description:"The body of a role write: the role's description. The builtin mark and the role name are not part of the body; a member sent under those names reaches nothing."`

	Tenant string `path:"tenant" description:"The tenant whose policy data the route acts on. It is the route's policy domain, named in the path; the path-tenant layer holds it to the tenant the request resolved to before the handler runs."`
	Role   string `path:"role" description:"The role name within the tenant. Roles are addressed by this natural key; no opaque row identifier is exposed."`

	DescriptionKey string `json:"description_key" description:"Deprecated: the i18n key of the role's description text, accepted for a client that still sends it. description wins when both are present."`

	Description *string `json:"description" description:"The role's free-text description, at most 500 characters. It wins over description_key when present. An over-long value is a 400."`
}

// The success bodies. Each is an alias of an anonymous struct, so the fragment
// publishes the wrapper inline rather than as a component of its own.
type (
	// permissionListBody is the 200 answer of the catalog read.
	permissionListBody = struct {
		_ struct{} `description:"The tenant-governed permissions."`

		Permissions []AuthzTenantAdminPermissionView `json:"permissions" required:"true" description:"Every permission of the governed set, one view each, in permission-name order."`
	}

	// roleListBody is the 200 answer of a role list.
	roleListBody = struct {
		_ struct{} `description:"The tenant's roles."`

		Roles []AuthzTenantAdminRoleView `json:"roles" required:"true" description:"Every role of the tenant, ordered by role name."`
	}

	// roleBindingPageBody is the 200 answer of the role usage read.
	roleBindingPageBody = struct {
		_ struct{} `description:"One page of the accounts a role is bound to."`

		Bindings   []AuthzTenantAdminRoleBindingView `json:"bindings" required:"true" description:"The page's bindings, ordered by account identifier."`
		NextCursor string                            `json:"next_cursor" required:"true" description:"The account to pass as after on the next request; empty when this page is the last."`
	}

	// grantListBody is the 200 answer of a grant list.
	grantListBody = struct {
		_ struct{} `description:"The role's grants."`

		Grants []AuthzTenantAdminGrantView `json:"grants" required:"true" description:"Every grant the role holds, ordered by resource and then action."`
	}

	// bindingListBody is the 200 answer of a tenant's binding list.
	bindingListBody = struct {
		_ struct{} `description:"The tenant's bindings."`

		Bindings []AuthzTenantAdminBindingView `json:"bindings" required:"true" description:"Every binding of the tenant, ordered by account and then role."`
	}

	// accountBindingListBody is the 200 answer of the read that lists one
	// account's bindings within a tenant.
	accountBindingListBody = struct {
		_ struct{} `description:"The account's bindings within the tenant."`

		Bindings []AuthzTenantAdminBindingView `json:"bindings" required:"true" description:"Every binding the account holds within the tenant, ordered by role name."`
	}
)

// permissionListOperation declares GET /:tenant/permissions: every permission
// of the governed set.
var permissionListOperation = speedhttp.NewOperation[tenantInput, permissionListBody](
	"authz.tenantadmin.permissions.list",
	http.MethodGet,
	pathPermissionsFragment,
	"List every permission a tenant administrator may hold and grant",
	"Lists the governed set — the permissions a tenant administrator may hold and grant — one view each, in "+
		"permission-name order. A permission outside the governed set is not listed and cannot be written "+
		"through the grant routes.",
	nil,
	[]speedhttp.Response{
		{
			Status:      http.StatusOK,
			Description: "Every governed permission",
			Body:        &speedhttp.Body{MediaType: "application/json"},
		},
		speedhttp.ProblemResponse(http.StatusNotFound, "The path tenant is not the tenant the request resolved to"),
		speedhttp.ProblemResponse(http.StatusUnauthorized, "The request carries no identity"),
		speedhttp.ProblemResponse(http.StatusForbidden,
			"The caller holds no authz.tenant.role.read in the tenant's domain"),
		speedhttp.ProblemResponse(http.StatusServiceUnavailable, "A dependency could not answer"),
	},
)

// roleListOperation declares GET /:tenant/roles: every role of the tenant.
var roleListOperation = speedhttp.NewOperation[tenantInput, roleListBody](
	"authz.tenantadmin.roles.list",
	http.MethodGet,
	pathRolesFragment,
	"List a tenant's roles",
	"Lists every role stored for the tenant, one view each, ordered by role name ascending. A tenant with no "+
		"role yields an empty list. The list is not paginated.",
	nil,
	[]speedhttp.Response{
		{
			Status:      http.StatusOK,
			Description: "Every role of the tenant",
			Body:        &speedhttp.Body{MediaType: "application/json"},
		},
		speedhttp.ProblemResponse(http.StatusBadRequest, "The tenant is empty"),
		speedhttp.ProblemResponse(http.StatusNotFound, "The path tenant is not the tenant the request resolved to"),
		speedhttp.ProblemResponse(http.StatusUnauthorized, "The request carries no identity"),
		speedhttp.ProblemResponse(http.StatusForbidden,
			"The caller holds no authz.tenant.role.read in the tenant's domain"),
		speedhttp.ProblemResponse(http.StatusServiceUnavailable, "A dependency could not answer"),
	},
)

// roleBindingListOperation declares GET /:tenant/roles/:role/bindings: one page
// of the accounts the role is bound to.
var roleBindingListOperation = speedhttp.NewOperation[roleBindingListInput, roleBindingPageBody](
	"authz.tenantadmin.roles.bindings.list",
	http.MethodGet,
	pathRoleBindingsFragment,
	"List the accounts holding a role",
	"Lists one page of the accounts the role is bound to within the tenant, ordered by account identifier, "+
		"each with its display name, whether it is active and when the binding was written. The display name "+
		"and the activity state come from the host's account identity reader; an assembly that delivers none "+
		"answers an empty name and an unknown activity state. A role with no binding yields an empty page. "+
		"The list is paginated by the account cursor: pass the previous next_cursor as after.",
	nil,
	[]speedhttp.Response{
		{
			Status:      http.StatusOK,
			Description: "One page of the role's bindings",
			Body:        &speedhttp.Body{MediaType: "application/json"},
		},
		speedhttp.ProblemResponse(http.StatusBadRequest, "The tenant is empty, or limit is negative"),
		speedhttp.ProblemResponse(http.StatusNotFound, "The path tenant is not the tenant the request resolved to"),
		speedhttp.ProblemResponse(http.StatusUnauthorized, "The request carries no identity"),
		speedhttp.ProblemResponse(http.StatusForbidden,
			"The caller holds no authz.tenant.role.read in the tenant's domain"),
		speedhttp.ProblemResponse(http.StatusServiceUnavailable, "A dependency could not answer"),
	},
)

// rolePutOperation declares PUT /:tenant/roles/:role: it creates or rewrites a
// role without the builtin mark. A reserved role name is refused.
var rolePutOperation = speedhttp.NewOperation[roleWriteInput, struct{}](
	"authz.tenantadmin.roles.put",
	http.MethodPut,
	pathRoleFragment,
	"Create or rewrite a role",
	"The request carries the role's description, free text or the legacy i18n key; description wins when "+
		"both are present, and a description longer than 500 characters is a 400. The role name is not "+
		"writable: a role is never renamed, because the name is the key a binding holds. The builtin mark "+
		"is not writable here either. platform-admin and tenant-admin are reserved: the tenant surface "+
		"cannot create, rewrite or delete either role row. A mandatory audit row is written before the "+
		"action; if it cannot be written the action is not carried out and the answer is 503.",
	&speedhttp.RequestBody{
		MediaType:   "application/json",
		Description: "The role's description.",
		Required:    true,
		Name:        "AuthzTenantAdminRoleWrite",
	},
	[]speedhttp.Response{
		{
			Status:      http.StatusNoContent,
			Description: "The role was written",
		},
		speedhttp.ProblemResponse(http.StatusBadRequest, "The body is not one object, or the tenant is empty"),
		speedhttp.ProblemResponse(http.StatusNotFound, "The path tenant is not the tenant the request resolved to"),
		speedhttp.ProblemResponse(http.StatusUnauthorized, "The request carries no identity"),
		speedhttp.ProblemResponse(http.StatusForbidden,
			"The caller holds no authz.tenant.role.manage in the tenant's domain, has not proved an email address (contact-required), or named a reserved role (protected)"),
		speedhttp.ProblemResponse(http.StatusConflict, "The role is builtin and cannot be changed"),
		speedhttp.ProblemResponse(http.StatusServiceUnavailable, "A dependency could not answer"),
	},
)

// roleDeleteOperation declares DELETE /:tenant/roles/:role: it removes a role,
// which revokes its grants and bindings with it. A reserved role name is
// refused.
var roleDeleteOperation = speedhttp.NewOperation[roleInput, struct{}](
	"authz.tenantadmin.roles.delete",
	http.MethodDelete,
	pathRoleFragment,
	"Remove a role, revoking its grants and bindings with it",
	"Removes the role and, in one transaction, its permission grants and its account bindings, so no "+
		"grant or binding outlives the role. platform-admin and tenant-admin are reserved: the tenant surface "+
		"cannot remove either role row. A mandatory audit row is written before the action; if it cannot be "+
		"written the action is not carried out and the answer is 503.",
	nil,
	[]speedhttp.Response{
		{
			Status:      http.StatusNoContent,
			Description: "The role was removed",
		},
		speedhttp.ProblemResponse(http.StatusBadRequest, "The tenant is empty"),
		speedhttp.ProblemResponse(http.StatusNotFound, "The path tenant is not the tenant the request resolved to"),
		speedhttp.ProblemResponse(http.StatusUnauthorized, "The request carries no identity"),
		speedhttp.ProblemResponse(http.StatusForbidden,
			"The caller holds no authz.tenant.role.manage in the tenant's domain, has not proved an email address (contact-required), or named a reserved role (protected)"),
		speedhttp.ProblemResponse(http.StatusConflict, "The role is builtin and cannot be removed"),
		speedhttp.ProblemResponse(http.StatusServiceUnavailable, "A dependency could not answer"),
	},
)

// grantListOperation declares GET /:tenant/roles/:role/grants: every permission
// grant the role holds.
var grantListOperation = speedhttp.NewOperation[roleInput, grantListBody](
	"authz.tenantadmin.grants.list",
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
		speedhttp.ProblemResponse(http.StatusNotFound, "The path tenant is not the tenant the request resolved to"),
		speedhttp.ProblemResponse(http.StatusUnauthorized, "The request carries no identity"),
		speedhttp.ProblemResponse(http.StatusForbidden,
			"The caller holds no authz.tenant.role.read in the tenant's domain"),
		speedhttp.ProblemResponse(http.StatusServiceUnavailable, "A dependency could not answer"),
	},
)

// grantPutOperation declares PUT /:tenant/roles/:role/grants/:permission: it
// grants a governed permission to a role the caller may raise.
var grantPutOperation = speedhttp.NewOperation[grantInput, struct{}](
	"authz.tenantadmin.grants.put",
	http.MethodPut,
	pathGrantFragment,
	"Grant a governed permission to a role",
	"Grants one permission to a role. The permission must be in the governed set and the caller must hold "+
		"it; a permission outside the governed set is a 400, and one the caller does not hold is a refusal "+
		"with the protected code. platform-admin and tenant-admin are reserved: their grants cannot be "+
		"changed here. A mandatory audit row is written before the action; if it cannot be written the "+
		"action is not carried out and the answer is 503.",
	nil,
	[]speedhttp.Response{
		{
			Status:      http.StatusNoContent,
			Description: "The permission was granted",
		},
		speedhttp.ProblemResponse(http.StatusBadRequest,
			"The tenant is empty, the permission is not in the governed set, or the translation has no row for it"),
		speedhttp.ProblemResponse(http.StatusNotFound, "The path tenant is not the tenant the request resolved to"),
		speedhttp.ProblemResponse(http.StatusUnauthorized, "The request carries no identity"),
		speedhttp.ProblemResponse(http.StatusForbidden,
			"The caller holds no authz.tenant.role.manage in the tenant's domain, has not proved an email address (contact-required), does not hold the permission it is granting, or named a reserved role (protected)"),
		speedhttp.ProblemResponse(http.StatusConflict, "The role is builtin and its grants cannot be changed"),
		speedhttp.ProblemResponse(http.StatusServiceUnavailable, "A dependency could not answer"),
	},
)

// grantDeleteOperation declares DELETE /:tenant/roles/:role/grants/:permission:
// it revokes a permission from a role.
var grantDeleteOperation = speedhttp.NewOperation[grantInput, struct{}](
	"authz.tenantadmin.grants.delete",
	http.MethodDelete,
	pathGrantFragment,
	"Revoke a permission from a role",
	"Revokes one permission from a role. platform-admin and tenant-admin are reserved: their grants cannot "+
		"be changed here. A mandatory audit row is written before the action; if it cannot be written the "+
		"action is not carried out and the answer is 503.",
	nil,
	[]speedhttp.Response{
		{
			Status:      http.StatusNoContent,
			Description: "The permission was revoked",
		},
		speedhttp.ProblemResponse(http.StatusBadRequest,
			"The tenant is empty, or the translation has no row for the named permission"),
		speedhttp.ProblemResponse(http.StatusNotFound, "The path tenant is not the tenant the request resolved to"),
		speedhttp.ProblemResponse(http.StatusUnauthorized, "The request carries no identity"),
		speedhttp.ProblemResponse(http.StatusForbidden,
			"The caller holds no authz.tenant.role.manage in the tenant's domain, has not proved an email address (contact-required), or named a reserved role (protected)"),
		speedhttp.ProblemResponse(http.StatusConflict, "The role is builtin and its grants cannot be revoked"),
		speedhttp.ProblemResponse(http.StatusServiceUnavailable, "A dependency could not answer"),
	},
)

// bindingListOperation declares GET /:tenant/bindings: every role binding of
// the tenant.
var bindingListOperation = speedhttp.NewOperation[tenantInput, bindingListBody](
	"authz.tenantadmin.bindings.list",
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
		speedhttp.ProblemResponse(http.StatusNotFound, "The path tenant is not the tenant the request resolved to"),
		speedhttp.ProblemResponse(http.StatusUnauthorized, "The request carries no identity"),
		speedhttp.ProblemResponse(http.StatusForbidden,
			"The caller holds no authz.tenant.role.read in the tenant's domain"),
		speedhttp.ProblemResponse(http.StatusServiceUnavailable, "A dependency could not answer"),
	},
)

// bindingOfOperation declares GET /:tenant/bindings/:account: every binding the
// account holds within the tenant.
var bindingOfOperation = speedhttp.NewOperation[tenantAccountInput, accountBindingListBody](
	"authz.tenantadmin.bindings.of",
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
		speedhttp.ProblemResponse(http.StatusNotFound, "The path tenant is not the tenant the request resolved to"),
		speedhttp.ProblemResponse(http.StatusUnauthorized, "The request carries no identity"),
		speedhttp.ProblemResponse(http.StatusForbidden,
			"The caller holds no authz.tenant.role.read in the tenant's domain"),
		speedhttp.ProblemResponse(http.StatusServiceUnavailable, "A dependency could not answer"),
	},
)

// bindingPutOperation declares PUT /:tenant/bindings/:account/:role: it binds a
// role to an account within the tenant.
var bindingPutOperation = speedhttp.NewOperation[bindingInput, struct{}](
	"authz.tenantadmin.bindings.put",
	http.MethodPut,
	pathBindingFragment,
	"Bind a role to an account within a tenant",
	"Binds a role to an account within the tenant. The account must be an active member of the tenant, and "+
		"the caller's own permissions must cover every permission the role holds. An empty account is "+
		"refused with a 400. platform-admin is reserved and cannot be bound here; tenant-admin may be bound "+
		"to another account, subject to the same subset rule. A mandatory audit row is written before the "+
		"action; if it cannot be written the action is not carried out and the answer is 503.",
	nil,
	[]speedhttp.Response{
		{
			Status:      http.StatusNoContent,
			Description: "The role was bound",
		},
		speedhttp.ProblemResponse(http.StatusBadRequest,
			"The tenant or the account is empty, no such role, or the account is not an active member of the tenant"),
		speedhttp.ProblemResponse(http.StatusNotFound, "The path tenant is not the tenant the request resolved to"),
		speedhttp.ProblemResponse(http.StatusUnauthorized, "The request carries no identity"),
		speedhttp.ProblemResponse(http.StatusForbidden,
			"The caller holds no authz.tenant.role.manage in the tenant's domain, has not proved an email address (contact-required), the role is higher than the caller's own permissions, or the role is platform-admin (protected)"),
		speedhttp.ProblemResponse(http.StatusServiceUnavailable, "A dependency could not answer"),
	},
)

// bindingDeleteOperation declares DELETE /:tenant/bindings/:account/:role: it
// removes an account's binding to a role within the tenant.
var bindingDeleteOperation = speedhttp.NewOperation[bindingInput, struct{}](
	"authz.tenantadmin.bindings.delete",
	http.MethodDelete,
	pathBindingFragment,
	"Remove an account's binding to a role within a tenant",
	"Removes the account's binding to the role within the tenant. An empty account is refused with a 400. "+
		"platform-admin is reserved and cannot be removed here. A caller may not remove its own tenant-admin "+
		"binding: that is refused with the protected code, and another tenant administrator removes it. The "+
		"last tenant administrator of the tenant cannot be removed. A mandatory audit row is written before "+
		"the action; if it cannot be written the action is not carried out and the answer is 503.",
	nil,
	[]speedhttp.Response{
		{
			Status:      http.StatusNoContent,
			Description: "The binding was removed",
		},
		speedhttp.ProblemResponse(http.StatusBadRequest, "The account or the tenant is empty"),
		speedhttp.ProblemResponse(http.StatusNotFound, "The path tenant is not the tenant the request resolved to"),
		speedhttp.ProblemResponse(http.StatusUnauthorized, "The request carries no identity"),
		speedhttp.ProblemResponse(http.StatusForbidden,
			"The caller holds no authz.tenant.role.manage in the tenant's domain, has not proved an email address (contact-required), is removing its own tenant-admin binding, or named a reserved role (protected)"),
		speedhttp.ProblemResponse(http.StatusConflict, "The binding is the last tenant administrator of the tenant"),
		speedhttp.ProblemResponse(http.StatusServiceUnavailable, "A dependency could not answer"),
	},
)

// operations is this module's declaration of the API it serves. The fragment is
// generated from it; the routes are mounted from the same declarations.
var operations = speedhttp.Operations{
	permissionListOperation,
	roleListOperation,
	roleBindingListOperation,
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
