package http

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	nethttp "net/http"
	"testing"
	"time"
)

// TestTheBoundAddressIsReported pins the record a host reads the real address
// from. An endpoint configured on port 0 is given one by the operating
// system, and without this record nothing outside the process can learn which:
// a harness would have to pick a free port beforehand and race every other
// process for it.
func TestTheBoundAddressIsReported(t *testing.T) {
	logger, records := newRecordingLogger()
	_, addr := startTestEndpoint(t, testListener("public"), nethttp.NotFoundHandler(), logger)

	attrs, found := records.first(listeningMsg)
	if !found {
		t.Fatalf("binding a listener wrote no %q record; the records were %v",
			listeningMsg, records.at(slog.LevelInfo))
	}
	if attrs["listener"] != "public" {
		t.Errorf("the record names listener %q, want %q", attrs["listener"], "public")
	}
	if attrs["address"] != addr {
		t.Errorf("the record gives the address %q, want the one the socket bound, %q",
			attrs["address"], addr)
	}
	if attrs["configured"] != "127.0.0.1:0" {
		t.Errorf("the record gives the configured address %q, want %q", attrs["configured"], "127.0.0.1:0")
	}
	if _, port, err := net.SplitHostPort(attrs["address"]); err != nil || port == "0" {
		t.Errorf("the reported address %q carries no port a client could connect to", attrs["address"])
	}
}

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
	conn, err := net.DialTimeout("tcp", addr, waitDeadline())
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
	l, addr := startTestEndpoint(t, testListener("public"), h, logger)

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
	case <-time.After(waitDeadline()):
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
	s := testListener("public")
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

// streamingHandler stands for a long-lived response — a server-sent event
// stream, a long poll — that ends when the endpoint begins to drain.
type streamingHandler struct {
	entered chan struct{}
	// early receives whether Draining's channel was already closed when the
	// handler began; signalled whether it closed before the handler's own
	// deadline.
	early     chan bool
	signalled chan bool
}

func newStreamingHandler() *streamingHandler {
	return &streamingHandler{
		entered:   make(chan struct{}, 1),
		early:     make(chan bool, 1),
		signalled: make(chan bool, 1),
	}
}

func (h *streamingHandler) ServeHTTP(w nethttp.ResponseWriter, r *nethttp.Request) {
	draining := Draining(r.Context())
	select {
	case <-draining:
		h.early <- true
	default:
		h.early <- false
	}
	w.WriteHeader(nethttp.StatusOK)
	_ = nethttp.NewResponseController(w).Flush()
	h.entered <- struct{}{}
	select {
	case <-draining:
		h.signalled <- true
	case <-time.After(waitDeadline()):
		h.signalled <- false
	}
}

// TestALongLivedHandlerLearnsTheDrainHasBegun pins the signal a stream needs to
// end on its own. The drain does not cancel a request's context — an ordinary
// request has to be able to finish against a whole system — so without a
// signal of its own a stream runs until the drain-timeout cuts it, and every
// orderly shutdown of an endpoint with a client attached is reported as a
// drain that ran out of time.
func TestALongLivedHandlerLearnsTheDrainHasBegun(t *testing.T) {
	h := newStreamingHandler()
	logger, _ := newRecordingLogger()
	l, addr := startTestEndpoint(t, testListener("public"), h, logger)

	go func() { _ = get("http://" + addr + "/") }()
	waitFor(t, h.entered, "the stream to begin")
	if <-h.early {
		t.Fatal("Draining reported the drain as begun while the endpoint was still accepting")
	}

	if err := l.stop(context.Background()); err != nil {
		t.Fatalf("stop: %v", err)
	}
	started := time.Now()
	if err := l.close(context.Background()); err != nil {
		t.Fatalf("the drain of an endpoint whose stream ends on the signal reported %v", err)
	}
	if !<-h.signalled {
		t.Fatal("the stream never learnt that the drain had begun")
	}
	if waited := time.Since(started); waited >= waitDeadline() {
		t.Errorf("the drain took %s, as long as the stream's own deadline, so the signal did not end it", waited)
	}
}

// TestDrainingOutsideAnEndpointNeverCloses pins the answer for a context no
// endpoint of this module created: a nil channel, which never becomes ready,
// rather than one already closed that would end every stream at once.
func TestDrainingOutsideAnEndpointNeverCloses(t *testing.T) {
	if ch := Draining(context.Background()); ch != nil {
		t.Fatalf("Draining on a context no endpoint created returned %v, want nil", ch)
	}
}

// TestTheDrainLeavesRequestContextsAlone pins why the signal is a channel of
// its own: the drain lets in-flight requests finish, so it does not cancel
// their contexts, and a handler that watches only its request context is not
// told anything.
func TestTheDrainLeavesRequestContextsAlone(t *testing.T) {
	cancelled := make(chan bool, 1)
	entered := make(chan struct{}, 1)
	h := nethttp.HandlerFunc(func(_ nethttp.ResponseWriter, r *nethttp.Request) {
		entered <- struct{}{}
		select {
		case <-r.Context().Done():
			cancelled <- true
		case <-time.After(300 * time.Millisecond):
			cancelled <- false
		}
	})
	logger, _ := newRecordingLogger()
	l, addr := startTestEndpoint(t, testListener("public"), h, logger)

	go func() { _ = get("http://" + addr + "/") }()
	waitFor(t, entered, "the request to reach the handler")
	if err := l.stop(context.Background()); err != nil {
		t.Fatalf("stop: %v", err)
	}
	if err := l.close(context.Background()); err != nil {
		t.Fatalf("the drain reported %v", err)
	}
	if <-cancelled {
		t.Error("the drain cancelled the context of a request still in flight")
	}
}
