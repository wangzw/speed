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
//
// org.personal is not exempt: the compiled default turns its switch on, so
// the baseline template mounts its route and removing it lowers the account
// endpoint's route count. See TestTheBaselineRouteCountExemptionsAreTrueForItsConfiguration.
var mayLeaveRouteCountUnchanged = map[string]string{
	"sms.admin": "its management routes are mounted only on the endpoints sms.admin.endpoints names, " +
		"and the item has no default: the baseline template mounts no SMS management surface because " +
		"an SMS vendor account is not part of the baseline, so this removal is vacuous",
}

// TestTheBaselineRouteCountExemptionsAreTrueForItsConfiguration holds each
// baseline exemption to its stated reason over the template this build's case
// runs: dropping the module leaves every endpoint it describes at the same
// route count. It also holds the personal module out of the table: the compiled
// default enables org.personal, so removing it lowers the account
// endpoint's route count and an exemption claiming otherwise would be false. A
// configuration that turns the switch off reddens the organization half, which is
// the check retiring a default that moved back.
func TestTheBaselineRouteCountExemptionsAreTrueForItsConfiguration(t *testing.T) {
	names := make([]string, 0, len(mayLeaveRouteCountUnchanged))
	for name := range mayLeaveRouteCountUnchanged {
		names = append(names, name)
	}
	sort.Strings(names)

	groups := make([]variantGroup, 0, len(names)+1)
	for _, name := range names {
		groups = append(groups, variantGroupNamed(t, name))
	}
	personal := variantGroupNamed(t, "org.personal")
	if reason, exempt := mayLeaveRouteCountUnchanged["org.personal"]; exempt {
		t.Errorf("the baseline build exempts org.personal (%q), but the compiled default "+
			"enables its route: the exemption is false for this build", reason)
	}

	topo := topology{name: "exemptions", ports: listenerCount}
	before, after := removalRouteCounts(t, topo, append(groups, personal))
	for _, group := range groups {
		assertRouteCountsUnchanged(t, group, before, after[group.id])
	}
	assertRouteCountsLowered(t, personal, before, after[personal.id])
}

// assertRouteCountsUnchanged holds an exemption's stated reason to the run:
// every endpoint the removed modules describe keeps its route count, so the
// removal is vacuous and only the build's exemption lets it pass. A
// configuration that mounts the route reddens this assertion, which is what
// retires the exemption.
func assertRouteCountsUnchanged(t *testing.T, group variantGroup, before, after map[string]chainLine) {
	t.Helper()
	measured := false
	for _, removed := range group.modules {
		for _, endpoint := range endpointOfModule(t, removed) {
			measured = true
			was, known := before[endpoint]
			now, seen := after[endpoint]
			if !known || !seen {
				t.Errorf("the run wrote no chain for endpoint %q, so the removal of %q cannot be measured",
					endpoint, removed)
				continue
			}
			if now.routes != was.routes {
				t.Errorf("dropping %v left endpoint %q with %d route(s), want the baseline's unchanged %d: "+
					"the exemption's stated reason does not hold for this configuration",
					group.modules, endpoint, now.routes, was.routes)
			}
		}
	}
	if !measured {
		t.Errorf("the variant %q describes no endpoint, so its removal cannot be measured", group.id)
	}
}
