package login

import (
	"errors"
	"log/slog"
	"mime"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/authn/internal/mount"
	"github.com/vislake/speed/pkg/log"
)

// The route paths this module mounts, relative to the base path. A callback's
// provider segment is a login method's name for a social method and an
// enterprise provider's identifier for SSO.
const (
	pathMethods      = "/methods"
	pathFlows        = "/flows"
	pathFlowsAuth    = "/flows/authenticated"
	pathFlowsAnswer  = "/flows/answer"
	pathFlowsCurrent = "/flows/current"
	pathCallback     = "/callback/:provider"
)

// The carriers a flow token travels in. The cookie keeps its __Host- prefix and
// its Secure attribute unless insecure use was allowed; SameSite=None lets it
// ride an IdP-initiated cross-site POST, which a Lax cookie would not.
const (
	flowCookieName  = "__Host-authn-flow"
	flowCookiePlain = "authn-flow"
	flowHeader      = "Authn-Flow"
	flowCookiePath  = "/"
)

// routes binds the product to the endpoint its routes were mounted on, whose
// name is the realm of every 401 a handler writes.
type routes struct {
	*product
	endpoint string
}

// mountRoutes registers this module's routes and the account self-service
// routes. The callback is registered through Callback, so its cross-site POST
// skips the cross-origin check while every other route keeps it.
func (p *product) mountRoutes() {
	r := &routes{product: p, endpoint: p.mounter.Endpoint()}
	m := p.mounter

	m.Public(http.MethodGet, pathMethods, r.methods)
	m.Public(http.MethodPost, pathFlows, r.startFlow)
	m.Guarded(http.MethodPost, pathFlowsAuth, r.startAuthenticated)
	m.Public(http.MethodPost, pathFlowsAnswer, r.answer)
	m.Public(http.MethodGet, pathFlowsCurrent, r.current)
	m.Public(http.MethodDelete, pathFlowsCurrent, r.abandon)
	m.Callback([]string{http.MethodGet, http.MethodPost}, pathCallback, r.callback)

	if p.selfservice != nil {
		p.selfservice.Mount(m, p.postFeature)
	}
}

// cookieName is the name of the flow cookie: the prefixed one in production, the
// plain one under insecure use, as the __Host- prefix requires Secure and a path
// of "/" and forbids a Domain.
func (r *routes) cookieName() string {
	if r.allowInsecure {
		return flowCookiePlain
	}
	return flowCookieName
}

// setFlowCookie writes the flow cookie, the carrier of the binding secret for a
// cookie-bound flow.
func (r *routes) setFlowCookie(c *gin.Context, token string, maxAge int) {
	c.SetSameSite(http.SameSiteNoneMode)
	c.SetCookie(r.cookieName(), token, maxAge, flowCookiePath, "", !r.allowInsecure, true)
}

// readBindingToken reads the flow token from the request. A cookie-bound route
// reads the cookie; an answer route reads the cookie first and the header next,
// so that a header-bound client can carry it without a cookie. A callback reads
// only the cookie, because a user agent navigation cannot set a header.
// fromHeader reports that the token arrived in the Authn-Flow header, which is
// what makes a reply echo it in its body.
func (r *routes) readBindingToken(c *gin.Context, allowHeader bool) (token string, fromHeader, ok bool) {
	if v, err := c.Cookie(r.cookieName()); err == nil && v != "" {
		return v, false, true
	}
	if allowHeader {
		if v := c.GetHeader(flowHeader); v != "" {
			return v, true, true
		}
	}
	return "", false, false
}

// requireBinding loads the flow the request is bound to. It answers the request
// and returns false when the binding is missing or does not match, which is the
// 401 flow-invalid that covers a missing cookie, a wrong header and a binding
// that names another flow.
func (r *routes) requireBinding(c *gin.Context, allowHeader bool) (token string, echo string, rec record, raw []byte, ok bool) {
	found, fromHeader, present := r.readBindingToken(c, allowHeader)
	if !present {
		mount.Unauthorized(c, r.endpoint, mount.CodeFlowInvalid)
		return "", "", record{}, nil, false
	}
	loaded, rawBytes, err := r.loadFlow(c.Request.Context(), found)
	if err != nil {
		r.fail(c, err)
		return "", "", record{}, nil, false
	}
	if fromHeader {
		echo = found
	}
	return found, echo, loaded, rawBytes, true
}

