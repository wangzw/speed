package redis

import (
	"testing"

	"github.com/vislake/speed/pkg/event"
)

// TestTheTraceContextSurvivesTheStreamEntry pins this backend's line format: the
// tracing context a publish writes into the stream entry comes back out of a
// decode. A Redis entry holds strings only, so the context is one field in the
// encoded form; this holds the writer and the reader to the same one without a
// server.
func TestTheTraceContextSurvivesTheStreamEntry(t *testing.T) {
	carrier := map[string]string{"traceparent": "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01"}
	evt := event.Event{ID: "e1", Type: "t", TenantID: "acme", TraceContext: carrier}
	got := decode("t", entryValues(evt, []byte(`{"k":"v"}`)))
	if want := carrier["traceparent"]; got.TraceContext["traceparent"] != want {
		t.Fatalf("the tracing context came out of the entry as %v, want the traceparent %q",
			got.TraceContext, want)
	}
}
