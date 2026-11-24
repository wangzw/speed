package http

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	nethttp "net/http"
	"net/http/httptest"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/gin-gonic/gin"
)

func equalProblem(a, b Problem) bool {
	return a.Type == b.Type && a.Title == b.Title && a.Status == b.Status &&
		a.Detail == b.Detail && a.Instance == b.Instance &&
		errors.Is(a.Cause, b.Cause) && errors.Is(b.Cause, a.Cause)
}

type recordingResponder struct {
	mu       sync.Mutex
	problems []Problem
	headers  []nethttp.Header
}

const (
	customMediaType = "text/x-custom-error"
	customBody      = "custom:"
)

func (r *recordingResponder) RespondError(w nethttp.ResponseWriter, _ *nethttp.Request, p Problem) {
	r.mu.Lock()
	r.problems = append(r.problems, p)
	r.headers = append(r.headers, w.Header().Clone())
	r.mu.Unlock()

	w.Header().Set("Content-Type", customMediaType)
	w.WriteHeader(p.Status)
	_, _ = io.WriteString(w, customBody+strconv.Itoa(p.Status))
}

func (r *recordingResponder) calls() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.problems)
}

func (r *recordingResponder) problem(t *testing.T, n int) Problem {
	t.Helper()
	r.mu.Lock()
	defer r.mu.Unlock()
	if n >= len(r.problems) {
		t.Fatalf("the responder was asked %d times, and the test reads call %d", len(r.problems), n)
	}
	return r.problems[n]
}

func (r *recordingResponder) headerSeen(t *testing.T, n int) nethttp.Header {
	t.Helper()
	r.mu.Lock()
	defer r.mu.Unlock()
	if n >= len(r.headers) {
		t.Fatalf("the responder was asked %d times, and the test reads call %d", len(r.headers), n)
	}
	return r.headers[n]
}

func failWith(p Problem) gin.HandlerFunc {
	return func(c *gin.Context) { Fail(c, p) }
}

func problemBodyOf(t *testing.T, recorder *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var members map[string]any
	if err := json.Unmarshal(recorder.Body.Bytes(), &members); err != nil {
		t.Fatalf("the response body %q is not a JSON object: %v", recorder.Body.String(), err)
	}
	return members
}

// TestFailWritesAProblemDetailsObject pins the default shape: the status the Problem asks for, the
// media type RFC 9457 defines, and the members that were given.
func TestFailWritesAProblemDetailsObject(t *testing.T) {
	logger, _ := newRecordingLogger()
	handler := mustAssemble(t, testSettings("public"), nil, logger, func(e Endpoint) {
		e.Route("GET", "/things", failWith(Problem{
			Type:     "https://example.test/problems/exists",
			Title:    "The thing exists",
			Status:   nethttp.StatusConflict,
			Detail:   "a thing with that name is already there",
			Instance: "/things/7",
		}))
	})

	recorder := serveOnce(handler, nethttp.MethodGet, "/things")

	if recorder.Code != nethttp.StatusConflict {
		t.Errorf("the status was %d, want %d", recorder.Code, nethttp.StatusConflict)
	}
	if got := recorder.Header().Get("Content-Type"); got != "application/problem+json" {
		t.Errorf("the media type was %q, want %q", got, "application/problem+json")
	}
	want := map[string]any{
		"type":     "https://example.test/problems/exists",
		"title":    "The thing exists",
		"status":   float64(nethttp.StatusConflict),
		"detail":   "a thing with that name is already there",
		"instance": "/things/7",
	}
	got := problemBodyOf(t, recorder)
	if len(got) != len(want) {
		t.Errorf("the body carries the members %v, want exactly %v", got, want)
	}
	for member, value := range want {
		if got[member] != value {
			t.Errorf("the member %q is %v, want %v", member, got[member], value)
		}
	}
}

