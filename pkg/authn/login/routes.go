package login

import (
	"errors"
	"log/slog"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/authn/internal/mount"
	"github.com/vislake/speed/pkg/i18n"
	"github.com/vislake/speed/pkg/log"
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
// routes. Each route is registered from the operation that declares it, so the
// handler's input and answer types, the fragment and the route are one source.
// The callback is registered through Callback, so its cross-site POST skips the
// cross-origin check while every other route keeps it.
func (p *product) mountRoutes() {
	r := &routes{product: p, endpoint: p.mounter.Endpoint()}
	m := p.mounter

	mount.Declared(m, methodsOperation, mountRel(methodsOperation.Path), withRequest(methodsOperation.Handle(r.methods)))
	mount.Declared(m, registerOperation, mountRel(registerOperation.Path),
		withRequest(mount.GuardBody(nil, registerOperation.Handle(r.register))))
	mount.Declared(m, startOperation, mountRel(startOperation.Path),
		withRequest(mount.GuardBody(nil, startOperation.Handle(r.startFlow))))
	mount.Declared(m, startAuthenticatedOperation, mountRel(startAuthenticatedOperation.Path),
		withRequest(mount.GuardBody(r.identityPrecheck, startAuthenticatedOperation.Handle(r.startAuthenticated))),
		mount.Impersonable())
	mount.Declared(m, answerOperation, mountRel(answerOperation.Path),
		withRequest(mount.GuardBody(r.bindingPrecheck, answerOperation.Handle(r.answer))))
	mount.Declared(m, currentOperation, mountRel(currentOperation.Path), withRequest(currentOperation.Handle(r.current)))
	mount.Declared(m, abandonOperation, mountRel(abandonOperation.Path), withRequest(abandonOperation.Handle(r.abandon)))
	mount.Declared(m, passwordResetStartOperation, mountRel(passwordResetStartOperation.Path),
		withRequest(mount.GuardBody(nil, passwordResetStartOperation.Handle(r.passwordResetStart))))
	mount.Declared(m, passwordResetCurrentOperation, mountRel(passwordResetCurrentOperation.Path),
		withRequest(passwordResetCurrentOperation.Handle(r.passwordResetCurrent)))
	mount.Declared(m, passwordResetDeleteOperation, mountRel(passwordResetDeleteOperation.Path),
		withRequest(passwordResetDeleteOperation.Handle(r.passwordResetDelete)))
	mount.Declared(m, passwordResetVerifyOperation, mountRel(passwordResetVerifyOperation.Path),
		withRequest(mount.GuardBody(nil, passwordResetVerifyOperation.Handle(r.passwordResetVerify))))
	mount.Declared(m, passwordResetCompleteOperation, mountRel(passwordResetCompleteOperation.Path),
		withRequest(mount.GuardBody(nil, passwordResetCompleteOperation.Handle(r.passwordResetComplete))))
	m.Callback([]string{http.MethodGet}, mountRel(callbackGetOperation.Path),
		withRequest(callbackGetOperation.Handle(r.callbackGet)))
	m.Callback([]string{http.MethodPost}, mountRel(callbackPostOperation.Path),
		withRequest(callbackPostOperation.Handle(r.callbackPost)))

	if p.selfservice != nil {
		p.selfservice.Mount(m, mountRel, p.postFeature)
	}
}

// bindingPrecheck refuses an answer whose flow binding is missing or wrong
// before the entry point reads the body, which is the order the design gives the
// answer route: the content type, then the binding, then the body's own fields.
// The body is read only after the binding holds.
func (r *routes) bindingPrecheck(c *gin.Context) bool {
	call := r.call(c.Request.Context(), nil)
	if _, _, _, _, ok := r.requireBinding(call, true); !ok {
		call.refuse(c)
		return false
	}
	return true
}

// identityPrecheck refuses a request with no identity before the entry point
// reads the body. It is the check the start handler makes first, moved ahead of
// the body so that the request is not read for a caller the route will refuse
// anyway. An impersonating identity passes: the flow is started on the effective
// account and runs on the operator's session.
func (r *routes) identityPrecheck(c *gin.Context) bool {
	id, ok := authn.FromContext(c.Request.Context())
	if !ok || id.Account() == "" {
		mount.Unauthorized(c, r.endpoint, "")
		return false
	}
	return true
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

// setFlowCookie adds the flow cookie, the carrier of the binding secret for a
// cookie-bound flow. A maxAge of -1 clears it.
func (r *routes) setFlowCookie(c *call, token string, maxAge int) {
	c.setCookie(&http.Cookie{
		Name:     r.cookieName(),
		Value:    token,
		Path:     flowCookiePath,
		MaxAge:   maxAge,
		Secure:   !r.allowInsecure,
		HttpOnly: true,
		SameSite: http.SameSiteNoneMode,
	})
}

// readBindingToken reads the flow token from the request. A cookie-bound route
// reads the cookie; an answer route reads the cookie first and the header next,
// so that a header-bound client can carry it without a cookie. A callback reads
// only the cookie, because a user agent navigation cannot set a header.
// fromHeader reports that the token arrived in the Authn-Flow header, which is
// what makes a reply echo it in its body.
func (r *routes) readBindingToken(c *call, allowHeader bool) (token string, fromHeader, ok bool) {
	if v := c.cookieValue(r.cookieName()); v != "" {
		return v, false, true
	}
	if allowHeader {
		if v := c.header(flowHeader); v != "" {
			return v, true, true
		}
	}
	return "", false, false
}

// requireBinding loads the flow the request is bound to. It answers the request
// and returns false when the binding is missing or does not match, which is the
// 401 flow-invalid that covers a missing cookie, a wrong header and a binding
// that names another flow.
func (r *routes) requireBinding(c *call, allowHeader bool) (token string, echo string, rec record, raw []byte, ok bool) {
	found, fromHeader, present := r.readBindingToken(c, allowHeader)
	if !present {
		c.unauthorized(mount.CodeFlowInvalid)
		return "", "", record{}, nil, false
	}
	loaded, rawBytes, err := r.loadFlow(c.ctx, found)
	if err != nil {
		r.fail(c, err)
		return "", "", record{}, nil, false
	}
	if fromHeader {
		echo = found
	}
	return found, echo, loaded, rawBytes, true
}

// localeOf reads the language a request names and passes it through as it
// stands. It reads first the language the i18n layer settled into the request
// context — the supported locale the request's header asked for, negotiated by
// quality — and falls back to reading the header directly for a run with no i18n
// layer, taking the first tag as written. Either way the value is a BCP 47 tag
// the SMS module resolves against the languages its carrier declares: the empty
// string names none and resolves to the run's default.
func localeOf(c *call) string {
	if locale, ok := i18n.FromContext(c.ctx); ok {
		return string(locale)
	}
	header := c.header("Accept-Language")
	for _, part := range strings.Split(header, ",") {
		tag := strings.TrimSpace(strings.SplitN(part, ";", 2)[0])
		if tag == "" {
			continue
		}
		return tag
	}
	return ""
}

// fail maps an error from the engine or a dependency to the answer the design
// gives it. Every case names a code; the default is the 503 that never says
// which dependency failed.
func (r *routes) fail(c *call, err error) {
	var throttled *authn.ThrottledError
	switch {
	case err == nil:
		return
	case errors.As(err, &throttled):
		c.throttled(throttled.RetryAfter)
	case errors.Is(err, authn.ErrThrottled):
		c.throttled(0)
	case errors.Is(err, authn.ErrFlowInvalid), errors.Is(err, authn.ErrStepOutOfOrder):
		c.unauthorized(mount.CodeFlowInvalid)
	case errors.Is(err, authn.ErrMethodDisabled):
		c.forbidden(mount.CodeMethodDisabled)
	case errors.Is(err, authn.ErrImpersonationRestricted):
		c.forbidden(mount.CodeImpersonationRestricted)
	case errors.Is(err, authn.ErrAccountUnavailable):
		c.forbidden(mount.CodeAccountUnavailable)
	case errors.Is(err, authn.ErrLinkRequired):
		var conflicted *authn.LinkRequiredError
		if errors.As(err, &conflicted) {
			c.failureEmail = maskEmail(conflicted.Email)
		}
		c.unauthorized(mount.CodeLinkRequired)
	case errors.Is(err, authn.ErrContactTaken):
		// The address a signup declared was taken before its account write: the
		// caller is in the link-required position the earlier read would have put
		// them in.
		c.unauthorized(mount.CodeLinkRequired)
	case errors.Is(err, authn.ErrBindingConflict), errors.Is(err, authn.ErrAccountExists),
		errors.Is(err, authn.ErrLastFactor),
		errors.Is(err, authn.ErrFactorLimit):
		c.fail(mount.CodeConflict)
	case errors.Is(err, authn.ErrLastLoginMethod):
		c.fail(mount.CodeLastLoginMethod)
	case errors.Is(err, authn.ErrIdentifierRequired):
		c.fail(mount.CodeLoginIdentifierRequired)
	case errors.Is(err, authn.ErrUnsupportedRegion):
		c.fail(mount.CodeUnsupportedRegion)
	case authn.IsUsernameShape(err):
		c.fail(mount.CodeUsernameInvalid)
	case errors.Is(err, authn.ErrPasswordPolicy), errors.Is(err, authn.ErrInvalidArgument),
		errors.Is(err, authn.ErrInvalidIdentifier):
		c.fail(mount.CodeBadRequest)
	case errors.Is(err, authn.ErrUsernameReserved):
		c.fail(mount.CodeUsernameReserved)
	case errors.Is(err, authn.ErrBusy):
		c.busy()
	case errors.Is(err, authn.ErrInvalidCredentials), errors.Is(err, authn.ErrAccountNotFound),
		errors.Is(err, authn.ErrSignupClosed), errors.Is(err, authn.ErrDomainMismatch),
		errors.Is(err, authn.ErrNotProvisioned), errors.Is(err, authn.ErrProvisionRefused),
		errors.Is(err, authn.ErrNoFactor), errors.Is(err, authn.ErrFactorNotEnrolled):
		c.unauthorized(mount.CodeLoginFailed)
	case errors.Is(err, authn.ErrHumanVerificationRequired):
		c.forbidden(mount.CodeHumanVerificationRequired)
	case errors.Is(err, authn.ErrUnavailable):
		log.FromContext(c.ctx).Warn(ModuleName+": a dependency of a login route is unavailable",
			slog.String("cause", err.Error()))
		c.unavailable()
	default:
		log.FromContext(c.ctx).Error(ModuleName+": a login route failed on a call it cannot answer for",
			slog.String("cause", err.Error()))
		c.unavailable()
	}
}

// refusedStep answers a first-factor method's refusal. A refusal is a credential
// that did not hold, which is login-failed, except for the two causes the design
// answers as a 400: a phone prefix the deployment does not allow, and an
// identifier that cannot name an account. The cause goes to no body.
func (r *routes) refusedStep(c *call, cause error) {
	switch {
	case errors.Is(cause, authn.ErrUnsupportedRegion):
		c.fail(mount.CodeUnsupportedRegion)
	case errors.Is(cause, authn.ErrInvalidIdentifier):
		c.fail(mount.CodeBadRequest)
	default:
		c.unauthorized(mount.CodeLoginFailed)
	}
}
