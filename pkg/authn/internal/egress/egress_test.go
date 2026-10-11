package egress

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/x509"
	"errors"
	"io"
	"log"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"testing/iotest"
	"time"

	"github.com/vislake/speed/pkg/observability"
)

// judged records every address the connection-time judgement was asked about,
// and can make an attempt fail after it was admitted, as an unreachable host
// would.
type judged struct {
	mu        sync.Mutex
	addrs     []string
	failAfter map[string]bool
}

func (j *judged) hook(next controlFunc) controlFunc {
	return func(network, address string, c syscall.RawConn) error {
		j.mu.Lock()
		j.addrs = append(j.addrs, address)
		fail := j.failAfter[hostOf(address)]
		j.mu.Unlock()
		if err := next(network, address, c); err != nil {
			return err
		}
		if fail {
			return errors.New("simulated unreachable host")
		}
		return nil
	}
}

func (j *judged) seen() []string {
	j.mu.Lock()
	defer j.mu.Unlock()
	return append([]string(nil), j.addrs...)
}

func hostOf(address string) string {
	h, _, err := net.SplitHostPort(address)
	if err != nil {
		return address
	}
	return h
}

// fixedLookup answers every name the table holds and records the names asked.
type fixedLookup struct {
	mu    sync.Mutex
	table map[string][]string
	asked []string
}

func (f *fixedLookup) lookup(_ context.Context, host string) ([]netip.Addr, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.asked = append(f.asked, host)
	answers, ok := f.table[host]
	if !ok {
		return nil, errors.New("no such host in the test resolver")
	}
	out := make([]netip.Addr, len(answers))
	for i, a := range answers {
		out[i] = netip.MustParseAddr(a)
	}
	return out, nil
}

// tlsServer starts a TLS server on the loopback interface and returns it with a
// pool that trusts its certificate. Its certificate names example.com and the
// loopback addresses.
//
// The server's error log is discarded: the cases that refuse a certificate or
// a protocol version end the handshake on the client's side by design, and the
// server would log each of them as a handshake error.
func tlsServer(t *testing.T, h http.Handler) (*httptest.Server, *x509.CertPool) {
	t.Helper()
	srv := httptest.NewUnstartedServer(h)
	srv.Config.ErrorLog = log.New(io.Discard, "", 0)
	srv.StartTLS()
	t.Cleanup(srv.Close)
	pool := x509.NewCertPool()
	pool.AddCert(srv.Certificate())
	return srv, pool
}

func portOf(t *testing.T, srv *httptest.Server) string {
	t.Helper()
	_, port, err := net.SplitHostPort(strings.TrimPrefix(srv.URL, "https://"))
	if err != nil {
		t.Fatalf("server address %q: %v", srv.URL, err)
	}
	return port
}

func get(client *http.Client, url string) (*http.Response, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	// The deadline goes with the function, so the body is read before it.
	body, err := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	resp.Body = io.NopCloser(bytes.NewReader(body))
	return resp, err
}

type counter struct {
	n atomic.Int32
}

func (c *counter) handler(next http.HandlerFunc) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c.n.Add(1)
		next(w, r)
	})
}

func ok(w http.ResponseWriter, _ *http.Request) {
	_, _ = io.WriteString(w, "ok")
}

func TestALoopbackServerIsRefusedByDefault(t *testing.T) {
	var hits counter
	srv, pool := tlsServer(t, hits.handler(ok))
	var j judged
	client := build(Options{RootCAs: pool}, hooks{control: j.hook})

	_, err := get(client, srv.URL)

	if !errors.Is(err, ErrDestinationDenied) {
		t.Fatalf("err = %v, want ErrDestinationDenied", err)
	}
	if n := hits.n.Load(); n != 0 {
		t.Errorf("the server saw %d requests, want 0", n)
	}
	if got := j.seen(); len(got) != 1 {
		t.Errorf("judged %v, want exactly the one attempt", got)
	}
}

