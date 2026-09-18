package pgtest

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"go/build"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
)

// The create-conflict cases, run against a stand-in docker that replays the
// answers the real one gave during the race on issue #12.
//
// ensureContainer is what every test binary on this machine goes through to
// reach the shared container, and on a machine where the container is not up
// yet several of them go through it at once. Losing that race is normal: one
// binary creates the container and the others are refused the name. The refusal
// is not the end of it, though. docker takes the name before the container can
// be looked at, so for a window afterwards the losing binary asks about an
// object that does not exist yet — the window measured 100–200ms on a cold
// image, and it is what the old recovery, which looked exactly once, fell into.
//
// The responses below are the ones captured inside that window, so whether a
// loser recovers is decided by the code and not by the machine it runs on. The
// real race is checked against a real docker separately, on demand, in
// coldstart_test.go — it cannot be a gate, because whether the window opens at
// all is a matter of timing.

// The daemon's answers, taken from the capture on the issue. inspect and start
// are answered with the same two responses both before the container exists and
// in the window after the name is taken, which is why the recovery cannot tell
// the two apart by what they say and waits for the container instead.
//
// What is kept of each is only the part that says what is wrong. The match is
// containment (see hitWindow), and around that part sit a prefix that moves
// between docker versions and this package's own container name — neither is
// docker's to keep stable. Both answers are measured, on docker 28.0.4: an
// inspect of a name that is not there answers "Error: No such object: <name>",
// and a start answers "Error response from daemon: No such container: <name>"
// ahead of "Error: failed to start containers: <name>". The words are held to
// the machine's own docker by
// TestWhatDockerSaysAboutAMissingContainerIsWhatTheWindowAsksFor, so a docker
// that words them differently fails there rather than quietly costing the
// window one of its halves.
const (
	// conflictResponse is the refusal of a second container under the shared
	// name. docker leaves 125 for it.
	conflictResponse = `docker: Error response from daemon: Conflict. The container name "/speed-pgtest" is already in use by container "5b8f0c2a91d4".`
	// missingObjectResponse is the words inspect answers a name that is not
	// there with, in the window and before it alike.
	missingObjectResponse = `No such object`
	// missingContainerResponse is the words start answers a name that is not
	// there with.
	missingContainerResponse = `No such container`
	// conflictStatus and failureStatus are the exit statuses that come with
	// them: docker's own for a refused create, and the CLI's for a command
	// that could not be carried out.
	conflictStatus = 125
	failureStatus  = 1
)

// TestWhatDockerSaysAboutAMissingContainerIsWhatTheWindowAsksFor holds the two
// replayed answers to the machine's own docker.
//
// The window cases replay what docker answered during the race, and this is the
// case that says the answers are docker's own words: a stand-in can only repeat
// what it was told, so nothing inside a replay can check the material it is made
// of. That is why it is here, beside the constants, and not with the cold-start
// harness in coldstart_test.go — the harness is about the race, this is about
// the words the race is replayed with.
//
// It makes the two calls a recovery makes — the look and the start — against a
// name that cannot be there, behind the harness's own shim, so that each answer
// is read back the way a peer's log holds it: the exit status docker left with,
// and the first line of standard error. What has to come out is a peer whose
// create was refused and was then told by that call that the container was not
// there — the window as hitWindow counts it, on this machine's docker.
func TestWhatDockerSaysAboutAMissingContainerIsWhatTheWindowAsksFor(t *testing.T) {
	if reason := Skip(); reason != "" {
		if _, underCI := os.LookupEnv("CI"); underCI {
			t.Fatalf("CI is set, so a skip here would leave the words the window cases replay unheld to "+
				"a real docker: %s", reason)
		}
		t.Skip(reason)
	}
	dockerPath, err := exec.LookPath("docker")
	if err != nil {
		t.Fatalf("this case drives the machine's own docker, which is not on PATH: %v", err)
	}
	if dockerPath, err = filepath.Abs(dockerPath); err != nil {
		t.Fatalf("resolving the docker to run: %v", err)
	}
	// Beside the shared name and not the shared name itself: what this needs is
	// a name that is not there, and the shared container may well be up.
	name := containerName + "-wording-guard"

	for _, c := range []struct {
		call     string
		args     []string
		constant string
		words    string
	}{
		{
			"inspect",
			[]string{"inspect", "--format", "{{.State.Running}}", name},
			"missingObjectResponse", missingObjectResponse,
		},
		{"start", []string{"start", name}, "missingContainerResponse", missingContainerResponse},
	} {
		t.Run(c.call, func(t *testing.T) {
			shim := plantShim(t, dockerPath, t.TempDir(), "wording-guard", 0)
			ctx, cancel := context.WithTimeout(context.Background(), dockerTimeout)
			defer cancel()
			err := exec.CommandContext(ctx, filepath.Join(shim, "docker"), c.args...).Run()
			if err == nil {
				t.Fatalf("docker %s answered cleanly although the name is not there, so there is no "+
					"answer to read: is %s a container on this machine?", strings.Join(c.args, " "), name)
			}
			var exit *exec.ExitError
			if !errors.As(err, &exit) {
				t.Fatalf("running docker %s behind the shim: %v", strings.Join(c.args, " "), err)
			}
			recorded := readCalls(filepath.Join(shim, "calls.log"))
			if len(recorded) != 1 {
				t.Fatalf("the shim recorded %d calls for the one it ran — a call with nothing recorded "+
					"never reached docker or was cut off before it could: %v", len(recorded), recorded)
			}
			run := peerRun{calls: []dockerCall{
				{sub: "run", status: conflictStatus, stderr: conflictResponse},
				recorded[0],
			}}
			if !run.hitWindow() {
				t.Errorf("docker %s against a name that is not there left exit %d and %q, and a peer told "+
					"that is not counted as one that reached the window, whose %s half asks for %q: bring "+
					"the constant to the words this docker answers with, since the answers the window cases "+
					"replay are the real ones only as long as this holds",
					strings.Join(c.args, " "), recorded[0].status, recorded[0].stderr, c.constant, c.words)
			}
		})
	}
}

