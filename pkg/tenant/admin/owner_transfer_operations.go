package admin

import (
	"net/http"
	"reflect"

	speedhttp "github.com/vislake/speed/pkg/http"
)

// The owner-transfer routes as the fragments write them. The platform routes
// are relative to the platform API root and the tenant routes to the
// tenants-scope root; the account-level routes are relative to the ordinary
// root, because the target acts by its own identity and no tenant context.
const (
	pathTenantOwnerRequestsFragment = "/tenants/:tenant/owner/requests"
	pathTenantPendingFragment       = "/tenants/:tenant/owner/requests/pending"
	pathTenantRequestFragment       = "/tenants/:tenant/owner/requests/:request"

	pathOwnerRequestsFragment = "/:tenant/owner/requests"
	pathOwnerPendingFragment  = "/:tenant/owner/requests/pending"
	pathOwnerRequestFragment  = "/:tenant/owner/requests/:request"

	pathMyRequestsFragment = "/auth/me/owner-transfer-requests"
	pathMyRequestFragment  = "/auth/me/owner-transfer-requests/:request"
	pathMyConfirmFragment  = "/auth/me/owner-transfer-requests/:request/confirm"
	pathMyDeclineFragment  = "/auth/me/owner-transfer-requests/:request/decline"
)

// The success bodies. Each names the field it carries so the generated client
// has one shape per answer.
type (
	// transferRequestBody is the 201 answer of a request.
	transferRequestBody = struct {
		_       struct{}           `description:"The owner-transfer request as recorded, pending."`
		Request TenantTransferView `json:"request" required:"true" description:"The pending request. Ownership has not changed."`
	}

	// pendingTransferBody is the 200 answer of the pending read. A null request
	// means the organization has none.
	pendingTransferBody = struct {
		_       struct{}            `description:"The organization's pending owner-transfer request, or null."`
		Request *TenantTransferView `json:"request" required:"true" description:"The pending request, or null when the organization has none."`
	}

	// myTransferListBody is the 200 answer of the target's listing.
	myTransferListBody = struct {
		_        struct{}         `description:"Every pending owner-transfer request addressed to the caller."`
		Requests []MyTransferView `json:"requests" required:"true" description:"The caller's pending requests, ordered by request time."`
	}

	// myTransferBody is the 200 answer of one request addressed to the caller.
	myTransferBody = struct {
		_       struct{}       `description:"One owner-transfer request addressed to the caller, in whatever state it is."`
		Request MyTransferView `json:"request" required:"true" description:"The request, with the organization's name and its state."`
	}
)

// The request inputs.

// requestTransferInput is the platform surface's request: the tenant in the path
// and the account that becomes its owner in the body.
type requestTransferInput struct {
	ID      string `path:"tenant" description:"The tenant's name. The platform name layer resolves it to the internal identifier before the handler runs; a name no tenant carries is a 404."`
	Account string `json:"account" required:"true" description:"The account identifier of the active member the request names as the next owner."`
}

// pendingTransferInput is the platform surface's pending read.
type pendingTransferInput struct {
	ID string `path:"tenant" description:"The tenant's name. The platform name layer resolves it to the internal identifier before the handler runs; a name no tenant carries is a 404."`
}

// cancelTransferInput is the platform surface's cancellation.
type cancelTransferInput struct {
	ID      string `path:"tenant" description:"The tenant's name. The platform name layer resolves it to the internal identifier before the handler runs; a name no tenant carries is a 404."`
	Request string `path:"request" description:"The owner-transfer request identifier."`
}

// ownerRequestTransferInput is the tenant surface's request.
type ownerRequestTransferInput struct {
	Tenant  string `path:"tenant" description:"The tenant's name. The path-tenant layer resolves it to the internal identifier before the handler runs, holds it to the tenant the request resolved to, and refuses the platform domain; a name no tenant carries is a 404."`
	Account string `json:"account" required:"true" description:"The account identifier of the active member the request names as the next owner."`
}

// ownerPendingTransferInput is the tenant surface's pending read.
type ownerPendingTransferInput struct {
	Tenant string `path:"tenant" description:"The tenant's name. The path-tenant layer resolves it to the internal identifier before the handler runs, holds it to the tenant the request resolved to, and refuses the platform domain; a name no tenant carries is a 404."`
}

// ownerCancelTransferInput is the tenant surface's cancellation.
type ownerCancelTransferInput struct {
	Tenant  string `path:"tenant" description:"The tenant's name. The path-tenant layer resolves it to the internal identifier before the handler runs, holds it to the tenant the request resolved to, and refuses the platform domain; a name no tenant carries is a 404."`
	Request string `path:"request" description:"The owner-transfer request identifier."`
}

