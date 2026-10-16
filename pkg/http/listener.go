package http

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"log/slog"
	"net"
	nethttp "net/http"
	"sync"
	"time"

	"github.com/vislake/speed/pkg/log"
)

// listener is one listening socket together with everything about it that
// exists only between Serve and the end of the shutdown: the server, the socket,
// and the drain.
//
// One listener is one net.Listen and one net/http.Server. Several endpoints may
// hang on it and share the socket; the handler the server serves is the
// listener's dispatch, which routes a request to the endpoint that registered
// its route (see dispatch.go).
//
// Closing the socket and waiting for the drain are two steps, stop and close,
// so that the module's Stop can close every listener's socket before it waits
// for any of them. Its state is read by Accepting from any goroutine at any
// moment and written by Serve, by those two steps and by the background drain,
// so all of it is behind the mutex.
type listener struct {
	// settings is the configuration this listener binds: its name, address,
	// timeouts and TLS, declared at construction and repeated by start.
	settings listenerSettings
	// endpoints are the endpoints hanging on this listener, in name order, set
	// at construction. The startup log names them.
	endpoints []*endpoint

	mu sync.Mutex
	// name and drainTimeout are copied from the settings when the address is
	// bound, so the messages the background drain writes carry them without
	// reaching back into the settings.
	name         string
	drainTimeout time.Duration

	server *nethttp.Server
	socket net.Listener

	// bound says the address was bound at least once. Stop and Close on a
	// listener that never got there return nil: a startup failure rolls back
	// every constructed instance whatever stage it reached.
	bound bool
	// accepting is what Accepting reports. It goes false on Stop and on an
	// accept loop that exited on its own.
	accepting bool
	// stopping records that Stop ran, which is what tells a normal shutdown
	// apart from an accept loop that died by itself.
	stopping bool

	// draining is closed when stop closes the socket. Every request context
	// the server creates carries it, and Draining hands it to a handler.
	draining chan struct{}

	// drained is closed when the background drain has finished, and drainErr
	// holds what it came back with. close waits on the channel rather than
	// draining itself, because the drain starts in stop.
	drained  chan struct{}
	drainErr error
	// reported says drainErr has been handed to a caller already. The module's
	// Stop waits for the drain and reports it, and its Close comes back to the
	// same listener afterwards; a failure handed to both would reach the
	// shutdown's aggregate twice.
	reported bool

	// handlers counts the handler calls under way on this listener. The drain
	// waits for it as well as for Shutdown, because Shutdown stops tracking a
	// connection once its handler has hijacked it.
	handlers inFlight

	// logger is where this listener's own diagnostics go. It is the module's
	// logger, not the one injected into a request context.
	logger *slog.Logger
	// listen opens the socket. It is a field so that a test can hand in a
	// socket whose Accept fails, which is the only way to reach the accept
	// loop's anomalous exit from outside.
	listen func(network, address string) (net.Listener, error)
}

// listeningMsg is the record a listener writes once its address is bound.
const listeningMsg = "an HTTP listener is listening"