func TestTheDevelopmentSwitchReachesLoopback(t *testing.T) {
	var hits counter
	srv, pool := tlsServer(t, hits.handler(ok))
	client := New(Options{AllowLoopback: true, RootCAs: pool})

	resp, err := get(client, srv.URL)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK || string(body) != "ok" {
		t.Errorf("got %d %q, want 200 ok", resp.StatusCode, body)
	}
	if n := hits.n.Load(); n != 1 {
		t.Errorf("the server saw %d requests, want 1", n)
	}
}

func TestTheDevelopmentSwitchDoesNotOpenAnythingButLoopback(t *testing.T) {
	var j judged
	client := build(Options{AllowLoopback: true}, hooks{control: j.hook})

	for _, host := range []string{"10.1.2.3", "169.254.169.254", "[fe80::1]", "[64:ff9b::a9fe:a9fe]"} {
		_, err := get(client, "https://"+host+":443/")
		if !errors.Is(err, ErrDestinationDenied) {
			t.Errorf("%s: err = %v, want ErrDestinationDenied", host, err)
		}
	}
}

func TestACertificateTheRootsDoNotTrustIsRefused(t *testing.T) {
	var hits counter
	srv, _ := tlsServer(t, hits.handler(ok))
	client := New(Options{AllowLoopback: true})

	_, err := get(client, srv.URL)

	var unknown x509.UnknownAuthorityError
	if !errors.As(err, &unknown) {
		t.Fatalf("err = %v, want an unknown-authority error", err)
	}
	if n := hits.n.Load(); n != 0 {
		t.Errorf("the server saw %d requests, want 0", n)
	}
}

func TestTheClientCarriesNoTotalTimeoutAndNoProxy(t *testing.T) {
	client := New(Options{})
	if client.Timeout != 0 {
		t.Errorf("Timeout = %v, want 0: the deadline belongs to each call's context", client.Timeout)
	}
	tr := client.Transport.(*guarded).next.(*http.Transport)
	if tr.Proxy != nil {
		t.Error("the transport has a Proxy function")
	}
	if tr.TLSClientConfig.InsecureSkipVerify {
		t.Error("certificate verification is switched off")
	}
	if tr.TLSClientConfig.MinVersion < 0x0303 {
		t.Errorf("MinVersion = %#x, want TLS 1.2 or above", tr.TLSClientConfig.MinVersion)
	}
}

func TestARedirectIsHandedBackAndNotFollowed(t *testing.T) {
	var hits counter
	mux := http.NewServeMux()
	mux.HandleFunc("/start", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/next", http.StatusFound)
	})
	mux.HandleFunc("/next", ok)
	srv, pool := tlsServer(t, hits.handler(mux.ServeHTTP))
	client := New(Options{AllowLoopback: true, RootCAs: pool})

	resp, err := get(client, srv.URL+"/start")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if resp.StatusCode != http.StatusFound || resp.Header.Get("Location") != "/next" {
		t.Errorf("got %d Location %q, want the 302 as the server sent it", resp.StatusCode, resp.Header.Get("Location"))
	}
	if n := hits.n.Load(); n != 1 {
		t.Errorf("the server saw %d requests, want 1: the redirect was followed", n)
	}
}

func TestARedirectToAnotherHostIsNotFollowed(t *testing.T) {
	var hits counter
	srv, pool := tlsServer(t, hits.handler(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "https://169.254.169.254/latest/meta-data/", http.StatusTemporaryRedirect)
	}))
	var j judged
	client := build(Options{AllowLoopback: true, RootCAs: pool}, hooks{control: j.hook})

	resp, err := get(client, srv.URL)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if resp.StatusCode != http.StatusTemporaryRedirect {
		t.Errorf("status = %d, want 307", resp.StatusCode)
	}
	if got := j.seen(); len(got) != 1 {
		t.Errorf("connection attempts %v, want only the first request's", got)
	}
}