// dockerStubScript is the stand-in this file plants on PATH. It is a shell
// script rather than a program because it has to carry the name docker has:
// the code under test runs the docker CLI out of PATH, and a stub is the only
// way to answer it without a daemon.
//
// Its answers come from a file per subcommand, one response per line, with the
// last line repeating once the list runs out; a subcommand with no file is a
// case calling docker in an order it did not describe, and says so. Every call
// is appended to calls.log, so a case can assert the calls the code made.
//
// It uses shell builtins only, on purpose: PATH holds this directory and
// nothing else, so that no case can reach a real docker however this machine
// has docker installed and wherever it keeps it.
const dockerStubScript = `#!/bin/sh
dir=${0%/*}
sub=$1
count=0
if [ -f "$dir/$sub.count" ]; then
	read -r count <"$dir/$sub.count"
fi
count=$((count + 1))
echo "$count" >"$dir/$sub.count"
echo "$sub" >>"$dir/calls.log"

line=
last=
i=0
while IFS= read -r candidate; do
	i=$((i + 1))
	if [ "$i" -eq "$count" ]; then
		line=$candidate
		break
	fi
	last=$candidate
done <"$dir/$sub.responses"
if [ -z "$line" ]; then
	line=$last
fi

case $line in
ok:*)
	echo "${line#ok:}"
	exit 0
	;;
fail:*)
	rest=${line#fail:}
	code=${rest%%:*}
	echo "${rest#*:}" >&2
	exit "$code"
	;;
esac
echo "the docker stand-in was asked for $sub, which this case does not describe" >&2
exit 127
`

// dockerStub is a docker on PATH that answers from a script the case wrote.
type dockerStub struct {
	dir string
}

// newDockerStub plants the stand-in in a directory of its own and points PATH
// at that directory alone.
//
// PATH goes back to what it was when the case ends, so the cases in this
// package that do use the real container are unaffected by any of this — with
// one exception. A case that gave up on a call leaves PATH on the stand-in that
// swallows calls instead, because that call is still reading PATH on every
// round it makes, and what it must not find there is the machine's own docker.
// The restoring is done here, in a cleanup of its own, rather than through
// t.Setenv for exactly that reason: t.Setenv puts the old PATH back
// unconditionally, and this is the one case in which putting it back is the
// wrong thing to do.
func newDockerStub(t *testing.T) *dockerStub {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "docker"), []byte(dockerStubScript), 0o755); err != nil {
		t.Fatalf("planting the docker stand-in: %v", err)
	}
	previous := os.Getenv("PATH")
	os.Setenv("PATH", dir)
	t.Cleanup(func() {
		if !abandoned {
			os.Setenv("PATH", previous)
			return
		}
		// Planted here and not before, because this is the only moment it is
		// needed: on every other path the directory would be made for a call
		// that never comes, left behind in the machine's temporary files. A
		// failure here cannot fail the case the usual way — a t.Fatal in a
		// cleanup would leave the remaining cleanups unrun — so it is reported
		// and PATH is left where it is, which points at a directory about to be
		// removed and so at no docker at all, the real one included.
		swallow, err := swallowStubDir()
		if err != nil {
			t.Errorf("a call has outlived this case and the stand-in that keeps it away from the real "+
				"docker could not be planted: %v", err)
			return
		}
		os.Setenv("PATH", swallow)
	})
	return &dockerStub{dir: dir}
}

// answers describes what the stand-in says to one subcommand: one response per
// call, with the last response answering every call after it.
func (s *dockerStub) answers(t *testing.T, sub string, responses ...string) {
	t.Helper()
	body := strings.Join(responses, "\n") + "\n"
	if err := os.WriteFile(filepath.Join(s.dir, sub+".responses"), []byte(body), 0o600); err != nil {
		t.Fatalf("describing what the stand-in answers to docker %s: %v", sub, err)
	}
}

// calls is the subcommands the code under test has run so far, in order.
func (s *dockerStub) calls(t *testing.T) []string {
	t.Helper()
	body, err := os.ReadFile(filepath.Join(s.dir, "calls.log"))
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		t.Fatalf("reading what the code ran: %v", err)
	}
	return strings.Fields(string(body))
}

