//go:build !review

package main

import (
	"context"
	"testing"

	tenantpersonal "github.com/vislake/speed/pkg/tenant/personal"
)

// reviewEnabled says this test binary is built with the review tag; the
// default build is not, so TestMain compiles one program and no case looks for
// a second.
const reviewEnabled = false

// TestTheDefaultSettingsWriteGuardHasNoReviewFixture holds the build split: the
// review fixture's protected- business tenant is refused only under the review
// tag. A default build compiles the false answer from review_off.go, so a
// tenant carrying that prefix is an ordinary business tenant here, while the
// personal domain keeps its refusal. The probe names the fixture the review
// build adds; it is a test value, not a policy this build carries.
func TestTheDefaultSettingsWriteGuardHasNoReviewFixture(t *testing.T) {
	guard := settingsWriteGuard{}
	ctx := context.Background()
	for _, domain := range []string{"protected-acme", "protected-"} {
		denied, err := guard.Denied(ctx, domain)
		if err != nil {
			t.Fatalf("Denied(%q): %v", domain, err)
		}
		if denied {
			t.Errorf("Denied(%q) = true in a default build, want the review fixture compiled out", domain)
		}
	}
	for _, domain := range []string{tenantpersonal.IDPrefix + "acct-1", tenantpersonal.IDPrefix} {
		denied, err := guard.Denied(ctx, domain)
		if err != nil {
			t.Fatalf("Denied(%q): %v", domain, err)
		}
		if !denied {
			t.Errorf("Denied(%q) = false, want the personal domain still refused", domain)
		}
	}
	if denied, err := guard.Denied(ctx, "acme"); err != nil || denied {
		t.Errorf("Denied(acme) = %v, %v; want an ordinary business tenant allowed", denied, err)
	}
}
