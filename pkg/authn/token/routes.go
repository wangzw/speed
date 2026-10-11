package token

import (
	"errors"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/vislake/speed/pkg/authn"
)

// The fixed shapes of the token surface. formLimit is the longest body the two
// POST endpoints read before the form is parsed; a larger body is
// invalid_request. basicRealm is the protection space of the client-
// authentication challenge the token endpoint answers invalid_client with, the
// name a Basic scheme asks the client for. bearerType is the token_type of a
// success body.
const (
	formLimit  = 8 << 10 // 8 KiB
	basicRealm = "authn-token"
	bearerType = "Bearer"
)

// The grant_type values the token endpoint accepts. The set is closed and the
// design fixes it: authn is not an OIDC provider and offers no others.
const (
	grantClientCredentials = "client_credentials"
	grantRefreshToken      = "refresh_token"
)

// The error member of the RFC 6749 §5.2 failure body. The set is closed and the
// endpoint answers nothing else. The body never carries error_description: a
// cause stays in the log, and an OAuth client library reads the error field
// alone.
const (
	errInvalidRequest       = "invalid_request"
	errInvalidClient        = "invalid_client"
	errInvalidGrant         = "invalid_grant"
	errUnsupportedGrantType = "unsupported_grant_type"
	errTemporarilyUnavail   = "temporarily_unavailable"
)

// mountTokenRoutes registers the token and revocation endpoints through the
// mounter's Callback. Callback is the registration for a route an external
// party reaches from another site: it registers each POST as an open route with
// the cross-origin check bypassed. The two endpoints read no cookie and take
// their client authentication from the Basic header or the form, so the check
// would guard nothing and would keep a cross-origin single-page application from
// calling the endpoint at all; the browser's cross-origin read permission is
// the host's CORS layer (design-authn-session).
func (p *product) mountTokenRoutes() {
	p.mounter.Callback([]string{http.MethodPost}, tokenPath, p.handleToken)
	p.mounter.Callback([]string{http.MethodPost}, revokePath, p.handleRevoke)
}

// tokenResponse is the success body of POST B/token. refresh_token is present
// only for the refresh_token grant; a client_credentials response carries none.
type tokenResponse struct {
	AccessToken  string `json:"access_token"`
	TokenType    string `json:"token_type"`
	ExpiresIn    int    `json:"expires_in"`
	RefreshToken string `json:"refresh_token,omitempty"`
}

// handleToken serves POST B/token: the two grants of the closed set. A body
// that is not a form, one past the limit, and a missing grant_type are
// invalid_request; a grant_type outside the set is unsupported_grant_type.
func (p *product) handleToken(c *gin.Context) {
	form, ok := parseForm(c)
	if !ok {
		p.oauthError(c, http.StatusBadRequest, errInvalidRequest)
		return
	}
	switch form.Get("grant_type") {
	case grantClientCredentials:
		p.grantClientCredentials(c, form)
	case grantRefreshToken:
		p.grantRefreshToken(c, form)
	case "":
		p.oauthError(c, http.StatusBadRequest, errInvalidRequest)
	default:
		p.oauthError(c, http.StatusBadRequest, errUnsupportedGrantType)
	}
}

// grantClientCredentials serves the client_credentials grant. The client
// identifier is the account identifier and the secret is in the Basic header or
// the form; both being absent is a client that did not authenticate.
func (p *product) grantClientCredentials(c *gin.Context, form url.Values) {
	clientID, secret, ok := clientCredential(c.Request, form)
	if !ok {
		p.logCause(c, "the client presented no identifier or secret", slog.LevelInfo, nil)
		p.invalidClient(c, 0)
		return
	}
	access, err := p.clientCredentials(c.Request.Context(), clientID, secret)
	if err != nil {
		p.answerGrantError(c, err)
		return
	}
	p.writeToken(c, access, authn.RefreshToken{})
}

