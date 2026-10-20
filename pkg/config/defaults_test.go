package config

import (
	"errors"
	"io"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/spf13/pflag"

	"github.com/vislake/speed/pkg/core"
)

// fakeDefaults is a host defaults document as a module delivers one. The data
// is what a real parser would read; the fake format below answers with the
// content it was built with, so a case states the parsed document directly.
type fakeDefaults struct {
	format string
	data   []byte
}

func (f fakeDefaults) Format() string { return f.format }
func (f fakeDefaults) Data() []byte   { return f.data }

// withDefaults packs a document as the module a host would register.
func withDefaults(format string, data string) core.Module {
	return providing("host.defaults", fakeDefaults{format: format, data: []byte(data)})
}

// loadDefaults builds a loader over one cache declaration, one parser and the
// extra modules. The cache prototype is data_test.go's, so a documented default
// is told apart from the field value it replaces.
func loadDefaults(t *testing.T, format fakeFormat, items map[string]Item, extra ...core.Module) (*loader, error) {
	t.Helper()
	prototype := cacheOptions{
		Addr:   "localhost:6379",
		TTL:    5 * time.Minute,
		Labels: map[string]string{"tier": "hot"},
	}
	reg := core.New()
	reg.Register(identifying("host", HostIdentity{Prefix: "MYAPP"}))
	reg.Register(providing("format.yaml", format))
	reg.Register(declaring("cache", Schema{
		Namespace: "cache",
		Mounts:    []Mount{{Value: &prototype}},
		Items:     items,
	}))
	for _, m := range extra {
		reg.Register(m)
	}
	return newLoader(reg, io.Discard)
}

// yamlParser is the parser every case registers unless it is the parser the
// case is about.
func yamlParser(content map[string]any) fakeFormat {
	return fakeFormat{name: "yaml", content: content}
}

// givePrimaryWhole puts a parsed primary source into a loader exactly as
// ReadPrimary does: the un-folded tree is kept for container leaves, the
// folded one feeds the layering tool. givePrimary in data_test.go stops short
// of the first half because no case there reads a container through it after a
// document has supplied one.
func givePrimaryWhole(t *testing.T, l *loader, content map[string]any) {
	t.Helper()
	if err := validatePrimary(l.m, content); err != nil {
		t.Fatalf("the primary source was refused: %v", err)
	}
	cloned, _ := clonedTree(content).(map[string]any)
	l.primary = cloned
	if err := l.v.MergeConfigMap(content); err != nil {
		t.Fatalf("merging the primary source failed: %v", err)
	}
}

// TestDefaultsDocumentSetsScalarDefaults pins that a value in the document
// replaces the prototype's field value as the item's default, and that both
// spellings of a path reach it: one key holding the dots, and nested keys.
// A key the document does not give keeps the prototype's value, the same
// leaf-wise rule every other layer follows.
func TestDefaultsDocumentSetsScalarDefaults(t *testing.T) {
	l, err := loadDefaults(t, yamlParser(map[string]any{
		"cache.addr": "defaults:1",
		"cache":      map[string]any{"ttl": "2m"},
	}), nil, withDefaults("yaml", "ignored"))
	if err != nil {
		t.Fatalf("loading with a defaults document failed: %v", err)
	}
	opts := decodeCacheOptions(t, l)
	if opts.Addr != "defaults:1" {
		t.Fatalf("cache.addr decoded to %q, want the document's value written flat", opts.Addr)
	}
	if opts.TTL != 2*time.Minute {
		t.Fatalf("cache.ttl decoded to %v, want the document's value written nested", opts.TTL)
	}
	if want := map[string]string{"tier": "hot"}; !reflect.DeepEqual(opts.Labels, want) {
		t.Fatalf("cache.labels decoded to %#v, want the prototype's %#v: a key the document does "+
			"not give keeps its field value", opts.Labels, want)
	}
}

