package profile

import (
	"errors"
	"log/slog"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/authn/internal/mount"
)

// admitStrength holds the caller to the freshness a write of the profile asks
// for, answering the step-up 401 and returning false when it falls short.
//
// The requirement is the account's own: an account with an active factor is held
// to a fresh local multi-factor authentication, so that a stolen single-factor
// session, or an assurance only an IdP declared, cannot change the profile; an
// account with no active factor is held to a fresh first factor. A factor still
// pending or disabled is not a factor the account has. The factors being
// unreadable is a 503: a requirement that cannot be decided must not read as
// one that asks for nothing.
func (p *product) admitStrength(c *gin.Context, id authn.Identity) bool {
	factors, err := p.mfa.Factors(c.Request.Context(), id.Account())
	if err != nil {
		p.cause(c, "reading the account's factors", err)
		return false
	}
	requirement := authn.FreshFirstFactor()
	for _, f := range factors {
		if f.Status == authn.FactorActive {
			requirement = authn.FreshLocalMulti(p.settings.recentWindow())
			break
		}
	}
	if id.Satisfies(requirement, time.Now()) {
		return true
	}
	mount.StepUpRequired(c, id, requirement)
	return false
}

// readFailure maps a failure a read of the profile met. The identity's own
// account gone — a tombstone, or a row that cannot be read — is the caller's
// identity no longer answering for an account, which is the same 401 an absent
// identity gets; anything else is a 503 whose cause stays in the log.
func (p *product) readFailure(c *gin.Context, err error) {
	if errors.Is(err, authn.ErrAccountNotFound) {
		mount.Unauthorized(c, p.mounter.Endpoint(), "")
		return
	}
	p.cause(c, "reading the profile", err)
}

// writeFailure maps a failure a write of the profile met. The identity's own
// account gone is the 401 a read of it gets; a value the account module refuses
// on shape or on its attribute bounds is a 400; anything else is a 503 whose
// cause stays in the log.
func (p *product) writeFailure(c *gin.Context, err error) {
	switch {
	case errors.Is(err, authn.ErrAccountNotFound):
		mount.Unauthorized(c, p.mounter.Endpoint(), "")
	case errors.Is(err, authn.ErrInvalidArgument):
		mount.Fail(c, mount.CodeBadRequest)
	default:
		p.cause(c, "updating the profile", err)
	}
}

// cause logs a failure and answers 503, never telling the caller which dependency
// failed.
func (p *product) cause(c *gin.Context, op string, err error) {
	p.logger.Error(ModuleName+": "+op+" failed", slog.String("cause", err.Error()))
	mount.Unavailable(c)
}
