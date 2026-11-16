package main

// The deployment template's placeholders, held to the configuration namespaces
// the capabilities read.

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
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

// TestTheDeployTemplateCarriesTheSocialCredentials holds the deployment
// template, rendered the way deploy/entrypoint.sh renders it, to the sections
// the two social login methods read. A method whose required items are empty
// fails the startup, so the placeholders have to land under the namespaces the
// methods decode: authn.login.google and authn.login.github.
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

// totpSection is the part of the TOTP factor's section these cases read.
type totpSection struct {
	Issuer string `config:"issuer"`
}

// TestTheDeployTemplateNamesTheSiteInTheAuthenticator holds the site name the
// deployment template writes into the otpauth address, which an authenticator
// app shows beside the account.
func TestTheDeployTemplateNamesTheSiteInTheAuthenticator(t *testing.T) {
	t.Parallel()
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
	t.Parallel()
	locator := writeConfigFile(t, renderDeployTemplate(t))
	var got webauthnSection
	readEffective(t, "deploy/fly.yaml.tmpl", locator, "authn.mfa.webauthn", &got)
	if got.RPName != "Speed Demo" {
		t.Errorf("authn.mfa.webauthn.rp-name = %q, want the site name Speed Demo", got.RPName)
	}
}

// humanverifySection is the part of the human-verification module's section the
// deployment template carries: the two surface modes, the widget's public site
// key, the siteverify secret, the hostname the widget must have run on and the
// action the widget is rendered with.
type humanverifySection struct {
	Mode      string `config:"mode"`
	PhoneMode string `config:"phone-mode"`
	SiteKey   string `config:"site-key"`
	SecretKey string `config:"secret-key"`
	Hostname  string `config:"hostname"`
	Action    string `config:"action"`
}

// TestTheDeployTemplateCarriesTheHumanVerificationSection holds the deployment
// template to the section the human-verification module decodes. The entrypoint
// defaults the non-phone surface to pow and the phone surface to captcha, and
// requires the Turnstile credentials only when a selected surface is turnstile;
// the template still has to place both modes, the credentials, the expected
// hostname and the widget's action under the namespace the module reads, so a
// deployment that turns the mechanism on finds them where it looks. The render
// names a different mode on each surface, so the two placeholders are shown to
// be wired to their own item and not to one another.
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

// placeholderPattern matches the placeholder tokens deploy/fly.yaml.tmpl holds
// for the entrypoint to render.
var placeholderPattern = regexp.MustCompile(`__[A-Z][A-Z0-9_]*__`)

// TestTheEntrypointRendersEveryDeploymentTemplatePlaceholder holds the two
// deploy files together. A placeholder the template carries and the entrypoint
// does not replace would reach the container as itself; the render helper the
// other cases use fills the placeholders on its own, so the wiring between the
// script and the template is asserted here.
func TestTheEntrypointRendersEveryDeploymentTemplatePlaceholder(t *testing.T) {
	t.Parallel()
	template, err := os.ReadFile(filepath.Join("..", "deploy", "fly.yaml.tmpl"))
	if err != nil {
		t.Fatalf("reading deploy/fly.yaml.tmpl: %v", err)
	}
	script, err := os.ReadFile(filepath.Join("..", "deploy", "entrypoint.sh"))
	if err != nil {
		t.Fatalf("reading deploy/entrypoint.sh: %v", err)
	}
	seen := make(map[string]bool)
	for _, placeholder := range placeholderPattern.FindAllString(string(template), -1) {
		if seen[placeholder] {
			continue
		}
		seen[placeholder] = true
		if !strings.Contains(string(script), `replace(line, "`+placeholder+`"`) {
			t.Errorf("deploy/fly.yaml.tmpl carries %s, but deploy/entrypoint.sh has no replacement for "+
				"it; a container would render the placeholder literally", placeholder)
		}
	}
}

// entrypointEnvironment is the environment the human-verification block of
// deploy/entrypoint.sh runs with: this process's variables with every DEMO_*
// entry removed, the inputs the script requires before the modes, and the
// case's overrides.
func entrypointEnvironment(overrides map[string]string) []string {
	values := map[string]string{
		"FLY_APP_NAME":              "demo-test",
		"DEMO_OPERATOR_USERNAME":    "operator",
		"DEMO_OPERATOR_EMAIL":       "operator@demo.test",
		"DEMO_OPERATOR_PASSWORD":    "operator-test-secret",
		"DEMO_DB_ENCRYPTION_KEY":    "test-only-encryption-key-0123456789",
		"DEMO_SMTP_HOST":            "smtp.example.test",
		"DEMO_SMTP_FROM":            "Demo <no-reply@demo.example.test>",
		"DEMO_GOOGLE_CLIENT_ID":     "google-client.example.test",
		"DEMO_GOOGLE_CLIENT_SECRET": "google-client-secret",
		"DEMO_GITHUB_CLIENT_ID":     "github-client",
		"DEMO_GITHUB_CLIENT_SECRET": "github-client-secret",
	}
	for name, value := range overrides {
		values[name] = value
	}
	env := make([]string, 0, len(values)+8)
	for _, entry := range os.Environ() {
		if strings.HasPrefix(entry, "DEMO_") {
			continue
		}
		env = append(env, entry)
	}
	for name, value := range values {
		env = append(env, name+"="+value)
	}
	return env
}