// TestDocumentDefaultLosesToHigherLayers pins the document's place in the
// stack: it replaces the prototype's field value and is itself replaced by the
// primary source and by the environment.
func TestDocumentDefaultLosesToHigherLayers(t *testing.T) {
	document := map[string]any{"cache": map[string]any{"addr": "defaults:1"}}
	cases := []struct {
		name    string
		primary map[string]any
		env     string
		want    string
	}{
		{name: "document only", want: "defaults:1"},
		{name: "primary source", primary: map[string]any{"cache": map[string]any{"addr": "file:1"}}, want: "file:1"},
		{name: "environment", env: "env:1", want: "env:1"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.env != "" {
				t.Setenv("MYAPP_CACHE__ADDR", tc.env)
			}
			l, err := loadDefaults(t, yamlParser(document), nil, withDefaults("yaml", "ignored"))
			if err != nil {
				t.Fatalf("loading with a defaults document failed: %v", err)
			}
			if tc.primary != nil {
				givePrimaryWhole(t, l, tc.primary)
			}
			if got := decodeCacheOptions(t, l).Addr; got != tc.want {
				t.Fatalf("cache.addr decoded to %q, want %q", got, tc.want)
			}
		})
	}
}

// TestDocumentContainerAppliesWhole pins that a container leaf named by the
// document replaces the prototype's whole value rather than merging under it,
// and that its inner keys arrive as the document wrote them. The keys are the
// module's own data and are not leaves of the manifest, which is why the value
// is replaced as one and why the layering tool's folding never touches it: the
// mixed-case key is what tells this read path from the folded default layer.
func TestDocumentContainerAppliesWhole(t *testing.T) {
	l, err := loadDefaults(t, yamlParser(map[string]any{
		"cache": map[string]any{"labels": map[string]any{"Zone": "cold"}},
	}), nil, withDefaults("yaml", "ignored"))
	if err != nil {
		t.Fatalf("loading with a defaults document failed: %v", err)
	}
	want := map[string]string{"Zone": "cold"}
	if got := decodeCacheOptions(t, l).Labels; !reflect.DeepEqual(got, want) {
		t.Fatalf("cache.labels decoded to %#v, want %#v: the document's container replaces the "+
			"prototype's, the prototype's entry is not merged under it, and the inner key arrives "+
			"as written", got, want)
	}
}

// TestPrimarySourceReplacesDocumentContainerWhole pins the other half: the
// primary source still replaces the whole container, the document's entries
// included.
func TestPrimarySourceReplacesDocumentContainerWhole(t *testing.T) {
	l, err := loadDefaults(t, yamlParser(map[string]any{
		"cache": map[string]any{"labels": map[string]any{"zone": "cold"}},
	}), nil, withDefaults("yaml", "ignored"))
	if err != nil {
		t.Fatalf("loading with a defaults document failed: %v", err)
	}
	givePrimaryWhole(t, l, map[string]any{
		"cache": map[string]any{"labels": map[string]any{"region": "eu"}},
	})
	want := map[string]string{"region": "eu"}
	if got := decodeCacheOptions(t, l).Labels; !reflect.DeepEqual(got, want) {
		t.Fatalf("cache.labels decoded to %#v, want %#v: the primary source replaces the whole "+
			"container, the document's entries included", got, want)
	}
}

// TestUnknownDefaultsKeyStopsTheStartup pins that the document is judged like
// the primary source: a key no module declares is a startup failure, not a
// value that quietly vanishes. The message names the document rather than the
// primary source, which is where the reader has to go and fix it.
func TestUnknownDefaultsKeyStopsTheStartup(t *testing.T) {
	_, err := loadDefaults(t, yamlParser(map[string]any{
		"cache": map[string]any{"unknown-setting": "x"},
	}), nil, withDefaults("yaml", "ignored"))
	if !errors.Is(err, ErrUnknownKey) {
		t.Fatalf("an undeclared key in the defaults document returned %v, want ErrUnknownKey", err)
	}
	if !strings.Contains(err.Error(), defaultsDocument) {
		t.Fatalf("the rejection reads %q, which does not name the defaults document", err)
	}
	if !strings.Contains(err.Error(), "no module declares") {
		t.Fatalf("the rejection reads %q, which does not say the manifest has no such key", err)
	}
}

