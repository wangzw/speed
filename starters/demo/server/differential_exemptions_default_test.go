package main

import (
	"fmt"
	"sort"
	"testing"
)

// mayLeaveRouteCountUnchanged is the exemption table: the routing modules whose
// removal cannot lower an endpoint's route count over the baseline template,
// each with the reason. A module that mounts its routes only when a switch is on
// mounts none here, so dropping it changes no route count and the removal check
// cannot tell a real module from a name that registered nothing. The check stays
// fail-closed otherwise: every other vacuous removal fails.
//
// org.personal is not exempt from the route-count table, but its removal is not
// measurable there: the account module requires the provisioning seam it
// provides, so a run without it does not start at all. That failure is held
// instead. See TestTheBaselineRouteCountExemptionsAreTrueForItsConfiguration.
var mayLeaveRouteCountUnchanged = map[string]string{
	"authn.sso.saml": "the metadata route is mounted only while authn.sso.saml.enabled is true, and " +
		"the baseline template leaves the switch off: the method and the metadata route are not " +
		"delivered in the baseline, so this removal is vacuous",
	"sms.admin": "its management routes are mounted only on the endpoints sms.admin.endpoints names, " +
		"and the item has no default: the baseline template mounts no SMS management surface because " +
		"an SMS vendor account is not part of the baseline, so this removal is vacuous",
}

// TestTheBaselineRouteCountExemptionsAreTrueForItsConfiguration holds each
// baseline exemption to its stated reason over the template this build's case
// runs: dropping the module leaves every endpoint it describes at the same
// route count. It also holds the personal module out of the table: the baseline
// names org.personal and the module is always in force, so an exemption
// claiming its removal is vacuous would be false. Since the account module
// requires the provisioning seam this module provides, dropping it fails the
// startup instead of lowering a route count; the case holds that failure.
func TestTheBaselineRouteCountExemptionsAreTrueForItsConfiguration(t *testing.T) {
	t.Parallel()
	names := make([]string, 0, len(mayLeaveRouteCountUnchanged))
	for name := range mayLeaveRouteCountUnchanged {
		names = append(names, name)
	}
	sort.Strings(names)

	groups := make([]variantGroup, 0, len(names))
	for _, name := range names {
		groups = append(groups, variantGroupNamed(t, name))
	}
	personal := variantGroupNamed(t, "org.personal")
	if reason, exempt := mayLeaveRouteCountUnchanged["org.personal"]; exempt {
		t.Errorf("the baseline build exempts org.personal (%q), but the compiled default "+
			"enables its route: the exemption is false for this build", reason)
	}

	topo := topology{name: "exemptions", ports: listenerCount}
	before, after := removalRouteCounts(t, topo, groups)
	for _, group := range groups {
		assertRouteCountsUnchanged(t, group, before, after[group.id])
	}

	variant, _, err := startVariant(t, topo, personal.modules)
	if miss := startupFailureMiss(variant, err, fmt.Sprintf(
		"the account module's required provisioning seam to stop a run without %v", personal.modules)); miss != "" {
		t.Fatal(miss)
	}
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
