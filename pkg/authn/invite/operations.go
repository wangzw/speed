package invite

import (
	"net/http"
	"reflect"
	"time"

	speedhttp "github.com/vislake/speed/pkg/http"
)

// The fragment's info section: the title this module's fragment carries and the
// version of the fragment's shape. Both are the ones the hand-written fragment
// published before the declarations generated it.
const (
	fragmentTitle   = "authn invite"
	fragmentVersion = "1.0.0"
)

// The routes as the fragment writes them: relative to the module's API root,
// which is the mounter's base less the part this capability owns. The mounter
// registration resolves the same declaration against its base, so the path is
// written once.
const (
	invitationsPath = "/:tenant/invitations"
	invitationPath  = "/:tenant/invitations/:id"
	acceptPath      = "/auth/invitations/:token"
)

// AuthnInviteInvitation is one invitation as the management face shows it. It
// never carries the acceptance token.
type AuthnInviteInvitation struct {
	_ struct{} `description:"One invitation as the management face shows it. It never carries the acceptance token."`

	ID         string    `json:"id" required:"true" description:"The invitation's opaque identifier, used to revoke it."`
	Tenant     string    `json:"tenant" required:"true" description:"The name of the tenant the invitation grants a membership in."`
	Email      string    `json:"email" required:"true" description:"The normalized address"`
	Status     string    `json:"status" required:"true" enum:"pending,accepted,revoked" description:"The stored state. Expiry is not stored: an expired pending invitation still reads pending."`
	InvitedBy  string    `json:"invited_by" required:"true" description:"The account identifier of the issuer; empty when a host command issued the invitation without an HTTP identity."`
	ExpiresAt  time.Time `json:"expires_at" required:"true" description:"RFC 3339 deadline in UTC; a pending invitation at or past it can no longer be accepted."`
	AcceptedAt time.Time `json:"accepted_at" required:"true" description:"The zero instant 0001-01-01T00:00:00Z when the invitation has not been accepted"`
	CreatedAt  time.Time `json:"created_at" required:"true" description:"RFC 3339 timestamp in UTC of issuance."`
}

// AuthnInviteIssued is the answer of an issue: the invitation's view, the
// outcome of the notice, and the one-time acceptance token only when the notice
// did not reach the address.
type AuthnInviteIssued struct {
	_ struct{} `description:"The answer of an issue: the invitation's view, the outcome of the notice, and the one-time acceptance token only when the notice did not reach the address."`

	Invitation    AuthnInviteInvitation `json:"invitation" required:"true" description:"The issued invitation; a token is delivered beside it only when the notice did not reach the address."`
	Token         string                `json:"token,omitempty" description:"The acceptance token, present only when email_delivery is failed or not_configured: the inviter has to hand the link over. Absent when the notice was sent, because the recipient already holds it."`
	EmailDelivery string                `json:"email_delivery" required:"true" enum:"sent,failed,not_configured" description:"How the acceptance notice ended: sent when the carrier accepted it, failed when delivery failed or its outcome is unknown, and not_configured when the assembly delivers no mail. A failed notice does not undo the invitation, and the token beside it is still the one-time link; a sent notice carries no token in this answer."`
}

// AuthnInviteView is the answer of the acceptance read. A redeemable invitation
// carries its summary; one that is expired, accepted or revoked carries the
// state alone, without the invited address, the tenant's name or the deadline.
type AuthnInviteView struct {
	_ struct{} `description:"The answer of the acceptance read: the invitation's state, and its summary only while it can still be redeemed."`

	State      string                 `json:"state" required:"true" enum:"pending,expired,accepted,revoked" description:"The state the read judges. Only pending carries the invitation summary: expired, accepted and revoked answer the state alone, without the invited address, the tenant's name or the deadline."`
	Invitation *AuthnInviteInvitation `json:"invitation,omitempty" description:"The invitation summary, present only for a pending invitation that can still be redeemed."`
}

