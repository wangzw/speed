//go:build review

package main

import "testing"

// mayLeaveRouteCountUnchanged is the review build's exemption table: the routing
// modules whose removal cannot lower an endpoint's route count over the review
// configuration, each with the reason. It is deliberately not the baseline
// build's table: the review configuration enables workspace.personal, so that
// module's route is mounted and removing it lowers the account endpoint's route
// count. The human-verification gate is off in the review configuration, so that
// module's challenge route is absent and its removal is vacuous. See
// differential_exemptions_default_test.go for the baseline build's table.
var mayLeaveRouteCountUnchanged = map[string]string{
	"authn.humanverify": "its challenge route is mounted only when authn.humanverify.mode is pow; " +
		"the review configuration states the mode as off, so this removal is vacuous",
}

// TestReviewRouteCountExemptionsAreTrueForItsConfiguration holds the review
// build's exemption to its stated reason and holds the baseline template's
// workspace exemption out: the review configuration enables workspace.personal,
// so removing that module lowers the account endpoint's route count and an
// exemption claiming otherwise would be false. A configuration that leaves the
// gate to mount its challenge route reddens the human-verification half, and one
// that disables the personal workspace reddens the workspace half.
func TestReviewRouteCountExemptionsAreTrueForItsConfiguration(t *testing.T) {
	if reason, exempt := mayLeaveRouteCountUnchanged["workspace.personal"]; exempt {
		t.Errorf("the review build exempts workspace.personal (%q), but the review configuration "+
			"enables its route: the exemption is false for this build and the baseline table's "+
			"entry must not be shared into it", reason)
	}

	humanVerify := variantGroupNamed(t, "authn.humanverify")
	personal := variantGroupNamed(t, "workspace.personal")

	topo := topology{name: "exemptions", ports: listenerCount}
	before, after := removalRouteCounts(t, topo, []variantGroup{humanVerify, personal})
	assertRouteCountsUnchanged(t, humanVerify, before, after[humanVerify.id])
	assertRouteCountsLowered(t, personal, before, after[personal.id])
}
