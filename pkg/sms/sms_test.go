package sms

import (
	"errors"
	"testing"
)

// Provider.Model has to be reachable from both seams: the management surface
// holds a Provider, and Product is Sender plus Provider. A method expression
// stops compiling if either seam loses it.
var (
	_ func(Provider) CarrierModel = Provider.Model
	_ func(Product) CarrierModel  = Product.Model
)

// TestTheSentinelsAreDistinct holds that a caller can tell the failure kinds
// apart: ErrUnroutable is neither a defective message, an unknown outcome, a
// template state nor a configuration fault.
func TestTheSentinelsAreDistinct(t *testing.T) {
	sentinels := map[string]error{
		"ErrThrottled":     ErrThrottled,
		"ErrRejected":      ErrRejected,
		"ErrUnroutable":    ErrUnroutable,
		"ErrUncertain":     ErrUncertain,
		"ErrInvalidConfig": ErrInvalidConfig,
		"ErrNotSendable":   ErrNotSendable,
	}
	for aName, a := range sentinels {
		for bName, b := range sentinels {
			if aName == bName {
				continue
			}
			if errors.Is(a, b) {
				t.Errorf("%s matches %s, and the two have to be told apart", aName, bName)
			}
		}
	}
}

// TestApprovalNotRequiredIsItsOwnState holds the closed vocabulary's new member:
// it carries its own value and is distinct from Unknown, which says a review
// exists and this process does not track it.
func TestApprovalNotRequiredIsItsOwnState(t *testing.T) {
	if ApprovalNotRequired != "not-required" {
		t.Errorf("ApprovalNotRequired = %q, want %q", ApprovalNotRequired, "not-required")
	}
	if ApprovalNotRequired == ApprovalUnknown {
		t.Error("ApprovalNotRequired is ApprovalUnknown, which is the state it has to be told apart from")
	}
	if ApprovalNotRequired == ApprovalApproved {
		t.Error("ApprovalNotRequired is ApprovalApproved, and the vendor's absence of a review is not a review")
	}
}

// TestTheSendableStatesAreApprovedAndNotRequired holds which states a message may
// go out under. NotRequired qualifies because the vendor has no review to wait
// on; Unknown does not, because a review exists and this process does not follow
// it.
func TestTheSendableStatesAreApprovedAndNotRequired(t *testing.T) {
	cases := map[ApprovalState]bool{
		ApprovalApproved:    true,
		ApprovalNotRequired: true,
		ApprovalUnknown:     false,
		ApprovalPending:     false,
		ApprovalRejected:    false,
		ApprovalWithdrawn:   false,
	}
	for state, want := range cases {
		if got := sendable(state); got != want {
			t.Errorf("sendable(%q) = %t, want %t", state, got, want)
		}
	}
}

// TestIdentityKindsAreTheSharedVocabulary holds the closed set of identity kinds
// a carrier maps its vendor's spelling onto.
func TestIdentityKindsAreTheSharedVocabulary(t *testing.T) {
	want := []IdentityKind{
		IdentitySignName, IdentitySenderID, IdentityLongCode, IdentityShortCode,
		IdentityTollFree, IdentityPool, IdentityMessagingService,
	}
	seen := make(map[IdentityKind]bool, len(want))
	for _, kind := range want {
		if kind == "" {
			t.Error("an identity kind is the empty string")
		}
		if seen[kind] {
			t.Errorf("the identity kind %q is declared twice", kind)
		}
		seen[kind] = true
	}
	if IdentitySignName != "sign-name" || IdentityMessagingService != "messaging-service" {
		t.Errorf("the identity kinds do not carry their wire values: %q, %q",
			IdentitySignName, IdentityMessagingService)
	}
}