// TestDefaultsKeyNotAcceptedFromPrimaryStopsTheStartup pins the second kind of
// key the document may not give: the manifest has it, and the item does not
// take the primary source. The defaults document feeds the layer the primary
// source feeds, so the same items accept it; letting another through would
// swallow a value written on purpose.
func TestDefaultsKeyNotAcceptedFromPrimaryStopsTheStartup(t *testing.T) {
	items := map[string]Item{"addr": {Origins: OriginFlag, FlagName: "cache-addr"}}
	_, err := loadDefaults(t, yamlParser(map[string]any{
		"cache": map[string]any{"addr": "defaults:1"},
	}), items, withDefaults("yaml", "ignored"))
	if !errors.Is(err, ErrUnknownKey) {
		t.Fatalf("a key the item does not take from the primary source returned %v, want ErrUnknownKey", err)
	}
	if !strings.Contains(err.Error(), "does not accept from the primary source") {
		t.Fatalf("the rejection reads %q, which does not tell this case from a key the manifest "+
			"has never heard of", err)
	}
	if !strings.Contains(err.Error(), defaultsDocument) {
		t.Fatalf("the rejection reads %q, which does not name the defaults document", err)
	}
}

// TestDefaultsDocumentDoesNotSatisfyRequired pins that the document belongs to
// the default layer, which never counts as somebody having given a value. The
// document really does supply the value -- the item's default holds it -- and
// Decode still reports the item missing, or "nobody gave it" and "somebody
// gave the default" would be the same answer.
func TestDefaultsDocumentDoesNotSatisfyRequired(t *testing.T) {
	items := map[string]Item{"addr": {Required: true}}
	l, err := loadDefaults(t, yamlParser(map[string]any{
		"cache": map[string]any{"addr": "defaults:1"},
	}), items, withDefaults("yaml", "ignored"))
	if err != nil {
		t.Fatalf("loading with a defaults document failed: %v", err)
	}
	if got := l.m.byPath["cache.addr"].def; got != "defaults:1" {
		t.Fatalf("the item's default is %#v, want the document's value: the case has to prove the "+
			"document supplied one, or it tests nothing", got)
	}
	var opts cacheOptions
	err = l.Reader().Decode("cache", &opts)
	if !errors.Is(err, ErrMissingRequired) {
		t.Fatalf("a required item supplied only by the defaults document returned %v, want "+
			"%v", err, ErrMissingRequired)
	}
	if !strings.Contains(err.Error(), "cache.addr") {
		t.Fatalf("the rejection reads %q, which does not name the item", err)
	}
}

