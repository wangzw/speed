package pgtest

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// The cold-start check: the race the recovery path exists for, driven against a
// real docker by processes running the real code.
//
// It is not part of the suite, and it must not be. Starting a round means
// removing the shared container and the image it is built from, which the cases
// in the packages around this one are using at the time; and whether the window
// it is about opens at all is a matter of timing — the window was reached in
// two rounds out of six when the race was reproduced, so a run that reaches it
// is evidence and a run that does not says nothing. The gate on the recovery
// path is the deterministic one in recovery_test.go, which replays the daemon's
// answers; this check is what says those answers are the real ones.
//
// Run it alone, on a machine whose container nothing else needs:
//
//	SPEED_PGTEST_COLDSTART=1 go -C pkg/db test -count=1 -v -run '^TestColdStartConcurrency$' ./internal/pgtest/
//
// A round is eight peers started at once on a machine with no container and no
// image, each of them taking a database the way any case does. What the round
// reports is two things at once: whether any peer lost the create race and was
// then told, by its own later calls, that the container was still not there —
// that is what reaching the window looks like from outside — and whether every
// peer came up on the one container all the same.

// The environment of the check and of its peers. The gate is read by the
// harness and by nothing else: with it unset, which is every `go test ./...`
// and every make check, both halves skip before they look at anything.
const (
	coldstartEnv           = "SPEED_PGTEST_COLDSTART"
	coldstartChildEnv      = "SPEED_PGTEST_COLDSTART_CHILD"
	coldstartRendezvousEnv = "SPEED_PGTEST_COLDSTART_RENDEZVOUS"
	coldstartPeersEnv      = "SPEED_PGTEST_COLDSTART_PEERS"
)

const (
	// coldstartPeers is how many processes start at once. Eight is the width
	// the race was reproduced at, and enough of them lose the create that the
	// window is reached with some regularity.
	coldstartPeers = 8
	// coldstartRounds bounds the rounds one run may take, and coldstartWindows
	// is how many of them have to have reached the window before it stops
	// early. The window was reached in a third of the rounds when the race was
	// reproduced, so twenty rounds are far past enough — and reaching the limit
	// without it is reported as that, not as a pass.
	coldstartRounds  = 20
	coldstartWindows = 2
	// coldstartRoundLimit bounds one round: a cold image is pulled inside it,
	// by the peer that wins the create.
	coldstartRoundLimit = 5 * time.Minute
	// coldstartBuildLimit bounds the build of the peer binary, which compiles
	// this module's dependencies on a cold cache.
	coldstartBuildLimit = 5 * time.Minute
	// coldstartRendezvousTimeout bounds the wait for the last peer, so that a
	// peer which never started fails the round instead of hanging it.
	coldstartRendezvousTimeout = 2 * time.Minute
)

// coldstartShimScript is the docker each peer finds first on its PATH.
//
// It records what it was asked and what came back, then does it — through the
// real docker, by the absolute path substituted for the placeholder below. It
// copies nothing of the code under test and delays nothing, so what it writes
// is the race as it happened.
//
// The log is one per peer, beside the script, and that is the point of it: the
// window's "No such object" and the answer a first look at a container that has
// not been created yet gets are the same text, so a shared log could not tell a
// peer that ran into the window on its own recovery from a peer that merely
// looked before anything existed. Reading one peer's log alone is what makes
// that distinction.
const coldstartShimScript = `#!/bin/sh
real=REAL_DOCKER
dir=${0%/*}
log=$dir/calls.log
err=$dir/stderr
started=$(date +%s.%N)
"$real" "$@" 2>"$err"
status=$?
finished=$(date +%s.%N)
{
	printf 'argv: %s\n' "$*"
	printf 'exit: %s\n' "$status"
	printf 'from: %s to: %s\n' "$started" "$finished"
	printf 'stderr: %s\n' "$(cat "$err")"
	echo ---
} >>"$log"
cat "$err" >&2
exit "$status"
`

// shimRealDocker is the placeholder the real docker's path replaces.
const shimRealDocker = "REAL_DOCKER"

// peerRun is what one peer did in one round.
type peerRun struct {
	role     string
	exit     int
	stdout   string
	stderr   string
	calls    []dockerCall
	startErr error
}

