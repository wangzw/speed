package impersonation

import (
	"net/http"
	"reflect"
	"time"

	"github.com/vislake/speed/pkg/authn/internal/mount"
	speedhttp "github.com/vislake/speed/pkg/http"
)

const (
	fragmentTitle   = "authn impersonation"
	fragmentVersion = "1.0.0"
)

const impersonationPath = "/auth/impersonation"

// AuthnImpersonationGrant is an impersonation record as a client sees it: it
// omits the reason and the administrator's identity.
type AuthnImpersonationGrant struct {
	_ struct{} `description:"An impersonation record as a client sees it. The reason and the administrator's identity are not part of it."`

	ID        string     `json:"id" required:"true" description:"Identifier of the impersonation record, minted when the start was granted."`
	Target    string     `json:"target" required:"true" description:"The impersonated account"`
	Tenant    string     `json:"tenant" required:"true" description:"The name of the tenant the impersonation is confined to"`
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
	// ExpiresIn is set with access_token: a pointer so a cookie start omits it
	// while a bearer start writes it even at zero.
	ExpiresIn *int64 `json:"expires_in,omitempty" format:"int64" description:"Present with access_token; the access token's lifetime in whole seconds"`
}

type startInput struct {
	_ struct{} `description:"The parameters of a start. Only account is required by the schema; a missing or empty tenant or reason is refused with 403."`

	Account    string `json:"account" required:"true" description:"Identifier of the account to impersonate: an enabled, non-protected user account other than the caller's own, and an active member of tenant."`
	Tenant     string `json:"tenant" description:"The name of the tenant the impersonation is confined to; it may not be empty or contain whitespace or control characters, so omitting it is refused, and a name no tenant carries is refused as not a member."`
	Reason     string `json:"reason" description:"Why the impersonation is started. It is trimmed, then must be one to 500 characters with no control characters; it is stored on the record and in the audit row and never appears in a response."`
	TTLSeconds int64  `json:"ttl_seconds" description:"Requested lifetime in whole seconds. Zero or absent takes the max-ttl setting (30 minutes by default); the settled lifetime is the shortest of the request, that setting, the remaining lifetime of the caller's session and a hard ceiling of one hour."`
}

var startResponse = speedhttp.Response{
	Status:      http.StatusOK,
	Description: "The started record, and an access token under a bearer credential",
	Body: &speedhttp.Body{
		MediaType: "application/json",
		Structure: reflect.TypeOf(AuthnImpersonationStart{}),
	},
}

var grantResponse = speedhttp.Response{
	Status:      http.StatusOK,
	Description: "The record still valid on the session",
	Body: &speedhttp.Body{
		MediaType: "application/json",
		Structure: reflect.TypeOf(AuthnImpersonationGrantEnvelope{}),
	},
}

var challengeHeader = speedhttp.Header{
	Name:        "WWW-Authenticate",
	Description: "The capability's challenge: the Bearer and Cookie schemes, never Basic.",
}

func unauthorized(description string) speedhttp.Response {
	response := speedhttp.ProblemResponse(http.StatusUnauthorized, description)
	response.Headers = []speedhttp.Header{challengeHeader}
	return response
}

func capabilityProblem(status int, code, description string) speedhttp.Response {
	response := speedhttp.ProblemResponse(status, description)
	response.ProblemType = typePrefix + code
	return response
}

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
			"Impersonation is refused for this caller or target, the caller has no seasoned non-SMS factor "+
				"(factor-not-seasoned), or the caller has an email address that is not proved "+
				"(contact-required)"),
		speedhttp.ProblemResponse(http.StatusConflict, "An impersonation is already in force"),
		capabilityProblem(http.StatusUnsupportedMediaType, mount.CodeUnsupportedMediaType,
			"The request body is not application/json"),
		speedhttp.ProblemResponse(http.StatusServiceUnavailable, "A dependency could not answer"),
	},
)

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

var operations = speedhttp.Operations{
	startOperation,
	statusOperation,
	endOperation,
}
