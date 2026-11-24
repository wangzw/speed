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

type fakeDefaults struct {
	format string
	data   []byte
}

func (f fakeDefaults) Format() string { return f.format }
func (f fakeDefaults) Data() []byte   { return f.data }

func withDefaults(format string, data string) core.Module {
	return providing("host.defaults", fakeDefaults{format: format, data: []byte(data)})
}

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

func yamlParser(content map[string]any) fakeFormat {
	return fakeFormat{name: "yaml", content: content}
}

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

// TestDefaultsDocumentSetsScalarDefaults checks that defaults document sets scalar defaults.
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

// TestDocumentDefaultLosesToHigherLayers checks that document default loses to higher layers.
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

// TestDocumentContainerAppliesWhole checks that document container applies whole.
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

// TestPrimarySourceReplacesDocumentContainerWhole checks that primary source replaces document container whole.
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

// TestUnknownDefaultsKeyStopsTheStartup checks that unknown defaults key stops the startup.
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

// TestDefaultsKeyNotAcceptedFromPrimaryStopsTheStartup checks that defaults key not accepted from primary stops the startup.
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

// TestDefaultsDocumentSatisfiesRequired checks that defaults document satisfies required.
func TestDefaultsDocumentSatisfiesRequired(t *testing.T) {
	cases := map[string]map[string]any{
		"flat":   {"cache.addr": "defaults:1"},
		"nested": {"cache": map[string]any{"addr": "defaults:1"}},
	}
	for name, document := range cases {
		t.Run(name, func(t *testing.T) {
			items := map[string]Item{"addr": {Required: true}}
			l, err := loadDefaults(t, yamlParser(document), items, withDefaults("yaml", "ignored"))
			if err != nil {
				t.Fatalf("loading with a defaults document failed: %v", err)
			}
			var opts cacheOptions
			if err := l.Reader().Decode("cache", &opts); err != nil {
				t.Fatalf("a required item supplied by the defaults document failed to decode: %v", err)
			}
			if opts.Addr != "defaults:1" {
				t.Fatalf("cache.addr decoded to %q, want the document's value: the prototype's "+
					"%q is not what satisfied the item", opts.Addr, "localhost:6379")
			}
		})
	}
}

// TestDocumentSilentOnRequiredItemKeepsPrototypeOut checks that document silent on required item keeps prototype out.
func TestDocumentSilentOnRequiredItemKeepsPrototypeOut(t *testing.T) {
	items := map[string]Item{"addr": {Required: true}}
	l, err := loadDefaults(t, yamlParser(map[string]any{
		"cache": map[string]any{"ttl": "2m"},
	}), items, withDefaults("yaml", "ignored"))
	if err != nil {
		t.Fatalf("loading with a defaults document failed: %v", err)
	}
	if got := l.m.byPath["cache.addr"].def; got != "localhost:6379" {
		t.Fatalf("the item's default is %#v, want the prototype's non-zero value: the case has to "+
			"prove the prototype holds something, or it tests nothing", got)
	}
	var opts cacheOptions
	err = l.Reader().Decode("cache", &opts)
	if !errors.Is(err, ErrMissingRequired) {
		t.Fatalf("a required item the document does not give returned %v, want %v",
			err, ErrMissingRequired)
	}
	if !strings.Contains(err.Error(), "cache.addr") {
		t.Fatalf("the rejection reads %q, which does not name the item", err)
	}
	for _, source := range []string{
		"primary config source", "environment", "command line", defaultsDocument, "carrier struct",
	} {
		if !strings.Contains(err.Error(), source) {
			t.Fatalf("the rejection reads %q, which does not mention %q", err, source)
		}
	}
}

// TestDocumentNullDoesNotSatisfyRequired checks that document null does not satisfy required.
func TestDocumentNullDoesNotSatisfyRequired(t *testing.T) {
	cases := map[string]map[string]any{
		"flat":   {"cache.addr": nil},
		"nested": {"cache": map[string]any{"addr": nil}},
	}
	for name, document := range cases {
		t.Run(name, func(t *testing.T) {
			items := map[string]Item{"addr": {Required: true}}
			l, err := loadDefaults(t, yamlParser(document), items, withDefaults("yaml", "ignored"))
			if err != nil {
				t.Fatalf("loading with a defaults document failed: %v", err)
			}
			if got := l.m.byPath["cache.addr"].def; got != "localhost:6379" {
				t.Fatalf("the item's default is %#v, want the prototype's value: a null does not "+
					"replace it either", got)
			}
			var opts cacheOptions
			if err := l.Reader().Decode("cache", &opts); !errors.Is(err, ErrMissingRequired) {
				t.Fatalf("a required item the document wrote as null returned %v, want %v",
					err, ErrMissingRequired)
			}
		})
	}
}

