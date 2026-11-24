package http

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

type readInput struct {
	ID    string `path:"id"`
	Limit int    `query:"limit"`
	Token string `header:"X-Token"`
	Mode  string `json:"mode"`
}

type readReply struct {
	Seen readInput `json:"seen"`
}

func serve(t *testing.T, method, path string, handler gin.HandlerFunc, request *http.Request) *httptest.ResponseRecorder {
	t.Helper()
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	engine.Handle(method, path, handler)
	recorder := httptest.NewRecorder()
	engine.ServeHTTP(recorder, request)
	return recorder
}

// TestTheHandlerReceivesTheDeclaredInput holds the binding half: the handler gets the declared
// input filled from the path, query and header.
func TestTheHandlerReceivesTheDeclaredInput(t *testing.T) {
	operation := NewOperation[readInput, readReply](
		"the.module.read", http.MethodPost, "/items/:id", "", "",
		requestBody("application/json"),
		[]Response{{
			Status:      200,
			Description: "The item",
			Headers:     []Header{{Name: "X-Mode", Description: "The mode", Value: "set"}},
			Body:        &Body{MediaType: "application/json"},
		}},
	)
	handler := operation.Handle(func(_ context.Context, in readInput) (readReply, error) {
		return readReply{Seen: in}, nil
	})
	request := httptest.NewRequest(http.MethodPost, "/items/abc?limit=7",
		jsonBody(`{"mode":"fast"}`))
	request.Header.Set("X-Token", "secret")
	request.Header.Set("Content-Type", "application/json")

	recorder := serve(t, http.MethodPost, "/items/:id", handler, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("the operation answered %d, want 200", recorder.Code)
	}
	if got := recorder.Header().Get("X-Mode"); got != "set" {
		t.Errorf("the declared header reads %q, want set", got)
	}
	var reply readReply
	if err := json.Unmarshal(recorder.Body.Bytes(), &reply); err != nil {
		t.Fatalf("the answer is not JSON: %v", err)
	}
	want := readInput{ID: "abc", Limit: 7, Token: "secret", Mode: "fast"}
	if reply.Seen != want {
		t.Errorf("the handler saw %+v, want %+v", reply.Seen, want)
	}
}

type parameterOnlyInput struct {
	ID    string `path:"id"`
	Limit int    `query:"limit"`
}

// TestAMalformedParameterIsAnsweredBadRequest holds the parameter refusal.
func TestAMalformedParameterIsAnsweredBadRequest(t *testing.T) {
	operation := NewOperation[parameterOnlyInput, struct{}](
		"the.module.read", http.MethodGet, "/items/:id", "", "", nil,
		[]Response{{Status: 204, Description: "No content"}},
	)
	handler := operation.Handle(func(context.Context, parameterOnlyInput) (struct{}, error) {
		t.Error("the handler ran with a parameter it could not have read")
		return struct{}{}, nil
	})
	request := httptest.NewRequest(http.MethodGet, "/items/abc?limit=many", nil)
	recorder := serve(t, http.MethodGet, "/items/:id", handler, request)
	if recorder.Code != http.StatusBadRequest {
		t.Errorf("a malformed limit answered %d, want 400", recorder.Code)
	}
}

// TestAnUndeclaredBodyMemberIsRefused holds the body half: the entry point decodes only the fields
// the declaration carries.
func TestAnUndeclaredBodyMemberIsRefused(t *testing.T) {
	operation := NewOperation[readInput, struct{}](
		"the.module.write", http.MethodPost, "/items/:id", "", "",
		requestBody("application/json"),
		[]Response{{Status: 204, Description: "No content"}},
	)
	handler := operation.Handle(func(context.Context, readInput) (struct{}, error) {
		t.Error("the handler ran with an undeclared body member")
		return struct{}{}, nil
	})
	request := httptest.NewRequest(http.MethodPost, "/items/abc",
		jsonBody(`{"mode":"fast","smuggled":true}`))
	request.Header.Set("Content-Type", "application/json")
	recorder := serve(t, http.MethodPost, "/items/:id", handler, request)
	if recorder.Code != http.StatusBadRequest {
		t.Errorf("an undeclared body member answered %d, want 400", recorder.Code)
	}
}

