// Package selfservice is the account self-service routes of the login module:
// the change of the account's password, the addition or replacement of its
// phone, the removal of one of its identifiers and the unlinking of an external
// identity. The account's own profile reads and writes are the profile module's
// routes, not these.
//
// Every route runs on the identity the gateway authenticated. A route that
// changes how the account is proven asks for a fresh authentication first (a
// local multi-factor one when the account has an active factor, a first factor
// otherwise), goes through the Mounter's cross-origin check, and is refused
// whenever the identity is an impersonation: the gateway already answers 401
// for such a request because none of these routes is registered as
// impersonable, and the handler answers 403 again as the fallback for a
// registration that was made wrongly.
//
// The package depends on the root package's interfaces, on the route
// components in internal/mount and on kv.KV. It knows nothing of the login
// engine, which builds a Service in its Init and calls Mount.
package selfservice

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/vislake/speed/pkg/audit"
	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/authn/internal/clock"
	"github.com/vislake/speed/pkg/authn/internal/mount"
	"github.com/vislake/speed/pkg/event"
	"github.com/vislake/speed/pkg/kv"
	"github.com/vislake/speed/pkg/settings"
)

// Deps is what a Service is built from.
type Deps struct {
	// Accounts, Admin, Passwords, Sessions and MFA are required.
	Accounts  authn.Accounts
	Admin     authn.AccountAdmin
	Passwords authn.Passwords
	Sessions  authn.Sessions
	MFA       authn.MFA

	// SMSCodes switches the two phone routes on: without it they are not
	// registered. With it, KV and Throttle are required as well.
	SMSCodes authn.SMSCodes
	KV       kv.KV
	Throttle authn.Throttle

	// EmailSender is the channel a self-service email verification code goes out
	// over. It switches the email branch of POST /identifiers on: without it an
	// email address cannot be added self-service, and the branch answers that the
	// deployment offers no channel. With it, KV is required as well, for the
	// pending record.
	EmailSender authn.EmailCodeSender

	// Settings is read for authn.mfa.recent-window, for the two authn.sms
	// durations the phone routes answer with, and for the two authn.login
	// email-code durations the email route answers with. Without it, or when a
	// read fails or lands outside the setting's domain, the declared default is
	// used.
	Settings settings.Store
	// Bus and Audit are optional: without them the event and the audit row are
	// not written.
	Bus   event.Bus
	Audit audit.Audit
	// ThrottleEngaged is called after the event of a back-off that a failed phone
	// code started, so that the module that owns the engagement counter can
	// count it.
	ThrottleEngaged func(ctx context.Context, scope authn.ThrottleScope)
	// Clock reads the time freshness is judged against. The zero value is the
	// system clock.
	Clock clock.Source
}

// Service holds what the routes need. Its Mount registers them.
type Service struct {
	deps      Deps
	durations *durations
}

// New checks that the dependencies the routes cannot do without are there and
// returns the service. A missing one is a defect of the assembly, and the error
// names it.
func New(d Deps) (*Service, error) {
	var missing []string
	need := func(name string, absent bool) {
		if absent {
			missing = append(missing, name)
		}
	}
	need("Accounts", d.Accounts == nil)
	need("Admin", d.Admin == nil)
	need("Passwords", d.Passwords == nil)
	need("Sessions", d.Sessions == nil)
	need("MFA", d.MFA == nil)
	if d.SMSCodes != nil {
		need("KV", d.KV == nil)
		need("Throttle", d.Throttle == nil)
	}
	if d.EmailSender != nil {
		need("KV", d.KV == nil)
	}
	if len(missing) > 0 {
		return nil, fmt.Errorf("authn.login: the account self-service routes need %s", strings.Join(missing, ", "))
	}
	return &Service{deps: d, durations: newDurations(d.Settings)}, nil
}

// routes is the Service bound to the endpoint its routes were mounted on, whose
// name is the realm of the 401 a handler writes.
type routes struct {
	*Service
	endpoint string
}

