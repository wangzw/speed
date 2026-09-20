// Package authn authenticates the caller of an HTTP request, decides which
// routes go without it, and carries the identity a permission model reads.
//
// This is the interface package. It registers no module and exports no module
// name: what registers is the gateway subpackage, which delivers Gate
// exclusively, and one subpackage per credential-verifying backend, each
// delivering Verifier without claiming it. A module that only reads an identity
// imports this package, and nothing else follows it into the assembly.
//
// Nothing here reaches past the standard library. The gateway takes the
// registry, the configuration and the HTTP entry point; a backend takes
// whatever its credential store needs. Keeping those out of the interface
// leaves a reader of FromContext carrying none of them.
//
// This package does not decide what a caller may do. That is a permission
// model's work, it is a separate module, and this one never writes 403.
package authn

import "context"

// Gate is the capability the authentication layer delivers, and the surface an
// exemption is registered on.
//
// It is taken up with core.Resolve[authn.Gate](reg) in the registrant's own
// Init and is not declared in Requires: the gateway is constructed before any
// Init runs, so there is no order to arrange, while declaring it would draw the
// gateway into commands whose host never listed it.
type Gate interface {
	// Exempt registers one route as needing no credential. The pattern is
	// the string the registrant passed to Endpoint.Route, method included;
	// this package invents no syntax of its own for it.
	//
	// Registration holds during Init alone. A call outside that window is a
	// mistake at the call site rather than a condition to report, and it
	// panics, as the entry point's own registration surface does.
	Exempt(endpoint, pattern string)
}

// Credential is one credential to be verified, as it arrived.
type Credential struct {
	// Scheme is the authentication scheme named in the Authorization
	// header, lower-cased: Bearer and bearer are one scheme.
	Scheme string
	// Value is what followed the scheme, unchanged. Whether it is a token,
	// an encoded pair or something else is the backend's business.
	Value string
}

// Verifier is one credential-verifying backend.
//
// It is delivered without claiming the capability, so several stand side by
// side behind one gateway: a host accepting credentials from a directory and
// from a local store at once lists both, and each answers for what it owns.
type Verifier interface {
	// Name reports this backend's identity. The ownership table, the
	// startup account and the string form of an identity are written with
	// it. By convention it is the ModuleName of the subpackage that
	// registers this backend, and nothing checks that: instances taken up
	// together do not carry the name of the module that produced them.
	Name() string
	// Claim reports the subject space this backend owns.
	Claim() Claim
	// Verify answers for a subject that falls inside this backend's claim.
	// The subject arrives separately from the credential because ownership
	// was settled before the call.
	Verify(ctx context.Context, subject string, c Credential) Verdict
}

// Claim is the subject space one backend owns.
//
// A subject belongs to exactly one backend, and that is what lets a refusal be
// final. Without it no backend can say no: a stale row in one store outvotes an
// account disabled in another, and the store that knows least decides.
type Claim struct {
	// Schemes are the authentication schemes this backend takes whole. A
	// credential arriving under one of them goes to this backend, which
	// reads the subject out of it. Names here are compared lower-cased.
	Schemes []string
	// Subjects are the subjects this backend owns, written out. One may not
	// be empty, may not be blanks alone, and may not contain Separator.
	Subjects []string
	// Open says this backend answers for subjects no written claim covers.
	// A backend setting it implements Owner as well.
	Open bool
}

// Owner is implemented by a backend whose Claim is open.
//
// Owns is asked before any credential is read, so it settles who answers rather
// than whether the credential holds, and it spends none of the budget a
// verification gets.
type Owner interface {
	Owns(ctx context.Context, subject string) (bool, error)
}
