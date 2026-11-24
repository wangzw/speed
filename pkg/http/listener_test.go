package http

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	nethttp "net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func startTestEndpoint(t *testing.T, s listenerSettings, h nethttp.Handler, logger *slog.Logger) (*listener, string) {
	t.Helper()
	l := &listener{}
	if err := l.start(s, h, logger); err != nil {
		t.Fatalf("binding endpoint %q: %v", s.name, err)
	}
	t.Cleanup(func() { _ = l.close(context.Background()) })
	addr := l.addr()
	if addr == nil {
		t.Fatal("the endpoint bound without an address to connect to")
	}
	return l, addr.String()
}

func get(url string) error {
	req, err := nethttp.NewRequestWithContext(context.Background(), nethttp.MethodGet, url, nil)
	if err != nil {
		return err
	}
	resp, err := nethttp.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	return nil
}

// TestBindFailureIsStartupFailure pins the synchronous half of Serve: an address that cannot be
// bound comes back from the callback, so the startup ends there.
func TestBindFailureIsStartupFailure(t *testing.T) {
	taken, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("taking a port to collide with: %v", err)
	}
	defer taken.Close()

	s := testListener("public")
	s.address = taken.Addr().String()
	logger, _ := newRecordingLogger()

	var l listener
	err = l.start(s, nethttp.NotFoundHandler(), logger)
	if !errors.Is(err, ErrListen) {
		t.Fatalf("binding a taken address did not report ErrListen: %v", err)
	}
	mustContain(t, err.Error(), s.address, "the address that could not be bound")
	mustContain(t, err.Error(), `"public"`, "the endpoint that could not bind")
	if l.isAccepting() {
		t.Error("an endpoint that failed to bind reports that it is accepting requests")
	}
}

// TestBindFailureKeepsTheUnderlyingNetError pins the wrapping: %w all the way down, so a caller can
// still reach syscall.EADDRINUSE through the sentinel.
func TestBindFailureKeepsTheUnderlyingNetError(t *testing.T) {
	taken, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("taking a port to collide with: %v", err)
	}
	defer taken.Close()

	s := testListener("public")
	s.address = taken.Addr().String()
	logger, _ := newRecordingLogger()

	var l listener
	err = l.start(s, nethttp.NotFoundHandler(), logger)
	var opErr *net.OpError
	if !errors.As(err, &opErr) {
		t.Fatalf("the net error chain did not survive the wrapping: %v", err)
	}
	if opErr.Op != "listen" {
		t.Errorf("the underlying error is about %q, not about listening", opErr.Op)
	}
}

// TestTLSCertFailureIsSynchronous pins where a certificate is loaded.
func TestTLSCertFailureIsSynchronous(t *testing.T) {
	dir := t.TempDir()
	s := testListener("public")
	s.tlsCertFile = filepath.Join(dir, "absent.pem")
	s.tlsKeyFile = filepath.Join(dir, "absent-key.pem")
	logger, _ := newRecordingLogger()

	var l listener
	err := l.start(s, nethttp.NotFoundHandler(), logger)
	if !errors.Is(err, ErrListen) {
		t.Fatalf("a certificate that cannot be loaded did not fail the callback: %v", err)
	}
	mustContain(t, err.Error(), s.tlsCertFile, "the certificate path that could not be read")
	if l.isAccepting() {
		t.Error("an endpoint whose certificate failed to load reports that it is accepting requests")
	}
}

// TestCertificateFailureIsAListenFailure pins how the failure is classified.
func TestCertificateFailureIsAListenFailure(t *testing.T) {
	dir := t.TempDir()
	s := testListener("public")
	s.tlsCertFile = filepath.Join(dir, "absent.pem")
	s.tlsKeyFile = filepath.Join(dir, "absent-key.pem")
	logger, _ := newRecordingLogger()

	var l listener
	err := l.start(s, nethttp.NotFoundHandler(), logger)
	if !errors.Is(err, ErrListen) {
		t.Fatalf("a certificate that cannot be loaded was not reported as ErrListen: %v", err)
	}
	mustContain(t, err.Error(), s.tlsCertFile, "the certificate path")
	mustContain(t, err.Error(), s.tlsKeyFile, "the key path")
	mustContain(t, err.Error(), `"public"`, "the endpoint that did not come up")

	others := []struct {
		name     string
		sentinel error
	}{
		{"ErrUnknownEndpoint", ErrUnknownEndpoint},
		{"ErrRouteConflict", ErrRouteConflict},
		{"ErrMiddlewareCycle", ErrMiddlewareCycle},
		{"ErrInvalidSpec", ErrInvalidSpec},
		{"ErrDrainTimeout", ErrDrainTimeout},
	}
	for _, other := range others {
		if errors.Is(err, other.sentinel) {
			t.Errorf("a certificate failure also matches %s; it is a bind failure and "+
				"nothing else", other.name)
		}
	}
}