// TestFailDefaultsTheTypeAndTheTitle pins what an unset Type and Title become: "about:blank", and
// the standard text of the status.
func TestFailDefaultsTheTypeAndTheTitle(t *testing.T) {
	for _, c := range []struct {
		status int
		title  string
	}{
		{nethttp.StatusNotFound, "Not Found"},
		{nethttp.StatusTeapot, "I'm a teapot"},
		{nethttp.StatusTooManyRequests, "Too Many Requests"},
	} {
		t.Run(strconv.Itoa(c.status), func(t *testing.T) {
			logger, _ := newRecordingLogger()
			handler := mustAssemble(t, testSettings("public"), nil, logger, func(e Endpoint) {
				e.Route("GET", "/things", failWith(Problem{Status: c.status}))
			})

			recorder := serveOnce(handler, nethttp.MethodGet, "/things")

			got := problemBodyOf(t, recorder)
			if got["type"] != "about:blank" {
				t.Errorf("the type is %v, want %q", got["type"], "about:blank")
			}
			if got["title"] != c.title {
				t.Errorf("the title is %v, want %q", got["title"], c.title)
			}
			if got["status"] != float64(c.status) {
				t.Errorf("the status member is %v, want %d", got["status"], c.status)
			}
			for _, absent := range []string{"detail", "instance"} {
				if _, present := got[absent]; present {
					t.Errorf("the body carries %q although nothing was given for it: %v", absent, got)
				}
			}
		})
	}
}

// TestFailLeavesTheCauseOutOfTheBody pins that the default response says nothing of what a Cause
// carries.
func TestFailLeavesTheCauseOutOfTheBody(t *testing.T) {
	const secret = "dial tcp db-7.internal:5432 password=hunter2"
	logger, _ := newRecordingLogger()
	handler := mustAssemble(t, testSettings("public"), nil, logger, func(e Endpoint) {
		e.Route("GET", "/things", failWith(Problem{
			Status: nethttp.StatusBadGateway,
			Cause:  errors.New(secret),
		}))
	})

	recorder := serveOnce(handler, nethttp.MethodGet, "/things")

	for _, leak := range []string{"hunter2", "db-7.internal"} {
		if strings.Contains(recorder.Body.String(), leak) {
			t.Errorf("the body carries %q from the Cause: %s", leak, recorder.Body.String())
		}
		if strings.Contains(fmt.Sprint(recorder.Header()), leak) {
			t.Errorf("a header carries %q from the Cause: %v", leak, recorder.Header())
		}
	}
	if _, present := problemBodyOf(t, recorder)["cause"]; present {
		t.Error("the body has a cause member")
	}
	if recorder.Code != nethttp.StatusBadGateway {
		t.Errorf("the status was %d, want %d", recorder.Code, nethttp.StatusBadGateway)
	}
}

// TestFailWithAnIllegalStatusWrites500 pins what a status that is no final status code becomes.
func TestFailWithAnIllegalStatusWrites500(t *testing.T) {
	for _, status := range []int{0, -1, 99, 100, 103, 199, 600, 1000} {
		t.Run(strconv.Itoa(status), func(t *testing.T) {
			logger, _ := newRecordingLogger()
			handler := mustAssemble(t, testSettings("public"), nil, logger, func(e Endpoint) {
				e.Route("GET", "/things", failWith(Problem{Status: status}))
			})

			recorder := serveOnce(handler, nethttp.MethodGet, "/things")

			if recorder.Code != nethttp.StatusInternalServerError {
				t.Errorf("a Problem with the status %d was written with %d, want %d",
					status, recorder.Code, nethttp.StatusInternalServerError)
			}
			members := problemBodyOf(t, recorder)
			if got := members["status"]; got != float64(nethttp.StatusInternalServerError) {
				t.Errorf("the body says the status is %v, and the response was written with %d",
					got, nethttp.StatusInternalServerError)
			}
			if got, want := members["title"], nethttp.StatusText(nethttp.StatusInternalServerError); got != want {
				t.Errorf("the title is %v, want %q: it is the standard text of the status that was "+
					"written, not of the number that was asked for", got, want)
			}
		})
	}
}

