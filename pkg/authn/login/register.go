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

const (
	stateRegistered     = "registered"
	verificationPending = "pending"
)

const (
	signupSourcePrefix = "authn:signup:ip:"
	signupGlobalPrefix = "authn:signup:global:"
)

const (
	signupSourceBucket = time.Minute
	signupGlobalBucket = time.Hour
	signupMinuteLayout = "200601021504"
	signupHourLayout   = "2006010215"
)

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

// signupSourceAllow skips a counter it cannot write with a Warn: it is a coarse pre-throttle, and the authoritative back-off is in the database.
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

// signupGlobalAllow refuses when its counter cannot be written: it is the only bound on an open registration.
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

func untilNextBucket(now time.Time, bucket time.Duration) time.Duration {
	next := now.Truncate(bucket).Add(bucket)
	wait := next.Sub(now)
	if wait < time.Second {
		return time.Second
	}
	return wait
}

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