// start binds the address and puts the assembled dispatch handler behind it.
//
// The bind is synchronous and the accept loop is not: the Serve callback must
// not block, because the driver advances through the stage before it enters
// running state, while a failure to bind has to terminate the startup rather
// than surface in a goroutine nobody is watching. Loading the certificate is
// synchronous for the same reason, which is why the TLS listener is built here
// instead of letting the server's own ServeTLS do it after the callback has
// returned.
func (l *listener) start(s listenerSettings, h nethttp.Handler, logger *slog.Logger) error {
	if logger == nil {
		logger = log.Default()
	}
	open := l.listen
	if open == nil {
		open = net.Listen
	}

	socket, err := open("tcp", s.address)
	if err != nil {
		return fmt.Errorf("%w: listener %q on %q: %w", ErrListen, s.name, s.address, err)
	}
	if s.tls() {
		// A certificate that cannot be loaded joins ErrListen rather than
		// adding a sentinel of its own. The criterion is the host's
		// remedy: a mistyped certificate path and an address already in
		// use are both fixed by changing the configuration and starting
		// again, so they are one class to act on, and the text carries
		// which half of the pair failed and why.
		cert, certErr := tls.LoadX509KeyPair(s.tlsCertFile, s.tlsKeyFile)
		if certErr != nil {
			closeErr := socket.Close()
			return errors.Join(fmt.Errorf("%w: listener %q could not load the certificate %q "+
				"with the key %q: %w", ErrListen, s.name, s.tlsCertFile, s.tlsKeyFile, certErr), closeErr)
		}
		socket = tls.NewListener(socket, &tls.Config{
			Certificates: []tls.Certificate{cert},
			MinVersion:   tls.VersionTLS12,
			// The protocols net/http's own ServeTLS offers. Building the
			// listener here is for loading the certificate synchronously,
			// not for choosing protocols; without this list ALPN offers
			// nothing, and every client falls back to HTTP/1.1.
			NextProtos: []string{"h2", "http/1.1"},
		})
	}

	draining := make(chan struct{})
	server := &nethttp.Server{
		Addr:              s.address,
		Handler:           l.track(h),
		ReadHeaderTimeout: s.readHeaderTimeout,
		ReadTimeout:       s.readTimeout,
		WriteTimeout:      s.writeTimeout,
		IdleTimeout:       s.idleTimeout,
		// Every request context descends from this one, which is how the
		// draining signal reaches a handler without a layer of its own on the
		// chain.
		BaseContext: func(net.Listener) context.Context {
			return withDraining(context.Background(), draining)
		},
		// What net/http writes for itself goes to this module's logger rather
		// than to the process default: a panic outside the request path — in
		// the base layer, or in the protocol implementation — and its own
		// reports about a connection would otherwise be the only records in
		// the process that miss the configured destinations. The wording is
		// net/http's and this module does not rewrite it.
		ErrorLog: slog.NewLogLogger(logger.Handler(), slog.LevelError),
	}
	// The socket is wrapped so that closing it twice is closing it once.
	// stop closes it and starts the Shutdown that follows straight away,
	// while the accept loop only stops tracking the socket once it has
	// returned; the two overlap, and in that window Shutdown closes a socket
	// Stop has already closed. Unwrapped, the second close answers "use of
	// closed network connection" and a clean shutdown reports a failure.
	socket = &onceCloser{Listener: socket}

	l.mu.Lock()
	l.settings = s
	l.name = s.name
	l.drainTimeout = s.drainTimeout
	l.server = server
	l.socket = socket
	l.bound = true
	l.accepting = true
	l.draining = draining
	l.drained = make(chan struct{})
	l.logger = logger
	l.mu.Unlock()

	// The address is reported as the socket bound it, beside the one the
	// configuration gave: a listener on port 0 is handed a port by the
	// operating system, and this record is the one place outside the process
	// that says which. The socket is bound already, so a client reading it may
	// connect at once. The endpoints on this listener are named so that a
	// reader of the record knows what the socket now serves.
	logger.Info(listeningMsg,
		"listener", s.name,
		"address", socket.Addr().String(),
		"configured", s.address,
		"tls", s.tls(),
		"endpoints", l.endpointNames())

	go l.accept(server, socket)
	return nil
}

// endpointNames lists the endpoint names hanging on this listener, in name
// order, for the record written once the address is bound.
func (l *listener) endpointNames() []string {
	names := make([]string, 0, len(l.endpoints))
	for _, e := range l.endpoints {
		names = append(names, e.settings.name)
	}
	return names
}

// accept runs the accept loop and records how it ended.
//
// An exit this module caused — Stop closed the socket, or the server was shut
// down — is the normal path and says nothing. Any other exit is an anomaly:
// the address was bound successfully, so there is no startup to terminate any
// more, and Run is by now blocked waiting for the host to cancel, with nothing
// this module could wake it with. What is left is to write it down and to stop
// claiming the endpoint accepts requests. Whether that amounts to an unhealthy
// process is for whoever reads Accepting to decide; this module does not judge
// it and does not restart anything.
func (l *listener) accept(server *nethttp.Server, socket net.Listener) {
	err := server.Serve(socket)

	l.mu.Lock()
	defer l.mu.Unlock()
	l.accepting = false
	if l.stopping || errors.Is(err, nethttp.ErrServerClosed) || errors.Is(err, net.ErrClosed) {
		return
	}
	l.logger.Error("the accept loop of an HTTP listener exited on its own; "+
		"the listener no longer accepts requests and the process keeps running",
		"listener", l.name, "error", err)
}

// stop closes the socket in the calling goroutine and leaves the drain running
// in the background.
//
// Closing the socket is not put in the goroutine: once this returns, "no new
// request is accepted" has to be true already, and when a goroutine gets
// scheduled is not up to the caller. Waiting for the drain is left to close, so
// that every endpoint of the module stops accepting before the module waits
// for any of them, and their drains overlap.
func (l *listener) stop(ctx context.Context) error {
	l.mu.Lock()
	if !l.bound || l.stopping {
		// Never bound, or stopped already. Both are ordinary: rollback
		// stops instances that never served, and a host may stop twice.
		l.mu.Unlock()
		return nil
	}
	l.stopping = true
	l.accepting = false
	name, server, socket := l.name, l.server, l.socket
	draining, drained, timeout := l.draining, l.drained, l.drainTimeout
	l.mu.Unlock()

	closeErr := socket.Close()
	// The signal goes out once the socket is closed, so a handler that ends
	// its response on it cannot be followed by a request the listener still
	// accepted.
	close(draining)
	go l.drain(ctx, server, drained, timeout)
	if closeErr != nil {
		return fmt.Errorf("http: listener %q could not close its listening socket: %w", name, closeErr)
	}
	return nil
}

