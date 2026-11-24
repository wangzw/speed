//go:build integration

package nats

import (
	"io"
	"net"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type stallingProxy struct {
	listener net.Listener
	upstream string
	stalled  atomic.Bool

	mu    sync.Mutex
	conns []net.Conn
	wg    sync.WaitGroup
}

func startStallingProxy(t *testing.T, upstream string) *stallingProxy {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listening: %v", err)
	}
	p := &stallingProxy{listener: listener, upstream: upstream}
	p.wg.Add(1)
	go p.serve()
	t.Cleanup(p.stop)
	return p
}

func (p *stallingProxy) url() string { return "nats://" + p.listener.Addr().String() }

func (p *stallingProxy) stop() {
	p.stalled.Store(false)
	_ = p.listener.Close()
	p.mu.Lock()
	for _, c := range p.conns {
		_ = c.Close()
	}
	p.mu.Unlock()
	p.wg.Wait()
}

func (p *stallingProxy) track(c net.Conn) {
	p.mu.Lock()
	p.conns = append(p.conns, c)
	p.mu.Unlock()
}

func (p *stallingProxy) serve() {
	defer p.wg.Done()
	for {
		client, err := p.listener.Accept()
		if err != nil {
			return
		}
		server, err := net.Dial("tcp", p.upstream)
		if err != nil {
			_ = client.Close()
			continue
		}
		p.track(client)
		p.track(server)
		p.wg.Add(2)
		go func() {
			defer p.wg.Done()
			_, _ = io.Copy(server, client)
		}()
		go func() {
			defer p.wg.Done()
			buf := make([]byte, 32*1024)
			for {
				n, readErr := server.Read(buf)
				for p.stalled.Load() {
					time.Sleep(10 * time.Millisecond)
				}
				if n > 0 {
					if _, writeErr := client.Write(buf[:n]); writeErr != nil {
						return
					}
				}
				if readErr != nil {
					return
				}
			}
		}()
	}
}

func hostPort(url string) string { return strings.TrimPrefix(url, "nats://") }