// TestANonJSONBodyIsUnsupportedMediaType holds the media-type refusal.
func TestANonJSONBodyIsUnsupportedMediaType(t *testing.T) {
	operation := NewOperation[readInput, struct{}](
		"the.module.write", http.MethodPost, "/items/:id", "", "",
		requestBody("application/json"),
		[]Response{{Status: 204, Description: "No content"}},
	)
	handler := operation.Handle(func(context.Context, readInput) (struct{}, error) {
		t.Error("the handler ran with a body it could not decode")
		return struct{}{}, nil
	})
	request := httptest.NewRequest(http.MethodPost, "/items/abc", jsonBody(`{"mode":"fast"}`))
	request.Header.Set("Content-Type", "text/plain")
	recorder := serve(t, http.MethodPost, "/items/:id", handler, request)
	if recorder.Code != http.StatusUnsupportedMediaType {
		t.Errorf("a text body answered %d, want 415", recorder.Code)
	}
}

type refusal struct{ status int }

func (r refusal) Error() string   { return "refused" }
func (r refusal) StatusCode() int { return r.status }

// TestADeclaredFailureWritesItsStatus holds the failure mapping.
func TestADeclaredFailureWritesItsStatus(t *testing.T) {
	operation := NewOperation[struct{}, struct{}](
		"the.module.read", http.MethodGet, "/items", "", "", nil,
		[]Response{
			{Status: 204, Description: "No content"},
			{Status: 409, Description: "Conflict"},
		},
	)
	handler := operation.Handle(func(context.Context, struct{}) (struct{}, error) {
		return struct{}{}, refusal{status: http.StatusConflict}
	})
	recorder := serve(t, http.MethodGet, "/items", handler, httptest.NewRequest(http.MethodGet, "/items", nil))
	if recorder.Code != http.StatusConflict {
		t.Errorf("a declared conflict answered %d, want 409", recorder.Code)
	}
}

// TestAnUndeclaredFailureIsAnsweredInternal holds the declaration's force: a status the operation
// did not declare is never written.
func TestAnUndeclaredFailureIsAnsweredInternal(t *testing.T) {
	operation := NewOperation[struct{}, struct{}](
		"the.module.read", http.MethodGet, "/items", "", "", nil,
		[]Response{{Status: 204, Description: "No content"}},
	)
	handler := operation.Handle(func(context.Context, struct{}) (struct{}, error) {
		return struct{}{}, refusal{status: http.StatusConflict}
	})
	recorder := serve(t, http.MethodGet, "/items", handler, httptest.NewRequest(http.MethodGet, "/items", nil))
	if recorder.Code != http.StatusInternalServerError {
		t.Errorf("an undeclared failure answered %d, want 500", recorder.Code)
	}
}

// TestAOneTwoHundredFourAnswersNoBody holds the success side of a 204.
func TestAOneTwoHundredFourAnswersNoBody(t *testing.T) {
	operation := NewOperation[struct{}, struct{}](
		"the.module.delete", http.MethodDelete, "/items", "", "", nil,
		[]Response{{Status: 204, Description: "No content"}},
	)
	handler := operation.Handle(func(context.Context, struct{}) (struct{}, error) {
		return struct{}{}, nil
	})
	recorder := serve(t, http.MethodDelete, "/items", handler,
		httptest.NewRequest(http.MethodDelete, "/items", nil))
	if recorder.Code != http.StatusNoContent {
		t.Errorf("the operation answered %d, want 204", recorder.Code)
	}
	if recorder.Body.Len() != 0 {
		t.Errorf("the answer carries a body: %q", recorder.Body.String())
	}
}

