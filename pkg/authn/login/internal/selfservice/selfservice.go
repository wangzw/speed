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
	"fmt"
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
	// deployment offers no channel. With it, KV is required for the pending
	// record and Signup for the unproved reservation another account may hold.
	EmailSender authn.EmailCodeSender

	// Signup reads the reservation of a contact a self-registration wrote. The
	// email branch asks it before sending a code, so an address another account
	// reserved is answered as taken instead of being silently left unsent.
	Signup authn.SignupVerification

	// Human is the deployment's human-verification gate. It is optional: without
	// it, or with a gate that requires nothing, the routes here send without
	// asking for a proof. With a gate that requires one, every send first presents
	// a valid, single-use proof, checked before any budget is spent.
	Human authn.HumanVerifier

	// Settings is read for authn.mfa.recent-window, for the two authn.sms
	// durations the phone routes answer with, and for the authn.login email-code
	// durations and budgets the email route answers with and counts against.
	// Without it, or when a read fails or lands outside the setting's domain, the
	// declared default is used.
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
	budgets   *counts
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
		need("Signup", d.Signup == nil)
	}
	if len(missing) > 0 {
		return nil, fmt.Errorf("authn.login: the account self-service routes need %s", strings.Join(missing, ", "))
	}
	return &Service{deps: d, durations: newDurations(d.Settings), budgets: newCounts(d.Settings)}, nil
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

// Mount registers the routes. rel turns a declared route into the relative path
// the mounter joins to its base; the login engine passes its own resolver, so
// the fragment's paths and the mounting read one declaration. The two phone
// routes are registered only when the service has SMSCodes; post, when it is not
// nil, is called in that same judgement with the feature name the surface
// publishes. The email branch is switched on by EmailSender, and its feature is
// posted in the judgement that reads that dependency. The engine passes its
// report function here and calls Mount from its Init, where the feature board
// accepts a post.
//
// Every route is guarded: the gateway authenticates it, and a request without a
// credential never reaches a handler.
func (s *Service) Mount(m mount.Mounter, rel func(string) string, post func(name string)) {
	r := &routes{Service: s, endpoint: m.Endpoint()}
	mount.Declared(m, passwordOperation, rel(pathPassword),
		withWriter(r.guardedBody(actionPasswordChange, passwordOperation.Handle(r.changePassword))))
	if s.deps.SMSCodes != nil {
		mount.Declared(m, startPhoneOperation, rel(pathPhone),
			withWriter(r.guardedBody("", startPhoneOperation.Handle(r.startPhone))))
		mount.Declared(m, verifyPhoneOperation, rel(pathPhoneVerify),
			withWriter(r.guardedBody("", verifyPhoneOperation.Handle(r.verifyPhone))))
		postFeature(post, FeaturePhone)
	}
	if s.deps.EmailSender != nil {
		postFeature(post, FeatureEmail)
	}
	mount.Declared(m, addIdentifierOperation, rel(pathIdentifiers),
		withWriter(r.guardedBody("", addIdentifierOperation.Handle(r.addIdentifier))))
	mount.Declared(m, verifyIdentifierOperation, rel(pathIdentifiersVerify),
		withWriter(r.guardedBody("", verifyIdentifierOperation.Handle(r.verifyIdentifier))))
	mount.Declared(m, removeIdentifierOperation, rel(pathIdentifier),
		withWriter(removeIdentifierOperation.Handle(r.removeIdentifier)))
	mount.Declared(m, unlinkOperation, rel(pathLink), withWriter(unlinkOperation.Handle(r.unlink)))
}

// guardedBody applies the capability's body rules before the entry point binds
// a write route's body: the media type, then the identity, session and
// freshness checks the handler makes first, then the 64 KiB cap. The caller the
// check decided is carried to the handler, so the checks run once.
func (r *routes) guardedBody(action string, next gin.HandlerFunc) gin.HandlerFunc {
	return mount.GuardBody(func(c *gin.Context) bool {
		call := r.call(c.Request.Context(), nil)
		who, ok := r.authorize(call, action)
		if !ok {
			call.refuse(c)
			return false
		}
		c.Request = c.Request.WithContext(withCaller(c.Request.Context(), who))
		return true
	}, next)
}

// postFeature reports a feature name when the engine handed Mount a reporting
// function. A nil function is a caller that publishes no feature document.
func postFeature(post func(name string), name string) {
	if post != nil {
		post(name)
	}
}
