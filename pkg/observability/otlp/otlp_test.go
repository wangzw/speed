package otlp

import (
	"context"
	"crypto/x509"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vislake/speed/pkg/config"
	"github.com/vislake/speed/pkg/core"
)

func configured() *Config {
	cfg := prototype()
	cfg.Endpoint = "collector.example:4317"
	return cfg
}

// TestThePrototypeCarriesTheDefaults pins the deployable default.
func TestThePrototypeCarriesTheDefaults(t *testing.T) {
	cfg := prototype()
	if cfg.Protocol != ProtocolGRPC {
		t.Errorf("protocol defaults to %q, want %q", cfg.Protocol, ProtocolGRPC)
	}
	if !cfg.Insecure {
		t.Error("insecure defaults to false: a collector beside the process would need a certificate to " +
			"receive anything")
	}
	if cfg.ProbeAtStartup {
		t.Error("probe-at-startup defaults to true: a collector that starts after the process does is an " +
			"ordinary deployment, and the probe would turn it into a startup failure")
	}
	if cfg.ProbeTimeout <= 0 {
		t.Error("probe-timeout has no default, so an enabled probe would have no bound")
	}
}

// TestValidateNamesEachFailure covers one case of each sentinel this module
// judges; endpoint and header shapes are covered below.
func TestValidateNamesEachFailure(t *testing.T) {
	unknownProtocol := configured()
	unknownProtocol.Protocol = "carrier-pigeon"
	if err := unknownProtocol.validate(); !errors.Is(err, ErrUnknownProtocol) {
		t.Errorf("an unknown protocol gave %v, want %v", err, ErrUnknownProtocol)
	}
	if got := unknownProtocol.validate().Error(); !strings.Contains(got, ProtocolGRPC) || !strings.Contains(got, ProtocolHTTP) {
		t.Errorf("the text is %q, want it to list the accepted protocols: the set is closed, so listing "+
			"it is the whole of the fix", got)
	}

	badHeader := configured()
	badHeader.Headers = []string{"authorization"}
	if err := badHeader.validate(); !errors.Is(err, ErrInvalidHeader) {
		t.Errorf("a header without a value gave %v, want %v", err, ErrInvalidHeader)
	}

	badProbe := configured()
	badProbe.ProbeTimeout = 0
	if err := badProbe.validate(); !errors.Is(err, ErrInvalidDuration) {
		t.Errorf("a zero probe timeout gave %v, want %v", err, ErrInvalidDuration)
	}

	badEndpoint := configured()
	badEndpoint.Endpoint = "http://collector.example:4318"
	if err := badEndpoint.validate(); !errors.Is(err, ErrInvalidEndpoint) {
		t.Errorf("an endpoint written as a URL gave %v, want %v", err, ErrInvalidEndpoint)
	}
}

// TestAnUnreadableHeaderNeverPutsTheCredentialInTheError pins the one
// thing a header error must not do.
func TestAnUnreadableHeaderNeverPutsTheCredentialInTheError(t *testing.T) {
	cfg := configured()
	cfg.Headers = []string{"authorization Bearer sk-live-do-not-print"}
	err := cfg.validate()
	if !errors.Is(err, ErrInvalidHeader) {
		t.Fatalf("an unreadable header gave %v, want %v", err, ErrInvalidHeader)
	}
	if strings.Contains(err.Error(), "sk-live-do-not-print") {
		t.Errorf("the error text is %q, and it carries the credential", err)
	}
	if !strings.Contains(err.Error(), "element 1") {
		t.Errorf("the error text is %q, want it to say which element is wrong: with the content left out, "+
			"the position is all a reader has to locate it by", err)
	}

	// The same element written key=value would parse, so the case above is the
	// one where echoing the element is tempting and unsafe.
	withSeparator := configured()
	withSeparator.Headers = []string{"=sk-live-do-not-print"}
	if err := withSeparator.validate(); !errors.Is(err, ErrInvalidHeader) {
		t.Fatalf("an empty key gave %v, want %v", err, ErrInvalidHeader)
	} else if strings.Contains(err.Error(), "sk-live-do-not-print") {
		t.Errorf("the error text is %q, and it carries the credential", err)
	}
}