// TestAnEmptyOutIsAJSONBodyOnABodyCarryingStatus holds the descriptor and the write side together:
// only a no-body status suppresses an empty Out's body.
func TestAnEmptyOutIsAJSONBodyOnABodyCarryingStatus(t *testing.T) {
	operation := NewOperation[struct{}, struct{}](
		"the.module.write", http.MethodPost, "/items", "", "", nil,
		[]Response{{Status: http.StatusOK, Description: "Written"}},
	)
	descriptor := operation.Descriptor()
	if descriptor.SuccessBody == nil {
		t.Fatal("the descriptor carries no success body for an empty Out")
	}
	if kind := descriptor.SuccessBody.Kind(); kind != reflect.Struct || descriptor.SuccessBody.NumField() != 0 {
		t.Errorf("the success body type is %v, want the empty struct", descriptor.SuccessBody)
	}
	handler := operation.Handle(func(context.Context, struct{}) (struct{}, error) {
		return struct{}{}, nil
	})
	recorder := serve(t, http.MethodPost, "/items", handler,
		httptest.NewRequest(http.MethodPost, "/items", nil))
	if recorder.Code != http.StatusOK {
		t.Errorf("the operation answered %d, want 200", recorder.Code)
	}
	if body := recorder.Body.String(); body != "{}" {
		t.Errorf("the answer body reads %q, want {}", body)
	}
	if mediaType := recorder.Header().Get("Content-Type"); !strings.HasPrefix(mediaType, "application/json") {
		t.Errorf("the answer's Content-Type is %q, want application/json", mediaType)
	}
}

// TestANoBodySuccessKeepsAnEmptyOutBodyless holds the other side: a 204 writes no body at all, so
// the descriptor carries no success body type.
func TestANoBodySuccessKeepsAnEmptyOutBodyless(t *testing.T) {
	operation := NewOperation[struct{}, struct{}](
		"the.module.delete", http.MethodDelete, "/items", "", "", nil,
		[]Response{{Status: http.StatusNoContent, Description: "No content"}},
	)
	if body := operation.Descriptor().SuccessBody; body != nil {
		t.Errorf("the descriptor's success body is %v, want nil for a 204", body)
	}
}

// TestASimpleNoBodySuccessWithANonEmptyOutPanics holds the no-body side: a 204 declares no body.
func TestASimpleNoBodySuccessWithANonEmptyOutPanics(t *testing.T) {
	defer func() {
		thrown := recover()
		if thrown == nil {
			t.Fatal("building the handler did not panic on a 204 with a non-empty Out")
		}
		if message := errString(thrown); !strings.Contains(message, "carries no body") {
			t.Errorf("the panic reads %q, want it to name the missing body", message)
		}
	}()
	NewOperation[struct{}, readReply](
		"the.module.read", http.MethodGet, "/items", "", "", nil,
		[]Response{{Status: http.StatusNoContent, Description: "No content"}},
	).Handle(func(context.Context, struct{}) (readReply, error) { return readReply{}, nil })
}

func jsonBody(body string) *strings.Reader { return strings.NewReader(body) }

// TestASimpleSuccessBodyOfAnotherMediaTypePanics holds the simple form's contract: its entry point
// always writes JSON.
func TestASimpleSuccessBodyOfAnotherMediaTypePanics(t *testing.T) {
	defer func() {
		thrown := recover()
		if thrown == nil {
			t.Fatal("building the handler did not panic on a non-JSON success body")
		}
		message := errString(thrown)
		if !strings.Contains(message, "must be application/json") {
			t.Errorf("the panic reads %q, want it to name the required media type", message)
		}
		if !strings.Contains(message, "ReplyOperation") {
			t.Errorf("the panic reads %q, want it to direct the author to ReplyOperation", message)
		}
	}()
	NewOperation[struct{}, readReply](
		"the.module.read", http.MethodGet, "/items", "", "", nil,
		[]Response{{Status: http.StatusOK, Description: "The item", Body: &Body{MediaType: "text/plain"}}},
	).Handle(func(context.Context, struct{}) (readReply, error) { return readReply{}, nil })
}

// TestASimpleSuccessBodyWithNoMediaTypeDefaultsToJSON holds the other side: an empty media type
// declares the JSON answer the entry point writes.
func TestASimpleSuccessBodyWithNoMediaTypeDefaultsToJSON(t *testing.T) {
	operation := NewOperation[struct{}, readReply](
		"the.module.read", http.MethodGet, "/items", "", "", nil,
		[]Response{{Status: http.StatusOK, Description: "The item", Body: &Body{}}},
	)
	handler := operation.Handle(func(context.Context, struct{}) (readReply, error) {
		return readReply{Seen: readInput{ID: "abc"}}, nil
	})
	recorder := serve(t, http.MethodGet, "/items", handler, httptest.NewRequest(http.MethodGet, "/items", nil))

	if recorder.Code != http.StatusOK {
		t.Fatalf("the operation answered %d, want 200", recorder.Code)
	}
	if mediaType := recorder.Header().Get("Content-Type"); !strings.HasPrefix(mediaType, "application/json") {
		t.Errorf("the answer's Content-Type is %q, want application/json", mediaType)
	}
}

