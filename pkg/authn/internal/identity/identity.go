// Package identity holds the authenticated caller and the context it travels
// in: making one and writing it into a context are reachable only inside this
// capability directory, and every reader returning a slice or map returns a copy.
package identity

import (
	"context"
	"maps"
	"slices"
	"time"

	"github.com/vislake/speed/pkg/authn/internal/model"
)

// Identity is the caller a request was authenticated as; the zero value is no
// caller. Whether a request has an identity is told by From's second result,
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

// New makes an identity from a principal, storing it as it is and settling
// nothing; backend and challenge are separate because the gateway stamps them
// and nothing a backend hands over may reach them. The principal is copied.
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

// Key is the accountability key, always string(Actor()), which rate limiting
// counts by and the audit trail records as the actor; every consumer treats it
// as an opaque string. An empty key does not mean no identity: From tells that.
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

// Satisfies reports whether the identity meets a requirement at now: assurance
// at least the required one, a MaxAge fresh, and a Local requirement not met by
// an IdP declaration. The zero identity and a negative MaxAge satisfy nothing.
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

type (
	key       struct{}
	accessKey struct{}
)

// With puts an identity into a request's context.
func With(ctx context.Context, i Identity) context.Context {
	return context.WithValue(ctx, key{}, i)
}

// From takes the identity out of a request's context and has no fallback: the
// second result is false wherever none was written, which a caller has to
// handle rather than read as some subject.
func From(ctx context.Context) (Identity, bool) {
	i, ok := ctx.Value(key{}).(Identity)
	return i, ok
}

// WithAccess puts the access level the gateway recorded for the route into a
// request's context.
func WithAccess(ctx context.Context, a model.Access) context.Context {
	return context.WithValue(ctx, accessKey{}, a)
}

// AccessFrom takes the access level out of a request's context; with none
// recorded it reads as AccessUnknown, which a consumer must not treat as open
// or as authenticated. It and From are read together.
func AccessFrom(ctx context.Context) model.Access {
	a, _ := ctx.Value(accessKey{}).(model.Access)
	return a
}