func prepareWith(t *testing.T, cfg *Config) error {
	t.Helper()
	reg := core.New()
	reg.Register(config.Module())
	reg.Supply(fixedReader{cfg: *cfg}, config.ModuleName)
	_, err := prepare(context.Background(), reg)
	return err
}

// TestAnEndpointWithASchemeAPathOrWhitespaceFailsThePreparation pins the
// shape check.
func TestAnEndpointWithASchemeAPathOrWhitespaceFailsThePreparation(t *testing.T) {
	for name, c := range map[string]struct{ endpoint, defect string }{
		"a scheme":          {"http://collector.example:4318", "observability.otlp.endpoint carries a scheme"},
		"a TLS scheme":      {"https://user:sk-live-do-not-print@collector.example", "carries a scheme"},
		"a gRPC dns target": {"dns:///collector.example:4317", "carries a scheme"},
		"a unix socket":     {"unix:///tmp/otel.sock", "carries a scheme"},
		"a path":            {"collector.example:4318/v1/traces", "observability.otlp.endpoint carries a path"},
		"nothing":           {"", "observability.otlp.endpoint is empty"},
		"a space":           {"collector.example :4317", "observability.otlp.endpoint contains whitespace"},
		"a trailing tab":    {"collector.example:4317\t", "contains whitespace"},
		"a bare path-slash": {"collector.example/", "carries a path"},
	} {
		t.Run(name, func(t *testing.T) {
			cfg := configured()
			cfg.Endpoint = c.endpoint
			err := prepareWith(t, cfg)
			if !errors.Is(err, ErrInvalidEndpoint) {
				t.Fatalf("the endpoint %q gave %v, want %v", c.endpoint, err, ErrInvalidEndpoint)
			}
			text := err.Error()
			for _, want := range []string{c.defect, "host:port", "no scheme", "no path"} {
				if !strings.Contains(text, want) {
					t.Errorf("the text is %q, want it to carry %q: it names the item and the form to write", text, want)
				}
			}
			if strings.Contains(text, "sk-live-do-not-print") {
				t.Errorf("the text is %q, and it repeats the endpoint's userinfo", text)
			}
		})
	}
}

// TestAHostWithAnOptionalPortPassesThePreparation pins the forms the
// shape check lets through.
func TestAHostWithAnOptionalPortPassesThePreparation(t *testing.T) {
	for _, endpoint := range []string{
		"collector.example:4317",
		"collector.example",
		"10.0.0.7:4318",
		"[::1]:4317",
		"[2001:db8::1]",
	} {
		cfg := configured()
		cfg.Endpoint = endpoint
		if err := prepareWith(t, cfg); err != nil {
			t.Errorf("the endpoint %q failed the preparation: %v", endpoint, err)
		}
	}
}

// TestTheSpecificationsNameForHTTPIsAccepted pins http/protobuf as a second
// spelling of http and the error naming it accepted.
func TestTheSpecificationsNameForHTTPIsAccepted(t *testing.T) {
	cfg := configured()
	cfg.Protocol = "http/protobuf"
	if err := prepareWith(t, cfg); err != nil {
		t.Errorf("http/protobuf failed the preparation: %v", err)
	}

	unknown := configured()
	unknown.Protocol = "http/json"
	err := prepareWith(t, unknown)
	if !errors.Is(err, ErrUnknownProtocol) {
		t.Fatalf("http/json gave %v, want %v", err, ErrUnknownProtocol)
	}
	if !strings.Contains(err.Error(), "http/protobuf") {
		t.Errorf("the text is %q, want it to list http/protobuf among the accepted names", err)
	}
}