type jsonTaggedOnlyInput struct {
	Name string `json:"name"`
}

type formTaggedOnlyInput struct {
	Name string `form:"name"`
}

// TestABodyTaggedInputWithoutAMediaTypePanics holds the declaration check: a body- tagged field
// under an empty media type would be declared and never read, so building the handler refuses it.
func TestABodyTaggedInputWithoutAMediaTypePanics(t *testing.T) {
	cases := []struct {
		name  string
		build func()
	}{
		{
			name: "json tag",
			build: func() {
				NewOperation[jsonTaggedOnlyInput, struct{}](
					"the.module.body", http.MethodPost, "/items", "", "", nil,
					[]Response{{Status: http.StatusNoContent}},
				).Handle(func(context.Context, jsonTaggedOnlyInput) (struct{}, error) { return struct{}{}, nil })
			},
		},
		{
			name: "form tag",
			build: func() {
				NewOperation[formTaggedOnlyInput, struct{}](
					"the.module.body", http.MethodPost, "/items", "", "", nil,
					[]Response{{Status: http.StatusNoContent}},
				).Handle(func(context.Context, formTaggedOnlyInput) (struct{}, error) { return struct{}{}, nil })
			},
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			defer func() {
				thrown := recover()
				if thrown == nil {
					t.Fatal("building the handler did not panic on a body tag without a media type")
				}
				if message := errString(thrown); !strings.Contains(message, "declares no request media type") {
					t.Errorf("the panic reads %q, want it to name the missing media type", message)
				}
			}()
			testCase.build()
		})
	}
}

// TestAParameterOnlyInputWithoutAMediaTypeBuilds holds the accepted side: an input carrying only
// parameter tags takes no body and needs no media type.
func TestAParameterOnlyInputWithoutAMediaTypeBuilds(t *testing.T) {
	operation := NewOperation[parameterOnlyInput, struct{}](
		"the.module.read", http.MethodGet, "/items/:id", "", "", nil,
		[]Response{{Status: http.StatusNoContent, Description: "No content"}},
	)
	handler := operation.Handle(func(_ context.Context, in parameterOnlyInput) (struct{}, error) {
		if in.ID != "abc" || in.Limit != 7 {
			t.Errorf("the handler saw %+v, want id=abc limit=7", in)
		}
		return struct{}{}, nil
	})
	recorder := serve(t, http.MethodGet, "/items/:id", handler,
		httptest.NewRequest(http.MethodGet, "/items/abc?limit=7", nil))

	if recorder.Code != http.StatusNoContent {
		t.Errorf("the operation answered %d, want 204", recorder.Code)
	}
}