// TestAnIllegalStatusReachesTheClientAs500 is the same statement on a real connection, where it
// matters: net/http answers a 1xx code written alone with an informational response and then a 200.
func TestAnIllegalStatusReachesTheClientAs500(t *testing.T) {
	addr, _ := serveRoutes(t, nil, func(e Endpoint) {
		e.Route("GET", "/status/:code", func(c *gin.Context) {
			code, err := strconv.Atoi(c.Param("code"))
			if err != nil {
				c.String(nethttp.StatusBadRequest, "the test asked for the status %q", c.Param("code"))
				return
			}
			Fail(c, Problem{Status: code})
		})
	})

	for _, c := range []struct{ asked, answered int }{
		{0, 500},
		{100, 500},
		{103, 500},
		{199, 500},
		{600, 500},
		{999, 500},
		{200, 200},
		{599, 599},
	} {
		got, err := getResponse(context.Background(), addr, "/status/"+strconv.Itoa(c.asked))
		if err != nil {
			t.Fatalf("Status %d: the request failed: %v", c.asked, err)
		}
		if got.status != c.answered {
			t.Errorf("Status %d reached the client as %d, want %d", c.asked, got.status, c.answered)
		}
		var members map[string]any
		if err := json.Unmarshal([]byte(got.body), &members); err != nil {
			t.Fatalf("Status %d: the body %q is not a problem details object: %v", c.asked, got.body, err)
		}
		if members["status"] != float64(c.answered) {
			t.Errorf("Status %d: the body says the status is %v, want %d", c.asked, members["status"], c.answered)
		}
	}
}

// TestTheTitleIsLeftOutWhenTheStatusHasNoStandardText pins what an unset Title becomes for a status
// the standard library has no text for.
func TestTheTitleIsLeftOutWhenTheStatusHasNoStandardText(t *testing.T) {
	for _, c := range []struct {
		name      string
		problem   Problem
		wantTitle any
	}{
		{"no standard text", Problem{Status: 299}, nil},
		{"no standard text at the top of the range", Problem{Status: 599}, nil},
		{"a standard text", Problem{Status: nethttp.StatusNotFound}, "Not Found"},
		{"a title given for a status with a standard text", Problem{Status: nethttp.StatusNotFound, Title: "No such thing"}, "No such thing"},
		{"a title given for a status without one", Problem{Status: 299, Title: "Accepted with a caveat"}, "Accepted with a caveat"},
	} {
		t.Run(c.name, func(t *testing.T) {
			logger, _ := newRecordingLogger()
			handler := mustAssemble(t, testSettings("public"), nil, logger, func(e Endpoint) {
				e.Route("GET", "/things", failWith(c.problem))
			})

			members := problemBodyOf(t, serveOnce(handler, nethttp.MethodGet, "/things"))

			title, present := members["title"]
			if c.wantTitle == nil && present {
				t.Errorf("the body carries the title %q for a status that has no standard text: %v", title, members)
			}
			if c.wantTitle != nil && title != c.wantTitle {
				t.Errorf("the title is %v, want %v", title, c.wantTitle)
			}
			if members["type"] != "about:blank" {
				t.Errorf("the type is %v, want %q: leaving the title out does not touch the rest", members["type"], "about:blank")
			}
		})
	}
}

// TestFailWritesTheFinalStatusesAtTheEdgesOfTheLegalRange is the other side of that check: 200 and
// 599 are final statuses and are written as they are, with the body saying the same.
func TestFailWritesTheFinalStatusesAtTheEdgesOfTheLegalRange(t *testing.T) {
	for _, status := range []int{nethttp.StatusOK, 599} {
		t.Run(strconv.Itoa(status), func(t *testing.T) {
			logger, _ := newRecordingLogger()
			handler := mustAssemble(t, testSettings("public"), nil, logger, func(e Endpoint) {
				e.Route("GET", "/things", failWith(Problem{Status: status}))
			})

			recorder := serveOnce(handler, nethttp.MethodGet, "/things")

			if recorder.Code != status {
				t.Errorf("a Problem with the status %d was written with %d", status, recorder.Code)
			}
			if got := problemBodyOf(t, recorder)["status"]; got != float64(status) {
				t.Errorf("the body says the status is %v, want %d", got, status)
			}
		})
	}
}

// TestFailEndsTheChain pins what Fail does to the handlers after the one that called it: they do
// not run, and the layers around it see the chain as aborted with the status that was written.
func TestFailEndsTheChain(t *testing.T) {
	tr := &trace{}
	var aborted bool
	var status int
	logger, _ := newRecordingLogger()
	handler := mustAssemble(t, testSettings("public"), nil, logger, func(e Endpoint) {
		e.Use(Middleware{Name: "observer", Stage: StageObserve, Handler: func(c *gin.Context) {
			c.Next()
			aborted, status = c.IsAborted(), c.Writer.Status()
		}})
		e.Route("GET", "/things",
			failWith(Problem{Status: nethttp.StatusForbidden}),
			noteHandler(tr, "after-the-failure"),
		)
	})

	serveOnce(handler, nethttp.MethodGet, "/things")

	if got := tr.seen(); len(got) != 0 {
		t.Errorf("the handlers %v ran after Fail", got)
	}
	if !aborted || status != nethttp.StatusForbidden {
		t.Errorf("the layer around the failure saw aborted=%v status=%d, want true and %d",
			aborted, status, nethttp.StatusForbidden)
	}
}

