package http

import "reflect"

// Operations is the resource type a module declares its API operations as. A
// reader turns the declarations into the module's OpenAPI fragment; the
// fragment is a build product and is never written by hand.
//
// An operation's metadata lives beside the handler that serves it, so a route
// added to the entry point without an operation, or an operation without a
// route, is a difference between two declarations the module itself holds.
type Operations []Operation

// Operation is one operation a module serves. Path is relative to the Spec's
// Prefix and Version, the same way fragment paths are.
type Operation struct {
	// ID is the OpenAPI operationId.
	ID string
	// Method is the HTTP method, one of net/http's method constants.
	Method string
	// Path is the route, relative to the API root, written in gin's form:
	// "/vendors/:id". A ":"-parameter becomes an OpenAPI "{id}" parameter.
	Path string
	// Summary is the one-line description of the operation.
	Summary string
	// Description explains the operation's behaviour to a client.
	Description string
	// Input is an optional struct whose fields carry the operation's
	// parameters, tagged `path:"name"`, `query:"name"`, `header:"name"` or
	// `cookie:"name"`. The fields' types and tags carry the schema and its
	// description.
	Input reflect.Type
	// Request is the optional request body.
	Request *Body
	// Responses is the closed set of answers the handler writes.
	Responses []Response
}

// Body is one message body: the media type and the Go type that describes its
// shape. The type's fields carry the JSON schema through their tags, with
// `description` and `required` read as they are by encoding/json and the
// schema reader.
type Body struct {
	// MediaType is the content type the body is written with, such as
	// "application/json" or "application/problem+json".
	MediaType string
	// Structure names the Go type of the body: a struct, or a slice or map
	// of one.
	Structure reflect.Type
}

// Response is one answer an operation writes.
type Response struct {
	// Status is the HTTP status code.
	Status int
	// Description explains the answer to a client.
	Description string
	// Headers is an optional struct whose fields carry response headers,
	// tagged `header:"Name"`.
	Headers reflect.Type
	// Body is the optional answer body.
	Body *Body
}

// ProblemDocument is the shape of the RFC 9457 problem document the entry
// point writes when a handler fails. It is the declared counterpart of
// Problem, which is the type a handler fills in: this one carries the JSON
// shape and the descriptions the contract publishes, the other carries the
// values a module hands the writer.
type ProblemDocument struct {
	_ struct{} `description:"An RFC 9457 problem document. This is the entry point's default error shape, which a host-supplied ErrorResponder may replace."`

	// Type is the machine-readable problem code.
	Type string `json:"type" required:"true" description:"The machine-readable code identifying the kind of problem. A module that sets none gets the RFC 9457 default about:blank; codes this platform defines are URNs of the form urn:speed:<capability>:<code>."`
	// Title is a short summary of the kind of problem.
	Title string `json:"title" description:"A short summary of the kind of problem. A module that sets none gets the standard English status text for the status code."`
	// Status is the HTTP status code.
	Status int `json:"status" required:"true" description:"The HTTP status code the response is written with, repeated from the status line."`
	// Detail explains this occurrence.
	Detail string `json:"detail" description:"A longer explanation of this occurrence, present only when the answering module has one to give. It may be a sentence rendered in the request language or a language-neutral token such as \"forbidden\", so clients must not parse it."`
	// Instance identifies this occurrence.
	Instance string `json:"instance" description:"A URI identifying this occurrence; absent when there is none to give."`
}