// TestStopClosesListenerSynchronously is the first of an endpoint's two steps: when stop returns,
// the endpoint has already stopped accepting and a new connection is refused.
func TestStopClosesListenerSynchronously(t *testing.T) {
	gate := newGateHandler(t)
	logger, _ := newRecordingLogger()
	l, addr := startTestEndpoint(t, testListener("public"), gate, logger)

	inFlight := make(chan error, 1)
	go func() { inFlight <- get("http://" + addr + "/") }()
	waitFor(t, gate.entered, "the request to reach the handler")

	// stop is called with a deadline of its own: an implementation that drained inside stop would otherwise park here until the whole binary times out, and this assertion tells the two shapes apart.
	stopped := make(chan error, 1)
	go func() { stopped <- l.stop(context.Background()) }()
	select {
	case err := <-stopped:
		if err != nil {
			t.Fatalf("Stop: %v", err)
		}
	case <-time.After(waitDeadline()):
		t.Fatal("stop did not return while a request was in flight; waiting for the drain " +
			"belongs to close, or the module's other endpoints go on accepting meanwhile")
	}

	if l.isAccepting() {
		t.Error("the endpoint still reports that it is accepting requests after Stop returned")
	}
	conn, err := net.DialTimeout("tcp", addr, waitDeadline())
	if err == nil {
		conn.Close()
		t.Error("a new connection was accepted after Stop returned, so the socket outlived the promise")
	}
	select {
	case <-gate.finished:
		t.Error("stop cut off a request that was in flight; the drain lets it finish")
	default:
	}

	gate.letGo()
	if err := <-inFlight; err != nil {
		t.Errorf("the in-flight request did not finish cleanly: %v", err)
	}
}

// TestCloseWaitsForDrain is the second step, and the two tests only tell the steps apart together.
func TestCloseWaitsForDrain(t *testing.T) {
	gate := newGateHandler(t)
	logger, _ := newRecordingLogger()
	l, addr := startTestEndpoint(t, testListener("public"), gate, logger)

	inFlight := make(chan error, 1)
	go func() { inFlight <- get("http://" + addr + "/") }()
	waitFor(t, gate.entered, "the request to reach the handler")

	if err := l.stop(context.Background()); err != nil {
		t.Fatalf("Stop: %v", err)
	}

	closed := make(chan error, 1)
	go func() { closed <- l.close(context.Background()) }()
	select {
	case err := <-closed:
		t.Fatalf("Close returned while a request was still in flight: %v", err)
	case <-time.After(100 * time.Millisecond):
	}

	gate.letGo()
	select {
	case err := <-closed:
		if err != nil {
			t.Errorf("Close reported a failure on a clean drain: %v", err)
		}
	case <-time.After(waitDeadline()):
		t.Fatal("Close did not return after the in-flight request finished")
	}
	if err := <-inFlight; err != nil {
		t.Errorf("the in-flight request did not finish cleanly: %v", err)
	}
}

// TestDrainTimeoutCutsInflight pins the end of the drain budget: the failure is reported, and the
// connections it was waiting for are really cut.
func TestDrainTimeoutCutsInflight(t *testing.T) {
	gate := newGateHandler(t)
	logger, _ := newRecordingLogger()
	s := testListener("public")
	s.drainTimeout = 100 * time.Millisecond
	l, addr := startTestEndpoint(t, s, gate, logger)

	inFlight := make(chan error, 1)
	go func() { inFlight <- get("http://" + addr + "/") }()
	waitFor(t, gate.entered, "the request to reach the handler")

	if err := l.stop(context.Background()); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	err := l.close(context.Background())
	if !errors.Is(err, ErrDrainTimeout) {
		t.Fatalf("a drain that ran out of time did not report ErrDrainTimeout: %v", err)
	}
	mustContain(t, err.Error(), `"public"`, "the endpoint that did not drain")
	mustContain(t, err.Error(), s.drainTimeout.String(), "the timeout that expired")

	select {
	case clientErr := <-inFlight:
		if clientErr == nil {
			t.Error("the client got a complete response, so the connection was not cut")
		}
	case <-time.After(waitDeadline()):
		t.Fatal("the client was left hanging, so the connection was not cut")
	}
}

