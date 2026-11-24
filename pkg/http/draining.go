package http

import "context"

// Draining returns a channel that is closed once the endpoint serving the request ctx
// belongs to has stopped accepting and begun to drain. A context not derived from a
// request returns nil, a channel that never becomes ready.
func Draining(ctx context.Context) <-chan struct{} {
	ch, _ := ctx.Value(drainingKey{}).(<-chan struct{})
	return ch
}

type drainingKey struct{}

func withDraining(ctx context.Context, ch <-chan struct{}) context.Context {
	return context.WithValue(ctx, drainingKey{}, ch)
}
