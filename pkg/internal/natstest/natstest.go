// Package natstest hands the tests a NATS server to run against.
//
// It drives the docker CLI through os/exec, for the same reason the other
// fixtures do: a test-only container library would still land in a module's
// dependency list.
//
// Isolation is by bucket or stream name rather than by server: JetStream has no
// logical databases, and the cases name their own so that two of them on one
// server cannot see each other's entries.
//
// The key-value and event modules' NATS backends both test against this one, so
// it stands as a shared unit of its own in pkg/internal/natstest, inside the
// pkg module's internal/ tree: it is read by tests only, belongs to no public
// surface, and the compiler holds that, keeping anything outside the module
// from importing it. The pool-based fixture lives in pkg/internal/pgpool/pgtest,
// inside the pool wrapper's directory, and the record behind both is
// docs/adr/adr-shared-backend-unit-2026-09-21.md.
package natstest

import (
	"context"
	"errors"
	"fmt"
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
	// bringUpBound bounds the wait that follows a create lost to another test
	// binary, and bringUpInterval is how often it looks while it waits. The
	// bound is generous against the window a losing creator spends being told
	// there is no such container — measured at 100–200ms on a cold pgtest
	// image, and this one starts lighter — while staying bounded, so a daemon
	// that is genuinely broken ends in a failure carrying docker's own words
	// rather than in a hang.
	bringUpBound    = 30 * time.Second
	bringUpInterval = 150 * time.Millisecond
)

// URL returns the address of a server with JetStream enabled.
func URL(t *testing.T) string {
	t.Helper()
	requireDocker(t)
	ensureServer(t)
	addr := publishedAddr(t)
	waitReady(t, addr)
	return "nats://" + addr
}

// ensureServer leaves the shared server running, tolerating the window in which
// another test binary on this machine is creating it.
func ensureServer(t *testing.T) {
	t.Helper()
	// A running one is the whole of the happy path, and it is what every test
	// binary after the first finds.
	if running(t) {
		return
	}
	// A container that is there but not running is started, not removed: it is
	// either an interrupted run's leftover or another test binary's container a
	// moment before it comes up, and removing it would reach into that window
	// and take the other binary's server with it.
	if exists(t) {
		if _, err := docker(t, "start", containerName); err != nil && !running(t) {
			t.Fatalf("natstest: starting the existing %s container: %v", containerName, err)
		}
		return
	}
	createErr := create(t)
	if createErr == nil {
		return
	}
	// The create can lose to another test binary starting at the same moment:
	// docker refuses the duplicate name, and it takes the name before the
	// container can be looked at, so the loser spends a window being told there
	// is no such container and only then finds the winner's. The wait below is
	// that window, and the container it ends on is the winner's; a losing
	// process never creates one of its own.
	deadline := time.Now().Add(bringUpBound)
	for time.Now().Before(deadline) {
		if running(t) {
			return
		}
		time.Sleep(bringUpInterval)
	}
	t.Fatalf("natstest: the %s container did not come up within %s: create failed with %v, and it is %s",
		containerName, bringUpBound, createErr, stateWords(t))
}

// create starts the container. It reports docker's refusal as an error rather
// than failing the case, because the caller has to decide whether the refusal
// was the race above.
func create(t *testing.T) error {
	t.Helper()
	// The trailing argument is the server's own: -js turns JetStream on, and
	// without it every bucket operation fails at open.
	_, err := docker(t, "run", "-d", "--name", containerName, "-p", "0:"+clientPort, image, "-js")
	return err
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

// stateWords reports what docker says the container's state is right now, for
// the give-up message: the state, or why it could not be read.
func stateWords(t *testing.T) string {
	t.Helper()
	out, err := docker(t, "inspect", "-f", "{{.State.Status}}", containerName)
	if err != nil {
		return fmt.Sprintf("not inspectable (%v)", err)
	}
	return strings.TrimSpace(out)
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
	if err != nil {
		// docker's own words are on stderr, and without them a failure here
		// reads as a bare "exit status 125" with nothing to act on.
		var exit *exec.ExitError
		if errors.As(err, &exit) {
			return string(out), fmt.Errorf("%w: %s", err, strings.TrimSpace(string(exit.Stderr)))
		}
	}
	return string(out), err
}
