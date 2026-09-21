package redistest

import (
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
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
