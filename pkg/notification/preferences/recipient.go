package preferences

import "context"

// recipientKey is the context key under which the recipient a preference request
// acts for travels.
type recipientKey struct{}

// WithRecipient returns ctx carrying recipient as the user the preference routes
// act for.
//
// This module holds no identity and declares no permission: who may read or
// change a recipient's preferences is the host's authorization decision. The
// host's identity and authorization side therefore names the recipient — by
// calling this on the request context before the request reaches this module's
// handlers — and the handlers read it back rather than resolving it. A route
// reached with no recipient acts for none: the read answers for the empty
// recipient, and a write is refused, because a preference names a recipient and
// none was given.
func WithRecipient(ctx context.Context, recipient string) context.Context {
	return context.WithValue(ctx, recipientKey{}, recipient)
}

// recipientFrom reads the recipient the host's identity and authorization side
// put on the request context.
func recipientFrom(ctx context.Context) (string, bool) {
	recipient, ok := ctx.Value(recipientKey{}).(string)
	return recipient, ok
}
