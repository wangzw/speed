package login

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/authn/internal/mount"
	speedhttp "github.com/vislake/speed/pkg/http"
)

// The state names a registration answers with.
const (
	stateRegistered     = "registered"
	verificationPending = "pending"
)

// The prefixes of the two budget counters a registration carries, one bucket per
// source address per minute and one for the whole deployment per hour. The kv has
// no namespace of its own, so each key carries the authn: prefix.
const (
	signupSourcePrefix = "authn:signup:ip:"
	signupGlobalPrefix = "authn:signup:global:"
)

// The bucket widths and key layouts of the two budgets.
const (
	signupSourceBucket = time.Minute
	signupGlobalBucket = time.Hour
	signupMinuteLayout = "200601021504"
	signupHourLayout   = "2006010215"
)

// register opens an account for anonymous self-registration. It is a public
// route: the mounter applies the cross-origin check before the handler, and the
// handler reads no cookie and keeps no flow record, so there is no binding and no
// sequence number. The account module's Signups.Register does the whole write in
// one transaction — the enabled account, the username identifier written through
// self, the password credential and the email contact awaiting verification —
// and sends no email and makes no session.
//
// The order is the design's: the registration switch is read here only as a
// pre-check, so that a closed deployment refuses before any count or derivation;
// the two budgets then bound the route; and only then is Signups.Register called,
// which reads the same switch again as the authority and holds the username
// back-off before it checks uniqueness.
func (r *routes) register(ctx context.Context, in registerInput, meta *speedhttp.Meta) (speedhttp.Reply, error) {
	c := r.call(ctx, meta)
	if !r.passwordSignupEnabled() {
		r.auditSignup(c, "", outcomeRefused)
		c.forbidden(mount.CodeSignupClosed)
		return c.result()
	}
	if !r.signupSourceAllow(c) || !r.signupGlobalAllow(c) {
		return c.result()
	}
	if in.Username == "" || in.Email == "" || in.Password == "" {
		r.auditSignup(c, "", outcomeRefused)
		c.fail(mount.CodeBadRequest)
		return c.result()
	}
	acct, err := r.signups.Register(ctx, authn.RegisterAccount{
		Username:    in.Username,
		Email:       in.Email,
		Password:    in.Password,
		DisplayName: in.DisplayName,
		Locale:      in.Locale,
		TimeZone:    in.TimeZone,
	})
	if err != nil {
		r.failRegister(c, err)
		return c.result()
	}
	r.auditSignup(c, acct.ID, outcomeSuccess)
	c.json(http.StatusCreated, registrationReply{
		State:     stateRegistered,
		AccountID: string(acct.ID),
		Verification: verificationView{
			State: verificationPending,
			Email: in.Email,
		},
	})
	return c.result()
}

// signupSourceAllow applies the per-source registration budget. Zero turns it
// off. A counter that cannot be written is skipped with a Warn, which is the
// direction the source-address pre-throttle of a login takes: the layer is only a
// coarse pre-throttle before the authoritative failure back-off in the database,
// and the relaxation's upper bound is the free attempts of that back-off.
func (r *routes) signupSourceAllow(c *call) bool {
	limit := r.settings.signupIPLimit()
	if limit <= 0 {
		return true
	}
	now := r.clock.Now()
	key := signupSourcePrefix + c.clientIP + ":" + now.UTC().Format(signupMinuteLayout)
	count, err := r.kv.IncrByFloatWithTTL(c.ctx, key, 1, signupSourceBucket)
	if err != nil {
		r.logger.Warn(ModuleName+": the per-source budget of a registration could not be counted, so it is skipped",
			slog.String("cause", err.Error()))
		return true
	}
	if count > float64(limit) {
		r.auditSignup(c, "", outcomeThrottled)
		c.throttled(untilNextBucket(now, signupSourceBucket))
		return false
	}
	return true
}

// signupGlobalAllow applies the deployment-wide hourly registration budget. A
// counter that cannot be written is a refusal, not a pass: the budget is the only
// bound on a deployment whose registration is open, and the failure document's
// direction for a dependency that cannot answer is 503.
func (r *routes) signupGlobalAllow(c *call) bool {
	limit := r.settings.signupGlobalHourly()
	now := r.clock.Now()
	key := signupGlobalPrefix + now.UTC().Format(signupHourLayout)
	count, err := r.kv.IncrByFloatWithTTL(c.ctx, key, 1, signupGlobalBucket)
	if err != nil {
		r.logger.Warn(ModuleName+": the deployment-wide budget of a registration could not be counted, so the "+
			"registration is refused", slog.String("cause", err.Error()))
		c.unavailable()
		return false
	}
	if count > float64(limit) {
		r.auditSignup(c, "", outcomeThrottled)
		c.throttled(untilNextBucket(now, signupGlobalBucket))
		return false
	}
	return true
}

// untilNextBucket is how long the bucket a count fell into has left: the delay a
// 429 of a budget states, never below one second so that a client does not retry
// in the same bucket.
func untilNextBucket(now time.Time, bucket time.Duration) time.Duration {
	next := now.Truncate(bucket).Add(bucket)
	wait := next.Sub(now)
	if wait < time.Second {
		return time.Second
	}
	return wait
}

// failRegister maps a registration's failure to the answer the design gives it.
// The register route's two 409s are username-taken for a login name and
// email-taken for an address another account reserved or holds, not the conflict
// the other routes answer with; the signup-username back-off and the two budgets
// are the one 429; and a database that cannot answer, or a derivation capacity
// that is full, are the 503s.
func (r *routes) failRegister(c *call, err error) {
	if outcome, named := signupAuditOutcome(err); named {
		r.auditSignup(c, "", outcome)
	}
	var throttled *authn.ThrottledError
	switch {
	case errors.As(err, &throttled):
		c.throttled(throttled.RetryAfter)
	case errors.Is(err, authn.ErrThrottled):
		c.throttled(0)
	case errors.Is(err, authn.ErrSignupClosed):
		c.forbidden(mount.CodeSignupClosed)
	case errors.Is(err, authn.ErrContactTaken):
		c.fail(mount.CodeEmailTaken)
	case errors.Is(err, authn.ErrAccountExists):
		c.fail(mount.CodeUsernameTaken)
	case errors.Is(err, authn.ErrBusy):
		c.busy()
	case authn.IsUsernameShape(err):
		c.fail(mount.CodeUsernameInvalid)
	case errors.Is(err, authn.ErrPasswordPolicy), errors.Is(err, authn.ErrInvalidIdentifier),
		errors.Is(err, authn.ErrInvalidArgument),
		errors.Is(err, authn.ErrInvalidAccountID):
		c.fail(mount.CodeBadRequest)
	case errors.Is(err, authn.ErrUsernameReserved):
		c.fail(mount.CodeUsernameReserved)
	case errors.Is(err, authn.ErrUnavailable):
		r.logger.Warn(ModuleName+": a dependency of the register route is unavailable",
			slog.String("cause", err.Error()))
		c.unavailable()
	default:
		r.logger.Error(ModuleName+": the register route failed on a call it cannot answer for",
			slog.String("cause", err.Error()))
		c.unavailable()
	}
}
