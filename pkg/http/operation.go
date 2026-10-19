package http

import (
	"context"
	"fmt"
	"reflect"

	"github.com/gin-gonic/gin"
)

// Operations is the resource type a module declares its API operations as. A
// reader turns the declarations into the module's OpenAPI fragment; the
// fragment is a build product and is never written by hand.
type Operations []Declared

// Declared is one operation as the generator reads it: the descriptor, without
// the handler's type parameters.
type Declared interface {
	// Descriptor is the operation's declared shape.
	Descriptor() OperationDescriptor
}

// OperationDescriptor is one operation's declared shape, as the generator
// reads it.
type OperationDescriptor struct {
	// ID is the OpenAPI operationId.
	ID string
	// Method is the HTTP method.
	Method string
	// Path is the route, relative to the API root, in gin's form.
	Path string
	// Summary is the one-line description.
	Summary string
	// Description explains the operation's behaviour.
	Description string
	// Input is the handler's input type, or nil when it takes none. Its fields
	// carry the parameters through `path`, `query`, `header` and `cookie`
	// tags, and the request body according to RequestMediaType: `json` tags
	// under application/json, `form` tags under
	// application/x-www-form-urlencoded, `form` and `file` tags under
	// multipart/form-data, and the single `raw` tag under
	// application/octet-stream.
	Input reflect.Type
	// RequestMediaType is the request body's media type, empty when the
	// operation takes no body.
	RequestMediaType string
	// SuccessStatus is the status of the one 2xx response.
	SuccessStatus int
	// SuccessBody is the success response's body type. It is nil when the
	// success status carries no body; an empty struct is a body too, which the
	// entry point writes as the JSON object {}.
	SuccessBody reflect.Type
	// Simple is true for the simple form. Only its success response writes the
	// declared headers: the handler cannot build the other answers, which are
	// problem documents written without them. A reply operation sets it false,
	// because its handler builds each declared answer and that answer's
	// declared headers are written on whatever status it names.
	Simple bool
	// Responses is the closed set of answers, the success one included.
	Responses []Response
}

// Operation is one operation a module serves, bound to its handler's types.
//
// In is the handler's input: a struct whose fields carry the operation's
// parameters and request body, tagged the way encoding/json and the schema
// reader read them. Out is the success response's body. The entry point binds
// In from the request and writes Out as the declared success response, so a
// handler has no way to read a parameter the declaration does not carry, and
// no way to answer a body the declaration does not describe.
type Operation[In, Out any] struct {
	ID          string
	Method      string
	Path        string
	Summary     string
	Description string
	// RequestMediaType is the request body's media type; empty when the
	// operation takes no body. The entry point binds In's body fields
	// according to this media type: `json` tags for application/json, `form`
	// tags for application/x-www-form-urlencoded, `form` and `file` tags for
	// multipart/form-data, and the single `raw` tag for
	// application/octet-stream. An input carrying a body tag under an empty
	// media type is a declaration defect and panics when the handler is built.
	RequestMediaType string
	// Responses is the closed set of answers. Exactly one must be 2xx, and
	// that one's body is Out.
	Responses []Response
}

// NewOperation builds an operation declaration. Its handler is bound later
// with Handle; the declaration itself is a static resource the generator reads.
func NewOperation[In, Out any](
	id, method, path, summary, description, requestMediaType string,
	responses []Response,
) Operation[In, Out] {
	return Operation[In, Out]{
		ID:               id,
		Method:           method,
		Path:             path,
		Summary:          summary,
		Description:      description,
		RequestMediaType: requestMediaType,
		Responses:        responses,
	}
}

// Descriptor returns the operation's shape for the generator.
func (o Operation[In, Out]) Descriptor() OperationDescriptor {
	status := successStatus(o.Responses)
	body := bodyType[Out]()
	if noBodyStatus(status) && body != nil {
		panic(fmt.Sprintf(
			"http: the success response %d carries no body, but the handler returns one; use an empty Out type",
			status,
		))
	}
	// An empty Out is still a body: the entry point answers every success
	// status but a no-body one with a JSON body, which for an empty Out is {}.
	// The descriptor carries the empty struct's type so a reader publishes that
	// body rather than reading the success response as body-less.
	if body == nil && status != 0 && !noBodyStatus(status) {
		body = outType[Out]()
	}
	return OperationDescriptor{
		ID:               o.ID,
		Method:           o.Method,
		Path:             o.Path,
		Summary:          o.Summary,
		Description:      o.Description,
		Input:            inputType[In](),
		RequestMediaType: o.RequestMediaType,
		SuccessStatus:    status,
		SuccessBody:      body,
		Simple:           true,
		Responses:        o.Responses,
	}
}

