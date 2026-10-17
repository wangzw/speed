package admin

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/vislake/speed/pkg/audit"
	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/authn/internal/mount"
)

// caller reads the identity the gateway put on the request. The routes are
// guarded, so a request that reaches a handler carries one; an absent identity
// is answered with the gateway's own 401 rather than read as a caller.
func (m *product) caller(c *gin.Context) (authn.Identity, bool) {
	id, ok := authn.FromContext(c.Request.Context())
	if !ok {
		mount.Unauthorized(c, m.mounter.Endpoint(), "")
		return authn.Identity{}, false
	}
	return id, true
}

// admit applies the two checks every management route makes before it reads
// anything: the actor is not impersonating, and a Decider is there. It answers
// and returns false when either fails, so that a deployment with no Decider
// tells a caller nothing about which objects exist.
func (m *product) admit(c *gin.Context, id authn.Identity) bool {
	if id.Impersonating() {
		mount.Forbidden(c, mount.CodeImpersonationRestricted)
		return false
	}
	if m.decider == nil {
		mount.Forbidden(c, mount.CodeForbidden)
		return false
	}
	return true
}

// asks puts one Decision to the Decider. It returns true only when the action
// is allowed; a refusal is a 403 and an error or a panic is a 503, each already
// answered, and the handler returns.
func (m *product) asks(c *gin.Context, d authn.Decision) bool {
	allow, err := m.callDecider(c.Request.Context(), d)
	switch {
	case err != nil:
		m.cause(c, "asking the Decider", err)
		return false
	case !allow:
		mount.Forbidden(c, mount.CodeForbidden)
		return false
	}
	return true
}

// callDecider calls Decide and turns a returned error and a panic alike into
// ErrUnavailable, so that a defective Decider is refused rather than taken for a
// permission or a decline, and never takes the request down with it.
func (m *product) callDecider(ctx context.Context, d authn.Decision) (allow bool, err error) {
	defer func() {
		if rec := recover(); rec != nil {
			allow, err = false, fmt.Errorf("%w: the Decider panicked", authn.ErrUnavailable)
		}
	}()
	allow, err = m.decider.Decide(ctx, d)
	if err != nil {
		return false, fmt.Errorf("%w: the Decider could not answer", authn.ErrUnavailable)
	}
	return allow, nil
}

// require holds the actor to a strength requirement, answering the step-up 401
// and returning false when it falls short. A zero requirement asks for nothing.
func (m *product) require(c *gin.Context, id authn.Identity, req authn.Requirement) bool {
	if req == (authn.Requirement{}) {
		return true
	}
	if id.Satisfies(req, m.now()) {
		return true
	}
	mount.StepUpRequired(c, id, req)
	return false
}

// auditRequired writes the mandatory audit row of a write, outside any
// transaction and before the action. A row that does not land is the module's
// own failure or the audit module's state, and the action is not carried out:
// the answer is 503 and the handler returns.
func (m *product) auditRequired(c *gin.Context, e audit.Entry) bool {
	if err := m.audit.Record(c.Request.Context(), e); err != nil {
		m.logger.Error(ModuleName+": the mandatory audit row could not be written, so the action was not carried out",
			slog.String("action", e.Action), slog.String("cause", err.Error()))
		mount.Unavailable(c)
		return false
	}
	return true
}

// auditBestEffort writes an audit row that does not block the action. A failure
// is an Error line and changes nothing about the answer.
func (m *product) auditBestEffort(ctx context.Context, e audit.Entry) {
	if err := m.audit.Record(ctx, e); err != nil {
		m.logger.Error(ModuleName+": an audit row could not be written",
			slog.String("action", e.Action), slog.String("cause", err.Error()))
	}
}

// cause logs a failure and answers 503, never telling the caller which
// dependency failed.
func (m *product) cause(c *gin.Context, op string, err error) {
	m.logger.Error(ModuleName+": "+op+" failed", slog.String("cause", err.Error()))
	mount.Unavailable(c)
}

// answer maps a failure a capability returned to the response it belongs to.
// A failure the route did not cause that no sentinel names is a 503.
func (m *product) answer(c *gin.Context, op string, err error) {
	switch {
	case errors.Is(err, authn.ErrAccountNotFound), errors.Is(err, authn.ErrSessionNotFound),
		errors.Is(err, authn.ErrProviderNotFound), errors.Is(err, authn.ErrNotImpersonating):
		c.Status(http.StatusNotFound)
		c.Writer.WriteHeaderNow()
	case errors.Is(err, authn.ErrInvalidArgument), errors.Is(err, authn.ErrInvalidIdentifier),
		errors.Is(err, authn.ErrPasswordPolicy), errors.Is(err, authn.ErrInvalidConfig),
		errors.Is(err, authn.ErrIdentifierRequired):
		mount.Fail(c, mount.CodeBadRequest)
	case errors.Is(err, authn.ErrAccountExists), errors.Is(err, authn.ErrBindingConflict),
		errors.Is(err, authn.ErrDomainClaimed), errors.Is(err, authn.ErrLastLoginMethod),
		errors.Is(err, authn.ErrFactorLimit), errors.Is(err, authn.ErrLastFactor):
		mount.Fail(c, mount.CodeConflict)
	case errors.Is(err, authn.ErrUnsupportedRegion):
		mount.Fail(c, mount.CodeUnsupportedRegion)
	default:
		m.cause(c, op, err)
	}
}

// notFound answers an administration route whose object is not there: a bare
// 404, the shape the design states for a missing account, session, provider or
// record, carrying no problem code of this capability.
func (m *product) notFound(c *gin.Context) {
	c.Status(http.StatusNotFound)
	c.Writer.WriteHeaderNow()
}

// bad answers a body or a query parameter the route refuses: a 400 with this
// capability's code. It is for shape problems the handler judges itself, ahead
// of any capability call.
func (m *product) bad(c *gin.Context) {
	mount.Fail(c, mount.CodeBadRequest)
}