// TestHeaderElementsAreReadWithoutTheirSurroundingWhitespace pins the
// reading of a list written the way a person writes one.
func TestHeaderElementsAreReadWithoutTheirSurroundingWhitespace(t *testing.T) {
	cfg := configured()
	cfg.Headers = []string{"authorization=Bearer token", " x-tenant=acme", "x-region = eu-west ", "\tx-empty="}
	got, err := cfg.headerMap()
	if err != nil {
		t.Fatalf("headerMap: %v", err)
	}
	want := map[string]string{
		"authorization": "Bearer token",
		"x-tenant":      "acme",
		"x-region":      "eu-west",
		"x-empty":       "",
	}
	if len(got) != len(want) {
		t.Errorf("headerMap read %q, want %q", got, want)
	}
	for key, value := range want {
		if v, ok := got[key]; !ok || v != value {
			t.Errorf("headerMap read %q, want %q: the key %q is missing or its value differs", got, want, key)
		}
	}
}

// TestAHeaderKeyWithWhitespaceInsideIsRefusedByPosition pins the other
// half of the trimming.
func TestAHeaderKeyWithWhitespaceInsideIsRefusedByPosition(t *testing.T) {
	for name, element := range map[string]string{
		"a space": "x api key=sk-live-do-not-print",
		"a tab":   "x\tkey=sk-live-do-not-print",
	} {
		t.Run(name, func(t *testing.T) {
			cfg := configured()
			cfg.Headers = []string{"x-tenant=acme", element}
			err := cfg.validate()
			if !errors.Is(err, ErrInvalidHeader) {
				t.Fatalf("a key with %s inside gave %v, want %v", name, err, ErrInvalidHeader)
			}
			text := err.Error()
			if !strings.Contains(text, "element 2") {
				t.Errorf("the text is %q, want it to name element 2", text)
			}
			for _, leaked := range []string{"sk-live-do-not-print", "x api key", "x\tkey", "x-tenant", "acme"} {
				if strings.Contains(text, leaked) {
					t.Errorf("the text is %q, and it repeats %q from the headers", text, leaked)
				}
			}
			for _, want := range []string{"commas", "main configuration source"} {
				if !strings.Contains(text, want) {
					t.Errorf("the text is %q, want it to carry %q: it says how the list is separated and "+
						"where a value with a comma goes", text, want)
				}
			}
			if !strings.Contains(text, "no whitespace") {
				t.Errorf("the text is %q, want it to say a key has no whitespace in it", text)
			}
		})
	}
}

// TestHeaderMapReadsThePairs pins the accepted shape.
func TestHeaderMapReadsThePairs(t *testing.T) {
	cfg := configured()
	cfg.Headers = []string{"authorization=Bearer token", "x-tenant=acme"}
	got, err := cfg.headerMap()
	if err != nil {
		t.Fatalf("headerMap: %v", err)
	}
	if got["authorization"] != "Bearer token" || got["x-tenant"] != "acme" {
		t.Errorf("headerMap read %v, want the two pairs as written", got)
	}
	if empty, err := configured().headerMap(); err != nil || empty != nil {
		t.Errorf("no headers gave %v and %v, want nil and nil", empty, err)
	}
}

// TestNoInputItemTakesItsValueFromTheCommandLine pins the rule for the
// whole namespace rather than for a list of items.
func TestNoInputItemTakesItsValueFromTheCommandLine(t *testing.T) {
	schema, ok := Module().Resources[0].(config.Schema)
	if !ok {
		t.Fatalf("the descriptor declares %T as its resource, want a config.Schema", Module().Resources[0])
	}
	for path, item := range schema.Items {
		if item.Origins&config.OriginFlag != 0 {
			t.Errorf("input item %q accepts a command-line origin, and its value would then be readable "+
				"from the process list", path)
		}
	}
	headers, ok := schema.Items["headers"]
	if !ok {
		t.Fatal("headers is not declared, so its metadata cannot be stated")
	}
	if !headers.Sensitive {
		t.Error("headers is not marked sensitive, so its values would reach the diagnostics and its " +
			"default the help output")
	}
	if headers.Description == "" {
		t.Error("headers is sensitive without a description, which the configuration layer refuses: a " +
			"reader whose default is not echoed has nothing else to go on")
	}
}

