// Package model holds the value types of the authentication capability: the
// identifiers, the closed sets, and the plain records that the root package
// re-exports under aliases and that the identity, verdict and step packages
// refer to in their signatures.
//
// The criterion for a type landing here is that more than one of Identity,
// Verdict and Step mentions it, or that it appears on the root package's
// surface without carrying a property of its own. A type whose value lies in
// not being writable from outside — Identity, Verdict, Step — lives in a package
// of its own instead, so that the unexported fields of one are not in scope of
// the other. This package imports nothing but the standard library and no
// package of this capability directory, which is what keeps the layering
// acyclic: model, then identity, verdict and step, then the root.
//
// Only shapes, constants and format checks live here. A policy over the shapes
// — which account namespace a backend may issue from, how far a clock may drift,
// what totals a stamped identity's attributes may come to — is the gateway's,
// because it depends on facts only the gateway holds. The one set of numbers
// that is declared here and read by several modules is the sub-bounds of the two
// writers of an attribute set, the account module and the login engine's idp.*
// payload (bounds.go), with the format checks that apply them: the account,
// session and login modules all refer to them and none writes a number of its
// own, and the gateway's totals are repeated there only so that a test can show
// the sub-bounds add up to less.
package model
