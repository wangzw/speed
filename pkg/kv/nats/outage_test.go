package nats

import (
	"context"
	"io"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/vislake/speed/pkg/internal/natstest"
)

// cutProxy is a TCP proxy in front of the NATS server that can cut every link
// it carries and refuse new ones. A cut link is what an operator's cut between
// the store and its server looks like from the client's side: the connections
// are gone, and nothing answers until the link is restored.
type cutProxy struct {
	listener net.Listener
	upstream string

	mu    sync.Mutex
	cut   bool
	conns []net.Conn
	wg    sync.WaitGroup
}

func startCutProxy(t *testing.T, upstream string) *cutProxy {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listening: %v", err)
	}
	p := &cutProxy{listener: listener, upstream: upstream}
	p.wg.Add(1)
	go p.serve()
	t.Cleanup(p.stop)
	return p
}

// url is the proxy's address in the form the client takes.
func (p *cutProxy) url() string { return "nats://" + p.listener.Addr().String() }

func (p *cutProxy) stop() {
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

func (p *cutProxy) serve() {
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
func (p *cutProxy) attach(client net.Conn) {
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

// cutAll breaks every link and refuses new ones until restore.
func (p *cutProxy) cutAll() {
	p.mu.Lock()
	p.cut = true
	conns := p.conns
	p.conns = nil
	p.mu.Unlock()
	for _, c := range conns {
		_ = c.Close()
	}
}

func (p *cutProxy) restore() {
	p.mu.Lock()
	p.cut = false
	p.mu.Unlock()
}

// await polls cond until it holds or bound passes.
func await(t *testing.T, bound time.Duration, what string, cond func() bool) {
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

// TestAWriteTheCutLinkRefusesDoesNotLandLater is the case for what a failed
// write means here: the capability hands the caller its own failure, and the
// caller decides whether to retry by whether the operation is idempotent
// (design-kv). A write kept by the client library and sent once the link is
// back breaks that decision — the caller was told the write failed and can
// retry it, or leave the entry alone, and either choice is wrong if the store
// writes it anyway. For a counter driven by a read-modify-write that is a
// count taken twice.
//
// The case cuts the link, waits until the client knows it is down, writes
// there, restores the link, and then reads the key: it must be absent.
func TestAWriteTheCutLinkRefusesDoesNotLandLater(t *testing.T) {
	proxy := startCutProxy(t, hostPort(natstest.URL(t)))
	opened, err := open(context.Background(), &Config{
		URL: proxy.url(), Bucket: bucketName(), Timeout: 10 * time.Second,
	})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	s := opened.(*store)
	t.Cleanup(func() { _ = s.Close(context.Background()) })

	const key = "cut-key"
	// Warm up through the proxy: the bucket is there, and the case has a
	// working link to cut.
	if warmErr := s.Set(context.Background(), "warmup", []byte("v"), 0); warmErr != nil {
		t.Fatalf("the warm-up write: %v", warmErr)
	}

	// The write below has to be issued while the client knows the link is
	// down: that is the state in which the client library would otherwise hold
	// the write in its reconnect buffer and send it once the server answered
	// again.
	proxy.cutAll()
	await(t, 30*time.Second, "the client to notice the cut link", s.nc.IsReconnecting)

	// The deadline is shorter than the library's wait between reconnect
	// attempts (two seconds), so the write is decided while the link is still
	// cut.
	refused, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if writeErr := s.Set(refused, key, []byte("landed"), 0); writeErr == nil {
		t.Fatalf("a write issued while the link was cut succeeded")
	}

	proxy.restore()
	await(t, 30*time.Second, "the connection to come back", s.nc.IsConnected)
	// Give a client that kept the write the moment it needs to send what it
	// kept, so a kept write cannot pass this case by landing late.
	time.Sleep(time.Second)
	value, present, err := s.Get(context.Background(), key)
	if err != nil {
		t.Fatalf("reading back the refused key: %v", err)
	}
	if present {
		t.Fatalf("the key holds %q after a write this store refused: the refused write was kept and sent "+
			"once the link was back", value)
	}
}
