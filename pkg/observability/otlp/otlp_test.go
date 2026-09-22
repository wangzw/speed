package otlp

import (
	"errors"
	"strings"
	"testing"

	"github.com/vislake/speed/pkg/config"
)

// configured is the prototype with the one required item filled in.
func configured() *Config {
	cfg := prototype()
	cfg.Endpoint = "collector.example:4317"
	return cfg
}

// TestThePrototypeCarriesTheDefaults pins the deployable default: a collector
// beside the process, over plaintext, on the gRPC port it listens on by
// default.
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

// TestValidateNamesEachFailure walks the failures this module judges.
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
}

// TestAnUnreadableHeaderNeverPutsTheCredentialInTheError pins the one thing a
// header error must not do. A header is where a collector's credential goes,
// and the error text reaches the startup diagnostics, which are written to
// standard error where anyone with the logs can read them.
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

// TestNoInputItemTakesItsValueFromTheCommandLine pins the rule for the whole
// namespace rather than for a list of items: a value that reached the command
// line would appear in the process list, and the endpoint and the headers are
// exactly what a host does not want there. The check is over the declaration, so
// an item added later without a thought about it fails here.
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