func fileHolding(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "file.pem")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("writing the file: %v", err)
	}
	return path
}

// TestACertificateItemThatCannotLoadFailsThePreparation pins that an unloadable
// certificate fails startup in Prepare, not every handshake.
func TestACertificateItemThatCannotLoadFailsThePreparation(t *testing.T) {
	ca := newAuthority(t)
	client := ca.issue(t, "client", x509.ExtKeyUsageClientAuth)
	other := ca.issue(t, "other", x509.ExtKeyUsageClientAuth)
	const marker = "marker-content-3f7a"
	notPEM := fileHolding(t, marker)
	labelled := fileHolding(t, "-----BEGIN "+marker+"-----\nAAAA\n-----END "+marker+"-----\n")
	missing := filepath.Join(t.TempDir(), "absent.pem")

	for name, c := range map[string]struct {
		set      func(*Config)
		sentinel error
		named    []string
	}{
		"a client certificate without its key": {
			func(c *Config) { c.ClientCertFile = client.certFile },
			ErrUnpairedClientCertificate,
			[]string{"observability.otlp.client-cert-file is set and observability.otlp.client-key-file is not"},
		},
		"a client key without its certificate": {
			func(c *Config) { c.ClientKeyFile = client.keyFile },
			ErrUnpairedClientCertificate,
			[]string{"observability.otlp.client-key-file is set and observability.otlp.client-cert-file is not"},
		},
		"a ca-file that does not exist": {
			func(c *Config) { c.CAFile = missing },
			ErrUnreadableCertificate,
			[]string{"observability.otlp.ca-file", "cannot be read"},
		},
		"a ca-file that is not PEM": {
			func(c *Config) { c.CAFile = notPEM },
			ErrUnreadableCertificate,
			[]string{"observability.otlp.ca-file", "no PEM certificate"},
		},
		"a client certificate file that does not exist": {
			func(c *Config) { c.ClientCertFile, c.ClientKeyFile = missing, client.keyFile },
			ErrUnreadableCertificate,
			[]string{"observability.otlp.client-cert-file", "cannot be read"},
		},
		"a client key file that does not exist": {
			func(c *Config) { c.ClientCertFile, c.ClientKeyFile = client.certFile, missing },
			ErrUnreadableCertificate,
			[]string{"observability.otlp.client-key-file", "cannot be read"},
		},
		"a client certificate that is not PEM": {
			func(c *Config) { c.ClientCertFile, c.ClientKeyFile = notPEM, client.keyFile },
			ErrUnreadableCertificate,
			[]string{"observability.otlp.client-cert-file", "observability.otlp.client-key-file"},
		},
		"a client certificate whose PEM block is of another type": {
			func(c *Config) { c.ClientCertFile, c.ClientKeyFile = labelled, client.keyFile },
			ErrUnreadableCertificate,
			[]string{"observability.otlp.client-cert-file", "observability.otlp.client-key-file"},
		},
		"a client key whose PEM block is of another type": {
			func(c *Config) { c.ClientCertFile, c.ClientKeyFile = client.certFile, labelled },
			ErrUnreadableCertificate,
			[]string{"observability.otlp.client-cert-file", "observability.otlp.client-key-file"},
		},
		"a client key that is not PEM": {
			func(c *Config) { c.ClientCertFile, c.ClientKeyFile = client.certFile, notPEM },
			ErrUnreadableCertificate,
			[]string{"observability.otlp.client-cert-file", "observability.otlp.client-key-file"},
		},
		"a client certificate with another certificate's key": {
			func(c *Config) { c.ClientCertFile, c.ClientKeyFile = client.certFile, other.keyFile },
			ErrUnreadableCertificate,
			[]string{"observability.otlp.client-cert-file", "observability.otlp.client-key-file"},
		},
	} {
		t.Run(name, func(t *testing.T) {
			cfg := configured()
			cfg.Insecure = false
			c.set(cfg)
			err := prepareWith(t, cfg)
			if !errors.Is(err, c.sentinel) {
				t.Fatalf("Prepare gave %v, want %v", err, c.sentinel)
			}
			for _, want := range c.named {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("the text is %q, want it to carry %q", err, want)
				}
			}
			if strings.Contains(err.Error(), marker) {
				t.Errorf("the text is %q, and it repeats what the file holds", err)
			}
		})
	}
}

