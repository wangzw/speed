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
	// is how many of them have to have covered the window before it stops
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
	// coldstartAcquiredMarker is the line a peer prints once Acquire has handed
	// it a database. It is what tells a peer that came up from one whose
	// acquisition skipped: both leave an exit status of 0, and the skip —
	// docker unreachable, whatever the reason — is exactly the outcome a round
	// must not count as a peer that reached the container.
	coldstartAcquiredMarker = "pgtest-coldstart: acquired"
	// coldstartSkipTailLines is how much of a peer's output a report carries
	// when the peer skipped and its output holds no reason: enough for the last
	// thing it said, not enough to paste a test log into a round's summary.
	coldstartSkipTailLines = 5
)

// coldstartShimScript is the docker each peer finds first on its PATH.
//
// It records what it was asked and what came back, then does it — through the
// real docker, by the absolute path substituted for the placeholder below. It
// copies nothing of the code under test and delays nothing, so what it writes
// is the race as it happened.
//
// The newlines in a call's standard error become spaces, because the log is one
// entry per line and a reader that keeps only the prefixed line keeps only the
// first line docker wrote. docker puts the thing that went wrong last: a create
// refused for a name already taken says so underneath however many lines the
// image pull took, and this round removes the image before every round, so that
// is the ordinary case rather than the odd one. Keeping the first line alone
// left every refused create unrecognisable.
//
// The log is one per peer, beside the script, and that is the point of it: what
// the window answers a look with and what a first look at a container that has
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
	printf 'stderr: %s\n' "$(tr '\n' ' ' < "$err")"
	echo ---
} >>"$log"
cat "$err" >&2
exit "$status"
`

// shimRealDocker is the placeholder the real docker's path replaces.
const shimRealDocker = "REAL_DOCKER"

// peerRun is what one peer did in one round.
type peerRun struct {
	role string
	exit int
	// acquired is the marker the peer printed once Acquire handed it a
	// database: the one thing in this account that tells a peer which came up
	// from a peer whose acquisition skipped, since their exit statuses agree.
	acquired bool
	stdout   string
	stderr   string
	calls    []dockerCall
	startErr error
}

// peerOutcome is what one peer's run came to.
type peerOutcome int

const (
	// peerConverged is a peer that acquired a database: it exited cleanly and
	// left the marker behind saying so.
	peerConverged peerOutcome = iota
	// peerSkipped is a peer whose acquisition skipped: the same clean exit
	// status, and no marker. A peer that cannot reach the daemon never enters
	// the race at all, and a harness reading only the status would report it as
	// a peer that came up on the container.
	peerSkipped
	// peerFailed is a peer that did not come up and said so with its exit
	// status. Under CI a skip arrives here instead: Acquire fails rather than
	// skips there.
	peerFailed
)

// String names the outcome the way a round's output refers to it.
func (o peerOutcome) String() string {
	switch o {
	case peerConverged:
		return "came up on the container"
	case peerSkipped:
		return "skipped"
	case peerFailed:
		return "failed"
	}
	return fmt.Sprintf("peerOutcome(%d)", int(o))
}

// outcome is what this peer's run came to.
func (r peerRun) outcome() peerOutcome {
	switch {
	case r.exit != 0:
		return peerFailed
	case r.acquired:
		return peerConverged
	default:
		return peerSkipped
	}
}

// skipExplanation is what a peer's output says about a run that neither
// acquired nor failed: the reason a skipped case printed, or, where there is
// none, the tail of what the peer did print.
func (r peerRun) skipExplanation() string {
	if reason := skipReason(r.stdout); reason != "" {
		return reason
	}
	lines := strings.Split(strings.TrimSpace(r.stdout+"\n"+r.stderr), "\n")
	if len(lines) > coldstartSkipTailLines {
		lines = lines[len(lines)-coldstartSkipTailLines:]
	}
	return "no reason in its output, which ended with: " + strings.Join(lines, " / ")
}

// peerAcquired reports whether a peer's output is one that holds a database:
// the harness reads this off the line the peer prints once Acquire returns, and
// a case that runs a peer reads it there too, so that a change to how the
// outcome is read is a change in one place.
func peerAcquired(stdout string) bool {
	return strings.Contains(stdout, coldstartAcquiredMarker)
}

// skipReason reads the reason out of a peer's output when a skipped case left
// one there. The case prints it as a log line just ahead of the summary line
// that names the skip, indented as far as that log line is.
func skipReason(stdout string) string {
	lines := strings.Split(stdout, "\n")
	for index, line := range lines {
		if !strings.HasPrefix(line, "--- SKIP:") {
			continue
		}
		var reason []string
		for index--; index >= 0; index-- {
			if !strings.HasPrefix(lines[index], "    ") {
				break
			}
			reason = append([]string{strings.TrimSpace(lines[index])}, reason...)
		}
		return strings.Join(reason, " ")
	}
	return ""
}

// roundSummary is one round of peers classified and counted.
type roundSummary struct {
	converged int
	skipped   int
	failed    int
	// reached holds the roles that lost the create race and were then told by
	// their own calls that the container was not there.
	reached []string
}

// summarizeRound classifies the peers of one round and adds them up.
func summarizeRound(runs []peerRun) roundSummary {
	var summary roundSummary
	for _, run := range runs {
		switch run.outcome() {
		case peerConverged:
			summary.converged++
		case peerSkipped:
			summary.skipped++
		case peerFailed:
			summary.failed++
		}
		if run.hitWindow() {
			summary.reached = append(summary.reached, run.role)
		}
	}
	return summary
}

// coversWindow is whether this round is one that says anything about the
// window: a peer reached it, and no peer's outcome is anything but coming up.
//
// A round with a skipped peer is not one. A peer that skipped was never in the
// race, and counting the round anyway would put the summary's "every peer in
// them came up on the one container" behind a round it is not true of. A round
// with a failed peer is not one either, for the plainer reason that the run
// reports it as a failure.
func (s roundSummary) coversWindow() bool {
	return len(s.reached) > 0 && s.skipped == 0 && s.failed == 0
}

// runTotals is one run of the check added up, and what its conclusion is drawn
// from: how many rounds were run, how many of them had a peer reach the window,
// how many the run may claim as covered — a round covers the window when a peer
// reached it and none of the round's peers skipped or failed — and how many
// peers never came up at all.
//
// The two counts are apart because they are apart in fact: a round can be one
// that reached the window and still not be one the run may claim, and the
// summary that said the one while holding the other was claiming coverage
// behind rounds it was not true of.
type runTotals struct {
	rounds   int
	reached  int
	covered  int
	failures int
}

// conclusionKind is which of the four things a run has to say about itself.
type conclusionKind int

const (
	// conclusionFailed: a peer could not reach the container, which the run
	// reports as a failure however much of the window it saw.
	conclusionFailed conclusionKind = iota
	// conclusionNeverReached: no round had a peer reach the window, so the run
	// covers nothing about the recovery.
	conclusionNeverReached
	// conclusionReachedNotCovered: rounds reached the window, and none of them
	// is one the run may claim — peers skipped instead of racing.
	conclusionReachedNotCovered
	// conclusionCovered: rounds covered the window, and every peer in them came
	// up on the one container.
	conclusionCovered
)

// conclusion is what a run has to say, and which of the four things it is: the
// harness reads the kind to decide how the run reports it — an error, a skip or
// a log line — and hands the sentence over as it stands.
type conclusion struct {
	kind     conclusionKind
	sentence string
}

// conclusion draws the run's conclusion from its totals.
//
// It is a function of the totals and nothing else, here rather than at the
// harness's closing switch, so that what a state concludes — and every word of
// how it says it — is a thing a case can hold from outside: the counts and the
// sentences went out of step once already, with the covered rounds counted and
// the sentence about reaching, and nothing in a harness that builds its
// sentences as it goes could say so.
func (totals runTotals) conclusion() conclusion {
	switch {
	case totals.failures > 0:
		return conclusion{conclusionFailed, fmt.Sprintf(
			"%d of the peers in these %d rounds could not reach the container, and a create that cannot "+
				"be recovered has to end in a reported failure rather than in a peer without a database",
			totals.failures, totals.rounds)}
	case totals.reached == 0:
		return conclusion{conclusionNeverReached, fmt.Sprintf(
			"none of the %d rounds reached the window: no peer lost the create race and was then told by "+
				"its own calls that the container was not there, so this run covers nothing about the "+
				"recovery. The deterministic cases are the gate; this check says the answers they replay "+
				"are the real ones, and it did not get to say it", totals.rounds)}
	case totals.covered == 0:
		return conclusion{conclusionReachedNotCovered, fmt.Sprintf(
			"the window was reached in %d of the %d rounds, but no round covered it: the rounds that "+
				"reached it had peers skip instead of racing, and a round counts as covered only when "+
				"all of its peers came up on the one container. The deterministic cases are the gate; "+
				"this check says the answers they replay are the real ones, and it did not get to say it",
			totals.reached, totals.rounds)}
	default:
		return conclusion{conclusionCovered, fmt.Sprintf(
			"%d of %d rounds covered the window, and every peer in them came up on the one container",
			totals.covered, totals.rounds)}
	}
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

// answeredWith reports whether an answer carries the part of it that says what
// is wrong, with letter case folded away. Case is not docker's to keep stable:
// the same refusal comes back capitalized from one version of the CLI and
// lower-cased from another, and both are the same answer.
func answeredWith(stderr, words string) bool {
	return strings.Contains(strings.ToLower(stderr), strings.ToLower(words))
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
// recovery makes, and it has to be the answer that call is given in the window:
// a failed look says there is no such object, a failed start says there is no
// such container. A non-zero status on its own is not the window — a daemon
// that hiccuped says something else — and counting one as the window would have
// a round claim coverage it never had, which is the same defect as counting a
// failure from some other call.
//
// The words are matched by containment with letter case folded away, and what
// each is matched against is only the part of that answer which says what is
// wrong; the answer itself is everything that call wrote to standard error, on
// one line. The machine's own docker is held to the words by
// TestWhatDockerSaysAboutAMissingContainerIsWhatTheWindowAsksFor. A docker CLI
// that words either of them differently — another phrase, another sentence —
// still has that half of this never match, the round reported as one that did
// not reach the window; letter case is the one difference tolerated. That is
// the safe direction — an answer this does not recognize costs the round a
// coverage claim it cannot show, and never makes one.
func (r peerRun) hitWindow() bool {
	conflicted := false
	for _, call := range r.calls {
		if conflicted {
			switch {
			case call.sub == "inspect" && call.status != 0 && answeredWith(call.stderr, missingObjectResponse):
				return true
			case call.sub == "start" && call.status != 0 && answeredWith(call.stderr, missingContainerResponse):
				return true
			}
		}
		// The casing is read here, unlike the two halves above. docker
		// capitalises the refusal of a taken name, while "conflicting
		// options" and its kind do not, and folding the case would let
		// those set this gate: a round would then count a window it
		// never entered, which is the one direction this may not go.
		if call.sub == "run" && call.status == conflictStatus && strings.Contains(call.stderr, conflictMarker) {
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

	var rounds, reachedRounds, coveredRounds, failures, skips int
	var skipped []string
	for round := 1; round <= coldstartRounds && coveredRounds < coldstartWindows; round++ {
		rounds = round
		coldStart(t, dockerPath, round)
		runs := runPeers(t, peerBinary, dockerPath, root, round)
		summary := summarizeRound(runs)
		if len(summary.reached) > 0 {
			reachedRounds++
		}

		for _, run := range runs {
			switch run.outcome() {
			case peerFailed:
				failures++
				t.Errorf("round %d: %s came up on nothing after the container was created for it:\nstdout:\n%s\nstderr:\n%s",
					round, run.role, run.stdout, run.stderr)
			case peerSkipped:
				skips++
				skipped = append(skipped, fmt.Sprintf("round %d %s: %s", round, run.role, run.skipExplanation()))
			}
			t.Logf("round %d: %s exited %d: %s", round, run.role, run.exit, run.timeline())
		}
		switch {
		case summary.coversWindow():
			coveredRounds++
			t.Logf("round %d: the window was reached by %s, and every peer in it came up on the one container",
				round, strings.Join(summary.reached, ", "))
		case len(summary.reached) > 0 && summary.skipped > 0:
			t.Logf("round %d: the window was reached by %s, but %d of the %d peers skipped instead of "+
				"racing, so the round is not one that covered it", round, strings.Join(summary.reached, ", "),
				summary.skipped, len(runs))
		}
		// A round that failed is not one to repeat: the next round starts from
		// the same place and the code has not changed. Stopping here also
		// leaves the log of the round that failed at the end of the output.
		if failures > 0 {
			break
		}
	}

	if skips > 0 {
		t.Logf("%d of the %d peers that ran skipped and are counted as neither coming up nor failing: %s",
			skips, rounds*coldstartPeers, strings.Join(skipped, "; "))
	}
	// The run ends on the conclusion its totals draw, and says exactly what
	// that conclusion says: the sentences are the conclusion's, handed over as
	// they stand, so that what a state says about itself is held by the cases
	// around this one rather than by the shape of this switch.
	verdict := runTotals{
		rounds:   rounds,
		reached:  reachedRounds,
		covered:  coveredRounds,
		failures: failures,
	}.conclusion()
	switch verdict.kind {
	case conclusionFailed:
		t.Error(verdict.sentence)
	case conclusionNeverReached, conclusionReachedNotCovered:
		t.Skip(verdict.sentence)
	case conclusionCovered:
		t.Log(verdict.sentence)
	default:
		// A kind added to the set and not given an arm here would leave
		// the run saying nothing at all about the race and passing, which
		// is the one outcome this check must not be able to reach.
		t.Fatalf("the run concluded kind %d, which nothing here reports: %s",
			verdict.kind, verdict.sentence)
	}
}

// TestColdstartChild is the peer half of the check. The harness starts the
// package's test binary once per peer with this case selected, and this is what
// each of them runs: meet the others, then take a database the way any case
// does.
//
// It does that one thing so that a peer's exit status is the outcome of one
// acquisition and nothing else — which is what makes a failure of it a thing
// the round can report. What the status cannot say is whether the acquisition
// happened at all: a skip leaves the same clean status behind, so a peer also
// says in its own words when it holds a database.
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
	// On standard output, not through the test log: the harness reads this from
	// outside the process, and the marker reaches it however verbosely the peer
	// was run. The -test.v the harness passes is for the skip reasons, and under
	// it a passing case's log is printed too — a marker that went to the log
	// would be tied to that. A peer whose acquisition skipped leaves no marker,
	// and that absence is all the harness needs to tell the two apart.
	fmt.Fprintln(os.Stdout, coldstartAcquiredMarker)
}

// TestAColdstartPeerThatSkipsLeavesNoMarker covers the end of the marker
// protocol the harness reads: a peer left without a database says nothing that
// could be taken for one.
//
// The classification cases feed the marker in as an input; this one puts it
// there, by running a real peer against a stand-in that cannot answer docker
// info, which is what an unreachable daemon looks like to Acquire. What has to
// come back is a peer that exited cleanly, printed no marker, and left its
// reason in its output. A marker printed by a peer that never got a database is
// what lets a round count a skip as one that came up, which is the defect the
// marker was added for.
//
// The peer is this test binary, re-run; the harness builds a separate one so
// that it can point it at a checkout of the code before a fix, and what this
// case is about is the binary under test. Its environment is built here rather
// than through peerEnvironment, with one peer at the rendezvous: the count is
// not what this is checking, and the CI flag the harness carries over would
// turn the skip it is looking for into a failure.
func TestAColdstartPeerThatSkipsLeavesNoMarker(t *testing.T) {
	stub := newDockerStub(t)
	skipIfStray(t)
	ctx, cancel := context.WithTimeout(context.Background(), coldstartRendezvousTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.v", "-test.run=^TestColdstartChild$")
	cmd.Env = []string{
		"PATH=" + stub.dir,
		coldstartChildEnv + "=peer-0",
		coldstartRendezvousEnv + "=" + t.TempDir(),
		coldstartPeersEnv + "=1",
	}
	var stdout, stderr strings.Builder
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("the peer came back with %v, and an acquisition that cannot reach the daemon "+
			"skips:\nstdout:\n%s\nstderr:\n%s", err, stdout.String(), stderr.String())
	}

	if peerAcquired(stdout.String()) {
		t.Errorf("the peer left the marker behind although its acquisition skipped, so a round would "+
			"count it as a peer that came up on the container:\n%s", stdout.String())
	}
	if reason := skipReason(stdout.String()); reason == "" {
		t.Errorf("the peer skipped without a reason its output can be read for, so a round reporting "+
			"the skip has nothing to say about it:\n%s", stdout.String())
	}
	if calls := stub.calls(t); len(calls) == 0 || calls[0] != "info" {
		t.Errorf("the stand-in was asked for %v, and the first thing a peer does with an unreachable "+
			"daemon is ask docker about it: what this peer skipped on was not docker", calls)
	}
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
// The environment comes from peerEnvironment rather than being inherited, so
// that nothing on the machine running the check can change what a peer sees.
// The case runs verbosely because the reason a skip gives is a log line, and a
// skip is an outcome the round has to be able to report.
func runPeer(peerBinary, shimDir, rendezvous, role string) peerRun {
	ctx, cancel := context.WithTimeout(context.Background(), coldstartRoundLimit)
	defer cancel()
	cmd := exec.CommandContext(ctx, peerBinary, "-test.v", "-test.run=^TestColdstartChild$")
	cmd.Env = peerEnvironment(shimDir, rendezvous, role, os.Environ())
	var stdout, stderr strings.Builder
	cmd.Stdout, cmd.Stderr = &stdout, &stderr

	run := peerRun{role: role}
	err := cmd.Run()
	run.stdout, run.stderr = stdout.String(), stderr.String()
	run.acquired = peerAcquired(run.stdout)
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

// peerEnvironment assembles the environment one peer runs with out of the
// environment the harness itself runs in.
//
// It is built rather than inherited, so that nothing on the machine can change
// what a peer sees: PATH begins with that peer's own shim, and the names below
// are the whole of what is carried over. What is carried is what a peer needs
// to reach the same daemon the harness reaches — a machine can keep its docker
// socket, its client configuration and its home anywhere, and a peer that
// cannot reach the daemon skips out of the race rather than running it, which
// is one outcome a round must never cover up. The CI flag is carried for the
// reason it always was: it decides whether a peer may skip at all.
func peerEnvironment(shimDir, rendezvous, role string, host []string) []string {
	path, _ := hostEnvironment(host, "PATH")
	env := []string{
		"PATH=" + shimDir + string(os.PathListSeparator) + path,
		coldstartChildEnv + "=" + role,
		coldstartRendezvousEnv + "=" + rendezvous,
		coldstartPeersEnv + "=" + strconv.Itoa(coldstartPeers),
	}
	// Carried when the harness has them and absent when it does not: a peer
	// handed an empty HOME or DOCKER_HOST reads it as one set to nothing, which
	// is not the machine the harness is running on.
	for _, name := range []string{"CI", "HOME", "DOCKER_HOST", "DOCKER_CONFIG", "DOCKER_CONTEXT"} {
		if value, present := hostEnvironment(host, name); present {
			env = append(env, name+"="+value)
		}
	}
	return env
}

// hostEnvironment reads one name out of an environment list, and says whether
// it is there at all: a name set to nothing and a name that is not set are not
// the same thing to the code that reads it.
func hostEnvironment(host []string, name string) (string, bool) {
	for _, entry := range host {
		if value, found := strings.CutPrefix(entry, name+"="); found {
			return value, true
		}
	}
	return "", false
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

// TestHitWindowAsksForTheWindowsOwnAnswers holds the coverage claim to what
// the window actually says: the two messages docker answers a look and a start
// with while the name is taken and the object is not there yet.
//
// A non-zero status on its own is not the window. A daemon that hiccuped, a
// start that failed for a reason of its own — counting either as the window
// lets a round record coverage it never had, and the conclusion that the
// deterministic cases replay real answers is then behind a round where nothing
// raced at all. The answers are paired with the calls they belong to, since
// each is what its own subcommand is answered with in the window.
func TestHitWindowAsksForTheWindowsOwnAnswers(t *testing.T) {
	conflict := dockerCall{sub: "run", status: conflictStatus, stderr: conflictResponse}
	for _, c := range []struct {
		name  string
		calls []dockerCall
		want  bool
	}{
		{
			"a look the window answers",
			[]dockerCall{conflict, {sub: "inspect", status: failureStatus, stderr: missingObjectResponse}},
			true,
		},
		{
			"a look the window answers in lower case",
			[]dockerCall{conflict, {
				sub: "inspect", status: failureStatus,
				stderr: "error: no such object: speed-pgtest",
			}},
			true,
		},
		{
			"a look the window answers in upper case",
			[]dockerCall{conflict, {
				sub: "inspect", status: failureStatus,
				stderr: "Error: No such object: speed-pgtest",
			}},
			true,
		},
		{
			"a start the window answers",
			[]dockerCall{conflict, {sub: "start", status: failureStatus, stderr: missingContainerResponse}},
			true,
		},
		// The wording here is written out rather than measured: this machine's
		// docker answers a start in upper case. Without this case no observation
		// separates a fix that folds case on the look alone from one that folds
		// it on both halves.
		{
			"a start the window answers in lower case",
			[]dockerCall{conflict, {
				sub: "start", status: failureStatus,
				stderr: "error response from daemon: no such container: speed-pgtest",
			}},
			true,
		},
		{
			"a daemon that hiccuped on a look",
			[]dockerCall{conflict, {
				sub: "inspect", status: failureStatus,
				stderr: "Cannot connect to the Docker daemon at unix:///var/run/docker.sock. Is the docker daemon running?",
			}},
			false,
		},
		{
			"a daemon that hiccuped on a start",
			[]dockerCall{conflict, {
				sub: "start", status: failureStatus,
				stderr: `docker: Error response from daemon: OCI runtime create failed: exec: "docker-entrypoint.sh": executable file not found in $PATH`,
			}},
			false,
		},
		{
			"a look answered with the start's answer",
			[]dockerCall{conflict, {sub: "inspect", status: failureStatus, stderr: missingContainerResponse}},
			false,
		},
		{
			"a look answered with the daemon's refusal of a container",
			[]dockerCall{conflict, {
				sub: "inspect", status: failureStatus,
				stderr: "Error response from daemon: No such container: speed-pgtest",
			}},
			false,
		},
		{
			"the window's answer before this peer lost the race",
			[]dockerCall{{sub: "inspect", status: failureStatus, stderr: missingObjectResponse}, conflict},
			false,
		},
		{
			"a failure from a call the recovery does not make",
			[]dockerCall{conflict, {sub: "port", status: failureStatus, stderr: missingObjectResponse}},
			false,
		},
		{
			"a peer that never lost the race",
			[]dockerCall{{sub: "inspect", status: failureStatus, stderr: missingObjectResponse}},
			false,
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			run := peerRun{calls: c.calls}
			if got := run.hitWindow(); got != c.want {
				t.Errorf("the peer is counted as having reached the window: %v, want %v", got, c.want)
			}
		})
	}
}

// TestPeerOutcomeTellsASkipFromComingUp holds the one thing the harness could
// not tell apart: a peer whose acquisition skipped leaves the same exit status
// behind as a peer that came up.
//
// A run that reads the status alone counts every peer whose docker was
// unreachable as one that reached the container — eight of eight, on the
// strength of a status that nothing had qualified.
func TestPeerOutcomeTellsASkipFromComingUp(t *testing.T) {
	for _, c := range []struct {
		name string
		run  peerRun
		want peerOutcome
	}{
		{"a peer that holds a database", peerRun{exit: 0, acquired: true}, peerConverged},
		{"a peer whose acquisition skipped", peerRun{exit: 0}, peerSkipped},
		{"a peer that came up on nothing", peerRun{exit: 1}, peerFailed},
		{"a peer that failed after acquiring", peerRun{exit: 1, acquired: true}, peerFailed},
	} {
		t.Run(c.name, func(t *testing.T) {
			if got := c.run.outcome(); got != c.want {
				t.Errorf("the peer's outcome is %s, want %s", got, c.want)
			}
		})
	}
}

// TestSkipReasonReadsWhatASkippedPeerSaid holds the reading of a peer's output
// to the shapes it comes in: a skip that printed its reason — the reason is a
// log line, and it is printed ahead of the summary line — and a peer that left
// none, whose account is the tail of what it printed.
func TestSkipReasonReadsWhatASkippedPeerSaid(t *testing.T) {
	// One peer run on a machine whose daemon the shim answered as unreachable,
	// with CI unset: exit 0, no marker, and this.
	stdout := "=== RUN   TestColdstartChild\n" +
		"    coldstart_test.go:409: the docker daemon is not reachable, so the PostgreSQL cases cannot " +
		"run: docker info --format {{.ServerVersion}}: exit status 1: Cannot connect to the Docker " +
		"daemon at unix:///var/run/docker.sock\n" +
		"--- SKIP: TestColdstartChild (0.00s)\nPASS\n"
	want := "coldstart_test.go:409: the docker daemon is not reachable, so the PostgreSQL cases cannot " +
		"run: docker info --format {{.ServerVersion}}: exit status 1: Cannot connect to the Docker " +
		"daemon at unix:///var/run/docker.sock"
	if got := skipReason(stdout); got != want {
		t.Errorf("the skip reason reads:\n%s\nwant:\n%s", got, want)
	}
	if got := skipReason("=== RUN   TestColdstartChild\n--- SKIP: TestColdstartChild (0.00s)\nPASS\n"); got != "" {
		t.Errorf("a skip that printed no reason of its own reads as %q, want nothing", got)
	}

	run := peerRun{stdout: "one\ntwo\nthree\nfour\nfive\nsix\nseven\n"}
	wantTail := "no reason in its output, which ended with: three / four / five / six / seven"
	if got := run.skipExplanation(); got != wantTail {
		t.Errorf("a peer whose output holds no reason explains itself as:\n%s\nwant:\n%s", got, wantTail)
	}
}

// TestARoundWithASkippedPeerCoversNothing pins what a skip does to a round: the
// round may have reached the window, and it is still not one the summary may
// claim.
//
// The state this rules out is the mixed round — some peers skipping while
// others lose the create race. Counting that round made it one that covered the
// window, with a summary saying every peer in it came up on the one container,
// of a round in which some peers never asked for one.
func TestARoundWithASkippedPeerCoversNothing(t *testing.T) {
	windowReached := peerRun{role: "peer-0", acquired: true, calls: []dockerCall{
		{sub: "run", status: conflictStatus, stderr: conflictResponse},
		{sub: "inspect", status: failureStatus, stderr: missingObjectResponse},
	}}
	cameUp := peerRun{role: "peer-1", acquired: true}
	skipped := peerRun{role: "peer-2"}
	failed := peerRun{role: "peer-3", exit: 1}

	for _, c := range []struct {
		name string
		runs []peerRun
		want bool
	}{
		{"every peer came up, one of them through the window", []peerRun{windowReached, cameUp}, true},
		{"a peer skipped", []peerRun{windowReached, skipped}, false},
		{"a peer failed", []peerRun{windowReached, failed}, false},
		{"no peer reached the window", []peerRun{cameUp, cameUp}, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			if got := summarizeRound(c.runs).coversWindow(); got != c.want {
				t.Errorf("the round covers the window: %v, want %v", got, c.want)
			}
		})
	}

	summary := summarizeRound([]peerRun{windowReached, cameUp, skipped})
	if summary.converged != 2 || summary.skipped != 1 || summary.failed != 0 {
		t.Errorf("the round added up to %d coming up, %d skipping and %d failing, want 2, 1 and 0",
			summary.converged, summary.skipped, summary.failed)
	}
	if len(summary.reached) != 1 || summary.reached[0] != "peer-0" {
		t.Errorf("the round has %v reach the window, want peer-0 alone", summary.reached)
	}
}

// TestEachConclusionSaysWhatItsCountIs holds the run's closing sentence to the
// count it is about, in each of the four states a run can end in.
//
// The defect this rules out is one of wording, and one a harness that writes
// its sentences as it goes cannot see for itself: the rounds a run counts are
// the ones that covered the window, the sentence said "reached", and a log of a
// mixed round — peers reaching the window, peers skipping — ended on "none of
// the rounds reached the window" a few lines under the lines saying otherwise.
// Both halves are pinned here: which state a set of totals falls into, and
// every word each state says about itself.
func TestEachConclusionSaysWhatItsCountIs(t *testing.T) {
	said := make(map[string]string)
	for _, c := range []struct {
		name   string
		totals runTotals
		want   conclusionKind
		// says has to appear in the sentence, and silent must not: the claims
		// the state is and is not allowed to make about what it counted.
		says   []string
		silent []string
	}{
		{
			name:   "a peer that could not reach the container",
			totals: runTotals{rounds: 20, reached: 20, covered: 19, failures: 2},
			want:   conclusionFailed,
			says:   []string{"2 of the peers", "these 20 rounds", "reported failure"},
		},
		{
			name:   "no round reached the window",
			totals: runTotals{rounds: 20},
			want:   conclusionNeverReached,
			says:   []string{"none of the 20 rounds reached the window"},
			silent: []string{"covered"},
		},
		{
			// The mixed round as the audit of the race saw it: the window
			// reached in every round, and in each of them a peer that skipped
			// instead of racing.
			name:   "rounds reached the window and none of them covered it",
			totals: runTotals{rounds: 20, reached: 20},
			want:   conclusionReachedNotCovered,
			says:   []string{"reached in 20 of the 20 rounds", "no round covered it", "skip"},
			silent: []string{"none of the 20 rounds reached", "every peer in them came up"},
		},
		{
			name:   "rounds covered the window",
			totals: runTotals{rounds: 20, reached: 20, covered: 3},
			want:   conclusionCovered,
			says:   []string{"3 of 20 rounds covered the window", "every peer in them came up"},
			silent: []string{"reached"},
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			got := c.totals.conclusion()
			if got.kind != c.want {
				t.Errorf("the conclusion of %+v is kind %d, want %d", c.totals, got.kind, c.want)
			}
			for _, want := range c.says {
				if !strings.Contains(got.sentence, want) {
					t.Errorf("the conclusion of %+v reads:\n%s\nand it has to say %q",
						c.totals, got.sentence, want)
				}
			}
			for _, unwanted := range c.silent {
				if strings.Contains(got.sentence, unwanted) {
					t.Errorf("the conclusion of %+v reads:\n%s\nand it must not say %q",
						c.totals, got.sentence, unwanted)
				}
			}
			if other, seen := said[got.sentence]; seen {
				t.Errorf("the conclusion of %+v says exactly what %s says, and the states are told "+
					"apart by what they say:\n%s", c.totals, other, got.sentence)
			}
			said[got.sentence] = c.name
		})
	}
}

// TestPeerEnvironmentCarriesTheDaemonAlong holds the environment a peer is
// built to what it needs to reach the same daemon the harness reaches.
//
// The asymmetry this rules out is one no round can see for itself: the harness
// reaching a daemon through its own environment and its peers not reaching it
// through theirs, so that every peer skips a race it was started for.
func TestPeerEnvironmentCarriesTheDaemonAlong(t *testing.T) {
	list := string(os.PathListSeparator)
	for _, c := range []struct {
		name string
		host []string
		want []string
	}{
		{
			name: "a machine that keeps its docker behind its environment",
			host: []string{"PATH=/usr/bin:/bin", "HOME=/home/tester", "DOCKER_HOST=tcp://127.0.0.1:2375", "CI=true"},
			want: []string{
				"PATH=/shim" + list + "/usr/bin:/bin",
				coldstartChildEnv + "=peer-3",
				coldstartRendezvousEnv + "=/rendezvous",
				coldstartPeersEnv + "=" + strconv.Itoa(coldstartPeers),
				"CI=true",
				"HOME=/home/tester",
				"DOCKER_HOST=tcp://127.0.0.1:2375",
			},
		},
		{
			name: "a machine that keeps none of it",
			host: []string{"PATH=/usr/bin"},
			want: []string{
				"PATH=/shim" + list + "/usr/bin",
				coldstartChildEnv + "=peer-3",
				coldstartRendezvousEnv + "=/rendezvous",
				coldstartPeersEnv + "=" + strconv.Itoa(coldstartPeers),
			},
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			env := peerEnvironment("/shim", "/rendezvous", "peer-3", c.host)
			if got, want := strings.Join(env, "\n"), strings.Join(c.want, "\n"); got != want {
				t.Errorf("the peer's environment is:\n%s\nwant:\n%s", got, want)
			}
		})
	}
}
