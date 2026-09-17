package pgtest

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
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
const (
	// conflictResponse is the refusal of a second container under the shared
	// name. docker leaves 125 for it.
	conflictResponse = `docker: Error response from daemon: Conflict. The container name "/speed-pgtest" is already in use by container "5b8f0c2a91d4".`
	// missingObjectResponse is what inspect answers in the window.
	missingObjectResponse = `Error: No such object: speed-pgtest`
	// missingContainerResponse is what start answers in the window.
	missingContainerResponse = `Error: No such container: speed-pgtest`
	// conflictStatus and failureStatus are the exit statuses that come with
	// them: docker's own for a refused create, and the CLI's for a command
	// that could not be carried out.
	conflictStatus = 125
	failureStatus  = 1
)

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
// The environment is set for the case and restored when it ends, so the cases
// in this package that do use the real container are unaffected by any of this.
func newDockerStub(t *testing.T) *dockerStub {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "docker"), []byte(dockerStubScript), 0o755); err != nil {
		t.Fatalf("planting the docker stand-in: %v", err)
	}
	t.Setenv("PATH", dir)
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

// withRecoveryPace shrinks the wait that follows a lost create for the length
// of one case, so that the case can reach the end of it and assert what is
// reported there. The production values are put back when the case ends.
func withRecoveryPace(t *testing.T, bound, interval time.Duration) {
	t.Helper()
	previousBound, previousInterval := recoveryBound, recoveryInterval
	recoveryBound, recoveryInterval = bound, interval
	t.Cleanup(func() {
		recoveryBound, recoveryInterval = previousBound, previousInterval
	})
}

// callWithin runs ensureContainer and fails the case when it has not come back
// within limit.
//
// A recovery that keeps waiting is the failure mode this whole check is about
// — a daemon that is down has to end in a reported failure, not in a test run
// that never finishes — and a case that waits for it would take the suite's own
// timeout to say so.
func callWithin(t *testing.T, limit time.Duration) (time.Duration, error) {
	t.Helper()
	started := time.Now()
	done := make(chan error, 1)
	go func() { done <- ensureContainer() }()
	select {
	case err := <-done:
		return time.Since(started), err
	case <-time.After(limit):
		t.Fatalf("bringing the container up has not come back within %s, and a create that cannot be "+
			"recovered has to end in a failure within the wait it was given (%s)", limit, recoveryBound)
		return 0, nil
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
	withRecoveryPace(t, time.Minute, time.Millisecond)
	stub.answers(t, "inspect",
		failed(failureStatus, missingObjectResponse),
		failed(failureStatus, missingObjectResponse),
		succeeded("true"),
	)
	stub.answers(t, "run", failed(conflictStatus, conflictResponse))

	_, err := callWithin(t, 10*time.Second)
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
	withRecoveryPace(t, time.Minute, time.Millisecond)
	stub.answers(t, "inspect",
		failed(failureStatus, missingObjectResponse),
		succeeded("false"),
		succeeded("true"),
	)
	stub.answers(t, "run", failed(conflictStatus, conflictResponse))
	stub.answers(t, "start", failed(failureStatus, missingContainerResponse))

	_, err := callWithin(t, 10*time.Second)
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
	withRecoveryPace(t, 200*time.Millisecond, time.Millisecond)
	stub.answers(t, "inspect", failed(failureStatus, missingObjectResponse))
	stub.answers(t, "run", failed(conflictStatus, conflictResponse))

	took, err := callWithin(t, 10*time.Second)
	if err == nil {
		t.Fatal("the container was reported as up although every look at it failed")
	}
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
	if took > time.Minute {
		t.Errorf("the failure took %s, and the wait it was given was %s: it has to end when that "+
			"wait ends", took, recoveryBound)
	}
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
	withRecoveryPace(t, 200*time.Millisecond, time.Millisecond)
	stub.answers(t, "inspect",
		failed(failureStatus, missingObjectResponse),
		succeeded("false"),
	)
	stub.answers(t, "run", failed(conflictStatus, conflictResponse))
	stub.answers(t, "start", failed(failureStatus, missingContainerResponse))

	took, err := callWithin(t, 10*time.Second)
	if err == nil {
		t.Fatal("the container was reported as up although it was never started")
	}
	for _, want := range []string{conflictResponse, missingContainerResponse} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the failure does not carry %q:\n%v", want, err)
		}
	}
	if took > time.Minute {
		t.Errorf("the failure took %s, and the wait it was given was %s", took, recoveryBound)
	}
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
	// The wait is set far beyond what the case allows the call: if this path
	// ever picks up the recovery's retries, the case fails on the time rather
	// than on a number of calls that a loaded machine could vary.
	withRecoveryPace(t, time.Hour, time.Millisecond)
	stub.answers(t, "inspect", succeeded("false"))
	stub.answers(t, "start", failed(failureStatus, missingContainerResponse))

	_, err := callWithin(t, 10*time.Second)
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
