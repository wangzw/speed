package egress

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"syscall"
	"time"
)

// MaxBodyBytes is the most a response body may carry: 1 MiB.
const MaxBodyBytes = 1 << 20

// maxHeaderBytes is the most a response's header block may carry.
const maxHeaderBytes = 64 << 10

var (
	// ErrDestinationDenied is the failure of a connection attempt to an address
	// that is not public unicast. It is the same failure for every route to
	// that address: a literal, a name resolving to it, or one record among
	// several.
	ErrDestinationDenied = errors.New("egress: destination address is not permitted")

	// ErrSchemeNotHTTPS is the failure of a request whose URL scheme is not
	// https. No connection has been opened when it is returned.
	ErrSchemeNotHTTPS = errors.New("egress: only https requests are permitted")

	// ErrBodyTooLarge is the read error of a response body that carries more
	// than [MaxBodyBytes].
	ErrBodyTooLarge = errors.New("egress: response body exceeds the size limit")
)

// Options configures [New]. The zero value is the production client.
type Options struct {
	// AllowLoopback admits connections to loopback addresses, for development
	// against an identity provider running on the same machine. Every other
	// non-public address stays refused.
	AllowLoopback bool

	// RootCAs replaces the system roots when the peer's certificate chains to a
	// root the system does not hold: a deployment whose identity provider uses a
	// private certificate authority, or a test serving its own certificate. Nil
	// means the system roots. [RootCAsFromFile] reads a PEM file into one.
	// Verification itself is never switched off.
	RootCAs *x509.CertPool
}

// New returns the restricted client; see the package documentation for what it
// refuses. The client carries no total timeout, so each call takes its deadline
// from its context.
func New(o Options) *http.Client {
	return build(o, hooks{})
}

// hooks are the points a test replaces. Production leaves them zero.
type hooks struct {
	// lookup resolves a host name; nil means the system resolver.
	lookup func(ctx context.Context, host string) ([]netip.Addr, error)

	// control wraps the connection-time judgement; nil leaves it as it is.
	control func(next controlFunc) controlFunc
}

type controlFunc = func(network, address string, c syscall.RawConn) error

