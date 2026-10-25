package sms

import (
	"testing"
)

// TestTheChallengeCarriesTheFlowSource holds that the address the engine put on
// the flow reaches the code engine as the send's source. The factor has no
// request of its own: FlowState.Source is the only place it can learn where the
// call came from, and an empty one is passed through as empty.
func TestTheChallengeCarriesTheFlowSource(t *testing.T) {
	const source = "203.0.113.7"
	r := newRig()
	f := flow(3, "en-US", nil)
	f.Source = source
	if _, err := r.f.Challenge(t.Context(), f, record()); err != nil {
		t.Fatalf("Challenge: %v", err)
	}
	sent, ok := r.cs.lastSend()
	if !ok {
		t.Fatal("Challenge sent no code")
	}
	if sent.source != source {
		t.Errorf("the send carried source %q, want the flow's address %q", sent.source, source)
	}
}
