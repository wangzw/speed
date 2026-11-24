package session

import (
	"net/http"
	"time"

	"github.com/vislake/speed/pkg/authn"
	speedhttp "github.com/vislake/speed/pkg/http"
)

// The fragment's info section: the title this module's fragment carries and the
// version of the fragment's shape.
const (
	fragmentTitle   = "authn session"
	fragmentVersion = "1.0.0"
)

// The routes as the fragment writes them: relative to the module's API root,
// which is the mounter's base less the part this capability owns.
const (
	sessionPath  = "/auth/session"
	logoutPath   = "/auth/logout"
	sessionsPath = "/auth/sessions"
	sessionByID  = "/auth/sessions/:id"
)

// AuthnSessionView is the body of GET /session: who the request is taken to be,
// or that nobody is signed in. Every field but authenticated is omitted when the
// answer has nothing to say about it.
type AuthnSessionView struct {
	_ struct{} `description:"Whether the request carries a signed-in identity and, when it does, the facts the identity is built from."`

	// Authenticated reports whether the request carries a signed-in identity.
	Authenticated bool `json:"authenticated" required:"true" description:"False when nobody is signed in; every other field is then absent"`
	// SessionID is the public identifier of the session.
	SessionID string `json:"session_id,omitempty" description:"The public identifier of the session the request came with; absent when nobody is signed in."`
	// AccountID is the account the request is taken to be from.
	AccountID string `json:"account_id,omitempty" description:"The account the request is taken to be from; the impersonated account while impersonating"`
	// ActorID is the account whose hands are on the request, present only while impersonating.
	ActorID string `json:"actor_id,omitempty" description:"The account whose hands are on the request; present only while impersonating"`
	// Impersonating reports whether the identity is acting as another account.
	Impersonating bool `json:"impersonating,omitempty" description:"True only while the identity is impersonating another account; absent otherwise."`
	// Methods are the authentication methods the identity completed.
	Methods []authn.Method `json:"methods,omitempty" items.enum:"pwd,sms,otp,webauthn,oauth,oidc,saml,static,client-secret" description:"The authentication methods the identity completed, each from the closed set named by the enum; absent when the session records none."`
	// Assurance is the level the authentication reached.
	Assurance string `json:"assurance,omitempty" enum:"none,single,multi" description:"The assurance level the authentication reached; \"none\" when the identity carries no level."`
	// AuthTime is the instant of the last interactive authentication.
	AuthTime *time.Time `json:"auth_time,omitempty" description:"The instant of the last interactive authentication, absent when the identity carries none"`
	// Provider is the backend that verified the credential.
	Provider string `json:"provider,omitempty" description:"The backend that verified the credential"`
}

// AuthnSessionItem is one live session of the caller's, as the listing reports
// it.
type AuthnSessionItem struct {
	_ struct{} `description:"One live session of the caller's, as the listing reports it."`

	// ID is the public session identifier.
	ID string `json:"id" required:"true" description:"The public session identifier; it names the row and is not a credential."`
	// Kind is the session kind.
	Kind string `json:"kind" required:"true" description:"The session kind, browser or token; service-account sessions are never listed."`
	// CreatedAt is when the session was created.
	CreatedAt time.Time `json:"created_at" required:"true" description:"When the session was created, as an RFC 3339 instant in UTC."`
	// LastSeenAt is the last activity the row was written for.
	LastSeenAt time.Time `json:"last_seen_at" required:"true" description:"Last activity of the session, as an RFC 3339 instant in UTC. For a browser session this is the last use the row was written for: touches are coalesced, written at most once per effective touch interval — the configured interval capped at half the idle timeout — so the value lags the newest use by up to that interval. A token session has no renewal path, so its value is the creation time."`
	// ExpiresAt is the absolute expiry.
	ExpiresAt time.Time `json:"expires_at" required:"true" description:"The absolute expiry, as an RFC 3339 instant in UTC. A browser session can stop being valid earlier, once its idle timeout passes."`
	// ClientIP is the address the entry point reported, or empty.
	ClientIP string `json:"client_ip" required:"true" description:"Empty when the session carries none"`
	// UserAgent is the client the session was opened from, or empty.
	UserAgent string `json:"user_agent" required:"true" description:"Empty when the session carries none"`
	// Current says this is the session the request came with.
	Current bool `json:"current" required:"true" description:"Whether this is the session the request came with"`
}

// AuthnSessionList is the page of the caller's own live sessions.
type AuthnSessionList struct {
	_ struct{} `description:"The caller's own live sessions, in the order the listing reports them."`

	// Sessions are the live browser and token sessions of the account.
	Sessions []AuthnSessionItem `json:"sessions" required:"true" description:"The live browser and token sessions of the account, most recently created first; the empty array when there are none."`
}

// AuthnSessionRevoked is the answer of revoking every session of the caller's
// but the current one.
type AuthnSessionRevoked struct {
	_ struct{} `description:"The outcome of revoking every session of the caller's but the current one."`

	// Revoked is the number of sessions revoked.
	Revoked int `json:"revoked" required:"true" description:"The number of sessions revoked"`
}

type revokeOneInput struct {
	ID string `path:"id" description:"The public session identifier, as returned in the session listing. It names the row and is not a credential."`
}

// challengeHeader is the header every 401 on this module's routes carries: the
// gateway's challenge when it refuses first, or a handler's own fault.
var challengeHeader = speedhttp.Header{
	Name:        "WWW-Authenticate",
	Description: "The capability's challenge: the Bearer and Cookie schemes, never Basic.",
}

