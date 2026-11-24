package otlp

import (
	"crypto/x509"
	"os"
	"strings"
	"testing"
)

const ignoredLineText = "standard OpenTelemetry variables are set and have no effect"

// withoutOverriddenVariables unsets every variable on this module's list for
// the rest of the case, so that a case reading what New logged reads the same
// whatever the environment the tests run in sets.
func withoutOverriddenVariables(t *testing.T) {
	t.Helper()
	for _, v := range overriddenVariables {
		t.Setenv(v.name, "")
		if err := os.Unsetenv(v.name); err != nil {
			t.Fatalf("unsetting %s: %v", v.name, err)
		}
	}
}

var offTheList = map[string]string{
	"OTEL_EXPORTER_OTLP_TIMEOUT":             "5000",
	"OTEL_EXPORTER_OTLP_TRACES_TIMEOUT":      "5000",
	"OTEL_EXPORTER_OTLP_METRICS_COMPRESSION": "gzip",
	"OTEL_SERVICE_NAME":                      "named-by-the-environment",
	"OTEL_TRACES_SAMPLER":                    "always_on",
}

// TestASetStandardVariableThisModuleOverridesIsNamedOnce pins the line
// that tells a deployment its variable has no effect.
func TestASetStandardVariableThisModuleOverridesIsNamedOnce(t *testing.T) {
	ca := newAuthority(t)
	client := ca.issue(t, "client", x509.ExtKeyUsageClientAuth)
	type variable struct{ name, value, item string }
	var overridden []variable
	for _, signal := range []string{"", "TRACES_", "METRICS_"} {
		prefix := "OTEL_EXPORTER_OTLP_" + signal
		overridden = append(overridden,
			variable{prefix + "ENDPOINT", "http://endpoint-token-6d2f.example:4318", endpointKey},
			variable{prefix + "HEADERS", "x-header-token-6d2f=header-value-6d2f", ModuleName + ".headers"},
			variable{prefix + "INSECURE", "true", insecureKey},
			variable{prefix + "CERTIFICATE", ca.file, caFileKey + ", with " + insecureKey + " false"},
			variable{
				prefix + "CLIENT_CERTIFICATE", client.certFile,
				clientCertFileKey + ", with " + insecureKey + " false",
			},
			variable{prefix + "CLIENT_KEY", client.keyFile, clientKeyFileKey + ", with " + insecureKey + " false"},
		)
	}
	overridden = append(overridden,
		variable{"OTEL_EXPORTER_OTLP_PROTOCOL", "http/json", ModuleName + ".protocol"},
		variable{"OTEL_EXPORTER_OTLP_TRACES_PROTOCOL", "http/json", ModuleName + ".protocol"},
		variable{"OTEL_EXPORTER_OTLP_METRICS_TEMPORALITY_PREFERENCE", "delta", "none, fixed by this module"},
		variable{
			"OTEL_EXPORTER_OTLP_METRICS_DEFAULT_HISTOGRAM_AGGREGATION", "base2_exponential_bucket_histogram",
			"none, fixed by this module",
		},
	)
	for _, v := range overridden {
		t.Setenv(v.name, v.value)
	}
	for name, value := range offTheList {
		t.Setenv(name, value)
	}

	configured := newAuthority(t)
	transports := []struct {
		name  string
		apply func(*Config)
	}{
		{"plaintext", func(c *Config) { c.Insecure = true }},
		{"tls", func(c *Config) { c.CAFile = configured.file }},
	}
	for _, protocol := range []string{ProtocolGRPC, ProtocolHTTP} {
		for _, transport := range transports {
			cfg := Config{Endpoint: "127.0.0.1:4317", Protocol: protocol, ProbeTimeout: defaultProbeTimeout}
			transport.apply(&cfg)
			t.Run(protocol+"/"+transport.name, func(t *testing.T) {
				logged, names := exportersLogging(t, cfg)
				lines := strings.Split(strings.TrimSpace(logged), "\n")
				if len(lines) != 1 || !strings.Contains(lines[0], ignoredLineText) {
					t.Fatalf("the module logged %q, want exactly one line naming the variables it overrides", logged)
				}
				if len(names) != 1 || names[0] != ModuleName {
					t.Errorf("the logger was asked for under %q, want %q alone", names, ModuleName)
				}
				line := lines[0]
				if !strings.Contains(line, "level=WARN") {
					t.Errorf("the line is %q, want a warning", line)
				}
				for _, v := range overridden {
					if !strings.Contains(line, v.name+": "+v.item) {
						t.Errorf("the line is %q, want it to name %s and %q", line, v.name, v.item)
					}
				}
				for _, leaked := range []string{
					"6d2f", "http/json", "delta", "base2_exponential", ca.file,
					client.certFile, client.keyFile,
				} {
					if strings.Contains(logged, leaked) {
						t.Errorf("the line is %q, and it repeats the value %q", line, leaked)
					}
				}
				for name := range offTheList {
					if strings.Contains(logged, name) {
						t.Errorf("the line is %q, and it names %s, which is not on the list", line, name)
					}
				}
			})
		}
	}
}

// TestNoLineWhenNoOverriddenVariableIsSet pins the other side.
func TestNoLineWhenNoOverriddenVariableIsSet(t *testing.T) {
	for _, signal := range []string{"", "TRACES_", "METRICS_"} {
		for _, suffix := range []string{"ENDPOINT", "HEADERS", "INSECURE", "CERTIFICATE", "CLIENT_CERTIFICATE"} {
			name := "OTEL_EXPORTER_OTLP_" + signal + suffix
			t.Setenv(name, "")
			if err := os.Unsetenv(name); err != nil {
				t.Fatalf("unsetting %s: %v", name, err)
			}
		}
		t.Setenv("OTEL_EXPORTER_OTLP_"+signal+"CLIENT_KEY", "")
	}
	t.Setenv("OTEL_EXPORTER_OTLP_PROTOCOL", " ")
	t.Setenv("OTEL_EXPORTER_OTLP_TRACES_PROTOCOL", "")
	t.Setenv("OTEL_EXPORTER_OTLP_METRICS_TEMPORALITY_PREFERENCE", "")
	t.Setenv("OTEL_EXPORTER_OTLP_METRICS_DEFAULT_HISTOGRAM_AGGREGATION", "")
	for name, value := range offTheList {
		t.Setenv(name, value)
	}

	logged, _ := exportersLogging(t, Config{
		Endpoint:     "127.0.0.1:4317",
		Protocol:     ProtocolGRPC,
		Insecure:     true,
		ProbeTimeout: defaultProbeTimeout,
	})
	if logged != "" {
		t.Errorf("the module logged %q, want nothing: no overridden variable is set", logged)
	}
}
