package authn

import (
	"context"
	"errors"
	"time"
)

// ThrottleScope names the kind of thing a failure counter is kept for. The set
// is closed: counters under different scopes never share a key space.
type ThrottleScope string

// The throttle scopes.
const (
	ScopeIdentifier ThrottleScope = "identifier" // a login identifier, normalized
	ScopeAccount    ThrottleScope = "account"    // an account
	ScopeFactor     ThrottleScope = "factor"     // a second factor
	ScopePhone      ThrottleScope = "phone"      // a phone number
	ScopeClient     ThrottleScope = "client"     // an API client
	// ScopeSignupUsername counts anonymous registrations under the digest of a
	// normalized username, so that a name being taken and a name being free
	// reach the same threshold on the same key.
	ScopeSignupUsername ThrottleScope = "signup-username"
)

// ErrThrottled reports that an attempt was refused because earlier failures put
// the key into back-off. The error that carries it is a *ThrottledError, and
// errors.As reads RetryAfter out of it.
var ErrThrottled = errors.New("authn: throttled")

// Throttle is the read and write surface of failure back-off.
//
// An implementation that cannot read its state has to say so with an error and
// not with a zero retryAfter: a back-off that cannot be read must not let the
// check pass, so a caller that gets an error from Check does not verify.
type Throttle interface {
	// Check reports how long the key still has to wait: zero means it may
	// try now.
	Check(ctx context.Context, scope ThrottleScope, key string) (retryAfter time.Duration, err error)
	// Fail records one failed attempt, and engaged reports that this
	// failure is the one that started a back-off.
	Fail(ctx context.Context, scope ThrottleScope, key string) (engaged bool, err error)
	// Succeed clears the key's failures.
	Succeed(ctx context.Context, scope ThrottleScope, key string) error
}

// ThrottledError is what a throttled attempt returns. It reads as ErrThrottled
// through errors.Is and carries how long the caller is to wait.
type ThrottledError struct {
	// RetryAfter is how long until the key may try again. A caller answering
	// over HTTP sends it as Retry-After when it is positive.
	RetryAfter time.Duration
}

// Error is the fixed text authn: throttled. It names no key and no scope, so
// the error can reach a log without echoing what was being attempted.
func (e *ThrottledError) Error() string { return "authn: throttled" }

// Is reports whether target is ErrThrottled.
func (e *ThrottledError) Is(target error) bool { return target == ErrThrottled }
