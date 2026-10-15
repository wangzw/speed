package http

import (
	"errors"
	"io"
	nethttp "net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

// bindContext builds a gin context the way a handler test does, outside this
// module's chain: no layer ever ran, so the request context carries no
// responder and a refusal is written in the default shape.
func bindContext(method, target string, body io.Reader) (*gin.Context, *httptest.ResponseRecorder) {
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(method, target, body)
	return c, recorder
}

// nameRequired is a body shape with its own Validate, which refuses an empty
// name. It is the third kind of failure BindJSON tells apart.
type nameRequired struct {
	Name string `json:"name"`
}

func (n *nameRequired) Validate() error {
	if n.Name == "" {
		return errors.New("name is required")
	}
	return nil
}

// TestBindJSONRefusesAnOversizedBodyAsTooLarge pins the first of the three
// failures: a body whose read crosses the endpoint's limit comes back from gin
// as an *http.MaxBytesError, and BindJSON answers it as a 413 through Fail
// rather than as one of the two shape failures.
//
// The limit here is imposed by a MaxBytesReader, which is what the body limit
// layer wraps an unknown-length body in; a request that declares an over-limit
// length is refused by that layer before a handler runs.
func TestBindJSONRefusesAnOversizedBodyAsTooLarge(t *testing.T) {
	const limit = 16
	recorder := httptest.NewRecorder()
	body := nethttp.MaxBytesReader(recorder,
		io.NopCloser(strings.NewReader(`{"name":"`+strings.Repeat("x", 64)+`"}`)), limit)
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(nethttp.MethodPost, "/things", body)

	var dst struct {
		Name string `json:"name"`
	}
	if BindJSON(c, &dst) {
		t.Fatal("BindJSON reported success for a body over the endpoint's limit")
	}
	assertRefusal(t, recorder, c, nethttp.StatusRequestEntityTooLarge)
	if detail, _ := problemBodyOf(t, recorder)["detail"].(string); detail == "" {
		t.Error("the refusal carries no detail naming what the reading failed with")
	}
}

// TestBindJSONRefusesAMalformedBodyAsABadRequest pins the second failure: a body
// that is not a JSON value dst can hold — here a truncated object — is a 400.
func TestBindJSONRefusesAMalformedBodyAsABadRequest(t *testing.T) {
	c, recorder := bindContext(nethttp.MethodPost, "/things",
		strings.NewReader(`{"name":`))

	var dst struct {
		Name string `json:"name"`
	}
	if BindJSON(c, &dst) {
		t.Fatal("BindJSON reported success for a body that is not a JSON object")
	}
	assertRefusal(t, recorder, c, nethttp.StatusBadRequest)
}

// TestBindJSONRefusesAValidatedBodyAsUnprocessable pins the third failure: a
// body that binds but whose own Validate refuses it is a 422, and the detail is
// the text Validate gave.
func TestBindJSONRefusesAValidatedBodyAsUnprocessable(t *testing.T) {
	c, recorder := bindContext(nethttp.MethodPost, "/things",
		strings.NewReader(`{"name":""}`))

	var dst nameRequired
	if BindJSON(c, &dst) {
		t.Fatal("BindJSON reported success for a body its own Validate refuses")
	}
	assertRefusal(t, recorder, c, nethttp.StatusUnprocessableEntity)
	if detail, _ := problemBodyOf(t, recorder)["detail"].(string); detail != "name is required" {
		t.Errorf("the refusal's detail is %q, want the text Validate gave", detail)
	}
}

// TestBindJSONBindsAValidBody pins the success path: a body that reads and whose
// dst has no Validate is reported true and leaves the response untouched, so the
// handler goes on to answer it.
func TestBindJSONBindsAValidBody(t *testing.T) {
	c, recorder := bindContext(nethttp.MethodPost, "/things",
		strings.NewReader(`{"name":"within"}`))

	var dst struct {
		Name string `json:"name"`
	}
	if !BindJSON(c, &dst) {
		t.Fatalf("BindJSON reported failure for a body it should have bound; the response was %q",
			recorder.Body.String())
	}
	if dst.Name != "within" {
		t.Errorf("the body bound to %q, want %q", dst.Name, "within")
	}
	assertNoResponseWritten(t, recorder, c)
}

// TestBindJSONAcceptsABodyThatValidates pins the success half of the validation
// step: a dst that has a Validate which accepts the body is reported true, so a
// shape that can be refused is not refused when it is fine.
func TestBindJSONAcceptsABodyThatValidates(t *testing.T) {
	c, recorder := bindContext(nethttp.MethodPost, "/things",
		strings.NewReader(`{"name":"within"}`))

	var dst nameRequired
	if !BindJSON(c, &dst) {
		t.Fatalf("BindJSON reported failure for a body its own Validate accepts; the response was %q",
			recorder.Body.String())
	}
	if dst.Name != "within" {
		t.Errorf("the body bound to %q, want %q", dst.Name, "within")
	}
	assertNoResponseWritten(t, recorder, c)
}

// assertRefusal checks the shape of a refusal BindJSON wrote: the status, the
// default problem details media type, the status carried in the body, and that
// the chain was ended.
func assertRefusal(t *testing.T, recorder *httptest.ResponseRecorder, c *gin.Context, status int) {
	t.Helper()
	if recorder.Code != status {
		t.Errorf("the refusal's status is %d, want %d", recorder.Code, status)
	}
	if got := recorder.Header().Get("Content-Type"); got != problemMediaType {
		t.Errorf("the refusal's media type is %q, want the default shape's %q", got, problemMediaType)
	}
	if got := problemBodyOf(t, recorder)["status"]; got != float64(status) {
		t.Errorf("the refusal's body carries status %v, want %d", got, status)
	}
	if !c.IsAborted() {
		t.Error("the refusal did not end the chain")
	}
}

// assertNoResponseWritten checks that a successful binding wrote nothing: the
// handler is the one that answers a request that bound.
func assertNoResponseWritten(t *testing.T, recorder *httptest.ResponseRecorder, c *gin.Context) {
	t.Helper()
	if c.Writer.Written() {
		t.Error("a successful binding wrote part of a response")
	}
	if recorder.Body.Len() != 0 {
		t.Errorf("a successful binding wrote the body %q", recorder.Body.String())
	}
}
