package config

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"testing"

	"github.com/vislake/speed/pkg/core"
)

type fakeSource struct {
	scheme string
	data   []byte
	format string
	err    error
	calls  *int
}

func (f fakeSource) Scheme() string { return f.scheme }

func (f fakeSource) Fetch(_ context.Context, locator *url.URL) ([]byte, string, error) {
	if f.calls != nil {
		*f.calls++
	}
	if f.err != nil {
		return nil, "", f.err
	}
	if locator.Host != "" {
		return nil, "", fmt.Errorf("%w: %q names the host %q, and this transport reads local "+
			"files only", ErrMalformedLocator, locator, locator.Host)
	}
	return f.data, f.format, nil
}

type fakeFormat struct {
	name    string
	content map[string]any
	err     error
}

func (f fakeFormat) Name() string { return f.name }

func (f fakeFormat) Unmarshal([]byte) (map[string]any, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.content, nil
}

func providing(name string, resources ...any) core.Module {
	return core.Module{Name: name, Resources: resources}
}

func locatorSetup(t *testing.T, host HostIdentity, source fakeSource, format fakeFormat) (*manifest, *transports) {
	t.Helper()
	reg := core.New()
	reg.Register(identifying("host", host))
	reg.Register(providing("source", source))
	reg.Register(providing("format", format))
	m, err := newManifest(reg)
	if err != nil {
		t.Fatalf("collecting the manifest failed: %v", err)
	}
	tr, err := collectTransports(reg)
	if err != nil {
		t.Fatalf("collecting the transports failed: %v", err)
	}
	return m, tr
}

func defaultSource() fakeSource {
	return fakeSource{scheme: "file", data: []byte("irrelevant"), format: "yaml"}
}

func defaultFormat() fakeFormat {
	return fakeFormat{name: "yaml", content: map[string]any{}}
}

// TestLocatorPrecedence checks that locator precedence.
func TestLocatorPrecedence(t *testing.T) {
	m, _ := locatorSetup(t, HostIdentity{Prefix: "MYAPP", DefaultLocator: "file:///default.yaml"},
		defaultSource(), defaultFormat())
	env := map[string]string{"MYAPP_CONFIG": "file:///from-env.yaml"}
	const fromFlag = "file:///from-flag.yaml"

	if got := locate(m, nil, "", false); got != "file:///default.yaml" {
		t.Fatalf("with nothing else given the locator is %q, want the host's default", got)
	}
	if got := locate(m, env, "", false); got != "file:///from-env.yaml" {
		t.Fatalf("with the environment given the locator is %q, want the variable's value", got)
	}
	if got := locate(m, env, fromFlag, true); got != "file:///from-flag.yaml" {
		t.Fatalf("with all three given the locator is %q, want the command line's value", got)
	}
	if got := locate(m, nil, fromFlag, true); got != "file:///from-flag.yaml" {
		t.Fatalf("with the command line given the locator is %q, want its value", got)
	}
}

// TestAbsentLocatorIsLegal checks that absent locator is legal.
func TestAbsentLocatorIsLegal(t *testing.T) {
	m, _ := locatorSetup(t, HostIdentity{Prefix: "MYAPP"}, defaultSource(), defaultFormat())
	if got := locate(m, nil, "", false); got != "" {
		t.Fatalf("with nothing given the locator is %q, want none at all", got)
	}
}

// TestNoPrefixDisablesEnvLocator checks that no prefix disables env locator.
func TestNoPrefixDisablesEnvLocator(t *testing.T) {
	m, _ := locatorSetup(t, HostIdentity{DefaultLocator: "file:///default.yaml"},
		defaultSource(), defaultFormat())
	env := map[string]string{"_CONFIG": "file:///from-env.yaml", "CONFIG": "file:///from-env.yaml"}
	if got := locate(m, env, "", false); got != "file:///default.yaml" {
		t.Fatalf("with no prefix the locator is %q, want the host's default: no variable name "+
			"can be derived", got)
	}
}

func readLocator(t *testing.T, tr *transports, locator string) error {
	t.Helper()
	content, err := tr.read(t.Context(), locator)
	if err == nil {
		t.Fatalf("reading %q produced %#v, want a rejection", locator, content)
	}
	return err
}

