package egress

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"io"
	"log"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"sync"
	"syscall"
	"testing"
	"testing/iotest"
	"time"
)

// TestNewRefusesLoopbackWithDefaultOptions holds the production constructor's
// default path: neither the zero Options nor Options with only roots reaches a
// loopback server.
func TestNewRefusesLoopbackWithDefaultOptions(t *testing.T) {
	var hits counter
	srv, pool := tlsServer(t, hits.handler(ok))
	for name, o := range map[string]Options{"zero": {}, "only roots": {RootCAs: pool}} {
		_, err := get(New(o), srv.URL)
		if !errors.Is(err, ErrDestinationDenied) {
			t.Errorf("%s: err = %v, want ErrDestinationDenied", name, err)
		}
	}
	if n := hits.n.Load(); n != 0 {
		t.Errorf("the server saw %d requests, want 0", n)
	}
}

// TestTheBodyCapHoldsForEveryStatus holds that an error or redirect response is
// capped like a success.
func TestTheBodyCapHoldsForEveryStatus(t *testing.T) {
	for _, code := range []int{http.StatusFound, http.StatusNotFound, http.StatusInternalServerError, http.StatusNoContent + 1} {
		srv, pool := tlsServer(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(code)
			_, _ = w.Write(bytes.Repeat([]byte{'a'}, MaxBodyBytes+1))
		}))
		resp, err := get(New(Options{AllowLoopback: true, RootCAs: pool}), srv.URL)
		if !errors.Is(err, ErrBodyTooLarge) {
			t.Errorf("status %d: read error = %v, want ErrBodyTooLarge", code, err)
		}
		if resp != nil {
			if got, _ := io.ReadAll(resp.Body); len(got) > MaxBodyBytes {
				t.Errorf("status %d: %d bytes", code, len(got))
			}
		}
	}
}

// TestTheBodyCapIsOneMebibyte pins the cap's value with literal sizes, end to
// end, so that a test sized from the constant cannot follow it when it moves.
func TestTheBodyCapIsOneMebibyte(t *testing.T) {
	if MaxBodyBytes != 1048576 {
		t.Fatalf("MaxBodyBytes = %d, want 1048576", MaxBodyBytes)
	}
	for _, c := range []struct {
		size    int
		wantErr bool
	}{{1048576, false}, {1048577, true}} {
		srv, pool := tlsServer(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.(http.Flusher).Flush()
			_, _ = w.Write(bytes.Repeat([]byte{'a'}, c.size))
		}))
		_, err := get(New(Options{AllowLoopback: true, RootCAs: pool}), srv.URL)
		if errors.Is(err, ErrBodyTooLarge) != c.wantErr {
			t.Errorf("%d bytes: err = %v, want ErrBodyTooLarge: %v", c.size, err, c.wantErr)
		}
	}
}

// errCloser is a body that records its close and answers it with err.
type errCloser struct {
	io.Reader
	err    error
	closed bool
}

func (c *errCloser) Close() error { c.closed = true; return c.err }

func TestCappedBodyCloseClosesTheWrappedBody(t *testing.T) {
	sentinel := errors.New("close failed")
	inner := &errCloser{Reader: strings.NewReader("x"), err: sentinel}
	b := &cappedBody{body: inner, left: 10}
	if err := b.Close(); !errors.Is(err, sentinel) {
		t.Errorf("Close = %v, want the wrapped body's error", err)
	}
	if !inner.closed {
		t.Error("the wrapped body was not closed")
	}
}

// TestCappedBodyPassesOnAReadError holds that an error from the wrapped body is
// delivered as it is and not turned into the end of the body.
func TestCappedBodyPassesOnAReadError(t *testing.T) {
	sentinel := errors.New("connection reset")
	b := &cappedBody{body: io.NopCloser(io.MultiReader(strings.NewReader("abc"), iotest.ErrReader(sentinel))), left: 10}
	got, err := io.ReadAll(b)
	if string(got) != "abc" || !errors.Is(err, sentinel) {
		t.Errorf("read %q, %v; want %q and the wrapped error", got, err, "abc")
	}
}

func TestATruncatedBodyIsAnErrorNotAShortBody(t *testing.T) {
	srv, pool := tlsServer(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Length", "100")
		_, _ = w.Write([]byte("0123456789"))
		w.(http.Flusher).Flush()
		panic(http.ErrAbortHandler)
	}))
	_, err := get(New(Options{AllowLoopback: true, RootCAs: pool}), srv.URL)
	if !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Errorf("err = %v, want io.ErrUnexpectedEOF", err)
	}
}

