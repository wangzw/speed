package http

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

type replyBody struct {
	Name string `json:"name"`
}

func jsonBodyResponse(status int) Response {
	return Response{
		Status:      status,
		Description: "The body",
		Body:        &Body{MediaType: "application/json", Structure: reflect.TypeOf(replyBody{})},
	}
}

func serveReply(
	t *testing.T,
	method, path string,
	handler func(context.Context, struct{}, *Meta) (Reply, error),
	request *http.Request,
) *httptest.ResponseRecorder {
	t.Helper()
	operation := NewReplyOperation[struct{}]("the.module.reply", method, path, "", "", nil, []Response{
		jsonBodyResponse(http.StatusOK),
	})
	return serve(t, method, path, operation.Handle(handler), request)
}

// TestAJSONReplyIsWrittenWhenItsTypeMatches holds the JSON row of the matrix.
func TestAJSONReplyIsWrittenWhenItsTypeMatches(t *testing.T) {
	recorder := serveReply(t, http.MethodGet, "/things",
		func(context.Context, struct{}, *Meta) (Reply, error) {
			return JSONReply(http.StatusOK, replyBody{Name: "alice"}), nil
		},
		httptest.NewRequest(http.MethodGet, "/things", nil))

	if recorder.Code != http.StatusOK {
		t.Fatalf("the operation answered %d, want 200", recorder.Code)
	}
	if mediaType := recorder.Header().Get("Content-Type"); !strings.HasPrefix(mediaType, "application/json") {
		t.Errorf("the answer's Content-Type is %q, want application/json", mediaType)
	}
	if body := strings.TrimSpace(recorder.Body.String()); body != `{"name":"alice"}` {
		t.Errorf("the answer body is %q, want the JSON reply", body)
	}
}

// TestAJSONReplyOfAnotherTypeIsAnsweredInternal holds the body-type check: a reply whose type is
// not the declared structure is never written.
func TestAJSONReplyOfAnotherTypeIsAnsweredInternal(t *testing.T) {
	recorder := serveReply(t, http.MethodGet, "/things",
		func(context.Context, struct{}, *Meta) (Reply, error) {
			return JSONReply(http.StatusOK, struct{ Other string }{Other: "x"}), nil
		},
		httptest.NewRequest(http.MethodGet, "/things", nil))

	if recorder.Code != http.StatusInternalServerError {
		t.Fatalf("a mismatched JSON reply answered %d, want 500", recorder.Code)
	}
	if mediaType := recorder.Header().Get("Content-Type"); !strings.HasPrefix(mediaType, problemMediaType) {
		t.Errorf("the defect is not a problem document: Content-Type is %q", mediaType)
	}
}

// TestAnUndeclaredStatusIsAnsweredInternal holds the status check.
func TestAnUndeclaredStatusIsAnsweredInternal(t *testing.T) {
	operation := NewReplyOperation[struct{}]("the.module.reply", http.MethodGet, "/things", "", "", nil,
		[]Response{{
			Status: http.StatusOK, Description: "The body",
			Body: &Body{MediaType: "application/json", Structure: reflect.TypeOf(replyBody{})},
		}})
	handler := operation.Handle(func(context.Context, struct{}, *Meta) (Reply, error) {
		return JSONReply(http.StatusNotFound, replyBody{Name: "absent"}), nil
	})
	recorder := serve(t, http.MethodGet, "/things", handler, httptest.NewRequest(http.MethodGet, "/things", nil))

	if recorder.Code != http.StatusInternalServerError {
		t.Errorf("an undeclared status answered %d, want 500", recorder.Code)
	}
	if body := recorder.Body.String(); strings.Contains(body, "absent") {
		t.Errorf("the undeclared answer was written: %s", body)
	}
}

// TestAnEmptyReplyWritesNoBody checks that an Empty reply writes no body.
func TestAnEmptyReplyWritesNoBody(t *testing.T) {
	operation := NewReplyOperation[struct{}]("the.module.reply", http.MethodDelete, "/things", "", "", nil,
		[]Response{{Status: http.StatusNoContent, Description: "No content"}})
	handler := operation.Handle(func(context.Context, struct{}, *Meta) (Reply, error) {
		return EmptyReply(http.StatusNoContent), nil
	})
	recorder := serve(t, http.MethodDelete, "/things", handler,
		httptest.NewRequest(http.MethodDelete, "/things", nil))

	if recorder.Code != http.StatusNoContent {
		t.Errorf("the operation answered %d, want 204", recorder.Code)
	}
	if recorder.Body.Len() != 0 {
		t.Errorf("an empty reply wrote a body: %q", recorder.Body.String())
	}
}

// TestAnEmptyReplyAgainstABodyDeclarationIsInternal holds the other direction of the empty check.
func TestAnEmptyReplyAgainstABodyDeclarationIsInternal(t *testing.T) {
	recorder := serveReply(t, http.MethodGet, "/things",
		func(context.Context, struct{}, *Meta) (Reply, error) {
			return EmptyReply(http.StatusOK), nil
		},
		httptest.NewRequest(http.MethodGet, "/things", nil))

	if recorder.Code != http.StatusInternalServerError {
		t.Errorf("an empty reply against a body declaration answered %d, want 500", recorder.Code)
	}
}

