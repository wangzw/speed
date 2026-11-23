package main

// The compile-time ceiling: every capability the program can ever run is
// registered in this binary. A capability that is not imported cannot be
// enabled at run time, so the import set is the ceiling.

import (
	"encoding/json"
	"fmt"
	"net/http"
	"path/filepath"
	"testing"

	"github.com/vislake/speed/pkg/authn/invitemail"
	"github.com/vislake/speed/pkg/core"
)

// TestDefaultBuildCompilesEveryCapability holds the ceiling: each name in
// capabilityModules is registered in the binary. Before the capability roster
// became unconditional, the social methods, the SMS family, the mail carriers,
// the verification code sender and the notification fixture were compiled only
// under deploy, so this lookup failed for them.
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
// compiled in and named for a serve run, not test-only support. A deployment
// can therefore configure them, and every run reaches them through the same
// roster.
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

// TestDefaultBuildNamesTheDeployCapabilities holds the names the deployment
// variant contributed: the deployment's mail and social paths and the extra
// capabilities are all in the default build's ceiling now, and a rename in any
// package fails here instead of silently dropping a capability from every
// build.
func TestDefaultBuildNamesTheDeployCapabilities(t *testing.T) {
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
			t.Errorf("capabilityModules does not name %q; the binary no longer carries "+
				"a capability a former deployment variant added", name)
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
