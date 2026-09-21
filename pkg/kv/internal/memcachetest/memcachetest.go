// Package memcachetest hands the tests a Memcached server to run against.
//
// It drives the docker CLI through os/exec, for the same reason the other
// fixtures do: a test-only container library would still land in this module's
// dependency list.
//
// Isolation is per case rather than per container: Memcached has no logical
// databases, so each Acquire flushes the server and the cases run one at a
// time. Nothing else in the workspace uses this server.
package memcachetest

import (
	"context"
	"net"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

const (
	// containerName is fixed so that every test binary on this machine reuses
	// one server rather than starting its own.
	containerName = "speed-memcachetest"
	// image is pinned: the cases lean on compare-and-swap and on add, and their
	// behaviour is a property of a version.
	image = "memcached:1.6-alpine"
	// serverPort is Memcached's own.
	serverPort = "11211"
	// readyTimeout bounds the wait for a cold container to answer, the image
	// pull included.
	readyTimeout = 2 * time.Minute
	// dockerTimeout bounds one docker invocation.
	dockerTimeout = time.Minute
	runningState  = "true"
)

// Addr returns the address of the shared server.
//
// It does not empty it. Memcached has no logical databases and its own flush is
// a command a client sends, so the caller that wants a clean server sends it —
// see the flush in the cases that need one.
func Addr(t *testing.T) string {
	t.Helper()
	requireDocker(t)
	return ensureServer(t)
}

func requireDocker(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("docker"); err != nil {
		if os.Getenv("CI") != "" {
			t.Fatalf("memcachetest: docker is required in CI and was not found: %v", err)
		}
		t.Skip("memcachetest: docker is not installed, and the cases that need a real Memcached are skipped")
	}
}

func ensureServer(t *testing.T) string {
	t.Helper()
	if !running(t) {
		if exists(t) {
			run(t, "rm", "-f", containerName)
		}
		run(t, "run", "-d", "--name", containerName, "-p", "0:"+serverPort, image)
	}
	addr := publishedAddr(t)
	waitReady(t, addr)
	return addr
}

func running(t *testing.T) bool {
	t.Helper()
	out, err := docker(t, "inspect", "-f", "{{.State.Running}}", containerName)
	return err == nil && strings.TrimSpace(out) == runningState
}

func exists(t *testing.T) bool {
	t.Helper()
	_, err := docker(t, "inspect", "-f", "{{.Name}}", containerName)
	return err == nil
}

func publishedAddr(t *testing.T) string {
	t.Helper()
	out, err := docker(t, "port", containerName, serverPort)
	if err != nil {
		t.Fatalf("memcachetest: reading the published port: %v", err)
	}
	line := strings.TrimSpace(strings.Split(strings.TrimSpace(out), "\n")[0])
	i := strings.LastIndex(line, ":")
	if i < 0 {
		t.Fatalf("memcachetest: docker port printed %q, which names no port", out)
	}
	return "127.0.0.1:" + line[i+1:]
}

func waitReady(t *testing.T, addr string) {
	t.Helper()
	deadline := time.Now().Add(readyTimeout)
	for time.Now().Before(deadline) {
		conn, err := net.DialTimeout("tcp", addr, 2*time.Second)
		if err == nil {
			_ = conn.Close()
			return
		}
		time.Sleep(200 * time.Millisecond)
	}
	t.Fatalf("memcachetest: the server at %s did not accept connections within %s", addr, readyTimeout)
}

func docker(t *testing.T, args ...string) (string, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), dockerTimeout)
	defer cancel()
	//nolint:gosec // every argument is one of this file's own constants; nothing a case supplies reaches the command line
	out, err := exec.CommandContext(ctx, "docker", args...).Output()
	return string(out), err
}

func run(t *testing.T, args ...string) {
	t.Helper()
	out, err := docker(t, args...)
	if err != nil {
		t.Fatalf("memcachetest: docker %s: %v\n%s", strings.Join(args, " "), err, out)
	}
}
