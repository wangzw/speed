package invite

import (
	"net/http"
	"reflect"
	"time"

	"github.com/vislake/speed/pkg/authn/internal/mount"
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
	resendPath      = "/:tenant/invitations/:id/resend"
	acceptPath      = "/auth/invitations/:token"
	eligibilityPath = "/auth/invitations/:token/eligibility"
)

// AuthnInviteInvitation is one invitation as the management face shows it. It
// never carries the acceptance token.
type AuthnInviteInvitation struct {
	_ struct{} `description:"One invitation as the management face shows it. It never carries the acceptance token."`

	ID        string `json:"id" required:"true" description:"The invitation's opaque identifier, used to revoke it."`
	Tenant    string `json:"tenant" required:"true" description:"The name of the tenant the invitation grants a membership in."`
	Email     string `json:"email" required:"true" description:"The normalized address"`
	Status    string `json:"status" required:"true" enum:"pending,accepted,revoked" description:"The stored state. Expiry is not stored: an expired pending invitation still reads pending."`
	State     string `json:"state" required:"true" enum:"pending,expired,accepted,revoked" description:"The state the server judges with the same clock the listing's status filter uses: pending, expired, accepted or revoked. A client shows this and never derives expiry from its own clock."`
	InvitedBy string `json:"invited_by" required:"true" description:"The account identifier of the issuer; empty when a host command issued the invitation without an HTTP identity. During an impersonation it is the impersonated account."`
	// The two names are absent, not null, when there is no issuer to name: a
	// host command issued the invitation, or the account no longer exists.
	InvitedByName     string     `json:"invited_by_name,omitempty" description:"The display name of the account that issued the invitation. Absent when a host command issued it, the account no longer exists, or the account read fails; during an impersonation it is the impersonated account's name."`
	InvitedByUsername string     `json:"invited_by_username,omitempty" description:"The username of the account that issued the invitation. Absent when a host command issued it, the account no longer exists, the account has no username, or the account read fails; during an impersonation it is the impersonated account's username."`
	Role              string     `json:"role,omitempty" description:"The role the invitation grants beside the membership. Absent when the invitation grants no role, never null. A resend keeps it unchanged."`
	ExpiresAt         time.Time  `json:"expires_at" required:"true" description:"RFC 3339 deadline in UTC; a pending invitation at or past it can no longer be accepted."`
	AcceptedAt        *time.Time `json:"accepted_at" nullable:"true" required:"true" description:"When the invitation was accepted, as an RFC 3339 instant in UTC; null while it has not been accepted."`
	RevokedAt         *time.Time `json:"revoked_at" nullable:"true" required:"true" description:"When the invitation was revoked, as an RFC 3339 instant in UTC; null while it has not been revoked."`
	LastSentAt        time.Time  `json:"last_sent_at" required:"true" description:"RFC 3339 timestamp in UTC of the most recent send. Issuance always writes it, so it is never null."`
	SendCount         int        `json:"send_count" required:"true" description:"How many times the invitation was sent: the first issuance counts as one and every resend adds one."`
	CreatedAt         time.Time  `json:"created_at" required:"true" description:"RFC 3339 timestamp in UTC of issuance."`
}

// AuthnInviteIssued is the answer of an issue or a resend: the invitation's
// view, the outcome of the notice, and the one-time acceptance token only when
// the notice did not reach the address.
type AuthnInviteIssued struct {
	_ struct{} `description:"The answer of an issue or a resend: the invitation's view, the outcome of the notice, and the one-time acceptance token only when the notice did not reach the address."`

	Invitation    AuthnInviteInvitation `json:"invitation" required:"true" description:"The issued invitation; a token is delivered beside it only when the notice did not reach the address."`
	Token         string                `json:"token,omitempty" description:"The acceptance token, present only when email_delivery is failed or not_configured: the inviter has to hand the link over. Absent when the notice was sent, because the recipient already holds it."`
	EmailDelivery string                `json:"email_delivery" required:"true" enum:"sent,failed,not_configured" description:"How the acceptance notice ended: sent when the carrier accepted it, failed when delivery failed or its outcome is unknown, and not_configured when the assembly delivers no mail. A failed notice does not undo the invitation, and the token beside it is still the one-time link; a sent notice carries no token in this answer."`
}

