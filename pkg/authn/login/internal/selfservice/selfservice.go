// Package selfservice is the account self-service routes of the login module: password, phone, identifier and external-link changes on the caller's own account.
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
	// Accounts reads and writes the account store. Required.
	Accounts authn.Accounts
	// Admin writes the account facts a self-service change touches. Required.
	Admin authn.AccountAdmin
	// Passwords sets the new password under the account store's policy. Required.
	Passwords authn.Passwords
	// Sessions rotates and revokes the account's sessions. Required.
	Sessions authn.Sessions
	// MFA lists the account's factors, for the freshness check. Required.
	MFA authn.MFA

	// SMSCodes switches the two phone routes on: without it they are not
	// registered. With it, KV and Throttle are required as well.
	SMSCodes authn.SMSCodes
	// KV holds the pending phone and email verification records; required with
	// SMSCodes and EmailSender.
	KV kv.KV
	// Throttle counts the SMS code attempts against a phone number; required
	// with SMSCodes.
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

	// Human is the deployment's human-verification gate. The login engine
	// requires it, so a service the engine builds always carries one; the gate
	// itself may be off, and then the email branch sends without asking for a
	// proof. With a gate that requires one, the email branch presents a valid,
	// single-use proof, checked before any budget is spent. The phone routes do
	// not use it: their proof travels to the code engine and is checked once
	// there.
	Human authn.HumanVerifier

	// Settings is read for authn.mfa.recent-window, for the two authn.sms
	// durations the phone routes answer with, and for the authn.login email-code
	// durations and budgets the email route answers with and counts against.
	// Without it, or when a read fails or lands outside the setting's domain, the
	// declared default is used.
	Settings settings.Store
	// Bus and Audit are optional: without them the event and the audit row are
	// not written.
	Bus event.Bus
	// Audit writes the change's audit row; optional.
	Audit audit.Audit
	// ThrottleEngaged is called after the event of a back-off that a failed phone
	// code started, so that the module that owns the engagement counter can
	// count it.
	ThrottleEngaged func(ctx context.Context, scope authn.ThrottleScope)
	// Clock reads the time freshness is judged against. The zero value is the
	// system clock.
	Clock clock.Source
}

// Service holds what the routes need; its Mount registers them.
type Service struct {
	deps      Deps
	durations *durations
	budgets   *counts
}

// New checks that the dependencies the routes cannot do without are there and returns the service. A missing one is an assembly defect.
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

type routes struct {
	*Service
	endpoint string
}

const (
	// FeaturePhone is the phone self-service surface: POST /phone and
	// POST /phone/verify, present exactly when SMSCodes is.
	FeaturePhone = "authn.phone"
	// FeatureEmail is the email branch of POST /identifiers, present exactly
	// when EmailSender is. The route itself is mounted either way; the name
	// describes the branch.
	FeatureEmail = "authn.identifiers.email"
)

// Mount registers the routes. rel turns a declared route into the relative path the mounter joins to its base.
func (s *Service) Mount(m mount.Mounter, rel func(string) string, post func(name string)) {
	r := &routes{Service: s, endpoint: m.Endpoint()}
	mount.Declared(m, passwordOperation, rel(pathPassword),
		withWriter(r.guardedBody(actionPasswordChange, passwordOperation.Handle(r.changePassword))), mount.Impersonable())
	if s.deps.SMSCodes != nil {
		mount.Declared(m, startPhoneOperation, rel(pathPhone),
			withWriter(r.guardedIdentity(startPhoneOperation.Handle(r.startPhone))), mount.Impersonable())
		mount.Declared(m, verifyPhoneOperation, rel(pathPhoneVerify),
			withWriter(r.guardedIdentity(verifyPhoneOperation.Handle(r.verifyPhone))), mount.Impersonable())
		postFeature(post, FeaturePhone)
	}
	if s.deps.EmailSender != nil {
		postFeature(post, FeatureEmail)
	}
	mount.Declared(m, addIdentifierOperation, rel(pathIdentifiers),
		withWriter(r.guardedIdentity(addIdentifierOperation.Handle(r.addIdentifier))), mount.Impersonable())
	mount.Declared(m, verifyIdentifierOperation, rel(pathIdentifiersVerify),
		withWriter(r.guardedIdentity(verifyIdentifierOperation.Handle(r.verifyIdentifier))), mount.Impersonable())
	mount.Declared(m, updateIdentifierOperation, rel(pathIdentifier),
		withWriter(r.guardedIdentity(updateIdentifierOperation.Handle(r.updateIdentifier))), mount.Impersonable())
	mount.Declared(m, removeIdentifierOperation, rel(pathIdentifier),
		withWriter(removeIdentifierOperation.Handle(r.removeIdentifier)), mount.Impersonable())
	mount.Declared(m, unlinkOperation, rel(pathLink), withWriter(unlinkOperation.Handle(r.unlink)), mount.Impersonable())
}

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

func (r *routes) guardedIdentity(next gin.HandlerFunc) gin.HandlerFunc {
	return mount.GuardBody(func(c *gin.Context) bool {
		call := r.call(c.Request.Context(), nil)
		id, ok := r.identify(call)
		if !ok {
			call.refuse(c)
			return false
		}
		c.Request = c.Request.WithContext(withCaller(c.Request.Context(),
			caller{account: id.Account(), session: id.Session()}))
		return true
	}, next)
}

func postFeature(post func(name string), name string) {
	if post != nil {
		post(name)
	}
}
