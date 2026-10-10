package ratelimit

import (
	"bytes"
	"context"
	"fmt"
	nethttp "net/http"
	"strings"
	"testing"
	"time"
)

// A backend that gives up on a call of its own accord answers with the
// context package's errors too: a client library that bounds a call with its
// own timeout returns context.DeadlineExceeded while the request that made the
// call is still live. That is the backend failing, and it has to be reported
// like any other backend failure; the request's own context is what tells a
// caller's cancellation apart.
func TestABackendTimeoutIsReportedWhileTheRequestIsLive(t *testing.T) {
	timedOut := fmt.Errorf("the backend's own call bound: %w", context.DeadlineExceeded)
	cases := []struct {
		name     string
		counters func() *fakeKV
		want     reason
	}{
		{"the read", func() *fakeKV { return &fakeKV{readErr: timedOut} }, reasonCounterRead},
		{"the increment", func() *fakeKV {
			failing := newFakeKV()
			failing.incrementErr = timedOut
			return failing
		}, reasonCounterIncrement},
		{"a cancellation the backend reports", func() *fakeKV { return &fakeKV{readErr: context.Canceled} }, reasonCounterRead},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var out lockedBuffer
			l := pinned("public")
			l.counters = c.counters()
			l.store = &fakeStore{values: map[string]any{windowPath: time.Minute, limitPath: 10}}
			l.signal = newSignalTo(&out)
			h := startHost(t, hostOptions{limiter: l})

			if got := h.get(t, "/things"); got.status != nethttp.StatusNoContent {
				t.Fatalf("the request was answered %d, want it let through to the route's 204", got.status)
			}

			if got := h.reached.count(); got != 1 {
				t.Errorf("the route served %d requests, want the one let through", got)
			}
			decided := h.decisions.all()
			if len(decided) != 1 || decided[0].tripped != c.want {
				t.Errorf("the decisions are %+v, want one that tripped %q", decided, c.want)
			}
			if !containsLine(out.String(), "letting requests through", string(c.want)) {
				t.Errorf("no failure line was written: %q", out.String())
			}
		})
	}
}

// pinnedClock is a clock a test moves by hand, for the quiet period a class
// has to keep before it may recover.
type pinnedClock struct{ at time.Time }

func (c *pinnedClock) now() time.Time           { return c.at }
func (c *pinnedClock) advance(by time.Duration) { c.at = c.at.Add(by) }

// signalOnAPinnedClock is a signal writing to out whose quiet period is
// measured on a clock the test moves.
func signalOnAPinnedClock(out *bytes.Buffer) (*signal, *pinnedClock) {
	clock := &pinnedClock{at: pinnedAt}
	s := newSignalTo(out)
	s.now = clock.now
	return s, clock
}

// TestAFailureConfinedToOneTenantWritesOneLine pins the promise the lines are
// kept to: a failure that persists writes one line, not one per request. A
// condition that holds for some requests and not others — one tenant's bad
// row, one key's bad value, a backend that answers some calls — alternates
// with decisions that pass, and a class that cleared on the first passing
// decision would write two lines per alternation for as long as the condition
// lasts.
func TestAFailureConfinedToOneTenantWritesOneLine(t *testing.T) {
	var out bytes.Buffer
	s, clock := signalOnAPinnedClock(&out)

	bad := stoppedAt(reasonThresholdDomain) // the tenant whose row is out of domain
	bad.tenant = "bad"
	good := everyClass // every other tenant
	good.tenant = "good"
	step := func(d decision) {
		s.settle(d)
		clock.advance(time.Second)
	}

	for range 5 {
		step(bad)
		step(good)
	}
	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	if len(lines) != 1 {
		t.Fatalf("five alternations of a failing and a passing tenant wrote %d lines, want 1:\n%s",
			len(lines), out.String())
	}
	if !containsLine(lines[0], "letting requests through", string(reasonThresholdDomain)) {
		t.Errorf("the line %q does not report the domain class", lines[0])
	}
	if !strings.Contains(lines[0], `"bad"`) {
		t.Errorf("the line %q does not name the tenant the failure was first seen for", lines[0])
	}

	// A passing decision inside the quiet period is not a recovery: the
	// condition has only been out of sight for a moment.
	step(good)
	if strings.Contains(out.String(), "back to limiting") {
		t.Fatalf("the class recovered a second after its last failure:\n%s", out.String())
	}

	// Once the failing tenant has been quiet for long enough, the class
	// recovers on the next decision that evaluates it, and says so once.
	clock.advance(recoveryQuiet)
	step(good)
	step(good)
	if !containsLine(out.String(), "back to limiting", string(reasonThresholdDomain)) {
		t.Fatalf("the class did not recover after a quiet period:\n%s", out.String())
	}
	if got := strings.Count(out.String(), "back to limiting"); got != 1 {
		t.Errorf("the recovery was written %d times, want once:\n%s", got, out.String())
	}
}

