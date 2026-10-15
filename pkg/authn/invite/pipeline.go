package invite

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

// caller reads the identity the gateway put on the request. The management
// routes are guarded, so a request that reaches a handler carries one; an
// absent identity is answered with the gateway's own 401 rather than read as a
// caller.
func (p *product) caller(c *gin.Context) (authn.Identity, bool) {
	id, ok := authn.FromContext(c.Request.Context())
	if !ok {
		mount.Unauthorized(c, p.mounter.Endpoint(), "")
		return authn.Identity{}, false
	}
	return id, true
}

// admit applies the two checks every management route makes before it reads
// anything: the actor is not impersonating, and a Decider is there.
func (p *product) admit(c *gin.Context, id authn.Identity) bool {
	if id.Impersonating() {
		mount.Forbidden(c, mount.CodeImpersonationRestricted)
		return false
	}
	if p.decider == nil {
		mount.Forbidden(c, mount.CodeForbidden)
		return false
	}
	return true
}

// asks puts one Decision to the Decider. It returns true only when the action
// is allowed; a refusal is a 403 and an error or a panic is a 503.
func (p *product) asks(c *gin.Context, d authn.Decision) bool {
	allow, err := p.callDecider(c.Request.Context(), d)
	switch {
	case err != nil:
		p.cause(c, "asking the Decider", err)
		return false
	case !allow:
		mount.Forbidden(c, mount.CodeForbidden)
		return false
	}
	return true
}

// callDecider calls Decide and turns a returned error and a panic alike into
// ErrUnavailable, so a defective Decider is refused rather than taken for a
// permission or a decline.
func (p *product) callDecider(ctx context.Context, d authn.Decision) (allow bool, err error) {
	defer func() {
		if rec := recover(); rec != nil {
			allow, err = false, fmt.Errorf("%w: the Decider panicked", authn.ErrUnavailable)
		}
	}()
	allow, err = p.decider.Decide(ctx, d)
	if err != nil {
		return false, fmt.Errorf("%w: the Decider could not answer", authn.ErrUnavailable)
	}
	return allow, nil
}

// auditRequired writes the mandatory audit row of a write, outside any
// transaction and before the action. A row that does not land is answered 503
// and the action is not carried out.
func (p *product) auditRequired(c *gin.Context, e audit.Entry) bool {
	if err := p.audit.Record(c.Request.Context(), e); err != nil {
		p.logger.Error(ModuleName+": the mandatory audit row could not be written, so the action was not carried out",
			slog.String("action", e.Action), slog.String("cause", err.Error()))
		mount.Unavailable(c)
		return false
	}
	return true
}

// auditBestEffort writes an audit row that does not block the action. A failure
// is an Error line and changes nothing about the answer.
func (p *product) auditBestEffort(ctx context.Context, e audit.Entry) {
	if err := p.audit.Record(ctx, e); err != nil {
		p.logger.Error(ModuleName+": an audit row could not be written",
			slog.String("action", e.Action), slog.String("cause", err.Error()))
	}
}

// cause logs a failure and answers 503, never telling the caller which
// dependency failed.
func (p *product) cause(c *gin.Context, op string, err error) {
	p.logger.Error(ModuleName+": "+op+" failed", slog.String("cause", err.Error()))
	mount.Unavailable(c)
}

// answer maps a failure the capability returned to the response it belongs to.
// The mapping is closed and fail-closed: an invitation that is not there is a
// 404, a malformed address or a bad argument is a 400, revoking an accepted
// invitation is a 409, and any other failure is a 503 whose cause stays in the
// log.
func (p *product) answer(c *gin.Context, op string, err error) {
	switch {
	case errors.Is(err, ErrInvitationNotFound):
		p.notFound(c)
	case errors.Is(err, ErrInvalidEmail), errors.Is(err, authn.ErrInvalidArgument),
		errors.Is(err, authn.ErrInvalidIdentifier):
		p.bad(c)
	case errors.Is(err, ErrInvitationAlreadyAccepted):
		mount.Fail(c, mount.CodeConflict)
	default:
		p.cause(c, op, err)
	}
}

// bad answers a request this module refuses on shape alone: a path parameter or
// a body it cannot read.
func (p *product) bad(c *gin.Context) { mount.Fail(c, mount.CodeBadRequest) }

// notFound answers a route whose object is not there: a bare 404, carrying no
// problem code of this capability.
func (p *product) notFound(c *gin.Context) {
	c.Status(http.StatusNotFound)
	c.Writer.WriteHeaderNow()
}

// noContent answers a write that landed: 204 with no body.
func (p *product) noContent(c *gin.Context) {
	c.Status(http.StatusNoContent)
	c.Writer.WriteHeaderNow()
}
