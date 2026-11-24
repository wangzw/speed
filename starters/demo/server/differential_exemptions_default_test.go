package main

import (
	"fmt"
	"sort"
	"testing"
)

// mayLeaveRouteCountUnchanged names the routing modules whose removal cannot
// lower an endpoint's route count.
var mayLeaveRouteCountUnchanged = map[string]string{
	"authn.sso.saml": "the metadata route is mounted only while authn.sso.saml.enabled is true, and " +
		"the baseline template leaves the switch off: the method and the metadata route are not " +
		"delivered in the baseline, so this removal is vacuous",
	"sms.admin": "its management routes are mounted only on the endpoints sms.admin.endpoints names, " +
		"and the item has no default: the baseline template mounts no SMS management surface because " +
		"an SMS vendor account is not part of the baseline, so this removal is vacuous",
}

// TestTheBaselineRouteCountExemptionsAreTrueForItsConfiguration holds each baseline exemption to its
// reason, and holds that dropping the personal module fails the startup instead.
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