// TestACertificateItemOverPlaintextFailsThePreparation pins the
// contradiction.
func TestACertificateItemOverPlaintextFailsThePreparation(t *testing.T) {
	ca := newAuthority(t)
	client := ca.issue(t, "client", x509.ExtKeyUsageClientAuth)
	for name, c := range map[string]struct {
		set   func(*Config)
		named []string
	}{
		"ca-file": {
			func(c *Config) { c.CAFile = ca.file },
			[]string{"observability.otlp.ca-file"},
		},
		"a client key pair": {
			func(c *Config) { c.ClientCertFile, c.ClientKeyFile = client.certFile, client.keyFile },
			[]string{"observability.otlp.client-cert-file", "observability.otlp.client-key-file"},
		},
		"client-key-file": {
			func(c *Config) { c.ClientKeyFile = client.keyFile },
			[]string{"observability.otlp.client-key-file"},
		},
		"all three": {
			func(c *Config) {
				c.CAFile, c.ClientCertFile, c.ClientKeyFile = ca.file, client.certFile, client.keyFile
			},
			[]string{
				"observability.otlp.ca-file",
				"observability.otlp.client-cert-file",
				"observability.otlp.client-key-file",
			},
		},
	} {
		t.Run(name, func(t *testing.T) {
			cfg := configured()
			cfg.Insecure = true
			c.set(cfg)
			err := prepareWith(t, cfg)
			if !errors.Is(err, ErrCertificateOverPlaintext) {
				t.Fatalf("Prepare gave %v, want %v", err, ErrCertificateOverPlaintext)
			}
			for _, want := range append(c.named, "observability.otlp.insecure") {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("the text is %q, want it to carry %q", err, want)
				}
			}
		})
	}
}

// TestCertificateItemsThatLoadPassThePreparation pins the accepted
// shapes.
func TestCertificateItemsThatLoadPassThePreparation(t *testing.T) {
	ca := newAuthority(t)
	client := ca.issue(t, "client", x509.ExtKeyUsageClientAuth)
	for name, set := range map[string]func(*Config){
		"ca-file":           func(c *Config) { c.CAFile = ca.file },
		"a client key pair": func(c *Config) { c.ClientCertFile, c.ClientKeyFile = client.certFile, client.keyFile },
		"ca-file and a pair": func(c *Config) {
			c.CAFile, c.ClientCertFile, c.ClientKeyFile = ca.file, client.certFile, client.keyFile
		},
	} {
		cfg := configured()
		cfg.Insecure = false
		set(cfg)
		if err := prepareWith(t, cfg); err != nil {
			t.Errorf("%s failed the preparation: %v", name, err)
		}
	}
}

// TestTheCertificateItemsArePathsRatherThanCredentials pins their
// declaration.
func TestTheCertificateItemsArePathsRatherThanCredentials(t *testing.T) {
	schema, ok := Module().Resources[0].(config.Schema)
	if !ok {
		t.Fatalf("the descriptor declares %T as its resource, want a config.Schema", Module().Resources[0])
	}
	for _, key := range []string{"ca-file", "client-cert-file", "client-key-file"} {
		item, ok := schema.Items[key]
		if !ok {
			t.Errorf("%s is not declared", key)
			continue
		}
		if item.Sensitive {
			t.Errorf("%s is marked sensitive, and it holds a path", key)
		}
		origins := item.Origins
		if origins == 0 {
			origins = config.OriginPrimary | config.OriginEnv
		}
		if origins&config.OriginFlag != 0 {
			t.Errorf("%s accepts a command-line origin", key)
		}
		if origins&config.OriginPrimary == 0 || origins&config.OriginEnv == 0 {
			t.Errorf("%s does not take both the primary source and the environment", key)
		}
	}
}
