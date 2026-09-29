package nats

import (
	"context"
	"io"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"

	"github.com/vislake/speed/pkg/event"
	"github.com/vislake/speed/pkg/internal/natstest"
)

// cutProxy is a TCP proxy in front of the NATS server that can cut every link
// it carries and refuse new ones. A cut link is what an operator's cut between
// the bus and its server looks like from the client's side: the connections
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

// cut breaks every link and refuses new ones until restore.
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

// TestAPublishTheCutLinkRefusesIsNotStoredLater is the case for what a failed
// publish means here, the lower bound design-event states: a publish that
// returns an error is one the bus did not accept. The publisher of an event
// the bus refused is free to publish it again under a new identifier, or not
// at all; either choice is wrong if the bus kept the refused event and sent it
// once its link was back, because the consumer then counts an event the
// publisher was told had not happened.
//
// The case cuts the link, waits until the client knows it is down, publishes
// there, and restores the link. The stream is then read through a connection
// of its own: nothing of that publish may be in it.
func TestAPublishTheCutLinkRefusesIsNotStoredLater(t *testing.T) {
	server := natstest.URL(t)
	proxy := startCutProxy(t, strings.TrimPrefix(server, "nats://"))
	bus := openBus(t, proxy.url(), "")
	eventType := nextType()
	delivered := make(chan event.Event, 8)
	bus.Subscribe(eventType, func(_ context.Context, evt event.Event) error {
		delivered <- evt
		return nil
	})
	if err := bus.begin(context.Background()); err != nil {
		t.Fatalf("begin: %v", err)
	}

	// Warm up through the proxy: the stream is there, this bus's reader is
	// live, and the case has a count to compare against.
	if err := bus.Publish(context.Background(), event.Event{Type: eventType, ID: "warmup"}); err != nil {
		t.Fatalf("the warm-up publish: %v", err)
	}
	select {
	case <-delivered:
	case <-time.After(30 * time.Second):
		t.Fatalf("the warm-up event was not delivered")
	}

	// The stream is read through a connection of its own, so its count stays
	// observable while the bus's link is cut.
	directConn, err := nats.Connect(server)
	if err != nil {
		t.Fatalf("connecting to the server directly: %v", err)
	}
	t.Cleanup(directConn.Close)
	direct, err := jetstream.New(directConn)
	if err != nil {
		t.Fatalf("opening JetStream directly: %v", err)
	}
	stream, err := direct.Stream(context.Background(), bus.streamName(eventType))
	if err != nil {
		t.Fatalf("binding to the event type's stream: %v", err)
	}
	entries := func() uint64 {
		info, err := stream.Info(context.Background())
		if err != nil {
			t.Fatalf("reading the stream: %v", err)
		}
		return info.State.Msgs
	}
	before := entries()

	// The publish below has to be issued while the client knows the link is
	// down: that is the state in which the client library would otherwise hold
	// the message in its reconnect buffer and send it once the server
	// answered again.
	proxy.cutAll()
	await(t, 30*time.Second, "the client to notice the cut link", bus.nc.IsReconnecting)

	// The deadline is shorter than the library's wait between reconnect
	// attempts (two seconds), so the publish is decided while the link is
	// still cut.
	refused, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := bus.Publish(refused, event.Event{Type: eventType, ID: "refused"}); err == nil {
		t.Fatalf("a publish issued while the link was cut was accepted")
	}

	proxy.restore()
	await(t, 30*time.Second, "the connection to come back", bus.nc.IsConnected)
	// Give a client that kept the publish the moment it needs to send what it
	// kept, so a kept publish cannot pass this case by arriving late.
	time.Sleep(time.Second)
	if after := entries(); after != before {
		t.Fatalf("the stream holds %d entries after a publish this bus refused, want the %d it held: "+
			"the refused event was kept and sent once the link was back", after, before)
	}

	// The link is usable again: the reader takes what is published now, so
	// nothing was left in a state where the connection takes no publish.
	if err := bus.Publish(context.Background(), event.Event{Type: eventType, ID: "after"}); err != nil {
		t.Fatalf("a publish after the link came back: %v", err)
	}
	select {
	case evt := <-delivered:
		if evt.ID != "after" {
			t.Fatalf("the reader delivered %q after the link came back, want the event published then", evt.ID)
		}
	case <-time.After(30 * time.Second):
		t.Fatalf("a publish after the link came back was not delivered")
	}
}