// TestFailKeepsTheHeadersSetBeforeIt pins that a header a layer sets before it refuses the request
// is on the response: the challenge an authentication layer sends with a 401.
func TestFailKeepsTheHeadersSetBeforeIt(t *testing.T) {
	refuse := func(c *gin.Context) {
		c.Header("WWW-Authenticate", `Bearer realm="things"`)
		c.Header("Retry-After", "30")
		Fail(c, Problem{Status: nethttp.StatusUnauthorized})
	}
	for _, c := range []struct {
		name      string
		responder *recordingResponder
	}{
		{"the default shape", nil},
		{"a delivered responder", &recordingResponder{}},
	} {
		t.Run(c.name, func(t *testing.T) {
			logger, _ := newRecordingLogger()
			var responder ErrorResponder
			if c.responder != nil {
				responder = c.responder
			}
			handler, err := assembleEndpointWith(t, testSettings("public"), nil, logger, responder, nil, func(e Endpoint) {
				e.Route("GET", "/things", refuse)
			})
			if err != nil {
				t.Fatalf("assembling: %v", err)
			}

			recorder := serveOnce(handler, nethttp.MethodGet, "/things")

			if got := recorder.Header().Get("WWW-Authenticate"); got != `Bearer realm="things"` {
				t.Errorf("WWW-Authenticate is %q on the response", got)
			}
			if got := recorder.Header().Get("Retry-After"); got != "30" {
				t.Errorf("Retry-After is %q on the response", got)
			}
			if c.responder != nil {
				seen := c.responder.headerSeen(t, 0)
				if seen.Get("Retry-After") != "30" || seen.Get("WWW-Authenticate") == "" {
					t.Errorf("the responder was handed a writer with the headers %v, which lost "+
						"what the layer set before it refused", seen)
				}
			}
		})
	}
}

// TestFailWritesNothingOnceTheResponseHasStarted pins the other half of Fail's contract: a response
// that has begun to go out is not rewritten.
func TestFailWritesNothingOnceTheResponseHasStarted(t *testing.T) {
	for _, c := range []struct {
		name       string
		start      gin.HandlerFunc
		wantStatus int
		wantBody   string
	}{
		{"a body already written", func(c *gin.Context) { c.String(nethttp.StatusOK, "partial") }, nethttp.StatusOK, "partial"},
		{"the header already committed", func(c *gin.Context) {
			c.Status(nethttp.StatusAccepted)
			c.Writer.WriteHeaderNow()
		}, nethttp.StatusAccepted, ""},
	} {
		t.Run(c.name, func(t *testing.T) {
			tr := &trace{}
			responder := &recordingResponder{}
			logger, _ := newRecordingLogger()
			handler, err := assembleEndpointWith(t, testSettings("public"), nil, logger, responder, nil, func(e Endpoint) {
				e.Route("GET", "/things",
					c.start,
					failWith(Problem{Status: nethttp.StatusConflict, Detail: "too late"}),
					noteHandler(tr, "after-the-failure"),
				)
			})
			if err != nil {
				t.Fatalf("assembling: %v", err)
			}

			recorder := serveOnce(handler, nethttp.MethodGet, "/things")

			if recorder.Code != c.wantStatus {
				t.Errorf("the status was %d, want the %d already written", recorder.Code, c.wantStatus)
			}
			if got := recorder.Body.String(); got != c.wantBody {
				t.Errorf("the body was %q, want the %q already written: Fail wrote after the "+
					"response had started", got, c.wantBody)
			}
			if responder.calls() != 0 {
				t.Errorf("the responder was asked %d times for a response that had started", responder.calls())
			}
			if got := tr.seen(); len(got) != 0 {
				t.Errorf("the handlers %v ran after Fail: it ends the chain even when it writes nothing", got)
			}
		})
	}
}

