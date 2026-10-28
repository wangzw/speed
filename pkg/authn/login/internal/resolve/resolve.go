// Package resolve is the account resolution step of the login engine. A login
// method hands the engine a [authn.Proof]; this package says which account the
// proof belongs to, and for a first login it decides whether an account is
// created, an existing one is linked or merged into, or the login is refused.
// The rules are those of the account design (A1 to A10 and first-login
// provisioning), and the package holds nothing else: it writes no HTTP answer,
// builds no session and imports neither the login engine nor a login method
// nor the tenant module.
//
// The key of an external identity is always the provider and the subject, never
// an email address. The email a provider declares is a hint, and whether it is
// believed depends on where the identity came from: a social provider's claim
// is believed only when it says the address is verified, and then only for the
// linking rules that name it; an enterprise provider's claim is replaced by the
// proof that the address is in a domain its tenant verified.
//
// A resolution of an automatic merge (the automatic link of A4, the
// merge_existing merge of A5) leaves two obligations to the caller, in
// [Result.Cleanup] and in this order: revoke every session the account had, then
// delete its password credential, and only then create the session of this
// login. The resolver holds neither the session module nor the password store.
package resolve

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/vislake/speed/pkg/authn"
)

// Signup says whether a first login by a social provider or a phone creates an
// account. The zero value reads as closed.
type Signup string

// The values of Signup.
const (
	SignupClosed Signup = "closed"
	SignupOpen   Signup = "open"
)

// AutoLink says whether a social login with no binding may be linked to an
// existing account by a verified email. The zero value reads as never.
type AutoLink string

// The values of AutoLink.
const (
	AutoLinkNever         AutoLink = "never"
	AutoLinkVerifiedEmail AutoLink = "verified-email"
)

// FactorReader lists the factors an account has registered. The multi-factor
// module is one; the resolver asks only whether the list is empty, and only for
// the merge_existing rule.
type FactorReader interface {
	Factors(ctx context.Context, id authn.AccountID) ([]authn.FactorRecord, error)
}

// Config is what a [Resolver] is built from.
type Config struct {
	// Accounts, Admin and Memberships are required.
	Accounts    authn.Accounts
	Admin       authn.AccountAdmin
	Memberships authn.Memberships
	// Factors is what merge_existing asks whether an account has factors. It is
	// required for that rule and without it no account is merged into.
	Factors FactorReader
	// SSO is the register of enterprise providers. Without it an enterprise
	// identity cannot be resolved without a binding, and a social identity has no
	// SSO domain to be refused for.
	SSO authn.SSOProviders
	// Provisioner is the host's hook before an account is created; nil means no
	// hook.
	Provisioner authn.Provisioner
	// Signup and AutoLink are the two relaxation switches, read when Switches is
	// not set; their zero values are the closed ones.
	Signup   Signup
	AutoLink AutoLink
	// Switches, when it is set, is read at each resolution and is what Signup
	// and AutoLink then mean. It is how a caller whose switches change while the
	// process runs reads the value current at the moment of the decision. A value
	// it returns outside the switch's set reads as the closed one.
	Switches func() (Signup, AutoLink)
}

// Resolver resolves proofs to accounts. It is safe for concurrent use.
type Resolver struct {
	accounts    authn.Accounts
	admin       authn.AccountAdmin
	memberships authn.Memberships
	factors     FactorReader
	sso         authn.SSOProviders
	provisioner authn.Provisioner
	signup      Signup
	autoLink    AutoLink
	switches    func() (Signup, AutoLink)
}

