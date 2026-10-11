// Package identity holds the authenticated caller and the context it travels
// in.
//
// Both making one and putting one into a context live here, so that neither is
// reachable from outside this capability directory: Go admits an internal
// package to the subtree it sits in and to nothing else. The interface package
// re-exports the type under an alias, so a permission model reads an identity
// the ordinary way while a package outside the directory cannot write one. The
// packages inside it are admitted by the compiler and held to a closed list by
// the dependency test of the interface package: the interface package itself,
// the gateway, the login engine and the test builder. Deciding who the caller is
// exactly once, in exactly one place, is the whole of what authentication does.
//
// The type lives here rather than beside the value types in internal/model
// because its value lies in its fields being unexported, and a package that
// also held the other types without that property would put them all in one
// scope. It refers to the value types in its signatures.
//
// The readers of an Identity are methods, as the surface the design states,
// and each one that returns a slice or a map returns a copy: an identity has
// no write entry and a caller cannot make one by changing what it was handed.
// The context writers, With and WithAccess, are functions of this package and
// therefore out of reach of everything outside the subtree.
package identity

import (
	"context"
	"maps"
	"slices"
	"time"

	"github.com/vislake/speed/pkg/authn/internal/model"
)

// Identity is the caller a request was authenticated as. The zero value is no
// caller: it has no account, satisfies no requirement, and its key is empty.
// Whether a request has an identity at all is told by From's second result,
// never by the key being non-empty.
type Identity struct {
	account    model.AccountID
	actor      model.AccountID // empty means the same as account
	session    model.SessionID
	methods    []model.Method
	assurance  model.Assurance
	fromIDP    bool
	authTime   time.Time
	backend    string
	challenge  string
	scope      string
	provider   string
	attributes map[string]string
}

// New makes an identity. On the request chain the gateway is its only caller,
// after it checked the principal's claims; the test builder is the other. New
// stores the principal as it is and settles nothing: an assurance left unstated
// is read as a single factor by the gateway before the call, and is kept as
// AssuranceNone here.
//
// Backend and challenge are separate parameters and not read from the
// principal: the gateway stamps them from the verifier's name and the
// credential's carrier, and nothing a backend handed over may reach them. The
// principal is copied, so a backend changing its own copy afterwards changes
// nothing here.
func New(p model.Principal, backend, challenge string) Identity {
	return Identity{
		account:    p.Account,
		actor:      p.Actor,
		session:    p.Session,
		methods:    slices.Clone(p.Methods),
		assurance:  p.Assurance,
		fromIDP:    p.IDPAssured,
		authTime:   p.AuthTime,
		backend:    backend,
		challenge:  challenge,
		scope:      p.Scope,
		provider:   p.Provider,
		attributes: maps.Clone(p.Attributes),
	}
}

// Account is the account the request is taken to be from: for an impersonation
// it is the account being impersonated.
func (i Identity) Account() model.AccountID { return i.account }

// Actor is the account whose hands are on the request. Without an impersonation
// it is the same as Account.
func (i Identity) Actor() model.AccountID {
	if i.actor == "" {
		return i.account
	}
	return i.actor
}

// Impersonating reports whether Actor differs from Account.
func (i Identity) Impersonating() bool { return i.Actor() != i.account }

// Key is the accountability key, and it is always string(Actor()).
//
// Rate limiting counts by it and the audit trail records it as the actor, so
// that an impersonation spends the actor's allowance and not the impersonated
// account's, and blame does not land on the one impersonated. Every consumer
// uses it as an opaque string and none parses it. The zero identity's key is
// empty, and an empty key does not mean "recognized as nobody in particular":
// whether there is an identity is told by From.
func (i Identity) Key() string { return string(i.Actor()) }

// Session is the login session the request belongs to, empty for a backend
// with none.
func (i Identity) Session() model.SessionID { return i.session }

// Methods is the set of ways the authentication was done, as a copy.
func (i Identity) Methods() []model.Method { return slices.Clone(i.methods) }

// Assurance is the strength the authentication reached.
func (i Identity) Assurance() model.Assurance { return i.assurance }