func TestAnyOtherSchemeIsRefusedBeforeDialling(t *testing.T) {
	var hits counter
	plain := httptest.NewServer(hits.handler(ok))
	t.Cleanup(plain.Close)

	var j judged
	// The switch is on, so the loopback server is reachable by address: the
	// scheme is the only thing standing between the request and a response.
	client := build(Options{AllowLoopback: true}, hooks{control: j.hook})

	for _, url := range []string{plain.URL, "ftp://127.0.0.1:21/", "file:///etc/passwd", "ws://127.0.0.1:1/"} {
		_, err := get(client, url)
		if !errors.Is(err, ErrSchemeNotHTTPS) {
			t.Errorf("%s: err = %v, want ErrSchemeNotHTTPS", url, err)
		}
	}
	if n := hits.n.Load(); n != 0 {
		t.Errorf("the server saw %d requests, want 0", n)
	}
	if got := j.seen(); len(got) != 0 {
		t.Errorf("connection attempts %v before the scheme was refused, want none", got)
	}
}

func TestARefusedSchemeClosesTheRequestBody(t *testing.T) {
	client := New(Options{})
	body := &closeRecorder{Reader: strings.NewReader("x")}
	req, err := http.NewRequest(http.MethodPost, "http://127.0.0.1/", body)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.Do(req); !errors.Is(err, ErrSchemeNotHTTPS) {
		t.Fatalf("err = %v, want ErrSchemeNotHTTPS", err)
	}
	if !body.closed.Load() {
		t.Error("the request body was not closed")
	}
}

type closeRecorder struct {
	io.Reader
	closed atomic.Bool
}

func (c *closeRecorder) Close() error {
	c.closed.Store(true)
	return nil
}

func TestTheBodyCap(t *testing.T) {
	payload := func(n int) []byte { return bytes.Repeat([]byte{'a'}, n) }
	gz := func(n int) []byte {
		var buf bytes.Buffer
		w := gzip.NewWriter(&buf)
		_, _ = w.Write(payload(n))
		_ = w.Close()
		return buf.Bytes()
	}
	cases := []struct {
		name    string
		serve   func(w http.ResponseWriter)
		wantLen int
		wantErr bool
	}{
		{"exactly the cap, with a length", func(w http.ResponseWriter) {
			w.Header().Set("Content-Length", strconv.Itoa(MaxBodyBytes))
			_, _ = w.Write(payload(MaxBodyBytes))
		}, MaxBodyBytes, false},
		{"exactly the cap, chunked", func(w http.ResponseWriter) {
			w.(http.Flusher).Flush()
			_, _ = w.Write(payload(MaxBodyBytes))
		}, MaxBodyBytes, false},
		{"one byte over, with a length", func(w http.ResponseWriter) {
			w.Header().Set("Content-Length", strconv.Itoa(MaxBodyBytes+1))
			_, _ = w.Write(payload(MaxBodyBytes + 1))
		}, MaxBodyBytes, true},
		{"one byte over, chunked", func(w http.ResponseWriter) {
			w.(http.Flusher).Flush()
			_, _ = w.Write(payload(MaxBodyBytes + 1))
		}, MaxBodyBytes, true},
		{"far over, chunked", func(w http.ResponseWriter) {
			w.(http.Flusher).Flush()
			_, _ = w.Write(payload(3 * MaxBodyBytes))
		}, MaxBodyBytes, true},
		{"under the cap compressed, over it decoded", func(w http.ResponseWriter) {
			w.Header().Set("Content-Encoding", "gzip")
			_, _ = w.Write(gz(MaxBodyBytes + 1))
		}, MaxBodyBytes, true},
		{"exactly the cap decoded", func(w http.ResponseWriter) {
			w.Header().Set("Content-Encoding", "gzip")
			_, _ = w.Write(gz(MaxBodyBytes))
		}, MaxBodyBytes, false},
		{"empty", func(http.ResponseWriter) {}, 0, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			srv, pool := tlsServer(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { c.serve(w) }))
			client := New(Options{AllowLoopback: true, RootCAs: pool})

			resp, err := get(client, srv.URL)
			if err != nil && !errors.Is(err, ErrBodyTooLarge) {
				t.Fatalf("get: %v", err)
			}
			if c.wantErr != errors.Is(err, ErrBodyTooLarge) {
				t.Errorf("read error = %v, want ErrBodyTooLarge: %v", err, c.wantErr)
			}
			got, _ := io.ReadAll(resp.Body)
			if len(got) != c.wantLen {
				t.Errorf("read %d bytes, want %d", len(got), c.wantLen)
			}
		})
	}
}