// grantRefreshToken serves the refresh_token grant. A missing refresh_token is
// invalid_request; a token that does not rotate is invalid_grant.
func (p *product) grantRefreshToken(c *gin.Context, form url.Values) {
	presented := form.Get("refresh_token")
	if presented == "" {
		p.oauthError(c, http.StatusBadRequest, errInvalidRequest)
		return
	}
	access, refresh, err := p.Refresh(c.Request.Context(), presented)
	if err != nil {
		p.answerGrantError(c, err)
		return
	}
	p.writeToken(c, access, refresh)
}

// clientCredential reads the client identifier and secret a request presents:
// the Basic header when there is a usable one, and otherwise the client_id and
// client_secret form fields. The second result is false when neither names a
// client and a secret.
func clientCredential(r *http.Request, form url.Values) (string, string, bool) {
	if id, secret, ok := r.BasicAuth(); ok && id != "" {
		return id, secret, true
	}
	id := form.Get("client_id")
	secret := form.Get("client_secret")
	if id == "" || secret == "" {
		return "", "", false
	}
	return id, secret, true
}

// grantFailure is how a failure of a grant is answered: the status, the error
// member, and the wait of a client in back-off.
type grantFailure struct {
	status int
	code   string
	retry  time.Duration
}

// classifyGrantError maps a failure of a grant to the endpoint's shape. A
// missing signing key and an unavailable dependency are temporarily_unavailable
// (503), never invalid_grant, so the client keeps its refresh token and retries.
// A client credential that did not verify, and a client in back-off, are
// invalid_client (401). An unusable grant is invalid_grant (400). Anything else
// is answered temporarily_unavailable as well: the direction is refuse, and the
// cause stays in the log.
func classifyGrantError(err error) grantFailure {
	switch {
	case errors.Is(err, authn.ErrNoSigningKey), errors.Is(err, authn.ErrUnavailable):
		return grantFailure{status: http.StatusServiceUnavailable, code: errTemporarilyUnavail}
	case errors.Is(err, authn.ErrInvalidCredentials):
		return grantFailure{status: http.StatusUnauthorized, code: errInvalidClient}
	case errors.Is(err, authn.ErrThrottled):
		var throttled *authn.ThrottledError
		if errors.As(err, &throttled) {
			return grantFailure{status: http.StatusUnauthorized, code: errInvalidClient, retry: throttled.RetryAfter}
		}
		return grantFailure{status: http.StatusUnauthorized, code: errInvalidClient}
	case errors.Is(err, authn.ErrInvalidGrant), errors.Is(err, authn.ErrRefreshReuse),
		errors.Is(err, authn.ErrSessionRevoked):
		return grantFailure{status: http.StatusBadRequest, code: errInvalidGrant}
	default:
		return grantFailure{status: http.StatusServiceUnavailable, code: errTemporarilyUnavail}
	}
}

// answerGrantError writes the answer a grant's failure maps to and logs the
// cause. A rejected client or an unusable grant is the caller's problem and is
// recorded at Info; an unavailable dependency is something an operator has to
// look at and is recorded at Warn; a ClaimsContributor that returned a reserved
// claim name is a host defect and is recorded at Error, though the answer stays
// the 503 the unavailable class carries.
func (p *product) answerGrantError(c *gin.Context, err error) {
	f := classifyGrantError(err)
	level := slog.LevelInfo
	cause := "the grant is not usable"
	switch {
	case errors.Is(err, authn.ErrReservedClaim):
		level = slog.LevelError
		cause = "the token endpoint could not serve the request"
	case f.code == errTemporarilyUnavail:
		level = slog.LevelWarn
		cause = "the token endpoint could not serve the request"
	}
	p.logCause(c, cause, level, err)
	if f.code == errInvalidClient {
		p.invalidClient(c, f.retry)
		return
	}
	p.oauthError(c, f.status, f.code)
}

