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
// which is the mounter's base less the part this capability owns. The mounter
// registration resolves the same declaration against its base, so the path is
// written once.
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

	Authenticated bool           `json:"authenticated" required:"true" description:"False when nobody is signed in; every other field is then absent"`
	SessionID     string         `json:"session_id,omitempty" description:"The public identifier of the session the request came with; absent when nobody is signed in."`
	AccountID     string         `json:"account_id,omitempty" description:"The account the request is taken to be from; the impersonated account while impersonating"`
	ActorID       string         `json:"actor_id,omitempty" description:"The account whose hands are on the request; present only while impersonating"`
	Impersonating bool           `json:"impersonating,omitempty" description:"True only while the identity is impersonating another account; absent otherwise."`
	Methods       []authn.Method `json:"methods,omitempty" items.enum:"pwd,sms,otp,webauthn,oauth,oidc,saml,static,client-secret" description:"The authentication methods the identity completed, each from the closed set named by the enum; absent when the session records none."`
	Assurance     string         `json:"assurance,omitempty" enum:"none,single,multi" description:"The assurance level the authentication reached; \"none\" when the identity carries no level."`
	AuthTime      *time.Time     `json:"auth_time,omitempty" description:"The instant of the last interactive authentication, absent when the identity carries none"`
	Provider      string         `json:"provider,omitempty" description:"The backend that verified the credential"`
}

// AuthnSessionItem is one live session of the caller's, as the listing reports
// it.
type AuthnSessionItem struct {
	_ struct{} `description:"One live session of the caller's, as the listing reports it."`

	ID         string    `json:"id" required:"true" description:"The public session identifier; it names the row and is not a credential."`
	Kind       string    `json:"kind" required:"true" description:"The session kind, browser or token; service-account sessions are never listed."`
	CreatedAt  time.Time `json:"created_at" required:"true" description:"When the session was created, as an RFC 3339 instant in UTC."`
	LastSeenAt time.Time `json:"last_seen_at" required:"true" description:"Last activity of the session, as an RFC 3339 instant in UTC. For a browser session this is the last use the row was written for: touches are coalesced, written at most once per effective touch interval — the configured interval capped at half the idle timeout — so the value lags the newest use by up to that interval. A token session has no renewal path, so its value is the creation time."`
	ExpiresAt  time.Time `json:"expires_at" required:"true" description:"The absolute expiry, as an RFC 3339 instant in UTC. A browser session can stop being valid earlier, once its idle timeout passes."`
	ClientIP   string    `json:"client_ip" required:"true" description:"Empty when the session carries none"`
	UserAgent  string    `json:"user_agent" required:"true" description:"Empty when the session carries none"`
	Current    bool      `json:"current" required:"true" description:"Whether this is the session the request came with"`
}

// AuthnSessionList is the page of the caller's own live sessions.
type AuthnSessionList struct {
	_ struct{} `description:"The caller's own live sessions, in the order the listing reports them."`

	Sessions []AuthnSessionItem `json:"sessions" required:"true" description:"The live browser and token sessions of the account, most recently created first; the empty array when there are none."`
}

// AuthnSessionRevoked is the answer of revoking every session of the caller's
// but the current one.
type AuthnSessionRevoked struct {
	_ struct{} `description:"The outcome of revoking every session of the caller's but the current one."`

	Revoked int `json:"revoked" required:"true" description:"The number of sessions revoked"`
}

// revokeOneInput is the declaration's input for the route that revokes one
// session by identifier: the identifier is a path parameter and the operation
// takes no body.
type revokeOneInput struct {
	ID string `path:"id" description:"The public session identifier, as returned in the session listing. It names the row and is not a credential."`
}

// challengeHeader declares the header every 401 on this module's routes
// carries: the gateway writes the endpoint's challenge when it refuses the
// request first, and a handler that answers 401 itself returns it in the fault's
// headers. Either way the exchange is a challenge, so the response declares the
// header and a handler's fault may write it.
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

// commonFailures are the answers every route on this module's mounter shares:
// the gateway refuses a request that carries no usable credential before the
// handler runs. A route with an answer of its own replaces the common entry of
// the same status.
var commonFailures = []speedhttp.Response{
	unauthorized("No usable credential"),
}

// sessionOperation declares the read route and binds its types: the handler
// receives no input and returns the view, so the entry point binds the request
// and writes the declared 200.
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

// logoutOperation declares the route that revokes the request's own session.
// The handler builds the answer because the success answer clears the session
// cookie, which the simple form cannot write.
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

// listOperation declares the route that lists the caller's own live sessions.
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

// revokeOthersOperation declares the route that revokes every session of the
// caller's but the current one.
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

// revokeOneOperation declares the route that revokes one of the caller's own
// sessions. The handler builds the answer because revoking the current session
// also clears the cookie.
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

// operations is this module's declaration of the API it serves. The fragment is
// generated from it; the routes are mounted from the same declarations.
var operations = speedhttp.Operations{
	sessionOperation,
	logoutOperation,
	listOperation,
	revokeOthersOperation,
	revokeOneOperation,
}
