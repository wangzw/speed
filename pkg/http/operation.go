package http

import (
	"context"
	"fmt"
	"reflect"

	"github.com/gin-gonic/gin"
)

// Operations is the resource type a module declares its API operations as.
type Operations []Declared

// Declared is one operation as the generator reads it: the descriptor, without the
// handler's type parameters.
type Declared interface {
	// Descriptor is the operation's declared shape.
	Descriptor() OperationDescriptor
}

// OperationDescriptor is one operation's declared shape, as the generator reads it.
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
	// Input is the handler's input type, or nil when it takes none.
	Input reflect.Type
	// Request is the declared request body, or nil when the operation takes none.
	Request *RequestBody
	// SuccessStatus is the status of the one 2xx response.
	SuccessStatus int
	// SuccessBody is the success response's body type.
	SuccessBody reflect.Type
	// Simple is true for the simple form. Only its success response writes the
	// declared headers: the handler cannot build the other answers, which are
	// problem documents written without them. A reply operation sets it false,
	// because its handler builds each declared answer and that answer's
	// declared headers are written on whatever status it names.
	Simple bool
	// Access is the level of caller identity the route admits.
	Access Access
	// Responses is the closed set of answers, the success one included.
	Responses []Response
}

// Operation is one operation a module serves, bound to its handler's types: In is
// the handler's input and Out its success body.
type Operation[In, Out any] struct {
	// ID is the operation's identifier in the generated document.
	ID string
	// Method is the HTTP method the route answers.
	Method string
	// Path is the route's path relative to the API root.
	Path string
	// Summary is the one-line summary published for the operation.
	Summary string
	// Description is the longer description published for the operation.
	Description string
	// Request is the declared request body, or nil when the operation takes no body.
	Request *RequestBody
	// Access is the level of caller identity the route admits. Its zero value
	// admits only an identified caller.
	Access Access
	// Responses is the closed set of answers. Exactly one must be 2xx, and
	// that one's body is Out.
	Responses []Response
}

// RequestBody is one operation's declared request body: the media type the entry point
// binds its input's body fields by, the description the contract publishes for the
// body, and whether a request must carry one.
type RequestBody struct {
	// MediaType is the media type the body is read and published with.
	MediaType string
	// Description explains the body to a client.
	Description string
	// Required reports whether a request must carry a body.
	Required bool
	// Name publishes the body's schema as a named component of the fragment under this
	// name, referenced by the operation.
	Name string
}

// NewOperation builds an operation declaration. Its handler is bound later with Handle;
// the declaration itself is a static resource the generator reads.
func NewOperation[In, Out any](
	id, method, path, summary, description string,
	request *RequestBody,
	responses []Response,
	opts ...OperationOption,
) Operation[In, Out] {
	options := resolveOptions(opts)
	return Operation[In, Out]{
		ID:          id,
		Method:      method,
		Path:        path,
		Summary:     summary,
		Description: description,
		Request:     request,
		Access:      options.access,
		Responses:   responses,
	}
}

// OperationOption sets one thing about an operation beyond its shape: the access level
// its route admits.
type OperationOption func(*operationOptions)

func resolveOptions(opts []OperationOption) operationOptions {
	var options operationOptions
	for _, option := range opts {
		option(&options)
	}
	if !options.access.Valid() {
		panic(fmt.Sprintf("http: the operation declares the access level %d, which is not one of the declared levels", options.access))
	}
	return options
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
	// An empty Out is still a body: the entry point answers every success status but a no-
	// body one with a JSON body, which for an empty Out is {}.
	if body == nil && status != 0 && !noBodyStatus(status) {
		body = outType[Out]()
	}
	return OperationDescriptor{
		ID:            o.ID,
		Method:        o.Method,
		Path:          o.Path,
		Summary:       o.Summary,
		Description:   o.Description,
		Input:         inputType[In](),
		Request:       o.Request,
		SuccessStatus: status,
		SuccessBody:   body,
		Simple:        true,
		Access:        o.Access,
		Responses:     o.Responses,
	}
}

// Handle binds a handler to the declaration and returns the entry point's handler.
func (o Operation[In, Out]) Handle(handle func(context.Context, In) (Out, error)) gin.HandlerFunc {
	return bind(handle, o.ID, inputType[In](), o.Request, o.Responses)
}

// ReplyOperation is one operation whose handler builds its own answer from the reply
// constructors.
type ReplyOperation[In any] struct {
	// ID is the operation's identifier in the generated document.
	ID string
	// Method is the HTTP method the route answers.
	Method string
	// Path is the route's path relative to the API root.
	Path string
	// Summary is the one-line summary published for the operation.
	Summary string
	// Description is the longer description published for the operation.
	Description string
	// Request is the declared request body, or nil when the operation takes no body.
	Request *RequestBody
	// Access is the level of caller identity the route admits. Its zero value
	// admits only an identified caller.
	Access Access
	// Responses is the closed set of answers.
	Responses []Response
}

// NewReplyOperation builds a reply operation declaration. Its handler is bound later
// with Handle; the declaration itself is a static resource the generator reads.
func NewReplyOperation[In any](
	id, method, path, summary, description string,
	request *RequestBody,
	responses []Response,
	opts ...OperationOption,
) ReplyOperation[In] {
	options := resolveOptions(opts)
	return ReplyOperation[In]{
		ID:          id,
		Method:      method,
		Path:        path,
		Summary:     summary,
		Description: description,
		Request:     request,
		Access:      options.access,
		Responses:   responses,
	}
}

// Descriptor returns the operation's shape for the generator. The success body is nil:
// a reply operation's body is whatever its handler builds, and each response's own
// Structure describes it.
func (o ReplyOperation[In]) Descriptor() OperationDescriptor {
	return OperationDescriptor{
		ID:            o.ID,
		Method:        o.Method,
		Path:          o.Path,
		Summary:       o.Summary,
		Description:   o.Description,
		Input:         inputType[In](),
		Request:       o.Request,
		SuccessStatus: successStatus(o.Responses),
		SuccessBody:   nil,
		Simple:        false,
		Access:        o.Access,
		Responses:     o.Responses,
	}
}

// Handle binds a handler to the declaration and returns the entry point's handler.
func (o ReplyOperation[In]) Handle(handle func(context.Context, In, *Meta) (Reply, error)) gin.HandlerFunc {
	return replyBind(handle, o.ID, inputType[In](), o.Request, o.Responses)
}

func inputType[In any]() reflect.Type {
	var zero In
	t := reflect.TypeOf(&zero).Elem()
	if t.Kind() == reflect.Struct && t.NumField() == 0 {
		return nil
	}
	return t
}

func bodyType[Out any]() reflect.Type {
	t := outType[Out]()
	if t.Kind() == reflect.Struct && t.NumField() == 0 {
		return nil
	}
	return t
}

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

// Body is one message body: the media type and, for a declared answer that is not the
// handler's success type, the Go type describing its shape.
type Body struct {
	// MediaType is the content type the body is written with.
	MediaType string
	// Structure names the Go type of the body.
	Structure reflect.Type
}

// Response is one answer an operation writes.
type Response struct {
	// Status is the HTTP status code.
	Status int
	// Description explains the answer to a client.
	Description string
	// Headers are the answer's headers.
	Headers []Header
	// Body is the answer body; its Structure is nil for the success response, whose body
	// is the operation's Out type.
	Body *Body
	// ProblemType is the RFC 9457 problem type the entry point writes when it answers this
	// status itself, before the handler runs: a request-binding refusal.
	ProblemType string
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

// ProblemDocument is the shape of the RFC 9457 problem document the entry point writes
// when a handler fails.
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
