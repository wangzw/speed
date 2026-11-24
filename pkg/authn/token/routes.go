package token

import (
	"context"
	"encoding/base64"
	"errors"
	"io"
	"log/slog"
	"mime"
	"net/http"
	"net/url"
	"reflect"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/authn/internal/mount"
	speedhttp "github.com/vislake/speed/pkg/http"
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

// The form field names each endpoint reads, taken from the declarations so the
// request reduction and the contract cannot drift apart.
var (
	tokenFormFields  = formFieldNames(reflect.TypeOf(tokenIssueInput{}))
	revokeFormFields = formFieldNames(reflect.TypeOf(revokeInput{}))
)

func formFieldNames(t reflect.Type) []string {
	names := make([]string, 0, t.NumField())
	for i := 0; i < t.NumField(); i++ {
		name := strings.Split(t.Field(i).Tag.Get("form"), ",")[0]
		if name == "" || name == "-" {
			continue
		}
		names = append(names, name)
	}
	return names
}

// mountTokenRoutes registers the token and revocation endpoints through the
// mounter's Callback. Callback is the registration for a route an external
// party reaches from another site: it registers each POST as an open route with
// the cross-origin check bypassed. The two endpoints read no cookie and take
// their client authentication from the Basic header or the form, so the check
// would guard nothing and would keep a cross-origin single-page application from
// calling the endpoint at all; the browser's cross-origin read permission is
// the host's CORS layer (design-authn-session).
func (p *product) mountTokenRoutes() {
	p.mounter.Callback([]string{http.MethodPost}, tokenPath,
		formBody(tokenFormFields, true, tokenOperation.Handle(p.handleToken)))
	p.mounter.Callback([]string{http.MethodPost}, revokePath,
		formBody(revokeFormFields, false, revokeOperation.Handle(p.handleRevoke)))
}

// formBody reduces the request body to the form the operation declares, before
// the entry point binds it. A token endpoint also reads a client credential out
// of the Authorization header first, so the header's usable value is folded into
// the form fields here (basicClient).
//
// The token surface reads its body the way RFC 6749 and RFC 7009 endpoints do,
// and the design fixes that reading: the body is capped at formLimit, a body
// that is not a urlencoded form or cannot be parsed reads as no form at all, a
// key the declaration does not name is ignored, and a repeated key keeps its
// first value. The entry point's own form binding refuses an unknown or
// repeated key and an unsupported Content-Type with a problem document, and an
// OAuth client that sends an extra parameter (token_type_hint and scope are the
// common ones) has to be answered in the RFC 6749 §5.2 shape instead. Reducing
// the body here keeps the answers in that shape while the handler still
// receives the typed input the declaration describes.
func formBody(fields []string, basicClient bool, next gin.HandlerFunc) gin.HandlerFunc {
	return func(c *gin.Context) {
		values := declaredForm(c, fields)
		if basicClient {
			applyBasicCredential(c.GetHeader("Authorization"), values)
		}
		encoded := values.Encode()
		c.Request.Body = io.NopCloser(strings.NewReader(encoded))
		c.Request.ContentLength = int64(len(encoded))
		c.Request.Header.Set("Content-Type", formMediaType)
		next(c)
	}
}

// declaredForm reads the body as a form and keeps the declared fields only. A
// body that is not a form, is over the limit, or does not parse yields an empty
// form, which the handlers answer as a request without the fields they need:
// invalid_request on the token endpoint and a 200 on the revocation endpoint.
func declaredForm(c *gin.Context, fields []string) url.Values {
	out := url.Values{}
	if mediaTypeOf(c.GetHeader("Content-Type")) != formMediaType {
		return out
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, formLimit)
	raw, err := io.ReadAll(c.Request.Body)
	if err != nil {
		return out
	}
	parsed, err := url.ParseQuery(string(raw))
	if err != nil {
		return out
	}
	for _, name := range fields {
		if values := parsed[name]; len(values) > 0 {
			out.Set(name, values[0])
		}
	}
	return out
}

func mediaTypeOf(raw string) string {
	mediaType, _, err := mime.ParseMediaType(raw)
	if err != nil {
		return strings.ToLower(strings.TrimSpace(raw))
	}
	return strings.ToLower(mediaType)
}

// handleToken serves POST B/token: the two grants of the closed set. A body
// that is not a form, one past the limit, and a missing grant_type are
// invalid_request; a grant_type outside the set is unsupported_grant_type.
func (p *product) handleToken(ctx context.Context, in tokenIssueInput, _ *speedhttp.Meta) (speedhttp.Reply, error) {
	switch in.GrantType {
	case grantClientCredentials:
		return p.grantClientCredentials(ctx, in)
	case grantRefreshToken:
		return p.grantRefreshToken(ctx, in)
	case "":
		return oauthReply(http.StatusBadRequest, errInvalidRequest), nil
	default:
		return oauthReply(http.StatusBadRequest, errUnsupportedGrantType), nil
	}
}

// grantClientCredentials serves the client_credentials grant. The client
// identifier is the account identifier and the secret is in the Basic header or
// the form; both being absent is a client that did not authenticate.
func (p *product) grantClientCredentials(ctx context.Context, in tokenIssueInput) (speedhttp.Reply, error) {
	clientID, secret, ok := clientCredential(in)
	if !ok {
		p.logCause(ctx, "the client presented no identifier or secret", slog.LevelInfo, nil)
		return p.invalidClient(ctx, 0), nil
	}
	access, err := p.clientCredentials(ctx, clientID, secret)
	if err != nil {
		return p.answerGrantError(ctx, err), nil
	}
	return tokenSuccess(access, authn.RefreshToken{}, p.clock.Now()), nil
}

func (p *product) grantRefreshToken(ctx context.Context, in tokenIssueInput) (speedhttp.Reply, error) {
	if in.RefreshToken == "" {
		return oauthReply(http.StatusBadRequest, errInvalidRequest), nil
	}
	access, refresh, err := p.Refresh(ctx, in.RefreshToken)
	if err != nil {
		return p.answerGrantError(ctx, err), nil
	}
	return tokenSuccess(access, refresh, p.clock.Now()), nil
}

func clientCredential(in tokenIssueInput) (string, string, bool) {
	if in.ClientID == "" || in.ClientSecret == "" {
		return "", "", false
	}
	return in.ClientID, in.ClientSecret, true
}

// applyBasicCredential writes the client credential an Authorization: Basic
// header carries into the form fields, when the header holds one. The endpoint
// reads the header before the form and ignores the form fields when the header
// holds, which is what writing over them reproduces; a header that does not
// hold leaves the form fields as the request sent them.
func applyBasicCredential(header string, values url.Values) {
	id, secret, ok := basicCredential(header)
	if !ok || id == "" {
		return
	}
	values.Set("client_id", id)
	values.Set("client_secret", secret)
}

// basicCredential reads an HTTP Basic Authorization header the way
// net/http.Request's BasicAuth does: the scheme compared without case, the
// value decoded as standard base64 and split at the first colon. A header that
// does not hold is no credential at all, and the form fields are read instead.
func basicCredential(header string) (string, string, bool) {
	const prefix = "Basic "
	if len(header) < len(prefix) || !strings.EqualFold(header[:len(prefix)], prefix) {
		return "", "", false
	}
	decoded, err := base64.StdEncoding.DecodeString(header[len(prefix):])
	if err != nil {
		return "", "", false
	}
	id, secret, found := strings.Cut(string(decoded), ":")
	if !found {
		return "", "", false
	}
	return id, secret, true
}

// oauthReply builds the RFC 6749 §5.2 failure body: a JSON object whose only
// member is error. It is not the entry point's problem document, whose shape an
// OAuth client cannot read; this is the design's one deliberate exception to
// that shape.
func oauthReply(status int, code string) speedhttp.Reply {
	return speedhttp.JSONReply(status, AuthnTokenOAuthError{Error: code})
}

func tokenSuccess(access authn.AccessToken, refresh authn.RefreshToken, now time.Time) speedhttp.Reply {
	body := AuthnTokenResponse{
		AccessToken: access.Value,
		TokenType:   bearerType,
		ExpiresIn:   expiresInSeconds(access.ExpiresAt, now),
	}
	if refresh.Value != "" {
		body.RefreshToken = refresh.Value
	}
	return speedhttp.JSONReply(http.StatusOK, body)
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

// answerGrantError builds the answer a grant's failure maps to and logs the
// cause. A rejected client or an unusable grant is the caller's problem and is
// recorded at Info; an unavailable dependency is something an operator has to
// look at and is recorded at Warn; a ClaimsContributor that returned a reserved
// claim name is a host defect and is recorded at Error, though the answer stays
// the 503 the unavailable class carries.
func (p *product) answerGrantError(ctx context.Context, err error) speedhttp.Reply {
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
	p.logCause(ctx, cause, level, err)
	if f.code == errInvalidClient {
		return p.invalidClient(ctx, f.retry)
	}
	return oauthReply(f.status, f.code)
}

// invalidClient builds the 401 of a client that did not authenticate, with the
// Basic challenge the scheme expects. When retryAfter is positive the client is
// in back-off and the wait is named in whole seconds, rounded up.
//
// The challenge travels as the reply's dynamic header, declared on the 401. The
// wait is written straight onto the response because it is present only in
// back-off: a declared header with an empty value would be written on every
// 401, and an empty Retry-After is not a value a client can read.
func (p *product) invalidClient(ctx context.Context, retryAfter time.Duration) speedhttp.Reply {
	if retryAfter > 0 {
		mount.SetResponseHeader(ctx, "Retry-After",
			strconv.FormatInt(int64((retryAfter+time.Second-1)/time.Second), 10))
	}
	return speedhttp.WithHeaders(oauthReply(http.StatusUnauthorized, errInvalidClient),
		speedhttp.HeaderValue{Name: "WWW-Authenticate", Value: `Basic realm="` + basicRealm + `"`})
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
func (p *product) handleRevoke(ctx context.Context, in revokeInput, _ *speedhttp.Meta) (speedhttp.Reply, error) {
	if err := p.Revoke(ctx, in.Token); err != nil {
		p.logCause(ctx, "the revocation endpoint could not serve the request", slog.LevelWarn, err)
		return oauthReply(http.StatusServiceUnavailable, errTemporarilyUnavail), nil
	}
	return speedhttp.EmptyReply(http.StatusOK), nil
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
func (p *product) logCause(ctx context.Context, cause string, level slog.Level, err error) {
	msg := diagnosticPrefix + ModuleName + ": " + cause
	if err == nil {
		p.logger.Log(ctx, level, msg)
		return
	}
	p.logger.Log(ctx, level, msg, slog.String("cause", err.Error()))
}
