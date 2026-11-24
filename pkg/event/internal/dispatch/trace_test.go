package dispatch

import "testing"

// TestTraceContextRoundTripsThroughItsTextForm pins the text encoding a
// string-valued backend stores the tracing carrier in.
func TestTraceContextRoundTripsThroughItsTextForm(t *testing.T) {
	carrier := map[string]string{
		"traceparent": "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01",
		"tracestate":  "congo=t61rcWkgMzE",
	}
	got := DecodeTraceContext(EncodeTraceContext(carrier))
	if len(got) != len(carrier) {
		t.Fatalf("the carrier came back with %d keys, want %d: %v", len(got), len(carrier), got)
	}
	for k, v := range carrier {
		if got[k] != v {
			t.Errorf("the carrier's %q came back as %q, want %q", k, got[k], v)
		}
	}
}

// TestAnEmptyTraceContextIsTheEmptyCarrier pins what an event published
// outside any span, and a row written before the column existed, read as.
func TestAnEmptyTraceContextIsTheEmptyCarrier(t *testing.T) {
	if got := EncodeTraceContext(nil); got != "" {
		t.Errorf("a nil carrier encoded to %q, want the empty string", got)
	}
	if got := EncodeTraceContext(map[string]string{}); got != "" {
		t.Errorf("an empty carrier encoded to %q, want the empty string", got)
	}
	if got := DecodeTraceContext(""); got != nil {
		t.Errorf("the empty text decoded to %v, want the empty carrier", got)
	}
	if got := DecodeTraceContext("{}"); got != nil {
		t.Errorf("an encoded empty carrier decoded to %v, want the empty carrier", got)
	}
}

// TestTextThatIsNotACarrierReadsAsEmpty pins the failure direction.
func TestTextThatIsNotACarrierReadsAsEmpty(t *testing.T) {
	if got := DecodeTraceContext("not the encoded form"); got != nil {
		t.Errorf("text that is not the encoded form decoded to %v, want the empty carrier", got)
	}
}
