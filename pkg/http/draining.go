package http

import "context"

// Draining returns a channel that is closed once the endpoint serving the
// request ctx belongs to has stopped accepting and begun to drain.
//
// It is the signal for a response that does not end on its own — a stream of
// server-sent events, a long poll, a connection taken over for another
// protocol. The drain lets every in-flight request finish and does not cancel
// its context, because an ordinary request has to be able to complete against
// a whole system; a handler that watches only its request context is
// therefore not told anything, runs until the endpoint's drain-timeout cuts
// it, and the shutdown reports a drain that ran out of time. A long-lived
// handler selects on this channel as well and ends its response when it
// closes.
//
// ctx has to be the request's context or one derived from it. For any other
// context the result is nil, a channel that never becomes ready: a context no
// endpoint created belongs to no drain.
func Draining(ctx context.Context) <-chan struct{} {
	ch, _ := ctx.Value(drainingKey{}).(<-chan struct{})
	return ch
}

// drainingKey carries an endpoint's draining channel in every request context
// that endpoint's server creates.
type drainingKey struct{}

// withDraining returns ctx carrying ch as its draining signal.
func withDraining(ctx context.Context, ch <-chan struct{}) context.Context {
	return context.WithValue(ctx, drainingKey{}, ch)
}
