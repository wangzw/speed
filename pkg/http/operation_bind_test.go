package http

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

// readInput is one operation's input: parameters from three sources and a body
// member.
type readInput struct {
	ID    string `path:"id"`
	Limit int    `query:"limit"`
	Token string `header:"X-Token"`
	Mode  string `json:"mode"`
}

// readReply is the success body.
type readReply struct {
	Seen readInput `json:"seen"`
}

// serve drives one operation through a gin engine and returns the answer.
func serve(t *testing.T, method, path string, handler gin.HandlerFunc, request *http.Request) *httptest.ResponseRecorder {
	t.Helper()
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	engine.Handle(method, path, handler)
	recorder := httptest.NewRecorder()
	engine.ServeHTTP(recorder, request)
	return recorder
}

// TestTheHandlerReceivesTheDeclaredInput holds the binding half: the handler
// gets the declared input filled from the path, query and header, and the
// entry point writes the declared success response.
func TestTheHandlerReceivesTheDeclaredInput(t *testing.T) {
	operation := NewOperation[readInput, readReply](
		"the.module.read", http.MethodPost, "/items/:id", "", "",
		"application/json",
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

// TestAMalformedParameterIsAnsweredBadRequest holds the parameter refusal.
func TestAMalformedParameterIsAnsweredBadRequest(t *testing.T) {
	operation := NewOperation[readInput, struct{}](
		"the.module.read", http.MethodGet, "/items/:id", "", "", "",
		[]Response{{Status: 204, Description: "No content"}},
	)
	handler := operation.Handle(func(context.Context, readInput) (struct{}, error) {
		t.Error("the handler ran with a parameter it could not have read")
		return struct{}{}, nil
	})
	request := httptest.NewRequest(http.MethodGet, "/items/abc?limit=many", nil)
	recorder := serve(t, http.MethodGet, "/items/:id", handler, request)
	if recorder.Code != http.StatusBadRequest {
		t.Errorf("a malformed limit answered %d, want 400", recorder.Code)
	}
}

// TestAnUndeclaredBodyMemberIsRefused holds the body half: the entry point
// decodes only the fields the declaration carries, so a member the handler
// could never read is a 400 rather than a silently dropped value.
func TestAnUndeclaredBodyMemberIsRefused(t *testing.T) {
	operation := NewOperation[readInput, struct{}](
		"the.module.write", http.MethodPost, "/items/:id", "", "",
		"application/json",
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
		"application/json",
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

// refusal is a handler failure naming a declared status.
type refusal struct{ status int }

func (r refusal) Error() string   { return "refused" }
func (r refusal) StatusCode() int { return r.status }

// TestADeclaredFailureWritesItsStatus holds the failure mapping.
func TestADeclaredFailureWritesItsStatus(t *testing.T) {
	operation := NewOperation[struct{}, struct{}](
		"the.module.read", http.MethodGet, "/items", "", "", "",
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

// TestAnUndeclaredFailureIsAnsweredInternal holds the declaration's force: a
// status the operation did not declare is never written.
func TestAnUndeclaredFailureIsAnsweredInternal(t *testing.T) {
	operation := NewOperation[struct{}, struct{}](
		"the.module.read", http.MethodGet, "/items", "", "", "",
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
		"the.module.delete", http.MethodDelete, "/items", "", "", "",
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

// jsonBody is a request body reader over a JSON string.
func jsonBody(body string) *strings.Reader { return strings.NewReader(body) }