func TestCappedBodyAtTheBoundary(t *testing.T) {
	const limit = 10
	data := func(n int) string { return strings.Repeat("x", n) }
	cases := []struct {
		name    string
		reader  func(string) io.Reader
		size    int
		wantLen int
		wantErr bool
	}{
		{"under", func(s string) io.Reader { return strings.NewReader(s) }, limit - 1, limit - 1, false},
		{"at the cap", func(s string) io.Reader { return strings.NewReader(s) }, limit, limit, false},
		{"one over", func(s string) io.Reader { return strings.NewReader(s) }, limit + 1, limit, true},
		{"at the cap, data and EOF together", func(s string) io.Reader { return iotest.DataErrReader(strings.NewReader(s)) }, limit, limit, false},
		{"one over, data and EOF together", func(s string) io.Reader { return iotest.DataErrReader(strings.NewReader(s)) }, limit + 1, limit, true},
		{"at the cap, a byte at a time", func(s string) io.Reader { return iotest.OneByteReader(strings.NewReader(s)) }, limit, limit, false},
		{"one over, a byte at a time", func(s string) io.Reader { return iotest.OneByteReader(strings.NewReader(s)) }, limit + 1, limit, true},
		{"far over", func(s string) io.Reader { return strings.NewReader(s) }, 5 * limit, limit, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			b := &cappedBody{body: io.NopCloser(c.reader(data(c.size))), left: limit}

			got, err := io.ReadAll(b)

			if len(got) != c.wantLen {
				t.Errorf("read %d bytes, want %d", len(got), c.wantLen)
			}
			if errors.Is(err, ErrBodyTooLarge) != c.wantErr {
				t.Errorf("err = %v, want ErrBodyTooLarge: %v", err, c.wantErr)
			}
			if c.wantErr {
				// The failure is permanent: it does not turn into an EOF.
				if n, again := b.Read(make([]byte, 4)); n != 0 || !errors.Is(again, ErrBodyTooLarge) {
					t.Errorf("read after the failure = %d, %v, want 0 and ErrBodyTooLarge", n, again)
				}
			}
		})
	}
}

func TestAProxyInTheEnvironmentIsNotUsed(t *testing.T) {
	var hits counter
	srv, pool := tlsServer(t, hits.handler(ok))

	// A listener standing in for the proxy, counting what connects to it.
	proxy, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = proxy.Close() })
	var proxied atomic.Int32
	go func() {
		for {
			c, acceptErr := proxy.Accept()
			if acceptErr != nil {
				return
			}
			proxied.Add(1)
			_ = c.Close()
		}
	}()
	for _, name := range []string{"HTTPS_PROXY", "https_proxy", "HTTP_PROXY", "http_proxy", "ALL_PROXY", "all_proxy"} {
		t.Setenv(name, "http://"+proxy.Addr().String())
	}
	for _, name := range []string{"NO_PROXY", "no_proxy"} {
		t.Setenv(name, "")
	}

	// The name is not a loopback one, which the environment's proxy logic
	// would skip on its own.
	names := &fixedLookup{table: map[string][]string{"example.com": {"127.0.0.1"}}}
	client := build(Options{AllowLoopback: true, RootCAs: pool}, hooks{lookup: names.lookup})

	resp, err := get(client, "https://example.com:"+portOf(t, srv)+"/")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want 200", resp.StatusCode)
	}
	if n := hits.n.Load(); n != 1 {
		t.Errorf("the server saw %d requests, want 1", n)
	}
	if n := proxied.Load(); n != 0 {
		t.Errorf("%d connections went to the proxy, want 0", n)
	}
}

