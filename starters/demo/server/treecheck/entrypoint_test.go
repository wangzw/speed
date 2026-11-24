package treecheck

import (
	"os"
	"os/exec"
	"regexp"
	"strings"
	"testing"

	"github.com/vislake/speed/starters/demo/server/internal/demotest"
)

const (
	deployTemplatePath = "../../deploy/fly.yaml.tmpl"
	entrypointPath     = "../../deploy/entrypoint.sh"
)

var placeholderPattern = regexp.MustCompile(`__[A-Z][A-Z0-9_]*__`)

// TestTheEntrypointRendersEveryDeploymentTemplatePlaceholder holds every placeholder of
// deploy/fly.yaml.tmpl to a replacement in deploy/entrypoint.sh.
func TestTheEntrypointRendersEveryDeploymentTemplatePlaceholder(t *testing.T) {
	t.Parallel()
	template, err := os.ReadFile(deployTemplatePath)
	if err != nil {
		t.Fatalf("reading deploy/fly.yaml.tmpl: %v", err)
	}
	script, err := os.ReadFile(entrypointPath)
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
	env := demotest.ChildEnv()
	if path, ok := os.LookupEnv("PATH"); ok {
		env = append(env, "PATH="+path)
	}
	for name, value := range values {
		env = append(env, name+"="+value)
	}
	return env
}

type entrypointModes struct {
	mode      string
	phoneMode string
	siteKey   string
	secretKey string
	action    string
	stderr    string
	err       error
}

func runEntrypointModes(t *testing.T, overrides map[string]string) entrypointModes {
	t.Helper()
	raw, err := os.ReadFile(entrypointPath)
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

// TestTheEntrypointDefaultsEachHumanVerificationSurface holds that the non-phone surface defaults to
// proof of work and the phone surface to captcha.
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

// TestTheEntrypointRendersTheConfiguredTurnstileAction holds DEMO_TURNSTILE_ACTION when named and the
// login default otherwise.
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

// TestTheEntrypointRequiresTurnstileCredentialsPerSelectedSurface holds that the two credentials are
// required exactly when a surface selects turnstile.
func TestTheEntrypointRequiresTurnstileCredentialsPerSelectedSurface(t *testing.T) {
	t.Parallel()
	credentials := map[string]string{
		"DEMO_TURNSTILE_SITE_KEY":   "turnstile-site-key.example.test",
		"DEMO_TURNSTILE_SECRET_KEY": "turnstile-secret",
	}

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
