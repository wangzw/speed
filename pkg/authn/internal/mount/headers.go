package mount

import (
	"context"
	nethttp "net/http"
)

// responseHeadersKey is the context key under which the wrapper leaves the
// response's header map for the typed handler of a route. A typed handler
// receives no response writer, but an answer can carry a header the body does
// not express: the WWW-Authenticate challenge of a 401, or the Cache-Control
// directive a session library adds beside a Set-Cookie. Those are written
// through this map before the handler returns, while the entry point has not
// written the response yet.
type responseHeadersKey struct{}

// withResponseHeaders returns ctx carrying header, the response's header map. A
// nil map leaves ctx as it is.
func withResponseHeaders(ctx context.Context, header nethttp.Header) context.Context {
	if header == nil {
		return ctx
	}
	return context.WithValue(ctx, responseHeadersKey{}, header)
}

// responseHeaders is the response's header map ctx carries, or nil when the
// handler is not running under the wrapper.
func responseHeaders(ctx context.Context) nethttp.Header {
	header, _ := ctx.Value(responseHeadersKey{}).(nethttp.Header)
	return header
}

// SetResponseHeader writes one response header from a typed handler, replacing
// any value already there. A handler receives no response writer, so this is how
// an answer carries a header its body does not express. Outside a route mounted
// through the Mounter there is no response to write to, and the call does
// nothing: a handler called directly by a test still answers its status and
// body.
func SetResponseHeader(ctx context.Context, name, value string) {
	if header := responseHeaders(ctx); header != nil {
		header.Set(name, value)
	}
}

// AddResponseHeader appends one value to a response header from a typed handler,
// leaving values already written there in place. It is for a directive that is
// additional to one a layer set: the wrapper's Cache-Control: no-store stays,
// and the handler adds the library's no-cache="Set-Cookie" beside it.
func AddResponseHeader(ctx context.Context, name, value string) {
	if header := responseHeaders(ctx); header != nil {
		header.Add(name, value)
	}
}

// UnauthorizedChallenge is the WWW-Authenticate value of a 401 written by this
// capability: Bearer and Cookie, never Basic. It is the value Unauthorized
// writes, and it is what a typed handler puts in the Fault it returns to have
// the entry point write the same challenge, in the header the matched 401
// response declares.
func UnauthorizedChallenge(realm string) string {
	return challenge(realm)
}