// New builds a Resolver. A missing required dependency and a value of Signup or
// AutoLink outside its set are authn.ErrInvalidConfig.
func New(c Config) (*Resolver, error) {
	switch {
	case c.Accounts == nil:
		return nil, fmt.Errorf("%w: resolve: no Accounts", authn.ErrInvalidConfig)
	case c.Admin == nil:
		return nil, fmt.Errorf("%w: resolve: no AccountAdmin", authn.ErrInvalidConfig)
	case c.Memberships == nil:
		return nil, fmt.Errorf("%w: resolve: no Memberships", authn.ErrInvalidConfig)
	}
	switch c.Signup {
	case "", SignupClosed, SignupOpen:
	default:
		return nil, fmt.Errorf("%w: resolve: signup is not one of closed and open", authn.ErrInvalidConfig)
	}
	switch c.AutoLink {
	case "", AutoLinkNever, AutoLinkVerifiedEmail:
	default:
		return nil, fmt.Errorf("%w: resolve: auto-link is not one of never and verified-email", authn.ErrInvalidConfig)
	}
	return &Resolver{
		accounts:    c.Accounts,
		admin:       c.Admin,
		memberships: c.Memberships,
		factors:     c.Factors,
		sso:         c.SSO,
		provisioner: c.Provisioner,
		signup:      c.Signup,
		autoLink:    c.AutoLink,
		switches:    c.Switches,
	}, nil
}

// currentSwitches is the two relaxation switches as they stand at this
// resolution: read from the provider when one was given, and the configured
// values otherwise.
func (r *Resolver) currentSwitches() (Signup, AutoLink) {
	if r.switches != nil {
		return r.switches()
	}
	return r.signup, r.autoLink
}

// signupOpen reports whether a first login may open an account, as the switch
// stands at this decision.
func (r *Resolver) signupOpen() bool {
	signup, _ := r.currentSwitches()
	return signup == SignupOpen
}

// autoLinkVerifiedEmail reports whether a first social login may be linked by a
// verified email, as the switch stands at this decision.
func (r *Resolver) autoLinkVerifiedEmail() bool {
	_, autoLink := r.currentSwitches()
	return autoLink == AutoLinkVerifiedEmail
}

// Request is one resolution.
type Request struct {
	Proof authn.Proof
	// Purpose is one of authn.PurposeLogin, PurposeLink, PurposeStepUp and
	// PurposeEnroll. Any other value, the empty one included, is refused.
	Purpose string
	// Account is the account the flow was started on, which is the account of a
	// link, a step-up or an enrollment and is ignored for a login.
	Account authn.AccountID
}

// Resolve resolves the proof of a request.
//
// A login resolves any of the three sources of a proof: an account the method
// already determined, a phone the method proved, or an external identity. A
// first login of a phone or an external identity that would open an account
// writes nothing and answers a Result whose Pending describes the account;
// Create opens it once a username is chosen. The other purposes never write: the
// proof must belong to the account the flow was started on, and a link of an
// external identity answers whether the key is free for that account.
//
// A failure is one of the sentinels of the root package, wrapped, or an error of
// a dependency passed on as it came; a caller maps what it does not know to an
// unavailable dependency.
func (r *Resolver) Resolve(ctx context.Context, req Request) (Result, error) {
	switch req.Purpose {
	case authn.PurposeLogin:
	case authn.PurposeLink, authn.PurposeStepUp, authn.PurposeEnroll:
		if req.Account == "" {
			return Result{}, fmt.Errorf("%w: resolve: no account for purpose %s", authn.ErrInvalidArgument, req.Purpose)
		}
	default:
		return Result{}, fmt.Errorf("%w: resolve: unknown purpose", authn.ErrInvalidArgument)
	}
	p := req.Proof
	sources := 0
	for _, set := range []bool{p.Account != "", p.Identifier != nil, p.External != nil} {
		if set {
			sources++
		}
	}
	if sources != 1 {
		return Result{}, fmt.Errorf("%w: resolve: a proof names exactly one source", authn.ErrInvalidArgument)
	}
	if req.Purpose == authn.PurposeLink && p.External == nil {
		return Result{}, fmt.Errorf("%w: resolve: only an external identity is linked", authn.ErrInvalidArgument)
	}
	switch {
	case p.Account != "":
		return r.fromAccount(ctx, req)
	case p.Identifier != nil:
		return r.fromIdentifier(ctx, req)
	}
	return r.fromExternal(ctx, req)
}