func TestEveryRecordOfANameIsJudged(t *testing.T) {
	var hits counter
	srv, pool := tlsServer(t, hits.handler(ok))
	port := portOf(t, srv)

	t.Run("a public record that fails to connect, then a private one", func(t *testing.T) {
		names := &fixedLookup{table: map[string][]string{"example.com": {"8.8.8.8", "127.0.0.1"}}}
		j := judged{failAfter: map[string]bool{"8.8.8.8": true}}
		client := build(Options{RootCAs: pool}, hooks{lookup: names.lookup, control: j.hook})

		_, err := get(client, "https://example.com:"+port+"/")

		if !errors.Is(err, ErrDestinationDenied) {
			t.Fatalf("err = %v, want ErrDestinationDenied", err)
		}
		want := []string{"8.8.8.8:" + port, "127.0.0.1:" + port}
		if got := j.seen(); !equal(got, want) {
			t.Errorf("judged %v, want %v: the second record was not judged", got, want)
		}
		if n := hits.n.Load(); n != 0 {
			t.Errorf("the server saw %d requests, want 0", n)
		}
	})

	t.Run("a private record, then a loopback one the switch admits", func(t *testing.T) {
		names := &fixedLookup{table: map[string][]string{"example.com": {"10.1.2.3", "127.0.0.1"}}}
		var j judged
		client := build(Options{AllowLoopback: true, RootCAs: pool}, hooks{lookup: names.lookup, control: j.hook})

		resp, err := get(client, "https://example.com:"+port+"/")
		if err != nil {
			t.Fatalf("get: %v", err)
		}
		if resp.StatusCode != http.StatusOK {
			t.Errorf("status = %d, want 200", resp.StatusCode)
		}
		want := []string{"10.1.2.3:" + port, "127.0.0.1:" + port}
		if got := j.seen(); !equal(got, want) {
			t.Errorf("judged %v, want %v", got, want)
		}
	})

	t.Run("a name that moves from a public to a private address between calls", func(t *testing.T) {
		var calls atomic.Int32
		lookup := func(context.Context, string) ([]netip.Addr, error) {
			if calls.Add(1) == 1 {
				return []netip.Addr{netip.MustParseAddr("8.8.8.8")}, nil
			}
			return []netip.Addr{netip.MustParseAddr("169.254.169.254")}, nil
		}
		j := judged{failAfter: map[string]bool{"8.8.8.8": true}}
		client := build(Options{RootCAs: pool}, hooks{lookup: lookup, control: j.hook})

		_, first := get(client, "https://example.com:"+port+"/")
		_, second := get(client, "https://example.com:"+port+"/")

		if errors.Is(first, ErrDestinationDenied) || first == nil {
			t.Errorf("first call err = %v, want the simulated connect failure", first)
		}
		if !errors.Is(second, ErrDestinationDenied) {
			t.Errorf("second call err = %v, want ErrDestinationDenied", second)
		}
	})
}