type failingListener struct {
	err    error
	closed chan struct{}
}

func (f *failingListener) Accept() (net.Conn, error) { return nil, f.err }
func (f *failingListener) Addr() net.Addr            { return &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1)} }

func (f *failingListener) Close() error {
	select {
	case <-f.closed:
	default:
		close(f.closed)
	}
	return nil
}

// TestAcceptLoopAnomalyFlipsAccepting pins that the process stays up, the endpoint
// stops claiming it accepts requests, and the event is written down.
func TestAcceptLoopAnomalyFlipsAccepting(t *testing.T) {
	logger, records := newRecordingLogger()
	broken := &failingListener{err: errors.New("the socket is beyond repair"), closed: make(chan struct{})}
	l := &listener{listen: func(string, string) (net.Listener, error) { return broken, nil }}

	if err := l.start(testListener("public"), nethttp.NotFoundHandler(), logger); err != nil {
		t.Fatalf("the bind itself failed, so the accept loop never ran: %v", err)
	}
	t.Cleanup(func() { _ = l.close(context.Background()) })

	waitUntil(t, "the endpoint to stop reporting that it accepts requests", func() bool {
		return !l.isAccepting()
	})
	waitUntil(t, "the anomalous exit to be written down", func() bool {
		return len(records.at(slog.LevelError)) > 0
	})

	written := strings.Join(records.at(slog.LevelError), "\n")
	mustContain(t, written, "public", "the endpoint whose accept loop exited")
	mustContain(t, written, "beyond repair", "what the accept loop came back with")
}

// TestNormalStopIsNotAnAnomalousExit is the other half of the same classification: a shutdown this
// module caused says nothing.
func TestNormalStopIsNotAnAnomalousExit(t *testing.T) {
	logger, records := newRecordingLogger()
	l, _ := startTestEndpoint(t, testListener("public"), nethttp.NotFoundHandler(), logger)

	if err := l.stop(context.Background()); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	if err := l.close(context.Background()); err != nil {
		t.Fatalf("Close: %v", err)
	}
	// The accept loop returns on its own goroutine, and its done signal is what says the goroutine is over, so the absence below is observed after the last moment a record could be written rather than after an interval.
	waitFor(t, l.acceptDone, "the accept loop to return")

	if written := records.at(slog.LevelError); len(written) > 0 {
		t.Errorf("a clean stop was reported as an anomalous exit: %s", strings.Join(written, "\n"))
	}
}

// TestStopToleratesNeverBound pins the rollback path: a startup that fails part-way stops and
// closes every constructed instance, including the endpoints that never got as far as binding.
func TestStopToleratesNeverBound(t *testing.T) {
	var l listener
	if err := l.stop(context.Background()); err != nil {
		t.Errorf("Stop on an endpoint that never bound: %v", err)
	}
	if err := l.close(context.Background()); err != nil {
		t.Errorf("Close on an endpoint that never bound: %v", err)
	}
	if l.isAccepting() {
		t.Error("an endpoint that never bound reports that it is accepting requests")
	}
}

// TestStopIsIdempotent pins the repeated call.
func TestStopIsIdempotent(t *testing.T) {
	logger, _ := newRecordingLogger()
	l, _ := startTestEndpoint(t, testListener("public"), nethttp.NotFoundHandler(), logger)

	if err := l.stop(context.Background()); err != nil {
		t.Fatalf("the first Stop: %v", err)
	}
	if err := l.stop(context.Background()); err != nil {
		t.Errorf("the second Stop: %v", err)
	}
	if err := l.close(context.Background()); err != nil {
		t.Errorf("Close after two Stops: %v", err)
	}
}

// TestCloseWithoutStop pins the path a host driving the stages by hand takes.
func TestCloseWithoutStop(t *testing.T) {
	logger, _ := newRecordingLogger()
	l, addr := startTestEndpoint(t, testListener("public"), nethttp.NotFoundHandler(), logger)

	if err := l.close(context.Background()); err != nil {
		t.Fatalf("Close without a preceding Stop: %v", err)
	}
	if l.isAccepting() {
		t.Error("the endpoint still reports that it is accepting requests after Close")
	}
	conn, err := net.DialTimeout("tcp", addr, waitDeadline())
	if err == nil {
		conn.Close()
		t.Error("the socket was still open after Close")
	}
}

