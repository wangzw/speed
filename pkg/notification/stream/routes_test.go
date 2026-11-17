package stream

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/vislake/speed/pkg/authn/authntest"
	"github.com/vislake/speed/pkg/core/coretest"
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

// The answer's status and headers are flushed before the handler builds its
// first event: the client learns the stream is open without waiting for an
// event to arrive. The case observes the order on the response writer — a
// flush with no body bytes yet, then the written frame — and reads the content
// type as it was on that first flush.
func TestTheHeadersAreFlushedBeforeTheFirstEvent(t *testing.T) {
	rig := newRig(t)
	ctx, cancel := context.WithCancel(authntest.Context(t, context.Background()))
	defer cancel()

	recorder := newTimingRecorder()
	req := httptest.NewRequest(http.MethodGet, pathStream, nil).WithContext(ctx)
	done := make(chan struct{})
	go func() {
		rig.engine.ServeHTTP(recorder, req)
		close(done)
	}()

	// The handler subscribes its connection when the answer's callback begins,
	// which the entry point reaches only after flushing the declared status and
	// headers. Reaching this point therefore means that flush has happened.
	conn := rig.firstConnection(t)
	opening := recorder.snapshot()
	if opening.writes != 0 {
		t.Fatalf("the answer wrote %d body chunks before the handler subscribed, want none", opening.writes)
	}
	if opening.emptyFlushes == 0 {
		t.Fatal("the answer flushed no headers before the handler subscribed")
	}
	if opening.status != http.StatusOK {
		t.Errorf("status at the header flush is %d, want 200", opening.status)
	}
	if opening.firstContent != "text/event-stream" {
		t.Errorf("Content-Type at the header flush is %q, want text/event-stream", opening.firstContent)
	}

	conn.ch <- frame{event: frameEventName, data: []byte(`{"message_id":"message-1"}`)}

	deadline := time.Now().Add(coretest.Scale(30 * time.Second))
	for recorder.snapshot().writes == 0 {
		if time.Now().After(deadline) {
			t.Fatal("the stream wrote no event")
		}
		time.Sleep(time.Millisecond)
	}
	after := recorder.snapshot()
	want := "event: message\ndata: {\"message_id\":\"message-1\"}\n\n"
	if after.body != want {
		t.Errorf("the event stream is %q, want %q", after.body, want)
	}
	// The header flush came first, and each event is flushed as it is written.
	if after.flushes < 2 {
		t.Errorf("the answer flushed %d times, want the header flush and one per event", after.flushes)
	}

	cancel()
	select {
	case <-done:
	case <-time.After(coretest.Scale(30 * time.Second)):
		t.Fatal("the stream did not end after the request context was cancelled")
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
// connected client parses. The case drives the real route and reads the bytes
// the handler flushes, so the promise is asserted on what a client receives.
func TestTheFrameIsAnEventNamedMessageWithJsonData(t *testing.T) {
	rig := newRig(t)
	ctx, cancel := context.WithCancel(authntest.Context(t, context.Background()))
	defer cancel()

	recorder := newFlushRecorder()
	req := httptest.NewRequest(http.MethodGet, pathStream, nil).WithContext(ctx)
	done := make(chan struct{})
	go func() {
		rig.engine.ServeHTTP(recorder, req)
		close(done)
	}()

	conn := rig.firstConnection(t)
	conn.ch <- frame{event: frameEventName, data: []byte(`{"message_id":"message-1"}`)}

	select {
	case got := <-recorder.flushes:
		want := "event: message\ndata: {\"message_id\":\"message-1\"}\n\n"
		if got != want {
			t.Errorf("the frame is %q, want %q", got, want)
		}
	case <-time.After(coretest.Scale(30 * time.Second)):
		t.Fatal("the stream flushed no frame")
	}

	cancel()
	select {
	case <-done:
	case <-time.After(coretest.Scale(30 * time.Second)):
		t.Fatal("the stream did not end after the request context was cancelled")
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