// TestABytesReplyWritesTheBodyAndMediaType holds the bytes row.
func TestABytesReplyWritesTheBodyAndMediaType(t *testing.T) {
	operation := NewReplyOperation[struct{}]("the.module.reply", http.MethodGet, "/blob", "", "", nil,
		[]Response{{Status: http.StatusOK, Description: "The bytes", Body: &Body{MediaType: "application/octet-stream"}}})
	handler := operation.Handle(func(context.Context, struct{}, *Meta) (Reply, error) {
		return BytesReply(http.StatusOK, "application/octet-stream", []byte{0, 1, 2, 255}), nil
	})
	recorder := serve(t, http.MethodGet, "/blob", handler, httptest.NewRequest(http.MethodGet, "/blob", nil))

	if recorder.Code != http.StatusOK {
		t.Fatalf("the operation answered %d, want 200", recorder.Code)
	}
	if got := recorder.Header().Get("Content-Type"); got != "application/octet-stream" {
		t.Errorf("Content-Type is %q, want application/octet-stream", got)
	}
	if got := recorder.Body.Bytes(); string(got) != string([]byte{0, 1, 2, 255}) {
		t.Errorf("the answer body is %v, want the raw bytes", got)
	}
}

// TestABytesReplyOfAnotherMediaTypeIsInternal holds the bytes media-type check.
func TestABytesReplyOfAnotherMediaTypeIsInternal(t *testing.T) {
	operation := NewReplyOperation[struct{}]("the.module.reply", http.MethodGet, "/blob", "", "", nil,
		[]Response{{Status: http.StatusOK, Description: "The bytes", Body: &Body{MediaType: "application/octet-stream"}}})
	handler := operation.Handle(func(context.Context, struct{}, *Meta) (Reply, error) {
		return BytesReply(http.StatusOK, "text/plain", []byte("x")), nil
	})
	recorder := serve(t, http.MethodGet, "/blob", handler, httptest.NewRequest(http.MethodGet, "/blob", nil))

	if recorder.Code != http.StatusInternalServerError {
		t.Errorf("a bytes reply of another media type answered %d, want 500", recorder.Code)
	}
}

// TestAnEventStreamDeclarationRefusesBytesAndStream holds the event stream's exclusivity: the
// fragment publishes no-store for it, which only the SSE writer supplies.
func TestAnEventStreamDeclarationRefusesBytesAndStream(t *testing.T) {
	for _, c := range []struct {
		name  string
		reply func() Reply
	}{
		{"bytes", func() Reply { return BytesReply(http.StatusOK, "text/event-stream", []byte("x")) }},
		{"stream", func() Reply {
			return StreamReply(http.StatusOK, "text/event-stream", func(context.Context, io.Writer) error { return nil })
		}},
	} {
		t.Run(c.name, func(t *testing.T) {
			operation := NewReplyOperation[struct{}]("the.module.reply", http.MethodGet, "/events", "", "", nil,
				[]Response{{
					Status: http.StatusOK, Description: "The events",
					Body: &Body{MediaType: "text/event-stream"},
				}})
			handler := operation.Handle(func(context.Context, struct{}, *Meta) (Reply, error) {
				return c.reply(), nil
			})
			recorder := serve(t, http.MethodGet, "/events", handler,
				httptest.NewRequest(http.MethodGet, "/events", nil))
			if recorder.Code != http.StatusInternalServerError {
				t.Errorf("a %s reply against an event stream answered %d, want 500", c.name, recorder.Code)
			}
		})
	}
}

// TestAStreamReplyWritesWhatTheCallbackWrites holds the stream row.
func TestAStreamReplyWritesWhatTheCallbackWrites(t *testing.T) {
	operation := NewReplyOperation[struct{}]("the.module.reply", http.MethodGet, "/stream", "", "", nil,
		[]Response{{Status: http.StatusOK, Description: "The stream", Body: &Body{MediaType: "text/plain"}}})
	handler := operation.Handle(func(context.Context, struct{}, *Meta) (Reply, error) {
		return StreamReply(http.StatusOK, "text/plain", func(_ context.Context, writer io.Writer) error {
			if _, err := io.WriteString(writer, "one"); err != nil {
				return err
			}
			_, err := io.WriteString(writer, "two")
			return err
		}), nil
	})
	recorder := serve(t, http.MethodGet, "/stream", handler, httptest.NewRequest(http.MethodGet, "/stream", nil))

	if recorder.Code != http.StatusOK {
		t.Fatalf("the operation answered %d, want 200", recorder.Code)
	}
	if got := recorder.Body.String(); got != "onetwo" {
		t.Errorf("the streamed body is %q, want onetwo", got)
	}
}

