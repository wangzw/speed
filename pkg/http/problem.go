package http

import (
	"context"
	"encoding/json"
	nethttp "net/http"

	"github.com/gin-gonic/gin"

	"github.com/vislake/speed/pkg/i18n"
)

// Problem is the content of one error response. Its fields are the ones RFC
// 9457 gives a problem details object.
type Problem struct {
	// Type identifies the kind of problem; it defaults to "about:blank".
	Type string
	// Title is a short summary of the kind of problem; it defaults to the
	// standard text of Status.
	Title string
	// Status is the status code the response is written with. A value that
	// is not a final status code, one outside 200 through 599, is written
	// as 500.
	Status int
	// Detail explains this occurrence of the problem.
	Detail string
	// Instance identifies this occurrence of the problem.
	Instance string
	// Cause is what the problem came from. The default response leaves it
	// out of the body, so nothing a caller wraps in it reaches a client; an
	// ErrorResponder that wants it reads it here.
	Cause error
}

// ErrorResponder decides the shape of every error response this module writes:
// the fallback's answer to a panic, the answer to a request that matches no
// route and to one whose method the matched path does not allow, and the
// responses written through Fail.
//
// It is taken up as an optional dependency, the way Logger is, and absence is
// the ordinary case: the default shape is a problem details object, which is
// what most assemblies want. A module that owns an API's error shape delivers
// one to replace it, and what it replaces is the whole of the shape.
type ErrorResponder interface {
	// RespondError writes the response for p. It is called only while no
	// part of the response has been written, and it writes p.Status as the
	// status code; the body and the media type are its own to decide. A
	// header the caller set before asking for the response is on w already
	// and stays.
	RespondError(w nethttp.ResponseWriter, r *nethttp.Request, p Problem)
}

// problemMediaType is the media type of the default response.
const problemMediaType = "application/problem+json"

// Fail writes p as an error response and ends the chain. It is what a layer or
// a handler calls to refuse or fail a request: a response header the caller
// needs on the response — WWW-Authenticate on a 401, Retry-After on a 429 — is
// set before the call and stays.
//
// The shape is the default one, or the delivered ErrorResponder's when the
// request context carries one. Once the response has started to go out, Fail
// writes nothing: the status and the body already on the wire are not
// rewritten, and only the chain is ended.
func Fail(c *gin.Context, p Problem) {
	c.Abort()
	if c.Writer.Written() {
		return
	}
	// The step-up answer shares its status with a refused credential, and a
	// layer that charges a source by outcome has to tell them apart. Every
	// writer of the answer writes this type, so it is recognised here rather
	// than at each of them.
	if p.Status == nethttp.StatusUnauthorized && p.Type == TypeStepUp {
		MarkStepUp(c)
	}
	respondProblem(c.Writer, c.Request, p, responderFrom(c.Request.Context()))
}

// respondProblem writes p through the responder, or in the default shape when
// no module delivers one.
func respondProblem(w nethttp.ResponseWriter, r *nethttp.Request, p Problem, responder ErrorResponder) {
	if responder != nil {
		responder.RespondError(w, r, p)
		return
	}
	writeDefaultProblem(w, r, p)
}

// problemBody is the default response's JSON object. The member names are the
// ones RFC 9457 defines.
type problemBody struct {
	Type     string `json:"type"`
	Title    string `json:"title,omitempty"`
	Status   int    `json:"status"`
	Detail   string `json:"detail,omitempty"`
	Instance string `json:"instance,omitempty"`
}

// writeDefaultProblem writes the default shape: p.Status as the status code,
// and a problem details object as the body. Cause stays out of it.
//
// The body is written here, so a Content-Length or Content-Encoding the caller
// set describes some other body: a handler that announced its own length and
// then panicked would otherwise leave the client waiting for bytes this
// response never sends. They are dropped; every other header the caller set
// stays. The language the body was written in is named in Content-Language when
// a resolver is assembled; see below.
func writeDefaultProblem(w nethttp.ResponseWriter, r *nethttp.Request, p Problem) {
	w.Header().Del("Content-Length")
	w.Header().Del("Content-Encoding")
	// Name the language the body is rendered in. The locale is the one resolved
	// for the request — the request's own when one was settled, the run's
	// configured default otherwise — so a client that asked for one language and
	// was answered in another learns which it got. With no resolver assembled
	// nothing is named, which is the header state an assembly that settles no
	// language has always written.
	if resolver := resolverFrom(r.Context()); resolver != nil {
		if locale := resolver.LocaleOf(r.Context()); locale != "" {
			w.Header().Set("Content-Language", string(locale))
		}
	}
	// A final status is 200 through 599. A 1xx code written alone is an
	// informational response that net/http follows with a 200, so a problem
	// carrying one would say 103 in its body to a client that was answered 200.
	status := p.Status
	if status < nethttp.StatusOK || status > 599 {
		status = nethttp.StatusInternalServerError
	}
	body := problemBody{Type: p.Type, Title: p.Title, Status: status, Detail: p.Detail, Instance: p.Instance}
	if body.Type == "" {
		body.Type = "about:blank"
	}
	if body.Title == "" {
		body.Title = nethttp.StatusText(status)
	}
	// The members are strings and an integer, which encoding/json always
	// marshals.
	encoded, _ := json.Marshal(body)
	w.Header().Set("Content-Type", problemMediaType)
	w.WriteHeader(status)
	_, _ = w.Write(encoded)
}

// responderKey is the context key the base layer puts the delivered
// ErrorResponder under, so that Fail can reach it from a request.
type responderKey struct{}

// withResponder returns ctx carrying responder, or ctx itself when no module
// delivers one: an absent responder is the default shape, which Fail falls back
// to on its own.
func withResponder(ctx context.Context, responder ErrorResponder) context.Context {
	if responder == nil {
		return ctx
	}
	return context.WithValue(ctx, responderKey{}, responder)
}

// responderFrom returns the ErrorResponder the request context carries, or nil
// when it carries none.
func responderFrom(ctx context.Context) ErrorResponder {
	responder, _ := ctx.Value(responderKey{}).(ErrorResponder)
	return responder
}

// resolverKey is the context key the base layer puts the delivered
// i18n.Resolver under, so that the default shape can name the language it wrote
// the body in.
type resolverKey struct{}

// withResolver returns ctx carrying resolver, or ctx itself when no module
// delivers one: with no resolver the default shape writes no Content-Language,
// which is what an assembly that settles no language has always written.
func withResolver(ctx context.Context, resolver i18n.Resolver) context.Context {
	if resolver == nil {
		return ctx
	}
	return context.WithValue(ctx, resolverKey{}, resolver)
}

// resolverFrom returns the i18n.Resolver the request context carries, or nil
// when it carries none.
func resolverFrom(ctx context.Context) i18n.Resolver {
	resolver, _ := ctx.Value(resolverKey{}).(i18n.Resolver)
	return resolver
}
