package sms

import (
	"testing"
)

// TestTheChallengeCarriesTheFlowSource checks that the flow's source address reaches the code engine as the send's source.
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