// TestAStreamErrorAbandonsTheConnection holds the stream failure: the status has already gone out.
func TestAStreamErrorAbandonsTheConnection(t *testing.T) {
	operation := NewReplyOperation[struct{}]("the.module.reply", http.MethodGet, "/stream", "", "", nil,
		[]Response{{Status: http.StatusOK, Description: "The stream", Body: &Body{MediaType: "text/plain"}}})
	failure := errors.New("the source went away")
	handler := operation.Handle(func(context.Context, struct{}, *Meta) (Reply, error) {
		return StreamReply(http.StatusOK, "text/plain", func(_ context.Context, writer io.Writer) error {
			if _, err := io.WriteString(writer, "start"); err != nil {
				return err
			}
			return failure
		}), nil
	})

	gin.SetMode(gin.TestMode)
	engine := gin.New()
	var captured *gin.Context
	engine.Use(func(c *gin.Context) {
		captured = c
		c.Next()
	})
	engine.Handle(http.MethodGet, "/stream", handler)
	recorder := httptest.NewRecorder()

	func() {
		defer func() {
			thrown := recover()
			if err, ok := thrown.(error); !ok || !errors.Is(err, http.ErrAbortHandler) {
				t.Fatalf("a failed stream panicked with %v, want http.ErrAbortHandler", thrown)
			}
		}()
		engine.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/stream", nil))
	}()

	if recorder.Code != http.StatusOK {
		t.Errorf("the stream's status is %d, want the committed 200", recorder.Code)
	}
	if got := recorder.Body.String(); got != "start" {
		t.Errorf("the partial body is %q, want start", got)
	}
	if captured == nil || len(captured.Errors) != 1 {
		t.Fatalf("the stream failure was not recorded in the error list: %v", captured)
	}
	if !errors.Is(captured.Errors[0], failure) {
		t.Errorf("the recorded error is %v, want the stream's failure", captured.Errors[0])
	}
}

// TestAnSSEReplyFormatsAndFlushesEvents holds the event stream row and its wire format.
func TestAnSSEReplyFormatsAndFlushesEvents(t *testing.T) {
	operation := NewReplyOperation[struct{}]("the.module.reply", http.MethodGet, "/events", "", "", nil,
		[]Response{{Status: http.StatusOK, Description: "The events", Body: &Body{MediaType: "text/event-stream"}}})
	handler := operation.Handle(func(context.Context, struct{}, *Meta) (Reply, error) {
		return SSEReply(http.StatusOK, func(events *SSEWriter) error {
			if err := events.Send("tick", []byte("one")); err != nil {
				return err
			}
			return events.Send("", []byte("two\nthree"))
		}), nil
	})
	recorder := serve(t, http.MethodGet, "/events", handler, httptest.NewRequest(http.MethodGet, "/events", nil))

	if recorder.Code != http.StatusOK {
		t.Fatalf("the operation answered %d, want 200", recorder.Code)
	}
	if got := recorder.Header().Get("Content-Type"); got != "text/event-stream" {
		t.Errorf("Content-Type is %q, want text/event-stream", got)
	}
	if got := recorder.Header().Get("Cache-Control"); got != "no-store" {
		t.Errorf("Cache-Control is %q, want no-store", got)
	}
	want := "event: tick\ndata: one\n\ndata: two\ndata: three\n\n"
	if got := recorder.Body.String(); got != want {
		t.Errorf("the event stream is %q, want %q", got, want)
	}
}

// TestSSEDataIsSplitOnEveryLineEnding holds the framing: SSE ends a line with CRLF, LF or CR.
func TestSSEDataIsSplitOnEveryLineEnding(t *testing.T) {
	operation := NewReplyOperation[struct{}]("the.module.reply", http.MethodGet, "/events", "", "", nil,
		[]Response{{Status: http.StatusOK, Description: "The events", Body: &Body{MediaType: "text/event-stream"}}})
	handler := operation.Handle(func(context.Context, struct{}, *Meta) (Reply, error) {
		return SSEReply(http.StatusOK, func(events *SSEWriter) error {
			return events.Send("", []byte("a\rb\r\nc\nd"))
		}), nil
	})
	recorder := serve(t, http.MethodGet, "/events", handler, httptest.NewRequest(http.MethodGet, "/events", nil))

	if recorder.Code != http.StatusOK {
		t.Fatalf("the operation answered %d, want 200", recorder.Code)
	}
	want := "data: a\ndata: b\ndata: c\ndata: d\n\n"
	if got := recorder.Body.String(); got != want {
		t.Errorf("the event stream is %q, want %q", got, want)
	}
}

// TestAStreamReplyWithoutACallbackIsInternal holds the stream's precondition: a stream with no
// callback is a defect known before anything is written.
func TestAStreamReplyWithoutACallbackIsInternal(t *testing.T) {
	operation := NewReplyOperation[struct{}]("the.module.reply", http.MethodGet, "/stream", "", "", nil,
		[]Response{{Status: http.StatusOK, Description: "The stream", Body: &Body{MediaType: "text/plain"}}})
	handler := operation.Handle(func(context.Context, struct{}, *Meta) (Reply, error) {
		return StreamReply(http.StatusOK, "text/plain", nil), nil
	})
	recorder := serve(t, http.MethodGet, "/stream", handler, httptest.NewRequest(http.MethodGet, "/stream", nil))

	if recorder.Code != http.StatusInternalServerError {
		t.Errorf("a stream reply with no callback answered %d, want 500", recorder.Code)
	}
	if mediaType := recorder.Header().Get("Content-Type"); !strings.HasPrefix(mediaType, problemMediaType) {
		t.Errorf("the defect is not a problem document: Content-Type is %q", mediaType)
	}
}

