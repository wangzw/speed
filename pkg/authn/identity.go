package authn

import (
	"context"

	"github.com/vislake/speed/pkg/authn/internal/identity"
)

// Separator stands between the owner and the subject in an identity's string
// form.
//
// A subject may not contain it. A backend whose natural identifier does is the
// one that maps it, because the alternative is a key that can be cut two ways.
const Separator = identity.Separator

// Identity is the caller this request was authenticated as.
//
// Reading one is open to anyone. Making one, and putting one into a request's
// context, are not on this surface at all: both live inside this release unit,
// so no layer on the chain can write an identity. What must happen once is the
// deciding, not the looking.
type Identity = identity.Identity

// FromContext takes the identity out of a request's context.
//
// There is no fallback. The second result is false on an exempt route, in a
// process where this module was not taken into the command that started it, and
// anywhere off the request path — and a caller has to handle that, because the
// alternative is reading an anonymous request as some subject. This is the
// reverse of how a logger travels, where falling back to the process root is
// right: a logger has a default and an identity does not.
func FromContext(ctx context.Context) (Identity, bool) { return identity.From(ctx) }
