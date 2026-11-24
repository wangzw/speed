package main

import (
	"slices"
	"testing"

	"github.com/vislake/speed/pkg/authn/humanverify"
	githublogin "github.com/vislake/speed/pkg/authn/login/github"
	googlelogin "github.com/vislake/speed/pkg/authn/login/google"
)

type socialCredentials struct {
	ClientID     string `config:"client-id"`
	ClientSecret string `config:"client-secret"`
}

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

// TestTheDeployTemplateKeepsTheReadinessOnTheOperationsListener holds that the rendered template keeps
// the probes on the operations listener, so fly.toml's readiness check reaches them.
func TestTheDeployTemplateKeepsTheReadinessOnTheOperationsListener(t *testing.T) {
	t.Parallel()
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

// TestTheDeployTemplateCarriesTheSocialCredentials holds that the rendered template lands the two
// social methods' credentials under their namespaces.
func TestTheDeployTemplateCarriesTheSocialCredentials(t *testing.T) {
	t.Parallel()
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

type totpSection struct {
	Issuer string `config:"issuer"`
}

// TestTheDeployTemplateNamesTheSiteInTheAuthenticator holds that the rendered template writes the site
// name into the otpauth address.
func TestTheDeployTemplateNamesTheSiteInTheAuthenticator(t *testing.T) {
	t.Parallel()
	locator := writeConfigFile(t, renderDeployTemplate(t))
	var got totpSection
	readEffective(t, "deploy/fly.yaml.tmpl", locator, "authn.mfa.totp", &got)
	if got.Issuer != "Speed Demo" {
		t.Errorf("authn.mfa.totp.issuer = %q, want the site name Speed Demo", got.Issuer)
	}
}

type webauthnSection struct {
	RPID    string   `config:"rp-id"`
	RPName  string   `config:"rp-name"`
	Origins []string `config:"origins"`
}

// TestTheDeployTemplateNamesTheSiteInThePasskeyPrompt holds that the rendered template writes the site
// name as the relying party's display name.
func TestTheDeployTemplateNamesTheSiteInThePasskeyPrompt(t *testing.T) {
	t.Parallel()
	locator := writeConfigFile(t, renderDeployTemplate(t))
	var got webauthnSection
	readEffective(t, "deploy/fly.yaml.tmpl", locator, "authn.mfa.webauthn", &got)
	if got.RPName != "Speed Demo" {
		t.Errorf("authn.mfa.webauthn.rp-name = %q, want the site name Speed Demo", got.RPName)
	}
}

type humanverifySection struct {
	Mode      string `config:"mode"`
	PhoneMode string `config:"phone-mode"`
	SiteKey   string `config:"site-key"`
	SecretKey string `config:"secret-key"`
	Hostname  string `config:"hostname"`
	Action    string `config:"action"`
}

// TestTheDeployTemplateCarriesTheHumanVerificationSection holds that the rendered template places both
// modes, the Turnstile credentials, the hostname and the action under the namespace the module reads.
func TestTheDeployTemplateCarriesTheHumanVerificationSection(t *testing.T) {
	t.Parallel()
	locator := writeConfigFile(t, renderDeployTemplate(t))
	var got humanverifySection
	readEffective(t, "deploy/fly.yaml.tmpl", locator, humanverify.ConfigNamespace, &got)
	want := humanverifySection{
		Mode:      "turnstile",
		PhoneMode: "pow",
		SiteKey:   "turnstile-site-key.example.test",
		SecretKey: "turnstile-client-secret",
		Hostname:  "demo.example.test",
		Action:    "login",
	}
	if got != want {
		t.Errorf("%s resolved to %+v, want %+v; the template's placeholders are not where the module "+
			"reads them", humanverify.ConfigNamespace, got, want)
	}
}

type smsSection struct {
	Carrier  string `config:"carrier"`
	Registry struct {
		Templates map[string]struct {
			Kind       string            `config:"kind"`
			Params     []string          `config:"params"`
			Codes      map[string]string `config:"codes"`
			Bodies     map[string]string `config:"bodies"`
			Attributes map[string]string `config:"attributes"`
		} `config:"templates"`
	} `config:"registry"`
}

// TestTheDeployTemplateCarriesTheSMSCarrier holds that the rendered template selects the log carrier
// and declares the required and reset templates.
func TestTheDeployTemplateCarriesTheSMSCarrier(t *testing.T) {
	t.Parallel()
	locator := writeConfigFile(t, renderDeployTemplate(t))
	var got smsSection
	readEffective(t, "deploy/fly.yaml.tmpl", locator, "sms", &got)

	if got.Carrier != "log" {
		t.Errorf("sms.carrier = %q, want the log carrier", got.Carrier)
	}
	for _, name := range []string{
		"authn.code.login",
		"authn.code.mfa",
		"authn.code.enroll",
		"authn.code.reset",
	} {
		spec, ok := got.Registry.Templates[name]
		if !ok {
			t.Errorf("sms.registry.templates carries no %q; the authentication module refuses a "+
				"registry that leaves a required template uncovered", name)
			continue
		}
		if spec.Kind != "verification" {
			t.Errorf("sms.registry.templates[%q].kind = %q, want verification", name, spec.Kind)
		}
		if want := []string{"code", "ttl_minutes"}; !slices.Equal(spec.Params, want) {
			t.Errorf("sms.registry.templates[%q].params = %v, want %v", name, spec.Params, want)
		}
		for _, language := range []string{"en-US", "zh-CN"} {
			if spec.Codes[language] == "" {
				t.Errorf("sms.registry.templates[%q] carries no %s code", name, language)
			}
		}
	}
}
