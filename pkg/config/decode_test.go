package config

import (
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/vislake/speed/pkg/core"
)

type decodeOptions struct {
	Addr string
	TTL  time.Duration
}

func readerOver(t *testing.T, modules ...core.Module) (*reader, *loader) {
	t.Helper()
	l := loadOver(t, modules...)
	return l.Reader().(*reader), l
}

// TestDecodeYieldsDefaultsIntoZeroStruct checks that decode yields defaults into zero struct.
func TestDecodeYieldsDefaultsIntoZeroStruct(t *testing.T) {
	defaults := decodeOptions{Addr: "localhost:6379", TTL: 5 * time.Minute}
	r, _ := readerOver(t, declaring("cache", Schema{Namespace: "cache", Mounts: []Mount{{Value: &defaults}}}))

	var opts decodeOptions
	if err := r.Decode("cache", &opts); err != nil {
		t.Fatalf("decoding failed: %v", err)
	}
	if opts.TTL != 5*time.Minute || opts.Addr != "localhost:6379" {
		t.Fatalf("a zero struct decoded to %#v, want the declared defaults", opts)
	}
}

// TestDecodeDoesNotConsultMountPrototype checks that decode does not consult mount prototype.
func TestDecodeDoesNotConsultMountPrototype(t *testing.T) {
	defaults := decodeOptions{Addr: "localhost:6379"}
	r, _ := readerOver(t, declaring("cache", Schema{Namespace: "cache", Mounts: []Mount{{Value: &defaults}}}))

	defaults.Addr = "changed-after-collection"
	var opts decodeOptions
	if err := r.Decode("cache", &opts); err != nil {
		t.Fatalf("decoding failed: %v", err)
	}
	if opts.Addr != "localhost:6379" {
		t.Fatalf("the decoded value is %q, want the value the prototype held when it was "+
			"collected", opts.Addr)
	}
}

// TestPrototypeSharedAcrossRegistriesIsIsolated checks that prototype shared across registries is isolated.
func TestPrototypeSharedAcrossRegistriesIsIsolated(t *testing.T) {
	defaults := decodeOptions{Addr: "localhost:6379"}
	declaration := declaring("cache", Schema{Namespace: "cache", Mounts: []Mount{{Value: &defaults}}})

	firstReader, first := readerOver(t, declaration)
	if err := givePrimary(t, first, map[string]any{"cache": map[string]any{"addr": "one:1"}}); err != nil {
		t.Fatalf("applying the primary source failed: %v", err)
	}

	secondReader, _ := readerOver(t, declaration)

	var a, b decodeOptions
	if err := firstReader.Decode("cache", &a); err != nil {
		t.Fatalf("decoding the first registry failed: %v", err)
	}
	if err := secondReader.Decode("cache", &b); err != nil {
		t.Fatalf("decoding the second registry failed: %v", err)
	}
	if a.Addr != "one:1" {
		t.Fatalf("the first registry decoded %q, want the value its own source gave", a.Addr)
	}
	if b.Addr != "localhost:6379" {
		t.Fatalf("the second registry decoded %q, want the declared default: the two registries "+
			"share a prototype and nothing else", b.Addr)
	}
}

// TestDecodeUnknownPathLeavesTargetUntouched checks that decode unknown path leaves target untouched.
func TestDecodeUnknownPathLeavesTargetUntouched(t *testing.T) {
	defaults := decodeOptions{Addr: "localhost:6379"}
	r, _ := readerOver(t, declaring("cache", Schema{Namespace: "cache", Mounts: []Mount{{Value: &defaults}}}))

	opts := decodeOptions{Addr: "put here by the caller", TTL: time.Second}
	if err := r.Decode("nowhere", &opts); err != nil {
		t.Fatalf("decoding a path nobody declared returned %v, want no error", err)
	}
	if opts.Addr != "put here by the caller" || opts.TTL != time.Second {
		t.Fatalf("the target became %#v, want what the caller put in it", opts)
	}
}

type requiredOptions struct {
	Addr  string
	Token string
}