// TestDialAllGivesEachAttemptAShareOfTheRemainingDeadline holds the share: with
// three records and 12s left, the attempts have about 4s, 6s and 12s.
func TestDialAllGivesEachAttemptAShareOfTheRemainingDeadline(t *testing.T) {
	var mu sync.Mutex
	var left []time.Duration
	d := &net.Dialer{ControlContext: func(ctx context.Context, _, _ string, _ syscall.RawConn) error {
		dl, ok := ctx.Deadline()
		if !ok {
			t.Error("an attempt has no deadline")
		}
		mu.Lock()
		left = append(left, time.Until(dl))
		mu.Unlock()
		return errors.New("unreachable")
	}}
	lookup := func(context.Context, string) ([]netip.Addr, error) {
		return []netip.Addr{netip.MustParseAddr("8.8.8.8"), netip.MustParseAddr("8.8.4.4"), netip.MustParseAddr("1.1.1.1")}, nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
	defer cancel()
	if _, err := dialAll(d, lookup)(ctx, "tcp", "example.com:443"); err == nil {
		t.Fatal("the dial succeeded")
	}
	want := []time.Duration{4 * time.Second, 6 * time.Second, 12 * time.Second}
	if len(left) != len(want) {
		t.Fatalf("%d attempts, want %d", len(left), len(want))
	}
	for i := range want {
		if left[i] > want[i] || left[i] < want[i]-time.Second {
			t.Errorf("attempt %d had %v, want about %v", i, left[i], want[i])
		}
	}
}

// TestTheDialSeesTheDeadlineOfTheCall holds that the deadline of the call reaches
// the dial, which net/http hands a context detached from the call, so that the
// share per attempt takes effect in production and not only when dialAll is
// called directly.
func TestTheDialSeesTheDeadlineOfTheCall(t *testing.T) {
	srv, pool := tlsServer(t, http.HandlerFunc(ok))
	port := portOf(t, srv)
	var hasDeadline bool
	lookup := func(ctx context.Context, _ string) ([]netip.Addr, error) {
		_, hasDeadline = ctx.Deadline()
		return []netip.Addr{netip.MustParseAddr("127.0.0.1")}, nil
	}
	client := build(Options{AllowLoopback: true, RootCAs: pool}, hooks{lookup: lookup})
	if _, err := get(client, "https://example.com:"+port+"/"); err != nil {
		t.Fatal(err)
	}
	if !hasDeadline {
		t.Error("the context of the dial carries no deadline, so the share of the deadline per attempt never applies")
	}
}

// TestADeniedRecordIsReportedWhateverItsPosition holds that a denied record is
// among the causes whether it comes before or after the attempt that fails.
func TestADeniedRecordIsReportedWhateverItsPosition(t *testing.T) {
	srv, pool := tlsServer(t, http.HandlerFunc(ok))
	port := portOf(t, srv)
	names := &fixedLookup{table: map[string][]string{"example.com": {"10.1.2.3", "8.8.8.8"}}}
	j := judged{failAfter: map[string]bool{"8.8.8.8": true}}
	client := build(Options{RootCAs: pool}, hooks{lookup: names.lookup, control: j.hook})
	_, err := get(client, "https://example.com:"+port+"/")
	if !errors.Is(err, ErrDestinationDenied) {
		t.Errorf("err = %v, want ErrDestinationDenied among the causes", err)
	}
}

// TestTheDefaultResolverResolvesNames holds that the system resolver turns a
// name into addresses that are judged and connected to.
func TestTheDefaultResolverResolvesNames(t *testing.T) {
	srv, pool := tlsServer(t, http.HandlerFunc(ok))
	port := portOf(t, srv)
	t.Run("refused by default", func(t *testing.T) {
		var j judged
		client := build(Options{RootCAs: pool}, hooks{control: j.hook})
		_, err := get(client, "https://localhost:"+port+"/")
		if !errors.Is(err, ErrDestinationDenied) {
			t.Errorf("err = %v, want ErrDestinationDenied", err)
		}
		if len(j.seen()) == 0 {
			t.Error("no address was judged: the name was not resolved")
		}
	})
	t.Run("reached with the switch", func(t *testing.T) {
		client := New(Options{AllowLoopback: true, RootCAs: pool})
		_, err := get(client, "https://localhost:"+port+"/")
		var hostname x509.HostnameError
		if !errors.As(err, &hostname) {
			t.Errorf("err = %v, want the handshake to have run and rejected the name", err)
		}
	})
}

// TestDialAllFailsWhenResolutionDoes holds that a lookup that fails, or answers
// with nothing, is an error of dialAll itself and keeps its cause.
func TestDialAllFailsWhenResolutionDoes(t *testing.T) {
	sentinel := errors.New("lookup failed")
	for name, lookup := range map[string]func(context.Context, string) ([]netip.Addr, error){
		"lookup error": func(context.Context, string) ([]netip.Addr, error) { return nil, sentinel },
		"empty answer": func(context.Context, string) ([]netip.Addr, error) { return nil, nil },
	} {
		conn, err := dialAll(&net.Dialer{}, lookup)(context.Background(), "tcp", "example.com:443")
		if conn != nil || err == nil {
			t.Errorf("%s: got (%v, %v), want a nil connection and an error", name, conn, err)
		}
		if name == "lookup error" && !errors.Is(err, sentinel) {
			t.Errorf("%s: err = %v, want the lookup's error", name, err)
		}
	}
}

// TestASuppliedPoolStillVerifiesTheCertificate holds that a root pool narrows
// the roots and nothing else: an empty pool trusts nothing, and a trusted
// certificate still has to name the host.
func TestASuppliedPoolStillVerifiesTheCertificate(t *testing.T) {
	var hits counter
	srvB, _ := tlsServer(t, hits.handler(ok))
	t.Run("a certificate outside the pool", func(t *testing.T) {
		// A pool that holds no root trusts nothing; it must not read as "no pool".
		_, err := get(New(Options{AllowLoopback: true, RootCAs: x509.NewCertPool()}), srvB.URL)
		var unknown x509.UnknownAuthorityError
		if !errors.As(err, &unknown) {
			t.Errorf("err = %v, want an unknown-authority error", err)
		}
	})
	t.Run("a certificate for another name", func(t *testing.T) {
		srv, pool := tlsServer(t, hits.handler(ok))
		names := &fixedLookup{table: map[string][]string{"other.example": {"127.0.0.1"}}}
		client := build(Options{AllowLoopback: true, RootCAs: pool}, hooks{lookup: names.lookup})
		_, err := get(client, "https://other.example:"+portOf(t, srv)+"/")
		var hostname x509.HostnameError
		if !errors.As(err, &hostname) {
			t.Errorf("err = %v, want a hostname error", err)
		}
	})
	if n := hits.n.Load(); n != 0 {
		t.Errorf("the servers saw %d requests, want 0", n)
	}
}

// TestTheHandshakeIsBounded holds the transport's bounds that the package
// documentation relies on for a call whose context has no deadline.
func TestTheHandshakeIsBounded(t *testing.T) {
	tr := New(Options{}).Transport.(*guarded).next.(*http.Transport)
	if tr.TLSHandshakeTimeout <= 0 || tr.TLSHandshakeTimeout > 30*time.Second {
		t.Errorf("TLSHandshakeTimeout = %v, want a bound of at most 30s", tr.TLSHandshakeTimeout)
	}
	if tr.IdleConnTimeout <= 0 {
		t.Errorf("IdleConnTimeout = %v, want a bound", tr.IdleConnTimeout)
	}
}

// TestTheProtocolVersionFloor holds that TLS 1.2 is spoken and TLS 1.1 is not.
func TestTheProtocolVersionFloor(t *testing.T) {
	serve := func(min, max uint16) (*httptest.Server, *x509.CertPool) {
		srv := httptest.NewUnstartedServer(http.HandlerFunc(ok))
		srv.Config.ErrorLog = log.New(io.Discard, "", 0)
		srv.TLS = &tls.Config{MinVersion: min, MaxVersion: max}
		srv.StartTLS()
		t.Cleanup(srv.Close)
		pool := x509.NewCertPool()
		pool.AddCert(srv.Certificate())
		return srv, pool
	}
	t.Run("TLS 1.2 only is reachable", func(t *testing.T) {
		srv, pool := serve(tls.VersionTLS12, tls.VersionTLS12)
		if _, err := get(New(Options{AllowLoopback: true, RootCAs: pool}), srv.URL); err != nil {
			t.Errorf("err = %v", err)
		}
	})
	t.Run("TLS 1.1 only is refused", func(t *testing.T) {
		srv, pool := serve(tls.VersionTLS10, tls.VersionTLS11)
		if _, err := get(New(Options{AllowLoopback: true, RootCAs: pool}), srv.URL); err == nil {
			t.Error("a TLS 1.1 server was reached")
		}
	})
}

// TestTheDialEndsWithTheCall holds that the name resolution and the connection
// attempts started for a call stop when the call does. net/http runs the dial on
// a context that is detached from the call, so without help a call that gave up
// leaves the dial running behind it for as long as the operating system lets a
// connect attempt take.
func TestTheDialEndsWithTheCall(t *testing.T) {
	for name, end := range map[string]func(context.Context) (context.Context, context.CancelFunc){
		"the deadline passes": func(ctx context.Context) (context.Context, context.CancelFunc) {
			return context.WithTimeout(ctx, 100*time.Millisecond)
		},
		"the caller cancels": func(ctx context.Context) (context.Context, context.CancelFunc) {
			ctx, cancel := context.WithCancel(ctx)
			time.AfterFunc(100*time.Millisecond, cancel)
			return ctx, cancel
		},
	} {
		t.Run(name, func(t *testing.T) {
			ended := make(chan struct{})
			started := make(chan struct{})
			lookup := func(ctx context.Context, _ string) ([]netip.Addr, error) {
				close(started)
				<-ctx.Done()
				close(ended)
				return nil, ctx.Err()
			}
			client := build(Options{}, hooks{lookup: lookup})
			ctx, cancel := end(context.Background())
			defer cancel()
			req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://example.com/", nil)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := client.Do(req); err == nil {
				t.Fatal("the call succeeded")
			}
			<-started
			select {
			case <-ended:
			case <-time.After(5 * time.Second):
				t.Fatal("the lookup was still running 5s after the call ended")
			}
		})
	}
}

