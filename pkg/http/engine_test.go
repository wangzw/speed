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

// TestATrailingSlashIsNotRedirected pins the redirect gin does by default and this module turns
// off.
func TestATrailingSlashIsNotRedirected(t *testing.T) {
	var runs int
	logger, _ := newRecordingLogger()
	handler := mustAssemble(t, testSettings("public"), nil, logger, func(e Endpoint) {
		e.Use(Middleware{Name: "counter", Stage: StageObserve, Handler: func(c *gin.Context) {
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

// TestClientIPDoesNotTakeForwardingHeaders pins that the address gin reports for a request is the
// connection's peer.
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

// TestTheGinContextFallsBackToTheRequestContext pins that a value put in the request context is
// reachable from the gin context used as a context.Context.
func TestTheGinContextFallsBackToTheRequestContext(t *testing.T) {
	logger, _ := newRecordingLogger()
	injected, _ := newRecordingLogger()

	var fromLayer, fromHandler, fromRequest *slog.Logger
	handler := mustAssemble(t, testSettings("public"), injected, logger, func(e Endpoint) {
		e.Use(Middleware{Name: "witness", Stage: StageObserve, Handler: func(c *gin.Context) {
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

// TestALayerReadsTheMatchedRouteBeforeNext pins the position the routing result is available in.
func TestALayerReadsTheMatchedRouteBeforeNext(t *testing.T) {
	var before string
	logger, _ := newRecordingLogger()
	handler := mustAssemble(t, testSettings("public"), nil, logger, func(e Endpoint) {
		e.Use(Middleware{Name: "reader", Stage: StageObserve, Handler: func(c *gin.Context) {
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

const freshProcessEnv = "SPEED_HTTP_GIN_SETTINGS_CHECK"

// TestMain runs the tests, or, in the child process TestGinsProcessWideSettings starts, the one
// check that cannot be made in a process that has run other tests.
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

func checkGinsProcessWideSettings() error {
	if mode := gin.Mode(); mode != gin.DebugMode {
		return fmt.Errorf("gin starts in %q mode, and the check needs it to start in %q to show a "+
			"change", mode, gin.DebugMode)
	}
	useNumber := binding.EnableDecoderUseNumber
	disallowUnknown := binding.EnableDecoderDisallowUnknownFields
	validator := binding.Validator
	debugWriter, errorWriter := gin.DefaultWriter, gin.DefaultErrorWriter

	r := newRouter([]listenerSettings{testListener("backend")}, []endpointSettings{testSettings("public")})
	r.open()
	e, err := r.Endpoint("public")
	if err != nil {
		return err
	}
	e.Route("POST", "/things", filler)
	r.seal()
	logger, _ := newRecordingLogger()
	if _, err := r.endpoints["public"].assemble(nil, logger, nil, nil, nil); err != nil {
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

// TestGinsProcessWideSettings pins what this module does to the state gin shares across a whole
// process, in a process of its own: it puts gin in release mode.
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

// TestTheChainKeepsTheWritersOptionalInterfaces pins that the interfaces the connection's writer
// carries reach a handler through everything this module puts around it.
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

	limit := waitDeadline()
	for way := range flushers {
		request, err := nethttp.NewRequestWithContext(context.Background(), nethttp.MethodGet,
			"http://"+addr+"/stream-"+paths[way], nil)
		if err != nil {
			t.Fatal(err)
		}
		client := &nethttp.Client{Timeout: limit, Transport: &nethttp.Transport{DisableKeepAlives: true}}
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

// TestTheChainKeepsTheRestOfTheWritersInterfaces checks that the response controller's deadlines
// survive the chain.
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
			limit := waitDeadline()
			select {
			case <-notified:
				gone <- true
			case <-time.After(limit):
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

	conn, err := net.DialTimeout("tcp", addr, waitDeadline())
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
	case <-time.After(2 * waitDeadline()):
		t.Fatal("the handler neither learnt that the client went away nor gave up")
	}
}

func pushOrNothing(c *gin.Context) {
	pusher := c.Writer.Pusher()
	if pusher == nil {
		c.String(nethttp.StatusOK, "none")
		return
	}
	c.String(nethttp.StatusOK, "pusher: %v", pusher.Push("relative.css", nil))
}

// TestThePusherOfAnHTTP2WriterReachesTheHandler pins that the interface an HTTP/2 stream's writer
// carries beyond the common ones, server push, is reachable from a handler.
func TestThePusherOfAnHTTP2WriterReachesTheHandler(t *testing.T) {
	certFile, keyFile, roots := writeSelfSignedCertificate(t, t.TempDir())
	l := testListener("public")
	l.tlsCertFile, l.tlsKeyFile = certFile, keyFile
	logger, _ := newRecordingLogger()
	handler := mustAssemble(t, testSettings("public"), nil, logger, func(e Endpoint) {
		e.Route("GET", "/", pushOrNothing)
	})
	_, addr := startTestEndpoint(t, l, handler, logger)

	transport := &nethttp.Transport{
		TLSClientConfig:   &tls.Config{RootCAs: roots, NextProtos: []string{"h2", "http/1.1"}, MinVersion: tls.VersionTLS12},
		ForceAttemptHTTP2: true,
	}
	t.Cleanup(transport.CloseIdleConnections)
	resp, err := getOverTLS(&nethttp.Client{Transport: transport, Timeout: waitDeadline()}, addr)
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

// TestThereIsNoPusherOnAnHTTP1Writer pins the other half of forwarding the interface: a connection
// that cannot push gives the handler no Pusher at all.
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

// TestAFailedHijackOverHTTP2LeavesTheResponseUnanswered pins what a handler that asks its writer
// for the connection gets over HTTP/2, where there is no connection to take over.
func TestAFailedHijackOverHTTP2LeavesTheResponseUnanswered(t *testing.T) {
	certFile, keyFile, roots := writeSelfSignedCertificate(t, t.TempDir())
	l := testListener("public")
	l.tlsCertFile, l.tlsKeyFile = certFile, keyFile
	logger, _ := newRecordingLogger()
	handler := mustAssemble(t, testSettings("public"), nil, logger, func(e Endpoint) {
		e.Route("GET", "/hijack", func(c *gin.Context) {
			_, _, _ = c.Writer.Hijack()
			panic("after the hijack failed")
		})
		e.Route("GET", "/plain", func(*gin.Context) { panic("with no hijack") })
	})
	_, addr := startTestEndpoint(t, l, handler, logger)

	transport := &nethttp.Transport{
		TLSClientConfig:   &tls.Config{RootCAs: roots, NextProtos: []string{"h2", "http/1.1"}, MinVersion: tls.VersionTLS12},
		ForceAttemptHTTP2: true,
	}
	t.Cleanup(transport.CloseIdleConnections)
	client := &nethttp.Client{Transport: transport, Timeout: waitDeadline()}

	type outcome struct {
		proto, mediaType, body string
		status                 int
	}
	answers := map[string]outcome{}
	for _, path := range []string{"/hijack", "/plain"} {
		request, err := nethttp.NewRequestWithContext(context.Background(), nethttp.MethodGet, "https://"+addr+path, nil)
		if err != nil {
			t.Fatal(err)
		}
		resp, err := client.Do(request)
		if err != nil {
			t.Fatalf("%s: the request failed: %v", path, err)
		}
		body, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		answers[path] = outcome{
			proto: resp.Proto, mediaType: resp.Header.Get("Content-Type"),
			body: string(body), status: resp.StatusCode,
		}
	}

	if got := answers["/hijack"]; got.proto != "HTTP/2.0" || got.status != nethttp.StatusOK || got.body != "" {
		t.Errorf("the handler that asked for the connection was answered %+v, want the empty 200 the "+
			"failed hijack leaves over HTTP/2", got)
	}
	want := outcome{proto: "HTTP/2.0", mediaType: "application/problem+json", status: nethttp.StatusInternalServerError}
	if got := answers["/plain"]; got.status != want.status || got.mediaType != want.mediaType || got.proto != want.proto {
		t.Errorf("the handler that panicked without asking for the connection was answered %+v, want %+v",
			got, want)
	}
}

// TestAHijackedConnectionIsNotWrittenToAfterAPanic pins the fallback's own use of the writer.
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