func requiredSetup(t *testing.T) (*reader, *loader) {
	t.Helper()
	defaults := requiredOptions{Addr: "unset-but-not-empty"}
	return readerOver(t,
		identifying("host", HostIdentity{Prefix: "MYAPP"}),
		declaring("remote", Schema{
			Namespace: "remote",
			Mounts:    []Mount{{Value: &defaults}},
			Items: map[string]Item{
				"addr": {Origins: OriginPrimary | OriginEnv | OriginFlag, Required: true, FlagName: "remote-addr"},
			},
		}),
	)
}

// TestRequiredMissingAcrossThreeLayers checks that required missing across three layers.
func TestRequiredMissingAcrossThreeLayers(t *testing.T) {
	r, _ := requiredSetup(t)

	var opts requiredOptions
	err := r.Decode("remote", &opts)
	if !errors.Is(err, ErrMissingRequired) {
		t.Fatalf("a required item nobody gave returned %v, want ErrMissingRequired", err)
	}
	if !strings.Contains(err.Error(), "remote.addr") {
		t.Fatalf("the rejection reads %q, which does not name the path", err)
	}
}

// TestDefaultLayerDoesNotSatisfyRequired checks that default layer does not satisfy required.
func TestDefaultLayerDoesNotSatisfyRequired(t *testing.T) {
	r, l := requiredSetup(t)
	if got := l.v.Get("remote.addr"); got != "unset-but-not-empty" {
		t.Fatalf("the base layer holds %#v, and this case needs a non-zero default", got)
	}
	var opts requiredOptions
	if err := r.Decode("remote", &opts); !errors.Is(err, ErrMissingRequired) {
		t.Fatalf("a required item holding a non-zero default returned %v, want ErrMissingRequired", err)
	}
}

// TestRequiredSatisfiedByOneLayerAlone checks that required satisfied by one layer alone.
func TestRequiredSatisfiedByOneLayerAlone(t *testing.T) {
	for name, give := range map[string]func(*testing.T) *reader{
		"the primary source": func(t *testing.T) *reader {
			r, l := requiredSetup(t)
			if err := givePrimary(t, l, map[string]any{"remote": map[string]any{"addr": "a:1"}}); err != nil {
				t.Fatalf("applying the primary source failed: %v", err)
			}
			return r
		},
		"the environment": func(t *testing.T) *reader {
			t.Setenv("MYAPP_REMOTE__ADDR", "a:1")
			r, _ := requiredSetup(t)
			return r
		},
		"the command line": func(t *testing.T) *reader {
			r, l := requiredSetup(t)
			if err := giveFlags(t, l, []string{"remote"}, "--remote-addr=a:1"); err != nil {
				t.Fatalf("parsing the command line failed: %v", err)
			}
			return r
		},
	} {
		t.Run(name, func(t *testing.T) {
			r := give(t)

			var opts requiredOptions
			if err := r.Decode("remote", &opts); err != nil {
				t.Fatalf("a required item given by %s returned %v, want no error", name, err)
			}
			if opts.Addr != "a:1" {
				t.Fatalf("the decoded value is %q, want what %s gave", opts.Addr, name)
			}
		})
	}
}

// TestRequiredNotCheckedGlobally checks that required not checked globally.
func TestRequiredNotCheckedGlobally(t *testing.T) {
	otherDefaults := requiredOptions{}
	r, _ := readerOver(t,
		identifying("host", HostIdentity{Prefix: "MYAPP"}),
		declaring("remote", Schema{
			Namespace: "remote",
			Mounts:    []Mount{{Value: &requiredOptions{}}},
			Items:     map[string]Item{"addr": {Required: true}},
		}),
		declaring("local", Schema{Namespace: "local", Mounts: []Mount{{Value: &otherDefaults}}}),
	)

	var opts requiredOptions
	if err := r.Decode("local", &opts); err != nil {
		t.Fatalf("decoding one module's mount failed on another module's required item: %v", err)
	}
}

type addrOnly struct {
	Addr string
}

type portOnly struct {
	Port int
}