// AuthnInvitePage is one page of a tenant's invitations together with the cursor
// of the next page.
type AuthnInvitePage struct {
	_ struct{} `description:"One page of a tenant's invitations together with the cursor of the next page."`

	Invitations []AuthnInviteInvitation `json:"invitations" required:"true" description:"The invitations of this page, ordered by status ascending (pending, accepted, revoked) and then newest first with the identifier as the tie-break."`
	NextCursor  string                  `json:"next_cursor" required:"true" description:"The cursor of the next page, empty on the last page"`
}

// AuthnInviteAccept is the membership an acceptance wrote.
type AuthnInviteAccept struct {
	_ struct{} `description:"The membership an acceptance wrote."`

	Account   string `json:"account" required:"true" description:"Identifier of the authenticated account the membership belongs to."`
	Tenant    string `json:"tenant" required:"true" description:"The name of the tenant the membership was written in."`
	Active    bool   `json:"active" required:"true" description:"Whether the membership is active; a suspended membership is refused rather than reactivated."`
	Source    string `json:"source" required:"true" description:"The membership's source: invite when this acceptance created it, the source it already had when it was there."`
	SSOExempt bool   `json:"sso_exempt" required:"true" description:"Whether the membership is exempt from SSO enforcement; an existing exemption is preserved."`
}

// issueInput is the request of an issuance: the tenant from the path and the
// address and optional deadline from the JSON body. ExpiresAt is a pointer so
// that an absent member and the zero instant are told apart before the domain
// call reads both as "use the configured lifetime".
type issueInput struct {
	_ struct{} `description:"The issue request: the invited address, which is normalized before it is stored, and an optional deadline."`

	Tenant    string     `path:"tenant" description:"The tenant's name the invitation belongs to. The path-tenant layer resolves it to the internal identifier before the handler runs and holds it to the tenant the request resolved to; the authn.membership.manage decision is made in that tenant."`
	Email     string     `json:"email" required:"true" description:"The invited address as given; it is trimmed, lower-cased and must be printable ASCII with exactly one @, at most 254 bytes."`
	ExpiresAt *time.Time `json:"expires_at" description:"Absolute deadline in RFC 3339. Omitted or the zero instant takes the configured lifetime; a value not later than the current time is refused with 400."`
}

// listInput is the request of a page read: the tenant from the path and the
// cursor, page cap and status from the query.
type listInput struct {
	Tenant string `path:"tenant" description:"The tenant's name the invitation belongs to. The path-tenant layer resolves it to the internal identifier before the handler runs and holds it to the tenant the request resolved to; the authn.membership.manage decision is made in that tenant."`
	Limit  int    `query:"limit" description:"Maximum number of invitations to return. Omitted or 0 serves 100; a value above 1000 is capped at 1000. A negative or non-integer value is refused with 400."`
	After  string `query:"after" description:"Opaque cursor from a previous page's next_cursor; omit it to start at the first page. A value that does not decode is refused with 400 rather than served as the first page."`
	Status string `query:"status" enum:"pending,expired,accepted,revoked" description:"Narrow the page to one view: pending is a pending invitation whose deadline has not passed, expired is a pending invitation whose deadline has passed, and accepted and revoked are the terminal stored states. Omitted lists every state. A value outside this set is refused with 400. Expiry is derived from the deadline, not stored."`
}

// revokeInput is the request of a revocation: the tenant and the invitation's
// identifier, both path parameters.
type revokeInput struct {
	Tenant string `path:"tenant" description:"The tenant's name the invitation belongs to. The path-tenant layer resolves it to the internal identifier before the handler runs and holds it to the tenant the request resolved to; the authn.membership.manage decision is made in that tenant."`
	ID     string `path:"id" description:"The invitation's opaque identifier, which also serves as the decision's target."`
}

// acceptViewInput is the request of the acceptance read: the token, which is
// the whole credential on the public route.
type acceptViewInput struct {
	Token string `path:"token" description:"The acceptance token, prefixed spd_inv_, which identifies the invitation and is the whole credential on this public route."`
}