// AuthnInviteLink is what an invitation link shows the person holding it: the
// organization it names, the inviter under the display name they are recognized
// by, the invited address in a masked form and the deadline. The full address,
// the issuer's account identifier and the token never reach it. A pending link
// carries the organization's name and display name, the inviter, the masked
// address and the deadline; an accepted one carries the two organization names
// so the client can offer the organization the caller now belongs to. The
// display name is free text an organization need not have chosen, so a client
// shows it when it is there and falls back to the name, which is always there.
type AuthnInviteLink struct {
	_ struct{} `description:"What an invitation link shows the person holding it: the organization, the inviter's display name, the masked invited address and the deadline, limited to the state the link is in."`

	Tenant            string     `json:"tenant,omitempty" description:"The tenant's name, present for a pending and an accepted invitation: the client shows it when the organization carries no display name, and an accepted link uses it to address the organization the caller now belongs to."`
	TenantDisplayName string     `json:"tenant_display_name" required:"true" description:"The organization's display name, the free text its administrators chose; empty when the roster carries none, in which case the client falls back to tenant."`
	InvitedByName     string     `json:"invited_by_name,omitempty" description:"The display name of the account that issued the invitation, present only for a pending invitation that an account issued. It is never the issuer's account identifier."`
	Email             string     `json:"email,omitempty" description:"The invited address masked on the server, for example a***@demo.test, so the recipient recognizes it without the link disclosing the whole address. Present only for a pending invitation."`
	ExpiresAt         *time.Time `json:"expires_at,omitempty" description:"RFC 3339 deadline in UTC. Present only for a pending invitation, the only state in which the link still grants anything."`
	Role              string     `json:"role,omitempty" description:"The role the invitation grants beside the membership, present only for a pending invitation that carries one. Absent when the invitation grants no role, never null."`
}

// AuthnInviteView is the answer of the acceptance read. A redeemable invitation
// carries its link; an accepted one carries the organization it granted; one
// that is expired or revoked carries the state alone.
type AuthnInviteView struct {
	_ struct{} `description:"The answer of the acceptance read: the invitation's state, and the link's summary for the states that may show one."`

	State      string           `json:"state" required:"true" enum:"pending,expired,accepted,revoked" description:"The state the read judges. Pending carries the organization's name and display name, the inviter, the masked address and the deadline; accepted carries the organization's name and display name; expired and revoked answer the state alone."`
	Invitation *AuthnInviteLink `json:"invitation,omitempty" description:"The link's summary, present for a pending invitation and for an accepted one, absent for an expired or a revoked invitation."`
}

// AuthnInviteEligibility is the answer of the eligibility read: the state a
// token points to and whether the signed-in account holds the invited address as
// a proven email identifier. It never carries the address, the inviter or the
// organization, and it answers for the calling account alone.
type AuthnInviteEligibility struct {
	_ struct{} `description:"The answer of the eligibility read: the invitation's state and whether the signed-in account has proved the invited address."`

	State       string `json:"state" required:"true" enum:"pending,expired,accepted,revoked" description:"The state the read judges, the same judgement the public read makes."`
	EmailProved bool   `json:"email_proved" required:"true" description:"Whether the signed-in account holds the invited address as a proven email identifier, the condition acceptance requires. False for an account that holds the address only through an unproved channel or through an invitation."`
}

// AuthnInvitePage is one page of a tenant's invitations together with the cursor
// of the next page.
type AuthnInvitePage struct {
	_ struct{} `description:"One page of a tenant's invitations together with the cursor of the next page."`

	Invitations []AuthnInviteInvitation `json:"invitations" required:"true" description:"The invitations of this page, in four groups: unexpired pending, expired pending, accepted, revoked. Each group is newest first with the identifier as the tie-break. A row that crosses its deadline between pages can change group."`
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
	Role      string `json:"role,omitempty" description:"The role the acceptance bound to the account in the tenant. Absent when the invitation carried no role, never null."`
}

