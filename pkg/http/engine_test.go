package http

import (
	"bufio"
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	nethttp "net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/gin-gonic/gin/binding"

	"github.com/vislake/speed/pkg/log"
)

// The tests in this file pin the settings of the gin engine each endpoint is
// given. Each is observed through what the assembled endpoint does with a
// request, not by reading the engine's fields: a setting that is on the engine
// and not in effect is no setting.

// TestATrailingSlashIsNotRedirected pins the redirect gin does by default and
// this module turns off. gin answers "/x/" for a route "/x" with a 301 before
// any global middleware runs, so a request that took the redirect would go past
// authentication, rate limiting, audit and telemetry without a single one of
// them having seen it. With the redirect off the request is a request for a
// path that matches nothing, a 404 that goes through every layer, once.
//
// The two rows are the two directions of the redirect, and the control rows are
// the paths that do match.
func TestATrailingSlashIsNotRedirected(t *testing.T) {
	var runs int
	logger, _ := newRecordingLogger()
	handler := mustAssemble(t, testSettings("public"), nil, logger, func(e Endpoint) {
		e.Use(Middleware{Name: "counter", Handler: func(c *gin.Context) {
			runs++
			c.Next()
		}})
		e.Route("GET", "/x", filler)
		e.Route("GET", "/y/", filler)
	})

	for _, c := range []struct {
		target string
		status int
	}{
		{"/x/", nethttp.StatusNotFound},
		{"/y", nethttp.StatusNotFound},
		{"/x", nethttp.StatusNoContent},
		{"/y/", nethttp.StatusNoContent},
	} {
		runs = 0
		recorder := serveOnce(handler, nethttp.MethodGet, c.target)

		if recorder.Code != c.status {
			t.Errorf("GET %s was answered %d, want %d", c.target, recorder.Code, c.status)
		}
		if location := recorder.Header().Get("Location"); location != "" {
			t.Errorf("GET %s was redirected to %q", c.target, location)
		}
		if runs != 1 {
			t.Errorf("the layer ran %d times for GET %s, want once: a redirect is answered before "+
				"any layer", runs, c.target)
		}
	}
}

// TestClientIPDoesNotTakeForwardingHeaders pins that the address gin reports
// for a request is the connection's peer. gin's default trusts every peer, so a
// client could put any address in X-Forwarded-For and have Context.ClientIP
// report it; the rate limiter and the audit trail would then be keyed by
// whatever the client wrote.
func TestClientIPDoesNotTakeForwardingHeaders(t *testing.T) {
	var reported string
	logger, _ := newRecordingLogger()
	handler := mustAssemble(t, testSettings("public"), nil, logger, func(e Endpoint) {
		e.Route("GET", "/things", func(c *gin.Context) { reported = c.ClientIP() })
	})

	request := httptest.NewRequest(nethttp.MethodGet, "/things", nil)
	request.RemoteAddr = "192.0.2.10:4711"
	request.Header.Set("X-Forwarded-For", "203.0.113.9")
	request.Header.Set("X-Real-IP", "203.0.113.10")
	handler.ServeHTTP(httptest.NewRecorder(), request)

	if reported != "192.0.2.10" {
		t.Errorf("ClientIP reported %q for a connection from 192.0.2.10 carrying forwarding "+
			"headers, want the connection's own address", reported)
	}
}

// TestTheGinContextFallsBackToTheRequestContext pins that a value put in the
// request context is reachable from the gin context used as a context.Context.
// The logger is put there by this module, and a handler or a layer that passes
// its gin context to log.FromContext, as it passes any context, gets the same
// logger it gets from the request's context. Without the fallback the gin
// context answers every key with nil and the process default logger is what the
// record is written with, with nothing to say so.
func TestTheGinContextFallsBackToTheRequestContext(t *testing.T) {
	logger, _ := newRecordingLogger()
	injected, _ := newRecordingLogger()

	var fromLayer, fromHandler, fromRequest *slog.Logger
	handler := mustAssemble(t, testSettings("public"), injected, logger, func(e Endpoint) {
		e.Use(Middleware{Name: "witness", Handler: func(c *gin.Context) {
			fromLayer = log.FromContext(c)
			c.Next()
		}})
		e.Route("GET", "/things", func(c *gin.Context) {
			fromHandler = log.FromContext(c)
			fromRequest = log.FromContext(c.Request.Context())
		})
	})

	serveOnce(handler, nethttp.MethodGet, "/things")

	if fromRequest != injected {
		t.Fatal("the request context does not carry the injected logger, so the rest proves nothing")
	}
	if fromHandler != fromRequest {
		t.Error("log.FromContext(c) in a handler is not the logger of the request's context")
	}
	if fromLayer != fromRequest {
		t.Error("log.FromContext(c) in a layer is not the logger of the request's context")
	}
}