// counted is how many times the code under test has run one subcommand.
func (s *dockerStub) counted(t *testing.T, sub string) int {
	t.Helper()
	n := 0
	for _, call := range s.calls(t) {
		if call == sub {
			n++
		}
	}
	return n
}

// succeeded is the response of a docker command that worked, printing out.
func succeeded(out string) string { return "ok:" + out }

// failed is the response of one that did not: the status docker left with, and
// what it wrote to standard error.
func failed(status int, message string) string { return fmt.Sprintf("fail:%d:%s", status, message) }

// swallowStubScript is docker as a call that outlived its case is left seeing
// it: it takes every call and fails it, saying what it is. Nothing it is asked
// can come up, so the call is left to whatever its own bound does about it —
// and, what this stands there for, nothing reaches the real docker.
//
// It fails rather than succeeding with nothing to say because of who else can
// meet it. A case that uses the real container asks docker whether the daemon
// is reachable first, and a stand-in that answered that question with a clean
// exit and no output would send it on to fail further down over a port that
// reads as empty — a report about a daemon the machine has and is not
// reachable only through this. The failure names this stand-in, so that case
// reports what actually happened.
const swallowStubScript = `#!/bin/sh
echo "the stand-in for a call that outlived its test case: the real docker is deliberately not reachable through this" >&2
exit 1
`

// swallowStubDir reports the directory of the stand-in that swallows calls a
// case has given up on, planting it the first time one is given up on.
//
// It is made beside the test's temporary files rather than inside one: a
// t.TempDir is removed when its case ends, and the call this stands in front of
// is still asking PATH for docker after that. The directory outlives every case
// on purpose — it is one directory per test binary, made the first time a case
// gives up on a call — and it stops there: TestMain removes it when the binary
// is on its way out, past which there is no call left to keep off the real
// docker and a directory nobody will look at again.
var swallowStubDir = sync.OnceValues(func() (string, error) {
	dir, err := os.MkdirTemp("", "pgtest-swallow-")
	if err != nil {
		return "", fmt.Errorf("making the directory of the stand-in for abandoned calls: %w", err)
	}
	// Recorded the moment it exists, ahead of the stand-in being planted in
	// it: a directory whose docker could not be written is still one TestMain
	// has to remove.
	swallowStubPlanted = dir
	if err := os.WriteFile(filepath.Join(dir, "docker"), []byte(swallowStubScript), 0o755); err != nil {
		return "", fmt.Errorf("planting the stand-in for abandoned calls: %w", err)
	}
	return dir, nil
})

// swallowStubPlanted is the directory swallowStubDir made, for TestMain to
// remove. It stays empty in a run where no case gave up on a call, which is
// what says there is nothing to remove — asking swallowStubDir instead would
// make the directory it is being asked about.
var swallowStubPlanted string

// TestMain is where the stand-in for calls a case gave up on is cleaned up.
//
// The run is the first moment it may go: the directory keeps calls that are
// still making rounds off the real docker, and a call a case gave up on is
// still asking PATH for docker when its case ends — so nothing inside a case
// can remove it, and nothing past the run has a use for it.
//
// The directory stops at the run and not at every way a run can end: one that
// dies before m.Run returns — its own test-timeout panic, SIGKILL, OOM — never
// reaches the removal and leaves the directory behind. That is the boundary
// testing.TempDir's cleanup has too, and this one stays there on purpose: of
// the ways a run can end, only SIGINT and SIGTERM could be caught, and a signal
// handler would be out of proportion to what is left — a few hundred bytes
// under the machine's temporary directory that nothing will look at again.
//
// The exit status is the run's own, which is the whole of what a test binary
// reports: leaving with anything else would make this package's cases unable to
// fail. That is what TestTheSwallowStandInIsGoneWhenTheRunEnds holds, and what
// it finds it reports by leaving directly instead of through this function's
// status — deliberately, since a status decided here is exactly what such a
// change would swallow.
func TestMain(m *testing.M) {
	code := m.Run()
	if swallowStubPlanted != "" {
		_ = os.RemoveAll(swallowStubPlanted)
	}
	os.Exit(code)
}

// abandoned records that a case gave up on a call that is still running, and
// that what the stand-ins this package plants record from here on is no longer
// any one case's own calls.
//
// A call that outlives its case is one whose implementation ignored the bound
// it was given: it is not coming back when the case needed it to, and every
// round it makes asks PATH for docker. The case's own stand-in goes away with
// the case, so PATH is left on the stand-in that swallows calls (see
// newDockerStub), which keeps those rounds off the machine's real docker; but
// the cases that run next plant stand-ins of their own, and once one is up the
// abandoned call's rounds land in that too — advancing its counts and taking
// answers meant for the case. From then on a stand-in is no longer evidence of
// what one case asked docker, and a case that asserted on it would report a
// defect that is not there.
var abandoned bool

// strayEvidence reports why a case cannot trust what its stand-in recorded, or
// an empty string when the record is its own.
func strayEvidence() string {
	if !abandoned {
		return ""
	}
	return "a call an earlier case gave up on is still running, and its rounds are landing in this " +
		"case's stand-in: the calls recorded here are not all its own, and nothing can be concluded " +
		"from them"
}