// TestTheQuietPeriodRunsFromTheLastFailure pins where the quiet period is
// measured from. A condition confined to some decisions keeps recurring for as
// long as it lasts, and every recurrence starts the quiet period again. Measured
// from the first failure instead, the class would recover one quiet period
// after the condition began however long it went on, and the next recurrence
// would write a new failure line: a pair of lines per quiet period, which is
// the flapping the period is there to prevent, only slower.
//
// The condition here lasts three quiet periods and recurs at half a period's
// spacing, so a class measured from its first failure has already recovered
// inside the loop; the last part pins the recovery to one quiet period after
// the last failure, in both directions.
func TestTheQuietPeriodRunsFromTheLastFailure(t *testing.T) {
	var out bytes.Buffer
	s, clock := signalOnAPinnedClock(&out)

	bad := stoppedAt(reasonThresholdDomain)
	bad.tenant = "bad"
	good := everyClass
	good.tenant = "good"

	step := recoveryQuiet / 4
	var lastFailure time.Time
	for elapsed := time.Duration(0); elapsed < 3*recoveryQuiet; elapsed += 2 * step {
		s.settle(bad)
		lastFailure = clock.now()
		clock.advance(step)
		s.settle(good)
		clock.advance(step)
	}
	if strings.Contains(out.String(), "back to limiting") {
		t.Fatalf("a failure recurring every %v for %v was reported recovered while it still "+
			"recurred, so the quiet period was not restarted by the later failures:\n%s",
			2*step, 3*recoveryQuiet, out.String())
	}
	if got := strings.Count(out.String(), "letting requests through"); got != 1 {
		t.Fatalf("a failure that never went quiet wrote %d failure lines, want 1:\n%s", got, out.String())
	}

	// The condition has stopped. Just short of a quiet period after the last
	// failure the class is still failing; at a quiet period it recovers.
	clock.at = lastFailure.Add(recoveryQuiet - time.Second)
	s.settle(good)
	if strings.Contains(out.String(), "back to limiting") {
		t.Fatalf("the class recovered %v after its last failure, short of the quiet period %v:\n%s",
			recoveryQuiet-time.Second, recoveryQuiet, out.String())
	}
	clock.at = lastFailure.Add(recoveryQuiet)
	s.settle(good)
	if got := strings.Count(out.String(), "back to limiting"); got != 1 {
		t.Fatalf("a quiet period after the last failure the recovery was written %d times, want once:\n%s",
			got, out.String())
	}
}

// TestAnEmptyEndpointListNamesTheKey pins the startup failure's fixing action:
// the host is told which key to write. The sentinel alone says an input item
// has no value and not which one, and a host running several modules is left
// to guess.
func TestAnEmptyEndpointListNamesTheKey(t *testing.T) {
	_, err := Module().Prepare(context.Background(), registryWith(t, nil))
	if err == nil {
		t.Fatal("Prepare accepted an empty endpoint list")
	}
	if !strings.Contains(err.Error(), ConfigNamespace+".endpoints") {
		t.Errorf("the failure %q does not name the key %s.endpoints", err, ConfigNamespace)
	}
}

// TestACounterFailureLineNamesNoIdentity keeps the counter classes' lines to
// the class alone. Their key carries the identity string, and a failure line
// on standard error is not the place to repeat it.
func TestACounterFailureLineNamesNoIdentity(t *testing.T) {
	var out bytes.Buffer
	s := newSignalTo(&out)
	d := stoppedAt(reasonCounterRead)
	d.tenant = "acme"
	s.settle(d)
	if strings.Contains(out.String(), "acme") || strings.Contains(out.String(), "first seen") {
		t.Errorf("the counter class's line carries a scope: %q", out.String())
	}
}