// TestALayerReadsTheMatchedRouteBeforeNext pins the position the routing result
// is available in. The layers run inside the engine, after the route is
// matched, so a layer that decides by the route — the authentication layer's
// exemptions, the audit layer's match — reads Context.FullPath before it hands
// the request on, and what it reads is the path as it was registered. A request
// that matched nothing reads the empty string.
func TestALayerReadsTheMatchedRouteBeforeNext(t *testing.T) {
	var before string
	logger, _ := newRecordingLogger()
	handler := mustAssemble(t, testSettings("public"), nil, logger, func(e Endpoint) {
		e.Use(Middleware{Name: "reader", Handler: func(c *gin.Context) {
			before = c.FullPath()
			c.Next()
		}})
		e.Route("GET", "/", filler)
		e.Route("GET", "/things/:id/*rest", filler)
		e.Route("GET", "/files/", filler)
		e.Route("POST", "/files/:name", filler)
	})

	for _, c := range []struct {
		method, target, want string
	}{
		{nethttp.MethodGet, "/", "/"},
		{nethttp.MethodGet, "/things/7/a/b", "/things/:id/*rest"},
		{nethttp.MethodGet, "/files/", "/files/"},
		{nethttp.MethodPost, "/files/report.csv", "/files/:name"},
		{nethttp.MethodGet, "/nothing", ""},
		{nethttp.MethodDelete, "/files/report.csv", ""},
	} {
		before = "unset"
		serveOnce(handler, c.method, c.target)

		if before != c.want {
			t.Errorf("%s %s: the layer read the route %q before Next, want %q", c.method, c.target,
				before, c.want)
		}
	}
}

// freshProcessEnv, when set, makes this test binary run the check of gin's
// process-wide settings and exit instead of running the tests.
const freshProcessEnv = "SPEED_HTTP_GIN_SETTINGS_CHECK"

// TestMain runs the tests, or, in the child process TestGinsProcessWideSettings
// starts, the one check that cannot be made in a process that has run other
// tests.
func TestMain(m *testing.M) {
	if os.Getenv(freshProcessEnv) != "" {
		if err := checkGinsProcessWideSettings(); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		fmt.Println("gin settings as the design says")
		os.Exit(0)
	}
	os.Exit(m.Run())
}

// checkGinsProcessWideSettings builds and assembles an endpoint in a process
// that has done nothing else with gin, and reports what it left of gin's
// process-wide state.
func checkGinsProcessWideSettings() error {
	if mode := gin.Mode(); mode != gin.DebugMode {
		return fmt.Errorf("gin starts in %q mode, and the check needs it to start in %q to show a "+
			"change", mode, gin.DebugMode)
	}
	useNumber := binding.EnableDecoderUseNumber
	disallowUnknown := binding.EnableDecoderDisallowUnknownFields
	validator := binding.Validator
	debugWriter, errorWriter := gin.DefaultWriter, gin.DefaultErrorWriter

	r := newRouter([]endpointSettings{testSettings("public")})
	r.open()
	e, err := r.Endpoint("public")
	if err != nil {
		return err
	}
	e.Route("POST", "/things", filler)
	r.seal()
	logger, _ := newRecordingLogger()
	if _, err := r.endpoints["public"].assemble(nil, logger, nil); err != nil {
		return err
	}

	if mode := gin.Mode(); mode != gin.ReleaseMode {
		return fmt.Errorf("gin is in %q mode after an endpoint was built and assembled, want %q", mode, gin.ReleaseMode)
	}
	if binding.EnableDecoderUseNumber != useNumber {
		return errors.New("assembling an endpoint changed binding.EnableDecoderUseNumber")
	}
	if binding.EnableDecoderDisallowUnknownFields != disallowUnknown {
		return errors.New("assembling an endpoint changed binding.EnableDecoderDisallowUnknownFields")
	}
	if binding.Validator != validator {
		return errors.New("assembling an endpoint replaced binding.Validator")
	}
	if gin.DefaultWriter != debugWriter || gin.DefaultErrorWriter != errorWriter {
		return errors.New("assembling an endpoint changed where gin writes its own output")
	}
	return nil
}