// TestAnSSEReplyWithoutAnEventCallbackIsInternal holds the event stream's precondition, checked
// before the status is committed.
func TestAnSSEReplyWithoutAnEventCallbackIsInternal(t *testing.T) {
	operation := NewReplyOperation[struct{}]("the.module.reply", http.MethodGet, "/events", "", "", nil,
		[]Response{{Status: http.StatusOK, Description: "The events", Body: &Body{MediaType: "text/event-stream"}}})
	handler := operation.Handle(func(context.Context, struct{}, *Meta) (Reply, error) {
		return SSEReply(http.StatusOK, nil), nil
	})
	recorder := serve(t, http.MethodGet, "/events", handler, httptest.NewRequest(http.MethodGet, "/events", nil))

	if recorder.Code != http.StatusInternalServerError {
		t.Errorf("an event stream reply with no callback answered %d, want 500", recorder.Code)
	}
	if mediaType := recorder.Header().Get("Content-Type"); !strings.HasPrefix(mediaType, problemMediaType) {
		t.Errorf("the defect is not a problem document: Content-Type is %q", mediaType)
	}
}

// TestADeclaredCacheControlStandsOnAnEventStream holds the default's limit: a declared Cache-
// Control is what the response carries.
func TestADeclaredCacheControlStandsOnAnEventStream(t *testing.T) {
	operation := NewReplyOperation[struct{}]("the.module.reply", http.MethodGet, "/events", "", "", nil,
		[]Response{{
			Status:      http.StatusOK,
			Description: "The events",
			Headers:     []Header{{Name: "Cache-Control", Value: "no-cache"}},
			Body:        &Body{MediaType: "text/event-stream"},
		}})
	handler := operation.Handle(func(context.Context, struct{}, *Meta) (Reply, error) {
		return SSEReply(http.StatusOK, func(events *SSEWriter) error {
			return events.Send("tick", nil)
		}), nil
	})
	recorder := serve(t, http.MethodGet, "/events", handler, httptest.NewRequest(http.MethodGet, "/events", nil))

	if got := recorder.Header().Get("Cache-Control"); got != "no-cache" {
		t.Errorf("Cache-Control is %q, want the declared no-cache", got)
	}
}

// TestARedirectReplySetsTheLocationAndWritesNoBody holds the redirect row.
func TestARedirectReplySetsTheLocationAndWritesNoBody(t *testing.T) {
	operation := NewReplyOperation[struct{}]("the.module.reply", http.MethodGet, "/old", "", "", nil,
		[]Response{{
			Status:      http.StatusFound,
			Description: "Moved",
			Headers:     []Header{{Name: "Location", Description: "Where to go"}},
		}})
	handler := operation.Handle(func(context.Context, struct{}, *Meta) (Reply, error) {
		return RedirectReply(http.StatusFound, "/new"), nil
	})
	recorder := serve(t, http.MethodGet, "/old", handler, httptest.NewRequest(http.MethodGet, "/old", nil))

	if recorder.Code != http.StatusFound {
		t.Fatalf("the operation answered %d, want 302", recorder.Code)
	}
	if got := recorder.Header().Get("Location"); got != "/new" {
		t.Errorf("Location is %q, want /new", got)
	}
	if recorder.Body.Len() != 0 {
		t.Errorf("a redirect wrote a body: %q", recorder.Body.String())
	}
}

// TestARedirectAgainstABodyDeclarationIsInternal holds the redirect's body check: a redirect answer
// carries no body, so it cannot answer a response that declares one.
func TestARedirectAgainstABodyDeclarationIsInternal(t *testing.T) {
	operation := NewReplyOperation[struct{}]("the.module.reply", http.MethodGet, "/old", "", "", nil,
		[]Response{{
			Status:      http.StatusFound,
			Description: "Moved",
			Headers:     []Header{{Name: "Location", Description: "Where to go"}},
			Body:        &Body{MediaType: "application/json", Structure: reflect.TypeOf(replyBody{})},
		}})
	handler := operation.Handle(func(context.Context, struct{}, *Meta) (Reply, error) {
		return RedirectReply(http.StatusFound, "/new"), nil
	})
	recorder := serve(t, http.MethodGet, "/old", handler, httptest.NewRequest(http.MethodGet, "/old", nil))

	if recorder.Code != http.StatusInternalServerError {
		t.Errorf("a redirect against a body declaration answered %d, want 500", recorder.Code)
	}
	if got := recorder.Header().Get("Location"); got != "" {
		t.Errorf("the redirect was written with Location %q beside the defect", got)
	}
}

// TestARedirectWithoutADeclaredLocationIsInternal holds the redirect check.
func TestARedirectWithoutADeclaredLocationIsInternal(t *testing.T) {
	operation := NewReplyOperation[struct{}]("the.module.reply", http.MethodGet, "/old", "", "", nil,
		[]Response{{Status: http.StatusFound, Description: "Moved"}})
	handler := operation.Handle(func(context.Context, struct{}, *Meta) (Reply, error) {
		return RedirectReply(http.StatusFound, "/new"), nil
	})
	recorder := serve(t, http.MethodGet, "/old", handler, httptest.NewRequest(http.MethodGet, "/old", nil))

	if recorder.Code != http.StatusInternalServerError {
		t.Errorf("a redirect without a declared Location answered %d, want 500", recorder.Code)
	}
}