// TestMalformedLocator checks that malformed locator.
func TestMalformedLocator(t *testing.T) {
	_, tr := locatorSetup(t, HostIdentity{}, defaultSource(), defaultFormat())
	if err := readLocator(t, tr, "://nope"); !errors.Is(err, ErrMalformedLocator) {
		t.Fatalf("a locator that is not a URI returned %v, want ErrMalformedLocator", err)
	}
	err := readLocator(t, tr, "/etc/app.yaml")
	if !errors.Is(err, ErrMalformedLocator) {
		t.Fatalf("a locator with no scheme returned %v, want ErrMalformedLocator", err)
	}
	if !strings.Contains(err.Error(), "scheme") {
		t.Fatalf("the rejection reads %q, which does not say what is missing", err)
	}
}

// TestSourceRejectedShapeMapsToMalformedLocator checks that source rejected shape maps to malformed locator.
func TestSourceRejectedShapeMapsToMalformedLocator(t *testing.T) {
	_, tr := locatorSetup(t, HostIdentity{}, defaultSource(), defaultFormat())
	err := readLocator(t, tr, "file://host/path")
	if !errors.Is(err, ErrMalformedLocator) {
		t.Fatalf("a shape the transport refuses returned %v, want ErrMalformedLocator", err)
	}
	if errors.Is(err, ErrSourceUnavailable) {
		t.Fatalf("the rejection reads %q, and a shape the transport refuses is not the source "+
			"being unreachable", err)
	}
}

// TestUnknownSchemeNamesMissingImport checks that unknown scheme names missing import.
func TestUnknownSchemeNamesMissingImport(t *testing.T) {
	_, tr := locatorSetup(t, HostIdentity{}, defaultSource(), defaultFormat())
	err := readLocator(t, tr, "etcd://10.0.0.1:2379/app/config")
	if !errors.Is(err, ErrUnknownScheme) {
		t.Fatalf("a scheme no transport answers for returned %v, want ErrUnknownScheme", err)
	}
	if !strings.Contains(err.Error(), "github.com/vislake/speed/pkg/config/source/etcd") {
		t.Fatalf("the rejection reads %q, and a missing transport is fixed by an import the "+
			"message has to name", err)
	}
	if strings.Contains(err.Error(), "source/file") {
		t.Fatalf("the rejection reads %q, which points at a transport that happens to be "+
			"imported instead of at the one that was asked for", err)
	}
}

// TestUnknownSchemeDerivesPathFromRequestedScheme checks that unknown scheme derives path from requested scheme.
func TestUnknownSchemeDerivesPathFromRequestedScheme(t *testing.T) {
	_, tr := locatorSetup(t, HostIdentity{}, defaultSource(), defaultFormat())
	err := readLocator(t, tr, "consul://c/app")
	if !strings.Contains(err.Error(), "github.com/vislake/speed/pkg/config/source/consul") {
		t.Fatalf("the rejection reads %q, which does not name the subpackage the requested "+
			"scheme is carried by", err)
	}
	err = readLocator(t, tr, "etcd://10.0.0.1:2379/app/config")
	if !strings.Contains(err.Error(), `the scheme "etcd"`) {
		t.Fatalf("the rejection reads %q, which drops the scheme itself: the name and the path "+
			"it implies are both owed to the reader", err)
	}
	err = readLocator(t, tr, "ETCD://h/p")
	if !strings.Contains(err.Error(), "source/etcd") {
		t.Fatalf("the rejection for an upper-cased scheme reads %q, want the lowercased path "+
			"the lookup went by", err)
	}
	if strings.Contains(err.Error(), "source/ETCD") {
		t.Fatalf("the rejection reads %q, which spells the path the way the locator was "+
			"written rather than the way the scheme was parsed", err)
	}
}

// TestUndeterminedFormat checks that undetermined format.
func TestUndeterminedFormat(t *testing.T) {
	source := defaultSource()
	source.format = ""
	_, tr := locatorSetup(t, HostIdentity{}, source, defaultFormat())
	if err := readLocator(t, tr, "file:///app.conf"); !errors.Is(err, ErrUndeterminedFormat) {
		t.Fatalf("a transport reporting no format returned %v, want ErrUndeterminedFormat", err)
	}
}

