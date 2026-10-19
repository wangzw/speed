package impersonation

import (
	"net/http"
	"reflect"
	"time"

	"github.com/vislake/speed/pkg/authn/internal/mount"
	speedhttp "github.com/vislake/speed/pkg/http"
)

// The fragment's info section: the title this module's fragment carries and the
// version of the fragment's shape.
const (
	fragmentTitle   = "authn impersonation"
	fragmentVersion = "1.0.0"
)

// impersonationPath is the route of the self-service group, relative to the
// module's API root, which is the mounter's base less the part this capability
// owns. The mounter registration resolves the same declaration against its base,
// so the path is written once.
const impersonationPath = "/auth/impersonation"

// AuthnImpersonationGrant is an impersonation record as a client sees it. It is
// built field by field so that a field added to the record later does not reach
// a client by being there. The reason and the administrator's identity are not
// part of it.
type AuthnImpersonationGrant struct {
	_ struct{} `description:"An impersonation record as a client sees it. The reason and the administrator's identity are not part of it."`

	ID        string     `json:"id" required:"true" description:"Identifier of the impersonation record, minted when the start was granted."`
	Target    string     `json:"target" required:"true" description:"The impersonated account"`
	Tenant    string     `json:"tenant" required:"true" description:"The tenant the impersonation is confined to"`
	StartedAt *time.Time `json:"started_at,omitempty" description:"Absent on the answer to a start, present on a record in force"`
	EndsAt    time.Time  `json:"ends_at" required:"true" description:"RFC 3339 instant at which the record stops being valid; never more than one hour after it began."`
}

// AuthnImpersonationGrantEnvelope is the answer of the status route.
type AuthnImpersonationGrantEnvelope struct {
	_ struct{} `description:"The answer of the status route."`

	Grant AuthnImpersonationGrant `json:"grant" required:"true" description:"The record in force on the session, including the instant it began."`
}

// AuthnImpersonationStart is the answer of a start: the record just created and,
// for a bearer credential, the impersonation access token.
type AuthnImpersonationStart struct {
	_ struct{} `description:"The answer of a start: the record just created and, for a bearer credential, the impersonation access token."`

	Grant       AuthnImpersonationGrant `json:"grant" required:"true" description:"The record just created; started_at is absent because a start answer does not carry the start instant."`
	AccessToken string                  `json:"access_token,omitempty" description:"Present on a bearer start; absent on a cookie start, whose carrier is the session cookie"`
	TokenType   string                  `json:"token_type,omitempty" description:"Present with access_token; always Bearer"`
	// ExpiresIn is a pointer so a cookie start omits it while a bearer start
	// writes it even at the zero a sub-second lifetime rounds to. The format
	// tag keeps the published schema the int64 the wire carries; the reflector
	// reads a pointer's own type as a plain integer.
	ExpiresIn *int64 `json:"expires_in,omitempty" format:"int64" description:"Present with access_token; the access token's lifetime in whole seconds"`
}

// startInput is the request of a start.
type startInput struct {
	_ struct{} `description:"The parameters of a start. Only account is required by the schema; a missing or empty tenant or reason is refused with 403."`

	Account    string `json:"account" required:"true" description:"Identifier of the account to impersonate: an enabled, non-protected user account other than the caller's own, and an active member of tenant."`
	Tenant     string `json:"tenant" description:"Identifier of the tenant the impersonation is confined to; it may not be empty or contain whitespace or control characters, so omitting it is refused."`
	Reason     string `json:"reason" description:"Why the impersonation is started. It is trimmed, then must be one to 500 characters with no control characters; it is stored on the record and in the audit row and never appears in a response."`
	TTLSeconds int64  `json:"ttl_seconds" description:"Requested lifetime in whole seconds. Zero or absent takes the max-ttl setting (30 minutes by default); the settled lifetime is the shortest of the request, that setting, the remaining lifetime of the caller's session and a hard ceiling of one hour."`
}

// startResponse declares the success answer of a start: the record and, under a
// bearer credential, the token. It is a reply operation because a cookie start
// also writes the rotated session cookie, which the simple form cannot write.
var startResponse = speedhttp.Response{
	Status:      http.StatusOK,
	Description: "The started record, and an access token under a bearer credential",
	Body: &speedhttp.Body{
		MediaType: "application/json",
		Structure: reflect.TypeOf(AuthnImpersonationStart{}),
	},
}

// grantResponse declares the success answer of the status route.
var grantResponse = speedhttp.Response{
	Status:      http.StatusOK,
	Description: "The record still valid on the session",
	Body: &speedhttp.Body{
		MediaType: "application/json",
		Structure: reflect.TypeOf(AuthnImpersonationGrantEnvelope{}),
	},
}

