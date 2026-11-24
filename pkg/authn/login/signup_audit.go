package login

import (
	"errors"
	"log/slog"

	"github.com/vislake/speed/pkg/audit"
	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/authn/internal/identity"
)

const (
	actionSignup = "authn.signup"
	signupMethod = "password"
)

// auditSignup writes method=password as the only detail; no address, username, code or identifier_mac is written.
func (r *routes) auditSignup(c *call, account authn.AccountID, outcome string) {
	if r.audit == nil {
		return
	}
	base := c.ctx
	ctx := base
	if account != "" {
		id := identity.New(authn.Principal{Account: account, AuthTime: r.clock.Now()}, "", "")
		ctx = identity.With(base, id)
	}
	err := r.audit.Record(ctx, audit.Entry{
		Action:  actionSignup,
		Object:  audit.AccountObject(string(account)),
		Outcome: outcome,
		Detail:  map[string]string{"method": signupMethod},
	})
	if err != nil {
		r.meters.auditFailed(base)
		r.logger.Error(ModuleName+": a signup audit row could not be written",
			slog.String("cause", err.Error()))
	}
}

func signupAuditOutcome(err error) (string, bool) {
	var throttled *authn.ThrottledError
	switch {
	case errors.As(err, &throttled), errors.Is(err, authn.ErrThrottled):
		return outcomeThrottled, true
	case errors.Is(err, authn.ErrSignupClosed), errors.Is(err, authn.ErrContactTaken),
		errors.Is(err, authn.ErrAccountExists), errors.Is(err, authn.ErrPasswordPolicy),
		errors.Is(err, authn.ErrInvalidIdentifier), errors.Is(err, authn.ErrUsernameReserved),
		errors.Is(err, authn.ErrInvalidArgument), errors.Is(err, authn.ErrInvalidAccountID):
		return outcomeRefused, true
	}
	return "", false
}