// entrypointModes is what the human-verification block of the entrypoint
// decided for one environment: the mechanism on each surface, the Turnstile
// credentials and action it rendered, and the diagnostic when it refused the
// environment.
type entrypointModes struct {
	mode      string
	phoneMode string
	siteKey   string
	secretKey string
	action    string
	stderr    string
	err       error
}

// runEntrypointModes runs the human-verification block of the real
// deploy/entrypoint.sh. The script is read and cut at the line that creates the
// volume's directories, and its five answers are printed there. Everything the
// block depends on — the origin, the required secrets, both surface modes, the
// Turnstile credential rule and the action — therefore runs the way the
// container runs it, while the case leaves the filesystem alone.
func runEntrypointModes(t *testing.T, overrides map[string]string) entrypointModes {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "deploy", "entrypoint.sh"))
	if err != nil {
		t.Fatalf("reading deploy/entrypoint.sh: %v", err)
	}
	const marker = "mkdir -p /data/var /data/objects"
	at := strings.Index(string(raw), marker)
	if at < 0 {
		t.Fatalf("deploy/entrypoint.sh no longer reaches %q after resolving the human-verification "+
			"modes; this case cuts the script there", marker)
	}
	script := string(raw)[:at] +
		"printf '%s\\n' \"$humanverify_mode\" \"$humanverify_phone_mode\" \"$turnstile_site_key\" " +
		"\"$turnstile_secret_key\" \"$turnstile_action\"\n"
	cmd := exec.Command("sh", "-c", script)
	cmd.Env = entrypointEnvironment(overrides)
	var stdout, stderr strings.Builder
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	runErr := cmd.Run()
	got := entrypointModes{stderr: stderr.String(), err: runErr}
	if runErr != nil {
		if stdout.Len() != 0 {
			t.Fatalf("the entrypoint printed its modes as well as failing: %q\n%s", stdout.String(),
				got.stderr)
		}
		return got
	}
	lines := strings.Split(strings.TrimSuffix(stdout.String(), "\n"), "\n")
	if len(lines) != 5 {
		t.Fatalf("the entrypoint printed %d answers, want 5: %q", len(lines), stdout.String())
	}
	got.mode, got.phoneMode, got.siteKey, got.secretKey, got.action = lines[0], lines[1], lines[2], lines[3], lines[4]
	return got
}

// TestTheEntrypointDefaultsEachHumanVerificationSurface holds the container
// entrypoint's defaults: the non-phone surface runs the self-hosted proof of
// work and the phone surface the self-hosted captcha, so the demo gates every
// send without a Cloudflare account and renders no credential.
func TestTheEntrypointDefaultsEachHumanVerificationSurface(t *testing.T) {
	t.Parallel()
	got := runEntrypointModes(t, nil)
	if got.err != nil {
		t.Fatalf("running the entrypoint's human-verification block: %v\n%s", got.err, got.stderr)
	}
	if got.mode != "pow" {
		t.Errorf("the non-phone surface defaulted to %q, want pow", got.mode)
	}
	if got.phoneMode != "captcha" {
		t.Errorf("the phone surface defaulted to %q, want captcha", got.phoneMode)
	}
	if got.siteKey != "" || got.secretKey != "" {
		t.Errorf("the default gate rendered the Turnstile credentials %q and %q; neither self-hosted "+
			"mechanism reads them", got.siteKey, got.secretKey)
	}
	if got.action != "login" {
		t.Errorf("the widget's action defaulted to %q, want login", got.action)
	}
}

// TestTheEntrypointRendersTheConfiguredTurnstileAction holds the action the
// entrypoint places in the rendered section: DEMO_TURNSTILE_ACTION when the
// environment names one, and the login default otherwise.
func TestTheEntrypointRendersTheConfiguredTurnstileAction(t *testing.T) {
	t.Parallel()
	got := runEntrypointModes(t, map[string]string{"DEMO_TURNSTILE_ACTION": "login-sms"})
	if got.err != nil {
		t.Fatalf("running the entrypoint's human-verification block: %v\n%s", got.err, got.stderr)
	}
	if got.action != "login-sms" {
		t.Errorf("the entrypoint rendered action %q, want the configured login-sms", got.action)
	}
}