func unauthorized(description string) speedhttp.Response {
	response := speedhttp.ProblemResponse(http.StatusUnauthorized, description)
	response.Headers = []speedhttp.Header{challengeHeader}
	return response
}

// commonFailures are the answers every route shares: the gateway's refusal of a
// request with no usable credential. A route's own answer for a status replaces
// the common entry for it.
var commonFailures = []speedhttp.Response{
	unauthorized("No usable credential"),
}

var sessionOperation = speedhttp.NewOperation[struct{}, AuthnSessionView](
	"authn.session.get",
	http.MethodGet,
	sessionPath,
	"Who is signed in on this session",
	"Answers whether the request carries a signed-in identity and, when it does, the facts about it. "+
		"It may be called without a credential: that answer is {\"authenticated\": false} rather than a "+
		"401, so a front end can ask on startup; a credential that is presented and does not hold is "+
		"still a 401. An impersonating identity is answered here, naming both the impersonated account "+
		"and the actor.",
	nil,
	speedhttp.Failures(commonFailures,
		unauthorized("A credential was presented and does not hold"),
		speedhttp.Response{
			Status:      http.StatusOK,
			Description: "The identity on the request, or that nobody is signed in",
			Body:        &speedhttp.Body{MediaType: "application/json"},
		},
	),
	speedhttp.Optional(),
)

var logoutOperation = speedhttp.NewReplyOperation[struct{}](
	"authn.session.logout",
	http.MethodPost,
	logoutPath,
	"Revoke the session the request came with and clear its cookie",
	"Revokes the session the request's credential belongs to and writes a Set-Cookie header that has "+
		"the browser drop the session cookie. When the identity is impersonating, the session revoked is "+
		"the administrator's own, the one the credential belongs to, so the impersonation ends with it. "+
		"A repeated call from the same credential is answered 401 on the replica that revoked the "+
		"session, which clears its verification cache as it revokes. Another replica can still accept it "+
		"while its own check cache holds (check-ttl, five seconds by default), unless the revocation "+
		"event clears that entry first; such a call is answered 204, and it clears that replica's cache "+
		"too, so a further call there is 401 as well.",
	nil,
	speedhttp.Failures(commonFailures,
		speedhttp.ProblemResponse(http.StatusBadRequest, "The request carries no session"),
		speedhttp.ProblemResponse(http.StatusServiceUnavailable, "The session store could not be reached"),
		speedhttp.Response{
			Status:      http.StatusNoContent,
			Description: "The session was revoked",
		},
	),
)

var listOperation = speedhttp.NewOperation[struct{}, AuthnSessionList](
	"authn.session.list",
	http.MethodGet,
	sessionsPath,
	"List the caller's own live sessions",
	"Lists the caller's own live browser and token sessions, most recently created first (by creation "+
		"instant, then by identifier), with the current one flagged. Service-account sessions are not "+
		"listed, and the list is not paginated: every live session of the account is returned. An "+
		"impersonating identity lists the impersonated account's sessions, never the operator's own.",
	nil,
	speedhttp.Failures(commonFailures,
		speedhttp.ProblemResponse(http.StatusServiceUnavailable, "The session store could not be reached"),
		speedhttp.Response{
			Status:      http.StatusOK,
			Description: "The page of the caller's sessions",
			Body:        &speedhttp.Body{MediaType: "application/json"},
		},
	),
)

var revokeOthersOperation = speedhttp.NewOperation[struct{}, AuthnSessionRevoked](
	"authn.session.revokeOthers",
	http.MethodDelete,
	sessionsPath,
	"Revoke every session of the caller's but the current one",
	"Revokes every unrevoked browser and token session of the caller's account except the one the "+
		"request came with, in a single statement, and answers with the number of rows it changed. "+
		"Expiry is not a condition: a session past its expiry that was never revoked is revoked and "+
		"counted too. Service-account sessions are untouched. An impersonating identity revokes the "+
		"impersonated account's sessions; the operator's own session is outside that set and stays.",
	nil,
	speedhttp.Failures(commonFailures,
		speedhttp.ProblemResponse(http.StatusServiceUnavailable, "The session store could not be reached"),
		speedhttp.Response{
			Status:      http.StatusOK,
			Description: "The number of sessions revoked",
			Body:        &speedhttp.Body{MediaType: "application/json"},
		},
	),
)

var revokeOneOperation = speedhttp.NewReplyOperation[revokeOneInput](
	"authn.session.revokeOne",
	http.MethodDelete,
	sessionByID,
	"Revoke one of the caller's own sessions",
	"Revokes one of the caller's own sessions. A session that does not exist, belongs to another "+
		"account, or is a service-account session is answered with the same 403, so the route does not "+
		"reveal which identifiers exist. Revoking the current session also writes a Set-Cookie header "+
		"that has the browser drop the session cookie. Revoking an already revoked session of the "+
		"caller's succeeds.",
	nil,
	speedhttp.Failures(commonFailures,
		speedhttp.ProblemResponse(http.StatusForbidden,
			"The session is not the caller's, does not exist, or is a service-account session"),
		speedhttp.ProblemResponse(http.StatusServiceUnavailable, "The session store could not be reached"),
		speedhttp.Response{
			Status:      http.StatusNoContent,
			Description: "The session was revoked",
		},
	),
)

var operations = speedhttp.Operations{
	sessionOperation,
	logoutOperation,
	listOperation,
	revokeOthersOperation,
	revokeOneOperation,
}
