package ratelimit

import (
	"bytes"
	"context"
	"fmt"
	nethttp "net/http"
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
