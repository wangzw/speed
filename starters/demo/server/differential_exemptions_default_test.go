//go:build !review

package main

import (
	"sort"
	"testing"
)

// mayLeaveRouteCountUnchanged is the baseline build's exemption table: the
// routing modules whose removal cannot lower an endpoint's route count over the
// baseline template, each with the reason. A module that mounts its routes only
// when a switch is on mounts none here, so dropping it changes no route count
// and the removal check cannot tell a real module from a name that registered
// nothing. The check stays fail-closed otherwise: every other vacuous removal
// fails. The review build has its own table in
// differential_exemptions_review_test.go, because its configuration sets these
// switches differently.
var mayLeaveRouteCountUnchanged = map[string]string{
	"workspace.personal": "its route is mounted only when workspace.personal.enabled is on; " +
		"the baseline template leaves the host switch off (the baseline default), so this " +
		"removal is vacuous",
	"authn.humanverify": "its challenge route is mounted only when authn.humanverify.mode is pow; " +
		"the baseline template leaves the gate off (the baseline default), so this removal is vacuous",
}

// TestTheBaselineRouteCountExemptionsAreTrueForItsConfiguration holds each
// baseline exemption to its stated reason over the template this build's case
// runs: dropping the module leaves every endpoint it describes at the same
// route count. Turning the workspace switch on or the gate to pow mounts the
// route, makes the removal lower the count, and reddens this case -- which is
// what retires the exemption.
func TestTheBaselineRouteCountExemptionsAreTrueForItsConfiguration(t *testing.T) {
	names := make([]string, 0, len(mayLeaveRouteCountUnchanged))
	for name := range mayLeaveRouteCountUnchanged {
		names = append(names, name)
	}
	sort.Strings(names)

	groups := make([]variantGroup, 0, len(names))
	for _, name := range names {
		groups = append(groups, variantGroupNamed(t, name))
	}

	topo := topology{name: "exemptions", ports: listenerCount}
	before, after := removalRouteCounts(t, topo, groups)
	for _, group := range groups {
		assertRouteCountsUnchanged(t, group, before, after[group.id])
	}
}
