package sms

import (
	"errors"
	"testing"
)

// Provider.Model must be reachable from both seams; a method expression stops
// compiling if either loses it.
var (
	_ func(Provider) CarrierModel = Provider.Model
	_ func(Product) CarrierModel  = Product.Model
)

// TestTheSentinelsAreDistinct checks that a caller can tell the failure kinds apart.
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

// TestApprovalNotRequiredIsItsOwnState checks that it carries its own value and is distinct from Unknown.
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

// TestTheSendableStatesAreApprovedAndNotRequired checks which states a message may go out under.
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

// TestIdentityKindsAreTheSharedVocabulary checks the closed set of identity kinds.
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