// myTransferInput is one account-level route addressed by request identifier.
type myTransferInput struct {
	Request string `path:"request" description:"The owner-transfer request identifier."`
}

// requestTransferOperation declares POST /tenants/:tenant/owner/requests on the
// platform surface.
var requestTransferOperation = speedhttp.NewReplyOperation[requestTransferInput](
	"tenant.admin.tenants.requestTransfer",
	http.MethodPost,
	pathTenantOwnerRequestsFragment,
	"Request a tenant's ownership transfer",
	"Records a pending request to hand one tenant's ownership to an active member. Ownership does not change "+
		"until the target confirms. The target must be an active member whose account is active; one pending "+
		"request per organization is accepted. A mandatory audit row lands before the write; if it cannot, "+
		"nothing is written and the request is answered 503.",
	&speedhttp.RequestBody{
		MediaType:   "application/json",
		Description: "The account identifier of the active member the request names as the next owner.",
		Required:    true,
		Name:        "TenantAdminTenantOwnerTransferRequest",
	},
	[]speedhttp.Response{
		{
			Status:      http.StatusCreated,
			Description: "The pending request",
			Body:        &speedhttp.Body{MediaType: "application/json", Structure: reflect.TypeOf(transferRequestBody{})},
		},
		speedhttp.ProblemResponse(http.StatusBadRequest,
			"The name is outside the slug domain, the request body is not one JSON object, the account member is absent, or the target is already the owner"),
		speedhttp.ProblemResponse(http.StatusUnauthorized, "The request carries no identity"),
		speedhttp.ProblemResponse(http.StatusForbidden,
			"The caller holds no tenant.manage in the platform domain, the caller has not proved an email address (contact-required), or the organization is protected"),
		{
			Status:      http.StatusNotFound,
			Description: "No tenant carries the name",
		},
		speedhttp.ProblemResponse(http.StatusConflict,
			"The target is not an active member, the target's account is not active, the tenant is a tombstone, "+
				"the organization has no owner, or a request is already pending"),
		speedhttp.ProblemResponse(http.StatusServiceUnavailable, "A dependency could not answer"),
	},
)