// TestTheHeadersOfAResponseAreBounded holds that the header block is bounded far
// below the transport's default of 10 MiB, which would be ten times the cap on
// the body.
func TestTheHeadersOfAResponseAreBounded(t *testing.T) {
	tr := New(Options{}).Transport.(*guarded).next.(*http.Transport)
	if tr.MaxResponseHeaderBytes <= 0 || tr.MaxResponseHeaderBytes > 64<<10 {
		t.Errorf("MaxResponseHeaderBytes = %d, want a bound of at most 64 KiB", tr.MaxResponseHeaderBytes)
	}
	srv, pool := tlsServer(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("X-Padding", strings.Repeat("a", 200<<10))
		_, _ = io.WriteString(w, "ok")
	}))
	_, err := get(New(Options{AllowLoopback: true, RootCAs: pool}), srv.URL)
	if err == nil || !strings.Contains(err.Error(), "headers exceeded") {
		t.Errorf("err = %v, want the header block over the bound to fail the call", err)
	}
}

// TestCloseIdleConnectionsReachesTheTransport holds that the owner of the client
// can release its idle connections, which the wrapper around the transport must
// not hide.
func TestCloseIdleConnectionsReachesTheTransport(t *testing.T) {
	states := make(chan http.ConnState, 16)
	srv := httptest.NewUnstartedServer(http.HandlerFunc(ok))
	// The server reclaims a connection left idle. Its timeout is set well
	// beyond the bound the test waits under, so a client that never carried
	// the call to the transport fails that wait instead of passing when the
	// server closes the connection on its own.
	srv.Config.IdleTimeout = 3 * time.Second
	srv.Config.ConnState = func(_ net.Conn, s http.ConnState) {
		states <- s
	}
	srv.StartTLS()
	t.Cleanup(srv.Close)
	pool := x509.NewCertPool()
	pool.AddCert(srv.Certificate())

	client := New(Options{AllowLoopback: true, RootCAs: pool})
	if _, err := get(client, srv.URL); err != nil {
		t.Fatal(err)
	}

	// StateIdle is the transition that says the connection is in the pool.
	// Waiting for it rather than for an interval is what keeps a loaded
	// machine from reading the connection before it has become idle.
	awaitState(t, states, 5*time.Second, http.StateIdle, "the connection to become idle")
	// Nothing has asked for a close yet, so a close reported now would be the
	// transport's own doing and the later one could not be attributed to the
	// call.
	for quiet := false; !quiet; {
		select {
		case s := <-states:
			if s == http.StateClosed {
				t.Fatal("the idle connection was closed before CloseIdleConnections was called")
			}
		default:
			quiet = true
		}
	}

	client.CloseIdleConnections()
	awaitState(t, states, time.Second, http.StateClosed, "the idle connection to close after CloseIdleConnections")
}

// awaitState takes transitions until want arrives, failing when the deadline
// passes first and when the connection closes while another state is awaited.
func awaitState(t *testing.T, states <-chan http.ConnState, deadline time.Duration, want http.ConnState, what string) {
	t.Helper()
	timeout := time.After(deadline)
	for {
		select {
		case s := <-states:
			if s == want {
				return
			}
			if s == http.StateClosed {
				t.Fatalf("the connection closed while waiting for %s", what)
			}
		case <-timeout:
			t.Fatalf("timed out after %s waiting for %s", deadline, what)
		}
	}
}

// TestTheIdlePoolIsFinite holds that the idle connections kept across all hosts
// have a bound, as the ones per host have.
func TestTheIdlePoolIsFinite(t *testing.T) {
	tr := New(Options{}).Transport.(*guarded).next.(*http.Transport)
	if tr.MaxIdleConns <= 0 {
		t.Errorf("MaxIdleConns = %d, want a bound", tr.MaxIdleConns)
	}
}
