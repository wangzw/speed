package redistest

import (
	"context"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
)

// TestAnExistingStoppedContainerIsStartedNotRemoved pins what happens to a
// container that is there and not running: it is another test binary's
// container a moment before it comes up, or an interrupted run's leftover, and
// in either case removing it reaches into the other binary's window and takes
// its server with it.
//
// The fake docker below records every call, so the case asserts on what was
// asked of docker rather than on timing.
func TestAnExistingStoppedContainerIsStartedNotRemoved(t *testing.T) {
	// The fixture waits for the published address to answer a Redis PING, so
	// the fake docker publishes the address of a server that answers one.
	addr := startPongServer(t)
	_, port, err := net.SplitHostPort(addr)
	if err != nil {
		t.Fatalf("reading the pong server's port: %v", err)
	}

	shim := newDockerShim(t, port)

	ensureServer(t)

	calls := shim.calls(t)
	if strings.Contains(calls, "rm") {
		t.Fatalf("the existing stopped container was removed:\n%s", calls)
	}
	if !strings.Contains(calls, "start "+containerName) {
		t.Fatalf("the existing stopped container was not started:\n%s", calls)
	}
}

// startPongServer listens on loopback and answers commands well enough for a
// client to finish its handshake and a Ping to succeed, which is all the
// fixture's readiness check asks of a server: +PONG to PING, the refusal an
// old server gives to HELLO (the client falls back to RESP2, which is all this
// check needs), and +OK to the rest. One read can carry several commands — a
// client pipelines its connection setup — so the answer count follows the
// commands in the buffer rather than the reads.
func startPongServer(t *testing.T) string {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("opening a listener: %v", err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				defer func() { _ = c.Close() }()
				buf := make([]byte, 512)
				for {
					n, err := c.Read(buf)
					if err != nil {
						return
					}
					body := string(buf[:n])
					upper := strings.ToUpper(body)
					replies := strings.Count(body, "*")
					if replies == 0 {
						replies = 1
					}
					for range replies {
						reply := "+OK\r\n"
						switch {
						case strings.Contains(upper, "PING"):
							reply = "+PONG\r\n"
						case strings.Contains(upper, "HELLO"):
							reply = "-ERR unknown command 'HELLO'\r\n"
						}
						if _, err := c.Write([]byte(reply)); err != nil {
							return
						}
					}
				}
			}(conn)
		}
	}()
	return listener.Addr().String()
}

// dockerShim is a fake docker on PATH. It answers the inspect calls the fixture
// makes, publishes the listener's port, keeps the running flag in a file, and
// records every call. The container it describes starts out
// created-but-not-running, which is the state the case above is about.
type dockerShim struct {
	dir string
}

func newDockerShim(t *testing.T, port string) *dockerShim {
	t.Helper()
	dir := t.TempDir()
	script := `#!/bin/sh
dir="$(dirname "$0")"
echo "$@" >>"$dir/calls.log"
case "$1" in
inspect)
	case "$3" in
	"{{.State.Running}}")
		if [ -f "$dir/running" ]; then echo true; else echo false; fi
		;;
	"{{.Name}}")
		echo /` + containerName + `
		;;
	*)
		exit 1
		;;
	esac
	;;
run)
	touch "$dir/running"
	echo ` + containerName + `
	;;
start)
	touch "$dir/running"
	echo ` + containerName + `
	;;
port)
	echo 127.0.0.1:` + port + `
	;;
*)
	exit 1
	;;
esac
`
	if err := os.WriteFile(filepath.Join(dir, "docker"), []byte(script), 0o755); err != nil {
		t.Fatalf("writing the docker shim: %v", err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return &dockerShim{dir: dir}
}

func (s *dockerShim) calls(t *testing.T) string {
	t.Helper()
	body, err := os.ReadFile(filepath.Join(s.dir, "calls.log"))
	if err != nil {
		return ""
	}
	return string(body)
}

// TestConcurrentClaimsNeverHandTwoCallersOneDatabase pins the arbitration the
// index allocation rests on.
//
// The claims come from separate clients under separate owners, which is what
// two test binaries on one machine are; what matters is that the caller's
// identity plays no part in which index it is given. An allocation derived
// from the caller — a counter seeded from the process id, say — hands two
// binaries one database as soon as their identities line up, and neither
// notices until a case in one of them fails on a key the other cleared.
func TestConcurrentClaimsNeverHandTwoCallersOneDatabase(t *testing.T) {
	requireDocker(t)
	addr := ensureServer(t)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Fewer than the databases on offer, so the claims below do not shut out
	// whatever else is running against the same server.
	const callers = 8
	got := make([]int, callers)
	errs := make([]error, callers)
	var wg sync.WaitGroup
	for i := range callers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			got[i], errs[i] = claimFor(ctx, addr, "owner-"+strconv.Itoa(i))
		}()
	}
	wg.Wait()

	seen := make(map[int]bool, callers)
	for i := range callers {
		if errs[i] != nil {
			t.Fatalf("claim %d: %v", i, errs[i])
		}
		if got[i] < 0 {
			t.Fatalf("claim %d was given no database although only %d of them were asked for",
				i, callers)
		}
		if seen[got[i]] {
			t.Fatalf("database %d was handed to two callers at once", got[i])
		}
		seen[got[i]] = true
	}

	// Given back, so this case leaves the server as it found it.
	client := redis.NewClient(&redis.Options{Addr: addr, DB: leaseDB})
	defer func() { _ = client.Close() }()
	for i := range callers {
		if err := client.Del(ctx, leaseKey(got[i])).Err(); err != nil {
			t.Fatalf("releasing database %d: %v", got[i], err)
		}
	}
}