// skipIfStray skips the case when what its stand-in records is not its own.
func skipIfStray(t *testing.T) {
	t.Helper()
	if reason := strayEvidence(); reason != "" {
		t.Skip(reason)
	}
}

// recoveryRun is what driving one ensureContainerWithin left behind: how long
// it took and what it ended on when it came back, or, when the case's guard
// gave up first, the channel the still-running call's result will arrive on.
type recoveryRun struct {
	took     time.Duration
	err      error
	outlived <-chan error
}

// driveRecovery runs ensureContainerWithin at the given pace and waits for it
// for up to limit.
//
// Nothing here waits past that limit. The call is what a case is timing, and a
// call that has not come back by then is one that ignored the bound it was
// given — the failure this whole file is about — which owes the case nothing,
// not even a return. The guard gives up where it stands: the call is recorded
// as one that has outlived its case (see abandoned), and whoever asked is
// handed the channel its result will arrive on, for a case that wants to wait
// for it on its own terms.
func driveRecovery(bound, interval, limit time.Duration) recoveryRun {
	started := time.Now()
	done := make(chan error, 1)
	go func() { done <- ensureContainerWithin(bound, interval) }()
	select {
	case err := <-done:
		return recoveryRun{took: time.Since(started), err: err}
	case <-time.After(limit):
		abandoned = true
		return recoveryRun{outlived: done}
	}
}

// underRecoveryWait is driveRecovery for a case: it fails the case when the
// call has not come back within limit.
//
// The waiting is what makes the bound a thing a case has to check at all: a
// recovery that keeps waiting is the failure mode this whole check is about, a
// daemon that is down has to end in a reported failure rather than in a test
// run that never finishes, and a case that simply waited for it would take the
// suite's own timeout to say so.
func underRecoveryWait(t *testing.T, bound, interval, limit time.Duration) (time.Duration, error) {
	t.Helper()
	run := driveRecovery(bound, interval, limit)
	if run.outlived != nil {
		t.Fatalf("bringing the container up has not come back within %s, and a create that cannot be "+
			"recovered has to end in a failure within the wait it was given (%s)", limit, bound)
	}
	return run.took, run.err
}

// requireEndedWithinItsBound fails the case when a failure that was given a
// bound took longer to arrive than that bound allows for.
//
// What it catches is an implementation that does not read its own bound — one
// that checks the deadline only every so often, or not at all. The case's own
// guard cannot say that: the guard is what ends a case whose call never comes
// back, and it is set far wider than the bound on purpose. The slack is for
// this goroutine's scheduling and the stand-in's own invocations; a second is
// orders of magnitude more than those take, and orders of magnitude less than
// what an implementation deaf to its bound would add — the assertion this
// replaces gave the failure a whole minute, which nothing that fails within
// the guard's ten can ever reach, so it could not fail at all.
func requireEndedWithinItsBound(t *testing.T, took, bound time.Duration) {
	t.Helper()
	const slack = time.Second
	if took > bound+slack {
		t.Errorf("the failure took %s, and the wait it was given was %s: it has to end when that wait "+
			"ends", took, bound)
	}
}

// TestConflictRecoveryReusesTheWinningContainer is the case the race is about:
// another test binary created the container and this one was refused the name,
// so the container this one goes on to use is the one that won.
//
// The winner is not there to be seen at first — inspect answers "No such
// object" for as long as the window lasts, which is what the old recovery ended
// on. The stand-in answers that twice before the container shows up: the first
// is the look taken before this process tried to create anything, the second
// the look the old recovery made and gave up on. One answer would make this
// case pass without the code being fixed at all.
func TestConflictRecoveryReusesTheWinningContainer(t *testing.T) {
	stub := newDockerStub(t)
	skipIfStray(t)
	stub.answers(t, "inspect",
		failed(failureStatus, missingObjectResponse),
		failed(failureStatus, missingObjectResponse),
		succeeded("true"),
	)
	stub.answers(t, "run", failed(conflictStatus, conflictResponse))

	_, err := underRecoveryWait(t, time.Minute, time.Millisecond, 10*time.Second)
	if err != nil {
		t.Fatalf("the container was reported as never coming up although the binary that created it "+
			"left one behind for this one to reuse: %v", err)
	}
	if got, want := strings.Join(stub.calls(t), " "), "inspect run inspect inspect"; got != want {
		t.Errorf("the docker calls were %q, want %q: recovering from the refused create means looking "+
			"again, not looking once", got, want)
	}
}

