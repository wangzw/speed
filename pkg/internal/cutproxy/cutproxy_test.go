package cutproxy

import (
	"errors"
	"io"
	"net"
	"os"
	"testing"
	"time"
)

// TestACutClosesTheLinkItCarriesAndRefusesTheNext checks that Cut closes the carried link and
// refuses the next, and Restore lets a new link through.
func TestACutClosesTheLinkItCarriesAndRefusesTheNext(t *testing.T) {
	upstream, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listening upstream: %v", err)
	}
	t.Cleanup(func() { _ = upstream.Close() })
	go echo(upstream)

	p := Start(t, upstream.Addr().String())

	carried := dialLink(t, p.Addr())
	roundTrip(t, carried, "before")

	p.Cut()
	refused(t, carried, "the link the cut was carrying")
	next := dialLink(t, p.Addr())
	refused(t, next, "a link dialled while the cut held")

	p.Restore()
	after := dialLink(t, p.Addr())
	roundTrip(t, after, "after")
}

func echo(listener net.Listener) {
	for {
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		go func() {
			defer func() { _ = conn.Close() }()
			_, _ = io.Copy(conn, conn)
		}()
	}
}

func dialLink(t *testing.T, addr string) net.Conn {
	t.Helper()
	conn, err := net.DialTimeout("tcp", addr, 5*time.Second)
	if err != nil {
		t.Fatalf("dialing %s: %v", addr, err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return conn
}

func roundTrip(t *testing.T, conn net.Conn, message string) {
	t.Helper()
	if err := conn.SetDeadline(time.Now().Add(10 * time.Second)); err != nil {
		t.Fatalf("setting the deadline: %v", err)
	}
	defer func() { _ = conn.SetDeadline(time.Time{}) }()
	if _, err := conn.Write([]byte(message)); err != nil {
		t.Fatalf("writing %q through the link: %v", message, err)
	}
	got := make([]byte, len(message))
	if _, err := io.ReadFull(conn, got); err != nil {
		t.Fatalf("reading %q back through the link: %v", message, err)
	}
	if string(got) != message {
		t.Fatalf("the link carried %q, want %q", got, message)
	}
}

func refused(t *testing.T, conn net.Conn, what string) {
	t.Helper()
	if err := conn.SetReadDeadline(time.Now().Add(10 * time.Second)); err != nil {
		t.Fatalf("setting the read deadline: %v", err)
	}
	var one [1]byte
	switch _, err := conn.Read(one[:]); {
	case err == nil:
		t.Fatalf("%s: the link is still open", what)
	case errors.Is(err, os.ErrDeadlineExceeded):
		t.Fatalf("%s: nothing closed the link; the read sat until its deadline", what)
	}
}
