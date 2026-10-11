package authn

import (
	"context"

	"github.com/vislake/speed/pkg/authn/internal/identity"
)

// Identity is the caller this request was authenticated as.
//
// An identity is centred on an account and carries no owner: its fields are the
// account, the actor whose hands are on the request, the session, the set of
// authentication methods, the assurance level and whether it is an IdP's
// declaration, the time of the last interactive authentication, the name of
// the backend that verified the credential, the scheme name a
// re-authentication challenge uses, the impersonation scope, the enterprise SSO
// provider, and a bounded attribute set. Backend and Challenge are stamped by
// the gateway and come from nothing a backend handed over.
//
// Its methods are:
//
//	Account() AccountID               the account the request is taken to be from
//	Actor() AccountID                 the account whose hands are on the request
//	Impersonating() bool              Actor() != Account()
//	Key() string                      the accountability key, always string(Actor())
//	Session() SessionID
//	Methods() []Method                a copy
//	Assurance() Assurance
//	AssuranceFromIDP() bool           the multi-factor level is an IdP's declaration
//	AuthTime() time.Time
//	Backend() string
//	Challenge() string
//	Scope() (string, bool)            the tenant an impersonation is confined to
//	Provider() string
//	Attribute(key string) (string, bool)
//	Attributes() map[string]string    a copy
//	Satisfies(r Requirement, now time.Time) bool
//
// Reading one is open to anyone. Making one, and putting one into a request's
// context, are not on this surface at all: both live in an internal package of
// this capability directory, which the compiler keeps every package outside the
// directory from importing. Inside the directory the writers are a closed
// list, held by deps_test.go: this package, which re-exports the read side and
// nothing else; the gateway, which writes the identity of the request chain;
// the login engine, which derives a context for the one audit record of a login
// and hands it to nothing else; and authntest, whose builder is for tests. A
// backend or a login method that imported the writing package would be outside
// the list and fails that test. What must happen once is the deciding, not the
// looking. The zero Identity is no caller: it has no account, satisfies no
// requirement and its key is empty, so whether a request has an identity is told
// by FromContext's second result and never by the key being non-empty.
type Identity = identity.Identity

// FromContext takes the identity out of a request's context.
//
// There is no fallback. The second result is false on an exempt route, on an
// optional route that carried no credential, on an endpoint the gateway does not
// guard, in a process where this module was not taken into the command that
// started it, and anywhere off the request path — and a caller has to handle
// that, because the alternative is reading an anonymous request as some
// account. This is the reverse of how a logger travels, where falling back to
// the process root is right: a logger has a default and an identity does not.
func FromContext(ctx context.Context) (Identity, bool) { return identity.From(ctx) }

// AccessFromContext takes the access level the gateway recorded for the route a
// request matched out of the request's context.
//
// There is no fallback either: a context the gateway decided nothing for reads
// as AccessUnknown, which a consumer must not treat as open or as authenticated.
// It and FromContext are a pair to read together, because the access level is
// what tells the causes of an absent identity apart.
func AccessFromContext(ctx context.Context) Access { return identity.AccessFrom(ctx) }