// challengeHeader declares the header every 401 on this module's routes
// carries: the gateway writes the endpoint's challenge when it refuses the
// request first, and a handler that answers 401 itself returns it in the fault's
// headers. Either way the exchange is a challenge, so the response declares the
// header and a handler's fault writes it.
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

// capabilityProblem is an error answer the entry point writes itself, before a
// handler runs: the binding refusals of the start route. Its problem type is
// the capability's code for the status, so a body refused before the handler is
// answered in the same vocabulary as one the handler refuses.
func capabilityProblem(status int, code, description string) speedhttp.Response {
	response := speedhttp.ProblemResponse(status, description)
	response.ProblemType = typePrefix + code
	return response
}

// startOperation declares POST /impersonation: it calls Start and then attaches
// the carrier, publishes the started event and writes the answer. The handler
// builds the answer because a cookie start writes the rotated session cookie.
var startOperation = speedhttp.NewReplyOperation[startInput](
	"authn.impersonation.start",
	http.MethodPost,
	impersonationPath,
	"Start impersonating another account",
	"Begins an impersonation on the caller's session and answers with the record just created. The "+
		"caller must have authenticated locally with a recent multi-factor authentication whose second "+
		"factor is a one-time code or a passkey, and must hold a seasoned factor for the account. A "+
		"cookie credential receives the record only and has the impersonation marker set on its "+
		"session; a bearer credential also receives an impersonation access token whose lifetime is the "+
		"shorter of the access-token setting and the record's remaining lifetime.",
	&speedhttp.RequestBody{
		MediaType:   "application/json",
		Description: "The target account, the tenant the impersonation is confined to, the reason to record, and the requested lifetime.",
		Required:    true,
	},
	[]speedhttp.Response{
		startResponse,
		capabilityProblem(http.StatusBadRequest, mount.CodeBadRequest, "The body could not be read"),
		unauthorized("No usable credential, or a step-up is required"),
		speedhttp.ProblemResponse(http.StatusForbidden,
			"Impersonation is refused for this caller or target, or the caller has no seasoned non-SMS factor (factor-not-seasoned)"),
		speedhttp.ProblemResponse(http.StatusConflict, "An impersonation is already in force"),
		capabilityProblem(http.StatusUnsupportedMediaType, mount.CodeUnsupportedMediaType,
			"The request body is not application/json"),
		speedhttp.ProblemResponse(http.StatusServiceUnavailable, "A dependency could not answer"),
	},
)

// statusOperation declares GET /impersonation: the impersonation in force on the
// caller's session, or 404 when there is none still valid. The handler builds
// the answer because the 404 is a bare status with no problem document.
var statusOperation = speedhttp.NewReplyOperation[struct{}](
	"authn.impersonation.status",
	http.MethodGet,
	impersonationPath,
	"The impersonation in force on the caller's session",
	"Answers the record still in force on the caller's session, including the instant it began. "+
		"Before answering, the record is judged against the current time and the bound session, actor, "+
		"target and target membership; one that has lapsed is ended and answers 404, as does a "+
		"credential that carries no session.",
	nil,
	[]speedhttp.Response{
		grantResponse,
		unauthorized("No usable credential"),
		{Status: http.StatusNotFound, Description: "No impersonation is in force"},
		speedhttp.ProblemResponse(http.StatusServiceUnavailable, "The record could not be read"),
	},
)

// endOperation declares DELETE /impersonation: it ends the record on the
// caller's session and clears the marker under a cookie, without rotating the
// token or writing a cookie. The handler builds the answer because the 404 and
// the 204 carry no problem document.
var endOperation = speedhttp.NewReplyOperation[struct{}](
	"authn.impersonation.end",
	http.MethodDelete,
	impersonationPath,
	"End the impersonation on the caller's session",
	"Ends the unended record on the caller's session with the reason explicit, whether or not it is "+
		"still valid, and answers 204. Repeating it is safe: once the record is ended a further call "+
		"answers 404. Under a cookie credential the impersonation marker is cleared from the session "+
		"without rotating its token, so the caller continues as its own identity; a failed audit write "+
		"does not undo the end.",
	nil,
	[]speedhttp.Response{
		{Status: http.StatusNoContent, Description: "The impersonation ended"},
		unauthorized("No usable credential"),
		{Status: http.StatusNotFound, Description: "The caller is not impersonating"},
		speedhttp.ProblemResponse(http.StatusServiceUnavailable, "The record could not be ended"),
	},
)

// operations is this module's declaration of the API it serves. The fragment is
// generated from it; the routes are mounted from the same declarations.
var operations = speedhttp.Operations{
	startOperation,
	statusOperation,
	endOperation,
}