// TestDocumentSuppliesRequiredStringList checks that document supplies required string list.
func TestDocumentSuppliesRequiredStringList(t *testing.T) {
	type endpointsOptions struct{ Endpoints []string }
	prototype := endpointsOptions{Endpoints: []string{"proto-a", "proto-b"}}
	l := loadOver(t,
		providing("format.yaml", yamlParser(map[string]any{
			"cache": map[string]any{"endpoints": []any{"a", "b"}},
		})),
		declaring("cache", Schema{
			Namespace: "cache",
			Mounts:    []Mount{{Value: &prototype}},
			Items: map[string]Item{
				"endpoints": {Origins: OriginPrimary | OriginEnv | OriginFlag, FlagName: "endpoints", Required: true},
			},
		}),
		withDefaults("yaml", "ignored"),
	)
	var opts endpointsOptions
	if err := l.Reader().Decode("cache", &opts); err != nil {
		t.Fatalf("a required string list supplied by the defaults document failed to decode: %v", err)
	}
	want := []string{"a", "b"}
	if !reflect.DeepEqual(opts.Endpoints, want) {
		t.Fatalf("cache.endpoints decoded to %#v, want the document's %#v", opts.Endpoints, want)
	}
}

// TestDocumentSuppliesRequiredContainerLeaf checks that document supplies required container leaf.
func TestDocumentSuppliesRequiredContainerLeaf(t *testing.T) {
	l, err := loadDefaults(t, yamlParser(map[string]any{
		"cache": map[string]any{
			"labels": map[string]any{"Zone": "cold"},
			"nodes":  []any{map[string]any{"addr": "node:1", "pool-size": 3}},
		},
	}), map[string]Item{
		"labels": {Required: true},
		"nodes":  {Required: true},
	}, withDefaults("yaml", "ignored"))
	if err != nil {
		t.Fatalf("loading with a defaults document failed: %v", err)
	}
	opts := decodeCacheOptions(t, l)
	wantLabels := map[string]string{"Zone": "cold"}
	if !reflect.DeepEqual(opts.Labels, wantLabels) {
		t.Fatalf("cache.labels decoded to %#v, want the document's %#v: a required container the "+
			"document supplies is satisfied and reads back whole", opts.Labels, wantLabels)
	}
	wantNodes := []redisOptions{{Addr: "node:1", PoolSize: 3}}
	if !reflect.DeepEqual(opts.Nodes, wantNodes) {
		t.Fatalf("cache.nodes decoded to %#v, want the document's %#v: a required struct list the "+
			"document supplies is satisfied and reads back whole", opts.Nodes, wantNodes)
	}
}

// TestDocumentSuppliesRequiredPointerLeaf checks that document supplies required pointer leaf.
func TestDocumentSuppliesRequiredPointerLeaf(t *testing.T) {
	type retriesOptions struct{ Retries *int }
	prototype := retriesOptions{}
	l := loadOver(t,
		providing("format.yaml", yamlParser(map[string]any{
			"cache": map[string]any{"retries": 3},
		})),
		declaring("cache", Schema{
			Namespace: "cache",
			Mounts:    []Mount{{Value: &prototype}},
			Items:     map[string]Item{"retries": {Required: true}},
		}),
		withDefaults("yaml", "ignored"),
	)
	var opts retriesOptions
	if err := l.Reader().Decode("cache", &opts); err != nil {
		t.Fatalf("a required pointer the defaults document supplies failed to decode: %v", err)
	}
	if opts.Retries == nil || *opts.Retries != 3 {
		t.Fatalf("cache.retries decoded to %v, want a pointer to 3", opts.Retries)
	}
}

// TestDocumentSatisfiedRequiredValueLosesToHigherLayers checks that document satisfied required value loses to higher layers.
func TestDocumentSatisfiedRequiredValueLosesToHigherLayers(t *testing.T) {
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
			items := map[string]Item{"addr": {Origins: OriginPrimary | OriginEnv | OriginFlag, FlagName: "cache-addr", Required: true}}
			l, err := loadDefaults(t, yamlParser(map[string]any{
				"cache": map[string]any{"addr": "defaults:1"},
			}), items, withDefaults("yaml", "ignored"))
			if err != nil {
				t.Fatalf("loading with a defaults document failed: %v", err)
			}
			if tc.primary != nil {
				givePrimaryWhole(t, l, tc.primary)
			}
			var opts cacheOptions
			if err := l.Reader().Decode("cache", &opts); err != nil {
				t.Fatalf("decoding failed: %v", err)
			}
			if opts.Addr != tc.want {
				t.Fatalf("cache.addr decoded to %q, want %q", opts.Addr, tc.want)
			}
		})
	}
}

// TestDocumentDefaultGoesThroughTheTypeRules checks that document default goes through the type rules.
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

// TestTwoDefaultsDocumentsConflict checks that two defaults documents conflict.
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

// TestHelpRendersDocumentDefault checks that help renders document default.
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

// TestHelpRendersDocumentListDefault checks that help renders document list default.
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

// TestDocumentDefaultStaysOutOfHelpWhenSensitive checks that document default stays out of help when sensitive.
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

// TestDefaultsFormatMustBeRegistered checks that defaults format must be registered.
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

	if _, err := loadDefaults(t, yamlParser(map[string]any{
		"cache": map[string]any{"addr": "defaults:1"},
	}), nil, withDefaults("YAML", "ignored")); err != nil {
		t.Fatalf("the format name YAML did not reach the parser registered as yaml: %v", err)
	}
}

// TestMalformedDefaultsDocumentStopsTheStartup checks that malformed defaults document stops the startup.
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
