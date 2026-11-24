package http

import (
	"errors"
	"net/http"
	"reflect"
)

// Fault is an error that carries the problem a handler wants answered: its status and
// the problem's type, title and detail.
type Fault struct {
	// Status is the HTTP status the problem is answered with.
	Status int
	// Type is the machine-readable problem code.
	Type string
	// Title is a short summary; empty means the standard status text.
	Title string
	// Detail explains this occurrence.
	Detail string
	// Headers are response headers the answer carries. Each name must be
	// declared on the response the fault's status selects; an undeclared one is
	// a handler defect and the answer is a 500.
	Headers []HeaderValue
}

// Error renders the fault as an error. It never reaches a client: the response body is
// the problem document built from the fields above.
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

func faultOf(err error) (Fault, bool) {
	var value Fault
	if errors.As(err, &value) {
		return value, true
	}
	var pointer *Fault
	if errors.As(err, &pointer) && pointer != nil {
		return *pointer, true
	}
	return Fault{}, false
}

// ErrorShape is the body shape the entry point's error handling writes: the media type
// and the Go type describing it.
type ErrorShape struct {
	// MediaType is the content type the error body is written with.
	MediaType string
	// Structure is the Go type describing the error body.
	Structure reflect.Type
}

// DefaultErrorShape is the problem document shape every module gets without declaring
// one.
func DefaultErrorShape() ErrorShape {
	return ErrorShape{MediaType: problemMediaType, Structure: reflect.TypeOf(ProblemDocument{})}
}

// ErrorResponse declares one error answer in the given shape. An empty shape falls back
// to the default problem document.
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

// ProblemResponse declares one error answer in the default problem shape.
func ProblemResponse(status int, description string) Response {
	return ErrorResponse(DefaultErrorShape(), status, description)
}

// ProblemResponseOf declares one error answer whose request-binding refusals carry the
// given problem type: the type is the one the entry point writes when it refuses the
// request before the handler runs.
func ProblemResponseOf(status int, description, problemType string) Response {
	response := ProblemResponse(status, description)
	response.ProblemType = problemType
	return response
}

// Failures merges an API's common failures with the operation's own: a specific
// response replaces the common one with the same status, and a status only one of the
// two carries is added.
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
