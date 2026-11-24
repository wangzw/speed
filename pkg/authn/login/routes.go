package login

import (
	"errors"
	"log/slog"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/authn/internal/cookiename"
	"github.com/vislake/speed/pkg/authn/internal/mount"
	"github.com/vislake/speed/pkg/i18n"
	"github.com/vislake/speed/pkg/log"
)

const flowHeader = "Authn-Flow"

type routes struct {
	*product
	endpoint string
}

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
	mount.Declared(m, callbackResumeOperation, mountRel(callbackResumeOperation.Path),
		withRequest(callbackResumeOperation.Handle(r.callbackResume)))

	if p.selfservice != nil {
		p.selfservice.Mount(m, mountRel, p.postFeature)
	}
}

// bindingPrecheck refuses before the entry point reads the body, the order this route fixes.
func (r *routes) bindingPrecheck(c *gin.Context) bool {
	call := r.call(c.Request.Context(), nil)
	if _, _, _, _, ok := r.requireBinding(call, true); !ok {
		call.refuse(c)
		return false
	}
	return true
}

// identityPrecheck refuses before the body is read; an impersonating identity passes.
func (r *routes) identityPrecheck(c *gin.Context) bool {
	id, ok := authn.FromContext(c.Request.Context())
	if !ok || id.Account() == "" {
		mount.Unauthorized(c, r.endpoint, "")
		return false
	}
	return true
}

func (r *routes) setFlowCookie(c *call, token string, maxAge int) {
	c.setCookie(cookiename.Cookie(r.publicURL, cookiename.Spec{
		Stem:     cookiename.Flow,
		Value:    token,
		MaxAge:   maxAge,
		SameSite: http.SameSiteLaxMode,
	}))
}

func (r *routes) readBindingToken(c *call, allowHeader bool) (token string, fromHeader, ok bool) {
	if v := c.cookieValue(r.flowCookie); v != "" {
		return v, false, true
	}
	if allowHeader {
		if v := c.header(flowHeader); v != "" {
			return v, true, true
		}
	}
	return "", false, false
}

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
		errors.Is(err, authn.ErrInvalidIdentifier), errors.Is(err, authn.ErrHumanProofTooLong):
		c.fail(mount.CodeBadRequest)
	case errors.Is(err, authn.ErrUsernameReserved):
		c.fail(mount.CodeUsernameReserved)
	case errors.Is(err, authn.ErrBusy):
		c.busy()
	case errors.Is(err, authn.ErrInvalidCredentials), errors.Is(err, authn.ErrAccountNotFound),
		errors.Is(err, authn.ErrSignupClosed), errors.Is(err, authn.ErrDomainMismatch),
		errors.Is(err, authn.ErrNotProvisioned), errors.Is(err, authn.ErrProvisionRefused),
		errors.Is(err, authn.ErrJITUsernameMissing), errors.Is(err, authn.ErrJITUsernameInvalid),
		errors.Is(err, authn.ErrJITUsernameTaken), errors.Is(err, authn.ErrJITUsernameStrategy),
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

// refusedStep answers a refusal as login-failed, except the two causes that answer 400: an unsupported phone prefix and an identifier that cannot name an account.
func (r *routes) refusedStep(c *call, cause error) {
	switch {
	case errors.Is(cause, authn.ErrUnsupportedRegion):
		c.fail(mount.CodeUnsupportedRegion)
	case errors.Is(cause, authn.ErrInvalidIdentifier):
		c.fail(mount.CodeBadRequest)
	case errors.Is(cause, authn.ErrHumanVerificationRequired):
		c.forbidden(mount.CodeHumanVerificationRequired)
	case errors.Is(cause, authn.ErrHumanProofTooLong):
		c.fail(mount.CodeBadRequest)
	case errors.Is(cause, authn.ErrInvalidArgument):
		c.fail(mount.CodeBadRequest)
	default:
		c.unauthorized(mount.CodeLoginFailed)
	}
}