// TestARedirectAgainstANonRedirectStatusIsInternal holds the redirect status check.
func TestARedirectAgainstANonRedirectStatusIsInternal(t *testing.T) {
	recorder := serveReply(t, http.MethodGet, "/things",
		func(context.Context, struct{}, *Meta) (Reply, error) {
			return RedirectReply(http.StatusOK, "/new"), nil
		},
		httptest.NewRequest(http.MethodGet, "/things", nil))

	if recorder.Code != http.StatusInternalServerError {
		t.Errorf("a redirect against a 200 answered %d, want 500", recorder.Code)
	}
}

// TestADeclaredDynamicHeaderIsWritten holds the declared side of the dynamic header check, and the
// order dynamic headers are written in.
func TestADeclaredDynamicHeaderIsWritten(t *testing.T) {
	operation := NewReplyOperation[struct{}]("the.module.reply", http.MethodGet, "/things", "", "", nil,
		[]Response{{
			Status:      http.StatusOK,
			Description: "The body",
			Headers:     []Header{{Name: "X-Trace", Value: "static"}},
			Body:        &Body{MediaType: "application/json", Structure: reflect.TypeOf(replyBody{})},
		}})
	handler := operation.Handle(func(context.Context, struct{}, *Meta) (Reply, error) {
		return WithHeaders(JSONReply(http.StatusOK, replyBody{Name: "alice"}),
			HeaderValue{Name: "X-Trace", Value: "dynamic"}), nil
	})
	recorder := serve(t, http.MethodGet, "/things", handler, httptest.NewRequest(http.MethodGet, "/things", nil))

	if recorder.Code != http.StatusOK {
		t.Fatalf("the operation answered %d, want 200", recorder.Code)
	}
	if got := recorder.Header().Get("X-Trace"); got != "dynamic" {
		t.Errorf("X-Trace is %q, want the dynamic value written after the static one", got)
	}
}

// TestAnUndeclaredDynamicHeaderIsInternal holds the undeclared side of the dynamic header check:
// the answer is not written at all.
func TestAnUndeclaredDynamicHeaderIsInternal(t *testing.T) {
	recorder := serveReply(t, http.MethodGet, "/things",
		func(context.Context, struct{}, *Meta) (Reply, error) {
			return WithHeaders(JSONReply(http.StatusOK, replyBody{Name: "alice"}),
				HeaderValue{Name: "X-Undeclared", Value: "x"}), nil
		},
		httptest.NewRequest(http.MethodGet, "/things", nil))

	if recorder.Code != http.StatusInternalServerError {
		t.Errorf("an undeclared dynamic header answered %d, want 500", recorder.Code)
	}
	if got := recorder.Header().Get("X-Undeclared"); got != "" {
		t.Errorf("the undeclared header was written with %q", got)
	}
	if got := strings.TrimSpace(recorder.Body.String()); strings.Contains(got, "alice") {
		t.Errorf("the reply was written beside the defect: %s", got)
	}
}

// TestCookiesAreAlwaysWritten holds the cookie row: a cookie is not part of the declared header set
// and is written anyway.
func TestCookiesAreAlwaysWritten(t *testing.T) {
	operation := NewReplyOperation[struct{}]("the.module.reply", http.MethodDelete, "/things", "", "", nil,
		[]Response{{Status: http.StatusNoContent, Description: "No content"}})
	handler := operation.Handle(func(context.Context, struct{}, *Meta) (Reply, error) {
		return WithCookies(EmptyReply(http.StatusNoContent), &http.Cookie{
			Name: "session", Value: "abc", Path: "/", HttpOnly: true,
		}), nil
	})
	recorder := serve(t, http.MethodDelete, "/things", handler,
		httptest.NewRequest(http.MethodDelete, "/things", nil))

	if recorder.Code != http.StatusNoContent {
		t.Fatalf("the operation answered %d, want 204", recorder.Code)
	}
	if got := recorder.Header().Get("Set-Cookie"); !strings.Contains(got, "session=abc") {
		t.Errorf("Set-Cookie is %q, want the reply's cookie", got)
	}
}

// TestMetaIsTheRequestSnapshot holds the Meta values.
func TestMetaIsTheRequestSnapshot(t *testing.T) {
	operation := NewReplyOperation[struct{}]("the.module.reply", http.MethodGet, "/items/:id", "", "", nil,
		[]Response{jsonBodyResponse(http.StatusOK)})
	handler := operation.Handle(func(_ context.Context, _ struct{}, meta *Meta) (Reply, error) {
		if meta == nil {
			t.Fatal("the handler received no Meta")
		}
		if meta.ClientIP != "192.0.2.1" {
			t.Errorf("Meta.ClientIP is %q, want 192.0.2.1", meta.ClientIP)
		}
		if meta.Route != "/items/:id" {
			t.Errorf("Meta.Route is %q, want /items/:id", meta.Route)
		}
		return JSONReply(http.StatusOK, replyBody{Name: "x"}), nil
	})
	recorder := serve(t, http.MethodGet, "/items/:id", handler,
		httptest.NewRequest(http.MethodGet, "/items/abc", nil))

	if recorder.Code != http.StatusOK {
		t.Errorf("the operation answered %d, want 200", recorder.Code)
	}
}