// TestADeliveredResponderReplacesTheWholeShape pins what delivering an ErrorResponder changes: the
// body and the media type are the responder's, the status is the one it writes from the Problem.
func TestADeliveredResponderReplacesTheWholeShape(t *testing.T) {
	cause := errors.New("the store said no")
	given := Problem{
		Type:     "https://example.test/problems/exists",
		Title:    "The thing exists",
		Status:   nethttp.StatusConflict,
		Detail:   "a thing with that name is already there",
		Instance: "/things/7",
		Cause:    cause,
	}
	responder := &recordingResponder{}
	logger, _ := newRecordingLogger()
	handler, err := assembleEndpointWith(t, testSettings("public"), nil, logger, responder, nil, func(e Endpoint) {
		e.Route("GET", "/things", failWith(given))
	})
	if err != nil {
		t.Fatalf("assembling: %v", err)
	}

	recorder := serveOnce(handler, nethttp.MethodGet, "/things")

	if responder.calls() != 1 {
		t.Fatalf("the responder was asked %d times, want once", responder.calls())
	}
	if got := responder.problem(t, 0); !equalProblem(got, given) {
		t.Errorf("the responder was handed %+v, want the whole of %+v, Cause included", got, given)
	}
	if got := recorder.Header().Get("Content-Type"); got != customMediaType {
		t.Errorf("the media type was %q, want the responder's %q", got, customMediaType)
	}
	if got, want := recorder.Body.String(), customBody+"409"; got != want {
		t.Errorf("the body was %q, want the responder's %q", got, want)
	}
	if recorder.Code != nethttp.StatusConflict {
		t.Errorf("the status was %d, want the %d the responder wrote", recorder.Code, nethttp.StatusConflict)
	}
}

// TestFailOnAContextOutsideTheChainWritesTheDefaultShape pins the answer when no responder can be
// found: the request context carries one only if this module's chain put it there.
func TestFailOnAContextOutsideTheChainWritesTheDefaultShape(t *testing.T) {
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(nethttp.MethodGet, "/things", nil)

	Fail(c, Problem{Status: nethttp.StatusGone, Detail: "gone for good"})

	if recorder.Code != nethttp.StatusGone {
		t.Errorf("the status was %d, want %d", recorder.Code, nethttp.StatusGone)
	}
	if got := recorder.Header().Get("Content-Type"); got != "application/problem+json" {
		t.Errorf("the media type was %q, want %q", got, "application/problem+json")
	}
	if got := problemBodyOf(t, recorder)["detail"]; got != "gone for good" {
		t.Errorf("the detail member is %v", got)
	}
	if !c.IsAborted() {
		t.Error("Fail did not end the chain of a context outside one")
	}
}

// TestARequestMatchingNoRouteIsAnsweredThroughEveryLayer pins the 404.
func TestARequestMatchingNoRouteIsAnsweredThroughEveryLayer(t *testing.T) {
	var runs int
	var fullPath string
	var status int
	logger, _ := newRecordingLogger()
	handler := mustAssemble(t, testSettings("public"), nil, logger, func(e Endpoint) {
		e.Use(Middleware{Name: "observer", Stage: StageObserve, Handler: func(c *gin.Context) {
			runs++
			fullPath = c.FullPath()
			c.Next()
			status = c.Writer.Status()
		}})
		e.Route("GET", "/things", filler)
	})

	recorder := serveOnce(handler, nethttp.MethodGet, "/nothing-here")

	if runs != 1 {
		t.Errorf("the layer ran %d times for a request that matched no route, want once", runs)
	}
	if fullPath != "" {
		t.Errorf("the layer read the route %q for a request that matched none", fullPath)
	}
	if status != nethttp.StatusNotFound || recorder.Code != nethttp.StatusNotFound {
		t.Errorf("the layer saw status %d and the response was %d, want %d for both",
			status, recorder.Code, nethttp.StatusNotFound)
	}
	if got := recorder.Header().Get("Content-Type"); got != "application/problem+json" {
		t.Errorf("the media type was %q, want %q", got, "application/problem+json")
	}
	if got := problemBodyOf(t, recorder)["title"]; got != "Not Found" {
		t.Errorf("the title is %v, want %q", got, "Not Found")
	}
}