// TestConflictRecoveryRetriesAStartThatFailedInTheWindow covers the other half
// of what the window hides: the winner has created the container but has not
// started it yet, or died between the two, and starting it fails while the
// object is still on its way.
//
// A start that fails there is as transient as a missing object is, and it is
// the round after it that decides whether the container came up — which is this
// case: the start is refused once, the next look finds the container running,
// and the binary uses it.
func TestConflictRecoveryRetriesAStartThatFailedInTheWindow(t *testing.T) {
	stub := newDockerStub(t)
	skipIfStray(t)
	stub.answers(t, "inspect",
		failed(failureStatus, missingObjectResponse),
		succeeded("false"),
		succeeded("true"),
	)
	stub.answers(t, "run", failed(conflictStatus, conflictResponse))
	stub.answers(t, "start", failed(failureStatus, missingContainerResponse))

	_, err := underRecoveryWait(t, time.Minute, time.Millisecond, 10*time.Second)
	if err != nil {
		t.Fatalf("the container was reported as never coming up although it was there to be started: %v", err)
	}
	if got, want := strings.Join(stub.calls(t), " "), "inspect run inspect start inspect"; got != want {
		t.Errorf("the docker calls were %q, want %q: a start refused in the window is a reason to look "+
			"again, not a reason to give up", got, want)
	}
}

// TestConflictRecoveryFailsWithinItsBoundCarryingTheLastWords covers the
// container that never arrives: every look at it fails, and the wait has to end
// by itself with docker's own message in hand.
//
// Both halves are the point. A recovery that waits forever turns a daemon that
// is down into a test run that never finishes, and one that fails with only the
// refused create in hand is what this race cost before: the CI logs said a
// container had not been created and nothing about the look that failed last,
// which is the one thing that says where it stopped.
func TestConflictRecoveryFailsWithinItsBoundCarryingTheLastWords(t *testing.T) {
	stub := newDockerStub(t)
	skipIfStray(t)
	stub.answers(t, "inspect", failed(failureStatus, missingObjectResponse))
	stub.answers(t, "run", failed(conflictStatus, conflictResponse))

	bound := 200 * time.Millisecond
	took, err := underRecoveryWait(t, bound, time.Millisecond, 10*time.Second)
	if err == nil {
		t.Fatal("the container was reported as up although every look at it failed")
	}
	t.Logf("the failure this reports reads: %v", err)
	for _, want := range []string{conflictResponse, missingObjectResponse} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the failure does not carry %q, so a log of it says nothing about what went "+
				"wrong last:\n%v", want, err)
		}
	}
	if looks := stub.counted(t, "inspect"); looks < 3 {
		t.Errorf("docker inspect ran %d times, so the recovery did not look again after a failed "+
			"look: %v", looks, err)
	}
	requireEndedWithinItsBound(t, took, bound)
}

// TestConflictRecoveryReportsCreateAndStartTogether covers the end of the
// window path that led here: the winner created the container, never started
// it, and the restarts do not take either.
//
// The refused create and the refused start are two different failures from two
// different calls, and the report has to carry both — the create is what
// happened first and the start is where the wait actually stopped.
func TestConflictRecoveryReportsCreateAndStartTogether(t *testing.T) {
	stub := newDockerStub(t)
	skipIfStray(t)
	stub.answers(t, "inspect",
		failed(failureStatus, missingObjectResponse),
		succeeded("false"),
	)
	stub.answers(t, "run", failed(conflictStatus, conflictResponse))
	stub.answers(t, "start", failed(failureStatus, missingContainerResponse))

	bound := 200 * time.Millisecond
	took, err := underRecoveryWait(t, bound, time.Millisecond, 10*time.Second)
	if err == nil {
		t.Fatal("the container was reported as up although it was never started")
	}
	t.Logf("the failure this reports reads: %v", err)
	for _, want := range []string{conflictResponse, missingContainerResponse} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the failure does not carry %q:\n%v", want, err)
		}
	}
	requireEndedWithinItsBound(t, took, bound)
}

// TestExistingContainerStartFailureIsReportedAtOnce pins the path that is not
// part of the race, and holds it to staying that way.
//
// A container that is there and not running is started in place, and when that
// fails for good — a stopped container that will not come back — the failure is
// docker's message, now. Waiting on it would hold a lasting failure for as long
// as the wait lasts, and four of this module's packages go through here.
func TestExistingContainerStartFailureIsReportedAtOnce(t *testing.T) {
	stub := newDockerStub(t)
	skipIfStray(t)
	stub.answers(t, "inspect", succeeded("false"))
	stub.answers(t, "start", failed(failureStatus, missingContainerResponse))

	_, err := underRecoveryWait(t, time.Hour, time.Millisecond, 10*time.Second)
	if err == nil {
		t.Fatal("a container that exists and will not start was reported as up")
	}
	if !strings.Contains(err.Error(), missingContainerResponse) {
		t.Errorf("the failure does not carry docker's own message:\n%v", err)
	}
	if got, want := strings.Join(stub.calls(t), " "), "inspect start"; got != want {
		t.Errorf("the docker calls were %q, want %q: nothing here is waiting on another process, so a "+
			"start that fails is reported rather than retried", got, want)
	}
}