// AssuranceFromIDP reports whether the multi-factor level is an enterprise
// IdP's declaration and not factors verified here.
func (i Identity) AssuranceFromIDP() bool { return i.fromIDP }

// AuthTime is when the last interactive authentication happened.
func (i Identity) AuthTime() time.Time { return i.authTime }

// Backend is the name of the verifier that authenticated the request, as the
// gateway read it from the verifier itself.
func (i Identity) Backend() string { return i.backend }

// Challenge is the scheme name a re-authentication challenge uses, set by the
// gateway from the credential's carrier.
func (i Identity) Challenge() string { return i.challenge }

// Scope is the tenant an impersonation is confined to. The second result is
// false when the request is not an impersonation.
func (i Identity) Scope() (string, bool) { return i.scope, i.scope != "" }

// Provider is the enterprise SSO provider the identity came through, empty when
// none did.
func (i Identity) Provider() string { return i.provider }

// Attribute reads one attribute without copying the set, which is the way to
// read on a hot path.
func (i Identity) Attribute(key string) (string, bool) {
	v, ok := i.attributes[key]
	return v, ok
}

// Attributes is the whole attribute set as a copy. It is never nil, so a
// caller may write into it.
func (i Identity) Attributes() map[string]string {
	out := make(map[string]string, len(i.attributes))
	maps.Copy(out, i.attributes)
	return out
}

// Satisfies reports whether the identity meets a requirement at the instant
// now. All of these hold:
//
//   - the assurance is not below the requirement's;
//   - the requirement asks for no freshness (MaxAge zero), or no more than
//     MaxAge has passed since AuthTime. An AuthTime after now is a negative
//     age and is not stale, so that clocks of two hosts that differ a little
//     do not read a login that just happened as stale; how far ahead a time may
//     be is the gateway's exit check, not this method. A zero AuthTime is as old
//     as time can be measured and is stale under any MaxAge. A zero now is a
//     clock that was never set and meets no MaxAge;
//   - when the requirement is Local, the assurance is not an IdP's
//     declaration.
//
// The refusal side is fail-closed: the zero identity satisfies nothing,
// whatever the requirement, and a negative MaxAge is met by no identity.
func (i Identity) Satisfies(r model.Requirement, now time.Time) bool {
	if i.account == "" {
		return false
	}
	if i.assurance < r.Assurance {
		return false
	}
	if r.MaxAge < 0 {
		return false
	}
	if r.MaxAge > 0 && (now.IsZero() || now.Sub(i.authTime) > r.MaxAge) {
		return false
	}
	if r.Local && i.fromIDP {
		return false
	}
	return true
}

// key and accessKey are unexported and empty, so no value from another package
// can equal either.
type (
	key       struct{}
	accessKey struct{}
)

// With puts an identity into a request's context.
func With(ctx context.Context, i Identity) context.Context {
	return context.WithValue(ctx, key{}, i)
}

// From takes the identity out of a request's context.
//
// There is no fallback. The second result is false on an exempt route, on an
// optional route without a credential, on an endpoint the gateway does not
// guard, in a process where this module was not taken into the command that
// started it, and anywhere off the request path. A caller has to handle that:
// the alternative is reading an anonymous request as some subject. This is the
// reverse of how a logger travels, where falling back to the process root is
// right — a logger has a default and an identity does not.
func From(ctx context.Context) (Identity, bool) {
	i, ok := ctx.Value(key{}).(Identity)
	return i, ok
}

// WithAccess puts the access level the gateway recorded for the route into a
// request's context.
func WithAccess(ctx context.Context, a model.Access) context.Context {
	return context.WithValue(ctx, accessKey{}, a)
}

// AccessFrom takes the recorded access level out of a request's context.
//
// There is no fallback either: a context the gateway did not decide anything
// for reads as AccessUnknown, which a consumer must not treat as open or as
// authenticated. It and From are a pair to read together.
func AccessFrom(ctx context.Context) model.Access {
	a, _ := ctx.Value(accessKey{}).(model.Access)
	return a
}