// Handle binds a handler to the declaration and returns the entry point's
// handler. The handler receives the declared input type and returns the
// declared success type, so any other signature does not compile here.
func (o Operation[In, Out]) Handle(handle func(context.Context, In) (Out, error)) gin.HandlerFunc {
	return bind(handle, inputType[In](), o.RequestMediaType, o.Responses)
}

// ReplyOperation is one operation whose handler builds its own answer from the
// reply constructors. It declares the same shape as Operation, but the answer's
// status, media type and headers are checked against the declaration when the
// handler returns, rather than fixed by a type parameter.
//
// In is the handler's input, bound the way Operation binds it. The handler also
// receives a read-only Meta snapshot of the request, and returns a Reply built
// by JSONReply, EmptyReply, BytesReply, StreamReply, SSEReply or RedirectReply,
// optionally wrapped by WithHeaders and WithCookies.
type ReplyOperation[In any] struct {
	ID          string
	Method      string
	Path        string
	Summary     string
	Description string
	// RequestMediaType is the request body's media type; empty when the
	// operation takes no body.
	RequestMediaType string
	// Responses is the closed set of answers. The handler may answer any status
	// it declares, and each reply is checked against that status's declaration.
	Responses []Response
}

// NewReplyOperation builds a reply operation declaration. Its handler is bound
// later with Handle; the declaration itself is a static resource the generator
// reads.
func NewReplyOperation[In any](
	id, method, path, summary, description, requestMediaType string,
	responses []Response,
) ReplyOperation[In] {
	return ReplyOperation[In]{
		ID:               id,
		Method:           method,
		Path:             path,
		Summary:          summary,
		Description:      description,
		RequestMediaType: requestMediaType,
		Responses:        responses,
	}
}

// Descriptor returns the operation's shape for the generator. The success body
// is nil: a reply operation's body is whatever its handler builds, and each
// response's own Structure describes it.
func (o ReplyOperation[In]) Descriptor() OperationDescriptor {
	return OperationDescriptor{
		ID:               o.ID,
		Method:           o.Method,
		Path:             o.Path,
		Summary:          o.Summary,
		Description:      o.Description,
		Input:            inputType[In](),
		RequestMediaType: o.RequestMediaType,
		SuccessStatus:    successStatus(o.Responses),
		SuccessBody:      nil,
		Simple:           false,
		Responses:        o.Responses,
	}
}

// Handle binds a handler to the declaration and returns the entry point's
// handler. The handler receives the declared input and the request's Meta, and
// returns the reply it wants written.
func (o ReplyOperation[In]) Handle(handle func(context.Context, In, *Meta) (Reply, error)) gin.HandlerFunc {
	return replyBind(handle, o.ID, inputType[In](), o.RequestMediaType, o.Responses)
}

// inputType is In's type, or nil for an empty struct: an operation whose
// handler takes nothing declares no input.
func inputType[In any]() reflect.Type {
	var zero In
	t := reflect.TypeOf(&zero).Elem()
	if t.Kind() == reflect.Struct && t.NumField() == 0 {
		return nil
	}
	return t
}

// bodyType is Out's type, or nil when Out is an empty struct: a 204 operation
// answers no body.
func bodyType[Out any]() reflect.Type {
	t := outType[Out]()
	if t.Kind() == reflect.Struct && t.NumField() == 0 {
		return nil
	}
	return t
}

// outType is Out's type, including the empty struct that bodyType reads as no
// body.
func outType[Out any]() reflect.Type {
	var zero Out
	return reflect.TypeOf(&zero).Elem()
}

// successStatus is the status of the single 2xx response, or 0 when the
// declaration does not carry exactly one.
func successStatus(responses []Response) int {
	status := 0
	for _, response := range responses {
		if response.Status >= 200 && response.Status < 300 {
			if status != 0 {
				return 0
			}
			status = response.Status
		}
	}
	return status
}

// Body is one message body: the media type and, for a declared answer that is
// not the handler's success type, the Go type describing its shape.
type Body struct {
	// MediaType is the content type the body is written with.
	MediaType string
	// Structure names the Go type of the body. The success response leaves it
	// nil: its body is the operation's Out type.
	Structure reflect.Type
}

// Response is one answer an operation writes.
type Response struct {
	// Status is the HTTP status code.
	Status int
	// Description explains the answer to a client.
	Description string
	// Headers are the answer's headers. The entry point writes each Value on
	// the success response.
	Headers []Header
	// Body is the answer body; its Structure is nil for the success response,
	// whose body is the operation's Out type.
	Body *Body
}

// Header is one response header.
type Header struct {
	// Name is the header field name.
	Name string
	// Description explains the header to a client.
	Description string
	// Value is the value the entry point writes on the success response.
	Value string
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
