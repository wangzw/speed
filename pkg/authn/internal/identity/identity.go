// Package identity holds the authenticated caller and the context it travels
// in.
//
// Both making one and putting one into a context live here, so that neither is
// reachable from outside this release unit: Go admits an internal package to
// the subtree it sits in and to nothing else. The interface package re-exports
// the type under an alias, so a permission model reads an identity the ordinary
// way while no layer on the chain can write one. Deciding who the caller is
// exactly once, in exactly one place, is the whole of what authentication does.
package identity

import "context"

// Separator stands between the owner and the subject in the string form.
const Separator = "/"

// Identity is the caller a request was authenticated as.
type Identity struct {
	subject string
	owner   string
}

// New makes an identity. The gateway is its only caller: ownership was settled
// against the ownership table before this, and the subject was checked to be
// one the named backend owns.
func New(owner, subject string) Identity {
	return Identity{owner: owner, subject: subject}
}

// Subject is who the caller is, inside the space its owner claims.
func (i Identity) Subject() string { return i.subject }

// Owner is the name reported by the backend that authenticated this caller.
func (i Identity) Owner() string { return i.owner }

// Key is the string form, owner-qualified even where one backend runs alone.
//
// The qualification is not optional, so that bob and local/bob cannot both name
// one caller and a policy written against either does not silently miss the
// other. Migrating from a system whose subjects were bare therefore means
// rewriting what was written against them.
func (i Identity) Key() string { return i.owner + Separator + i.subject }

// key is unexported and empty, so no value from another package can equal it.
type key struct{}

// With puts an identity into a request's context.
func With(ctx context.Context, i Identity) context.Context {
	return context.WithValue(ctx, key{}, i)
}

// From takes the identity out of a request's context.
//
// There is no fallback. The second result is false on an exempt route, in a
// process where this module was not included or stated itself disabled, and
// anywhere off the request path. A caller has to handle that: the alternative
// is reading an anonymous request as some subject. This is the reverse of how a
// logger travels, where falling back to the process root is right — a logger
// has a default and an identity does not.
func From(ctx context.Context) (Identity, bool) {
	i, ok := ctx.Value(key{}).(Identity)
	return i, ok
}