// acceptInput is the request of an acceptance: the token from the path and a
// body the acceptance no longer reads. The account-opening fields stay in the
// shape so an older client keeps working; acceptance writes a membership for the
// authenticated caller and never opens an account.
type acceptInput struct {
	_ struct{} `description:"The acceptance request; only the token is read."`

	Token       string `path:"token" description:"The acceptance token, prefixed spd_inv_, which identifies the invitation. It grants a membership only together with an authenticated caller who proves the invited address."`
	Username    string `json:"username" description:"Ignored: acceptance never opens an account. Kept so an older client keeps working."`
	DisplayName string `json:"display_name" description:"Ignored: acceptance never opens an account. Kept so an older client keeps working."`
	Password    string `json:"password" description:"Ignored: acceptance never opens an account. Kept so an older client keeps working."`
}

// challengeHeader declares the header every 401 on a management route carries:
// the gateway writes the endpoint's challenge when it refuses the request first,
// and a handler that answers 401 itself returns it in the fault's headers.
var challengeHeader = speedhttp.Header{
	Name:        "WWW-Authenticate",
	Description: "The capability's challenge: the Bearer and Cookie schemes, never Basic.",
}

// unauthorized is the declaration of a 401 that carries the capability's
// challenge.
func unauthorized(description string) speedhttp.Response {
	response := speedhttp.ProblemResponse(http.StatusUnauthorized, description)
	response.Headers = []speedhttp.Header{challengeHeader}
	return response
}

// throttledResponse is the platform-standard 429 of a spent send or probe
// budget. The handler's fault supplies the value; the declaration publishes the
// header on the contract, and the entry point refuses a header it did not
// declare.
func throttledResponse() speedhttp.Response {
	response := speedhttp.ProblemResponse(http.StatusTooManyRequests,
		"A send or acceptance-probe budget is spent; Retry-After names the seconds until the window opens again")
	response.Headers = []speedhttp.Header{{
		Name:        "Retry-After",
		Description: "The number of seconds until the counting window opens again",
		Value:       "1",
	}}
	return response
}

// managementFailures are the answers every management route shares: the gateway
// refuses a request that carries no usable credential before the handler runs.
var managementFailures = []speedhttp.Response{
	unauthorized("No usable credential"),
}

// The answers the management routes share beyond the credential check.
const (
	descriptionForbiddenTenant = "The caller may not manage memberships in the tenant"
	descriptionUnavailable     = "A dependency could not answer"
	descriptionNotJSON         = "The request body is not application/json"
)

// issueOperation declares the route that issues an invitation. The handler
// receives the tenant and the body through its input and returns the issued
// view, so the entry point writes the declared 201.
var issueOperation = speedhttp.NewOperation[issueInput, AuthnInviteIssued](
	"authn.invite.issue",
	http.MethodPost,
	invitationsPath,
	"Issue an invitation to an address in a tenant",
	"Normalizes the address (trimmed, lower-cased printable ASCII with exactly one @, at most 254 "+
		"bytes), then issues a pending invitation. A pending invitation for the same tenant and address "+
		"is superseded in the same transaction, so its token stops working. expires_at is optional: "+
		"when absent, the zero instant or the configured lifetime applies (168 hours by default), and "+
		"a deadline not later than the current time is refused. When the assembly has an invitation "+
		"notifier, the acceptance notice is delivered to the address and email_delivery reports "+
		"sent, failed or not_configured; a failed notice does not undo the invitation, which is still "+
		"answered 201, with its one-time token only when the notice was not sent. Requires the "+
		"authn.membership.manage decision in the "+
		"tenant; failure to write the mandatory audit row answers 503 without issuing.",
	&speedhttp.RequestBody{
		MediaType:   "application/json",
		Description: "The address to invite and an optional absolute deadline. Only email is required.",
		Required:    true,
	},
	speedhttp.Failures(managementFailures,
		speedhttp.ProblemResponseOf(http.StatusBadRequest,
			"The body could not be read, or the address is malformed", typePrefix+"bad-request"),
		speedhttp.ProblemResponseOf(http.StatusUnsupportedMediaType, descriptionNotJSON, typePrefix+"unsupported-media-type"),
		speedhttp.ProblemResponse(http.StatusForbidden, descriptionForbiddenTenant),
		throttledResponse(),
		speedhttp.ProblemResponse(http.StatusServiceUnavailable, descriptionUnavailable),
		speedhttp.Response{
			Status:      http.StatusCreated,
			Description: "The invitation view and the notice outcome; the one-time token only when the notice was not sent",
			Body:        &speedhttp.Body{MediaType: "application/json"},
		},
	),
)