// TestAReplyOperationBindsItsDeclaredInput holds the request half of a reply operation: it binds
// the same input the simple path does.
func TestAReplyOperationBindsItsDeclaredInput(t *testing.T) {
	operation := NewReplyOperation[formInput]("the.module.reply", http.MethodPost, "/items/:id", "", "",
		requestBody("application/x-www-form-urlencoded"),
		[]Response{jsonBodyResponse(http.StatusOK)})
	handler := operation.Handle(func(_ context.Context, in formInput, _ *Meta) (Reply, error) {
		if in.ID != "abc" || in.Name != "alice" || in.Age != 30 {
			t.Errorf("the handler saw %+v, want id=abc name=alice age=30", in)
		}
		return JSONReply(http.StatusOK, replyBody{Name: in.Name}), nil
	})
	request := httptest.NewRequest(http.MethodPost, "/items/abc", strings.NewReader("name=alice&age=30"))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	recorder := serve(t, http.MethodPost, "/items/:id", handler, request)
	if recorder.Code != http.StatusOK {
		t.Errorf("the operation answered %d, want 200", recorder.Code)
	}
}

// TestAReplyOperationAnswersADeclaredFault holds the failure path: a Fault is written with its
// status when the operation declares it, and a Failure that is not declared is answered 500.
func TestAReplyOperationAnswersADeclaredFault(t *testing.T) {
	operation := NewReplyOperation[struct{}]("the.module.reply", http.MethodGet, "/things", "", "", nil,
		[]Response{
			jsonBodyResponse(http.StatusOK),
			ProblemResponse(http.StatusConflict, "Already exists"),
		})
	handler := operation.Handle(func(context.Context, struct{}, *Meta) (Reply, error) {
		return nil, Fault{Status: http.StatusConflict, Type: "urn:speed:test:conflict"}
	})
	recorder := serve(t, http.MethodGet, "/things", handler, httptest.NewRequest(http.MethodGet, "/things", nil))

	if recorder.Code != http.StatusConflict {
		t.Errorf("a declared fault answered %d, want 409", recorder.Code)
	}
}

// TestAReplyOperationDescriptorHasNoSuccessBody holds the generator's view.
func TestAReplyOperationDescriptorHasNoSuccessBody(t *testing.T) {
	operation := NewReplyOperation[formInput]("the.module.reply", http.MethodGet, "/items", "Summary", "Description", nil,
		[]Response{jsonBodyResponse(http.StatusOK)})
	descriptor := operation.Descriptor()

	if descriptor.ID != "the.module.reply" || descriptor.Method != http.MethodGet ||
		descriptor.Path != "/items" || descriptor.Summary != "Summary" ||
		descriptor.Description != "Description" {
		t.Errorf("the descriptor lost the operation's metadata: %+v", descriptor)
	}
	if descriptor.Input != reflect.TypeOf(formInput{}) {
		t.Errorf("the descriptor's input is %v, want formInput", descriptor.Input)
	}
	if descriptor.SuccessStatus != http.StatusOK {
		t.Errorf("the descriptor's success status is %d, want 200", descriptor.SuccessStatus)
	}
	if descriptor.SuccessBody != nil {
		t.Errorf("the descriptor's success body is %v, want nil", descriptor.SuccessBody)
	}
	if len(descriptor.Responses) != 1 || descriptor.Responses[0].Body.Structure != reflect.TypeOf(replyBody{}) {
		t.Errorf("the descriptor lost the declared responses: %+v", descriptor.Responses)
	}
}

// TestAReplyOperationWithTwoSuccessesDeclaresNoSuccessStatus holds the descriptor's rule for an
// operation whose 2xx answers cannot be reduced to one.
func TestAReplyOperationWithTwoSuccessesDeclaresNoSuccessStatus(t *testing.T) {
	operation := NewReplyOperation[struct{}]("the.module.reply", http.MethodGet, "/things", "", "", nil,
		[]Response{jsonBodyResponse(http.StatusOK), jsonBodyResponse(http.StatusCreated)})

	if status := operation.Descriptor().SuccessStatus; status != 0 {
		t.Errorf("the descriptor's success status is %d, want 0", status)
	}
}

// TestAJSONReplyForAStatusWithoutABodyIsInternal holds the reply check: a 204 or 304 answer carries
// no body, so a JSON reply against it is refused rather than written and dropped by the transport.
func TestAJSONReplyForAStatusWithoutABodyIsInternal(t *testing.T) {
	for _, status := range []int{http.StatusNoContent, http.StatusNotModified} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			operation := NewReplyOperation[struct{}]("the.module.reply", http.MethodGet, "/things", "", "", nil,
				[]Response{{Status: status, Description: "No body"}})
			handler := operation.Handle(func(context.Context, struct{}, *Meta) (Reply, error) {
				return JSONReply(status, replyBody{Name: "alice"}), nil
			})
			recorder := serve(t, http.MethodGet, "/things", handler,
				httptest.NewRequest(http.MethodGet, "/things", nil))

			if recorder.Code != http.StatusInternalServerError {
				t.Errorf("a JSON reply for %d answered %d, want 500", status, recorder.Code)
			}
			if body := recorder.Body.String(); strings.Contains(body, "alice") {
				t.Errorf("the JSON body was written beside the defect: %s", body)
			}
		})
	}
}

