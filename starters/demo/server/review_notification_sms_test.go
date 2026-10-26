//go:build review

package main

// The review assembly's SMS side of the notification fixture: the type carries
// the closed channel set's sms member and names the template the rollout
// journey registers, and a recipient is addressed on the external channel their
// account can actually be reached on. A recipient with a proved phone is
// addressed by sms; one with only an email address falls back to email, and sms
// is removed from that recipient's effective set rather than attempted with the
// email address as its destination.

import (
	"testing"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/core"
	"github.com/vislake/speed/pkg/feature"
	"github.com/vislake/speed/pkg/notification"
)

const (
	reviewSMSTestEmail = "member@example.test"
	reviewSMSTestPhone = "+15551234567"
)

// TestReviewNotificationDeclaresSMSAndItsTemplateBinding holds the declaration
// side of the rollout journey: the review type's default channels include sms,
// its recipient-visible whitelist admits the destination the sms channel reads,
// and its catalog names the template the journey registers in every language.
func TestReviewNotificationDeclaresSMSAndItsTemplateBinding(t *testing.T) {
	var declared *notification.TypeSpec
	for _, resource := range core.Resources[notification.TypeSpec](core.ProcessRegistry) {
		if resource.Value.Key != reviewNotificationTypeKey {
			continue
		}
		spec := resource.Value
		declared = &spec
	}
	if declared == nil {
		t.Fatalf("the available set declares no %q type", reviewNotificationTypeKey)
	}

	var haveSMS bool
	for _, channel := range declared.DefaultChannels {
		if channel == notification.ChannelSMS {
			haveSMS = true
		}
	}
	if !haveSMS {
		t.Errorf("the type's default channels are %v; the rollout journey needs %q among them "+
			"or the delivery pipeline never walks the sms channel",
			declared.DefaultChannels, notification.ChannelSMS)
	}
	if !containsString(declared.RecipientVisibleParams, "to") {
		t.Errorf("the type's parameter whitelist is %v; the sms channel reads its destination "+
			"from %q, which must also be a declared template parameter",
			declared.RecipientVisibleParams, "to")
	}

	for _, locale := range []string{reviewDefaultLocale, reviewChineseLocale} {
		got := reviewCatalogEntry(t, locale, reviewNotificationTypeKey+".sms.template")
		if got != reviewNotificationSMSTemplate {
			t.Errorf("the %s catalog names the sms template %q, want %q",
				locale, got, reviewNotificationSMSTemplate)
		}
	}
}

// TestReviewNotificationControlFeatureIsDeclared holds the review-only gate the
// tenant control reads: the review module declares it as a static feature, so
// it is present exactly when that module is in force and absent from every
// assembly that does not compile it.
func TestReviewNotificationControlFeatureIsDeclared(t *testing.T) {
	declared := false
	for _, resource := range core.Resources[feature.Spec](core.ProcessRegistry) {
		if resource.Module != reviewNotificationModuleName ||
			resource.Value.Name != reviewNotificationControlFeature {
			continue
		}
		if resource.Value.Conditional {
			t.Errorf("the control feature %q is conditional, so a review run that does not "+
				"post it offers no control", resource.Value.Name)
		}
		declared = true
	}
	if !declared {
		t.Fatalf("the review module declares no %q feature, so the tenant control can never "+
			"be offered", reviewNotificationControlFeature)
	}
}

