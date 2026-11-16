//go:build review

package main

import (
	"fmt"
	"testing"
)

// mayLeaveRouteCountUnchanged is the review build's exemption table: the routing
// modules whose removal cannot lower an endpoint's route count over the review
// configuration, each with the reason. No module is exempt here. The review
// build names org.personal, and removing it does not lower a route count: the
// account module requires the provisioning seam the personal module provides,
// so the run does not start at all. That failure is held by the startup
// assertion below and by TestEveryRoutingVariantMustStart, not by this table.
// The human-verification gate is off, but the sms and login modules require the
// verifier, so dropping it drops them too and lowers the route counts. See
// differential_exemptions_default_test.go for the baseline build's table.
var mayLeaveRouteCountUnchanged = map[string]string{}

// TestReviewRouteCountExemptionsAreTrueForItsConfiguration holds each review
// build's exemption to its stated reason and holds the baseline template's
// organization and SMS-management exemptions out: the review build names the
// admin endpoint for sms.admin, so removing it lowers a route count and an
// exemption claiming otherwise would be false. org.personal is not exempt
// either, but its removal is not measured as a route count: the account module
// requires the provisioning seam the personal module provides, so the variant
// does not start at all. The case holds that startup failure instead, which is
// the same treatment the baseline build gives the module. The
// human-verification module is a required dependency of sms and login: dropping
// it drops its dependents, so its removal lowers the counts and it is not
// exempt. Making the verifier optional again would make the removal vacuous and
// redden the human-verification half, which is the check retiring the wrong
// state. One that mounts no SMS management surface reddens the sms.admin half.
func TestReviewRouteCountExemptionsAreTrueForItsConfiguration(t *testing.T) {
	t.Parallel()
	if reason, exempt := mayLeaveRouteCountUnchanged["org.personal"]; exempt {
		t.Errorf("the review build exempts org.personal (%q), but the review build "+
			"names the module: the exemption is false for this build and the baseline table's "+
			"entry must not be shared into it", reason)
	}
	if reason, exempt := mayLeaveRouteCountUnchanged["sms.admin"]; exempt {
		t.Errorf("the review build exempts sms.admin (%q), but the review configuration names the "+
			"admin endpoint in sms.admin.endpoints, so that module mounts its routes: the exemption "+
			"is false for this build and the baseline table's entry must not be shared into it", reason)
	}

	humanVerify := variantGroupNamed(t, "authn.humanverify")
	personal := variantGroupNamed(t, "org.personal")
	smsAdmin := variantGroupNamed(t, "sms.admin")

	topo := topology{name: "exemptions", ports: listenerCount}
	before, after := removalRouteCounts(t, topo, []variantGroup{humanVerify, smsAdmin})
	// The verifier is required by sms and login, so its removal drops them
	// as well: the removal is not vacuous even with both surfaces off.
	assertRouteCountsLowered(t, humanVerify, before, after[humanVerify.id])
	assertRouteCountsLowered(t, smsAdmin, before, after[smsAdmin.id])

	// The personal module's removal is a startup failure, not a vacuous route
	// count: the account module requires the seam it provides.
	variant, _, err := startVariant(t, topo, personal.modules)
	if miss := startupFailureMiss(variant, err, fmt.Sprintf(
		"the account module's required provisioning seam to stop a run without %v", personal.modules)); miss != "" {
		t.Fatal(miss)
	}
}
