package main

// The impersonation fixture's two configuration values and the step-up branch
// they make reachable. The factor the fixture needs seasoned is written by
// server/e2e/factorseed, so this file only holds what the configuration states
// and what the product offers a session that already proved TOTP.

import (
	"net/http"
	"testing"

	"github.com/vislake/speed/pkg/authn"
)

// TestTheE2EConfigurationNarrowsTheMFAWindows holds the two setting values the
// fixture depends on: authn.mfa.sensitive-min-age is the one-hour floor that
// lets a backdated factor count as seasoned, and authn.impersonation.max-auth-age
// is the one-minute floor that makes a fresh sign-in stale soon enough to reach
// the step-up.
func TestTheE2EConfigurationNarrowsTheMFAWindows(t *testing.T) {
	t.Parallel()
	var seed seedInput
	readEffective(t, "the e2e configuration", "file:e2e/config.yaml", "seed", &seed)

	seeded := make(map[string]string, len(seed.Settings))
	for _, item := range seed.Settings {
		seeded[item.Key] = item.Value
	}
	for key, want := range map[string]string{
		"authn.mfa.sensitive-min-age":      "1h",
		"authn.impersonation.max-auth-age": "1m",
	} {
		if got := seeded[key]; got != want {
			t.Errorf("the e2e configuration seeds %s as %q, want %q", key, got, want)
		}
	}
}

// TestTheStepUpAfterTOTPSignInOffersSMSWithoutTOTP holds the branch the fixture
// exists to reach: a session that proved TOTP is asked to strengthen itself, and
// the offered factors are the ones it did not prove. SMS is offered and TOTP is
// not, so the step-up can complete the impersonation the freshness rule refused.
func TestTheStepUpAfterTOTPSignInOffersSMSWithoutTOTP(t *testing.T) {
	t.Parallel()
	base, token := e2eRecordsServer(t)

	start := send(t, &http.Client{Timeout: probeTimeout()}, http.MethodPost,
		base+"/api/v1/auth/flows/authenticated",
		map[string]any{"purpose": "step-up"},
		map[string]string{"Authorization": "Bearer " + token})
	if start.status != http.StatusOK {
		t.Fatalf("starting the step-up answered %d:\n%s", start.status, start.body)
	}
	flow := decodeE2EFlow(t, start.body)
	if flow.Next.Kind != "factor" {
		t.Fatalf("the step-up answered kind %q, want a factor step:\n%s", flow.Next.Kind, start.body)
	}
	offered := make(map[string]bool, len(flow.Next.Factors))
	for _, factor := range flow.Next.Factors {
		offered[factor.Kind] = true
	}
	if !offered[string(authn.FactorSMS)] {
		t.Errorf("the step-up offers no sms factor: %+v", flow.Next.Factors)
	}
	if offered[string(authn.FactorTOTP)] {
		t.Errorf("the step-up offers totp, the method the session already proved: %+v", flow.Next.Factors)
	}
}
