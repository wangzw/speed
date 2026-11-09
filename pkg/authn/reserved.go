package authn

import "context"

// UsernameReservation answers whether a username is one the system keeps for
// itself.
//
// A username and a tenant name share one namespace: a personal tenant's name
// is its owner's username, and both live under the roster's name column. A
// surface that lets an account choose a name therefore has to refuse the
// names a username write refuses, and the reserved set is the account module's
// — the built-in list plus whatever the deployment appended — so it is asked
// through this seam rather than restated by the caller.
//
// The account module delivers it exclusively. A name outside the username
// shape is the normalizer's refusal rather than an answer about the set, and
// the caller answers it as a bad name.
type UsernameReservation interface {
	// ReservedUsername reports whether username belongs to the reserved set,
	// the prefix the personal namespace keeps included. The name is normalized
	// first; one that cannot be is ErrInvalidIdentifier.
	ReservedUsername(ctx context.Context, username string) (bool, error)
}