// TestReviewNoticePlansTheExternalChannelFromTheAccount holds the per-recipient
// choice: a proved phone is the destination and sms the external channel, and
// an account with only an email address falls back to email without sms.
func TestReviewNoticePlansTheExternalChannelFromTheAccount(t *testing.T) {
	phone := authn.Identifier{
		Kind:        authn.IdentifierPhone,
		Value:       reviewSMSTestPhone,
		VerifiedVia: authn.VerifiedViaSMSCode,
	}
	email := authn.Identifier{
		Kind:        authn.IdentifierEmail,
		Value:       reviewSMSTestEmail,
		VerifiedVia: authn.VerifiedViaEmailCode,
	}

	cases := []struct {
		name         string
		identifiers  []authn.Identifier
		wantTo       string
		wantChannels []notification.Channel
		wantOK       bool
	}{
		{
			name:         "a proved phone addresses sms",
			identifiers:  []authn.Identifier{email, phone},
			wantTo:       reviewSMSTestPhone,
			wantChannels: []notification.Channel{notification.ChannelInApp, notification.ChannelSMS},
			wantOK:       true,
		},
		{
			name:         "an account without a phone falls back to email",
			identifiers:  []authn.Identifier{email},
			wantTo:       reviewSMSTestEmail,
			wantChannels: []notification.Channel{notification.ChannelInApp, notification.ChannelEmail},
			wantOK:       true,
		},
		{
			name: "an unproved phone is not an sms destination",
			identifiers: []authn.Identifier{
				email,
				{Kind: authn.IdentifierPhone, Value: reviewSMSTestPhone, VerifiedVia: authn.VerifiedViaSelf},
			},
			wantTo:       reviewSMSTestEmail,
			wantChannels: []notification.Channel{notification.ChannelInApp, notification.ChannelEmail},
			wantOK:       true,
		},
		{
			name:        "an account with no address is not addressable",
			identifiers: nil,
			wantOK:      false,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			plan, ok := planReviewNotice(c.identifiers)
			if ok != c.wantOK {
				t.Fatalf("planReviewNotice answered ok=%t, want %t", ok, c.wantOK)
			}
			if !ok {
				return
			}
			if plan.To != c.wantTo {
				t.Errorf("the destination is %q, want %q", plan.To, c.wantTo)
			}
			if !sameChannels(plan.Channels, c.wantChannels) {
				t.Errorf("the channels are %v, want %v", plan.Channels, c.wantChannels)
			}
		})
	}
}

// TestReviewNoticePrunesOnlyUnaddressableChannels holds the write the fixture
// makes before a dispatch: it removes a channel the recipient cannot be
// addressed on and never adds one back, so a recipient's stored choice survives
// every window.
func TestReviewNoticePrunesOnlyUnaddressableChannels(t *testing.T) {
	noPhone := reviewNoticePlan{
		To:       reviewSMSTestEmail,
		Channels: []notification.Channel{notification.ChannelInApp, notification.ChannelEmail},
	}
	withPhone := reviewNoticePlan{
		To:       reviewSMSTestPhone,
		Channels: []notification.Channel{notification.ChannelInApp, notification.ChannelSMS},
	}
	cases := []struct {
		name      string
		effective []notification.Channel
		plan      reviewNoticePlan
		want      []notification.Channel
	}{
		{
			name:      "the declared defaults lose sms without a phone",
			effective: []notification.Channel{notification.ChannelInApp, notification.ChannelEmail, notification.ChannelSMS},
			plan:      noPhone,
			want:      []notification.Channel{notification.ChannelInApp, notification.ChannelEmail},
		},
		{
			name:      "a stored set that already dropped email is left alone",
			effective: []notification.Channel{notification.ChannelInApp},
			plan:      noPhone,
			want:      []notification.Channel{notification.ChannelInApp},
		},
		{
			name:      "the declared defaults lose email when a phone addresses sms",
			effective: []notification.Channel{notification.ChannelInApp, notification.ChannelEmail, notification.ChannelSMS},
			plan:      withPhone,
			want:      []notification.Channel{notification.ChannelInApp, notification.ChannelSMS},
		},
		{
			name:      "a recipient who turned sms off keeps it off",
			effective: []notification.Channel{notification.ChannelInApp, notification.ChannelEmail},
			plan:      withPhone,
			want:      []notification.Channel{notification.ChannelInApp},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := pruneReviewChannels(c.effective, c.plan); !sameChannels(got, c.want) {
				t.Errorf("the pruned set is %v, want %v", got, c.want)
			}
		})
	}
}

// containsString reports whether values holds want.
func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

// sameChannels reports whether two channel sets are equal element by element.
func sameChannels(a, b []notification.Channel) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