// pendingTransferOperation declares GET /tenants/:tenant/owner/requests/pending
// on the platform surface.
var pendingTransferOperation = speedhttp.NewReplyOperation[pendingTransferInput](
	"tenant.admin.tenants.pendingTransfer",
	http.MethodGet,
	pathTenantPendingFragment,
	"Read a tenant's pending ownership-transfer request",
	"Reads the organization's pending owner-transfer request, or null when it has none. An expired request "+
		"is settled as expired before the read, so it is never returned as pending.",
	nil,
	[]speedhttp.Response{
		{
			Status:      http.StatusOK,
			Description: "The pending request, or null",
			Body:        &speedhttp.Body{MediaType: "application/json", Structure: reflect.TypeOf(pendingTransferBody{})},
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

// cancelTransferOperation declares DELETE /tenants/:tenant/owner/requests/:request
// on the platform surface.
var cancelTransferOperation = speedhttp.NewReplyOperation[cancelTransferInput](
	"tenant.admin.tenants.cancelTransfer",
	http.MethodDelete,
	pathTenantRequestFragment,
	"Cancel a tenant's pending ownership-transfer request",
	"Cancels one pending owner-transfer request platform staff names. Ownership does not change. A request "+
		"that is already terminal is answered 409.",
	nil,
	[]speedhttp.Response{
		{
			Status:      http.StatusNoContent,
			Description: "The request was cancelled",
		},
		speedhttp.ProblemResponse(http.StatusBadRequest,
			"The name is outside the slug domain or the request identifier is not well formed"),
		speedhttp.ProblemResponse(http.StatusUnauthorized, "The request carries no identity"),
		speedhttp.ProblemResponse(http.StatusForbidden,
			"The caller holds no tenant.manage in the platform domain"),
		{
			Status:      http.StatusNotFound,
			Description: "No tenant carries the name, or no such request belongs to it",
		},
		speedhttp.ProblemResponse(http.StatusConflict, "The request is no longer pending, or it has expired"),
		speedhttp.ProblemResponse(http.StatusServiceUnavailable, "A dependency could not answer"),
	},
)

// tenantRequestTransferOperation declares POST /:tenant/owner/requests on the
// tenant surface.
var tenantRequestTransferOperation = speedhttp.NewReplyOperation[ownerRequestTransferInput](
	"tenant.admin.owner.requestTransfer",
	http.MethodPost,
	pathOwnerRequestsFragment,
	"Request the caller's organization's ownership transfer",
	"Records a pending request to hand one organization's ownership to an active member, called by the "+
		"organization's current owner. The tenant surface decides by owner identity: the acting account must "+
		"be the row's OwnerAccount, and no platform permission is asked. Ownership does not change until the "+
		"target confirms. A mandatory audit row lands before the write; if it cannot, nothing is written and "+
		"the request is answered 503.",
	&speedhttp.RequestBody{
		MediaType:   "application/json",
		Description: "The account identifier of the active member the request names as the next owner.",
		Required:    true,
		Name:        "TenantOwnerTransferRequest",
	},
	[]speedhttp.Response{
		{
			Status:      http.StatusCreated,
			Description: "The pending request",
			Body:        &speedhttp.Body{MediaType: "application/json", Structure: reflect.TypeOf(transferRequestBody{})},
		},
		speedhttp.ProblemResponse(http.StatusBadRequest,
			"The name is outside the slug domain, the request body is not one JSON object, the account member "+
				"is absent, or the target is already the owner"),
		speedhttp.ProblemResponse(http.StatusUnauthorized, "The request carries no identity"),
		speedhttp.ProblemResponse(http.StatusForbidden,
			"The caller is not the organization's owner, the caller has not proved an email address (contact-required), or the organization is protected"),
		{
			Status:      http.StatusNotFound,
			Description: "No tenant carries the name, or the path tenant is not the tenant the request resolved to or is the platform domain",
		},
		speedhttp.ProblemResponse(http.StatusConflict,
			"The target is not an active member, the target's account is not active, the tenant is a tombstone, "+
				"the organization has no owner, a request is already pending, or the caller stopped owning the organization"),
		speedhttp.ProblemResponse(http.StatusServiceUnavailable, "A dependency could not answer"),
	},
)

// tenantPendingTransferOperation declares GET /:tenant/owner/requests/pending on
// the tenant surface.
var tenantPendingTransferOperation = speedhttp.NewReplyOperation[ownerPendingTransferInput](
	"tenant.admin.owner.pendingTransfer",
	http.MethodGet,
	pathOwnerPendingFragment,
	"Read the caller's organization's pending ownership-transfer request",
	"Reads the organization's pending owner-transfer request, or null when it has none, called by the "+
		"organization's current owner. An expired request is settled as expired before the read.",
	nil,
	[]speedhttp.Response{
		{
			Status:      http.StatusOK,
			Description: "The pending request, or null",
			Body:        &speedhttp.Body{MediaType: "application/json", Structure: reflect.TypeOf(pendingTransferBody{})},
		},
		speedhttp.ProblemResponse(http.StatusUnauthorized, "The request carries no identity"),
		speedhttp.ProblemResponse(http.StatusForbidden,
			"The caller is not the organization's owner"),
		{
			Status:      http.StatusNotFound,
			Description: "No tenant carries the name, or the path tenant is not the tenant the request resolved to or is the platform domain",
		},
		speedhttp.ProblemResponse(http.StatusServiceUnavailable, "A dependency could not answer"),
	},
)

// tenantCancelTransferOperation declares DELETE /:tenant/owner/requests/:request
// on the tenant surface.
var tenantCancelTransferOperation = speedhttp.NewReplyOperation[ownerCancelTransferInput](
	"tenant.admin.owner.cancelTransfer",
	http.MethodDelete,
	pathOwnerRequestFragment,
	"Cancel the caller's organization's pending ownership-transfer request",
	"Cancels one pending owner-transfer request of the caller's organization. Ownership does not change. "+
		"The caller must still be the organization's owner.",
	nil,
	[]speedhttp.Response{
		{
			Status:      http.StatusNoContent,
			Description: "The request was cancelled",
		},
		speedhttp.ProblemResponse(http.StatusBadRequest,
			"The name is outside the slug domain or the request identifier is not well formed"),
		speedhttp.ProblemResponse(http.StatusUnauthorized, "The request carries no identity"),
		speedhttp.ProblemResponse(http.StatusForbidden,
			"The caller is not the organization's owner"),
		{
			Status:      http.StatusNotFound,
			Description: "No tenant carries the name, or no such request belongs to it",
		},
		speedhttp.ProblemResponse(http.StatusConflict, "The request is no longer pending, it has expired, or the caller stopped owning the organization"),
		speedhttp.ProblemResponse(http.StatusServiceUnavailable, "A dependency could not answer"),
	},
)

// myTransferRequestsOperation declares GET /auth/me/owner-transfer-requests.
var myTransferRequestsOperation = speedhttp.NewReplyOperation[struct{}](
	"tenant.admin.me.transferRequests",
	http.MethodGet,
	pathMyRequestsFragment,
	"List the caller's pending ownership-transfer requests",
	"Lists every pending owner-transfer request addressed to the caller, across every organization. The "+
		"caller's own identity is the whole credential: no tenant context and no organization permission is "+
		"asked. Expired requests are settled as expired before the read.",
	nil,
	[]speedhttp.Response{
		{
			Status:      http.StatusOK,
			Description: "The caller's pending requests",
			Body:        &speedhttp.Body{MediaType: "application/json", Structure: reflect.TypeOf(myTransferListBody{})},
		},
		speedhttp.ProblemResponse(http.StatusUnauthorized, "The request carries no identity"),
		speedhttp.ProblemResponse(http.StatusServiceUnavailable, "A dependency could not answer"),
	},
)

// myTransferRequestOperation declares GET /auth/me/owner-transfer-requests/:request.
var myTransferRequestOperation = speedhttp.NewReplyOperation[myTransferInput](
	"tenant.admin.me.transferRequest",
	http.MethodGet,
	pathMyRequestFragment,
	"Read one ownership-transfer request addressed to the caller",
	"Reads one request addressed to the caller in whatever state it is, so a deep link can show a request "+
		"that was already confirmed, declined, cancelled or expired. A request that does not exist and one "+
		"addressed to another account are answered alike with a bare 404.",
	nil,
	[]speedhttp.Response{
		{
			Status:      http.StatusOK,
			Description: "The request",
			Body:        &speedhttp.Body{MediaType: "application/json", Structure: reflect.TypeOf(myTransferBody{})},
		},
		speedhttp.ProblemResponse(http.StatusUnauthorized, "The request carries no identity"),
		{
			Status:      http.StatusNotFound,
			Description: "No such request is addressed to the caller",
		},
		speedhttp.ProblemResponse(http.StatusServiceUnavailable, "A dependency could not answer"),
	},
)

// myTransferConfirmOperation declares POST /auth/me/owner-transfer-requests/:request/confirm.
var myTransferConfirmOperation = speedhttp.NewReplyOperation[myTransferInput](
	"tenant.admin.me.confirmTransfer",
	http.MethodPost,
	pathMyConfirmFragment,
	"Confirm an ownership-transfer request addressed to the caller",
	"Confirms one pending request addressed to the caller and writes the transfer. The request is re-read "+
		"under the organization's lock; the requester must still be the owner and the caller must still be an "+
		"active member, or the request is answered 409. The previous owner stays an active member and keeps "+
		"every binding it held. A mandatory audit row lands before the write; if it cannot, nothing is written "+
		"and the request is answered 503. An unverified self-registration is answered 403 contact-required.",
	nil,
	[]speedhttp.Response{
		{
			Status:      http.StatusNoContent,
			Description: "The ownership was transferred",
		},
		speedhttp.ProblemResponse(http.StatusUnauthorized, "The request carries no identity"),
		speedhttp.ProblemResponse(http.StatusForbidden,
			"The caller has not proved an email address (contact-required)"),
		{
			Status:      http.StatusNotFound,
			Description: "No such request is addressed to the caller",
		},
		speedhttp.ProblemResponse(http.StatusConflict,
			"The request is no longer pending or has expired, the requester no longer owns the organization, "+
				"the organization was deleted, or the caller is no longer an active member"),
		speedhttp.ProblemResponse(http.StatusServiceUnavailable, "A dependency could not answer"),
	},
)

// myTransferDeclineOperation declares POST /auth/me/owner-transfer-requests/:request/decline.
var myTransferDeclineOperation = speedhttp.NewReplyOperation[myTransferInput](
	"tenant.admin.me.declineTransfer",
	http.MethodPost,
	pathMyDeclineFragment,
	"Decline an ownership-transfer request addressed to the caller",
	"Declines one pending request addressed to the caller. Ownership does not change. The caller's own "+
		"identity is the whole credential; the action is harmless and carries no registration gate.",
	nil,
	[]speedhttp.Response{
		{
			Status:      http.StatusNoContent,
			Description: "The request was declined",
		},
		speedhttp.ProblemResponse(http.StatusUnauthorized, "The request carries no identity"),
		{
			Status:      http.StatusNotFound,
			Description: "No such request is addressed to the caller",
		},
		speedhttp.ProblemResponse(http.StatusConflict, "The request is no longer pending or has expired"),
		speedhttp.ProblemResponse(http.StatusServiceUnavailable, "A dependency could not answer"),
	},
)