// TestDocumentDefaultGoesThroughTheTypeRules pins that the document's values
// take the same conversion path as every other source's: they keep the types
// the parser resolved, and the constraints on top of the conversion apply to
// them. A duration is the sharpest probe, since it is one type whose rule no
// library default gives -- it takes time.ParseDuration text and refuses a bare
// number, whatever layer the number came from.
func TestDocumentDefaultGoesThroughTheTypeRules(t *testing.T) {
	cases := []struct {
		name    string
		value   any
		want    time.Duration
		wantErr bool
	}{
		{name: "text", value: "2m", want: 2 * time.Minute},
		{name: "bare number", value: 120, wantErr: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			l, err := loadDefaults(t, yamlParser(map[string]any{
				"cache": map[string]any{"ttl": tc.value},
			}), nil, withDefaults("yaml", "ignored"))
			if err != nil {
				t.Fatalf("loading with a defaults document failed: %v", err)
			}
			var opts cacheOptions
			err = l.Reader().Decode("cache", &opts)
			if tc.wantErr {
				if !errors.Is(err, ErrTypeMismatch) {
					t.Fatalf("a bare number for a duration returned %v, want ErrTypeMismatch", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("decoding failed: %v", err)
			}
			if opts.TTL != tc.want {
				t.Fatalf("cache.ttl decoded to %v, want %v", opts.TTL, tc.want)
			}
		})
	}
}

// TestTwoDefaultsDocumentsConflict pins that a run carries one document: two
// declarations leave no ground to choose between them, and the error names
// both parties.
func TestTwoDefaultsDocumentsConflict(t *testing.T) {
	_, err := loadDefaults(t, yamlParser(map[string]any{}), nil,
		providing("host.defaults.a", fakeDefaults{format: "yaml"}),
		providing("host.defaults.b", fakeDefaults{format: "yaml"}),
	)
	if !errors.Is(err, ErrConfigConflict) {
		t.Fatalf("two defaults documents returned %v, want ErrConfigConflict", err)
	}
	for _, party := range []string{"host.defaults.a", "host.defaults.b"} {
		if !strings.Contains(err.Error(), party) {
			t.Fatalf("the conflict reads %q, which does not name %q", err, party)
		}
	}
}

// TestHelpRendersDocumentDefault pins that the help output's rendered default
// is the document's value: the argument's default is read off the item, and
// the item's default is what the document replaced.
func TestHelpRendersDocumentDefault(t *testing.T) {
	items := map[string]Item{"addr": {Origins: OriginPrimary | OriginEnv | OriginFlag, FlagName: "cache-addr"}}
	l, err := loadDefaults(t, yamlParser(map[string]any{
		"cache": map[string]any{"addr": "defaults:1"},
	}), items, withDefaults("yaml", "ignored"))
	if err != nil {
		t.Fatalf("loading with a defaults document failed: %v", err)
	}
	fs := pflag.NewFlagSet("test", pflag.ContinueOnError)
	fs.SetOutput(io.Discard)
	if err := l.BindLayer(fs, []string{"cache"}, []string{"cache"}); err != nil {
		t.Fatalf("binding the command line failed: %v", err)
	}
	usage := fs.FlagUsages()
	if !strings.Contains(usage, "defaults:1") {
		t.Fatalf("the help output reads %q, which does not render the document's default",
			strings.TrimSpace(usage))
	}
	if strings.Contains(usage, "localhost:6379") {
		t.Fatalf("the help output reads %q, which renders the prototype's value the document "+
			"replaced", strings.TrimSpace(usage))
	}
}

// TestHelpRendersDocumentListDefault pins the list half of the same rule: a
// []string item whose value the document supplies renders the document's
// entries. The document reaches the item as its parser gave it, a sequence of
// interface values, so the renderer has to read through them rather than print
// the interface itself.
func TestHelpRendersDocumentListDefault(t *testing.T) {
	type hostsOptions struct{ Hosts []string }
	prototype := hostsOptions{Hosts: []string{"proto-a", "proto-b"}}
	l := loadOver(t,
		providing("format.yaml", yamlParser(map[string]any{
			"cache": map[string]any{"hosts": []any{"a", "b"}},
		})),
		declaring("cache", Schema{
			Namespace: "cache",
			Mounts:    []Mount{{Value: &prototype}},
			Items: map[string]Item{
				"hosts": {Origins: OriginPrimary | OriginFlag, FlagName: "hosts", Placeholder: "HOST"},
			},
		}),
		withDefaults("yaml", "ignored"),
	)
	if got := l.m.byPath["cache.hosts"].def; !reflect.DeepEqual(got, []any{"a", "b"}) {
		t.Fatalf("the item's default is %#v, want the document's parsed list: the case has to "+
			"prove the document supplied one in the shape a parser gives it, or it tests nothing", got)
	}
	fs := pflag.NewFlagSet("test", pflag.ContinueOnError)
	fs.SetOutput(io.Discard)
	if err := l.BindLayer(fs, []string{"cache"}, []string{"cache"}); err != nil {
		t.Fatalf("binding the command line failed: %v", err)
	}
	usage := fs.FlagUsages()
	if !strings.Contains(usage, "a,b") {
		t.Fatalf("the help output reads %q, which does not render the document's list default",
			strings.TrimSpace(usage))
	}
	if strings.Contains(usage, "proto-a") {
		t.Fatalf("the help output reads %q, which renders the prototype's list the document "+
			"replaced", strings.TrimSpace(usage))
	}
}

// TestDocumentDefaultStaysOutOfHelpWhenSensitive pins the one place the
// document's value must not reach: a sensitive item hands the command line no
// default, however the default was arrived at.
func TestDocumentDefaultStaysOutOfHelpWhenSensitive(t *testing.T) {
	items := map[string]Item{
		"ttl": {Origins: OriginPrimary | OriginFlag, FlagName: "cache-ttl", Sensitive: true, Description: "cache TTL"},
	}
	l, err := loadDefaults(t, yamlParser(map[string]any{
		"cache": map[string]any{"ttl": "2m"},
	}), items, withDefaults("yaml", "ignored"))
	if err != nil {
		t.Fatalf("loading with a defaults document failed: %v", err)
	}
	if got := l.m.byPath["cache.ttl"].def; got != "2m" {
		t.Fatalf("the item's default is %#v, want the document's value: the case has to prove the "+
			"document supplied one, or a missing default would pass it too", got)
	}
	fs := pflag.NewFlagSet("test", pflag.ContinueOnError)
	fs.SetOutput(io.Discard)
	if err := l.BindLayer(fs, []string{"cache"}, []string{"cache"}); err != nil {
		t.Fatalf("binding the command line failed: %v", err)
	}
	usage := fs.FlagUsages()
	if !strings.Contains(usage, "--cache-ttl") {
		t.Fatalf("the help output reads %q, which does not list the argument", strings.TrimSpace(usage))
	}
	if strings.Contains(usage, "2m") {
		t.Fatalf("the help output reads %q, which echoes a sensitive default", strings.TrimSpace(usage))
	}
}

// TestDefaultsFormatMustBeRegistered pins the format selection: the name is
// folded to lower case and matched exactly, and a name no imported Format
// answers for fails the startup rather than falling back to anything.
func TestDefaultsFormatMustBeRegistered(t *testing.T) {
	cases := map[string]string{
		"unknown": "toml",
		"empty":   "",
	}
	for name, format := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := loadDefaults(t, yamlParser(map[string]any{}), nil, withDefaults(format, "ignored"))
			if !errors.Is(err, ErrUnknownFormat) {
				t.Fatalf("the format %q returned %v, want ErrUnknownFormat", format, err)
			}
			if !strings.Contains(err.Error(), defaultsDocument) {
				t.Fatalf("the rejection reads %q, which does not name the defaults document", err)
			}
		})
	}

	// The other half of the matching rule: case does not tell two names apart,
	// so the registered yaml parser answers for "YAML".
	if _, err := loadDefaults(t, yamlParser(map[string]any{
		"cache": map[string]any{"addr": "defaults:1"},
	}), nil, withDefaults("YAML", "ignored")); err != nil {
		t.Fatalf("the format name YAML did not reach the parser registered as yaml: %v", err)
	}
}

// TestMalformedDefaultsDocumentStopsTheStartup pins that a document the parser
// refuses is a startup failure classified like the primary source's, not an
// empty layer.
func TestMalformedDefaultsDocumentStopsTheStartup(t *testing.T) {
	parser := fakeFormat{name: "yaml", err: errors.New("duplicate key")}
	_, err := loadDefaults(t, parser, nil, withDefaults("yaml", "ignored"))
	if !errors.Is(err, ErrMalformedConfig) {
		t.Fatalf("an unparsable defaults document returned %v, want ErrMalformedConfig", err)
	}
	if !strings.Contains(err.Error(), defaultsDocument) {
		t.Fatalf("the rejection reads %q, which does not name the defaults document", err)
	}
}
