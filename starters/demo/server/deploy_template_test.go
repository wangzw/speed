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

// deployHTTPSection is the part of the deployment template's http section
// these cases read. The endpoint entry names every member the template writes,
// because pkg/config decodes a container element whole and refuses a member no
// field answers for.
type deployHTTPSection struct {
	Listeners map[string]struct {
		Address string `config:"address"`
	} `config:"listeners"`
	Endpoints map[string]struct {
		Listener     string   `config:"listener"`
		Prefixes     []string `config:"prefixes"`
		MaxBodyBytes *int64   `config:"max-body-bytes"`
	} `config:"endpoints"`
}

// TestTheDeployTemplateKeepsTheReadinessOnTheOperationsListener holds the
// deployment's own claim table: the compiled defaults put the operations
// probes on the backend listener, and the deployment keeps them on the
// operations listener so fly.toml's readiness check — GET /ready on port 8083
// — reaches the endpoint it names. The template therefore writes the endpoint
// table whole (http.endpoints is a container leaf), and this case reads the
// effective configuration the rendered template produces.
func TestTheDeployTemplateKeepsTheReadinessOnTheOperationsListener(t *testing.T) {
	locator := writeConfigFile(t, renderDeployTemplate(t))
	var got deployHTTPSection
	readEffective(t, "deploy/fly.yaml.tmpl", locator, "http", &got)

	for name, want := range map[string]string{
		"backend": "0.0.0.0:8080",
		"ops":     "0.0.0.0:8083",
	} {
		if address := got.Listeners[name].Address; address != want {
			t.Errorf("http.listeners.%s.address = %q, want %q", name, address, want)
		}
	}
	if listener := got.Endpoints["ops"].Listener; listener != "ops" {
		t.Errorf("http.endpoints.ops.listener = %q, want the ops listener, so GET /ready on port 8083 reaches it",
			listener)
	}
	if listener := got.Endpoints["web"].Listener; listener != "backend" {
		t.Errorf("http.endpoints.web.listener = %q, want the backend listener", listener)
	}
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

// totpSection is the part of the TOTP factor's section these cases read.
type totpSection struct {
	Issuer string `config:"issuer"`
}

// TestTheDeployTemplateNamesTheSiteInTheAuthenticator holds the site name the
// deployment template writes into the otpauth address, which an authenticator
// app shows beside the account.
func TestTheDeployTemplateNamesTheSiteInTheAuthenticator(t *testing.T) {
	locator := writeConfigFile(t, renderDeployTemplate(t))
	var got totpSection
	readEffective(t, "deploy/fly.yaml.tmpl", locator, "authn.mfa.totp", &got)
	if got.Issuer != "Speed Demo" {
		t.Errorf("authn.mfa.totp.issuer = %q, want the site name Speed Demo", got.Issuer)
	}
}

// webauthnSection is the part of the WebAuthn factor's section these cases
// read. The section's other two items are named because pkg/config decodes a
// container element whole and refuses a member no field answers for.
type webauthnSection struct {
	RPID    string   `config:"rp-id"`
	RPName  string   `config:"rp-name"`
	Origins []string `config:"origins"`
}

// TestTheDeployTemplateNamesTheSiteInThePasskeyPrompt holds the site name the
// deployment template writes as the relying party's display name, which the
// browser's WebAuthn prompt shows beside the account.
func TestTheDeployTemplateNamesTheSiteInThePasskeyPrompt(t *testing.T) {
	locator := writeConfigFile(t, renderDeployTemplate(t))
	var got webauthnSection
	readEffective(t, "deploy/fly.yaml.tmpl", locator, "authn.mfa.webauthn", &got)
	if got.RPName != "Speed Demo" {
		t.Errorf("authn.mfa.webauthn.rp-name = %q, want the site name Speed Demo", got.RPName)
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