// listOperation declares the route that reads one page of a tenant's
// invitations.
var listOperation = speedhttp.NewOperation[listInput, AuthnInvitePage](
	"authn.invite.list",
	http.MethodGet,
	invitationsPath,
	"One page of a tenant's invitations",
	"Returns one page of the tenant's invitations, ordered by status ascending (pending, accepted, "+
		"revoked) and then by creation time descending with the identifier as the tie-break. The "+
		"status query narrows the page: pending is a pending invitation whose deadline has not "+
		"passed, expired is a pending invitation whose deadline has passed, and accepted and revoked "+
		"are the terminal stored states; an absent status lists every state, and a value outside the "+
		"set is refused with 400. Expiry is derived from the deadline, not stored, so an expired row "+
		"still reads status pending. The cursor is opaque: pass the previous page's next_cursor as "+
		"after to resume, and an unreadable cursor is refused with 400 rather than served as the "+
		"first page. A read writes no audit row.",
	nil,
	speedhttp.Failures(managementFailures,
		speedhttp.ProblemResponseOf(http.StatusBadRequest, "The page query could not be read", typePrefix+"bad-request"),
		speedhttp.ProblemResponse(http.StatusForbidden, descriptionForbiddenTenant),
		speedhttp.ProblemResponse(http.StatusServiceUnavailable, descriptionUnavailable),
		speedhttp.Response{
			Status:      http.StatusOK,
			Description: "The page of invitations and the next cursor",
			Body:        &speedhttp.Body{MediaType: "application/json"},
		},
	),
)

// revokeOperation declares the route that revokes a pending invitation. The
// handler builds the answer because a missing invitation is a bare 404, which a
// simple operation's error path cannot write: it writes a problem document for
// every failure.
var revokeOperation = speedhttp.NewReplyOperation[revokeInput](
	"authn.invite.revoke",
	http.MethodDelete,
	invitationPath,
	"Revoke a pending invitation",
	"Revokes a pending invitation and voids its token; the invitation identifier also serves as the "+
		"decision's target. Revoking an already revoked invitation succeeds idempotently, while "+
		"revoking an accepted one answers 409 because removing a membership is not this route's job. "+
		"Failure to write the mandatory audit row answers 503 without revoking.",
	nil,
	speedhttp.Failures(managementFailures,
		speedhttp.ProblemResponseOf(http.StatusBadRequest, "The invitation identifier is missing", typePrefix+"bad-request"),
		speedhttp.ProblemResponse(http.StatusForbidden, descriptionForbiddenTenant),
		speedhttp.Response{
			Status:      http.StatusNotFound,
			Description: "No such invitation",
		},
		speedhttp.ProblemResponse(http.StatusConflict, "The invitation was already accepted"),
		speedhttp.ProblemResponse(http.StatusServiceUnavailable, descriptionUnavailable),
		speedhttp.Response{
			Status:      http.StatusNoContent,
			Description: "The invitation was revoked",
		},
	),
)

