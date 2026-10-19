package selfservice

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/vislake/speed/pkg/audit"
	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/authn/internal/mount"
	"github.com/vislake/speed/pkg/event"
	"github.com/vislake/speed/pkg/log"
)

// moduleName starts every log line of this package.
const moduleName = "authn.login"

// The audit actions this package writes, the best-effort ones of the action
// table, and the outcomes they carry.
const (
	actionPasswordChange = "authn.password.change"
	actionBindingUnlink  = "authn.binding.unlink"

	outcomeSuccess = "success"
	outcomeDenied  = "denied"
)

// caller is the account a request that changes how it is proven acts for, and
// the session it came with.
type caller struct {
	account authn.AccountID
	session authn.SessionID
}

// callerKey carries the caller a body precheck already decided to the handler,
// so the identity, session and freshness checks run once per request.
type callerKey struct{}

// withCaller returns ctx carrying the caller a precheck decided.
func withCaller(ctx context.Context, who caller) context.Context {
	return context.WithValue(ctx, callerKey{}, who)
}

// callerFrom returns the caller a precheck decided, or false when there is none.
func callerFrom(ctx context.Context) (caller, bool) {
	who, ok := ctx.Value(callerKey{}).(caller)
	return who, ok
}

// authorize is what every route that changes how the account is proven does
// before it reads the request body or calls anything. It answers the request and
// returns false when the identity may not go on:
//
//   - an impersonating identity is refused with 403 impersonation-restricted,
//     before any call is made;
//   - an identity with no login session has nothing to revoke the others
//     against and is refused with the plain 401;
//   - an identity that does not meet the freshness the account's factors call
//     for is refused with the step-up 401. A requirement that cannot be decided
//     because the factors cannot be read is a 503.
//
// action names the audit action of a refusal for a step-up, empty for a route
// whose refusals leave no row.
func (r *routes) authorize(c *call, action string) (caller, bool) {
	ctx := c.ctx
	if who, carried := callerFrom(ctx); carried {
		return who, true
	}
	id, ok := authn.FromContext(ctx)
	if !ok || id.Account() == "" {
		c.unauthorized("")
		return caller{}, false
	}
	if id.Impersonating() {
		c.forbidden(mount.CodeImpersonationRestricted)
		return caller{}, false
	}
	if id.Session() == "" {
		c.unauthorized("")
		return caller{}, false
	}
	req, err := r.requirement(ctx, id.Account())
	if err != nil {
		r.fail(c, err)
		return caller{}, false
	}
	if !id.Satisfies(req, r.deps.Clock.Now()) {
		if action != "" {
			r.record(ctx, action, outcomeDenied, id.Account(), nil)
		}
		c.stepUp(id, req)
		return caller{}, false
	}
	return caller{account: id.Account(), session: id.Session()}, true
}

// requirement is the freshness a change of the account's credentials asks for.
// An account with an active factor is held to a fresh local multi-factor
// authentication, so that a stolen single-factor session, or an assurance only
// an IdP declared, cannot change what proves the account. An account with no
// active factor is held to a fresh first factor. A factor still pending or
// disabled is not a factor the account has.
func (r *routes) requirement(ctx context.Context, account authn.AccountID) (authn.Requirement, error) {
	factors, err := r.deps.MFA.Factors(ctx, account)
	if err != nil {
		return authn.Requirement{}, err
	}
	for _, f := range factors {
		if f.Status == authn.FactorActive {
			return authn.FreshLocalMulti(r.durations.get(ctx, keyRecentWindow, defaultRecentWindow, minRecentWindow, maxRecentWindow)), nil
		}
	}
	return authn.FreshFirstFactor(), nil
}

// settle is how a change that alters what proves the account ends: the current
// session gets a new token, every other session of the account is revoked, and
// a new token is written to the response last. Both calls are made whatever the
// first one answered, because revoking the others is the half that protects the
// account; the cookie is written whenever the token was rotated, because the old
// one no longer holds. It answers the request and returns false when either
// call failed, the change itself being in force.
func (r *routes) settle(c *call, who caller, reason string) bool {
	ctx := c.ctx
	issued, rotateErr := r.deps.Sessions.Rotate(ctx, who.session)
	_, revokeErr := r.deps.Sessions.RevokeAccount(ctx, who.account, who.session, reason)
	if rotateErr == nil && issued.Token != "" {
		r.deps.Sessions.SetCookie(c.w, issued)
	}
	if err := errors.Join(rotateErr, revokeErr); err != nil {
		r.fail(c, err)
		return false
	}
	return true
}

// publish hands an event to the bus when there is one. A failure to publish is
// a Warn and never fails the request: an event is a hint.
func (r *routes) publish(ctx context.Context, eventType string, payload any) {
	if r.deps.Bus == nil {
		return
	}
	if err := r.deps.Bus.Publish(ctx, event.Event{Type: eventType, Payload: payload}); err != nil {
		log.FromContext(ctx).Warn(moduleName+": an event could not be published",
			slog.String("event_type", eventType), slog.String("cause", err.Error()))
	}
}

// record writes the best-effort audit row of a self-service change. A failure
// to write is an Error and never fails the request.
func (r *routes) record(ctx context.Context, action, outcome string, account authn.AccountID, detail map[string]string) {
	if r.deps.Audit == nil {
		return
	}
	err := r.deps.Audit.Record(ctx, audit.Entry{
		Action:  action,
		Object:  string(account),
		Outcome: outcome,
		Detail:  detail,
	})
	if err != nil {
		logError(ctx, "an audit row of an account self-service change could not be written", err)
	}
}

// engaged tells the bus, and the module that counts engagements, that a failed
// attempt started a back-off.
func (r *routes) engaged(ctx context.Context, scope authn.ThrottleScope) {
	r.publish(ctx, authn.EventThrottleEngaged, authn.ThrottleEngaged{Scope: string(scope)})
	if r.deps.ThrottleEngaged != nil {
		r.deps.ThrottleEngaged(ctx, scope)
	}
}

func logError(ctx context.Context, msg string, err error) {
	log.FromContext(ctx).Error(moduleName+": "+msg, slog.String("cause", err.Error()))
}

// seconds is d in whole seconds, rounded up.
func seconds(d time.Duration) int64 {
	return int64((d + time.Second - 1) / time.Second)
}
