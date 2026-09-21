// Package natstest hands the tests a NATS server to run against.
//
// It drives the docker CLI through os/exec, for the same reason the other
// fixtures do: a test-only container library would still land in this module's
// dependency list.
//
// Isolation is by bucket or stream name rather than by server: JetStream has no
// logical databases, and the cases name their own so that two of them on one
// server cannot see each other's entries.
package natstest

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
	containerName = "speed-natstest"
	// image is pinned: the cases lean on JetStream's compare-and-swap and on a
	// pull consumer, and both are properties of a version.
	image = "nats:2-alpine"
	// clientPort is NATS's own.
	clientPort = "4222"
	// readyTimeout bounds the wait for a cold container to answer, the image
	// pull included.
	readyTimeout = 3 * time.Minute
	// dockerTimeout bounds one docker invocation.
	dockerTimeout = time.Minute
	runningState  = "true"
)

// URL returns the address of a server with JetStream enabled.
func URL(t *testing.T) string {
	t.Helper()
	requireDocker(t)
	if !running(t) {
		if exists(t) {
			run(t, "rm", "-f", containerName)
		}
		// The trailing argument is the server's own: -js turns JetStream on,
		// and without it every bucket operation fails at open.
		run(t, "run", "-d", "--name", containerName, "-p", "0:"+clientPort, image, "-js")
	}
	addr := publishedAddr(t)
	waitReady(t, addr)
	return "nats://" + addr
}

func requireDocker(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("docker"); err != nil {
		if os.Getenv("CI") != "" {
			t.Fatalf("natstest: docker is required in CI and was not found: %v", err)
		}
		t.Skip("natstest: docker is not installed, and the cases that need a real NATS are skipped")
	}
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
	out, err := docker(t, "port", containerName, clientPort)
	if err != nil {
		t.Fatalf("natstest: reading the published port: %v", err)
	}
	line := strings.TrimSpace(strings.Split(strings.TrimSpace(out), "\n")[0])
	i := strings.LastIndex(line, ":")
	if i < 0 {
		t.Fatalf("natstest: docker port printed %q, which names no port", out)
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
	t.Fatalf("natstest: the server at %s did not accept connections within %s", addr, readyTimeout)
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
		t.Fatalf("natstest: docker %s: %v\n%s", strings.Join(args, " "), err, out)
	}
}
