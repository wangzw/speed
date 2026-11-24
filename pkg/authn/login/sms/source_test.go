package sms

import (
	"testing"

	"github.com/vislake/speed/pkg/authn"
)

// TestTheRequestAddressReachesTheCodeEngine holds that Begin takes the address from Start.Client and the answer step from StepInput.Client.
func TestTheRequestAddressReachesTheCodeEngine(t *testing.T) {
	const source = "203.0.113.7"
	ctx := t.Context()

	t.Run("begin", func(t *testing.T) {
		r := newRig()
		r.registerActive()
		start := loginStart(phoneFields(phoneNumber))
		start.Client = authn.ClientInfo{IP: source}
		if _, err := r.m.Begin(ctx, start); err != nil {
			t.Fatalf("Begin: %v", err)
		}
		if len(r.sm.sends) != 1 {
			t.Fatalf("Begin sent %d codes, want one", len(r.sm.sends))
		}
		if got := r.sm.sends[0].Source; got != source {
			t.Errorf("Begin's Send saw source %q, want the request's address %q", got, source)
		}
	})

	t.Run("answer", func(t *testing.T) {
		r := newRig()
		r.registerActive()
		in := submit(phoneFields(phoneNumber))
		in.Client = authn.ClientInfo{IP: source}
		if _, err := r.m.Answer(ctx, flowAt(stepPhone, nil), in); err != nil {
			t.Fatalf("Answer: %v", err)
		}
		if len(r.sm.sends) != 1 {
			t.Fatalf("Answer sent %d codes, want one", len(r.sm.sends))
		}
		if got := r.sm.sends[0].Source; got != source {
			t.Errorf("Answer's Send saw source %q, want the request's address %q", got, source)
		}
	})
}
