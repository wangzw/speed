// Package cutproxy fronts a TCP server with a link a test can cut: a proxy that
// carries the connections it accepts, and that closes every one of them and
// refuses new ones until it is restored. A cut link is what an operator's cut
// between a client and its server looks like from the client's side — the
// connections are gone, and nothing answers until the link is back — and the
// key-value and event modules' NATS backends exercise exactly that state: what
// a call issued while the link is down must and must not do.
//
// It stands as a shared unit of its own in pkg/internal/cutproxy, inside the
// pkg module's internal/ tree: it is read by tests only, belongs to no public
// surface, and the compiler holds that, keeping anything outside the module
// from importing it. Its placement follows
// docs/adr/adr-shared-backend-unit-2026-09-21.md: a fixture with no wrapper
// package of its own is a unit of its own under pkg's internal/.
//
// Await belongs to the same kit: a case around a cut watches the client notice
// it, or come back, and a client library announces neither — it answers a
// question about them — so the case asks until the answer changes. Both
// backends' cases watched it the same way.
package cutproxy

import (
	"io"
	"net"
	"sync"
	"testing"
	"time"
)

// Proxy carries TCP links between its listeners and one upstream server.
type Proxy struct {
	listener net.Listener
	upstream string

	mu    sync.Mutex
	cut   bool
	conns []net.Conn
	wg    sync.WaitGroup
}

// Start starts a proxy in front of upstream, an address of the form
// "host:port", and stops it when the test ends.
func Start(t *testing.T, upstream string) *Proxy {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("cutproxy: listening: %v", err)
	}
	p := &Proxy{listener: listener, upstream: upstream}
	p.wg.Add(1)
	go p.serve()
	t.Cleanup(p.stop)
	return p
}

// Addr is the address a client connects to, without a scheme: the caller
// speaks whatever protocol its client takes.
func (p *Proxy) Addr() string { return p.listener.Addr().String() }

// Cut closes every link the proxy carries and refuses new ones until Restore.
func (p *Proxy) Cut() {
	p.mu.Lock()
	p.cut = true
	conns := p.conns
	p.conns = nil
	p.mu.Unlock()
	for _, c := range conns {
		_ = c.Close()
	}
}

// Restore lets new links through again.
func (p *Proxy) Restore() {
	p.mu.Lock()
	p.cut = false
	p.mu.Unlock()
}

// Await polls cond until it holds or bound passes. The state a case watches
// through it has no signal to subscribe to, so the case asks the question until
// the answer changes.
func Await(t *testing.T, bound time.Duration, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(bound)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("waited %s for %s", bound, what)
}

func (p *Proxy) stop() {
	p.mu.Lock()
	p.cut = false
	for _, c := range p.conns {
		_ = c.Close()
	}
	p.conns = nil
	p.mu.Unlock()
	_ = p.listener.Close()
	p.wg.Wait()
}

func (p *Proxy) serve() {
	defer p.wg.Done()
	for {
		client, err := p.listener.Accept()
		if err != nil {
			return
		}
		p.attach(client)
	}
}

// attach pairs a client link with the server and copies between them, unless
// the link is cut, in which case the client is closed at once — a reconnect
// attempt against a cut link reaches nothing.
func (p *Proxy) attach(client net.Conn) {
	p.mu.Lock()
	if p.cut {
		p.mu.Unlock()
		_ = client.Close()
		return
	}
	server, err := net.Dial("tcp", p.upstream)
	if err != nil {
		p.mu.Unlock()
		_ = client.Close()
		return
	}
	p.conns = append(p.conns, client, server)
	p.mu.Unlock()
	p.wg.Add(2)
	go func() {
		defer p.wg.Done()
		_, _ = io.Copy(server, client)
		_ = server.Close()
	}()
	go func() {
		defer p.wg.Done()
		_, _ = io.Copy(client, server)
		_ = client.Close()
	}()
}
