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