// acceptViewOperation declares the route that reads the invitation a token
// points to. The route is public and the token is the whole credential. An
// unknown token is a bare 404, so the handler builds its answer.
var acceptViewOperation = speedhttp.NewReplyOperation[acceptViewInput](
	"authn.invite.acceptView",
	http.MethodGet,
	acceptPath,
	"Read the invitation a token points to",
	"Resolves the token's SHA-256 to an invitation and returns its view so a client can show what it "+
		"is about to accept. The route is public and the token is the whole credential; the response "+
		"never carries a token. An unknown token is 404. The view reflects the stored state even when "+
		"the invitation can no longer be accepted, so the client should read status and expires_at "+
		"rather than assume the link is redeemable.",
	nil,
	[]speedhttp.Response{
		{
			Status:      http.StatusOK,
			Description: "The invitation view",
			Body:        &speedhttp.Body{MediaType: "application/json", Structure: reflect.TypeOf(AuthnInviteView{})},
		},
		{
			Status:      http.StatusNotFound,
			Description: "No such invitation",
		},
		throttledResponse(),
		speedhttp.ProblemResponse(http.StatusServiceUnavailable, descriptionUnavailable),
	},
	speedhttp.Public(),
)

// acceptOperation declares the route that redeems an invitation. The route is
// optional: a request with no credential reaches the handler with no identity
// and is refused with 401. The token is not proof of the invited address, so a
// caller who has not proved it is 403 and is shown the proof step; the route
// never opens an account. An unknown token is a bare 404, so the handler builds
// its answer.
var acceptOperation = speedhttp.NewReplyOperation[acceptInput](
	"authn.invite.accept",
	http.MethodPost,
	acceptPath,
	"Redeem an invitation for the signed-in account that proves the invited address",
	"Redeems the invitation and writes a membership for the authenticated caller. The caller must "+
		"hold the invited address as a proven email identifier; holding the link is not enough, because "+
		"the inviter holds it too. When the caller has not proved it, the answer is 403 "+
		"invitation-email-unproved and the client shows the proof step; the invitation stays pending. "+
		"Acceptance never creates an account and never makes the invited address a sign-in identifier. "+
		"A membership that already exists keeps its source and its SSO exemption; a suspended one is "+
		"refused with 409 membership-inactive and the invitation is revoked. Two concurrent redemptions "+
		"of one token leave at most one winner. A token that is expired, revoked or already accepted is 409.",
	&speedhttp.RequestBody{
		MediaType:   "application/json",
		Description: "The body is required but its account-opening fields are ignored; only the token is read.",
		Required:    true,
	},
	[]speedhttp.Response{
		{
			Status:      http.StatusCreated,
			Description: "The membership the acceptance wrote",
			Body:        &speedhttp.Body{MediaType: "application/json", Structure: reflect.TypeOf(AuthnInviteAccept{})},
		},
		unauthorized("No authenticated caller"),
		speedhttp.ProblemResponseOf(http.StatusBadRequest, "The body or an argument is malformed", typePrefix+"bad-request"),
		speedhttp.ProblemResponseOf(http.StatusUnsupportedMediaType, descriptionNotJSON, typePrefix+"unsupported-media-type"),
		speedhttp.ProblemResponseOf(http.StatusForbidden,
			"The caller does not hold the invited address as a proven email identifier",
			typePrefix+"invitation-email-unproved"),
		{
			Status:      http.StatusNotFound,
			Description: "No such invitation",
		},
		speedhttp.ProblemResponse(http.StatusConflict,
			"The invitation is expired, revoked or already accepted"),
		speedhttp.ProblemResponseOf(http.StatusConflict,
			"The caller's membership in the tenant is suspended", typePrefix+"membership-inactive"),
		throttledResponse(),
		speedhttp.ProblemResponse(http.StatusServiceUnavailable, descriptionUnavailable),
	},
	speedhttp.Optional(),
)

// operations is this module's declaration of the API it serves. The fragment is
// generated from it; the routes are mounted from the same declarations.
// tenantOperations is the management surface, decided in the tenant's own
// policy domain.
var tenantOperations = speedhttp.Operations{
	issueOperation,
	listOperation,
	revokeOperation,
}

// publicOperations is the acceptance surface: the read is public and the write
// is optional, because it needs an identity but carries no decision to make.
var publicOperations = speedhttp.Operations{
	acceptViewOperation,
	acceptOperation,
}
