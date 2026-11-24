package memcached

import (
	"bufio"
	"io"
	"net"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

const replyDelay = 2 * time.Millisecond

type fakeServer struct {
	listener net.Listener
	accepted atomic.Int32
	closed   atomic.Int32
	wg       sync.WaitGroup

	mu       sync.Mutex
	open     map[net.Conn]struct{}
	stopping bool
}

func startFakeServer(t *testing.T) *fakeServer {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listening: %v", err)
	}
	s := &fakeServer{listener: listener, open: make(map[net.Conn]struct{})}
	s.wg.Add(1)
	go s.serve()
	t.Cleanup(s.stop)
	return s
}

func (s *fakeServer) stop() {
	_ = s.listener.Close()
	s.mu.Lock()
	s.stopping = true
	for conn := range s.open {
		_ = conn.Close()
	}
	s.mu.Unlock()
	s.wg.Wait()
}

func (s *fakeServer) addr() string { return s.listener.Addr().String() }

func (s *fakeServer) serve() {
	defer s.wg.Done()
	var conns sync.WaitGroup
	defer conns.Wait()
	for {
		conn, err := s.listener.Accept()
		if err != nil {
			return
		}
		s.accepted.Add(1)
		s.mu.Lock()
		if s.stopping {
			s.mu.Unlock()
			_ = conn.Close()
			continue
		}
		s.open[conn] = struct{}{}
		s.mu.Unlock()
		conns.Add(1)
		go func() {
			defer conns.Done()
			s.handle(conn)
		}()
	}
}

func (s *fakeServer) handle(conn net.Conn) {
	defer func() {
		s.mu.Lock()
		delete(s.open, conn)
		s.mu.Unlock()
		_ = conn.Close()
	}()
	r := bufio.NewReader(conn)
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			s.mu.Lock()
			if !s.stopping {
				s.closed.Add(1)
			}
			s.mu.Unlock()
			return
		}
		fields := strings.Fields(line)
		if len(fields) == 0 {
			continue
		}
		time.Sleep(replyDelay)
		switch fields[0] {
		case "version":
			_, _ = io.WriteString(conn, "VERSION 1.6.0\r\n")
		case "get", "gets":
			_, _ = io.WriteString(conn, "END\r\n")
		case "set", "add", "cas":
			if len(fields) < 5 {
				return
			}
			size, convErr := strconv.Atoi(fields[4])
			if convErr != nil {
				return
			}
			if _, discardErr := r.Discard(size + 2); discardErr != nil {
				return
			}
			_, _ = io.WriteString(conn, "STORED\r\n")
		case "delete":
			_, _ = io.WriteString(conn, "NOT_FOUND\r\n")
		default:
			_, _ = io.WriteString(conn, "ERROR\r\n")
		}
	}
}
