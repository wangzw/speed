package http

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	nethttp "net/http"
	"reflect"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/vislake/speed/pkg/log"
)

// Meta is what an operation's handler knows about the request it is answering,
// apart from its typed input: the caller's address and the route template the
// request matched. It is a snapshot taken when the handler is called.
type Meta struct {
	// ClientIP is the client's address as the endpoint resolved it.
	ClientIP string
	// Route is the route template the request matched, in gin's form.
	Route string
}

// Reply is one answer a handler builds instead of returning a typed body. Its
// set of implementations is closed: only the constructors in this package
// return one, because the marker method is unexported.
type Reply interface {
	isReply()
}

// replyKind is which answer a Reply builds.
type replyKind int

const (
	replyKindJSON replyKind = iota
	replyKindEmpty
	replyKindBytes
	replyKindStream
	replyKindSSE
	replyKindRedirect
)

// replyValue is the one implementation of Reply. Its zero fields are the ones
// the kind does not use.
type replyValue struct {
	kind      replyKind
	status    int
	mediaType string
	body      any
	data      []byte
	stream    func(context.Context, io.Writer) error
	events    func(*SSEWriter) error
	location  string
	headers   []HeaderValue
	cookies   []*nethttp.Cookie
}

func (*replyValue) isReply() {}

// JSONReply answers status with body encoded as JSON.
func JSONReply[T any](status int, body T) Reply {
	return &replyValue{kind: replyKindJSON, status: status, mediaType: requestMediaTypeJSON, body: body}
}

// EmptyReply answers status with no body.
func EmptyReply(status int) Reply {
	return &replyValue{kind: replyKindEmpty, status: status}
}

// BytesReply answers status with body as raw bytes of the given media type.
func BytesReply(status int, mediaType string, body []byte) Reply {
	return &replyValue{kind: replyKindBytes, status: status, mediaType: mediaType, data: body}
}

// StreamReply answers status by calling write with the response writer. The
// status and headers are committed before write runs, so an error from write
// cannot be turned into a different answer.
func StreamReply(status int, mediaType string, write func(context.Context, io.Writer) error) Reply {
	return &replyValue{kind: replyKindStream, status: status, mediaType: mediaType, stream: write}
}

// SSEReply answers status with a text/event-stream body produced by events.
func SSEReply(status int, events func(*SSEWriter) error) Reply {
	return &replyValue{kind: replyKindSSE, status: status, mediaType: "text/event-stream", events: events}
}

// RedirectReply answers status with the Location header set to location and no
// body.
func RedirectReply(status int, location string) Reply {
	return &replyValue{kind: replyKindRedirect, status: status, location: location}
}

// HeaderValue is one dynamic response header.
type HeaderValue struct {
	// Name is the header field name.
	Name string
	// Value is the value written for it.
	Value string
}

// WithHeaders adds dynamic headers to a reply. Every name must be declared on
// the response the reply's status selects; an undeclared one is a 500.
func WithHeaders(reply Reply, headers ...HeaderValue) Reply {
	value := replyValueOf(reply)
	if value == nil {
		return reply
	}
	copied := *value
	copied.headers = append(append([]HeaderValue(nil), value.headers...), headers...)
	return &copied
}

// WithCookies adds Set-Cookie headers to a reply. Cookies are not part of the
// declared header set and are always written.
func WithCookies(reply Reply, cookies ...*nethttp.Cookie) Reply {
	value := replyValueOf(reply)
	if value == nil {
		return reply
	}
	copied := *value
	copied.cookies = append(append([]*nethttp.Cookie(nil), value.cookies...), cookies...)
	return &copied
}

// replyValueOf reads the concrete reply behind the interface, or nil when the
// value is not one this package built.
func replyValueOf(reply Reply) *replyValue {
	value, _ := reply.(*replyValue)
	return value
}

// SSEWriter writes one server-sent event at a time and flushes it, so each
// event reaches the client before the next one is built.
type SSEWriter struct {
	writer io.Writer
	flush  func()
}

// Send writes one event and flushes it. An empty event name writes only the
// data field; each line of data gets its own data field.
func (w *SSEWriter) Send(event string, data []byte) error {
	var payload strings.Builder
	if event != "" {
		payload.WriteString("event: ")
		payload.WriteString(event)
		payload.WriteByte('\n')
	}
	for _, line := range splitEventData(data) {
		payload.WriteString("data: ")
		payload.WriteString(line)
		payload.WriteByte('\n')
	}
	payload.WriteByte('\n')
	if _, err := io.WriteString(w.writer, payload.String()); err != nil {
		return err
	}
	if w.flush != nil {
		w.flush()
	}
	return nil
}