// TestAJSONReplyAgainstADeclaredBodyForANoBodyStatusIsRefused holds the check itself, so the
// mismatch is caught even when the declared response carries a body the transport would drop.
func TestAJSONReplyAgainstADeclaredBodyForANoBodyStatusIsRefused(t *testing.T) {
	for _, status := range []int{http.StatusNoContent, http.StatusNotModified} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			response := Response{Status: status, Body: &Body{MediaType: "application/json"}}
			reply := &replyValue{kind: replyKindJSON, status: status}
			if reason := checkReplyDeclaration(reply, response); reason == "" {
				t.Errorf("a JSON reply for %d was accepted against a body declaration", status)
			}
		})
	}
}

func dynamicOperation(t *testing.T) ReplyOperation[struct{}] {
	t.Helper()
	return NewReplyOperation[struct{}]("the.module.reply", http.MethodGet, "/blob", "", "", nil,
		[]Response{{Status: http.StatusOK, Description: "The bytes", Body: &Body{MediaType: "*/*"}}})
}

// TestADynamicMediaTypeAcceptsABytesReply holds the dynamic row: a "*/*" declaration accepts any
// bytes media type the handler builds.
func TestADynamicMediaTypeAcceptsABytesReply(t *testing.T) {
	operation := dynamicOperation(t)
	handler := operation.Handle(func(context.Context, struct{}, *Meta) (Reply, error) {
		return BytesReply(http.StatusOK, "image/png", []byte{0x89, 0x50, 0x4e, 0x47}), nil
	})
	recorder := serve(t, http.MethodGet, "/blob", handler, httptest.NewRequest(http.MethodGet, "/blob", nil))

	if recorder.Code != http.StatusOK {
		t.Fatalf("a dynamic bytes reply answered %d, want 200", recorder.Code)
	}
	if got := recorder.Header().Get("Content-Type"); got != "image/png" {
		t.Errorf("Content-Type is %q, want image/png", got)
	}
	if got := recorder.Body.Bytes(); string(got) != string([]byte{0x89, 0x50, 0x4e, 0x47}) {
		t.Errorf("the answer body is %v, want the reply's bytes", got)
	}
}

// TestADynamicMediaTypeAcceptsAStreamReply holds the stream side of the dynamic row.
func TestADynamicMediaTypeAcceptsAStreamReply(t *testing.T) {
	operation := dynamicOperation(t)
	handler := operation.Handle(func(context.Context, struct{}, *Meta) (Reply, error) {
		return StreamReply(http.StatusOK, "text/csv", func(_ context.Context, writer io.Writer) error {
			_, err := io.WriteString(writer, "a,b")
			return err
		}), nil
	})
	recorder := serve(t, http.MethodGet, "/blob", handler, httptest.NewRequest(http.MethodGet, "/blob", nil))

	if recorder.Code != http.StatusOK {
		t.Fatalf("a dynamic stream reply answered %d, want 200", recorder.Code)
	}
	if got := recorder.Header().Get("Content-Type"); got != "text/csv" {
		t.Errorf("Content-Type is %q, want text/csv", got)
	}
	if got := recorder.Body.String(); got != "a,b" {
		t.Errorf("the streamed body is %q, want a,b", got)
	}
}

// TestADynamicMediaTypeRefusesAJSONReply holds the dynamic declaration's limit: only bytes and
// stream replies choose a media type at run time, so a JSON reply against "*/*" is a defect.
func TestADynamicMediaTypeRefusesAJSONReply(t *testing.T) {
	operation := dynamicOperation(t)
	handler := operation.Handle(func(context.Context, struct{}, *Meta) (Reply, error) {
		return JSONReply(http.StatusOK, replyBody{Name: "alice"}), nil
	})
	recorder := serve(t, http.MethodGet, "/blob", handler, httptest.NewRequest(http.MethodGet, "/blob", nil))

	if recorder.Code != http.StatusInternalServerError {
		t.Errorf("a JSON reply against a dynamic declaration answered %d, want 500", recorder.Code)
	}
}

// TestMetaCarriesTheContentLength holds the request length snapshot: a known length is recorded and
// an unknown one, which net/http spells -1, stays -1.
func TestMetaCarriesTheContentLength(t *testing.T) {
	operation := NewReplyOperation[struct{}]("the.module.reply", http.MethodPost, "/items", "", "", nil,
		[]Response{jsonBodyResponse(http.StatusOK)})
	var lengths []int64
	handler := operation.Handle(func(_ context.Context, _ struct{}, meta *Meta) (Reply, error) {
		lengths = append(lengths, meta.ContentLength)
		return JSONReply(http.StatusOK, replyBody{Name: "x"}), nil
	})

	known := httptest.NewRequest(http.MethodPost, "/items", strings.NewReader("hello"))
	serve(t, http.MethodPost, "/items", handler, known)

	unknown := httptest.NewRequest(http.MethodPost, "/items", strings.NewReader("hello"))
	unknown.ContentLength = -1
	serve(t, http.MethodPost, "/items", handler, unknown)

	if len(lengths) != 2 {
		t.Fatalf("the handler ran %d times, want 2", len(lengths))
	}
	if lengths[0] != 5 {
		t.Errorf("Meta.ContentLength is %d, want 5", lengths[0])
	}
	if lengths[1] != -1 {
		t.Errorf("Meta.ContentLength is %d, want -1 for an unknown length", lengths[1])
	}
}

