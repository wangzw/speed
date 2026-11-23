package main

import (
	"fmt"
	"testing"
)

// e2eDifferentialConfigBody renders the e2e configuration for a differential
// child: the listener, database, signing key and object store move under a
// temporary root, and every other value is the assembly's own. It is the
// configuration side of the case below, so the switches it turns on are the
// ones the removal is measured over.
func e2eDifferentialConfigBody(t *testing.T, ports []int, dsn string) string {
	t.Helper()
	return e2eConfigBody(t, t.TempDir(),
		fmt.Sprintf("127.0.0.1:%d", ports[backendListener]), dsn)
}

// TestTheE2EConfigurationLowersTheRouteCountOfItsSwitches holds the baseline
// route-count exemptions to the configuration they were written for. The
// baseline template leaves authn.sso.saml and sms.admin off, so dropping
// either changes no route count and the exemption table records a vacuous
// removal. The e2e configuration turns both on: SAML mounts its metadata
// route, and sms.admin names the admin endpoint in its endpoints item. Over
// that configuration each removal has to lower an endpoint's route count, so
// an exemption carried across from the baseline would be false here.
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

// assertRouteCountsLowered holds a removal that no exemption may excuse: every
// endpoint the removed modules describe loses routes. A configuration that
// keeps the count unchanged reddens this assertion, which is what proves the
// baseline's exemption does not hold for it.
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
