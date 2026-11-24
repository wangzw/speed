// Package authn is the identity system's interface package: the shapes a
// credential-verifying backend implements, the route registrations a module
// makes on the gateway, and the identity a handler reads out of a request.
package authn

import (
	"context"
	"net/http"
)

// Gate is the capability the authentication layer delivers and the surface a
// route's access level is registered on; a request matches when its method and
// c.FullPath() equal a registered pair, and registering outside Migrate..Start panics.
type Gate interface {
	// Exempt registers one route as needing no credential: no credential is
	// read and no backend is asked. A request to it carries no identity and
	// AccessFromContext reads AccessOpen.
	Exempt(endpoint, method, path string)

	// Optional registers one route as accepting a request with no credential:
	// such a request passes with no identity and reads AccessOptional, while a
	// credential that is present but does not hold is still refused.
	Optional(endpoint, method, path string)

	// Impersonable registers one route as admitting an impersonating identity,
	// one whose Actor differs from its Account; it is admitted only here and to
	// an endpoint the ScopeGuard covers.
	Impersonable(endpoint, method, path string)
}

// Credential is one credential to be verified, as it arrived.
type Credential struct {
	// Scheme is the authentication scheme named in the Authorization
	// header, lower-cased: Bearer and bearer are one scheme. It is empty
	// when the credential came from a cookie.
	Scheme string
	// Value is what followed the scheme, unchanged, or the value of the
	// cookie. Whether it is a token, an encoded pair or something else is
	// the backend's business.
	Value string
	// Cookie is the name of the cookie the credential came from, and empty
	// when it came from the Authorization header.
	Cookie string
	// Peer is the client address the entry point reported, for a backend's log
	// and throttling and never a ground for a judgement.
	Peer string
}

// Accepts is what a backend says about itself: which carriers it takes whole,
// and what kind of identity it may hand back. Every carrier key belongs to
// exactly one backend.
type Accepts struct {
	// Schemes are the Authorization schemes this backend takes whole, each an
	// RFC 9110 token and compared lower-cased. A credential arriving under
	// one of them goes to this backend.
	Schemes []string
	// Cookies are the cookie names this backend takes whole. A credential
	// arriving in one of them goes to this backend.
	Cookies []string
	// Accounts is the prefix of the account identifiers this backend may hand
	// back; the empty string is the library namespace, and declared non-empty
	// prefixes may not nest.
	Accounts string
	// Impersonation lets this backend hand back a non-zero Actor and Scope.
	Impersonation bool
	// SSO lets this backend hand back a non-zero Provider and an IDPAssured
	// that is true.
	SSO bool
}

// Verifier is one credential-verifying backend; several stand side by side
// behind one gateway, each answering for the carriers it declared. It answers
// Unavailable when its store is unreachable and Denied when a credential fails.
type Verifier interface {
	// Name reports this backend's name: [a-z0-9][a-z0-9.-]*, unique within an
	// assembly and a constant in its code. The gateway stamps it on the
	// identity as Backend.
	Name() string
	// Accepts reports the carriers this backend takes and the kind of
	// identity it may hand back. It is read once, at startup.
	Accepts() Accepts
	// Verify answers for one credential that arrived on a carrier this backend
	// declared; there is no subject argument. It returns once ctx ends, answering
	// Unavailable, and the call budget arrives as ctx's deadline.
	Verify(ctx context.Context, c Credential) Verdict
}

// Challenger is implemented by a backend that writes its own part of a 401's
// challenge; the gateway finds it with a type assertion. Each returned string is
// one WWW-Authenticate field value, written comma separated on every 401.
type Challenger interface {
	// Challenge returns the WWW-Authenticate field values this backend writes
	// for realm; rejected is true when the request carried a credential this
	// backend refused and false when it carried none this backend refused.
	Challenge(realm string, rejected bool) []string
}

// CookieClearer is implemented by a backend whose credential is a cookie; when a
// credential in that cookie is refused, the gateway has the backend write the
// header that clears it.
type CookieClearer interface {
	// ClearCookie writes the Set-Cookie header that clears the cookie the
	// refused credential came from.
	ClearCookie(w http.ResponseWriter)
}