// TestTheEntrypointRequiresTurnstileCredentialsPerSelectedSurface holds the
// entrypoint's credential rule: the site key and the secret are required when
// either surface selects turnstile, and are not rendered when neither does,
// even when both are present in the environment.
func TestTheEntrypointRequiresTurnstileCredentialsPerSelectedSurface(t *testing.T) {
	t.Parallel()
	credentials := map[string]string{
		"DEMO_TURNSTILE_SITE_KEY":   "turnstile-site-key.example.test",
		"DEMO_TURNSTILE_SECRET_KEY": "turnstile-secret",
	}

	// A self-hosted pair renders no credential even when the environment
	// carries one.
	selfHosted := map[string]string{
		"DEMO_HUMANVERIFY_MODE":       "pow",
		"DEMO_HUMANVERIFY_PHONE_MODE": "captcha",
	}
	for name, value := range credentials {
		selfHosted[name] = value
	}
	got := runEntrypointModes(t, selfHosted)
	if got.err != nil {
		t.Fatalf("running the entrypoint's human-verification block: %v\n%s", got.err, got.stderr)
	}
	if got.siteKey != "" || got.secretKey != "" {
		t.Errorf("a self-hosted pair rendered the Turnstile credentials %q and %q; no mechanism on "+
			"either surface reads them", got.siteKey, got.secretKey)
	}

	// Either surface selecting turnstile makes both credentials a startup
	// requirement.
	for _, surface := range []string{"DEMO_HUMANVERIFY_MODE", "DEMO_HUMANVERIFY_PHONE_MODE"} {
		for _, missing := range []string{"DEMO_TURNSTILE_SITE_KEY", "DEMO_TURNSTILE_SECRET_KEY"} {
			env := map[string]string{surface: "turnstile"}
			for name, value := range credentials {
				env[name] = value
			}
			delete(env, missing)
			missingGot := runEntrypointModes(t, env)
			if missingGot.err == nil {
				t.Errorf("%s selects turnstile and %s is unset, but the entrypoint rendered mode %q, "+
					"phone-mode %q, site key %q and secret %q; the missing credential is a startup "+
					"failure", surface, missing, missingGot.mode, missingGot.phoneMode, missingGot.siteKey,
					missingGot.secretKey)
				continue
			}
			if !strings.Contains(missingGot.stderr, missing+" is not set") {
				t.Errorf("%s selects turnstile and %s is unset, but the entrypoint failed with %q; "+
					"the diagnostic does not name the missing value", surface, missing, missingGot.stderr)
			}
		}
	}

	// With both surfaces on Turnstile, the two credentials are rendered once.
	got = runEntrypointModes(t, map[string]string{
		"DEMO_HUMANVERIFY_MODE":       "turnstile",
		"DEMO_HUMANVERIFY_PHONE_MODE": "turnstile",
		"DEMO_TURNSTILE_SITE_KEY":     credentials["DEMO_TURNSTILE_SITE_KEY"],
		"DEMO_TURNSTILE_SECRET_KEY":   credentials["DEMO_TURNSTILE_SECRET_KEY"],
	})
	if got.err != nil {
		t.Fatalf("running the entrypoint with both surfaces on Turnstile: %v\n%s", got.err, got.stderr)
	}
	if got.mode != "turnstile" || got.phoneMode != "turnstile" {
		t.Errorf("both surfaces resolved to %q and %q, want turnstile and turnstile", got.mode, got.phoneMode)
	}
	if got.siteKey != credentials["DEMO_TURNSTILE_SITE_KEY"] ||
		got.secretKey != credentials["DEMO_TURNSTILE_SECRET_KEY"] {
		t.Errorf("the entrypoint rendered site key %q and secret %q, want the supplied %q and %q",
			got.siteKey, got.secretKey, credentials["DEMO_TURNSTILE_SITE_KEY"],
			credentials["DEMO_TURNSTILE_SECRET_KEY"])
	}
}

// smsSection is the part of the deployment template's SMS section these cases
// read. The template entry names every member of the module's template spec,
// because pkg/config decodes a container element whole and refuses a member no
// field answers for.
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

// TestTheDeployTemplateCarriesTheSMSCarrier holds the deployment template to
// the SMS family the phone sign-in runs on: the log carrier, so no vendor
// account is needed, and every template the authentication module requires
// plus the optional reset one. The template is rendered the way
// deploy/entrypoint.sh renders it and loaded through the same pkg/config entry
// point the binary uses, so a deployment that runs the template carries the
// carrier and templates this case asserts.
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