// TestGinsProcessWideSettings pins what this module does to the state gin shares
// across a whole process, in a process of its own: it puts gin in release mode,
// because gin's debug mode prints a line to standard output for every route
// registered, which would put this module's records outside the logging module's
// destinations; and it leaves the rest alone, because the decoding options and
// the validator are shared by every gin engine in the process, so one module
// choosing a value for them would choose it for every other user of gin there.
//
// It runs the check in a child process because a process that has run other
// tests has already been through this module's engine construction: the mode is
// set, and a setting the construction changed is changed before any test can
// take the "before" reading. The child starts with gin in debug mode, which is
// what makes the change to release mode a change; and what gin printed while
// the child registered its routes is read here, since a mode that was not
// switched shows up as lines gin wrote.
func TestGinsProcessWideSettings(t *testing.T) {
	child := exec.Command(os.Args[0])
	child.Env = append(os.Environ(), freshProcessEnv+"=1", "GIN_MODE=debug")
	out, err := child.CombinedOutput()
	if err != nil {
		t.Fatalf("the check of gin's process-wide settings failed: %v\n%s", err, out)
	}
	if strings.Contains(string(out), "[GIN-debug]") {
		t.Errorf("gin wrote its debug output while the endpoint was built:\n%s", out)
	}
}

// TestTheChainKeepsTheWritersOptionalInterfaces pins that the interfaces the
// connection's writer carries reach a handler through everything this module
// puts around it: the layer that limits the body and builds the request context,
// the panic fallback, and gin's own writer. A layer that wrapped the writer and
// dropped them would leave a handler's type assertion failing quietly — a stream
// that arrives all at once at the end, an upgrade that cannot take the
// connection — and gin itself panics on the first call when the writer under it
// has lost the interface.
//
// Each interface is reached the two ways a handler reaches it: through gin's own
// writer, c.Writer.Flush and c.Writer.Hijack, which assert the interface on the
// writer they wrap, and through the response controller, which finds it by
// unwrapping. Both are observed on a real connection. The streaming handler
// flushes a first part and then waits for the test to confirm it arrived, which
// only happens if the flush reached the socket; the upgrading handler takes the
// connection over and answers on it by hand.
func TestTheChainKeepsTheWritersOptionalInterfaces(t *testing.T) {
	release := make(chan struct{})
	t.Cleanup(func() {
		select {
		case <-release:
		default:
			close(release)
		}
	})
	flushed := make(chan struct{}, 2)

	flushers := map[string]func(*gin.Context) error{
		"through gin's writer": func(c *gin.Context) error { c.Writer.Flush(); return nil },
		"through the response controller": func(c *gin.Context) error {
			return nethttp.NewResponseController(c.Writer).Flush()
		},
	}
	hijackers := map[string]func(*gin.Context) (net.Conn, error){
		"through gin's writer": func(c *gin.Context) (net.Conn, error) {
			conn, _, err := c.Writer.Hijack()
			return conn, err
		},
		"through the response controller": func(c *gin.Context) (net.Conn, error) {
			conn, _, err := nethttp.NewResponseController(c.Writer).Hijack()
			return conn, err
		},
	}
	paths := map[string]string{"through gin's writer": "gin", "through the response controller": "controller"}

	addr, _ := serveRoutes(t, nil, func(e Endpoint) {
		for way, flush := range flushers {
			e.Route("GET", "/stream-"+paths[way], func(c *gin.Context) {
				c.String(nethttp.StatusOK, "first;")
				if err := flush(c); err != nil {
					return
				}
				flushed <- struct{}{}
				<-release
				_, _ = io.WriteString(c.Writer, "second")
			})
		}
		for way, hijack := range hijackers {
			e.Route("GET", "/upgrade-"+paths[way], func(c *gin.Context) {
				conn, err := hijack(c)
				if err != nil {
					c.String(nethttp.StatusInternalServerError, "no hijack: %v", err)
					return
				}
				defer conn.Close()
				_, _ = io.WriteString(conn, "HTTP/1.1 200 OK\r\nContent-Length: 8\r\n\r\ntaken-up")
			})
		}
	})

	for way := range flushers {
		request, err := nethttp.NewRequestWithContext(context.Background(), nethttp.MethodGet,
			"http://"+addr+"/stream-"+paths[way], nil)
		if err != nil {
			t.Fatal(err)
		}
		client := &nethttp.Client{Timeout: waitDeadline, Transport: &nethttp.Transport{DisableKeepAlives: true}}
		resp, err := client.Do(request)
		if err != nil {
			t.Fatalf("flushing %s: the streaming request failed: %v", way, err)
		}
		first := make([]byte, len("first;"))
		if _, readErr := io.ReadFull(bufio.NewReader(resp.Body), first); readErr != nil || string(first) != "first;" {
			t.Fatalf("flushing %s: the first part of the stream was read as %q with %v: the flush did "+
				"not reach the client while the handler was still running", way, first, readErr)
		}
		waitFor(t, flushed, "the handler to report that its flush succeeded")
		_ = resp.Body.Close()
	}
	close(release)

	for way := range hijackers {
		got, err := getResponse(context.Background(), addr, "/upgrade-"+paths[way])
		if err != nil {
			t.Fatalf("hijacking %s: the request failed: %v", way, err)
		}
		if got.body != "taken-up" {
			t.Errorf("hijacking %s: the handler's answer was %d %q, want the one it wrote on the "+
				"connection it took", way, got.status, got.body)
		}
	}
}

