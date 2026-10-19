package http

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestFailuresMergesCommonAndSpecific holds the merge rule: an operation's
// response replaces the common one with the same status, and a status only one
// side declares is added.
func TestFailuresMergesCommonAndSpecific(t *testing.T) {
	common := []Response{
		ProblemResponse(http.StatusUnauthorized, "No credential"),
		ProblemResponse(http.StatusForbidden, "Not allowed"),
		ProblemResponse(http.StatusServiceUnavailable, "Unavailable"),
	}
	merged := Failures(common,
		ProblemResponse(http.StatusForbidden, "Impersonation is restricted"),
		ProblemResponse(http.StatusConflict, "Already there"),
	)
	if len(merged) != 4 {
		t.Fatalf("the merge holds %d responses, want 4: %v", len(merged), merged)
	}
	byStatus := map[int]Response{}
	for _, response := range merged {
		byStatus[response.Status] = response
	}
	if got := byStatus[http.StatusForbidden].Description; got != "Impersonation is restricted" {
		t.Errorf("the forbidden response reads %q, want the operation's override", got)
	}
	if _, ok := byStatus[http.StatusConflict]; !ok {
		t.Errorf("the merge dropped the operation's own status: %v", merged)
	}
	if len(common) != 3 {
		t.Errorf("the merge changed the common slice to %v", common)
	}
}

// TestAReturnedFaultWritesItsProblem holds the handler-returned path: the
// fault's status and problem fields reach the answer.
func TestAReturnedFaultWritesItsProblem(t *testing.T) {
	operation := NewOperation[struct{}, struct{}](
		"the.module.read", http.MethodGet, "/items", "", "", "",
		Failures([]Response{{Status: 204, Description: "No content"}},
			ProblemResponse(http.StatusConflict, "Conflict")),
	)
	handler := operation.Handle(func(context.Context, struct{}) (struct{}, error) {
		return struct{}{}, Fault{Status: http.StatusConflict, Type: "urn:test:conflict", Detail: "already there"}
	})
	recorder := serve(t, http.MethodGet, "/items", handler,
		httptest.NewRequest(http.MethodGet, "/items", nil))
	if recorder.Code != http.StatusConflict {
		t.Fatalf("a returned fault answered %d, want 409", recorder.Code)
	}
	var problem map[string]any
	if err := json.Unmarshal(recorder.Body.Bytes(), &problem); err != nil {
		t.Fatalf("the answer is not JSON: %v", err)
	}
	if problem["type"] != "urn:test:conflict" {
		t.Errorf("the problem type reads %v, want the fault's type", problem["type"])
	}
	if problem["detail"] != "already there" {
		t.Errorf("the problem detail reads %v, want the fault's detail", problem["detail"])
	}
}

// TestADeclaredFaultPanicIsAnswered holds the panic path: a handler that
// panics with a fault the operation declared is answered with that fault, by
// the recovery layer, without being treated as a defect.
func TestADeclaredFaultPanicIsAnswered(t *testing.T) {
	operation := NewOperation[struct{}, struct{}](
		"the.module.read", http.MethodGet, "/items", "", "", "",
		Failures([]Response{{Status: 204, Description: "No content"}},
			ProblemResponse(http.StatusConflict, "Conflict")),
	)
	handler := operation.Handle(func(context.Context, struct{}) (struct{}, error) {
		panic(Fault{Status: http.StatusConflict, Type: "urn:test:conflict", Detail: "already there"})
	})
	logger, _ := newRecordingLogger()
	assembled := assembleFallback(t, nil, logger, func(e Endpoint) {
		e.Route(http.MethodGet, "/items", handler)
	})
	recorder := serveOnce(assembled, http.MethodGet, "/items")
	if recorder.Code != http.StatusConflict {
		t.Fatalf("a declared fault panic answered %d, want 409", recorder.Code)
	}
	var problem map[string]any
	if err := json.Unmarshal(recorder.Body.Bytes(), &problem); err != nil {
		t.Fatalf("the answer is not JSON: %v", err)
	}
	if problem["type"] != "urn:test:conflict" {
		t.Errorf("the problem type reads %v, want the fault's type", problem["type"])
	}
}

// TestAnUndeclaredFaultPanicIsAServerError holds the declaration's force on the
// panic path: a status the operation did not declare is never written.
func TestAnUndeclaredFaultPanicIsAServerError(t *testing.T) {
	operation := NewOperation[struct{}, struct{}](
		"the.module.read", http.MethodGet, "/items", "", "", "",
		[]Response{{Status: 204, Description: "No content"}},
	)
	handler := operation.Handle(func(context.Context, struct{}) (struct{}, error) {
		panic(Fault{Status: http.StatusConflict, Type: "urn:test:conflict"})
	})
	logger, _ := newRecordingLogger()
	assembled := assembleFallback(t, nil, logger, func(e Endpoint) {
		e.Route(http.MethodGet, "/items", handler)
	})
	recorder := serveOnce(assembled, http.MethodGet, "/items")
	if recorder.Code != http.StatusInternalServerError {
		t.Errorf("an undeclared fault panic answered %d, want 500", recorder.Code)
	}
}

// TestAFaultPanicWithoutADeclarationIsAServerError holds the same force for a
// route that carries no operation declaration: the recovery layer has no set to
// consult, so a fault panic is a defect like any other.
func TestAFaultPanicWithoutADeclarationIsAServerError(t *testing.T) {
	logger, _ := newRecordingLogger()
	assembled := assembleFallback(t, nil, logger, func(e Endpoint) {
		e.Route(http.MethodGet, "/things", panicking(Fault{Status: http.StatusConflict}))
	})
	recorder := serveOnce(assembled, http.MethodGet, "/things")
	if recorder.Code != http.StatusInternalServerError {
		t.Errorf("a fault panic on an undeclared route answered %d, want 500", recorder.Code)
	}
}

// TestACustomResponderWritesTheReturnedFault holds the entry point's custom
// error handling: the hosted responder receives the fault as a problem and its
// own body is what the client gets, so a host can replace the error shape while
// keeping the typed handler boundary.
func TestACustomResponderWritesTheReturnedFault(t *testing.T) {
	operation := NewOperation[struct{}, struct{}](
		"the.module.read", http.MethodGet, "/items", "", "", "",
		Failures([]Response{{Status: 204, Description: "No content"}},
			ProblemResponse(http.StatusConflict, "Conflict")),
	)
	handler := operation.Handle(func(context.Context, struct{}) (struct{}, error) {
		return struct{}{}, Fault{Status: http.StatusConflict, Type: "urn:test:conflict"}
	})
	responder := &recordingResponder{}
	logger, _ := newRecordingLogger()
	assembled := assembleFallback(t, responder, logger, func(e Endpoint) {
		e.Route(http.MethodGet, "/items", handler)
	})
	recorder := serveOnce(assembled, http.MethodGet, "/items")
	if recorder.Code != http.StatusConflict {
		t.Fatalf("the fault answered %d, want 409", recorder.Code)
	}
	if got := recorder.Header().Get("Content-Type"); got != customMediaType {
		t.Errorf("the answer's media type reads %q, want the responder's %q", got, customMediaType)
	}
	if got := recorder.Body.String(); got != customBody+"409" {
		t.Errorf("the answer body reads %q, want the responder's own", got)
	}
	if got := responder.problem(t, 0); got.Type != "urn:test:conflict" {
		t.Errorf("the responder was handed %+v, want the fault's type", got)
	}
}