// TestAResponseWithoutABodyMustNotDeclareOne holds the declaration check for 204 and 304: the
// transport would drop the declared body, so a body there is a defect in the declaration.
func TestAResponseWithoutABodyMustNotDeclareOne(t *testing.T) {
	declaredBody := func(status int) Response {
		return Response{Status: status, Description: "No body", Body: &Body{MediaType: "application/json"}}
	}
	cases := []struct {
		name  string
		build func()
	}{
		{
			name: "simple operation with a 204",
			build: func() {
				NewOperation[struct{}, struct{}](
					"the.module.nobody", http.MethodGet, "/items", "", "", nil,
					[]Response{declaredBody(http.StatusNoContent)},
				).Handle(func(context.Context, struct{}) (struct{}, error) { return struct{}{}, nil })
			},
		},
		{
			name: "simple operation with a 304 beside its success",
			build: func() {
				NewOperation[struct{}, struct{}](
					"the.module.nobody", http.MethodGet, "/items", "", "", nil,
					[]Response{
						{Status: http.StatusOK, Description: "The item", Body: &Body{MediaType: "application/json"}},
						declaredBody(http.StatusNotModified),
					},
				).Handle(func(context.Context, struct{}) (struct{}, error) { return struct{}{}, nil })
			},
		},
		{
			name: "reply operation with a 204",
			build: func() {
				NewReplyOperation[struct{}](
					"the.module.nobody", http.MethodGet, "/items", "", "", nil,
					[]Response{declaredBody(http.StatusNoContent)},
				).Handle(func(context.Context, struct{}, *Meta) (Reply, error) {
					return EmptyReply(http.StatusNoContent), nil
				})
			},
		},
		{
			name: "reply operation with a 304",
			build: func() {
				NewReplyOperation[struct{}](
					"the.module.nobody", http.MethodGet, "/items", "", "", nil,
					[]Response{declaredBody(http.StatusNotModified)},
				).Handle(func(context.Context, struct{}, *Meta) (Reply, error) {
					return EmptyReply(http.StatusNotModified), nil
				})
			},
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			defer func() {
				thrown := recover()
				if thrown == nil {
					t.Fatal("building the handler did not panic on a body declared for a status without one")
				}
				if message := errString(thrown); !strings.Contains(message, "carries none") {
					t.Errorf("the panic reads %q, want it to say the status carries no body", message)
				}
			}()
			testCase.build()
		})
	}
}

// TestTheDescriptorCarriesTheRequestDeclaration holds the generator's view of the request body: the
// declared body itself, description and required included, on both operation forms.
func TestTheDescriptorCarriesTheRequestDeclaration(t *testing.T) {
	request := &RequestBody{MediaType: "application/json", Description: "The item.", Required: true}
	operation := NewOperation[readInput, readReply](
		"the.module.write", http.MethodPost, "/items/:id", "", "", request,
		[]Response{{Status: 200, Description: "The item", Body: &Body{MediaType: "application/json"}}},
	)
	if descriptor := operation.Descriptor(); descriptor.Request != request {
		t.Errorf("the descriptor carries %+v, want the declared request body", descriptor.Request)
	}

	replyRequest := &RequestBody{MediaType: "application/octet-stream", Required: true}
	reply := NewReplyOperation[struct{}](
		"the.module.write", http.MethodPut, "/items", "", "", replyRequest,
		[]Response{{Status: 204, Description: "Written"}},
	)
	if descriptor := reply.Descriptor(); descriptor.Request != replyRequest {
		t.Errorf("the reply descriptor carries %+v, want the declared request body", descriptor.Request)
	}
}

// TestABindingRefusalCarriesTheDeclaredProblemType holds that a request refused before the handler
// runs is answered in the operation's own vocabulary.
func TestABindingRefusalCarriesTheDeclaredProblemType(t *testing.T) {
	const (
		badRequestType = "urn:test:bad-request"
		mediaTypeType  = "urn:test:unsupported-media-type"
	)
	operation := NewOperation[readInput, struct{}](
		"the.module.write", http.MethodPost, "/items/:id", "", "",
		requestBody("application/json"),
		[]Response{
			{Status: http.StatusNoContent, Description: "No content"},
			{Status: http.StatusBadRequest, Description: "The body could not be read", ProblemType: badRequestType},
			{Status: http.StatusUnsupportedMediaType, Description: "Not JSON", ProblemType: mediaTypeType},
		},
	)
	handler := operation.Handle(func(context.Context, readInput) (struct{}, error) {
		t.Error("the handler ran with a body the binding refused")
		return struct{}{}, nil
	})

	malformed := httptest.NewRequest(http.MethodPost, "/items/abc", strings.NewReader("{"))
	malformed.Header.Set("Content-Type", "application/json")
	if got := problemBodyOf(t, serve(t, http.MethodPost, "/items/:id", handler, malformed))["type"]; got != badRequestType {
		t.Errorf("a malformed body's problem type = %v, want %s", got, badRequestType)
	}

	unsupported := httptest.NewRequest(http.MethodPost, "/items/abc", strings.NewReader("{}"))
	unsupported.Header.Set("Content-Type", "text/plain")
	if got := problemBodyOf(t, serve(t, http.MethodPost, "/items/:id", handler, unsupported))["type"]; got != mediaTypeType {
		t.Errorf("a wrong media type's problem type = %v, want %s", got, mediaTypeType)
	}
}
