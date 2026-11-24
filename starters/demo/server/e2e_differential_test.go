package main

import (
	"fmt"
	"testing"
)

func e2eDifferentialConfigBody(t *testing.T, ports []int, dsn string) string {
	t.Helper()
	return e2eConfigBody(t, t.TempDir(),
		fmt.Sprintf("127.0.0.1:%d", ports[backendListener]), dsn)
}

// TestTheE2EConfigurationLowersTheRouteCountOfItsSwitches holds that over the
// e2e configuration each exempted module's removal lowers an endpoint's count.
func TestTheE2EConfigurationLowersTheRouteCountOfItsSwitches(t *testing.T) {
	t.Parallel()
	groups := []variantGroup{
		variantGroupNamed(t, "authn.sso.saml"),
		variantGroupNamed(t, "sms.admin"),
	}
	topo := topology{name: "e2e", ports: listenerCount, body: e2eDifferentialConfigBody}
	before, after := removalRouteCounts(t, topo, groups)
	for _, group := range groups {
		assertRouteCountsLowered(t, group, before, after[group.id])
	}
}

func assertRouteCountsLowered(t *testing.T, group variantGroup, before, after map[string]chainLine) {
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
			if now.routes >= was.routes {
				t.Errorf("dropping %v left endpoint %q with %d route(s), not fewer than the baseline's %d: "+
					"the removal is not vacuous, so an exemption here would be false",
					group.modules, endpoint, now.routes, was.routes)
			}
		}
	}
	if !measured {
		t.Errorf("the variant %q describes no endpoint, so its removal cannot be measured", group.id)
	}
}