// issueInput is the request of an issuance: the tenant from the path and the
// address and optional deadline from the JSON body. ExpiresAt is a pointer so
// that an absent member and the zero instant are told apart before the domain
// call reads both as "use the configured lifetime".
type issueInput struct {
	_ struct{} `description:"The issue request: the invited address, which is normalized before it is stored, and an optional deadline."`

	Tenant    string     `path:"tenant" description:"The tenant's name the invitation belongs to. The path-tenant layer resolves it to the internal identifier before the handler runs and holds it to the tenant the request resolved to; the authn.membership.manage decision is made in that tenant."`
	Email     string     `json:"email" required:"true" description:"The invited address as given; it is trimmed, lower-cased and must be printable ASCII with exactly one @, at most 254 bytes."`
	ExpiresAt *time.Time `json:"expires_at" description:"Absolute deadline in RFC 3339. Omitted or null takes the configured lifetime; the zero instant, or a value not later than the current time, is refused with 400."`
	Role      string     `json:"role" description:"The role to grant beside the membership. Omitted grants no role. The role must exist in the tenant, the caller must hold authz.tenant.role.manage, every permission the role carries must be within the caller's held set, and it may not be platform-admin."`
}

// listInput is the request of a page read: the tenant from the path and the
// cursor, page cap and status from the query.
type listInput struct {
	Tenant string `path:"tenant" description:"The tenant's name the invitation belongs to. The path-tenant layer resolves it to the internal identifier before the handler runs and holds it to the tenant the request resolved to; the authn.membership.manage decision is made in that tenant."`
	Limit  int    `query:"limit" description:"Maximum number of invitations to return. Omitted or 0 serves 100; a value above 1000 is capped at 1000. A negative or non-integer value is refused with 400."`
	After  string `query:"after" description:"Opaque cursor from a previous page's next_cursor; omit it to start at the first page. A value this build did not mint, including one in an earlier format or carrying an unknown version, is refused with 400 rather than served as the first page."`
	Status string `query:"status" enum:"pending,expired,accepted,revoked" description:"Narrow the page to one view: pending is a pending invitation whose deadline has not passed, expired is a pending invitation whose deadline has passed, and accepted and revoked are the terminal stored states. Omitted lists every state. A value outside this set is refused with 400. Expiry is derived from the deadline, not stored."`
}

// revokeInput is the request of a revocation: the tenant and the invitation's
// identifier, both path parameters.
type revokeInput struct {
	Tenant string `path:"tenant" description:"The tenant's name the invitation belongs to. The path-tenant layer resolves it to the internal identifier before the handler runs and holds it to the tenant the request resolved to; the authn.membership.manage decision is made in that tenant."`
	ID     string `path:"id" description:"The invitation's opaque identifier, which also serves as the decision's target."`
}

// resendInput is the request of a resend: the tenant and the invitation's
// identifier, both path parameters. The invitation keeps its identity; only its
// token and its send record change.
type resendInput struct {
	Tenant string `path:"tenant" description:"The tenant's name the invitation belongs to. The path-tenant layer resolves it to the internal identifier before the handler runs and holds it to the tenant the request resolved to; the authn.membership.manage decision is made in that tenant."`
	ID     string `path:"id" description:"The invitation's opaque identifier, which also serves as the decision's target."`
}

// acceptViewInput is the request of the acceptance read: the token, which is
// the whole credential on the public route.
type acceptViewInput struct {
	Token string `path:"token" description:"The acceptance token, prefixed spd_inv_, which identifies the invitation and is the whole credential on this public route."`
}

