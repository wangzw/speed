package main

// The default build's compile-time ceiling: every capability the program can
// ever run is registered in this binary, and the review-only test controls are
// not. This is the property the build-tag split used to carry in the other
// direction; a capability that is not imported cannot be enabled at run time,
// so the import set is the ceiling.

import (
	"encoding/json"
	"fmt"
	"net/http"
	"path/filepath"
	"testing"

	"github.com/vislake/speed/pkg/authn/invitemail"
	"github.com/vislake/speed/pkg/core"
	"github.com/vislake/speed/pkg/feature"
)

// reviewOnlyModuleNames are the modules that must never exist in a default
// build: the SMS verdict control and the loopback guard. They are written as
// literals on purpose — importing or naming their constants would tie this
// default-build case to review-tagged code.
var reviewOnlyModuleNames = []string{"review.sms.control", "review.guard"}

// reviewOnlyFeatureNames are the features only the review controls declare.
var reviewOnlyFeatureNames = []string{"review.sms.control", "demo.notification.control"}

// TestDefaultBuildCompilesEveryCapability holds the ceiling: each name in
// capabilityModules is registered in a binary built without any tag. Before the
// capability roster became unconditional, the social methods, the SMS family,
// the mail carriers, the verification code sender and the notification fixture
// were compiled only under deploy or review, so this lookup failed for them.
func TestDefaultBuildCompilesEveryCapability(t *testing.T) {
	t.Parallel()
	if len(capabilityModules) == 0 {
		t.Fatal("capabilityModules names no module: the capability ceiling is empty")
	}
	seen := make(map[string]bool, len(capabilityModules))
	for _, name := range capabilityModules {
		if name == "" {
			t.Errorf("capabilityModules carries an empty name")
			continue
		}
		if seen[name] {
			t.Errorf("capabilityModules names %q more than once", name)
		}
		seen[name] = true
		if _, ok := core.ProcessRegistry.Lookup(name); !ok {
			t.Errorf("the default build registers no module %q: it is named in capabilityModules "+
				"but not imported, so no run could ever enable it", name)
		}
	}
}

// TestTheLogCarriersAreInEveryBuild holds requirement 2 of the carrier
// selection design: the two log carriers are ordinary capability modules,
// compiled into the default build and named for a serve run, not review-only
// test support. A deployment can therefore fall back to them, and a review run
// reaches them through the same roster.
func TestTheLogCarriersAreInEveryBuild(t *testing.T) {
	t.Parallel()
	named := make(map[string]bool, len(capabilityModules))
	for _, name := range capabilityModules {
		named[name] = true
	}
	for _, name := range []string{"mail.log", "sms.log"} {
		if _, ok := core.ProcessRegistry.Lookup(name); !ok {
			t.Errorf("the default build registers no %q log carrier", name)
		}
		if !named[name] {
			t.Errorf("capabilityModules does not name the %q log carrier, so a serve run cannot "+
				"consider it", name)
		}
	}
}

// TestTheDefaultBuildCarriesTheInvitationNotifier holds the invitation email
// channel in the ceiling: it is compiled into every build and named for serve,
// so a deployment that sets its accept base URL sends the notice and a bare run
// reports not_configured, without a separate binary.
func TestTheDefaultBuildCarriesTheInvitationNotifier(t *testing.T) {
	t.Parallel()
	if _, ok := core.ProcessRegistry.Lookup(invitemail.ModuleName); !ok {
		t.Errorf("the default build registers no %q", invitemail.ModuleName)
	}
	for _, name := range capabilityModules {
		if name == invitemail.ModuleName {
			return
		}
	}
	t.Errorf("capabilityModules does not name %q, so no serve run considers it",
		invitemail.ModuleName)
}

// TestDefaultBuildCarriesNoReviewControl holds the other side of the split: the
// review-only controls are not in this binary at all, so no configuration can
// enable them, and the features they declare are absent from the vocabulary.
func TestDefaultBuildCarriesNoReviewControl(t *testing.T) {
	t.Parallel()
	for _, name := range reviewOnlyModuleNames {
		if module, ok := core.ProcessRegistry.Lookup(name); ok {
			t.Errorf("the default build registers the review-only control %q (%+v); test controls "+
				"belong behind the review tag", name, module)
		}
	}
	declared := make(map[string]bool)
	for _, resource := range core.Resources[feature.Spec](core.ProcessRegistry) {
		declared[resource.Value.Name] = true
	}
	for _, name := range reviewOnlyFeatureNames {
		if declared[name] {
			t.Errorf("the default build declares the review-only feature %q; the review controls "+
				"must not reach a deployment or baseline binary", name)
		}
	}
}

// TestDefaultBuildNamesTheDeployAndReviewCapabilities holds the names the two
// former build variants contributed: the deployment's mail and social paths and
// the review assembly's extra capabilities are all in the default build's
// ceiling now, and a rename in either package fails here instead of silently
// dropping a capability from every build.
func TestDefaultBuildNamesTheDeployAndReviewCapabilities(t *testing.T) {
	t.Parallel()
	named := make(map[string]bool, len(capabilityModules))
	for _, name := range capabilityModules {
		named[name] = true
	}
	for _, name := range []string{
		"authn.login.google",
		"authn.login.github",
		"authn.login.wechat-mp",
		"authn.login.wechat-web",
		"authn.sso.oidc",
		"authn.sso.saml",
		"authn.sms",
		"sms.registry",
		"authn.login.sms",
		"authn.mfa.sms",
		"sms.admin",
		"mail.smtp",
		"mail.log",
		"authn.emailcode",
		"sms.log",
		"demo.notification",
	} {
		if !named[name] {
			t.Errorf("capabilityModules does not name %q; the default build no longer carries "+
				"a capability the deploy or review variant used to add", name)
		}
	}
}

// TestTheDefaultRunOffersNoSAMLSignIn serves the default assembly over a
// configuration that never names the SAML switch and reads the login discovery.
// The method is compiled into every build, but its own Prepare keeps it out of
// a run whose configuration does not turn it on, so the default deployment
// offers no SAML entry.
func TestTheDefaultRunOffersNoSAMLSignIn(t *testing.T) {
	t.Parallel()
	dsn := filepath.Join(t.TempDir(), "demo.db")
	config := writeConfigWith(t, freePorts(t, listenerCount), dsn)
	if res := runCommand(t, config, "sqlite", seedCommand); res.err != nil {
		t.Fatalf("seed failed: %v\n%s", res.err, res.combined())
	}
	_, ports := startServing(t, func(ports []int) *process {
		return start(t, writeConfigWith(t, ports, dsn), "sqlite", serveCommand)
	})

	base := fmt.Sprintf("http://127.0.0.1:%d", ports[backendListener])
	listing := get(t, base+"/api/v1/auth/methods")
	if listing.status != http.StatusOK {
		t.Fatalf("GET /api/v1/auth/methods answered %d: %s", listing.status, listing.body)
	}
	var discovery struct {
		Methods []struct {
			Name string `json:"name"`
		} `json:"methods"`
	}
	if err := json.Unmarshal(listing.body, &discovery); err != nil {
		t.Fatalf("reading the login discovery: %v\n%s", err, listing.body)
	}
	for _, method := range discovery.Methods {
		if method.Name == "saml" {
			t.Errorf("the login discovery lists saml although the configuration never turns "+
				"authn.sso.saml.enabled on: %+v", discovery.Methods)
		}
	}
}