// contentTypeJSON checks the request's Content-Type before the body is read, so
// that a body of another type is the 415 the design orders before the binding
// check. mount.Decode checks it again when it reads the body.
func contentTypeJSON(c *gin.Context) bool {
	value := c.GetHeader("Content-Type")
	media, params, err := mime.ParseMediaType(value)
	if err != nil || media != "application/json" {
		mount.Fail(c, mount.CodeUnsupportedMediaType)
		return false
	}
	for k, v := range params {
		if k != "charset" || !strings.EqualFold(v, "utf-8") {
			mount.Fail(c, mount.CodeUnsupportedMediaType)
			return false
		}
	}
	return true
}

// requestClient is where the request came from, for logs and audit. The entry
// point does not trust forwarded headers, so the address is the peer's.
func requestClient(c *gin.Context) authn.ClientInfo {
	return authn.ClientInfo{IP: c.ClientIP(), UserAgent: c.Request.UserAgent()}
}

// fail maps an error from the engine or a dependency to the answer the design
// gives it. Every case names a code; the default is the 503 that never says
// which dependency failed.
func (r *routes) fail(c *gin.Context, err error) {
	ctx := c.Request.Context()
	var throttled *authn.ThrottledError
	switch {
	case err == nil:
		return
	case errors.As(err, &throttled):
		mount.Throttled(c, throttled.RetryAfter)
	case errors.Is(err, authn.ErrThrottled):
		mount.Throttled(c, 0)
	case errors.Is(err, authn.ErrFlowInvalid), errors.Is(err, authn.ErrStepOutOfOrder):
		mount.Unauthorized(c, r.endpoint, mount.CodeFlowInvalid)
	case errors.Is(err, authn.ErrMethodDisabled):
		mount.Forbidden(c, mount.CodeMethodDisabled)
	case errors.Is(err, authn.ErrImpersonationRestricted):
		mount.Forbidden(c, mount.CodeImpersonationRestricted)
	case errors.Is(err, authn.ErrAccountUnavailable):
		mount.Forbidden(c, mount.CodeAccountUnavailable)
	case errors.Is(err, authn.ErrLinkRequired):
		mount.Unauthorized(c, r.endpoint, mount.CodeLinkRequired)
	case errors.Is(err, authn.ErrBindingConflict), errors.Is(err, authn.ErrAccountExists),
		errors.Is(err, authn.ErrLastLoginMethod), errors.Is(err, authn.ErrLastFactor),
		errors.Is(err, authn.ErrFactorLimit):
		mount.Fail(c, mount.CodeConflict)
	case errors.Is(err, authn.ErrIdentifierRequired):
		mount.Fail(c, mount.CodeLoginIdentifierRequired)
	case errors.Is(err, authn.ErrUnsupportedRegion):
		mount.Fail(c, mount.CodeUnsupportedRegion)
	case errors.Is(err, authn.ErrPasswordPolicy), errors.Is(err, authn.ErrInvalidArgument),
		errors.Is(err, authn.ErrInvalidIdentifier):
		mount.Fail(c, mount.CodeBadRequest)
	case errors.Is(err, authn.ErrBusy):
		mount.Busy(c)
	case errors.Is(err, authn.ErrInvalidCredentials), errors.Is(err, authn.ErrAccountNotFound),
		errors.Is(err, authn.ErrSignupClosed), errors.Is(err, authn.ErrDomainMismatch),
		errors.Is(err, authn.ErrNotProvisioned), errors.Is(err, authn.ErrProvisionRefused),
		errors.Is(err, authn.ErrNoFactor), errors.Is(err, authn.ErrFactorNotEnrolled):
		mount.Unauthorized(c, r.endpoint, mount.CodeLoginFailed)
	case errors.Is(err, authn.ErrUnavailable):
		log.FromContext(ctx).Warn(ModuleName+": a dependency of a login route is unavailable",
			slog.String("cause", err.Error()))
		mount.Unavailable(c)
	default:
		log.FromContext(ctx).Error(ModuleName+": a login route failed on a call it cannot answer for",
			slog.String("cause", err.Error()))
		mount.Unavailable(c)
	}
}

// refusedStep answers a first-factor method's refusal. A refusal is a credential
// that did not hold, which is login-failed, except for the two causes the design
// answers as a 400: a phone prefix the deployment does not allow, and an
// identifier that cannot name an account. The cause goes to no body.
func (r *routes) refusedStep(c *gin.Context, cause error) {
	switch {
	case errors.Is(cause, authn.ErrUnsupportedRegion):
		mount.Fail(c, mount.CodeUnsupportedRegion)
	case errors.Is(cause, authn.ErrInvalidIdentifier):
		mount.Fail(c, mount.CodeBadRequest)
	default:
		mount.Unauthorized(c, r.endpoint, mount.CodeLoginFailed)
	}
}
