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

// Meta is what an operation's handler knows about the request it is answering, apart
// from its typed input: the caller's address, the route template the request matched
// and the request body's declared length.
type Meta struct {
	// ClientIP is the client's address as the endpoint resolved it.
	ClientIP string
	// Route is the route template the request matched, in gin's form.
	Route string
	// ContentLength is the request's Content-Length, or -1 when the length is unknown,
	// which is the value net/http records for a chunked request.
	ContentLength int64
}

// Reply is one answer a handler builds instead of returning a typed body.
type Reply interface {
	isReply()
}

type replyKind int

const (
	replyKindJSON replyKind = iota
	replyKindEmpty
	replyKindBytes
	replyKindStream
	replyKindSSE
	replyKindRedirect
)

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

// StreamReply answers status by calling write with the response writer.
func StreamReply(status int, mediaType string, write func(context.Context, io.Writer) error) Reply {
	return &replyValue{kind: replyKindStream, status: status, mediaType: mediaType, stream: write}
}

// SSEReply answers status with a text/event-stream body produced by events.
func SSEReply(status int, events func(*SSEWriter) error) Reply {
	return &replyValue{kind: replyKindSSE, status: status, mediaType: "text/event-stream", events: events}
}

// RedirectReply answers status with the Location header set to location and no body.
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

// WithHeaders adds dynamic headers to a reply. Every name must be declared on the
// response the reply's status selects; an undeclared one is a 500.
func WithHeaders(reply Reply, headers ...HeaderValue) Reply {
	value := replyValueOf(reply)
	if value == nil {
		return reply
	}
	copied := *value
	copied.headers = append(append([]HeaderValue(nil), value.headers...), headers...)
	return &copied
}

// WithCookies adds Set-Cookie headers to a reply. Cookies are not part of the declared
// header set and are always written.
func WithCookies(reply Reply, cookies ...*nethttp.Cookie) Reply {
	value := replyValueOf(reply)
	if value == nil {
		return reply
	}
	copied := *value
	copied.cookies = append(append([]*nethttp.Cookie(nil), value.cookies...), cookies...)
	return &copied
}

func replyValueOf(reply Reply) *replyValue {
	value, _ := reply.(*replyValue)
	return value
}

const mediaTypeAny = "*/*"

// SSEWriter writes one server-sent event at a time and flushes it, so each event
// reaches the client before the next one is built.
type SSEWriter struct {
	writer io.Writer
	flush  func()
}

// NewSSEWriter returns a writer that sends events to w and flushes each one to flusher.
func NewSSEWriter(writer io.Writer, flusher nethttp.Flusher) *SSEWriter {
	writerOfEvents := &SSEWriter{writer: writer}
	if flusher != nil {
		writerOfEvents.flush = flusher.Flush
	}
	return writerOfEvents
}

// Send writes one event and flushes it. An empty event name writes only the data field;
// each line of data gets its own data field.
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

// splitEventData splits one event's data into the lines that each get a data field. SSE ends a line with CRLF, LF or CR, so a bare CR must start a new line: a client would otherwise treat everything after it as an unknown field and drop it.
func splitEventData(data []byte) []string {
	text := strings.ReplaceAll(string(data), "\r\n", "\n")
	text = strings.ReplaceAll(text, "\r", "\n")
	return strings.Split(text, "\n")
}