// TestUnknownFormatNamesMissingImport checks that unknown format names missing import.
func TestUnknownFormatNamesMissingImport(t *testing.T) {
	source := defaultSource()
	source.format = "toml"
	_, tr := locatorSetup(t, HostIdentity{}, source, defaultFormat())
	err := readLocator(t, tr, "file:///app.toml")
	if !errors.Is(err, ErrUnknownFormat) {
		t.Fatalf("a format no parser answers for returned %v, want ErrUnknownFormat", err)
	}
	if !strings.Contains(err.Error(), "github.com/vislake/speed/pkg/config/format/toml") {
		t.Fatalf("the rejection reads %q, and a missing parser is fixed by an import the message "+
			"has to name", err)
	}
	if strings.Contains(err.Error(), "format/yaml") {
		t.Fatalf("the rejection reads %q, which points at a parser that happens to be imported "+
			"instead of at the one the transport reported", err)
	}
}

// TestUnknownFormatDerivesPathFromReportedName checks that unknown format derives path from reported name.
func TestUnknownFormatDerivesPathFromReportedName(t *testing.T) {
	source := defaultSource()
	source.format = "hcl"
	_, tr := locatorSetup(t, HostIdentity{}, source, defaultFormat())
	err := readLocator(t, tr, "file:///app.hcl")
	if !strings.Contains(err.Error(), "github.com/vislake/speed/pkg/config/format/hcl") {
		t.Fatalf("the rejection reads %q, which does not name the subpackage the reported "+
			"format is carried by", err)
	}

	source.format = "weird format"
	_, tr = locatorSetup(t, HostIdentity{}, source, defaultFormat())
	err = readLocator(t, tr, "file:///app.conf")
	if !strings.Contains(err.Error(), `"weird format"`) {
		t.Fatalf("the rejection reads %q, and a name carrying a space has to be quoted or it "+
			"swallows the sentence around it", err)
	}
}

// TestMissingTransportHintsNameTheConvention checks that missing transport hints name the convention.
func TestMissingTransportHintsNameTheConvention(t *testing.T) {
	for _, err := range missingTransportHints(t) {
		if !strings.Contains(err.Error(), "by convention") {
			t.Fatalf("the rejection reads %q, which offers a path without saying it was "+
				"derived from a convention", err)
		}
	}
}

// TestMissingTransportHintsAreNotGuarantees checks that missing transport hints are not guarantees.
func TestMissingTransportHintsAreNotGuarantees(t *testing.T) {
	for _, err := range missingTransportHints(t) {
		if !strings.Contains(err.Error(), "free to live elsewhere") {
			t.Fatalf("the rejection reads %q, which presents a convention as the only place "+
				"an implementation can be", err)
		}
		if !strings.Contains(err.Error(), "cannot enumerate") {
			t.Fatalf("the rejection reads %q, which does not admit that this module cannot "+
				"tell which implementations exist", err)
		}
	}
}

func missingTransportHints(t *testing.T) []error {
	t.Helper()
	_, tr := locatorSetup(t, HostIdentity{}, defaultSource(), defaultFormat())
	schemeErr := readLocator(t, tr, "etcd://10.0.0.1:2379/app/config")
	source := defaultSource()
	source.format = "toml"
	_, tr = locatorSetup(t, HostIdentity{}, source, defaultFormat())
	return []error{schemeErr, readLocator(t, tr, "file:///app.toml")}
}

// TestFetchFailureMapsToSourceUnavailable checks that fetch failure maps to source unavailable.
func TestFetchFailureMapsToSourceUnavailable(t *testing.T) {
	source := defaultSource()
	source.err = errors.New("no such file or directory")
	_, tr := locatorSetup(t, HostIdentity{}, source, defaultFormat())
	err := readLocator(t, tr, "file:///missing.yaml")
	if !errors.Is(err, ErrSourceUnavailable) {
		t.Fatalf("a transport that could not read returned %v, want ErrSourceUnavailable", err)
	}
	if !strings.Contains(err.Error(), "no such file or directory") {
		t.Fatalf("the rejection reads %q, which drops what the transport reported", err)
	}
}