// TestDoubleCloseOfTheSocketIsNotAFailure pins the wrapper the socket is behind.
func TestDoubleCloseOfTheSocketIsNotAFailure(t *testing.T) {
	inner, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("opening a socket: %v", err)
	}
	once := &onceCloser{Listener: inner}

	if err := once.Close(); err != nil {
		t.Fatalf("the first close: %v", err)
	}
	if err := once.Close(); err != nil {
		t.Errorf("the second close reported %v, and a clean shutdown would carry it as a failure", err)
	}
}

// TestDrainsOfSeveralEndpointsRunTogether pins where the drain is started, which no single-endpoint
// assertion above can reach.
func TestDrainsOfSeveralEndpointsRunTogether(t *testing.T) {
	const budget = 300 * time.Millisecond
	const endpoints = 3

	gate := newGateHandler(t)
	logger, _ := newRecordingLogger()

	bound := make([]*listener, 0, endpoints)
	for i := range endpoints {
		s := testListener(fmt.Sprintf("endpoint-%d", i))
		s.drainTimeout = budget
		l, addr := startTestEndpoint(t, s, gate, logger)
		bound = append(bound, l)
		go func() { _ = get("http://" + addr + "/") }()
	}
	for range endpoints {
		waitFor(t, gate.entered, "a request to reach the handler")
	}

	starts := make([]chan time.Time, endpoints)
	ends := make([]chan time.Time, endpoints)
	for i, l := range bound {
		start, end := make(chan time.Time, 1), make(chan time.Time, 1)
		starts[i], ends[i] = start, end
		go func(l *listener, start, end chan<- time.Time) {
			<-l.drainBegan
			start <- time.Now()
			<-l.drained
			end <- time.Now()
		}(l, start, end)
	}

	for _, l := range bound {
		if err := l.stop(context.Background()); err != nil {
			t.Fatalf("Stop: %v", err)
		}
	}

	began, ended := make([]time.Time, 0, endpoints), make([]time.Time, 0, endpoints)
	limit := waitDeadline()
	for i := range bound {
		select {
		case at := <-ends[i]:
			ended = append(ended, at)
		case <-time.After(limit):
			t.Fatalf("the drain of endpoint-%d had not finished within %s of the endpoint "+
				"stopping, with no close called: each drain has to run from the moment its own "+
				"socket closed, not start when the caller waits", i, limit)
		}
		select {
		case at := <-starts[i]:
			began = append(began, at)
		case <-time.After(limit):
			t.Fatalf("the drain of endpoint-%d never began", i)
		}
	}

	lastBegan, earliestEnd := began[0], ended[0]
	for _, at := range began {
		if at.After(lastBegan) {
			lastBegan = at
		}
	}
	for _, at := range ended {
		if at.Before(earliestEnd) {
			earliestEnd = at
		}
	}
	if !lastBegan.Before(earliestEnd) {
		t.Errorf("the last drain began at %v and the first ended at %v: the drains ran one after "+
			"another, so a shutdown costs the sum of the budgets rather than the longest",
			lastBegan, earliestEnd)
	}

	for i, l := range bound {
		name := fmt.Sprintf("endpoint-%d", i)
		err := l.close(context.Background())
		if !errors.Is(err, ErrDrainTimeout) {
			t.Fatalf("the drain of %s ran out of time without reporting ErrDrainTimeout: %v", name, err)
		}
		mustContain(t, err.Error(), `"`+name+`"`, "the endpoint whose drain ran out of time")
	}
}

// TestTheServersOwnRecordsGoToTheModulesLogger pins what net/http writes for itself reaching the
// destinations this module's logger is configured with.
func TestTheServersOwnRecordsGoToTheModulesLogger(t *testing.T) {
	logger, records := newRecordingLogger()
	l, _ := startTestEndpoint(t, testListener("public"), nethttp.NotFoundHandler(), logger)

	if l.server == nil || l.server.ErrorLog == nil {
		t.Fatal("the listener's server was given no log of its own, so what net/http writes for " +
			"itself goes to the process default instead of the configured destinations")
	}
	l.server.ErrorLog.Print("net/http writing for itself")

	written := records.at(slog.LevelError)
	if len(written) != 1 {
		t.Fatalf("the server's own record produced %d entries on this module's logger, want one: %v",
			len(written), written)
	}
	mustContain(t, written[0], "net/http writing for itself", "the text net/http wrote")
}