// TestAMethodTheRouteDoesNotAllowIsAnsweredThroughEveryLayer is the same for the 405: the path
// exists and the method is not among its methods, so the answer is not a 404.
func TestAMethodTheRouteDoesNotAllowIsAnsweredThroughEveryLayer(t *testing.T) {
	var runs int
	var status int
	logger, _ := newRecordingLogger()
	handler := mustAssemble(t, testSettings("public"), nil, logger, func(e Endpoint) {
		e.Use(Middleware{Name: "observer", Stage: StageObserve, Handler: func(c *gin.Context) {
			runs++
			c.Next()
			status = c.Writer.Status()
		}})
		e.Route("GET", "/things", filler)
		e.Route("PUT", "/things", filler)
	})

	recorder := serveOnce(handler, nethttp.MethodDelete, "/things")

	if runs != 1 {
		t.Errorf("the layer ran %d times for a method the path does not allow, want once", runs)
	}
	if status != nethttp.StatusMethodNotAllowed || recorder.Code != nethttp.StatusMethodNotAllowed {
		t.Errorf("the layer saw status %d and the response was %d, want %d for both",
			status, recorder.Code, nethttp.StatusMethodNotAllowed)
	}
	allowed := strings.Split(recorder.Header().Get("Allow"), ", ")
	slices.Sort(allowed)
	if want := []string{"GET", "PUT"}; !slices.Equal(allowed, want) {
		t.Errorf("the Allow header lists %v, want %v", allowed, want)
	}
	if got := recorder.Header().Get("Content-Type"); got != "application/problem+json" {
		t.Errorf("the media type was %q, want %q", got, "application/problem+json")
	}
	if got := problemBodyOf(t, recorder)["title"]; got != "Method Not Allowed" {
		t.Errorf("the title is %v, want %q", got, "Method Not Allowed")
	}
}

// TestALayerRefusingAMethodNotAllowedRequestDoesNotSendAllow pins what the answer to a method the
// path does not allow keeps back until every layer has let the request through.
func TestALayerRefusingAMethodNotAllowedRequestDoesNotSendAllow(t *testing.T) {
	var atEntry string
	logger, _ := newRecordingLogger()
	handler := mustAssemble(t, testSettings("public"), nil, logger, func(e Endpoint) {
		e.Use(Middleware{Name: "gate", Stage: StageIdentify, Handler: func(c *gin.Context) {
			atEntry = c.Writer.Header().Get("Allow")
			if c.GetHeader("Authorization") == "" {
				c.Header("WWW-Authenticate", "Bearer")
				Fail(c, Problem{Status: nethttp.StatusUnauthorized})
				return
			}
			c.Next()
		}})
		e.Route("GET", "/things", filler)
	})

	refused := serveOnce(handler, nethttp.MethodDelete, "/things")

	if refused.Code != nethttp.StatusUnauthorized {
		t.Fatalf("the request with no credentials was answered %d, want %d from the layer",
			refused.Code, nethttp.StatusUnauthorized)
	}
	if got := refused.Header().Get("Allow"); got != "" {
		t.Errorf("the layer's refusal carries Allow %q: a caller that has not proved who it is was "+
			"told which methods the path takes", got)
	}
	if atEntry != "" {
		t.Errorf("the layer found Allow %q on the response when it was reached", atEntry)
	}
	if got := refused.Header().Get("WWW-Authenticate"); got != "Bearer" {
		t.Errorf("the layer's own header is %q on its refusal, want it kept", got)
	}

	request := httptest.NewRequest(nethttp.MethodDelete, "/things", nil)
	request.Header.Set("Authorization", "Bearer token")
	allowed := httptest.NewRecorder()
	handler.ServeHTTP(allowed, request)

	if allowed.Code != nethttp.StatusMethodNotAllowed {
		t.Fatalf("the request with credentials was answered %d, want %d", allowed.Code, nethttp.StatusMethodNotAllowed)
	}
	if got := allowed.Header().Get("Allow"); got != "GET" {
		t.Errorf("the 405 carries Allow %q once the layers let the request through, want %q", got, "GET")
	}
}

