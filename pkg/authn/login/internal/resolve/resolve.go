// Package resolve is the account resolution step of the login engine: it turns
// an authn.Proof into the account it belongs to, and for a first login decides
// whether to create an account, bind an existing one, or refuse. An external
// identity's key is always the provider and subject, never an email address.
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

const (
	// SignupClosed refuses a first login that would open an account.
	SignupClosed Signup = "closed"
	// SignupOpen lets a first login open an account.
	SignupOpen Signup = "open"
)

// Config is what a [Resolver] is built from.
type Config struct {
	// Accounts reads the account store. Required.
	Accounts authn.Accounts
	// Admin is the account administrator the resolver reads through. Required.
	Admin authn.AccountAdmin
	// Memberships reads and writes the provider tenants an account joins.
	// Required.
	Memberships authn.Memberships
	// SSO is the register of enterprise providers. Without it an enterprise
	// identity cannot be resolved without a binding.
	SSO authn.SSOProviders
	// Provisioner is the host's hook before an account is created; nil means no
	// hook.
	Provisioner authn.Provisioner
	// NormalizeUsername folds a claim value to the shared username and name
	// form. The account module delivers the same rule; the engine hands it in so
	// that this package imports neither the account module nor its library.
	NormalizeUsername func(string) (string, bool)
	// Signup is the relaxation switch, read when Switches is not set; its zero
	// value is the closed one.
	Signup Signup
	// Switches, when it is set, is read at each resolution and is what Signup
	// then means. It is how a caller whose switch changes while the process runs
	// reads the value current at the moment of the decision. A value it returns
	// outside the switch's set reads as the closed one.
	Switches func() Signup
}

// Resolver resolves proofs to accounts. It is safe for concurrent use.
type Resolver struct {
	accounts    authn.Accounts
	admin       authn.AccountAdmin
	memberships authn.Memberships
	sso         authn.SSOProviders
	provisioner authn.Provisioner
	normalize   func(string) (string, bool)
	signup      Signup
	switches    func() Signup
}

// New builds a Resolver. A missing required dependency and a value of Signup
// outside its set are authn.ErrInvalidConfig.
func New(c Config) (*Resolver, error) {
	switch {
	case c.Accounts == nil:
		return nil, fmt.Errorf("%w: resolve: no Accounts", authn.ErrInvalidConfig)
	case c.Admin == nil:
		return nil, fmt.Errorf("%w: resolve: no AccountAdmin", authn.ErrInvalidConfig)
	case c.Memberships == nil:
		return nil, fmt.Errorf("%w: resolve: no Memberships", authn.ErrInvalidConfig)
	case c.NormalizeUsername == nil:
		return nil, fmt.Errorf("%w: resolve: no username normalizer", authn.ErrInvalidConfig)
	}
	switch c.Signup {
	case "", SignupClosed, SignupOpen:
	default:
		return nil, fmt.Errorf("%w: resolve: signup is not one of closed and open", authn.ErrInvalidConfig)
	}
	return &Resolver{
		accounts:    c.Accounts,
		admin:       c.Admin,
		memberships: c.Memberships,
		sso:         c.SSO,
		provisioner: c.Provisioner,
		normalize:   c.NormalizeUsername,
		signup:      c.Signup,
		switches:    c.Switches,
	}, nil
}

// currentSignup is the relaxation switch as it stands at this resolution: read
// from the provider when one was given, and the configured value otherwise.
func (r *Resolver) currentSignup() Signup {
	if r.switches != nil {
		return r.switches()
	}
	return r.signup
}

// signupOpen reports whether a first login may open an account, as the switch
// stands at this decision.
func (r *Resolver) signupOpen() bool {
	return r.currentSignup() == SignupOpen
}

// Request is one resolution.
type Request struct {
	// Proof is the credential the method proved.
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

func canLogIn(a authn.Account) bool {
	return a.Status == authn.StatusActive && a.Kind != authn.KindService
}

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