// TestUnmarshalFailureMapsToMalformedConfig checks that unmarshal failure maps to malformed config.
func TestUnmarshalFailureMapsToMalformedConfig(t *testing.T) {
	format := defaultFormat()
	format.err = errors.New("line 3: mapping values are not allowed here")
	_, tr := locatorSetup(t, HostIdentity{}, defaultSource(), format)
	err := readLocator(t, tr, "file:///app.yaml")
	if !errors.Is(err, ErrMalformedConfig) {
		t.Fatalf("data the parser refused returned %v, want ErrMalformedConfig", err)
	}
	if !strings.Contains(err.Error(), "line 3") {
		t.Fatalf("the rejection reads %q, which drops what the parser reported", err)
	}
}

// TestFetchAndUnmarshalProduceTheContent checks that fetch and unmarshal produce the content.
func TestFetchAndUnmarshalProduceTheContent(t *testing.T) {
	format := defaultFormat()
	format.content = map[string]any{"cache": map[string]any{"addr": "redis:6379"}}
	_, tr := locatorSetup(t, HostIdentity{}, defaultSource(), format)
	content, err := tr.read(t.Context(), "file:///app.yaml")
	if err != nil {
		t.Fatalf("reading the primary source failed: %v", err)
	}
	section, ok := content["cache"].(map[string]any)
	if !ok || section["addr"] != "redis:6379" {
		t.Fatalf("the primary source produced %#v, want what the parser returned", content)
	}
}

// TestDuplicateSchemeRejected checks that duplicate scheme rejected.
func TestDuplicateSchemeRejected(t *testing.T) {
	reg := core.New()
	reg.Register(providing("source.file", fakeSource{scheme: "file"}))
	reg.Register(providing("source.other", fakeSource{scheme: "file"}))
	_, err := collectTransports(reg)
	if !errors.Is(err, ErrConfigConflict) {
		t.Fatalf("two transports for one scheme returned %v, want ErrConfigConflict", err)
	}
	for _, party := range []string{"source.file", "source.other", "file"} {
		if !strings.Contains(err.Error(), party) {
			t.Fatalf("the conflict reads %q, which does not name %q", err, party)
		}
	}
}

// TestDuplicateFormatNameRejected checks that duplicate format name rejected.
func TestDuplicateFormatNameRejected(t *testing.T) {
	reg := core.New()
	reg.Register(providing("format.yaml", fakeFormat{name: "yaml"}))
	reg.Register(providing("format.other", fakeFormat{name: "yaml"}))
	_, err := collectTransports(reg)
	if !errors.Is(err, ErrConfigConflict) {
		t.Fatalf("two parsers for one format name returned %v, want ErrConfigConflict", err)
	}
	for _, party := range []string{"format.other", "format.yaml", "yaml"} {
		if !strings.Contains(err.Error(), party) {
			t.Fatalf("the conflict reads %q, which does not name %q", err, party)
		}
	}
}

// TestTransportsAndFormatsAreSeparateNamespaces checks that transports and formats are separate namespaces.
func TestTransportsAndFormatsAreSeparateNamespaces(t *testing.T) {
	reg := core.New()
	reg.Register(providing("source", fakeSource{scheme: "yaml"}))
	reg.Register(providing("format", fakeFormat{name: "yaml"}))
	if _, err := collectTransports(reg); err != nil {
		t.Fatalf("a scheme and a format name that read alike conflicted: %v", err)
	}
}

// TestSchemeMatchIgnoresCase checks that scheme match ignores case.
func TestSchemeMatchIgnoresCase(t *testing.T) {
	calls := 0
	source := fakeSource{scheme: "ETCD", data: []byte("irrelevant"), format: "yaml", calls: &calls}
	format := defaultFormat()
	format.content = map[string]any{"cache": map[string]any{"addr": "redis:6379"}}
	_, tr := locatorSetup(t, HostIdentity{}, source, format)

	for _, locator := range []string{"ETCD:///app/config", "etcd:///app/config"} {
		content, err := tr.read(t.Context(), locator)
		if err != nil {
			t.Fatalf("reading %q failed: %v", locator, err)
		}
		section, ok := content["cache"].(map[string]any)
		if !ok || section["addr"] != "redis:6379" {
			t.Fatalf("reading %q produced %#v, want what the parser returned", locator, content)
		}
	}
	if calls != 2 {
		t.Fatalf("the transport was asked %d times, want both spellings to have reached it", calls)
	}
}