// TestDecodeIgnoresOtherModulesKeysInSameNamespace checks that decode ignores other modules keys in same namespace.
func TestDecodeIgnoresOtherModulesKeysInSameNamespace(t *testing.T) {
	r, _ := readerOver(t,
		declaring("first", Schema{Namespace: "shared", Mounts: []Mount{{Value: &addrOnly{Addr: "a:1"}}}}),
		declaring("second", Schema{Namespace: "shared", Mounts: []Mount{{Value: &portOnly{Port: 9}}}}),
	)

	var mine addrOnly
	if err := r.Decode("shared", &mine); err != nil {
		t.Fatalf("decoding a shared namespace failed: %v", err)
	}
	if mine.Addr != "a:1" {
		t.Fatalf("the decoded value is %q, want this module's own default", mine.Addr)
	}

	var theirs portOnly
	if err := r.Decode("shared", &theirs); err != nil {
		t.Fatalf("decoding the other module's mount failed: %v", err)
	}
	if theirs.Port != 9 {
		t.Fatalf("the other module decoded %d, want its own default", theirs.Port)
	}
}

// TestDecodeWritesEveryLayer checks that decode writes every layer.
func TestDecodeWritesEveryLayer(t *testing.T) {
	type nested struct {
		PoolSize int
	}
	type carrier struct {
		Addr  string
		Redis nested
	}
	defaults := carrier{Addr: "localhost:6379", Redis: nested{PoolSize: 10}}
	t.Setenv("MYAPP_CACHE__REDIS__POOL_SIZE", "42")
	r, _ := readerOver(t,
		identifying("host", HostIdentity{Prefix: "MYAPP"}),
		declaring("cache", Schema{Namespace: "cache", Mounts: []Mount{{Value: &defaults}}}),
	)

	var opts carrier
	if err := r.Decode("cache", &opts); err != nil {
		t.Fatalf("decoding failed: %v", err)
	}
	if opts.Redis.PoolSize != 42 {
		t.Fatalf("the nested value decoded to %d, want the environment's value", opts.Redis.PoolSize)
	}
	if opts.Addr != "localhost:6379" {
		t.Fatalf("the sibling value decoded to %q, want the declared default", opts.Addr)
	}
}

// TestDecodeNeedsAPointerToStruct checks that decode needs a pointer to struct.
func TestDecodeNeedsAPointerToStruct(t *testing.T) {
	r, _ := readerOver(t, declaring("cache", Schema{Namespace: "cache", Mounts: []Mount{{Value: &decodeOptions{}}}}))

	for name, c := range map[string]struct {
		target any
		names  string
	}{
		"a value":               {target: decodeOptions{}, names: "config.decodeOptions"},
		"a nil pointer":         {target: (*decodeOptions)(nil), names: "*config.decodeOptions"},
		"a pointer to a scalar": {target: new(int), names: "*int"},
	} {
		t.Run(name, func(t *testing.T) {
			defer func() {
				raised := recover()
				if raised == nil {
					t.Fatalf("decoding into %s returned, and there is nowhere to write", name)
				}
				text := fmt.Sprint(raised)
				if !strings.Contains(text, c.names) || !strings.Contains(text, `"cache"`) {
					t.Fatalf("the panic reads %q, which does not name both the target %s and "+
						"the path it was given for", text, c.names)
				}
			}()
			_ = r.Decode("cache", c.target)
		})
	}
}

type cyclicTarget struct {
	Addr string
	Next *cyclicTarget
}

// TestDecodeRefusesATargetThatCyclesBack checks that decode refuses a target that cycles back.
func TestDecodeRefusesATargetThatCyclesBack(t *testing.T) {
	r, _ := readerOver(t, declaring("cache", Schema{Namespace: "cache", Mounts: []Mount{{Value: &decodeOptions{}}}}))

	var target cyclicTarget
	target.Next = &target
	defer func() {
		raised := recover()
		if raised == nil {
			t.Fatalf("decoding into a target that reaches itself returned, and the walk does " +
				"not terminate")
		}
		text := fmt.Sprint(raised)
		if !strings.Contains(text, "Next") {
			t.Fatalf("the panic reads %q, which does not name the field that closes the cycle", text)
		}
	}()
	_ = r.Decode("cache", &target)
}

