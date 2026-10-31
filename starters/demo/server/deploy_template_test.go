package main

// The deployment template's placeholders, held to the configuration namespaces
// the capabilities read.

import (
	"testing"

	"github.com/vislake/speed/pkg/authn/humanverify"
	githublogin "github.com/vislake/speed/pkg/authn/login/github"
	googlelogin "github.com/vislake/speed/pkg/authn/login/google"
)

// socialCredentials is the part of a social method's section these cases read:
// the OAuth application's identifier and secret.
type socialCredentials struct {
	ClientID     string `config:"client-id"`
	ClientSecret string `config:"client-secret"`
}

// TestTheDeployTemplateCarriesTheSocialCredentials holds the deployment
// template, rendered the way deploy/entrypoint.sh renders it, to the sections
// the two social login methods read. A method whose required items are empty
// fails the startup, so the placeholders have to land under the namespaces the
// methods decode: authn.login.google and authn.login.github.
func TestTheDeployTemplateCarriesTheSocialCredentials(t *testing.T) {
	locator := writeConfigFile(t, renderDeployTemplate(t))
	for namespace, want := range map[string]socialCredentials{
		googlelogin.ConfigNamespace: {
			ClientID:     "google-client.example.test",
			ClientSecret: "google-client-secret",
		},
		githublogin.ConfigNamespace: {
			ClientID:     "github-client",
			ClientSecret: "github-client-secret",
		},
	} {
		var got socialCredentials
		readEffective(t, "deploy/fly.yaml.tmpl", locator, namespace, &got)
		if got != want {
			t.Errorf("%s resolved to %+v, want %+v; the template's placeholder for that item is not "+
				"where the method reads it", namespace, got, want)
		}
	}
}

// humanverifySection is the part of the human-verification module's section the
// deployment template carries: the mechanism, the widget's public site key, the
// siteverify secret and the hostname the widget must have run on.
type humanverifySection struct {
	Mode      string `config:"mode"`
	SiteKey   string `config:"site-key"`
	SecretKey string `config:"secret-key"`
	Hostname  string `config:"hostname"`
}

// TestTheDeployTemplateCarriesTheHumanVerificationSection holds the deployment
// template to the section the human-verification module decodes. The entrypoint
// defaults the mode to off and requires the Turnstile credentials only when the
// mode is turnstile; the template still has to place the mode, both credentials
// and the expected hostname under the namespace the module reads, so a
// deployment that turns the mechanism on finds them where it looks. The render
// here names the turnstile mode because that is the mode every item belongs to.
func TestTheDeployTemplateCarriesTheHumanVerificationSection(t *testing.T) {
	locator := writeConfigFile(t, renderDeployTemplate(t))
	var got humanverifySection
	readEffective(t, "deploy/fly.yaml.tmpl", locator, humanverify.ConfigNamespace, &got)
	want := humanverifySection{
		Mode:      "turnstile",
		SiteKey:   "turnstile-site-key.example.test",
		SecretKey: "turnstile-client-secret",
		Hostname:  "demo.example.test",
	}
	if got != want {
		t.Errorf("%s resolved to %+v, want %+v; the template's placeholders are not where the module "+
			"reads them", humanverify.ConfigNamespace, got, want)
	}
}
