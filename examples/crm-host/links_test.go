package main

import (
	"io"
	"net"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"
)

// A link is the path from a case's host processes to one backend its stack
// reaches over a connection: the database, the key-value store or the bus. A
// case laid out with newLinkedCase routes every such link through a proxy of
// its own in the test process, one per link even where two links reach one
// container, so the case can cut one link while the others carry on, and
// restore it. The harness's own connections — c.sql() and the backends'
// housekeeping — go to the servers directly and are never cut.
//
// A cut closes every connection the link carries, both halves, and closes each
// one the host opens while the link stays cut as soon as it is accepted: the
// host sees its connections end and every new one end at once, as it would
// when the server goes away and its address stops answering. A restore lets
// new connections through again; the ones the cut closed stay closed, and the
// host opens new ones as its clients reconnect.

// link names what a proxy carries.
type link string

const (
	databaseLink link = "the database"
	storeLink    link = "the key-value store"
	busLink      link = "the bus"
)

// proxyDialTimeout bounds the proxy's own dial to the server it forwards to.
const proxyDialTimeout = 5 * time.Second

// faultProxy forwards the connections of one link to its server, and cuts them
// on the case's word.
type faultProxy struct {
	link     link
	target   string
	listener net.Listener

	mu    sync.Mutex
	cut   bool
	conns map[net.Conn]struct{}
	// wg counts the proxy's goroutines, so its cleanup returns once none is
	// left.
	wg sync.WaitGroup
}

// startProxy listens on a loopback port for one link and forwards what arrives
// to target, until the test ends.
func startProxy(t *testing.T, l link, target string) *faultProxy {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listening for the proxy of %s: %v", l, err)
	}
	p := &faultProxy{link: l, target: target, listener: listener, conns: map[net.Conn]struct{}{}}
	p.wg.Add(1)
	go p.accept()
	t.Cleanup(p.close)
	return p
}

// addr is where the host is pointed instead of the server.
func (p *faultProxy) addr() string { return p.listener.Addr().String() }

func (p *faultProxy) accept() {
	defer p.wg.Done()
	for {
		conn, err := p.listener.Accept()
		if err != nil {
			return
		}
		p.wg.Add(1)
		go p.forward(conn)
	}
}

// forward carries one connection: the host's side and a connection of its own
// to the server, copied both ways until either side ends, and then both closed.
func (p *faultProxy) forward(host net.Conn) {
	defer p.wg.Done()
	if !p.track(host) {
		_ = host.Close()
		return
	}
	server, err := net.DialTimeout("tcp", p.target, proxyDialTimeout)
	if err != nil {
		p.drop(host)
		return
	}
	if !p.track(server) {
		_ = server.Close()
		p.drop(host)
		return
	}
	done := make(chan struct{}, 2)
	pipe := func(to, from net.Conn) {
		_, _ = io.Copy(to, from)
		done <- struct{}{}
	}
	go pipe(server, host)
	go pipe(host, server)
	<-done
	p.drop(host)
	p.drop(server)
	<-done
}

// track records a connection the link carries, and refuses it while the link
// is cut.
func (p *faultProxy) track(conn net.Conn) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.cut {
		return false
	}
	p.conns[conn] = struct{}{}
	return true
}

// drop closes a connection and forgets it.
func (p *faultProxy) drop(conn net.Conn) {
	p.mu.Lock()
	delete(p.conns, conn)
	p.mu.Unlock()
	_ = conn.Close()
}

// cutOff closes every connection the link carries and every one opened while
// it stays cut.
func (p *faultProxy) cutOff() {
	p.mu.Lock()
	p.cut = true
	conns := p.conns
	p.conns = map[net.Conn]struct{}{}
	p.mu.Unlock()
	for conn := range conns {
		_ = conn.Close()
	}
}

// restore lets new connections through again.
func (p *faultProxy) restore() {
	p.mu.Lock()
	p.cut = false
	p.mu.Unlock()
}

// carried reports how many connections the link carries now.
func (p *faultProxy) carried() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.conns)
}