// TestTheChainKeepsTheRestOfTheWritersInterfaces is the same statement for the
// interfaces that flushing and hijacking do not reach: the deadlines a handler
// sets through the response controller, which find the connection by unwrapping
// the writers around it, and the notification that the client went away, which
// gin asserts on the writer it wraps and which panics when the writer does not
// carry it.
//
// A handler that lost the first would find its call answered with "not
// supported" and its stream cut by the server's own timeout instead of the
// deadline it asked for; one that lost the second would panic in gin on the
// first call. The client going away is observed from the handler's side: the
// notification arrives when the test closes its connection.
func TestTheChainKeepsTheRestOfTheWritersInterfaces(t *testing.T) {
	gone := make(chan bool, 1)
	waiting := make(chan struct{}, 1)
	addr, _ := serveRoutes(t, nil, func(e Endpoint) {
		e.Route("GET", "/deadline", func(c *gin.Context) {
			controller := nethttp.NewResponseController(c.Writer)
			c.String(nethttp.StatusOK, "%v|%v", controller.SetWriteDeadline(time.Now().Add(time.Minute)),
				controller.SetReadDeadline(time.Now().Add(time.Minute)))
		})
		e.Route("GET", "/watch", func(c *gin.Context) {
			notified := c.Writer.CloseNotify()
			waiting <- struct{}{}
			select {
			case <-notified:
				gone <- true
			case <-time.After(waitDeadline):
				gone <- false
			}
		})
	})

	got, err := getResponse(context.Background(), addr, "/deadline")
	if err != nil {
		t.Fatalf("the request failed: %v", err)
	}
	if got.body != "<nil>|<nil>" {
		t.Errorf("setting the write and read deadlines through the response controller answered %q, "+
			"want no error for both: the writers around the connection hide it", got.body)
	}

	conn, err := net.DialTimeout("tcp", addr, waitDeadline)
	if err != nil {
		t.Fatalf("dialling the endpoint: %v", err)
	}
	defer conn.Close()
	if _, err := fmt.Fprintf(conn, "GET /watch HTTP/1.1\r\nHost: %s\r\n\r\n", addr); err != nil {
		t.Fatalf("writing the request: %v", err)
	}
	waitFor(t, waiting, "the handler to ask for the close notification")
	_ = conn.Close()
	select {
	case notified := <-gone:
		if !notified {
			t.Error("the handler was never told that the client went away")
		}
	case <-time.After(2 * waitDeadline):
		t.Fatal("the handler neither learnt that the client went away nor gave up")
	}
}