// The conditional features the account self-service routes publish. A name is
// posted by the engine under its own module name; this package owns the
// judgement that decides whether the surface the name describes is present, so
// the name constants live beside it.
const (
	// FeaturePhone is the phone self-service surface: POST /phone and
	// POST /phone/verify, present exactly when SMSCodes is.
	FeaturePhone = "authn.phone"
	// FeatureEmail is the email branch of POST /identifiers, present exactly
	// when EmailSender is. The route itself is mounted either way; the name
	// describes the branch.
	FeatureEmail = "authn.identifiers.email"
)

// Mount registers the routes. The two phone routes are registered only when the
// service has SMSCodes; post, when it is not nil, is called in that same
// judgement with the feature name the surface publishes. The email branch is
// switched on by EmailSender, and its feature is posted in the judgement that
// reads that dependency. The engine passes its report function here and calls
// Mount from its Init, where the feature board accepts a post.
//
// Every route is guarded: the gateway authenticates it, and a request without a
// credential never reaches a handler.
func (s *Service) Mount(m mount.Mounter, post func(name string)) {
	r := &routes{Service: s, endpoint: m.Endpoint()}
	m.Guarded(http.MethodPut, "/password", r.changePassword)
	if s.deps.SMSCodes != nil {
		m.Guarded(http.MethodPost, "/phone", r.startPhone)
		m.Guarded(http.MethodPost, "/phone/verify", r.verifyPhone)
		postFeature(post, FeaturePhone)
	}
	if s.deps.EmailSender != nil {
		postFeature(post, FeatureEmail)
	}
	m.Guarded(http.MethodPost, "/identifiers", r.addIdentifier)
	m.Guarded(http.MethodPost, "/identifiers/verify", r.verifyIdentifier)
	m.Guarded(http.MethodDelete, "/identifiers/:id", r.removeIdentifier)
	m.Guarded(http.MethodDelete, "/links/:id", r.unlink)
}

// postFeature reports a feature name when the engine handed Mount a reporting
// function. A nil function is a caller that publishes no feature document.
func postFeature(post func(name string), name string) {
	if post != nil {
		post(name)
	}
}

// The sentinels a handler reads out of what it called, and the answer of each.
// A wrong current password is the one place a 401 answers a request that is
// authenticated; the table of the account module gives it as login-failed.
func (r *routes) fail(c *gin.Context, err error) {
	ctx := c.Request.Context()
	var throttled *authn.ThrottledError
	switch {
	case errors.As(err, &throttled):
		mount.Throttled(c, throttled.RetryAfter)
	case errors.Is(err, authn.ErrThrottled):
		mount.Throttled(c, 0)
	case errors.Is(err, authn.ErrPasswordPolicy), errors.Is(err, authn.ErrInvalidIdentifier),
		errors.Is(err, authn.ErrInvalidArgument):
		mount.Fail(c, mount.CodeBadRequest)
	case errors.Is(err, authn.ErrIdentifierRequired):
		mount.Fail(c, mount.CodeLoginIdentifierRequired)
	case errors.Is(err, authn.ErrAccountExists), errors.Is(err, authn.ErrBindingConflict),
		errors.Is(err, authn.ErrLastLoginMethod), errors.Is(err, authn.ErrLastFactor),
		errors.Is(err, authn.ErrFactorLimit):
		mount.Fail(c, mount.CodeConflict)
	case errors.Is(err, authn.ErrUnsupportedRegion):
		mount.Fail(c, mount.CodeUnsupportedRegion)
	case errors.Is(err, authn.ErrInvalidCredentials):
		mount.Unauthorized(c, r.endpoint, mount.CodeLoginFailed)
	case errors.Is(err, authn.ErrAccountNotFound), errors.Is(err, authn.ErrSessionNotFound),
		errors.Is(err, authn.ErrSessionRevoked):
		mount.Unauthorized(c, r.endpoint, "")
	case errors.Is(err, authn.ErrBusy):
		mount.Busy(c)
	case errors.Is(err, authn.ErrUnavailable):
		logWarn(ctx, "a dependency of an account self-service route is unavailable", err)
		mount.Unavailable(c)
	default:
		logError(ctx, "an account self-service route failed on a call it cannot answer for", err)
		mount.Unavailable(c)
	}
}