func equal(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestAnIPv4LiteralInAnotherNotationIsJudgedByWhereItConnects(t *testing.T) {
	var hits counter
	srv, pool := tlsServer(t, hits.handler(ok))
	port := portOf(t, srv)
	hosts := []string{"0x7f.0.0.1", "0177.0.0.1", "2130706433", "127.1", "0x7f000001", "0177.1", "127.0.1"}

	for _, host := range hosts {
		if _, err := netip.ParseAddr(host); err == nil {
			t.Fatalf("%s parses as an address, so this vector no longer exercises the resolver path", host)
		}
	}

	t.Run("a resolver that reads them as loopback, as getaddrinfo does", func(t *testing.T) {
		table := map[string][]string{}
		for _, h := range hosts {
			table[h] = []string{"127.0.0.1"}
		}
		names := &fixedLookup{table: table}
		var j judged
		client := build(Options{RootCAs: pool}, hooks{lookup: names.lookup, control: j.hook})

		for _, host := range hosts {
			_, err := get(client, "https://"+host+":"+port+"/")
			if !errors.Is(err, ErrDestinationDenied) {
				t.Errorf("%s: err = %v, want ErrDestinationDenied", host, err)
			}
		}
		if len(names.asked) != len(hosts) {
			t.Errorf("the resolver was asked for %v, want each of %v", names.asked, hosts)
		}
		for _, a := range j.seen() {
			if got := hostOf(a); got != "127.0.0.1" {
				t.Errorf("judged %s, want the loopback address the resolver gave", a)
			}
		}
		if n := hits.n.Load(); n != 0 {
			t.Errorf("the server saw %d requests, want 0", n)
		}
	})
}

func TestTheConnectionTimeJudgement(t *testing.T) {
	cases := []struct {
		network, address string
		allowLoopback    bool
		wantDenied       bool
	}{
		{"tcp", "8.8.8.8:443", false, false},
		{"tcp4", "8.8.8.8:443", false, false},
		{"tcp6", "[2606:4700:4700::1111]:443", false, false},
		{"tcp", "127.0.0.1:443", false, true},
		{"tcp", "127.0.0.1:443", true, false},
		{"tcp6", "[::1]:443", false, true},
		{"tcp6", "[::1]:443", true, false},
		{"tcp6", "[::ffff:127.0.0.1]:443", false, true},
		{"tcp6", "[::ffff:8.8.8.8]:443", false, false},
		{"tcp6", "[64:ff9b::a9fe:a9fe]:443", false, true},
		{"tcp6", "[64:ff9b::a9fe:a9fe]:443", true, true},
		{"tcp4", "169.254.169.254:80", true, true},
		{"tcp6", "[fe80::1%eth0]:443", false, true},
		// What is not an address and a port is refused, not passed.
		{"tcp", "example.com:443", false, true},
		{"tcp", "8.8.8.8", false, true},
		{"tcp", "", false, true},
		{"tcp", ":443", false, true},
		// Only stream sockets are dialled.
		{"udp", "8.8.8.8:443", false, true},
		{"ip4:1", "8.8.8.8:0", false, true},
		{"unix", "/var/run/docker.sock", false, true},
	}
	for _, c := range cases {
		err := judge(c.allowLoopback)(c.network, c.address, nil)
		if denied := errors.Is(err, ErrDestinationDenied); denied != c.wantDenied || (err != nil) != c.wantDenied {
			t.Errorf("judge(%q, %q, loopback=%v) = %v, want denied=%v", c.network, c.address, c.allowLoopback, err, c.wantDenied)
		}
	}
}

func TestAnAttemptGetsAShareOfTheDeadline(t *testing.T) {
	t.Run("with a deadline the share is the time left over the attempts left", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		attempt, done := shareOfDeadline(ctx, 2)
		defer done()
		dl, ok := attempt.Deadline()
		if !ok {
			t.Fatal("the attempt has no deadline")
		}
		if left := time.Until(dl); left > 5*time.Second || left < 4*time.Second {
			t.Errorf("the attempt has %v, want about 5s", left)
		}
	})
	t.Run("the last attempt gets everything that is left", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		attempt, done := shareOfDeadline(ctx, 1)
		defer done()
		dl, _ := attempt.Deadline()
		if left := time.Until(dl); left < 9*time.Second {
			t.Errorf("the attempt has %v, want about 10s", left)
		}
	})
	t.Run("without a deadline there is none to share", func(t *testing.T) {
		attempt, done := shareOfDeadline(context.Background(), 3)
		defer done()
		if _, ok := attempt.Deadline(); ok {
			t.Error("the attempt has a deadline the caller did not give")
		}
	})
}