// close ends the proxy: its listener and every connection it carries.
func (p *faultProxy) close() {
	_ = p.listener.Close()
	p.cutOff()
	p.wg.Wait()
}

// newLinkedCase lays out a case whose host processes reach each backend over a
// link the case can cut.
func newLinkedCase(t *testing.T, s stack) *crmCase {
	t.Helper()
	return layOutCase(t, s, true)
}

// through is the locator a host process is given for a server the harness
// reaches at locator: the same locator on a case laid out with newCase, and
// one naming the link's proxy on a case laid out with newLinkedCase. The
// locator is a URL whose host is the server's address, or the address alone.
func (c *crmCase) through(l link, locator string) string {
	c.t.Helper()
	if !c.linked {
		return locator
	}
	target, rewrite := locator, func(addr string) string { return addr }
	if u, err := url.Parse(locator); err == nil && u.Scheme != "" && u.Host != "" {
		target = u.Host
		rewrite = func(addr string) string {
			u.Host = addr
			return u.String()
		}
	}
	if _, _, err := net.SplitHostPort(target); err != nil {
		c.t.Fatalf("the locator of %s names no address a proxy can stand in front of: %q", l, locator)
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.links == nil {
		c.links = map[link]*faultProxy{}
	}
	p, ok := c.links[l]
	switch {
	case !ok:
		p = startProxy(c.root, l, target)
		c.links[l] = p
	case p.target != target:
		c.t.Fatalf("%s is carried to %s, and a module of the stack reaches it at %s", l, p.target, target)
	}
	return rewrite(p.addr())
}

// proxyOf is the proxy of one link, failing the case when the stack reaches
// nothing over it.
func (c *crmCase) proxyOf(l link) *faultProxy {
	c.t.Helper()
	c.mu.Lock()
	p := c.links[l]
	c.mu.Unlock()
	if p == nil {
		c.t.Fatalf("stack %s reaches nothing over a link to %s that this case can cut", c.stack.name, l)
	}
	return p
}

// cut cuts one link.
func (c *crmCase) cut(l link) {
	c.t.Helper()
	c.proxyOf(l).cutOff()
}

// restore restores one link.
func (c *crmCase) restore(l link) {
	c.t.Helper()
	c.proxyOf(l).restore()
}

// forEachLinkedStack runs a case on every stack caseStacks selects for which
// reaches says the stack reaches a backend over a link, each as a parallel
// subtest with a case laid out by newLinkedCase.
func forEachLinkedStack(t *testing.T, reaches func(stack) bool, body func(t *testing.T, c *crmCase)) {
	t.Helper()
	ran := false
	for _, s := range caseStacks() {
		if !reaches(s) {
			continue
		}
		ran = true
		t.Run(s.name, func(t *testing.T) {
			t.Parallel()
			body(t, newLinkedCase(t, s))
		})
	}
	if !ran {
		t.Fatal("no stack reaches the backend this case cuts, so it checks nothing")
	}
}

// The stacks each kind of link is cut on: the ones whose backend is reached
// over a connection rather than held in the process.
func reachesStore(s stack) bool    { return !backends[s.kv].processLocal }
func reachesBus(s stack) bool      { return !backends[s.event].processLocal }
func reachesDatabase(s stack) bool { return !backends[s.engine].processLocal }

// requireCarried fails the case unless the link carries a connection, so a cut
// is known to reach what the case means it to rather than a link the stack's
// module never opened.
func (c *crmCase) requireCarried(l link) {
	c.t.Helper()
	if !eventually(settleLimit, func() bool { return c.proxyOf(l).carried() > 0 }) {
		c.t.Fatalf("the link to %s carried no connection within %s", l, settleLimit)
	}
}

// lines counts the lines of a run's output that start with a prefix.
func lines(out, prefix string) int {
	n := 0
	for line := range strings.SplitSeq(out, "\n") {
		if strings.HasPrefix(line, prefix) {
			n++
		}
	}
	return n
}
