package http

import (
	"context"
	"encoding/json"
	nethttp "net/http"

	"github.com/gin-gonic/gin"

	"github.com/vislake/speed/pkg/i18n"
)

// Problem is the content of one error response. Its fields are the ones RFC 9457 gives
// a problem details object.
type Problem struct {
	// Type identifies the kind of problem; it defaults to "about:blank".
	Type string
	// Title is a short summary of the kind of problem; it defaults to the standard text of
	// Status.
	Title string
	// Status is the status code the response is written with.
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

// ErrorResponder decides the shape of every error response this module writes: the fallback's answer to a panic, the answer to a request that matches no route and to one whose method the matched path does not allow, and the responses written through Fail.
type ErrorResponder interface {
	// RespondError writes the response for p.
	RespondError(w nethttp.ResponseWriter, r *nethttp.Request, p Problem)
}

const problemMediaType = "application/problem+json"

// Fail writes p as an error response and ends the chain. A response header the caller needs on the response, such as WWW-Authenticate on a 401 or Retry-After on a 429, is set before the call and stays; once the response has started, Fail only ends the chain.
func Fail(c *gin.Context, p Problem) {
	c.Abort()
	if c.Writer.Written() {
		return
	}
	// The step-up answer shares its status with a refused credential, and a layer that charges a source by outcome has to tell them apart. Every writer of the answer writes this type, so it is recognised here rather than at each of them.
	if p.Status == nethttp.StatusUnauthorized && p.Type == TypeStepUp {
		MarkStepUp(c)
	}
	respondProblem(c.Writer, c.Request, p, responderFrom(c.Request.Context()))
}

func respondProblem(w nethttp.ResponseWriter, r *nethttp.Request, p Problem, responder ErrorResponder) {
	if responder != nil {
		responder.RespondError(w, r, p)
		return
	}
	writeDefaultProblem(w, r, p)
}

type problemBody struct {
	Type     string `json:"type"`
	Title    string `json:"title,omitempty"`
	Status   int    `json:"status"`
	Detail   string `json:"detail,omitempty"`
	Instance string `json:"instance,omitempty"`
}

func writeDefaultProblem(w nethttp.ResponseWriter, r *nethttp.Request, p Problem) {
	w.Header().Del("Content-Length")
	w.Header().Del("Content-Encoding")
	// Name the language the body is rendered in: the locale resolved for the request, so a client that asked for one language and was answered in another learns which it got. With no resolver assembled nothing is named, which is the header state an assembly that settles no language has always written.
	if resolver := resolverFrom(r.Context()); resolver != nil {
		if locale := resolver.LocaleOf(r.Context()); locale != "" {
			w.Header().Set("Content-Language", string(locale))
		}
	}
	// A final status is 200 through 599.
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
	encoded, _ := json.Marshal(body)
	w.Header().Set("Content-Type", problemMediaType)
	w.WriteHeader(status)
	_, _ = w.Write(encoded)
}

type responderKey struct{}

func withResponder(ctx context.Context, responder ErrorResponder) context.Context {
	if responder == nil {
		return ctx
	}
	return context.WithValue(ctx, responderKey{}, responder)
}

func responderFrom(ctx context.Context) ErrorResponder {
	responder, _ := ctx.Value(responderKey{}).(ErrorResponder)
	return responder
}

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

func resolverFrom(ctx context.Context) i18n.Resolver {
	resolver, _ := ctx.Value(resolverKey{}).(i18n.Resolver)
	return resolver
}