// invalidClient writes the 401 of a client that did not authenticate, with the
// Basic challenge the scheme expects. When retryAfter is positive the client is
// in back-off and the wait is named in whole seconds, rounded up.
func (p *product) invalidClient(c *gin.Context, retryAfter time.Duration) {
	c.Header("WWW-Authenticate", `Basic realm="`+basicRealm+`"`)
	if retryAfter > 0 {
		c.Header("Retry-After", strconv.FormatInt(int64((retryAfter+time.Second-1)/time.Second), 10))
	}
	p.oauthError(c, http.StatusUnauthorized, errInvalidClient)
}

// oauthError writes the RFC 6749 §5.2 failure body: a JSON object whose only
// member is error. It does not go through the entry point's Fail, whose
// problem+json an OAuth client cannot read; this is the design's one deliberate
// exception to that shape.
func (p *product) oauthError(c *gin.Context, status int, code string) {
	c.JSON(status, gin.H{"error": code})
}

// writeToken writes the success body and the cache headers RFC 6749 §5.1
// requires of a response that carries a token. The mounter already put
// Cache-Control: no-store on the response; Pragma: no-cache joins it for a
// client that only understands the older header. expires_in is the access
// token's remaining lifetime in whole seconds.
func (p *product) writeToken(c *gin.Context, access authn.AccessToken, refresh authn.RefreshToken) {
	c.Header("Pragma", "no-cache")
	body := tokenResponse{
		AccessToken: access.Value,
		TokenType:   bearerType,
		ExpiresIn:   expiresInSeconds(access.ExpiresAt, p.clock.Now()),
	}
	if refresh.Value != "" {
		body.RefreshToken = refresh.Value
	}
	c.JSON(http.StatusOK, body)
}

// handleRevoke serves POST B/token/revoke. RFC 7009 and the design fix the
// answer for a token as 200 with an empty body, so a presenter cannot use the
// endpoint to probe a token's state: the token is revoked when it is one the
// module recognises, and a body that cannot be read simply revokes nothing.
//
// A dependency that cannot answer is the one exception, and Revoke reports it
// alone: the revocation did not happen, so the endpoint answers the token
// surface's RFC 6749 §5.2 shape with temporarily_unavailable rather than report
// a success the session does not back.
func (p *product) handleRevoke(c *gin.Context) {
	form, ok := parseForm(c)
	if !ok {
		c.Status(http.StatusOK)
		return
	}
	if err := p.Revoke(c.Request.Context(), form.Get("token")); err != nil {
		p.logCause(c, "the revocation endpoint could not serve the request", slog.LevelWarn, err)
		p.oauthError(c, http.StatusServiceUnavailable, errTemporarilyUnavail)
		return
	}
	c.Status(http.StatusOK)
}

// parseForm reads the request body as a form under the endpoint's limit. The
// second result is false when the body is not a form, is past the limit, or
// cannot be read; the caller chooses the answer, because the token endpoint
// refuses such a request and the revocation endpoint answers 200 whatever.
func parseForm(c *gin.Context) (url.Values, bool) {
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, formLimit)
	if err := c.Request.ParseForm(); err != nil {
		return nil, false
	}
	return c.Request.PostForm, true
}

// expiresInSeconds is the remaining lifetime of an access token in whole
// seconds, rounded up so a positive lifetime never reads as zero. A token
// already past its expiry reads as zero.
func expiresInSeconds(expiresAt, now time.Time) int {
	d := expiresAt.Sub(now)
	if d <= 0 {
		return 0
	}
	return int((d + time.Second - 1) / time.Second)
}

// logCause writes one line about a failure of the token endpoint, with the
// cause as its only field. The cause text holds no token, no refresh token and
// no secret: a presented credential is only ever handled as its digest, so what
// is logged here is the shape of the failure and never a value. It is the only
// place a cause is written, since the response carries none.
func (p *product) logCause(c *gin.Context, cause string, level slog.Level, err error) {
	msg := diagnosticPrefix + ModuleName + ": " + cause
	if err == nil {
		p.logger.Log(c.Request.Context(), level, msg)
		return
	}
	p.logger.Log(c.Request.Context(), level, msg, slog.String("cause", err.Error()))
}