func build(o Options, h hooks) *http.Client {
	lookup := h.lookup
	if lookup == nil {
		lookup = func(ctx context.Context, host string) ([]netip.Addr, error) {
			return net.DefaultResolver.LookupNetIP(ctx, "ip", host)
		}
	}
	control := controlFunc(judge(o.AllowLoopback))
	if h.control != nil {
		control = h.control(control)
	}
	dialer := &net.Dialer{Control: control}

	transport := &http.Transport{
		// A nil Proxy means a direct connection, whatever the environment
		// says: through a proxy the judgement would see the proxy.
		Proxy:                  nil,
		DialContext:            dialAll(dialer, lookup),
		ForceAttemptHTTP2:      true,
		TLSClientConfig:        &tls.Config{MinVersion: tls.VersionTLS12, RootCAs: o.RootCAs},
		TLSHandshakeTimeout:    10 * time.Second,
		ExpectContinueTimeout:  time.Second,
		MaxResponseHeaderBytes: maxHeaderBytes,
		MaxIdleConns:           32,
		MaxIdleConnsPerHost:    2,
		IdleConnTimeout:        90 * time.Second,
	}

	return &http.Client{
		Transport: &guarded{next: transport},
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
}

// judge is the connection-time check. The address it receives is the one the
// socket is about to connect to, so it cannot be told one thing and connect to
// another. An address that does not parse is refused.
func judge(allowLoopback bool) controlFunc {
	return func(network, address string, _ syscall.RawConn) error {
		switch network {
		case "tcp", "tcp4", "tcp6":
		default:
			return fmt.Errorf("%w: network %q", ErrDestinationDenied, network)
		}
		ap, err := netip.ParseAddrPort(address)
		if err != nil || !Permitted(ap.Addr(), allowLoopback) {
			return ErrDestinationDenied
		}
		return nil
	}
}

// dialAll resolves the host itself and connects to its addresses one after the
// other, so that each attempt is a separate connection through the dialer and
// so through the judgement. Resolving here also keeps the host in whatever
// notation it was written — 0x7f.0.0.1, 2130706433 — out of any parsing of
// ours: only the address that comes out is judged.
//
// The context net/http gives a dial is detached from the call that asked for
// it: it has neither the call's deadline nor its cancellation. [guarded] carries
// the call's context across as a value, and the dial ends with the call.
func dialAll(dialer *net.Dialer, lookup func(context.Context, string) ([]netip.Addr, error)) func(context.Context, string, string) (net.Conn, error) {
	return func(ctx context.Context, network, address string) (net.Conn, error) {
		ctx, release := endWithCall(ctx)
		defer release()
		host, port, err := net.SplitHostPort(address)
		if err != nil {
			return nil, err
		}
		addrs, err := resolve(ctx, lookup, host)
		if err != nil {
			return nil, err
		}

		var errs []error
		for i, addr := range addrs {
			if err := ctx.Err(); err != nil {
				errs = append(errs, err)
				break
			}
			attempt, cancel := shareOfDeadline(ctx, len(addrs)-i)
			conn, err := dialer.DialContext(attempt, network, net.JoinHostPort(addr.String(), port))
			cancel()
			if err == nil {
				return conn, nil
			}
			errs = append(errs, err)
		}
		return nil, errors.Join(errs...)
	}
}

// callKey is the context key under which [guarded] carries the context of the
// call to the dial.
type callKey struct{}

// endWithCall returns ctx bounded by the call it was started for: it ends when
// the call is cancelled and carries the call's deadline. A context that carries
// no call is returned as it is. The release func is called when the dial is
// over.
func endWithCall(ctx context.Context) (context.Context, context.CancelFunc) {
	call, ok := ctx.Value(callKey{}).(context.Context)
	if !ok {
		return ctx, func() {}
	}
	ctx, cancel := context.WithCancel(ctx)
	stop := context.AfterFunc(call, cancel)
	release := func() {
		stop()
		cancel()
	}
	if deadline, ok := call.Deadline(); ok {
		var cancelDeadline context.CancelFunc
		ctx, cancelDeadline = context.WithDeadline(ctx, deadline)
		return ctx, func() {
			cancelDeadline()
			release()
		}
	}
	return ctx, release
}

func resolve(ctx context.Context, lookup func(context.Context, string) ([]netip.Addr, error), host string) ([]netip.Addr, error) {
	if addr, err := netip.ParseAddr(host); err == nil {
		return []netip.Addr{addr}, nil
	}
	addrs, err := lookup(ctx, host)
	if err != nil {
		return nil, err
	}
	if len(addrs) == 0 {
		return nil, fmt.Errorf("egress: no addresses for host %q", host)
	}
	return addrs, nil
}

// shareOfDeadline bounds one attempt to an equal share of what is left of the
// context's deadline, so that an unreachable first address cannot spend the
// time the later ones need. A context without a deadline is passed on as it is.
func shareOfDeadline(ctx context.Context, remaining int) (context.Context, context.CancelFunc) {
	deadline, ok := ctx.Deadline()
	if !ok {
		return ctx, func() {}
	}
	return context.WithTimeout(ctx, time.Until(deadline)/time.Duration(remaining))
}

// guarded enforces the scheme and the body cap around the transport and carries
// the call's context to the dial.
type guarded struct {
	next http.RoundTripper
}

func (g *guarded) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.URL == nil || req.URL.Scheme != "https" {
		if req.Body != nil {
			_ = req.Body.Close()
		}
		return nil, ErrSchemeNotHTTPS
	}
	// The transport hands the dial a context detached from the request's.
	req = req.WithContext(context.WithValue(req.Context(), callKey{}, req.Context()))
	resp, err := g.next.RoundTrip(req)
	if err != nil {
		return nil, err
	}
	resp.Body = &cappedBody{body: resp.Body, left: MaxBodyBytes}
	return resp, nil
}

// CloseIdleConnections closes the idle connections of the transport behind the
// guard, which [http.Client.CloseIdleConnections] finds through this method.
func (g *guarded) CloseIdleConnections() {
	if c, ok := g.next.(interface{ CloseIdleConnections() }); ok {
		c.CloseIdleConnections()
	}
}

// cappedBody fails the read that would deliver byte MaxBodyBytes+1. The bytes
// up to the cap are delivered first, so a reader sees the error only after the
// permitted part and never a silently shortened body.
type cappedBody struct {
	body io.ReadCloser
	left int64
	err  error
}

func (b *cappedBody) Read(p []byte) (int, error) {
	if b.err != nil {
		return 0, b.err
	}
	// One byte beyond what is left is read, to tell a body that ends at the
	// cap from one that goes past it.
	if int64(len(p)) > b.left+1 {
		p = p[:b.left+1]
	}
	n, err := b.body.Read(p)
	if int64(n) > b.left {
		n = int(b.left)
		b.left = 0
		b.err = ErrBodyTooLarge
		return n, b.err
	}
	b.left -= int64(n)
	return n, err
}

func (b *cappedBody) Close() error {
	return b.body.Close()
}
