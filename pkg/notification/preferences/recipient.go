package preferences

import "context"

type recipientKey struct{}

// WithRecipient returns ctx carrying recipient as the user the preference
// routes act for.
func WithRecipient(ctx context.Context, recipient string) context.Context {
	return context.WithValue(ctx, recipientKey{}, recipient)
}

func recipientFrom(ctx context.Context) (string, bool) {
	recipient, ok := ctx.Value(recipientKey{}).(string)
	return recipient, ok
}
