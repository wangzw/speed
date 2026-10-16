//go:build integration

package memcached

import (
	"bufio"
	"context"
	"io"
	"net"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/vislake/speed/pkg/kv/internal/memcachetest"
)

// The cases in this file drive one interleaving of two writers claiming a key
// that has expired but is still held by the server, rather than racing them
// and hoping for it.
//
// This implementation decides expiry itself, so to the server an expired key is
// an entry like any other, and every write that replaces one has to be
// conditional on the entry it read: two writers that both read the expired
// entry must not both be told they wrote. The interleaving that separates a
// conditional replacement from anything weaker is the one where both writers
// read the entry, one of them then carries out its whole write, and only after
// that does the other write. A proxy in front of the second writer's client
// holds its first command after a read until the first writer has returned, so
// that interleaving happens on every run.

// holdingProxy forwards a client's connections to a server and, once armed,
// holds the first command a client sends after its first read until released.
type holdingProxy struct {
	listener net.Listener
	upstream string

	armed    atomic.Bool
	read     atomic.Bool
	holdOnce sync.Once
	held     chan struct{}
	release  chan struct{}

	mu    sync.Mutex
	conns []net.Conn
	wg    sync.WaitGroup
}

func startHoldingProxy(t *testing.T, upstream string) *holdingProxy {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listening: %v", err)
	}
	p := &holdingProxy{
		listener: listener,
		upstream: upstream,
		held:     make(chan struct{}),
		release:  make(chan struct{}),
	}
	p.wg.Add(1)
	go p.serve()
	t.Cleanup(p.stop)
	return p
}

func (p *holdingProxy) addr() string { return p.listener.Addr().String() }

// arm makes the proxy hold what follows the next read.
func (p *holdingProxy) arm() { p.armed.Store(true) }

// awaitHeld waits until a command is being held.
func (p *holdingProxy) awaitHeld(t *testing.T) {
	t.Helper()
	select {
	case <-p.held:
	case <-time.After(10 * time.Second):
		t.Fatal("the second writer never wrote after its read, so the interleaving this case is about " +
			"did not happen")
	}
}

// letGo releases the held command and everything after it.
func (p *holdingProxy) letGo() { close(p.release) }

func (p *holdingProxy) stop() {
	_ = p.listener.Close()
	p.mu.Lock()
	for _, conn := range p.conns {
		_ = conn.Close()
	}
	p.mu.Unlock()
	select {
	case <-p.release:
	default:
		close(p.release)
	}
	p.wg.Wait()
}

func (p *holdingProxy) track(conn net.Conn) {
	p.mu.Lock()
	p.conns = append(p.conns, conn)
	p.mu.Unlock()
}

func (p *holdingProxy) serve() {
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
			_, _ = io.Copy(client, server)
			_ = client.Close()
		}()
		go func() {
			defer p.wg.Done()
			p.forward(client, server)
			_ = server.Close()
		}()
	}
}

// forward copies one connection's commands to the server, a command and its
// data block at a time, holding the first command after the first read once
// the proxy is armed.
func (p *holdingProxy) forward(client, server net.Conn) {
	r := bufio.NewReader(client)
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			return
		}
		fields := strings.Fields(line)
		if len(fields) == 0 {
			continue
		}
		if p.armed.Load() && p.read.Load() {
			p.holdOnce.Do(func() { close(p.held) })
			<-p.release
		}
		if _, err := io.WriteString(server, line); err != nil {
			return
		}
		switch fields[0] {
		case "get", "gets":
			if p.armed.Load() {
				p.read.Store(true)
			}
		case "set", "add", "replace", "append", "prepend", "cas":
			// The data block follows the command line: its length is the
			// fifth field, then the terminating CRLF.
			if len(fields) < 5 {
				return
			}
			size, convErr := strconv.Atoi(fields[4])
			if convErr != nil {
				return
			}
			if _, copyErr := io.CopyN(server, r, int64(size+2)); copyErr != nil {
				return
			}
		}
	}
}