// TestDecodeReadsAnInlinedEmbeddedField checks that decode reads an inlined embedded field.
func TestDecodeReadsAnInlinedEmbeddedField(t *testing.T) {
	type inlineTarget struct {
		EmbeddedOptions
		Name string
	}
	defaults := inlineTarget{}
	r, l := readerOver(t, declaring("cache", Schema{Namespace: "cache", Mounts: []Mount{{Value: &defaults}}}))
	if err := givePrimary(t, l, map[string]any{"cache": map[string]any{"addr": "given:1"}}); err != nil {
		t.Fatalf("applying the primary source failed: %v", err)
	}

	var opts inlineTarget
	if err := r.Decode("cache", &opts); err != nil {
		t.Fatalf("decoding failed: %v", err)
	}
	if opts.Addr != "given:1" {
		t.Fatalf("the inlined field decoded to %q, want the value the primary source gave", opts.Addr)
	}
}

// TestContainerKeepsTheKeysTheSourceWrote checks that container keeps the keys the source wrote.
func TestContainerKeepsTheKeysTheSourceWrote(t *testing.T) {
	type holder struct {
		Labels map[string]string `config:"labels"`
	}
	var defaults holder
	l := loadOver(t,
		declaring("store", Schema{
			Namespace: "store",
			Mounts:    []Mount{{Value: &defaults}},
		}),
		providing("transport",
			fakeSource{scheme: "test", data: []byte("ignored"), format: "fake"},
			fakeFormat{name: "fake", content: map[string]any{
				"store": map[string]any{"labels": map[string]any{
					"Zone": "EU-West", "TierName": "Cold", "lower": "plain",
				}},
			}}))
	if err := l.ReadPrimary(t.Context(), "test:///anywhere"); err != nil {
		t.Fatalf("reading the primary source failed: %v", err)
	}
	var got holder
	if err := l.Reader().Decode("store", &got); err != nil {
		t.Fatalf("decoding failed: %v", err)
	}
	for _, key := range []string{"Zone", "TierName", "lower"} {
		if _, present := got.Labels[key]; !present {
			t.Fatalf("the container reads back as %v, and the source wrote %q", got.Labels, key)
		}
	}
}

// TestContainerElementTakesItsJSONTagAsWritten checks that container element takes its JSON tag as written.
func TestContainerElementTakesItsJSONTagAsWritten(t *testing.T) {
	type tier struct {
		TierName string `json:"tierName"`
		MaxSize  int    `json:"maxSize"`
	}
	type holder struct {
		Tiers []tier `config:"tiers"`
	}
	var defaults holder
	l := loadOver(t,
		declaring("storage", Schema{
			Namespace: "storage",
			Mounts:    []Mount{{Value: &defaults}},
		}),
		providing("transport",
			fakeSource{scheme: "test", data: []byte("ignored"), format: "fake"},
			fakeFormat{name: "fake", content: map[string]any{
				"storage": map[string]any{"tiers": []any{
					map[string]any{"tierName": "hot", "maxSize": 10},
				}},
			}}))
	if err := l.ReadPrimary(t.Context(), "test:///anywhere"); err != nil {
		t.Fatalf("reading the primary source failed: %v", err)
	}
	var got holder
	if err := l.Reader().Decode("storage", &got); err != nil {
		t.Fatalf("decoding failed: %v", err)
	}
	if len(got.Tiers) != 1 {
		t.Fatalf("the source gave one element and the target holds %d: %v", len(got.Tiers), got.Tiers)
	}
	if got.Tiers[0].TierName != "hot" || got.Tiers[0].MaxSize != 10 {
		t.Fatalf("the element reads back as %+v, and the source wrote what its json tags name",
			got.Tiers[0])
	}
}