// TestAnAbandonedCallIsCutOffFromTheRealDocker covers what has to happen when a
// case gives up on a call: the call is still running, and the one place it must
// not be able to reach from then on is the machine's own docker.
//
// The call it gives up on is one that honors its bound — a few seconds where
// the guard's limit is a fraction of one — which is what makes the case
// deterministic at both ends: the guard gives up long before the call would
// come back, and the call then ends by itself, after which the machine is put
// back the way it was found.
//
// The look is taken after the case that gave up has ended and its cleanup has
// run, and that is the point of taking it from out here: a PATH that a case
// only holds while it runs would look exactly the same from inside it, and that
// is the half of this that is easy to get wrong.
func TestAnAbandonedCallIsCutOffFromTheRealDocker(t *testing.T) {
	previous := os.Getenv("PATH")
	wasAbandoned := abandoned
	var outlived <-chan error
	t.Run("a case that gives up on a call", func(t *testing.T) {
		stub := newDockerStub(t)
		// Every look fails, so nothing the call asks will end it early.
		stub.answers(t, "inspect", failed(failureStatus, missingObjectResponse))
		stub.answers(t, "run", failed(conflictStatus, conflictResponse))

		// The interval is this call's own; the case only needs its bound to be
		// longer than the guard, so there is no reason for it to make a docker
		// invocation every millisecond while it waits the bound out.
		const bound, limit = 2 * time.Second, 100 * time.Millisecond
		run := driveRecovery(bound, 20*time.Millisecond, limit)
		if run.outlived == nil {
			t.Fatalf("the call came back within the %s guard although the bound it was given was %s",
				limit, bound)
		}
		outlived = run.outlived
	})
	if outlived == nil {
		t.Fatal("the case above never gave up on a call, so there is nothing to look at here")
	}

	swallow, err := swallowStubDir()
	if err != nil {
		t.Fatalf("resolving the stand-in for abandoned calls: %v", err)
	}
	if got, err := exec.LookPath("docker"); err != nil || got != filepath.Join(swallow, "docker") {
		t.Errorf("docker resolves to %q (%v) once the case that gave up has ended and its cleanup has "+
			"run, while the call it abandoned is still asking PATH for docker on every round it makes; "+
			"want the stand-in that swallows calls, %q — where that call lands must not be the "+
			"machine's own docker", got, err, filepath.Join(swallow, "docker"))
	}
	if reason := strayEvidence(); reason == "" {
		t.Error("a case that gave up on a call leaves the cases after it nothing to go on: they would " +
			"assert on calls that are not their own and report a defect that is not there")
	}

	// The call was given a bound it honors, so it ends on its own; this waits
	// for that return rather than guessing at how long it takes. The patience
	// is generous on purpose — the rounds are docker invocations, and a loaded
	// machine stretches them. Nothing is put back on the way out: a call that
	// is somehow still out there is precisely what the stand-in and the flag
	// are for, and putting PATH back would point it at the real docker.
	select {
	case <-outlived:
	case <-time.After(30 * time.Second):
		t.Fatal("the call has not come back well past the bound it was given, so the machine is left " +
			"as it is: with that call still asking for docker, putting PATH back is the one thing " +
			"that must not happen")
	}
	// What this puts back is what it found, and only that. A flag an earlier
	// case set belongs to a call that case gave up on, which this one knows
	// nothing about and has not waited for: clearing it here would re-arm the
	// call-sequence assertions of the cases that follow, on top of a call still
	// making rounds and taking the answers they are built on.
	os.Setenv("PATH", previous)
	abandoned = wasAbandoned
}

// TestTheSwallowStandInIsGoneWhenTheRunEnds covers the other end of the
// stand-in for calls a case gave up on: the directory that keeps such calls off
// the real docker has to outlive every case — the call is still asking PATH for
// docker when its case ends — and it has to stop at the run.
//
// Watching that takes a child process. The removal happens after every case has
// ended, when the test binary is on its way out, so nothing in this process can
// be observed doing it: only a process that has actually exited can be looked
// at from the outside, and this case re-executes the test binary for one.
//
// The child runs the case that gives up on a call, which is what makes the
// directory at all, and it is held to having passed it and exited cleanly: a
// case that never got as far as the swallow stand-in would leave the directory
// absent for reasons that say nothing about this. The temporary directory
// underneath the child is one of its own, so what is looked at afterwards is
// nothing else's leftovers.
//
// The second child is the guard on how the first one exits. A test binary exits
// with the status its run gave it, and one that always exited 0 would leave
// every case in this package unable to report a failure — the change this child
// is a check for. So a child running a case that fails at once has to come
// back with that failure in its status, which a child killed by its own bound —
// what a case that hung instead of failing looks like — has not. -test.run
// keeps either child from starting children of its own.
func TestTheSwallowStandInIsGoneWhenTheRunEnds(t *testing.T) {
	// A child is a whole test binary, and the case the first one runs waits out
	// a recovery bound of its own before it can end.
	const childLimit = 2 * time.Minute
	tmp := t.TempDir()
	ctx, cancel := context.WithTimeout(context.Background(), childLimit)
	defer cancel()

	cmd := exec.CommandContext(ctx, os.Args[0], "-test.v", "-test.run=^TestAnAbandonedCallIsCutOffFromTheRealDocker$")
	cmd.Env = []string{"TMPDIR=" + tmp}
	var stdout, stderr strings.Builder
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("the child that gives up on a call came back with %v:\nstdout:\n%s\nstderr:\n%s",
			err, stdout.String(), stderr.String())
	}
	if !strings.Contains(stdout.String(), "--- PASS: TestAnAbandonedCallIsCutOffFromTheRealDocker") {
		t.Fatalf("the child did not pass the case that gives up on a call, so the stand-in this is "+
			"about was never made:\nstdout:\n%s\nstderr:\n%s", stdout.String(), stderr.String())
	}
	entries, err := os.ReadDir(tmp)
	if err != nil {
		t.Fatalf("reading the temporary directory the child ran in: %v", err)
	}
	var left []string
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), "pgtest-swallow-") {
			left = append(left, entry.Name())
		}
	}
	if len(left) > 0 {
		t.Errorf("the child ended and left %v behind: the stand-in for calls a case gave up on is "+
			"made to outlive its case, and not the run", left)
	}

	// The second child fails at once: a peer environment that names the peer
	// without saying how many peers there are.
	cmd = exec.CommandContext(ctx, os.Args[0], "-test.run=^TestColdstartChild$")
	cmd.Env = []string{"TMPDIR=" + tmp, coldstartChildEnv + "=peer-0"}
	var childOut, childErr strings.Builder
	cmd.Stdout, cmd.Stderr = &childOut, &childErr
	err = cmd.Run()
	if err == nil {
		// So that the report below says what came back rather than nothing.
		err = errors.New("exit status 0")
	}
	var exit *exec.ExitError
	if !errors.As(err, &exit) || exit.ExitCode() <= 0 {
		// Reported on standard error and out of the process directly, not
		// through t.Fatalf: a t.Fatalf hands the failure to m.Run's exit status,
		// which is precisely the channel the change this guards against — a
		// TestMain leaving with a status of its own — swallows. Leaving here
		// goes through no part of TestMain, whatever it does with the status,
		// and is the only report in this package that does not.
		fmt.Fprintf(os.Stderr, "the child running a case that fails at once came back with %v, and a test binary "+
			"whose cases can fail leaves the failure in its exit status — one that exits 0 whatever "+
			"happened, or is killed before it can say anything, is one whose cases cannot report "+
			"anything:\nstdout:\n%s\nstderr:\n%s\n", err, childOut.String(), childErr.String())
		os.Exit(1)
	}
}