// pushOrNothing is a handler that reports what its writer offers for server push:
// "none" when gin hands it no Pusher, and otherwise the answer of a push whose
// target is not an absolute path.
//
// A relative target is refused by the HTTP/2 implementation with an error of its
// own before anything else is looked at, so an answer carrying that error shows
// the call reached the writer underneath. A layer that forwarded the interface
// to nothing would answer http.ErrNotSupported, which is also what the client's
// disabled push settings give for a target that passes: the relative one is what
// tells the two apart.
func pushOrNothing(c *gin.Context) {
	pusher := c.Writer.Pusher()
	if pusher == nil {
		c.String(nethttp.StatusOK, "none")
		return
	}
	c.String(nethttp.StatusOK, "pusher: %v", pusher.Push("relative.css", nil))
}

// TestThePusherOfAnHTTP2WriterReachesTheHandler pins that the interface an
// HTTP/2 stream's writer carries beyond the common ones, server push, is
// reachable from a handler, and that a call on it reaches the writer. gin hands
// out the writer's Pusher when the writer under it is one, and a layer around the
// writer that does not forward the interface turns it into nil: a handler that
// asks for it is told push is not available on a connection that offers it.
//
// A push that is accepted is not exercised: the client of Go's HTTP/2
// implementation switches it off, so what is pinned is that the interface and the
// call get through the chain.
func TestThePusherOfAnHTTP2WriterReachesTheHandler(t *testing.T) {
	certFile, keyFile, roots := writeSelfSignedCertificate(t, t.TempDir())
	s := testSettings("public")
	s.tlsCertFile, s.tlsKeyFile = certFile, keyFile
	logger, _ := newRecordingLogger()
	handler := mustAssemble(t, s, nil, logger, func(e Endpoint) {
		e.Route("GET", "/", pushOrNothing)
	})
	_, addr := startTestEndpoint(t, s, handler, logger)

	transport := &nethttp.Transport{
		TLSClientConfig:   &tls.Config{RootCAs: roots, NextProtos: []string{"h2", "http/1.1"}, MinVersion: tls.VersionTLS12},
		ForceAttemptHTTP2: true,
	}
	t.Cleanup(transport.CloseIdleConnections)
	resp, err := getOverTLS(&nethttp.Client{Transport: transport, Timeout: waitDeadline}, addr)
	if err != nil {
		t.Fatalf("the request failed: %v", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)

	if resp.ProtoMajor != 2 {
		t.Fatalf("the request went over %s, so this case did not exercise HTTP/2", resp.Proto)
	}
	if !strings.HasPrefix(string(body), "pusher: ") {
		t.Fatalf("the handler's writer gave no Pusher over HTTP/2: %q", body)
	}
	if strings.Contains(string(body), nethttp.ErrNotSupported.Error()) {
		t.Errorf("the handler's push was answered %q, which is what a Pusher that reaches nothing says; "+
			"the call has to reach the HTTP/2 writer", body)
	}
}

// TestThereIsNoPusherOnAnHTTP1Writer pins the other half of forwarding the
// interface: a connection that cannot push gives the handler no Pusher at all.
// gin hands a handler a nil Pusher for a writer that is not one, and handlers
// test for it; a layer that implemented the interface whatever it wrapped would
// turn "skip it" into "try it and be told no" on every HTTP/1.1 request.
func TestThereIsNoPusherOnAnHTTP1Writer(t *testing.T) {
	addr, _ := serveRoutes(t, nil, func(e Endpoint) {
		e.Route("GET", "/", pushOrNothing)
	})

	got, err := getResponse(context.Background(), addr, "/")
	if err != nil {
		t.Fatalf("the request failed: %v", err)
	}
	if got.body != "none" {
		t.Errorf("the handler's writer answered %q over HTTP/1.1, want no Pusher at all", got.body)
	}
}

// TestAFailedHijackOverHTTP2LeavesTheResponseToTheFallback pins that a hijack the
// writer refuses does not count as a start. An HTTP/2 stream has no connection
// to take over, so the call fails, and a handler that goes on to panic has
// written nothing: the panic is answered with the error response, as it is for a
// handler that never asked. A writer that recorded the hijack before it knew the
// outcome would take the response to have started and answer nothing.
//
// The control is the same panic without the hijack, which gets the same answer;
// the two are compared on the status and the media type.
func TestAFailedHijackOverHTTP2LeavesTheResponseToTheFallback(t *testing.T) {
	certFile, keyFile, roots := writeSelfSignedCertificate(t, t.TempDir())
	s := testSettings("public")
	s.tlsCertFile, s.tlsKeyFile = certFile, keyFile
	logger, _ := newRecordingLogger()
	hijackErrs := make(chan error, 1)
	handler := mustAssemble(t, s, nil, logger, func(e Endpoint) {
		e.Route("GET", "/after-a-failed-hijack", func(c *gin.Context) {
			_, _, err := c.Writer.Hijack()
			hijackErrs <- err
			panic("after the hijack failed")
		})
		e.Route("GET", "/plain", func(*gin.Context) { panic("with no hijack") })
	})
	_, addr := startTestEndpoint(t, s, handler, logger)

	transport := &nethttp.Transport{
		TLSClientConfig:   &tls.Config{RootCAs: roots, NextProtos: []string{"h2", "http/1.1"}, MinVersion: tls.VersionTLS12},
		ForceAttemptHTTP2: true,
	}
	t.Cleanup(transport.CloseIdleConnections)
	client := &nethttp.Client{Transport: transport, Timeout: waitDeadline}

	type outcome struct {
		proto, mediaType string
		status           int
	}
	answers := map[string]outcome{}
	for _, path := range []string{"/after-a-failed-hijack", "/plain"} {
		request, err := nethttp.NewRequestWithContext(context.Background(), nethttp.MethodGet, "https://"+addr+path, nil)
		if err != nil {
			t.Fatal(err)
		}
		resp, err := client.Do(request)
		if err != nil {
			t.Fatalf("%s: the request failed: %v", path, err)
		}
		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()
		answers[path] = outcome{proto: resp.Proto, mediaType: resp.Header.Get("Content-Type"), status: resp.StatusCode}
	}

	if err := <-hijackErrs; err == nil {
		t.Fatal("the HTTP/2 writer let a handler hijack, so this case did not exercise a failed hijack")
	}
	want := outcome{proto: "HTTP/2.0", mediaType: "application/problem+json", status: nethttp.StatusInternalServerError}
	for path, got := range answers {
		if got != want {
			t.Errorf("%s was answered %+v, want %+v", path, got, want)
		}
	}
}

// TestAHijackedConnectionIsNotWrittenToAfterAPanic pins the fallback's own use
// of the writer. A handler that has taken the connection over — a protocol
// upgrade — and then panics has no response left to answer: the connection is
// its own, and a status written through the server's writer now would be a
// write on a connection the server has let go, reported by net/http as an
// error of its own beside the record of the panic.
func TestAHijackedConnectionIsNotWrittenToAfterAPanic(t *testing.T) {
	addr, records := serveRoutes(t, nil, func(e Endpoint) {
		e.Route("GET", "/upgrade", func(c *gin.Context) {
			conn, _, err := nethttp.NewResponseController(c.Writer).Hijack()
			if err != nil {
				return
			}
			_, _ = io.WriteString(conn, "HTTP/1.1 200 OK\r\nContent-Length: 4\r\n\r\ntook")
			_ = conn.Close()
			panic("after the connection was taken")
		})
	})

	got, err := getResponse(context.Background(), addr, "/upgrade")
	if err != nil {
		t.Fatalf("the request failed: %v", err)
	}
	if got.body != "took" {
		t.Errorf("the client read %q, want the handler's own answer", got.body)
	}
	waitUntil(t, "the record of the panic", func() bool {
		return len(records.at(slog.LevelError)) >= 1
	})
	for _, line := range records.at(slog.LevelError) {
		if strings.Contains(line, "hijacked") {
			t.Errorf("net/http reported a write on the hijacked connection: %s", line)
		}
	}
	if written := records.at(slog.LevelError); len(written) != 1 {
		t.Errorf("the endpoint wrote %d error records for one panic, want one: %v", len(written), written)
	}
}
