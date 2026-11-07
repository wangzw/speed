package login

import (
	"errors"
	"log/slog"

	"github.com/vislake/speed/pkg/audit"
	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/authn/internal/identity"
)

// The audit action of an anonymous registration and the one detail its rows
// carry, from design-authn-events.md's action table and detail paragraph.
const (
	actionSignup = "authn.signup"
	signupMethod = "password"
)

// auditSignup writes the best-effort audit row of one registration attempt.
//
// The attempt that opened an account carries it as the object, and the row is
// written under an identity derived for this one call, the way a login's row is:
// the register route is public, so the request's own context carries no identity
// and the actor would otherwise be empty. The derived context reaches the one
// Record and is handed to nothing else, so it does not flow back into the
// request chain. A refused or throttled attempt carries no object and no actor.
//
// Every row's only detail is method=password. The address, the username and any
// verification code are never written; this build writes no identifier_mac
// either, since the design's "only" sentence makes it the complete detail set.
//
// The row is best-effort: a failure is an Error, a count on
// authn.audit.write-failures with mandatory=false, and nothing else. The
// registration's own answer is unchanged.
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

// signupAuditOutcome is the audit outcome of a registration's failure, and
// whether the design's closed set names one for it. A back-off is throttled. A
// refusal the server could decide — the switch, the name, the address, the
// password policy or an argument outside its domain — is refused. An outage or
// a capacity failure is neither: the action never reached a decision, the closed
// set has no value for it, and it leaves no row.
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