func TestACallStopsAtItsContextDeadline(t *testing.T) {
	release := make(chan struct{})
	srv, pool := tlsServer(t, http.HandlerFunc(func(http.ResponseWriter, *http.Request) { <-release }))
	t.Cleanup(func() { close(release) })
	client := New(Options{AllowLoopback: true, RootCAs: pool})

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL, nil)
	if err != nil {
		t.Fatal(err)
	}
	_, err = client.Do(req)

	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("err = %v, want context.DeadlineExceeded", err)
	}
}

// TestWrappingTheTransportKeepsEveryRefusal holds the way a caller adds the
// trace headers: it wraps the client's Transport in observability.Transport
// from outside, and the egress client is given no entry point of its own for
// it. The judgement sits in the dialer and the scheme and body checks sit in
// the transport the wrapper delegates to, so none of them may be lost to the
// extra layer. A wrapper that answered a request itself, or that rebuilt the
// transport, would let a caller reach an address the client refuses.
func TestWrappingTheTransportKeepsEveryRefusal(t *testing.T) {
	wrapped := func(o Options) *http.Client {
		client := build(o, hooks{})
		client.Transport = observability.Transport(client.Transport)
		return client
	}

	t.Run("a loopback address is still refused", func(t *testing.T) {
		var hits counter
		srv, pool := tlsServer(t, hits.handler(ok))

		_, err := get(wrapped(Options{RootCAs: pool}), srv.URL)

		if !errors.Is(err, ErrDestinationDenied) {
			t.Fatalf("err = %v, want ErrDestinationDenied", err)
		}
		if n := hits.n.Load(); n != 0 {
			t.Errorf("the server saw %d requests, want 0", n)
		}
	})

	t.Run("a scheme other than https is still refused", func(t *testing.T) {
		var hits counter
		plain := httptest.NewServer(hits.handler(ok))
		t.Cleanup(plain.Close)

		_, err := get(wrapped(Options{AllowLoopback: true}), plain.URL)

		if !errors.Is(err, ErrSchemeNotHTTPS) {
			t.Fatalf("err = %v, want ErrSchemeNotHTTPS", err)
		}
		if n := hits.n.Load(); n != 0 {
			t.Errorf("the server saw %d requests, want 0", n)
		}
	})

	t.Run("a body over the cap is still refused", func(t *testing.T) {
		srv, pool := tlsServer(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.(http.Flusher).Flush()
			_, _ = w.Write(bytes.Repeat([]byte{'a'}, MaxBodyBytes+1))
		}))

		resp, err := get(wrapped(Options{AllowLoopback: true, RootCAs: pool}), srv.URL)

		if !errors.Is(err, ErrBodyTooLarge) {
			t.Fatalf("err = %v, want ErrBodyTooLarge", err)
		}
		if resp != nil {
			if n, _ := io.Copy(io.Discard, resp.Body); n > MaxBodyBytes {
				t.Errorf("read %d bytes, want at most the cap", n)
			}
		}
	})

	t.Run("a permitted request still goes through", func(t *testing.T) {
		var hits counter
		srv, pool := tlsServer(t, hits.handler(ok))

		resp, err := get(wrapped(Options{AllowLoopback: true, RootCAs: pool}), srv.URL)
		if err != nil {
			t.Fatalf("err = %v, want the request to reach the server", err)
		}
		if resp.StatusCode != http.StatusOK || hits.n.Load() != 1 {
			t.Errorf("status %d after %d hit(s), want 200 after 1", resp.StatusCode, hits.n.Load())
		}
	})
}
