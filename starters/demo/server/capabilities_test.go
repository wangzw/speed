package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"path/filepath"
	"testing"

	"github.com/vislake/speed/pkg/authn/invitemail"
	"github.com/vislake/speed/pkg/core"
)

// TestDefaultBuildCompilesEveryCapability holds that each name in capabilityModules is registered in
// this binary.
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

// TestTheLogCarriersAreInEveryBuild holds that both log carriers are compiled in as ordinary
// capability modules and named for a serve run.
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

// TestTheDefaultBuildCarriesTheInvitationNotifier holds that the invitation email channel is compiled
// in and named for serve in every build.
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

// TestDefaultBuildNamesTheDeployCapabilities holds the names the deployment variant contributed to be
// in the default build's ceiling.
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

// TestTheDefaultRunOffersNoSAMLSignIn holds that the default run, whose configuration never names the
// SAML switch, offers no SAML entry.
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