// drain waits for the in-flight requests, bounded by this endpoint's own
// configured timeout.
//
// The bound cannot come from the context: the one Stop and Close receive has
// its cancellation stripped, precisely so that a drain honouring its context
// does not return the instant the host cancels. The clock starts here, where
// the draining really begins, so the time the module spends closing its other
// endpoints does not eat into this endpoint's drain budget.
func (l *listener) drain(ctx context.Context, server *nethttp.Server, drained chan struct{}, timeout time.Duration) {
	if timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}

	err := server.Shutdown(ctx)
	if err == nil {
		// Shutdown has waited for every connection it still tracks, which
		// leaves out the ones a handler hijacked: their handlers can still be
		// running, and a request is in flight until its handler returns.
		select {
		case <-l.handlers.idle():
		case <-ctx.Done():
			err = ctx.Err()
		}
	}
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		// Shutdown gives up waiting but leaves the connections it was
		// waiting for running. server.Close is what actually cuts them, and
		// without it the caller is told the listener was cut while the
		// requests carry on. A hijacked connection is not among them: it
		// belongs to its handler, and nothing here can cut it.
		cutErr := server.Close()
		err = fmt.Errorf("%w: listener %q still had requests in flight when its drain-timeout "+
			"of %s expired, and the connections still open were cut. Raise %s.%s.%s.drain-timeout "+
			"if the requests this listener serves legitimately take longer",
			ErrDrainTimeout, l.name, timeout, configNamespace, listenersItem, l.name)
		if cutErr != nil {
			err = errors.Join(err, fmt.Errorf(
				"http: listener %q could not cut its remaining connections: %w", l.name, cutErr))
		}
	case err != nil:
		err = fmt.Errorf("http: listener %q failed to drain: %w", l.name, err)
	}

	l.mu.Lock()
	l.drainErr = err
	l.mu.Unlock()
	close(drained)
}

// close waits for this endpoint's drain to finish and reports what it came back
// with, to the first caller only. An endpoint that never bound returns nil, and
// so does one that drained cleanly or whose drain was reported already.
//
// A close that was not preceded by a stop starts the drain itself: the
// module's Stop runs both, but a host driving the stages by hand may skip
// straight to Close, and an endpoint left listening after that would outlive
// the process's shutdown.
func (l *listener) close(ctx context.Context) error {
	l.mu.Lock()
	bound, stopping := l.bound, l.stopping
	l.mu.Unlock()
	if !bound {
		return nil
	}

	var stopErr error
	if !stopping {
		stopErr = l.stop(ctx)
	}

	l.mu.Lock()
	drained := l.drained
	l.mu.Unlock()
	<-drained

	l.mu.Lock()
	defer l.mu.Unlock()
	if l.reported {
		return stopErr
	}
	l.reported = true
	return errors.Join(stopErr, l.drainErr)
}

// track counts every call of next as in flight until it returns, the ones that
// hijack their connection included.
func (l *listener) track(next nethttp.Handler) nethttp.Handler {
	return nethttp.HandlerFunc(func(w nethttp.ResponseWriter, r *nethttp.Request) {
		l.handlers.enter()
		defer l.handlers.leave()
		next.ServeHTTP(w, r)
	})
}

// inFlight counts calls under way and tells a waiter when none is left.
//
// A sync.WaitGroup does not fit: the count may rise again after a waiter has
// begun, when a request read just before the socket closed reaches its
// handler, and a WaitGroup forbids an Add from zero concurrent with a Wait.
type inFlight struct {
	mu sync.Mutex
	n  int
	// zero is closed when the count falls to zero, and replaced by the next
	// waiter that finds calls under way.
	zero chan struct{}
}

func (f *inFlight) enter() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.n++
}

func (f *inFlight) leave() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.n--
	if f.n == 0 && f.zero != nil {
		close(f.zero)
		f.zero = nil
	}
}

// idle returns a channel that is closed once no call is under way; it is
// closed already when none is.
func (f *inFlight) idle() <-chan struct{} {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.n == 0 {
		done := make(chan struct{})
		close(done)
		return done
	}
	if f.zero == nil {
		f.zero = make(chan struct{})
	}
	return f.zero
}

// isAccepting reports whether this endpoint is still taking requests.
func (l *listener) isAccepting() bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.accepting
}

// addr is the address the socket really bound, which is what a test that asked
// for port 0 connects to. It is empty on an endpoint that never bound.
func (l *listener) addr() net.Addr {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.socket == nil {
		return nil
	}
	return l.socket.Addr()
}

// onceCloser closes the wrapped listener once and hands the same result back
// to every later call. Two closes race by construction here: stop closes the
// socket and starts the drain, and the drain's Shutdown closes every listener
// the server is still tracking, which this one is until the accept loop has
// returned. The window is short and the failure it produces is intermittent,
// which is what makes it worth closing rather than diagnosing.
type onceCloser struct {
	net.Listener
	once sync.Once
	err  error
}

func (o *onceCloser) Close() error {
	o.once.Do(func() { o.err = o.Listener.Close() })
	return o.err
}