// TestNotFoundAndMethodNotAllowedUseTheDeliveredResponder pins that the two answers this module
// writes on its own are decided in the same place as the ones Fail writes.
func TestNotFoundAndMethodNotAllowedUseTheDeliveredResponder(t *testing.T) {
	responder := &recordingResponder{}
	logger, _ := newRecordingLogger()
	handler, err := assembleEndpointWith(t, testSettings("public"), nil, logger, responder, nil, func(e Endpoint) {
		e.Route("GET", "/things", filler)
	})
	if err != nil {
		t.Fatalf("assembling: %v", err)
	}

	missing := serveOnce(handler, nethttp.MethodGet, "/nothing-here")
	refused := serveOnce(handler, nethttp.MethodPost, "/things")

	for _, c := range []struct {
		name     string
		recorder *httptest.ResponseRecorder
		status   int
	}{
		{"the 404", missing, nethttp.StatusNotFound},
		{"the 405", refused, nethttp.StatusMethodNotAllowed},
	} {
		if c.recorder.Code != c.status {
			t.Errorf("%s was written with %d", c.name, c.recorder.Code)
		}
		if got := c.recorder.Header().Get("Content-Type"); got != customMediaType {
			t.Errorf("%s has the media type %q, want the responder's %q", c.name, got, customMediaType)
		}
		if got, want := c.recorder.Body.String(), customBody+strconv.Itoa(c.status); got != want {
			t.Errorf("%s has the body %q, want the responder's %q", c.name, got, want)
		}
	}
	if responder.calls() != 2 {
		t.Fatalf("the responder was asked %d times for a 404 and a 405, want twice", responder.calls())
	}
	if got := responder.problem(t, 0); !equalProblem(got, Problem{
		Status: nethttp.StatusNotFound,
		Title:  nethttp.StatusText(nethttp.StatusNotFound),
	}) {
		t.Errorf("the 404 reached the responder as %+v", got)
	}
	if got := responder.problem(t, 1); !equalProblem(got, Problem{
		Status: nethttp.StatusMethodNotAllowed,
		Title:  nethttp.StatusText(nethttp.StatusMethodNotAllowed),
	}) {
		t.Errorf("the 405 reached the responder as %+v", got)
	}
	if got := responder.headerSeen(t, 1).Get("Allow"); got != "GET" {
		t.Errorf("the responder saw Allow %q on the 405, want %q", got, "GET")
	}
}

var sampleRepresentation = map[string]string{
	"Content-Disposition": `attachment; filename="report.csv"`,
	"ETag":                `"v1"`,
	"Last-Modified":       "Sat, 10 Oct 2026 02:29:23 GMT",
	"Expires":             "Sat, 10 Oct 2026 03:29:23 GMT",
	"Accept-Ranges":       "bytes",
	"Content-Range":       "bytes 0-9/100",
	"Cache-Control":       "public, max-age=3600",
}

func setSampleRepresentation(c *gin.Context) {
	for name, value := range sampleRepresentation {
		c.Header(name, value)
	}
}