// dockerCall is one recorded docker invocation, with the moments it started and
// finished: how long a peer took over a call, and how long it took to make the
// next one, is what says whether it looked inside the window or after it.
type dockerCall struct {
	sub      string
	status   int
	stderr   string
	started  float64
	finished float64
}

// timeline is a peer's calls in one line: each subcommand with the status it
// left with, how long after the call before it it started, and how long it took.
// The first call of a peer has no call before it and carries only its own
// duration.
func (r peerRun) timeline() string {
	var line strings.Builder
	var previous float64
	for index, call := range r.calls {
		if index > 0 {
			line.WriteString("; ")
			fmt.Fprintf(&line, "%s(exit %d, after %s, took %s)", call.sub, call.status,
				time.Duration((call.started-previous)*float64(time.Second)).Round(time.Millisecond),
				time.Duration((call.finished-call.started)*float64(time.Second)).Round(time.Millisecond))
		} else {
			fmt.Fprintf(&line, "%s(exit %d, took %s)", call.sub, call.status,
				time.Duration((call.finished-call.started)*float64(time.Second)).Round(time.Millisecond))
		}
		previous = call.finished
	}
	return line.String()
}

// hitWindow reports whether this peer lost the create race and then, by its own
// later calls, was told the container was still not there.
//
// The two have to belong to one peer. A look at a container that does not exist
// yet answers the same thing as a look inside the window, and every peer makes
// that first look; an answer that comes after this peer's own refused create is
// the window, an answer before it is the empty machine the round started on.
//
// The answer has to come from a look or a start, which are the two calls a
// recovery makes. A non-zero status from anything else is a failure of this
// peer for some other reason, and counting it as the window would have a round
// claim coverage it never had.
func (r peerRun) hitWindow() bool {
	conflicted := false
	for _, call := range r.calls {
		recovered := call.sub == "inspect" || call.sub == "start"
		if conflicted && recovered && call.status != 0 {
			return true
		}
		if call.sub == "run" && call.status == conflictStatus && strings.Contains(call.stderr, "Conflict") {
			conflicted = true
		}
	}
	return false
}

// TestColdStartConcurrency is the check itself.
func TestColdStartConcurrency(t *testing.T) {
	if os.Getenv(coldstartEnv) == "" {
		t.Skip("the cold-start check removes the shared container and image and drives a real docker, " +
			"so it runs on demand rather than in the suite: set " + coldstartEnv + "=1, alone")
	}
	dockerPath, err := exec.LookPath("docker")
	if err != nil {
		t.Fatalf("%s is set and this check drives a real docker, which is not on PATH: %v", coldstartEnv, err)
	}
	if dockerPath, err = filepath.Abs(dockerPath); err != nil {
		t.Fatalf("resolving the docker the peers are to run: %v", err)
	}

	peerBinary := buildPeerBinary(t)
	root := t.TempDir()

	var rounds, windowRounds, failures int
	for round := 1; round <= coldstartRounds && windowRounds < coldstartWindows; round++ {
		rounds = round
		coldStart(t, dockerPath, round)
		runs := runPeers(t, peerBinary, dockerPath, root, round)

		var reached []string
		for _, run := range runs {
			if run.hitWindow() {
				reached = append(reached, run.role)
			}
			if run.exit != 0 {
				failures++
				t.Errorf("round %d: %s came up on nothing after the container was created for it:\nstdout:\n%s\nstderr:\n%s",
					round, run.role, run.stdout, run.stderr)
			}
			t.Logf("round %d: %s exited %d: %s", round, run.role, run.exit, run.timeline())
		}
		if len(reached) > 0 {
			windowRounds++
			t.Logf("round %d: the window was reached by %s", round, strings.Join(reached, ", "))
		}
		// A round that failed is not one to repeat: the next round starts from
		// the same place and the code has not changed. Stopping here also
		// leaves the log of the round that failed at the end of the output.
		if failures > 0 {
			break
		}
	}

	switch {
	case failures > 0:
		t.Errorf("%d of the peers in these %d rounds could not reach the container, and a create that "+
			"cannot be recovered has to end in a reported failure rather than in a peer without a "+
			"database", failures, rounds)
	case windowRounds == 0:
		t.Skipf("none of the %d rounds reached the window: no peer lost the create race and was then "+
			"told by its own calls that the container was not there, so this run covers nothing about "+
			"the recovery. The deterministic cases are the gate; this check says the answers they replay "+
			"are the real ones, and it did not get to say it", rounds)
	default:
		t.Logf("%d of %d rounds reached the window, and every peer in them came up on the one container",
			windowRounds, rounds)
	}
}

