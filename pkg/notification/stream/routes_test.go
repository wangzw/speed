package stream

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/vislake/speed/pkg/authn/authntest"
)

// The route answers the stream content type. Its identity and flusher are
// settled and the header is set before the frame loop, so a context already
// cancelled lets the loop end at once while the headers stay on the response.
func TestTheRouteSetsTheEventStreamContentType(t *testing.T) {
	rig := newRig(t)
	ctx, cancel := context.WithCancel(authntest.Context(t, context.Background()))
	cancel()

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, pathStream, nil).WithContext(ctx)
	rig.engine.ServeHTTP(w, req)

	if got := w.Header().Get("Content-Type"); got != "text/event-stream" {
		t.Errorf("Content-Type is %q, want text/event-stream", got)
	}
	if w.Code != http.StatusOK {
		t.Errorf("status is %d, want 200", w.Code)
	}
}

// A request with no identity is a 401: there is no recipient to scope a
// connection to.
func TestTheRouteAnswers401WithoutAnIdentity(t *testing.T) {
	rig := newRig(t)
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, pathStream, nil)
	rig.engine.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("status is %d, want 401", w.Code)
	}
}

// A frame is the event name, the JSON data line, and a blank line: the shape a
// connected client parses.
func TestTheFrameIsAnEventNamedMessageWithJsonData(t *testing.T) {
	w := httptest.NewRecorder()
	if err := writeFrame(w, w, frame{event: frameEventName, data: []byte(`{"message_id":"message-1"}`)}); err != nil {
		t.Fatalf("writeFrame failed: %v", err)
	}
	want := "event: message\ndata: {\"message_id\":\"message-1\"}\n\n"
	if got := w.Body.String(); got != want {
		t.Errorf("the frame is %q, want %q", got, want)
	}
}

// flusherOf finds the flusher down an Unwrap chain and reports none when the
// chain ends without one.
func TestFlusherOfWalksUnwrapToFindAFlusher(t *testing.T) {
	recorder := httptest.NewRecorder()

	if f, ok := flusherOf(wrapper{w: recorder}); !ok || f == nil {
		t.Error("a flusher behind an Unwrap layer was not found")
	}
	if _, ok := flusherOf(plainWriter{w: recorder}); ok {
		t.Error("a flusher was reported where the chain has none")
	}
	if _, ok := flusherOf(nil); ok {
		t.Error("a flusher was reported for a nil writer")
	}
}
