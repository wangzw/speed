package http

import (
	"context"
	"errors"
	"fmt"
	"net"
	nethttp "net/http"
	"testing"
	"time"
)

// hijackingHandler takes the connection over from the server, the way a
// WebSocket upgrade does, and holds it until the test lets go. net/http stops
// tracking a connection once it has been hijacked, so its Shutdown neither
// waits for this handler nor cuts its connection.
type hijackingHandler struct {
	entered  chan struct{}
	release  chan struct{}
	finished chan struct{}
}

func newHijackingHandler(t *testing.T) *hijackingHandler {
	t.Helper()
	h := &hijackingHandler{
		entered:  make(chan struct{}, 1),
		release:  make(chan struct{}),
		finished: make(chan struct{}, 1),
	}
	t.Cleanup(h.letGo)
	return h
}

func (h *hijackingHandler) ServeHTTP(w nethttp.ResponseWriter, _ *nethttp.Request) {
	conn, _, err := nethttp.NewResponseController(w).Hijack()
	if err != nil {
		w.WriteHeader(nethttp.StatusInternalServerError)
		return
	}
	defer conn.Close()
	h.entered <- struct{}{}
	<-h.release
	h.finished <- struct{}{}
}

func (h *hijackingHandler) letGo() {
	select {
	case <-h.release:
	default:
		close(h.release)
	}
}

// openHijackedConnection sends one request on a raw connection and leaves the
// connection open, which is what a client of an upgraded protocol does.
func openHijackedConnection(t *testing.T, addr string) {
	t.Helper()
	conn, err := net.DialTimeout("tcp", addr, waitDeadline)
	if err != nil {
		t.Fatalf("dialling the endpoint: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	if _, err := fmt.Fprintf(conn, "GET / HTTP/1.1\r\nHost: %s\r\n\r\n", addr); err != nil {
		t.Fatalf("writing the request: %v", err)
	}
}

// TestTheDrainWaitsForAHijackedConnectionsHandler pins that a request whose
// handler took its connection over is still in flight for the drain. Shutdown
// forgets such a connection the moment it is hijacked, so a drain that only
// asked Shutdown would end with the handler still running, and the modules
// released after it would be released under that handler.
func TestTheDrainWaitsForAHijackedConnectionsHandler(t *testing.T) {
	h := newHijackingHandler(t)
	logger, _ := newRecordingLogger()
	l, addr := startTestEndpoint(t, testSettings("public"), h, logger)

	openHijackedConnection(t, addr)
	waitFor(t, h.entered, "the handler to take the connection over")

	if err := l.stop(context.Background()); err != nil {
		t.Fatalf("stop: %v", err)
	}
	closed := make(chan error, 1)
	go func() { closed <- l.close(context.Background()) }()
	select {
	case err := <-closed:
		t.Fatalf("the drain ended while the handler of a hijacked connection was still running: %v", err)
	case <-time.After(200 * time.Millisecond):
	}

	h.letGo()
	select {
	case err := <-closed:
		if err != nil {
			t.Errorf("the drain reported a failure once the handler returned: %v", err)
		}
	case <-time.After(waitDeadline):
		t.Fatal("the drain did not end after the handler of the hijacked connection returned")
	}
	select {
	case <-h.finished:
	default:
		t.Error("the drain ended before the handler of the hijacked connection finished")
	}
}

// TestAHijackedHandlerPastTheDrainTimeoutIsReported pins the other end: a
// handler of a hijacked connection that outlives the budget is a drain that
// ran out of time, reported as one, rather than a drain that came back clean
// while the handler goes on running.
func TestAHijackedHandlerPastTheDrainTimeoutIsReported(t *testing.T) {
	h := newHijackingHandler(t)
	logger, _ := newRecordingLogger()
	s := testSettings("public")
	s.drainTimeout = 100 * time.Millisecond
	l, addr := startTestEndpoint(t, s, h, logger)

	openHijackedConnection(t, addr)
	waitFor(t, h.entered, "the handler to take the connection over")

	if err := l.stop(context.Background()); err != nil {
		t.Fatalf("stop: %v", err)
	}
	err := l.close(context.Background())
	if !errors.Is(err, ErrDrainTimeout) {
		t.Fatalf("a hijacked connection's handler still running past the drain-timeout came "+
			"back as %v, not ErrDrainTimeout", err)
	}
	mustContain(t, err.Error(), `"public"`, "the endpoint that did not drain")
}
