package journal

import (
	"bytes"
	"context"
	nethttp "net/http"
	"net/http/httptest"
	"testing"

	"github.com/vislake/speed/pkg/audit"
	"github.com/vislake/speed/pkg/event"
)

// cancelled is a context whose caller has gone: net/http cancels a request's
// context when the client closes its connection, and a publisher's context ends
// the same way when the request that published has.
func cancelled() context.Context {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	return ctx
}

// A request whose client went away before the handler returned is still
// recorded.
//
// The work the handler did has happened by then — a delete committed, an export
// was read — and the row is the only trace of it this path leaves. Writing it
// under the request's own context loses it to the cancellation, and the failure
// signal then reports a healthy table as failing.
func TestARequestWhoseClientHasGoneIsStillRecorded(t *testing.T) {
	handle := open(t)
	var reported bytes.Buffer
	j := newTestJournal(handle, settings{})
	j.signal = &signal{out: &reported}
	m, err := newMatcher([]string{"DELETE /things/{id}"})
	if err != nil {
		t.Fatalf("building the matcher: %v", err)
	}
	j.matcher = m

	inner := nethttp.HandlerFunc(func(w nethttp.ResponseWriter, _ *nethttp.Request) {
		w.WriteHeader(nethttp.StatusNoContent)
	})
	layer := j.middlewareFor("public").Wrap(inner)
	r := httptest.NewRequest("DELETE", "/things/t1", nil).WithContext(cancelled())
	layer.ServeHTTP(httptest.NewRecorder(), r)

	rows := auditRows(t, handle)
	if len(rows) != 1 {
		t.Fatalf("want the request recorded after its client went away, got %d row(s); signal: %s",
			len(rows), reported.String())
	}
	if rows[0].Outcome != "204" {
		t.Errorf("outcome is %q, want %q", rows[0].Outcome, "204")
	}
	if reported.Len() != 0 {
		t.Errorf("the failure signal reported a healthy table: %s", reported.String())
	}
}

// The event path is the same shape: a process-local bus runs the subscriber on
// the publisher's context, and the event was published whether or not that
// context is still live.
func TestAnEventPublishedOnAFinishedContextIsStillRecorded(t *testing.T) {
	handle := open(t)
	var reported bytes.Buffer
	bus := newRecordingBus()
	j := newTestJournal(handle, settings{events: []string{"order.updated"}})
	j.signal = &signal{out: &reported}
	j.subscribe(bus)

	if err := bus.Publish(cancelled(), event.Event{
		Type:    "order.updated",
		Payload: map[string]any{"object": "order/o1"},
	}); err != nil {
		t.Fatalf("publishing: %v", err)
	}

	rows := auditRows(t, handle)
	if len(rows) != 1 {
		t.Fatalf("want the event recorded after its publisher's context ended, got %d row(s); signal: %s",
			len(rows), reported.String())
	}
	if rows[0].Source != string(audit.SourceEvent) {
		t.Errorf("source is %q, want %q", rows[0].Source, audit.SourceEvent)
	}
	if reported.Len() != 0 {
		t.Errorf("the failure signal reported a healthy table: %s", reported.String())
	}
}

// headerLog is a response writer that keeps every status it was handed, which
// is what lets a case send an informational response before the final one.
type headerLog struct {
	header nethttp.Header
	codes  []int
}

func (h *headerLog) Header() nethttp.Header {
	if h.header == nil {
		h.header = nethttp.Header{}
	}
	return h.header
}

func (h *headerLog) Write(b []byte) (int, error) { return len(b), nil }

func (h *headerLog) WriteHeader(code int) { h.codes = append(h.codes, code) }

// recordOne runs one matched request through the layer with the given inner
// handler and returns the row it left.
func recordOne(t *testing.T, inner nethttp.Handler) row {
	t.Helper()
	handle := open(t)
	j := newTestJournal(handle, settings{})
	m, err := newMatcher([]string{"GET /things"})
	if err != nil {
		t.Fatalf("building the matcher: %v", err)
	}
	j.matcher = m
	j.middlewareFor("public").Wrap(inner).ServeHTTP(&headerLog{}, httptest.NewRequest("GET", "/things", nil))
	rows := auditRows(t, handle)
	if len(rows) != 1 {
		t.Fatalf("want one row, got %d", len(rows))
	}
	return rows[0]
}

// An informational response is not the answer. A handler may send any number of
// them — 103 Early Hints ahead of the real response is the common one — before
// the status the request ends with, and the outcome column holds that one.
func TestAnInformationalResponseIsNotTheOutcome(t *testing.T) {
	t.Run("an explicit final status", func(t *testing.T) {
		got := recordOne(t, nethttp.HandlerFunc(func(w nethttp.ResponseWriter, _ *nethttp.Request) {
			w.WriteHeader(nethttp.StatusEarlyHints)
			w.WriteHeader(nethttp.StatusCreated)
		}))
		if got.Outcome != "201" {
			t.Fatalf("outcome is %q, want the final status %q", got.Outcome, "201")
		}
	})
	t.Run("the implicit final status", func(t *testing.T) {
		got := recordOne(t, nethttp.HandlerFunc(func(w nethttp.ResponseWriter, _ *nethttp.Request) {
			w.WriteHeader(nethttp.StatusEarlyHints)
			_, _ = w.Write([]byte("body"))
		}))
		if got.Outcome != "200" {
			t.Fatalf("outcome is %q, want the implicit final status %q", got.Outcome, "200")
		}
	})
}

// Switching protocols is the one informational status that is final: the
// connection is handed to another protocol and no other status follows.
func TestSwitchingProtocolsIsTheOutcome(t *testing.T) {
	got := recordOne(t, nethttp.HandlerFunc(func(w nethttp.ResponseWriter, _ *nethttp.Request) {
		w.WriteHeader(nethttp.StatusSwitchingProtocols)
	}))
	if got.Outcome != "101" {
		t.Fatalf("outcome is %q, want %q", got.Outcome, "101")
	}
}
