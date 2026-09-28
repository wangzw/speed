package ratelimit

import (
	"bytes"
	"context"
	"fmt"
	nethttp "net/http"
	"strings"
	"testing"
	"time"

	"github.com/vislake/speed/pkg/authn"
)

// A backend that gives up on a call of its own accord answers with the
// context package's errors too: a client library that bounds a call with its
// own timeout returns context.DeadlineExceeded while the request that made the
// call is still live. That is the backend failing, and it has to be reported
// like any other backend failure; the request's own context is what tells a
// caller's cancellation apart.
func TestABackendTimeoutIsReportedWhileTheRequestIsLive(t *testing.T) {
	timedOut := fmt.Errorf("the backend's own call bound: %w", context.DeadlineExceeded)
	next := nethttp.HandlerFunc(func(nethttp.ResponseWriter, *nethttp.Request) {})
	failingIncrement := newFakeKV()
	failingIncrement.incrementErr = timedOut
	cases := []struct {
		name     string
		counters *fakeKV
		want     reason
	}{
		{"the read", &fakeKV{readErr: timedOut}, reasonCounterRead},
		{"the increment", failingIncrement, reasonCounterIncrement},
		{"a cancellation the backend reports", &fakeKV{readErr: context.Canceled}, reasonCounterRead},
	}
	for _, c := range cases {
		var out bytes.Buffer
		l := pinned()
		l.counters = c.counters
		l.store = &fakeStore{values: map[string]any{windowPath: time.Minute, limitPath: 10}}
		l.signal = newSignalTo(&out)

		reached := false
		d := l.decide(newRecorder(), newRequest(), nethttp.HandlerFunc(
			func(w nethttp.ResponseWriter, r *nethttp.Request) { reached = true; next.ServeHTTP(w, r) }),
			authn.Identity{}, true)
		l.signal.settle(d)

		if d.tripped != c.want {
			t.Errorf("%s: tripped %q, want %q", c.name, d.tripped, c.want)
		}
		if !containsLine(out.String(), "letting requests through", string(c.want)) {
			t.Errorf("%s: no failure line was written: %q", c.name, out.String())
		}
		if !reached {
			t.Errorf("%s: the request was not let through", c.name)
		}
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