type flushCounter struct {
	*httptest.ResponseRecorder
	flushes int
}

func (f *flushCounter) Flush() { f.flushes++ }

// TestTheSSEHeadersAreFlushedBeforeTheFirstEvent holds the connection timing: the status and
// headers are flushed once before the handler builds its first event.
func TestTheSSEHeadersAreFlushedBeforeTheFirstEvent(t *testing.T) {
	operation := NewReplyOperation[struct{}]("the.module.reply", http.MethodGet, "/events", "", "", nil,
		[]Response{{Status: http.StatusOK, Description: "The events", Body: &Body{MediaType: "text/event-stream"}}})
	counter := &flushCounter{ResponseRecorder: httptest.NewRecorder()}
	flushedBeforeFirstEvent := false
	handler := operation.Handle(func(context.Context, struct{}, *Meta) (Reply, error) {
		return SSEReply(http.StatusOK, func(events *SSEWriter) error {
			flushedBeforeFirstEvent = counter.flushes > 0
			return events.Send("tick", []byte("one"))
		}), nil
	})

	gin.SetMode(gin.TestMode)
	engine := gin.New()
	engine.Handle(http.MethodGet, "/events", handler)
	engine.ServeHTTP(counter, httptest.NewRequest(http.MethodGet, "/events", nil))

	if !flushedBeforeFirstEvent {
		t.Error("the SSE headers were not flushed before the first event was built")
	}
	if counter.flushes < 2 {
		t.Errorf("the response was flushed %d times, want the header flush and one per event", counter.flushes)
	}
	if got := counter.Body.String(); got != "event: tick\ndata: one\n\n" {
		t.Errorf("the event stream is %q, want one tick event", got)
	}
}

type countingFlusher struct{ flushes int }

func (f *countingFlusher) Flush() { f.flushes++ }

// TestNewSSEWriterWritesAndFlushesEachEvent holds the exported constructor and its wire format:
// each event is written and flushed, and a nil flusher writes without panicking.
func TestNewSSEWriterWritesAndFlushesEachEvent(t *testing.T) {
	var body strings.Builder
	flusher := &countingFlusher{}
	writer := NewSSEWriter(&body, flusher)
	if err := writer.Send("tick", []byte("one\ntwo")); err != nil {
		t.Fatalf("sending an event: %v", err)
	}
	if got, want := body.String(), "event: tick\ndata: one\ndata: two\n\n"; got != want {
		t.Errorf("the event stream is %q, want %q", got, want)
	}
	if flusher.flushes != 1 {
		t.Errorf("the writer flushed %d times, want one per event", flusher.flushes)
	}

	body.Reset()
	if err := NewSSEWriter(&body, nil).Send("", []byte("three")); err != nil {
		t.Fatalf("sending without a flusher: %v", err)
	}
	if got, want := body.String(), "data: three\n\n"; got != want {
		t.Errorf("the event stream without a flusher is %q, want %q", got, want)
	}
}

// TestAReplyBodyWithoutAMediaTypePanics holds the declaration the writer cannot describe: a body
// whose media type is empty has no published shape.
func TestAReplyBodyWithoutAMediaTypePanics(t *testing.T) {
	defer func() {
		thrown := recover()
		if thrown == nil {
			t.Fatal("building the handler did not panic on a body with no media type")
		}
		if message := errString(thrown); !strings.Contains(message, "no media type") {
			t.Errorf("the panic reads %q, want it to name the missing media type", message)
		}
	}()
	NewReplyOperation[struct{}]("the.module.reply", http.MethodGet, "/things", "", "", nil,
		[]Response{{Status: 200, Description: "The thing", Body: &Body{}}}).
		Handle(func(context.Context, struct{}, *Meta) (Reply, error) { return EmptyReply(200), nil })
}

// TestAReplyJSONBodyWithoutAStructurePanics holds the other undescribable declaration: a JSON reply
// whose shape the declaration does not name cannot be validated against anything.
func TestAReplyJSONBodyWithoutAStructurePanics(t *testing.T) {
	defer func() {
		thrown := recover()
		if thrown == nil {
			t.Fatal("building the handler did not panic on a JSON body with no Structure")
		}
		if message := errString(thrown); !strings.Contains(message, "no Structure") {
			t.Errorf("the panic reads %q, want it to name the missing structure", message)
		}
	}()
	NewReplyOperation[struct{}]("the.module.reply", http.MethodGet, "/things", "", "", nil,
		[]Response{{Status: 200, Description: "The thing", Body: &Body{MediaType: "application/json"}}}).
		Handle(func(context.Context, struct{}, *Meta) (Reply, error) { return JSONReply(200, struct{}{}), nil })
}