// TestTheRunsStatusSurvivesTheChangeTheGuardIsFor is the case that notices a
// guard which has stopped guarding. TestTheSwallowStandInIsGoneWhenTheRunEnds
// holds a test binary to leaving a failed case in its exit status, and the
// change it is there for is TestMain taking the status into hands of its own —
// with that in place the guard itself, and every other case in this package,
// goes quiet without anything failing.
//
// A case inside the run cannot see that: whether this binary's failures would
// leave is not a thing it can be asked while it is running. A binary built from
// the changed source can be asked, so that is what this case does — it rewrites
// TestMain's two lines in a copy of this file and gives the copy to the build
// in place of the original, with the build's own -overlay, which leaves the
// checkout untouched.
//
// The mutant is then asked to run the guard. The guard's second child fails at
// once, and in the mutant that child is built from the same changed source, so
// its failure goes through the mutant's own TestMain and is swallowed — which
// is the change — and the guard reaches its report. Fixed, the report leaves
// directly: the mutant's exit status is non-zero and the diagnostic is on its
// standard error. In the source the guard was fixed from — t.Fatalf — the
// report goes into the same swallowed status, and the mutant leaves 0 with the
// diagnostic on standard output instead. Those two things are how the runs are
// told apart, and they are what makes this case red before the fix and green
// after.
//
// The copy is the whole file, this case's own source included, so the two lines
// are written here in pieces that never join: joined, they would be in the copy
// twice — once in TestMain and once in this case's source — and the count
// below, which is what says the rewrite has a line to land on, would be
// counting this case's own text as much as TestMain's. What is checked is the
// file the copy is made from: each piece, joined, has to appear exactly once,
// and a file where it does not is a failure that says the shape has moved and
// what to update. A mutant that was never mutated, passing for some other
// reason, is what that check is for.
//
// The words looked for on the mutant's standard error are the guard's own
// diagnostic, and the coupling is deliberate: that the diagnostic keeps
// reaching the outside world is the whole of what the guard is for, so
// rewording it should bring this case along rather than be absorbed by a weaker
// check like "standard error is not empty", which could not say what it read.
func TestTheRunsStatusSurvivesTheChangeTheGuardIsFor(t *testing.T) {
	// The toolchain is found without PATH: cases in this package move PATH
	// around on purpose (see TestAnAbandonedCallIsCutOffFromTheRealDocker), and
	// the build below is not a thing to look up through whatever PATH holds at
	// this point. A toolchain that cannot be found fails this case; it is not a
	// reason to skip it.
	goTool := filepath.Join(build.Default.GOROOT, "bin", "go")
	if _, err := os.Stat(goTool); err != nil {
		t.Fatalf("the go toolchain of this build is expected at %s and is not there (%v): the "+
			"mutant is built with it, and a build that cannot be started is this case's own failure",
			goTool, err)
	}
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok || !filepath.IsAbs(thisFile) {
		t.Fatalf("this case cannot name the source it rewrites: the file it sits in was reported as "+
			"%q, which is not an absolute path to a file", thisFile)
	}
	pkgDir := filepath.Dir(thisFile)
	source, err := os.ReadFile(thisFile)
	if err != nil {
		t.Fatalf("reading %s, the source of the mutant: %v", thisFile, err)
	}

	// The two lines the change rewrites, in pieces that never join in this
	// case's source, and what each is, for the report when it is not there.
	runLine := "code := m." + "Run()"
	exitLine := "os.Exit(" + "code)"
	for _, line := range []struct{ text, what string }{
		{runLine, "the line taking the run's status into a variable"},
		{exitLine, "the line handing that status to the exit"},
	} {
		if n := strings.Count(string(source), line.text); n != 1 {
			t.Fatalf("%s, which the mutant rewrites in TestMain, appears %d times in %s where "+
				"exactly one is expected: TestMain's shape has moved, and this case has to be updated "+
				"to rewrite the new one — left as it is, the mutant below would be no mutant at all "+
				"and this case would pass or fail for a reason other than the guard",
				line.what, n, filepath.Base(thisFile))
		}
	}
	mutated := strings.Replace(string(source), runLine, "m."+"Run()", 1)
	mutated = strings.Replace(mutated, exitLine, "os.Exit(0)", 1)

	dir := t.TempDir()
	mutantSource := filepath.Join(dir, "recovery_test.go")
	err = os.WriteFile(mutantSource, []byte(mutated), 0o644)
	if err != nil {
		t.Fatalf("writing the source of the mutant to %s: %v", mutantSource, err)
	}
	overlay, err := json.Marshal(map[string]map[string]string{"Replace": {thisFile: mutantSource}})
	if err != nil {
		t.Fatalf("encoding the overlay that puts %s in place of %s for the build: %v",
			mutantSource, thisFile, err)
	}
	overlayPath := filepath.Join(dir, "overlay.json")
	err = os.WriteFile(overlayPath, overlay, 0o644)
	if err != nil {
		t.Fatalf("writing the overlay to %s: %v", overlayPath, err)
	}

	// One bound for each of the two steps below, so that neither a build nor a
	// run that hangs can hold this case open. Both are seconds against it.
	const mutantLimit = 3 * time.Minute
	buildCtx, cancelBuild := context.WithTimeout(context.Background(), mutantLimit)
	defer cancelBuild()
	mutant := filepath.Join(dir, "mutant.test")
	build := exec.CommandContext(buildCtx, goTool, "test", "-c", "-overlay="+overlayPath, "-o", mutant, ".")
	build.Dir = pkgDir
	var buildOut, buildErr strings.Builder
	build.Stdout, build.Stderr = &buildOut, &buildErr
	err = build.Run()
	if err != nil {
		t.Fatalf("building the mutant came back with %v, which says nothing about the guard: a build "+
			"that does not come out is this case's own failure and not the mutant being caught:\n"+
			"stdout:\n%s\nstderr:\n%s", err, buildOut.String(), buildErr.String())
	}

	// Run directly rather than through go test: go test merges the binary's
	// standard error into its own output, and which stream the diagnostic is on
	// is half of what this case is about. -test.run keeps the mutant off this
	// case, which is in the same binary and would build a mutant of its own.
	runCtx, cancelRun := context.WithTimeout(context.Background(), mutantLimit)
	defer cancelRun()
	run := exec.CommandContext(runCtx, mutant, "-test.run=^TestTheSwallowStandInIsGoneWhenTheRunEnds$")
	// The mutant's temporary files are put under this case's. The guard in there
	// ends by leaving directly — the mutant working as it should — and a process
	// that leaves directly takes none of its cleanup with it, so what it leaves
	// would collect in the machine's temporary directory on every run of this
	// suite. TMPDIR is replaced rather than added to, so that the entry this case
	// hands the mutant is the only one it can read.
	run.Env = append(slices.DeleteFunc(os.Environ(), func(kv string) bool {
		return strings.HasPrefix(kv, "TMPDIR=")
	}), "TMPDIR="+t.TempDir())
	var stdout, stderr strings.Builder
	run.Stdout, run.Stderr = &stdout, &stderr
	err = run.Run()
	if err == nil {
		// So that the report below says what came back rather than nothing.
		err = errors.New("exit status 0")
	}
	var exit *exec.ExitError
	if !errors.As(err, &exit) || exit.ExitCode() <= 0 {
		t.Fatalf("the mutant ran the guard and came back with %v. The guard has to notice a TestMain "+
			"leaving with a status of its own and reach the outside world doing it, so a mutant "+
			"leaving 0 is a guard whose report stayed inside it — or a mutant that was never built "+
			"from the changed source:\nstdout:\n%s\nstderr:\n%s",
			err, stdout.String(), stderr.String())
	}
	const diagnostic = "leaves the failure in its exit status"
	if !strings.Contains(stderr.String(), diagnostic) {
		t.Fatalf("the mutant left with %v, but its standard error does not carry the guard's "+
			"diagnostic (%q): what the guard finds has to be readable from outside the run, which is "+
			"what leaving with it in the exit status is for:\nstdout:\n%s\nstderr:\n%s",
			err, diagnostic, stdout.String(), stderr.String())
	}
}
