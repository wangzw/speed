// Package authn is the interface package of the identity system: the shapes a
// credential-verifying backend implements, the route registrations a module
// makes on the gateway, and the identity a handler reads out of a request.
//
// This package registers no module and exports no module name: what registers
// is a subpackage, each delivering one capability this package defines. The
// gateway delivers Gate exclusively, and one subpackage per verifying backend
// delivers Verifier without claiming it. A module that only reads an identity
// imports this package, and nothing else follows it into the assembly.
//
// Nothing here reaches past the standard library and the value-type packages
// under internal/, which themselves import the standard library alone: no gin,
// no database, no third-party library appears in a signature. The gateway takes
// the registry, the configuration and the HTTP entry point; a backend takes
// whatever its credential store needs. Keeping those out of the interface
// leaves a reader of FromContext carrying none of them, and a test in this
// directory holds the line.
//
// The gateway takes a credential from two places, the Authorization header and
// a cookie some backend claims, and hands it unread to the backend that claims
// that carrier. This package neither interprets a credential nor decides what a
// caller may do: that is a permission model's work, it is a separate module,
// and neither this package nor the gateway writes 403.
package authn

import (
	"context"
	"net/http"
)

// Gate is the capability the authentication layer delivers, and the surface a
// route's access level is registered on.
//
// It is taken up with core.Resolve[authn.Gate](reg) in the registrant's own
// Init and is not declared in Requires: the gateway is constructed before any
// Init runs, so there is no order to arrange, and a declaration would draw in no
// provider either — the gateway delivers Gate exclusively, which is a claim the
// resolution closure does not follow into a command.
//
// The three methods share one set of rules. The method and the path are the
// pair the registrant passed to Endpoint.Route, in the same spelling: a request
// matches a registration when its method and the path the entry point matched
// it to, c.FullPath(), are, character for character, a registered pair. This
// package invents no syntax of its own for it and does not interpret the path,
// so a route carrying a parameter or a catch-all is registered by writing it as
// it was registered with the entry point.
//
// A registration that no registered route carries never matches, and nothing
// reports it. A request that matches no route, or whose method the matched path
// does not allow, matches no registration.
//
// Registration holds from the gateway's Migrate callback until its Start
// callback, a window that contains the whole Init stage and reaches beyond it at
// both ends. A call outside that window is a mistake at the call site rather
// than a condition to report, and it panics, as the entry point's own
// registration surface does. When Start closes the window it checks every
// registration at once and fails the start on a failure: the endpoint name has
// to be one the entry point declares (ErrUnknownEndpoint), the endpoint has to
// be one the gateway guards (ErrExemptionUnguarded), and one route may not be
// registered both Exempt and Optional, or both Exempt and Impersonable
// (ErrAccessConflict).
type Gate interface {
	// Exempt registers one route as needing no credential: no credential is
	// read and no backend is asked. A request to it carries no identity and
	// AccessFromContext reads AccessOpen.
	Exempt(endpoint, method, path string)

	// Optional registers one route as accepting a request with no credential
	// at all. Such a request passes with no identity and AccessFromContext
	// reads AccessOptional. A request that carries a credential is
	// authenticated as on any guarded route, and a credential that does not
	// hold is still refused: passing it as anonymous would let an expired or
	// stolen credential be ignored on this route without the caller noticing
	// that it had stopped working. A credential the gateway cannot get a
	// verdict on is an outage, never an anonymous request.
	Optional(endpoint, method, path string)

	// Impersonable registers one route as letting an impersonating identity
	// through, one whose Actor differs from its Account. Such an identity is
	// admitted only to a route registered here and to an endpoint the
	// ScopeGuard covers; anywhere else the request is refused.
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
	// Peer is the remote address the entry point reported, which is the
	// proxy's address behind one. It is for a backend's log and its
	// throttling and is no ground for a judgement.
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
	// back: the Account and the Actor it returns begin with it. The empty
	// string is the library namespace, which belongs to the backends that
	// answer for accounts the account module keeps and to identifiers a host
	// imported; an identifier in it may not fall under any other backend's
	// non-empty prefix. StaticPrefix belongs to the backend named static and
	// to no other, and a backend that is not for library accounts declares a
	// non-empty prefix of its own. Declared non-empty prefixes may not nest.
	Accounts string
	// Impersonation lets this backend hand back a non-zero Actor and Scope.
	Impersonation bool
	// SSO lets this backend hand back a non-zero Provider and an IDPAssured
	// that is true.
	SSO bool
}

// Verifier is one credential-verifying backend.
//
// It is delivered without claiming the capability, so several stand side by
// side behind one gateway: a host accepting credentials from a session store
// and from a local table at once lists both, and each answers for the carriers
// it declared.
//
// A backend answers Unavailable and never Denied when what it reads the
// credential against cannot be reached, answers Denied and never Unavailable for
// a credential that does not hold, and returns once ctx ends.
type Verifier interface {
	// Name reports this backend's name: [a-z0-9][a-z0-9.-]*, unique within an
	// assembly, and a constant in the backend's code rather than an input.
	// The gateway stamps it on the identity as Backend; nothing a backend
	// hands over reaches that field. By convention it is the backend
	// subpackage's last name element, and nothing checks that: instances
	// taken up together do not carry the name of the module that produced
	// them.
	Name() string
	// Accepts reports the carriers this backend takes and the kind of
	// identity it may hand back. It is read once, at startup.
	Accepts() Accepts
	// Verify answers for one credential that arrived on a carrier this
	// backend declared. There is no subject argument: a cookie or a token
	// holds none, and the user name in a Basic pair is a login name and not an
	// account identifier.
	//
	// It returns once ctx ends, answering Unavailable. The gateway's call
	// budget arrives as ctx's deadline, and a call that ignores it keeps its
	// request goroutine and a slot of the gateway's in-flight gate until it
	// returns. When what ended ctx is the request's own deadline or its
	// caller going away rather than the budget, the gateway does not charge
	// that Unavailable to this backend.
	Verify(ctx context.Context, c Credential) Verdict
}

// Challenger is implemented by a backend that writes its own part of a 401's
// challenge, which the gateway finds with a type assertion.
//
// realm is the realm the gateway uses, already escaped for writing between the
// quotes of a quoted string. rejected is true when the request carried a
// credential this backend claims and that credential was refused: a token
// backend adds error="invalid_token" then, as RFC 6750 and RFC 9068 require.
// Each returned string is one WWW-Authenticate field value; the gateway writes
// them together, comma separated, as the one header every 401 carries.
type Challenger interface {
	Challenge(realm string, rejected bool) []string
}

// CookieClearer is implemented by a backend whose credential is a cookie, which
// the gateway finds with a type assertion. When a credential in that cookie is
// refused, the gateway has the backend write the header that clears the
// cookie, so a browser does not keep sending one that cannot hold.
type CookieClearer interface {
	ClearCookie(w http.ResponseWriter)
}