func replyBind[In any](
	handle func(context.Context, In, *Meta) (Reply, error),
	operationID string,
	input reflect.Type,
	request *RequestBody,
	responses []Response,
) gin.HandlerFunc {
	checkResponseBodies(responses)
	checkReplyResponses(responses)
	binding := newRequestBinding(input, request)
	return func(c *gin.Context) {
		c.Set(declaredFailuresContextKey, declaredResponses(responses))
		in, err := bindInput[In](c, binding)
		if err != nil {
			refuseBinding(c, responses, err)
			return
		}
		meta := &Meta{
			ClientIP:      c.ClientIP(),
			Route:         c.FullPath(),
			ContentLength: c.Request.ContentLength,
		}
		reply, err := handle(c.Request.Context(), in, meta)
		if err != nil {
			writeFailure(c, operationID, responses, err)
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

func checkReplyDeclaration(value *replyValue, response Response) string {
	switch value.kind {
	case replyKindJSON:
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

func checkBodyMediaType(value *replyValue, response Response) string {
	if response.Body == nil {
		return "a body reply is declared against a response with no body"
	}
	declared := normaliseMediaType(response.Body.MediaType)
	// A dynamic media type accepts whatever bytes or stream reply the handler builds: the
	// declaration leaves the choice to the handler.
	if declared == mediaTypeAny {
		return ""
	}
	if declared == "text/event-stream" {
		return "an event stream is written only with SSEReply"
	}
	if declared != normaliseMediaType(value.mediaType) {
		return fmt.Sprintf("the reply media type is %q and the declared media type is %q",
			value.mediaType, response.Body.MediaType)
	}
	return ""
}

// checkReplyResponses refuses a reply declaration the writer cannot describe: a body with no media type, or a JSON body whose structure is not declared. Either would publish a shape the entry point cannot validate the reply against, so it is refused when the handler is built.
func checkReplyResponses(responses []Response) {
	for _, response := range responses {
		if response.Body == nil {
			continue
		}
		if normaliseMediaType(response.Body.MediaType) == "" {
			panic(fmt.Sprintf("http: the response %d declares a body with no media type", response.Status))
		}
		if isJSONMediaType(response.Body.MediaType) && response.Body.Structure == nil {
			panic(fmt.Sprintf("http: the response %d declares a JSON body with no Structure", response.Status))
		}
	}
}

func isJSONMediaType(mediaType string) bool {
	normalised := normaliseMediaType(mediaType)
	return normalised == "application/json" || strings.HasSuffix(normalised, "+json")
}

func checkDynamicHeaders(headers []HeaderValue, response Response) string {
	for _, header := range headers {
		if !responseDeclaresHeader(response, header.Name) {
			return fmt.Sprintf("the header %q is not declared on the response", header.Name)
		}
	}
	return ""
}

func responseDeclaresHeader(response Response, name string) bool {
	for _, header := range response.Headers {
		if strings.EqualFold(header.Name, name) {
			return true
		}
	}
	return false
}

func replyDefect(c *gin.Context, operationID, reason string) {
	log.FromContext(c.Request.Context()).Error(
		"a handler's reply did not match its operation declaration",
		"operation", operationID,
		"reason", reason,
	)
	Fail(c, Problem{Status: nethttp.StatusInternalServerError, Cause: errors.New(reason)})
}

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
	// An event stream is not cached: the stream's own headers are flushed as they are
	// produced, and a cache holding the first one would go on serving it.
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
		// The status and headers are flushed before the handler builds its
		// first event, so establishing the connection does not wait for one.
		c.Writer.Flush()
		writer := NewSSEWriter(c.Writer, c.Writer)
		if err := value.events(writer); err != nil {
			streamFailed(c, err)
		}
	case replyKindRedirect:
		c.Header("Location", value.location)
		c.Status(value.status)
		c.Writer.WriteHeaderNow()
	}
}

func declaredMediaType(response Response, fallback string) string {
	if response.Body != nil && response.Body.MediaType != "" {
		return response.Body.MediaType
	}
	return fallback
}

// streamFailed ends a reply whose body had already started to go out. The status cannot change any more, so the failure is recorded and the connection is abandoned; the recovery layer lets http.ErrAbortHandler past for exactly this reason.
func streamFailed(c *gin.Context, err error) {
	_ = c.Error(err)
	log.FromContext(c.Request.Context()).Error(
		"a streamed reply failed after its response had started",
		"error", err,
	)
	c.Abort()
	panic(nethttp.ErrAbortHandler)
}