// TestDuplicateSchemeIgnoringCaseRejected checks that duplicate scheme ignoring case rejected.
func TestDuplicateSchemeIgnoringCaseRejected(t *testing.T) {
	reg := core.New()
	reg.Register(providing("source.upper", fakeSource{scheme: "ETCD"}))
	reg.Register(providing("source.lower", fakeSource{scheme: "etcd"}))
	_, err := collectTransports(reg)
	if !errors.Is(err, ErrConfigConflict) {
		t.Fatalf("two transports for one scheme spelled differently returned %v, want "+
			"ErrConfigConflict", err)
	}
	for _, party := range []string{"source.upper", "source.lower", "etcd"} {
		if !strings.Contains(err.Error(), party) {
			t.Fatalf("the conflict reads %q, which does not name %q", err, party)
		}
	}
}

// TestFormatMatchIgnoresRegisteredNameCase checks that format match ignores registered name case.
func TestFormatMatchIgnoresRegisteredNameCase(t *testing.T) {
	format := fakeFormat{name: "YAML", content: map[string]any{"cache": map[string]any{"addr": "redis:6379"}}}
	_, tr := locatorSetup(t, HostIdentity{}, defaultSource(), format)
	content, err := tr.read(t.Context(), "file:///app.yaml")
	if err != nil {
		t.Fatalf("reading a file whose parser is registered upper-cased failed: %v", err)
	}
	section, ok := content["cache"].(map[string]any)
	if !ok || section["addr"] != "redis:6379" {
		t.Fatalf("the primary source produced %#v, want what the parser returned", content)
	}
}

// TestFormatMatchIgnoresReportedNameCase checks that format match ignores reported name case.
func TestFormatMatchIgnoresReportedNameCase(t *testing.T) {
	source := defaultSource()
	source.format = "YAML"
	format := defaultFormat()
	format.content = map[string]any{"cache": map[string]any{"addr": "redis:6379"}}
	_, tr := locatorSetup(t, HostIdentity{}, source, format)
	content, err := tr.read(t.Context(), "file:///app.yaml")
	if err != nil {
		t.Fatalf("reading a source reporting an upper-cased format name failed: %v", err)
	}
	section, ok := content["cache"].(map[string]any)
	if !ok || section["addr"] != "redis:6379" {
		t.Fatalf("the primary source produced %#v, want what the parser returned", content)
	}
}

// TestDuplicateFormatNameIgnoringCaseRejected checks that duplicate format name ignoring case rejected.
func TestDuplicateFormatNameIgnoringCaseRejected(t *testing.T) {
	reg := core.New()
	reg.Register(providing("format.upper", fakeFormat{name: "YAML"}))
	reg.Register(providing("format.lower", fakeFormat{name: "yaml"}))
	_, err := collectTransports(reg)
	if !errors.Is(err, ErrConfigConflict) {
		t.Fatalf("two parsers for one format name spelled differently returned %v, want "+
			"ErrConfigConflict", err)
	}
	for _, party := range []string{"format.upper", "format.lower", "yaml"} {
		if !strings.Contains(err.Error(), party) {
			t.Fatalf("the conflict reads %q, which does not name %q", err, party)
		}
	}
}

// TestUnknownFormatPathFollowsTheKeyLookedUp checks that unknown format path follows the key looked up.
func TestUnknownFormatPathFollowsTheKeyLookedUp(t *testing.T) {
	source := defaultSource()
	source.format = "TOML"
	_, tr := locatorSetup(t, HostIdentity{}, source, defaultFormat())
	err := readLocator(t, tr, "file:///app.toml")
	if !errors.Is(err, ErrUnknownFormat) {
		t.Fatalf("a format no parser answers for returned %v, want ErrUnknownFormat", err)
	}
	if !strings.Contains(err.Error(), "github.com/vislake/speed/pkg/config/format/toml") {
		t.Fatalf("the rejection reads %q, want the lowercased path the lookup went by", err)
	}
	if strings.Contains(err.Error(), "format/TOML") {
		t.Fatalf("the rejection reads %q, which spells the path the way the transport reported "+
			"the name rather than the way it was looked up", err)
	}
}
