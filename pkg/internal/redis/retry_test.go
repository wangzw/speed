package redis

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type replyLoss int

const (
	dropTheConnection replyLoss = iota
	neverAnswer
)

type lossyServer struct {
	listener net.Listener
	target   string
	loss     replyLoss
	received atomic.Int32
	wg       sync.WaitGroup
}

func startLossyServer(t *testing.T, target string, loss replyLoss) *lossyServer {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listening: %v", err)
	}
	s := &lossyServer{listener: listener, target: strings.ToUpper(target), loss: loss}
	s.wg.Add(1)
	go s.serve()
	t.Cleanup(func() {
		_ = listener.Close()
		s.wg.Wait()
	})
	return s
}

func (s *lossyServer) addr() string { return s.listener.Addr().String() }

func (s *lossyServer) serve() {
	defer s.wg.Done()
	var conns sync.WaitGroup
	defer conns.Wait()
	for {
		conn, err := s.listener.Accept()
		if err != nil {
			return
		}
		conns.Add(1)
		go func() {
			defer conns.Done()
			s.handle(conn)
		}()
	}
}

func (s *lossyServer) handle(conn net.Conn) {
	defer func() { _ = conn.Close() }()
	r := bufio.NewReader(conn)
	for {
		args, err := readCommand(r)
		if err != nil {
			return
		}
		switch strings.ToUpper(args[0]) {
		case "PING":
			_, _ = io.WriteString(conn, "+PONG\r\n")
		case s.target:
			s.received.Add(1)
			if s.loss == dropTheConnection {
				return
			}
			_, _ = io.Copy(io.Discard, r)
			return
		default:
			_, _ = io.WriteString(conn, "-ERR unknown command\r\n")
		}
	}
}

func readCommand(r *bufio.Reader) ([]string, error) {
	header, err := r.ReadString('\n')
	if err != nil {
		return nil, err
	}
	if !strings.HasPrefix(header, "*") {
		return nil, fmt.Errorf("unexpected line %q", header)
	}
	n, err := strconv.Atoi(strings.TrimSpace(header[1:]))
	if err != nil || n < 1 {
		return nil, fmt.Errorf("unexpected array header %q", header)
	}
	args := make([]string, 0, n)
	for range n {
		lenLine, err := r.ReadString('\n')
		if err != nil {
			return nil, err
		}
		size, err := strconv.Atoi(strings.TrimSpace(strings.TrimPrefix(lenLine, "$")))
		if err != nil {
			return nil, fmt.Errorf("unexpected bulk header %q", lenLine)
		}
		buf := make([]byte, size+2)
		if _, err := io.ReadFull(r, buf); err != nil {
			return nil, err
		}
		args = append(args, string(buf[:size]))
	}
	return args, nil
}

// TestACommandWhoseReplyIsLostIsNotSentAgain pins the rule that the client Connect builds does not
// retry.
func TestACommandWhoseReplyIsLostIsNotSentAgain(t *testing.T) {
	for name, loss := range map[string]replyLoss{
		"the connection drops":    dropTheConnection,
		"the reply does not come": neverAnswer,
	} {
		t.Run(name, func(t *testing.T) {
			server := startLossyServer(t, "INCRBYFLOAT", loss)
			ctx := context.Background()
			client, err := Connect(ctx, Config{
				Addr:        server.addr(),
				DialTimeout: time.Second,
				ReadTimeout: 200 * time.Millisecond,
			})
			if err != nil {
				t.Fatalf("Connect: %v", err)
			}
			t.Cleanup(func() { _ = client.Close() })

			err = client.IncrByFloat(ctx, "counter", 1).Err()
			if err == nil {
				t.Fatal("an increment whose reply was lost reported success")
			}
			if errors.Is(err, context.Canceled) {
				t.Fatalf("the increment failed for a reason this case did not set up: %v", err)
			}
			if got := server.received.Load(); got != 1 {
				t.Fatalf("the server received the increment %d times, want once: the client sent a "+
					"command again after its reply was lost, and the server ran it each time", got)
			}
		})
	}
}