// TestTheDefaultShapeDropsTheHeadersThatDescribeAnotherBody pins what the default response does
// with the headers the caller set before it failed.
func TestTheDefaultShapeDropsTheHeadersThatDescribeAnotherBody(t *testing.T) {
	setDescribing := func(c *gin.Context) {
		c.Header("Content-Length", "1000")
		c.Header("Content-Encoding", "gzip")
		setSampleRepresentation(c)
		c.Writer.Header().Add("Set-Cookie", "session=abc; Path=/")
		c.Header("X-Test", "kept")
		c.Header("WWW-Authenticate", "Bearer")
		c.Header("Retry-After", "30")
	}
	addr, _ := serveRoutes(t, nil, func(e Endpoint) {
		e.Use(Middleware{Name: "refuser", Stage: StageGuard, Handler: func(c *gin.Context) {
			if c.Request.URL.Path != "/by-layer" {
				c.Next()
				return
			}
			setDescribing(c)
			c.Header("Cache-Control", "no-store")
			Fail(c, Problem{Status: nethttp.StatusUnauthorized, Detail: "refused by a layer"})
		}})
		e.Route("GET", "/by-panic", func(c *gin.Context) {
			setDescribing(c)
			panic("after the headers")
		})
		e.Route("GET", "/by-handler", func(c *gin.Context) {
			setDescribing(c)
			Fail(c, Problem{Status: nethttp.StatusConflict, Detail: "refused by a handler"})
		})
		e.Route("GET", "/by-layer", filler)
	})

	for _, c := range []struct {
		name   string
		path   string
		status int
		kept   map[string]string
	}{
		{"a panic answered by the fallback", "/by-panic", nethttp.StatusInternalServerError, nil},
		{"a refusal by a handler", "/by-handler", nethttp.StatusConflict, sampleRepresentation},
		{"a refusal by a layer", "/by-layer", nethttp.StatusUnauthorized, map[string]string{
			"Content-Disposition": sampleRepresentation["Content-Disposition"],
			"ETag":                sampleRepresentation["ETag"],
			"Last-Modified":       sampleRepresentation["Last-Modified"],
			"Expires":             sampleRepresentation["Expires"],
			"Accept-Ranges":       sampleRepresentation["Accept-Ranges"],
			"Content-Range":       sampleRepresentation["Content-Range"],
			"Cache-Control":       "no-store",
		}},
	} {
		t.Run(c.name, func(t *testing.T) {
			got, err := getResponse(context.Background(), addr, c.path)
			if err != nil {
				t.Fatalf("the request failed outright: %v", err)
			}
			if got.bodyErr != nil {
				t.Fatalf("reading the body ended with %v: the response declared a length its body "+
					"does not have", got.bodyErr)
			}
			if got.status != c.status {
				t.Errorf("the status was %d, want %d", got.status, c.status)
			}
			if got.header.Get("Content-Type") != "application/problem+json" {
				t.Errorf("the media type was %q", got.header.Get("Content-Type"))
			}
			var members map[string]any
			if err := json.Unmarshal([]byte(got.body), &members); err != nil {
				t.Fatalf("the body %q is not a complete problem details object: %v", got.body, err)
			}
			if members["status"] != float64(c.status) {
				t.Errorf("the body says the status is %v, want %d", members["status"], c.status)
			}
			if got.header.Get("Content-Length") == "1000" {
				t.Error("the response carries the Content-Length the caller set for a body of its own")
			}
			if got.header.Get("Content-Encoding") != "" {
				t.Errorf("the response carries Content-Encoding %q for a body that is not encoded",
					got.header.Get("Content-Encoding"))
			}
			for name := range sampleRepresentation {
				want, kept := c.kept[name]
				switch {
				case kept && got.header.Get(name) != want:
					t.Errorf("the header %s is %q, want the %q the caller set before failing", name,
						got.header.Get(name), want)
				case !kept && got.header.Get(name) != "":
					t.Errorf("the header %s is %q on the answer to a panic: it describes content the "+
						"handler meant to send and this response is not that", name, got.header.Get(name))
				}
			}
			for header, want := range map[string]string{
				"Set-Cookie":       "session=abc; Path=/",
				"X-Test":           "kept",
				"WWW-Authenticate": "Bearer",
				"Retry-After":      "30",
			} {
				if got.header.Get(header) != want {
					t.Errorf("the header %s is %q, want the %q the caller set before failing", header,
						got.header.Get(header), want)
				}
			}
		})
	}
}

// TestADeliveredResponderIsHandedTheHeadersAsTheCallerLeftThem is the other half: dropping the
// headers that describe another body is the default shape's doing.
func TestADeliveredResponderIsHandedTheHeadersAsTheCallerLeftThem(t *testing.T) {
	responder := &recordingResponder{}
	logger, _ := newRecordingLogger()
	handler, err := assembleEndpointWith(t, testSettings("public"), nil, logger, responder, nil, func(e Endpoint) {
		set := func(c *gin.Context) {
			c.Header("Content-Length", "1000")
			c.Header("Content-Encoding", "gzip")
			setSampleRepresentation(c)
			c.Header("X-Test", "kept")
		}
		e.Route("GET", "/by-handler", func(c *gin.Context) {
			set(c)
			Fail(c, Problem{Status: nethttp.StatusConflict})
		})
		e.Route("GET", "/by-panic", func(c *gin.Context) {
			set(c)
			panic("after the headers")
		})
	})
	if err != nil {
		t.Fatalf("assembling: %v", err)
	}

	serveOnce(handler, nethttp.MethodGet, "/by-handler")
	serveOnce(handler, nethttp.MethodGet, "/by-panic")

	if responder.calls() != 2 {
		t.Fatalf("the responder was asked %d times, want twice", responder.calls())
	}
	for i, way := range []string{"a refusal through Fail", "a panic"} {
		seen := responder.headerSeen(t, i)
		if seen.Get("Content-Length") != "1000" || seen.Get("Content-Encoding") != "gzip" ||
			seen.Get("X-Test") != "kept" {
			t.Errorf("%s: the responder was handed the headers %v, want every one the caller set", way, seen)
		}
		for name, want := range sampleRepresentation {
			if seen.Get(name) != want {
				t.Errorf("%s: the responder was handed %s %q, want the %q the caller set", way, name,
					seen.Get(name), want)
			}
		}
	}
}