// credentialsFailure is the failure of a login whose source is a password or a
// phone and whose account cannot log in: the shape of a wrong password.
func credentialsFailure(why string, cause error) error {
	if cause == nil {
		return fmt.Errorf("%w: resolve: %s", authn.ErrInvalidCredentials, why)
	}
	return fmt.Errorf("%w: resolve: %s: %w", authn.ErrInvalidCredentials, why, cause)
}

// canLogIn reports whether an account is one an interactive login may land on:
// an enabled account that is not a service account.
func canLogIn(a authn.Account) bool {
	return a.Status == authn.StatusActive && a.Kind != authn.KindService
}

// getAccount reads an account, passing a dependency's failure on. found is
// false for an account that does not exist.
func (r *Resolver) getAccount(ctx context.Context, id authn.AccountID) (a authn.Account, found bool, err error) {
	a, err = r.accounts.Get(ctx, id)
	switch {
	case err == nil:
		return a, true, nil
	case errors.Is(err, authn.ErrAccountNotFound):
		return authn.Account{}, false, nil
	}
	return authn.Account{}, false, err
}

// externalAccount reads the account an external login lands on and refuses one
// that cannot log in, with authn.ErrAccountUnavailable: the caller was
// authenticated at the provider already. An account that is not there is
// authn.ErrAccountNotFound.
func (r *Resolver) externalAccount(ctx context.Context, id authn.AccountID) error {
	a, found, err := r.getAccount(ctx, id)
	switch {
	case err != nil:
		return err
	case !found:
		return fmt.Errorf("%w: resolve: the bound account does not exist", authn.ErrAccountNotFound)
	case !canLogIn(a):
		return fmt.Errorf("%w: resolve", authn.ErrAccountUnavailable)
	}
	return nil
}

// fromAccount resolves a proof whose method determined the account itself.
func (r *Resolver) fromAccount(ctx context.Context, req Request) (Result, error) {
	a, found, err := r.getAccount(ctx, req.Proof.Account)
	switch {
	case err != nil:
		return Result{}, err
	case !found:
		return Result{}, credentialsFailure("no such account", authn.ErrAccountNotFound)
	case !canLogIn(a):
		return Result{}, credentialsFailure("the account cannot log in", nil)
	}
	return r.confirmed(req, a.ID)
}

// confirmed ends a resolution that found an account and wrote nothing. For a
// login the account is the one found. For every other purpose it must be the
// account the flow was started on, and anything else is a proof of somebody
// else's.
func (r *Resolver) confirmed(req Request, id authn.AccountID) (Result, error) {
	if req.Purpose == authn.PurposeLogin {
		return Result{Account: id, Outcome: OutcomeBound}, nil
	}
	if id != req.Account {
		return Result{}, credentialsFailure("the proof is not of the flow's account", nil)
	}
	return Result{Account: id, Outcome: OutcomeVerified}, nil
}

// seamFault is the failure of a seam the resolver does not own. It reads as an
// unavailable dependency and nothing else: the cause is for the log and is not
// in the chain errors.Is walks, so that what a host's hook returned cannot pass
// for one of the sentinels the engine maps to another answer.
type seamFault struct {
	seam  string
	cause error
}

func (e *seamFault) Error() string {
	if e.cause == nil {
		return "authn: resolve: " + e.seam + " is unavailable"
	}
	return "authn: resolve: " + e.seam + " failed: " + e.cause.Error()
}

func (e *seamFault) Is(target error) bool { return target == authn.ErrUnavailable }

// Cause is what the seam returned.
func (e *seamFault) Cause() error { return e.cause }

// emailDomain is the domain of an email address in lower case, or empty for an
// address that has no domain a verification could name: not exactly one @, an
// empty side, or a character outside ASCII. The address is already normalized
// by whoever hands it over; this reads a domain from it and decides nothing
// else.
func emailDomain(email string) string {
	email = strings.ToLower(strings.TrimSpace(email))
	local, domain, ok := strings.Cut(email, "@")
	if !ok || local == "" || domain == "" || strings.Contains(domain, "@") {
		return ""
	}
	for i := 0; i < len(email); i++ {
		if email[i] >= 0x80 || email[i] <= ' ' {
			return ""
		}
	}
	return domain
}