// splitEventData splits one event's data into the lines that each get a data
// field. SSE ends a line with CRLF, LF or CR, so a bare CR must start a new
// line: a client reading the frame would otherwise treat everything after it
// as an unknown field and drop it.
func splitEventData(data []byte) []string {
	text := strings.ReplaceAll(string(data), "\r\n", "\n")
	text = strings.ReplaceAll(text, "\r", "\n")
	return strings.Split(text, "\n")
}

// replyBind builds the entry point's handler for one reply operation: it binds
// the declared input, calls the handler with the request's Meta, then writes
// the reply the declaration permits.
func replyBind[In any](
	handle func(context.Context, In, *Meta) (Reply, error),
	operationID string,
	input reflect.Type,
	requestMediaType string,
	responses []Response,
) gin.HandlerFunc {
	checkResponseBodies(responses)
	binding := newRequestBinding(input, requestMediaType)
	return func(c *gin.Context) {
		c.Set(declaredFailuresContextKey, declaredStatuses(responses))
		in, err := bindInput[In](c, binding)
		if err != nil {
			refuseBinding(c, err)
			return
		}
		meta := &Meta{ClientIP: c.ClientIP(), Route: c.FullPath()}
		reply, err := handle(c.Request.Context(), in, meta)
		if err != nil {
			writeFailure(c, responses, err)
			return
		}
		writeReply(c, operationID, responses, reply)
	}
}

// writeReply checks a reply against the operation's declaration and writes it.
// A reply that does not match the declaration is answered as an internal error
// and its own answer is not written at all.
func writeReply(c *gin.Context, operationID string, responses []Response, reply Reply) {
	value := replyValueOf(reply)
	if value == nil {
		replyDefect(c, operationID, "the handler returned no reply")
		return
	}
	response, declared := responseOf(responses, value.status)
	if !declared {
		replyDefect(c, operationID, fmt.Sprintf("the status %d is not declared", value.status))
		return
	}
	if reason := checkReplyDeclaration(value, response); reason != "" {
		replyDefect(c, operationID, reason)
		return
	}
	if reason := checkDynamicHeaders(value.headers, response); reason != "" {
		replyDefect(c, operationID, reason)
		return
	}
	writeReplyValue(c, operationID, response, value)
}

// checkReplyDeclaration compares a reply kind with the declared response it
// answers, and returns why they do not match, or "".
func checkReplyDeclaration(value *replyValue, response Response) string {
	switch value.kind {
	case replyKindJSON:
		// A 204 or 304 response carries no body, so the encoded body would be
		// dropped by the transport; answering it is a defect, not a write.
		if noBodyStatus(value.status) {
			return fmt.Sprintf("a JSON reply is declared against the status %d, which carries no body", value.status)
		}
		if response.Body == nil {
			return "a JSON reply is declared against a response with no body"
		}
		if normaliseMediaType(response.Body.MediaType) != requestMediaTypeJSON {
			return fmt.Sprintf("a JSON reply is declared against the media type %q", response.Body.MediaType)
		}
		if response.Body.Structure != nil {
			body := reflect.TypeOf(value.body)
			if body != response.Body.Structure {
				return fmt.Sprintf("the reply body is %v and the declared structure is %v", body, response.Body.Structure)
			}
		}
	case replyKindEmpty:
		if response.Body != nil {
			return "an empty reply is declared against a response with a body"
		}
	case replyKindBytes:
		return checkBodyMediaType(value, response)
	case replyKindStream:
		if value.stream == nil {
			return "a stream reply carries no write callback"
		}
		return checkBodyMediaType(value, response)
	case replyKindSSE:
		if value.events == nil {
			return "an event stream reply carries no event callback"
		}
		if response.Body == nil {
			return "an event stream reply is declared against a response with no body"
		}
		if normaliseMediaType(response.Body.MediaType) != "text/event-stream" {
			return fmt.Sprintf("an event stream reply is declared against the media type %q", response.Body.MediaType)
		}
	case replyKindRedirect:
		if response.Body != nil {
			return "a redirect reply is declared against a response with a body"
		}
		if response.Status < 300 || response.Status >= 400 {
			return fmt.Sprintf("a redirect reply is declared against the status %d", response.Status)
		}
		if !responseDeclaresHeader(response, "Location") {
			return "a redirect reply is declared against a response with no Location header"
		}
	}
	return ""
}