// eligibilityInput is the request of the eligibility read: the token from the
// path; the caller is the signed-in account the gateway identifies.
type eligibilityInput struct {
	Token string `path:"token" description:"The acceptance token, prefixed spd_inv_, which identifies the invitation. The route is guarded, so only a signed-in caller reaches it and the answer is about that caller alone."`
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
		"A send or acceptance-probe budget is spent ("+typePrefix+mount.CodeThrottled+"); Retry-After names the seconds until the window opens again")
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
	descriptionForbiddenTenant = "The caller may not manage memberships in the tenant (" + typePrefix + mount.CodeForbidden + ")"
	descriptionContactRequired = "The acting account holds an email address it has not proved (" + typePrefix + mount.CodeContactRequired + ")"
	descriptionUnavailable     = "A dependency could not answer (" + typePrefix + mount.CodeUnavailable + ")"
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
		"bytes), then issues a pending invitation. The address cannot be a service account's, cannot "+
		"already be a member of the tenant (active or suspended), and cannot already hold an "+
		"unexpired pending invitation; the system tenant cannot be invited into. expires_at is "+
		"optional: omitted or null takes the configured lifetime (168 hours by "+
		"default); the zero instant, or a deadline not later than the current time, is refused with 400, "+
		"and one later than 90 days after issuance is refused. An expired pending invitation for the same "+
		"tenant and address is superseded: the old row stays expired and a new invitation takes the live slot. An optional "+
		"role grants that role beside the membership: it must exist in the tenant "+
		"(400 invitation-role-unknown), the caller must hold authz.tenant.role.manage, and the role "+
		"must be one the caller may grant (403 invitation-role-protected for platform-admin, a "+
		"permission the caller does not hold, or the host write guard's refusal). When the "+
		"assembly has an invitation notifier, the acceptance notice is delivered to the address and "+
		"email_delivery reports sent, failed or not_configured; a failed notice does not undo the "+
		"invitation, which is still answered 201, with its one-time token only when the notice was "+
		"not sent. Requires the authn.membership.manage decision in the tenant; failure to write the "+
		"mandatory audit row answers 503 without issuing.",
	&speedhttp.RequestBody{
		MediaType:   "application/json",
		Description: "The address to invite and an optional absolute deadline. Only email is required.",
		Required:    true,
	},
	speedhttp.Failures(managementFailures,
		speedhttp.ProblemResponseOf(http.StatusBadRequest,
			"The body could not be read or the address is malformed ("+typePrefix+"bad-request); the "+
				"deadline is the zero instant or is not later than issuance ("+typePrefix+"bad-request); "+
				"the address belongs to a service account or the tenant is the system tenant "+
				"("+typePrefix+mount.CodeInvitationTargetInvalid+"); the deadline lies more than 90 days "+
				"after issuance ("+typePrefix+mount.CodeInvitationExpiryTooLong+"); "+
				"the role is not one the tenant has ("+typePrefix+mount.CodeInvitationRoleUnknown+")",
			typePrefix+"bad-request"),
		speedhttp.ProblemResponseOf(http.StatusUnsupportedMediaType, descriptionNotJSON, typePrefix+"unsupported-media-type"),
		speedhttp.ProblemResponseOf(http.StatusForbidden,
			descriptionForbiddenTenant+"; "+descriptionContactRequired+"; the role is platform-admin, "+
				"carries a permission the caller does not hold, or the host write guard refuses the "+
				"binding ("+typePrefix+mount.CodeInvitationRoleProtected+")",
			typePrefix+mount.CodeForbidden),
		speedhttp.ProblemResponseOf(http.StatusConflict,
			"The address is already a member of the tenant ("+typePrefix+mount.CodeInvitationMember+"), "+
				"or already holds an unexpired pending invitation ("+typePrefix+mount.CodeInvitationPending+")",
			typePrefix+mount.CodeInvitationPending),
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
	"Returns one page of the tenant's invitations in four groups: unexpired pending first, then "+
		"expired pending, then accepted, then revoked. Each group is ordered by creation time "+
		"descending with the identifier as the tie-break, and the cursor carries the group so the "+
		"next page resumes after it; a row that crosses its deadline between two pages moves to a "+
		"later group and can therefore be repeated at the boundary, never skipped, because the clock "+
		"only moves a row forward. The status query narrows the "+
		"page: pending is a pending invitation whose deadline has not passed, expired is a pending "+
		"invitation whose deadline has passed, and accepted and revoked are the terminal stored "+
		"states; an absent status lists every state, and a value outside the set is refused with "+
		"400. Expiry is derived from the deadline, not stored, so an expired row still reads status "+
		"pending. The cursor is opaque: pass the previous page's next_cursor as after to resume, "+
		"and a cursor this build did not mint, including one in an earlier format or carrying an "+
		"unknown version, is refused with 400 rather than served as the first page. A read "+
		"writes no audit row.",
	nil,
	speedhttp.Failures(managementFailures,
		speedhttp.ProblemResponseOf(http.StatusBadRequest, "The page query could not be read ("+typePrefix+"bad-request)", typePrefix+"bad-request"),
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
	"Revokes a pending invitation that has not expired and voids its token; the invitation "+
		"identifier also serves as the decision's target. Revoking an already revoked invitation "+
		"succeeds idempotently, while an expired or accepted one answers 409 invitation-terminal: "+
		"removing a membership is not this route's job, and an expired link no longer grants "+
		"anything. Failure to write the mandatory audit row answers 503 without revoking.",
	nil,
	speedhttp.Failures(managementFailures,
		speedhttp.ProblemResponseOf(http.StatusBadRequest, "The invitation identifier is missing ("+typePrefix+"bad-request)", typePrefix+"bad-request"),
		speedhttp.ProblemResponseOf(http.StatusForbidden, descriptionForbiddenTenant+"; "+descriptionContactRequired, typePrefix+mount.CodeForbidden),
		speedhttp.Response{
			Status:      http.StatusNotFound,
			Description: "No such invitation",
		},
		speedhttp.ProblemResponseOf(http.StatusConflict,
			"The invitation is expired or already accepted ("+typePrefix+mount.CodeInvitationTerminal+")",
			typePrefix+mount.CodeInvitationTerminal),
		speedhttp.ProblemResponse(http.StatusServiceUnavailable, descriptionUnavailable),
		speedhttp.Response{
			Status:      http.StatusNoContent,
			Description: "The invitation was revoked",
		},
	),
)

// resendOperation declares the route that sends a pending invitation again. The
// handler builds the answer because an unknown invitation is a bare 404, which a
// simple operation's error path cannot write.
var resendOperation = speedhttp.NewReplyOperation[resendInput](
	"authn.invite.resend",
	http.MethodPost,
	resendPath,
	"Resend a pending invitation",
	"Resends a pending invitation that has not expired. The same row keeps its identity and its "+
		"deadline; a new token replaces the old one, which stops working. send_count counts every "+
		"send, the first issuance as one, and last_sent_at is the moment of the latest. The admission "+
		"order is the issue route's: sign-in, the signup gate, the authn.membership.manage decision "+
		"with the invitation identifier as its target, and the mandatory resend audit row on that "+
		"identifier. The domain call then charges the issuing account's send budget before the "+
		"terminal check, so a terminal row still costs the operator, and the address's and the "+
		"deployment's budgets after it, counting those two only when the write lands. An expired, "+
		"accepted or revoked invitation answers 409 invitation-terminal, and an identifier naming no "+
		"invitation of the tenant is a bare 404. The notice and the one-time link follow the issue "+
		"answer: sent carries no token, failed and not_configured carry one.",
	nil,
	speedhttp.Failures(managementFailures,
		speedhttp.ProblemResponseOf(http.StatusBadRequest, "The invitation identifier is missing ("+typePrefix+"bad-request)", typePrefix+"bad-request"),
		speedhttp.ProblemResponseOf(http.StatusForbidden, descriptionForbiddenTenant+"; "+descriptionContactRequired, typePrefix+mount.CodeForbidden),
		speedhttp.Response{
			Status:      http.StatusNotFound,
			Description: "No such invitation",
		},
		speedhttp.ProblemResponseOf(http.StatusConflict,
			"The invitation is expired, accepted or revoked ("+typePrefix+mount.CodeInvitationTerminal+")",
			typePrefix+mount.CodeInvitationTerminal),
		throttledResponse(),
		speedhttp.ProblemResponse(http.StatusServiceUnavailable, descriptionUnavailable),
		speedhttp.Response{
			Status:      http.StatusOK,
			Description: "The invitation view, the notice outcome, and the one-time token only when the notice was not sent",
			Body: &speedhttp.Body{
				MediaType: "application/json",
				Structure: reflect.TypeOf(AuthnInviteIssued{}),
			},
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
		"never carries a token. An unknown token is 404. A pending invitation answers the "+
		"organization's display name, the inviter's display name, the invited address masked on the "+
		"server and the deadline; an accepted one answers the organization's name and display name; "+
		"an expired or revoked one answers its state alone. The full address and the issuer's account "+
		"identifier never reach this answer. The view reflects the stored state even when the "+
		"invitation can no longer be accepted, so the client reads state and, for a pending "+
		"invitation, invitation.expires_at rather than assuming the link is redeemable.",
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

// eligibilityOperation declares the route that answers, for the signed-in
// caller alone, whether an invitation could be accepted by this account. It is
// guarded rather than public: the public read stays open so an anonymous
// visitor can see the landing page, while the proof question names an account.
// An unknown token is a bare 404, so the handler builds its answer.
var eligibilityOperation = speedhttp.NewReplyOperation[eligibilityInput](
	"authn.invite.eligibility",
	http.MethodGet,
	eligibilityPath,
	"Read whether the signed-in account could accept the invitation a token points to",
	"Resolves the token's SHA-256 to an invitation and answers its state together with whether the "+
		"signed-in caller holds the invited address as a proven email identifier, so a client can show "+
		"the proof step on arrival instead of on the first attempt to accept. The route is guarded: a "+
		"request without a usable credential is 401. The answer never carries the invited address, the "+
		"inviter or the organization, and an unknown token is 404. The probe budget is charged exactly "+
		"as the public read charges it, by token and by client, before the token is looked up.",
	nil,
	[]speedhttp.Response{
		{
			Status:      http.StatusOK,
			Description: "The invitation's state and whether the caller has proved the invited address",
			Body:        &speedhttp.Body{MediaType: "application/json", Structure: reflect.TypeOf(AuthnInviteEligibility{})},
		},
		unauthorized("No usable credential"),
		{
			Status:      http.StatusNotFound,
			Description: "No such invitation",
		},
		throttledResponse(),
		speedhttp.ProblemResponse(http.StatusServiceUnavailable, descriptionUnavailable),
	},
)

// acceptOperation declares the route that redeems an invitation. The route is
// optional: a request with no credential reaches the handler with no identity
// and is refused with 401. A caller that is not a user account is 403
// invitation-target-invalid. The token is not proof of the invited address, so a
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
		"A caller that is not a user account is refused with 403 invitation-target-invalid: an "+
		"invitation grants a membership to a person, and a service account signs in with a service "+
		"secret, while a subject with no account row such as a static password-table subject has no "+
		"account to grant it to. A read of the caller's account that fails answers 503 and leaves the "+
		"invitation pending. Acceptance never creates an account and never makes the invited "+
		"address a sign-in identifier. A membership that already exists keeps its source and its SSO "+
		"exemption; a suspended one is refused with 409 membership-inactive and the invitation is "+
		"revoked. Two concurrent redemptions of one token leave at most one winner. A token that is "+
		"expired, revoked or already accepted, or whose role can no longer be granted, is 409.",
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
		speedhttp.ProblemResponseOf(http.StatusBadRequest, "The body or an argument is malformed ("+typePrefix+"bad-request)", typePrefix+"bad-request"),
		speedhttp.ProblemResponseOf(http.StatusUnsupportedMediaType, descriptionNotJSON, typePrefix+"unsupported-media-type"),
		speedhttp.ProblemResponseOf(http.StatusForbidden,
			"The caller does not hold the invited address as a proven email identifier ("+typePrefix+mount.CodeInvitationEmailUnproved+"); "+
				"the caller is not a user account, such as a service account or a subject with no account row ("+
				typePrefix+mount.CodeInvitationTargetInvalid+")",
			typePrefix+mount.CodeInvitationEmailUnproved),
		{
			Status:      http.StatusNotFound,
			Description: "No such invitation",
		},
		speedhttp.ProblemResponseOf(http.StatusConflict,
			"The invitation is expired, revoked or already accepted, or its role can no longer be granted "+
				"and the invitation was revoked ("+typePrefix+mount.CodeInvitationTerminal+"); "+
				"the tenant is disabled and the invitation stays pending ("+typePrefix+mount.CodeInvitationTenantInactive+"); "+
				"the caller's membership in the tenant is suspended ("+typePrefix+mount.CodeMembershipInactive+")",
			typePrefix+mount.CodeInvitationTerminal),
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
	resendOperation,
}

// publicOperations is the acceptance surface: the read is public, the write is
// optional because it needs an identity but carries no decision to make, and
// the eligibility read is guarded because it answers about the caller alone.
var publicOperations = speedhttp.Operations{
	acceptViewOperation,
	acceptOperation,
	eligibilityOperation,
}