// TestColdstartChild is the peer half of the check. The harness starts the
// package's test binary once per peer with this case selected, and this is what
// each of them runs: meet the others, then take a database the way any case
// does.
//
// It does that one thing so that a peer's exit status is the outcome of one
// acquisition and nothing else — which is what makes "this peer converged" a
// thing a round can read, and a failure of it a thing the round can report.
func TestColdstartChild(t *testing.T) {
	role := os.Getenv(coldstartChildEnv)
	if role == "" {
		t.Skip("the peer half of TestColdStartConcurrency, which re-executes this binary once per peer")
	}
	peers, err := strconv.Atoi(os.Getenv(coldstartPeersEnv))
	if err != nil {
		t.Fatalf("the check started this peer with %q as the number of peers: %v",
			os.Getenv(coldstartPeersEnv), err)
	}
	if err := meetPeers(os.Getenv(coldstartRendezvousEnv), role, peers); err != nil {
		t.Fatal(err)
	}
	Acquire(t)
}

// meetPeers blocks until every peer has reached this point, so that they go for
// the container together rather than one after the other.
func meetPeers(rendezvous, role string, peers int) error {
	if err := os.WriteFile(filepath.Join(rendezvous, "ready-"+role), nil, 0o600); err != nil {
		return fmt.Errorf("announcing %s at the rendezvous: %w", role, err)
	}
	deadline := time.Now().Add(coldstartRendezvousTimeout)
	for time.Now().Before(deadline) {
		entries, err := os.ReadDir(rendezvous)
		if err != nil {
			return fmt.Errorf("reading the rendezvous: %w", err)
		}
		arrived := 0
		for _, entry := range entries {
			if strings.HasPrefix(entry.Name(), "ready-") {
				arrived++
			}
		}
		if arrived >= peers {
			return nil
		}
		time.Sleep(5 * time.Millisecond)
	}
	return fmt.Errorf("only some of the %d peers reached the rendezvous within %s",
		peers, coldstartRendezvousTimeout)
}