// checkBodyMediaType compares a bytes or stream reply's media type with the one
// the declared response carries.
func checkBodyMediaType(value *replyValue, response Response) string {
	if response.Body == nil {
		return "a body reply is declared against a response with no body"
	}
	if normaliseMediaType(response.Body.MediaType) != normaliseMediaType(value.mediaType) {
		return fmt.Sprintf("the reply media type is %q and the declared media type is %q",
			value.mediaType, response.Body.MediaType)
	}
	return ""
}

// checkDynamicHeaders requires every dynamic header's name to be declared on
// the response being written.
func checkDynamicHeaders(headers []HeaderValue, response Response) string {
	for _, header := range headers {
		if !responseDeclaresHeader(response, header.Name) {
			return fmt.Sprintf("the header %q is not declared on the response", header.Name)
		}
	}
	return ""
}

// responseDeclaresHeader reports whether the response declares a header name.
func responseDeclaresHeader(response Response, name string) bool {
	for _, header := range response.Headers {
		if strings.EqualFold(header.Name, name) {
			return true
		}
	}
	return false
}

// replyDefect records and answers a reply that did not match its declaration.
func replyDefect(c *gin.Context, operationID, reason string) {
	log.FromContext(c.Request.Context()).Error(
		"a handler's reply did not match its operation declaration",
		"operation", operationID,
		"reason", reason,
	)
	Fail(c, Problem{Status: nethttp.StatusInternalServerError, Cause: errors.New(reason)})
}

// writeReplyValue writes one checked reply: the declared static headers, then
// the dynamic ones, then the body.
func writeReplyValue(c *gin.Context, operationID string, response Response, value *replyValue) {
	// A JSON body is encoded before anything is written, so a body that cannot
	// be encoded is answered as the internal error it is rather than after the
	// status has gone out.
	var encoded []byte
	if value.kind == replyKindJSON {
		var err error
		encoded, err = json.Marshal(value.body)
		if err != nil {
			replyDefect(c, operationID, "the JSON reply body could not be encoded: "+err.Error())
			return
		}
	}
	for _, header := range response.Headers {
		c.Header(header.Name, header.Value)
	}
	// An event stream is not cached: the stream's own headers are flushed as
	// they are produced, and a cache holding the first one would go on serving
	// it. A declared Cache-Control stands; this only supplies the default.
	if value.kind == replyKindSSE && c.Writer.Header().Get("Cache-Control") == "" {
		c.Header("Cache-Control", "no-store")
	}
	for _, header := range value.headers {
		c.Header(header.Name, header.Value)
	}
	for _, cookie := range value.cookies {
		nethttp.SetCookie(c.Writer, cookie)
	}
	switch value.kind {
	case replyKindJSON:
		c.Data(value.status, declaredMediaType(response, requestMediaTypeJSON), encoded)
	case replyKindEmpty:
		c.Status(value.status)
		c.Writer.WriteHeaderNow()
	case replyKindBytes:
		c.Data(value.status, value.mediaType, value.data)
	case replyKindStream:
		c.Header("Content-Type", value.mediaType)
		c.Status(value.status)
		c.Writer.WriteHeaderNow()
		if err := value.stream(c.Request.Context(), c.Writer); err != nil {
			streamFailed(c, err)
		}
	case replyKindSSE:
		c.Header("Content-Type", declaredMediaType(response, "text/event-stream"))
		c.Status(value.status)
		c.Writer.WriteHeaderNow()
		writer := &SSEWriter{writer: c.Writer, flush: c.Writer.Flush}
		if err := value.events(writer); err != nil {
			streamFailed(c, err)
		}
	case replyKindRedirect:
		c.Header("Location", value.location)
		c.Status(value.status)
		c.Writer.WriteHeaderNow()
	}
}

// declaredMediaType is the media type the matched response declares, or the
// fallback when the declaration leaves it out.
func declaredMediaType(response Response, fallback string) string {
	if response.Body != nil && response.Body.MediaType != "" {
		return response.Body.MediaType
	}
	return fallback
}

// streamFailed ends a reply whose body had already started to go out. The
// status cannot change any more, so the failure is recorded and the connection
// is abandoned; the recovery layer lets http.ErrAbortHandler past for exactly
// this reason.
func streamFailed(c *gin.Context, err error) {
	_ = c.Error(err)
	log.FromContext(c.Request.Context()).Error(
		"a streamed reply failed after its response had started",
		"error", err,
	)
	c.Abort()
	panic(nethttp.ErrAbortHandler)
}
