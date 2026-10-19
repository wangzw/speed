package http

import (
	"net/http"
	"reflect"
)

// Fault is an error that carries the problem a handler wants answered: its
// status and the problem's type, title and detail. A handler returns one, or
// panics with one; the entry point writes the status as a problem document, and
// only when the operation declared it.
//
// Type is the machine-readable code, usually a URN such as
// urn:speed:objectstore:not-found. It may be empty (the RFC 9457 default
// about:blank). Title and Detail are the text a client reads; a module that
// answers in the request language fills them, and an empty Title falls back to
// the standard status text.
type Fault struct {
	// Status is the HTTP status the problem is answered with.
	Status int
	// Type is the machine-readable problem code.
	Type string
	// Title is a short summary; empty means the standard status text.
	Title string
	// Detail explains this occurrence.
	Detail string
}

// Error renders the fault as an error. It never reaches a client: the response
// body is the problem document built from the fields above.
func (f Fault) Error() string {
	if f.Detail != "" {
		return f.Detail
	}
	return http.StatusText(f.Status)
}

// StatusCode is the status the fault is answered with.
func (f Fault) StatusCode() int { return f.Status }

// Problem is the document the fault asks for.
func (f Fault) Problem() Problem {
	return Problem{Status: f.Status, Type: f.Type, Title: f.Title, Detail: f.Detail, Cause: f}
}

// ErrorShape is the body shape the entry point's error handling writes: the
// media type and the Go type describing it. The default is the RFC 9457
// problem document. A host that delivers its own ErrorResponder declares the
// shape that responder writes, and the error responses its modules declare
// reference that shape — so the contract describes the answer the process
// actually writes, not the default one it replaced.
type ErrorShape struct {
	// MediaType is the content type the error body is written with.
	MediaType string
	// Structure is the Go type describing the error body.
	Structure reflect.Type
}

// DefaultErrorShape is the problem document shape every module gets without
// declaring one.
func DefaultErrorShape() ErrorShape {
	return ErrorShape{MediaType: problemMediaType, Structure: reflect.TypeOf(ProblemDocument{})}
}

// ErrorResponse declares one error answer in the given shape. An empty shape
// falls back to the default problem document.
func ErrorResponse(shape ErrorShape, status int, description string) Response {
	if shape.Structure == nil {
		shape = DefaultErrorShape()
	}
	return Response{
		Status:      status,
		Description: description,
		Body: &Body{
			MediaType: shape.MediaType,
			Structure: shape.Structure,
		},
	}
}

// ProblemResponse declares one error answer in the default problem shape. It is
// the building block of a module's common failures and of an operation's own.
func ProblemResponse(status int, description string) Response {
	return ErrorResponse(DefaultErrorShape(), status, description)
}

// Failures merges an API's common failures with the operation's own: a
// specific response replaces the common one with the same status, and a status
// only one of the two carries is added. The result is the operation's closed
// response set, and it is what the fragment publishes and what the entry point
// accepts from a handler.
func Failures(common []Response, specific ...Response) []Response {
	merged := append([]Response(nil), common...)
	at := make(map[int]int, len(merged))
	for i, response := range merged {
		at[response.Status] = i
	}
	for _, response := range specific {
		if i, replaced := at[response.Status]; replaced {
			merged[i] = response
			continue
		}
		at[response.Status] = len(merged)
		merged = append(merged, response)
	}
	return merged
}
