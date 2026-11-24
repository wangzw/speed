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

// The fixed shapes of the token surface: formLimit caps the POST body before the
// form is parsed, larger being invalid_request; basicRealm is the client-
// authentication challenge realm; bearerType is the token_type of a success body.
const (
	formLimit  = 8 << 10 // 8 KiB
	basicRealm = "authn-token"
	bearerType = "Bearer"
)

// The grant_type values the token endpoint accepts. The set is closed: authn is
// not an OIDC provider and offers no others.
const (
	grantClientCredentials = "client_credentials"
	grantRefreshToken      = "refresh_token"
)

// The error member of the RFC 6749 §5.2 failure body; the set is closed and the
// endpoint answers nothing else. The body never carries error_description.
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

// mountTokenRoutes registers the token and revocation endpoints as open routes
// with the cross-origin check bypassed: neither reads a cookie, client
// authentication comes from the Basic header or the form, and CORS is the host's.
func (p *product) mountTokenRoutes() {
	p.mounter.Callback([]string{http.MethodPost}, tokenPath,
		formBody(tokenFormFields, true, tokenOperation.Handle(p.handleToken)))
	p.mounter.Callback([]string{http.MethodPost}, revokePath,
		formBody(revokeFormFields, false, revokeOperation.Handle(p.handleRevoke)))
}

// formBody reduces the body to the declared form fields and folds in a Basic
// client credential; it caps the body at formLimit and keeps the answers in the
// RFC 6749 §5.2 shape, with unknown keys ignored and a repeated key keeping its first.
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

// declaredForm reads the body as a form and keeps the declared fields only; a
// body that is not a form, is over the limit, or does not parse yields an empty
// form, which the handlers answer as a request without the fields they need.
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

// applyBasicCredential writes the client credential of an Authorization: Basic
// header over the form fields, since the endpoint reads the header before the
// form; a header that does not hold leaves the form fields as sent.
func applyBasicCredential(header string, values url.Values) {
	id, secret, ok := basicCredential(header)
	if !ok || id == "" {
		return
	}
	values.Set("client_id", id)
	values.Set("client_secret", secret)
}

// basicCredential reads an HTTP Basic Authorization header as
// net/http.Request.BasicAuth does; a header that does not hold is no credential,
// and the form fields are read instead.
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

// oauthReply builds the RFC 6749 §5.2 failure body, a JSON object whose only
// member is error: the one deliberate departure from the entry point's problem
// document, whose shape an OAuth client cannot read.
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

// classifyGrantError maps a grant failure to the endpoint's shape:
// temporarily_unavailable (503) for an unavailable dependency or an unknown
// failure, invalid_client (401) for a bad client, invalid_grant (400) for a bad grant.
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
// cause: Info for a rejected client or unusable grant, Warn for an unavailable
// dependency, and Error for a reserved claim name, though the answer stays 503.
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
// Basic challenge and a Retry-After in whole seconds rounded up when the client
// is in back-off (written straight on, as the declared header is 401-only).
func (p *product) invalidClient(ctx context.Context, retryAfter time.Duration) speedhttp.Reply {
	if retryAfter > 0 {
		mount.SetResponseHeader(ctx, "Retry-After",
			strconv.FormatInt(int64((retryAfter+time.Second-1)/time.Second), 10))
	}
	return speedhttp.WithHeaders(oauthReply(http.StatusUnauthorized, errInvalidClient),
		speedhttp.HeaderValue{Name: "WWW-Authenticate", Value: `Basic realm="` + basicRealm + `"`})
}

// handleRevoke serves POST B/token/revoke: RFC 7009 fixes the answer as 200 with
// an empty body, so a presenter cannot probe a token's state. A dependency that
// cannot answer is the exception, answered temporarily_unavailable (503).
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

// logCause writes one line about a token endpoint failure with the cause as its
// only field. The cause holds no credential value, and this is the only place a
// cause is written, since the response carries none.
func (p *product) logCause(ctx context.Context, cause string, level slog.Level, err error) {
	msg := diagnosticPrefix + ModuleName + ": " + cause
	if err == nil {
		p.logger.Log(ctx, level, msg)
		return
	}
	p.logger.Log(ctx, level, msg, slog.String("cause", err.Error()))
}