// coldStart clears away everything that would make the next round a warm one:
// the shared container, and the image that creating one is built from.
//
// It runs before every round rather than once, because the window only opens on
// a machine where the image has to be pulled first. A round that started warm
// would run to the end without the chance of reaching the window at all, and
// the image is therefore checked to be gone before the round is allowed to
// count as one.
func coldStart(t *testing.T, dockerPath string, round int) {
	t.Helper()
	for _, args := range [][]string{
		{"rm", "--force", containerName},
		{"rmi", image},
	} {
		out, err := exec.Command(dockerPath, args...).CombinedOutput()
		t.Logf("round %d: docker %s: %v\n%s", round, strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	if err := exec.Command(dockerPath, "image", "inspect", "--format", "{{.Id}}", image).Run(); err == nil {
		t.Fatalf("the %s image is still on this machine after being removed, so no round of this check "+
			"would start cold and the window it is about would not open", image)
	}
}

// runPeers starts one round's peers at once and collects what they did.
//
// The shims are planted first, in this goroutine: a peer that failed to start
// has to fail the round, and t.Fatalf belongs to the test's own goroutine.
func runPeers(t *testing.T, peerBinary, dockerPath, root string, round int) []peerRun {
	t.Helper()
	rendezvous := t.TempDir()
	shims := make([]string, coldstartPeers)
	for index := range shims {
		shims[index] = plantShim(t, dockerPath, root, fmt.Sprintf("peer-%d", index), round)
	}

	runs := make([]peerRun, coldstartPeers)
	var starting sync.WaitGroup
	for index := range runs {
		starting.Add(1)
		go func() {
			defer starting.Done()
			runs[index] = runPeer(peerBinary, shims[index], rendezvous, fmt.Sprintf("peer-%d", index))
		}()
	}
	starting.Wait()

	for _, run := range runs {
		if run.startErr != nil {
			t.Fatalf("starting %s: %v", run.role, run.startErr)
		}
	}
	return runs
}

// plantShim writes one peer's pass-through docker and reports the directory to
// put in front of its PATH.
func plantShim(t *testing.T, dockerPath, root, role string, round int) string {
	t.Helper()
	dir := filepath.Join(root, fmt.Sprintf("round-%d", round), "shim-"+role)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("making the directory for %s's docker: %v", role, err)
	}
	script := strings.Replace(coldstartShimScript, shimRealDocker, strconv.Quote(dockerPath), 1)
	if err := os.WriteFile(filepath.Join(dir, "docker"), []byte(script), 0o755); err != nil {
		t.Fatalf("planting %s's docker: %v", role, err)
	}
	return dir
}

// runPeer starts one peer as a separate process and collects what it did.
//
// The environment is built rather than inherited, so that nothing on the
// machine running the check can change what a peer sees: PATH begins with that
// peer's own shim, and everything below it — the CI flag included, which
// decides whether a peer may skip instead of failing — is passed on as it is.
func runPeer(peerBinary, shimDir, rendezvous, role string) peerRun {
	ctx, cancel := context.WithTimeout(context.Background(), coldstartRoundLimit)
	defer cancel()
	cmd := exec.CommandContext(ctx, peerBinary, "-test.run=^TestColdstartChild$")
	cmd.Env = []string{
		"PATH=" + shimDir + string(os.PathListSeparator) + os.Getenv("PATH"),
		coldstartChildEnv + "=" + role,
		coldstartRendezvousEnv + "=" + rendezvous,
		coldstartPeersEnv + "=" + strconv.Itoa(coldstartPeers),
	}
	if value, present := os.LookupEnv("CI"); present {
		cmd.Env = append(cmd.Env, "CI="+value)
	}
	var stdout, stderr strings.Builder
	cmd.Stdout, cmd.Stderr = &stdout, &stderr

	run := peerRun{role: role}
	err := cmd.Run()
	run.stdout, run.stderr = stdout.String(), stderr.String()
	var exit *exec.ExitError
	switch {
	case err == nil:
	case errors.As(err, &exit):
		run.exit = exit.ExitCode()
	default:
		run.startErr = err
	}
	run.calls = readCalls(filepath.Join(shimDir, "calls.log"))
	return run
}

// readCalls reads one peer's log of what docker was asked. A peer that got as
// far as running docker has a log; one that did not has none, and an empty list
// is what says so.
func readCalls(path string) []dockerCall {
	body, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var calls []dockerCall
	for _, entry := range strings.Split(string(body), "---\n") {
		if strings.TrimSpace(entry) == "" {
			continue
		}
		call := dockerCall{}
		for _, line := range strings.Split(entry, "\n") {
			switch {
			case strings.HasPrefix(line, "argv: "):
				if fields := strings.Fields(strings.TrimPrefix(line, "argv: ")); len(fields) > 0 {
					call.sub = fields[0]
				}
			case strings.HasPrefix(line, "exit: "):
				if status, err := strconv.Atoi(strings.TrimSpace(strings.TrimPrefix(line, "exit: "))); err == nil {
					call.status = status
				}
			case strings.HasPrefix(line, "stderr: "):
				call.stderr = strings.TrimPrefix(line, "stderr: ")
			case strings.HasPrefix(line, "from: "):
				from, to, found := strings.Cut(strings.TrimPrefix(line, "from: "), " to: ")
				if !found {
					continue
				}
				call.started, _ = strconv.ParseFloat(from, 64)
				call.finished, _ = strconv.ParseFloat(to, 64)
			}
		}
		calls = append(calls, call)
	}
	return calls
}

// buildPeerBinary compiles this package's test binary for the peers to run.
//
// The peers are separate processes of the code as it stands — the same
// ensureContainer every test binary on this machine goes through, reached by
// several of them at once, which is the thing being checked. Compiling it here
// rather than re-executing this binary is also what lets the check run against
// a checkout of the code before a fix: run it from that checkout's directory
// and the peers are built from there.
func buildPeerBinary(t *testing.T) string {
	t.Helper()
	out := filepath.Join(t.TempDir(), "pgtest.test")
	ctx, cancel := context.WithTimeout(context.Background(), coldstartBuildLimit)
	defer cancel()
	if body, err := exec.CommandContext(ctx, "go", "test", "-c", "-o", out).CombinedOutput(); err != nil {
		t.Fatalf("building the peer binary out of this package failed: %v\n%s", err, body)
	}
	return out
}