// interleave writes key in space with a value that has already expired, then
// runs second's claim up to the write that follows its read, runs first's claim
// to completion, and lets second's claim finish. It reports what each claim
// returned, first's before second's.
func interleave[T any](t *testing.T, space, key string, claim func(s *store, mine string) (T, error)) (T, T) {
	t.Helper()
	addr := memcachetest.Addr(t)
	first := openOn(t, addr, space)
	proxy := startHoldingProxy(t, addr)
	second := openOn(t, proxy.addr(), space)

	ctx := context.Background()
	if err := first.Set(ctx, key, []byte("1"), time.Millisecond); err != nil {
		t.Fatalf("Set: %v", err)
	}
	time.Sleep(5 * time.Millisecond)

	type outcome struct {
		got T
		err error
	}
	secondDone := make(chan outcome, 1)
	proxy.arm()
	go func() {
		got, err := claim(second, "second")
		secondDone <- outcome{got, err}
	}()
	proxy.awaitHeld(t)
	firstGot, err := claim(first, "first")
	proxy.letGo()
	if err != nil {
		t.Fatalf("the first writer's claim: %v", err)
	}
	secondOutcome := <-secondDone
	if secondOutcome.err != nil {
		t.Fatalf("the second writer's claim: %v", secondOutcome.err)
	}
	return firstGot, secondOutcome.got
}

// TestAnExpiredKeyHasOneClaimantWhenTheClaimsInterleave holds the two
// conditional writes to one winner over an expired entry, whichever of them
// the claim is: the second writer read the same expired entry as the first,
// and the first wrote in between, so the second's write has to be refused and
// judged again against what the first left — a live value, which it does not
// get to replace.
func TestAnExpiredKeyHasOneClaimantWhenTheClaimsInterleave(t *testing.T) {
	for name, claim := range map[string]func(s *store, key, mine string) (bool, error){
		"CompareAndSwap": func(s *store, key, mine string) (bool, error) {
			return s.CompareAndSwap(context.Background(), key, nil, []byte(mine))
		},
		"SetIfAbsent": func(s *store, key, mine string) (bool, error) {
			return s.SetIfAbsent(context.Background(), key, []byte(mine), time.Hour)
		},
	} {
		t.Run(name, func(t *testing.T) {
			space, key := memcachetest.Space(t), "claimed"
			firstWon, secondWon := interleave(t, space, key, func(s *store, mine string) (bool, error) {
				return claim(s, key, mine)
			})
			if !firstWon {
				t.Fatal("the first writer, which claimed the expired key with nothing in its way, lost")
			}
			if secondWon {
				t.Fatal("both writers claimed the expired key: the second replaced the value the first " +
					"had just written, although it had read the expired entry that value replaced")
			}
			reader := openOn(t, memcachetest.Addr(t), space)
			if got, present, err := reader.Get(context.Background(), key); err != nil || !present ||
				string(got) != "first" {
				t.Fatalf("the key holds %q (present=%v, err=%v), want the first writer's value", got, present, err)
			}
		})
	}
}

// TestAnExpiredCounterCountsEveryIncrementWhenTheyInterleave is the increment's
// half: both increments read the expired counter, the first creates it again
// at 1, and the second's write has to be refused and counted again from the 1
// the first left rather than from the zero it read.
func TestAnExpiredCounterCountsEveryIncrementWhenTheyInterleave(t *testing.T) {
	space, key := memcachetest.Space(t), "counter"
	firstGot, secondGot := interleave(t, space, key, func(s *store, _ string) (float64, error) {
		return s.IncrByFloatWithTTL(context.Background(), key, 1, time.Hour)
	})
	if firstGot != 1 || secondGot != 2 {
		t.Fatalf("the interleaved increments of an expired counter returned %v and %v, want 1 and 2: an "+
			"increment was lost", firstGot, secondGot)
	}
}