// TestSourceWritingNothingLeavesEveryLeafItsDefault checks that source writing nothing leaves every leaf its default.
func TestSourceWritingNothingLeavesEveryLeafItsDefault(t *testing.T) {
	type holder struct {
		Labels map[string]string `config:"labels"`
		Hosts  []string          `config:"hosts"`
		Addr   string            `config:"addr"`
	}
	defaults := holder{
		Labels: map[string]string{"DefaultKey": "dv"},
		Hosts:  []string{"d1"},
		Addr:   "dflt",
	}
	l := loadOver(t,
		declaring("storage", Schema{
			Namespace: "storage",
			Mounts:    []Mount{{Value: &defaults}},
		}),
		providing("transport",
			fakeSource{scheme: "test", data: []byte("ignored"), format: "fake"},
			fakeFormat{name: "fake", content: map[string]any{
				"storage": map[string]any{"labels": nil, "hosts": nil, "addr": nil},
			}}))
	if err := l.ReadPrimary(t.Context(), "test:///anywhere"); err != nil {
		t.Fatalf("reading the primary source failed: %v", err)
	}
	var got holder
	if err := l.Reader().Decode("storage", &got); err != nil {
		t.Fatalf("decoding failed: %v", err)
	}
	if len(got.Labels) != 1 || got.Hosts[0] != "d1" || got.Addr != "dflt" {
		t.Fatalf("the three leaves read back as labels=%v hosts=%v addr=%q, and a key written "+
			"with nothing behind it leaves every one of them its declared default",
			got.Labels, got.Hosts, got.Addr)
	}
}

// TestContainerKeepsItsKeysWhenThePathIsWrittenFlat checks that container keeps its keys when the path is written flat.
func TestContainerKeepsItsKeysWhenThePathIsWrittenFlat(t *testing.T) {
	type holder struct {
		Labels map[string]string `config:"labels"`
	}
	var defaults holder
	l := loadOver(t,
		declaring("storage", Schema{
			Namespace: "storage",
			Mounts:    []Mount{{Value: &defaults}},
		}),
		providing("transport",
			fakeSource{scheme: "test", data: []byte("ignored"), format: "fake"},
			fakeFormat{name: "fake", content: map[string]any{
				"storage.labels": map[string]any{"Zone": "EU-West"},
			}}))
	if err := l.ReadPrimary(t.Context(), "test:///anywhere"); err != nil {
		t.Fatalf("reading the primary source failed: %v", err)
	}
	var got holder
	if err := l.Reader().Decode("storage", &got); err != nil {
		t.Fatalf("decoding failed: %v", err)
	}
	if _, present := got.Labels["Zone"]; !present {
		t.Fatalf("the flat spelling reads back as %v, and the source wrote %q", got.Labels, "Zone")
	}
}

// TestOneHolderCannotWriteIntoAnother checks that one holder cannot write into another.
func TestOneHolderCannotWriteIntoAnother(t *testing.T) {
	type holder struct {
		Anys map[string]any `config:"anys"`
	}
	var defaults holder
	l := loadOver(t,
		declaring("storage", Schema{
			Namespace: "storage",
			Mounts:    []Mount{{Value: &defaults}},
		}),
		providing("transport",
			fakeSource{scheme: "test", data: []byte("ignored"), format: "fake"},
			fakeFormat{name: "fake", content: map[string]any{
				"storage": map[string]any{"anys": map[string]any{
					"outer": map[string]any{"inner": "original"},
				}},
			}}))
	if err := l.ReadPrimary(t.Context(), "test:///anywhere"); err != nil {
		t.Fatalf("reading the primary source failed: %v", err)
	}
	reader := l.Reader()
	var first, second holder
	if err := reader.Decode("storage", &first); err != nil {
		t.Fatalf("the first decode failed: %v", err)
	}
	inner, isSection := first.Anys["outer"].(map[string]any)
	if !isSection {
		t.Fatalf("the first holder received %#v, want a section under outer", first.Anys["outer"])
	}
	inner["inner"] = "written by the first holder"

	if err := reader.Decode("storage", &second); err != nil {
		t.Fatalf("the second decode failed: %v", err)
	}
	again, isSection := second.Anys["outer"].(map[string]any)
	if !isSection {
		t.Fatalf("the second holder received %#v, want a section under outer", second.Anys["outer"])
	}
	if again["inner"] != "original" {
		t.Fatalf("the second holder reads %q, and what the first one wrote is its own", again["inner"])
	}
}
