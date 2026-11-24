package config

import (
	"errors"
	"maps"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/vislake/speed/pkg/core"
)

func expandOne(t *testing.T, s Schema, prefix string) map[string]*manifestItem {
	t.Helper()
	items, err := expandSchema("mod", s, prefix)
	if err != nil {
		t.Fatalf("expanding the declaration failed: %v", err)
	}
	byPath := make(map[string]*manifestItem, len(items))
	for _, item := range items {
		byPath[item.path] = item
	}
	return byPath
}

func expandFails(t *testing.T, s Schema, prefix string) error {
	t.Helper()
	items, err := expandSchema("mod", s, prefix)
	if err == nil {
		t.Fatalf("expanding the declaration produced %d items, want a rejection", len(items))
	}
	if !errors.Is(err, ErrInvalidSchema) {
		t.Fatalf("expansion returned %v, want ErrInvalidSchema", err)
	}
	return err
}

func mount(value any) []Mount { return []Mount{{Value: value}} }

func pathsOf(byPath map[string]*manifestItem) []string {
	return slices.Sorted(maps.Keys(byPath))
}

// TestFieldPathTagPrecedence checks that field path tag precedence.
func TestFieldPathTagPrecedence(t *testing.T) {
	type carrier struct {
		Tagged   string `config:"alpha" json:"beta"`
		JSONOnly string `json:"gamma,omitempty"`
		PoolSize int
	}
	byPath := expandOne(t, Schema{Mounts: mount(&carrier{})}, "")
	want := []string{"alpha", "gamma", "pool-size"}
	if got := pathsOf(byPath); !slices.Equal(got, want) {
		t.Fatalf("expansion produced paths %v, want %v", got, want)
	}
}

// TestCamelToKebab checks that camel to kebab.
func TestCamelToKebab(t *testing.T) {
	cases := map[string]string{
		"PoolSize":  "pool-size",
		"Addr":      "addr",
		"HTTPPort":  "http-port",
		"ID":        "id",
		"MaxConns2": "max-conns2",
	}
	for field, want := range cases {
		if got := camelToKebab(field); got != want {
			t.Errorf("field %s derives key %q, want %q", field, got, want)
		}
	}
}

// TestKeySegmentCharsetRejects checks that key segment charset rejects.
func TestKeySegmentCharsetRejects(t *testing.T) {
	type underscore struct {
		Field string `config:"pool_size"`
	}
	type capital struct {
		Field string `config:"PoolSize"`
	}
	type dotted struct {
		Field string `config:"redis.addr"`
	}
	type plain struct {
		Field string
	}
	type doubleUnderscore struct {
		Field string `config:"a__b"`
	}
	cases := map[string]struct {
		schema   Schema
		fragment string
	}{
		"underscore in a key":     {Schema{Mounts: mount(&underscore{})}, "pool_size"},
		"upper case in a key":     {Schema{Mounts: mount(&capital{})}, "PoolSize"},
		"dot inside a segment":    {Schema{Mounts: mount(&dotted{})}, "redis.addr"},
		"double underscore":       {Schema{Mounts: mount(&doubleUnderscore{})}, "a__b"},
		"empty namespace segment": {Schema{Namespace: "cache..redis", Mounts: mount(&plain{})}, "empty segment"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			err := expandFails(t, tc.schema, "")
			if !strings.Contains(err.Error(), tc.fragment) {
				t.Fatalf("rejection reads %q, which does not mention %q", err, tc.fragment)
			}
		})
	}
}

// TestSkipsUndecodableFields checks that skips undecodable fields.
func TestSkipsUndecodableFields(t *testing.T) {
	type inner struct {
		Addr string
	}
	type carrier struct {
		Kept     string
		hidden   string //nolint:unused // the point of the case is that it is skipped
		Hook     func() error
		Events   chan int
		Anything any
		Absent   *inner
		Present  *inner
	}
	byPath := expandOne(t, Schema{Mounts: mount(&carrier{Present: &inner{Addr: "localhost"}})}, "")
	want := []string{"kept", "present.addr"}
	if got := pathsOf(byPath); !slices.Equal(got, want) {
		t.Fatalf("expansion produced paths %v, want %v", got, want)
	}
	for _, skipped := range []string{"hidden", "hook", "events", "anything", "absent"} {
		if _, found := byPath[skipped]; found {
			t.Errorf("path %q reached the manifest, and no source can give it a value", skipped)
		}
	}
}

// TestNonNilStructPointerExpands checks that non nil struct pointer expands.
func TestNonNilStructPointerExpands(t *testing.T) {
	type tls struct {
		ServerName string
		MinVersion int
	}
	type carrier struct {
		TLS *tls
	}
	byPath := expandOne(t, Schema{Mounts: mount(&carrier{TLS: &tls{ServerName: "example.test", MinVersion: 3}})}, "")
	if got := byPath["tls.server-name"]; got == nil || got.def != "example.test" {
		t.Fatalf("tls.server-name is %+v, want the prototype's current value example.test", got)
	}
	if got := byPath["tls.min-version"]; got == nil || got.def != 3 {
		t.Fatalf("tls.min-version is %+v, want the prototype's current value 3", got)
	}
}

type ringA struct {
	B *ringB
}

type ringB struct {
	A *ringA
}

// TestTypeCycleNamesFieldAndType checks that type cycle names field and type.
func TestTypeCycleNamesFieldAndType(t *testing.T) {
	a := &ringA{}
	b := &ringB{A: a}
	a.B = b
	err := expandFails(t, Schema{Mounts: mount(a)}, "")
	for _, fragment := range []string{"config.ringA", "A"} {
		if !strings.Contains(err.Error(), fragment) {
			t.Fatalf("rejection reads %q, which does not mention %q", err, fragment)
		}
	}
}

// TestSameTypeOnSiblingBranchesIsNotCycle checks that same type on sibling branches is not cycle.
func TestSameTypeOnSiblingBranchesIsNotCycle(t *testing.T) {
	type node struct {
		Name string
	}
	type carrier struct {
		Left  *node
		Right *node
	}
	byPath := expandOne(t, Schema{Mounts: mount(&carrier{Left: &node{}, Right: &node{}})}, "")
	want := []string{"left.name", "right.name"}
	if got := pathsOf(byPath); !slices.Equal(got, want) {
		t.Fatalf("expansion produced paths %v, want %v", got, want)
	}
}

// TestDeepNestingHasNoDepthLimit checks that deep nesting has no depth limit.
func TestDeepNestingHasNoDepthLimit(t *testing.T) {
	const depth = 20
	nested := reflect.StructOf([]reflect.StructField{{Name: "Leaf", Type: reflect.TypeFor[string]()}})
	for range depth - 1 {
		nested = reflect.StructOf([]reflect.StructField{{Name: "Inner", Type: nested}})
	}
	byPath := expandOne(t, Schema{Mounts: mount(reflect.New(nested).Interface())}, "")
	want := strings.Repeat("inner.", depth-1) + "leaf"
	if got := pathsOf(byPath); !slices.Equal(got, []string{want}) {
		t.Fatalf("expansion produced paths %v, want the single path %q", got, want)
	}
}

// TestEnvNameDerivation checks that env name derivation.
func TestEnvNameDerivation(t *testing.T) {
	type redis struct {
		MaxConns int
	}
	byPath := expandOne(t, Schema{
		Namespace: "cache",
		Mounts:    []Mount{{Path: "redis", Value: &redis{}}},
	}, "MYAPP")
	item := byPath["cache.redis.max-conns"]
	if item == nil {
		t.Fatalf("expansion produced paths %v, want cache.redis.max-conns", pathsOf(byPath))
	}
	if want := "MYAPP_CACHE__REDIS__MAX_CONNS"; item.envName != want {
		t.Fatalf("item reads environment variable %q, want %q", item.envName, want)
	}
}

// TestPinnedEnvNameWins checks that pinned env name wins.
func TestPinnedEnvNameWins(t *testing.T) {
	type carrier struct {
		Colour string
	}
	byPath := expandOne(t, Schema{
		Mounts: mount(&carrier{}),
		Items:  map[string]Item{"colour": {EnvName: "NO_COLOR"}},
	}, "MYAPP")
	if got := byPath["colour"].envName; got != "NO_COLOR" {
		t.Fatalf("item reads environment variable %q, want the pinned NO_COLOR", got)
	}
}

// TestNoPrefixDisablesDerivedEnvNamesOnly checks that no prefix disables derived env names only.
func TestNoPrefixDisablesDerivedEnvNamesOnly(t *testing.T) {
	type carrier struct {
		Colour   string
		PoolSize int
	}
	byPath := expandOne(t, Schema{
		Mounts: mount(&carrier{}),
		Items:  map[string]Item{"colour": {EnvName: "NO_COLOR"}},
	}, "")
	if got := byPath["colour"].envName; got != "NO_COLOR" {
		t.Fatalf("pinned item reads environment variable %q, want NO_COLOR", got)
	}
	if got := byPath["pool-size"].envName; got != "" {
		t.Fatalf("derived item reads environment variable %q, want none without a prefix", got)
	}
}

// TestEmptyNamespaceLandsAtTopLevel checks that empty namespace lands at top level.
func TestEmptyNamespaceLandsAtTopLevel(t *testing.T) {
	type carrier struct {
		LogLevel string
	}
	byPath := expandOne(t, Schema{Mounts: mount(&carrier{})}, "MYAPP")
	item := byPath["log-level"]
	if item == nil {
		t.Fatalf("expansion produced paths %v, want the top-level path log-level", pathsOf(byPath))
	}
	if want := "MYAPP_LOG_LEVEL"; item.envName != want {
		t.Fatalf("item reads environment variable %q, want %q", item.envName, want)
	}
}

// TestContainerFieldsAreLeaves checks that container fields are leaves.
func TestContainerFieldsAreLeaves(t *testing.T) {
	type endpoint struct {
		Addr string
	}
	type carrier struct {
		Labels    map[string]string
		Endpoints []endpoint
		Names     []string
		Deadline  time.Duration
	}
	byPath := expandOne(t, Schema{Mounts: mount(&carrier{})}, "")
	want := []string{"deadline", "endpoints", "labels", "names"}
	if got := pathsOf(byPath); !slices.Equal(got, want) {
		t.Fatalf("expansion produced paths %v, want %v", got, want)
	}
	kinds := map[string]leafKind{
		"labels":    kindContainer,
		"endpoints": kindContainer,
		"names":     kindStringList,
		"deadline":  kindScalar,
	}
	for path, want := range kinds {
		if got := byPath[path].kind; got != want {
			t.Errorf("item %q is kind %d, want %d", path, got, want)
		}
	}
}

// TestFlagOriginRequiresFlagName checks that flag origin requires flag name.
func TestFlagOriginRequiresFlagName(t *testing.T) {
	type carrier struct {
		Addr string
	}
	err := expandFails(t, Schema{
		Mounts: mount(&carrier{}),
		Items:  map[string]Item{"addr": {Origins: OriginFlag}},
	}, "")
	if !strings.Contains(err.Error(), "FlagName") {
		t.Fatalf("rejection reads %q, which does not mention FlagName", err)
	}
}

// TestSensitiveRequiresDescription checks that sensitive requires description.
func TestSensitiveRequiresDescription(t *testing.T) {
	type carrier struct {
		Password string
	}
	err := expandFails(t, Schema{
		Mounts: mount(&carrier{}),
		Items:  map[string]Item{"password": {Sensitive: true}},
	}, "")
	if !strings.Contains(err.Error(), "password") {
		t.Fatalf("rejection reads %q, which does not name the item", err)
	}
}

// TestOrphanItemKeyRejected checks that orphan item key rejected.
func TestOrphanItemKeyRejected(t *testing.T) {
	type redis struct {
		Addr string
	}
	err := expandFails(t, Schema{
		Mounts: []Mount{{Path: "redis", Value: &redis{}}},
		Items:  map[string]Item{"redis.addres": {Required: true}},
	}, "")
	for _, fragment := range []string{"mod", "redis.addres"} {
		if !strings.Contains(err.Error(), fragment) {
			t.Fatalf("rejection reads %q, which does not mention %q", err, fragment)
		}
	}
}

// TestMountValueMustCarryAStruct checks that mount value must carry a struct.
func TestMountValueMustCarryAStruct(t *testing.T) {
	var absent *struct{ Addr string }
	cases := map[string]any{
		"a scalar":             "not a struct",
		"a nil struct pointer": absent,
		"nothing at all":       nil,
	}
	for name, value := range cases {
		t.Run(name, func(t *testing.T) {
			expandFails(t, Schema{Mounts: mount(value)}, "")
		})
	}
}

// TestTagDashSkipsField checks that tag dash skips field.
func TestTagDashSkipsField(t *testing.T) {
	type carrier struct {
		Kept     string
		Internal string `json:"-"`
		Excluded string `config:"-"`
	}
	byPath := expandOne(t, Schema{Mounts: mount(&carrier{})}, "")
	if got := pathsOf(byPath); !slices.Equal(got, []string{"kept"}) {
		t.Fatalf("expansion produced paths %v, want the single path kept", got)
	}
}

// TestTextUnmarshalerStructIsALeaf checks that text unmarshaler struct is a leaf.
func TestTextUnmarshalerStructIsALeaf(t *testing.T) {
	type carrier struct {
		Deadline time.Time
	}
	byPath := expandOne(t, Schema{Mounts: mount(&carrier{})}, "")
	item := byPath["deadline"]
	if item == nil {
		t.Fatalf("expansion produced paths %v, want the single path deadline", pathsOf(byPath))
	}
	if item.kind != kindScalar || item.typ != reflect.TypeFor[time.Time]() {
		t.Fatalf("deadline is kind %d of type %s, want a scalar of time.Time", item.kind, item.typ)
	}
}

// TestEmbeddedStructInlinesWithoutASegment checks that embedded struct inlines without a segment.
func TestEmbeddedStructInlinesWithoutASegment(t *testing.T) {
	byPath := expandOne(t, Schema{Mounts: mount(&embedder{})}, "")
	want := []string{"addr", "name"}
	if got := pathsOf(byPath); !slices.Equal(got, want) {
		t.Fatalf("expansion produced paths %v, want %v", got, want)
	}
}

// TestTaggedEmbeddedFieldContributesItsTagName checks that tagged embedded field contributes its tag name.
func TestTaggedEmbeddedFieldContributesItsTagName(t *testing.T) {
	byPath := expandOne(t, Schema{Mounts: mount(&taggedEmbedder{})}, "")
	want := []string{"base.addr", "name"}
	if got := pathsOf(byPath); !slices.Equal(got, want) {
		t.Fatalf("expansion produced paths %v, want %v", got, want)
	}
}

// TestEmbeddedPointerInlinesWhenNonNil checks that embedded pointer inlines when non nil.
func TestEmbeddedPointerInlinesWhenNonNil(t *testing.T) {
	byPath := expandOne(t, Schema{Mounts: mount(&pointerEmbedder{EmbeddedOptions: &EmbeddedOptions{Addr: ":9000"}})}, "")
	want := []string{"addr", "name"}
	if got := pathsOf(byPath); !slices.Equal(got, want) {
		t.Fatalf("expansion produced paths %v, want %v", got, want)
	}
	if got := byPath["addr"].def; got != ":9000" {
		t.Fatalf("the inlined item defaults to %v, want the value the prototype held", got)
	}
}

// TestEmbeddedNilPointerIsSkipped checks that embedded nil pointer is skipped.
func TestEmbeddedNilPointerIsSkipped(t *testing.T) {
	byPath := expandOne(t, Schema{Mounts: mount(&pointerEmbedder{})}, "")
	want := []string{"name"}
	if got := pathsOf(byPath); !slices.Equal(got, want) {
		t.Fatalf("expansion produced paths %v, want %v", got, want)
	}
}

// EmbeddedOptions is embedded by the cases above, and has to be exported for
// the embedded field itself to be.
type EmbeddedOptions struct {
	Addr string
}

type hiddenOptions struct {
	Secret string
}

type embedder struct {
	EmbeddedOptions
	hiddenOptions
	Name string
}

type taggedEmbedder struct {
	EmbeddedOptions `config:"base"`
	Name            string
}

type pointerEmbedder struct {
	*EmbeddedOptions
	Name string
}

// TestUndeclaredContainerTakesThePrimarySourceAlone checks that undeclared container takes the primary source alone.
func TestUndeclaredContainerTakesThePrimarySourceAlone(t *testing.T) {
	type carrier struct {
		Labels map[string]string
	}
	byPath := expandOne(t, Schema{Mounts: mount(&carrier{})}, "MYAPP")
	item := byPath["labels"]
	if item.origins != OriginPrimary {
		t.Fatalf("an undeclared map resolves to origins %v, want the primary source alone", item.origins)
	}
	if item.envName != "" {
		t.Fatalf("an undeclared map reads the environment variable %s, want none", item.envName)
	}
}

// TestContainerDeclaringEnvOriginIsAccepted checks that container declaring env origin is accepted.
func TestContainerDeclaringEnvOriginIsAccepted(t *testing.T) {
	type carrier struct {
		Labels map[string]string
	}
	byPath := expandOne(t, Schema{
		Mounts: mount(&carrier{}),
		Items:  map[string]Item{"labels": {Origins: OriginPrimary | OriginEnv}},
	}, "MYAPP")
	if got := byPath["labels"].envName; got != "MYAPP_LABELS" {
		t.Fatalf("the container reads the environment variable %q, want MYAPP_LABELS", got)
	}
}

// TestContainerDeclaringFlagOriginRejected checks that container declaring flag origin rejected.
func TestContainerDeclaringFlagOriginRejected(t *testing.T) {
	type endpoint struct {
		Addr string
	}
	type carrier struct {
		Endpoints []endpoint
	}
	err := expandFails(t, Schema{
		Mounts: mount(&carrier{}),
		Items:  map[string]Item{"endpoints": {Origins: OriginFlag, FlagName: "endpoints"}},
	}, "")
	if !strings.Contains(err.Error(), "primary config source") {
		t.Fatalf("the rejection reads %q, which does not say where such an item takes its value", err)
	}
}

// TestStringListMayDeclareEnvAndFlag checks that string list may declare env and flag.
func TestStringListMayDeclareEnvAndFlag(t *testing.T) {
	type carrier struct {
		Hosts []string
	}
	byPath := expandOne(t, Schema{
		Mounts: mount(&carrier{}),
		Items:  map[string]Item{"hosts": {Origins: OriginPrimary | OriginEnv | OriginFlag, FlagName: "hosts"}},
	}, "MYAPP")
	if got := byPath["hosts"].envName; got != "MYAPP_HOSTS" {
		t.Fatalf("the list reads the environment variable %q, want MYAPP_HOSTS", got)
	}
}

// TestMultiCharShortNameIsASchemaDefect checks that multi char short name is a schema defect.
func TestMultiCharShortNameIsASchemaDefect(t *testing.T) {
	type carrier struct {
		Addr string
	}
	err := expandFails(t, Schema{
		Mounts: mount(&carrier{}),
		Items:  map[string]Item{"addr": {Origins: OriginFlag, FlagName: "addr", FlagShort: "ab"}},
	}, "")
	if !strings.Contains(err.Error(), "single character") {
		t.Fatalf("the rejection reads %q, which does not say a short name is one character", err)
	}
}

// TestACommandLineNameNoArgumentCanReachIsASchemaDefect checks that a command line name no argument can reach is a schema defect.
func TestACommandLineNameNoArgumentCanReachIsASchemaDefect(t *testing.T) {
	type carrier struct {
		Addr string
	}
	for _, item := range []Item{
		{Origins: OriginFlag, FlagName: "addr", FlagShort: "é"},
		{Origins: OriginFlag, FlagName: "addr", FlagShort: "-"},
		{Origins: OriginFlag, FlagName: "--addr"},
		{Origins: OriginFlag, FlagName: "-addr"},
		{Origins: OriginFlag, FlagName: "addr=host"},
	} {
		err := expandFails(t, Schema{Mounts: mount(&carrier{}), Items: map[string]Item{"addr": item}}, "")
		if !strings.Contains(err.Error(), `"addr"`) {
			t.Errorf("the rejection of --%s -%s reads %q, which does not name the item",
				item.FlagName, item.FlagShort, err)
		}
	}
}

// TestLoadRefusesAShortNameTheLibraryWouldPanicOn checks that load refuses a short name the library would panic on.
func TestLoadRefusesAShortNameTheLibraryWouldPanicOn(t *testing.T) {
	type carrier struct {
		Addr string
	}
	reg := core.New()
	reg.Register(declaring("server", Schema{
		Namespace: "server",
		Mounts:    mount(&carrier{}),
		Items:     map[string]Item{"addr": {Origins: OriginFlag, FlagName: "addr", FlagShort: "é"}},
	}))
	var err error
	func() {
		defer func() {
			if p := recover(); p != nil {
				t.Fatalf("loading panicked with %v, want ErrInvalidSchema", p)
			}
		}()
		_, err = Load(t.Context(), reg, nil)
	}()
	if !errors.Is(err, ErrInvalidSchema) {
		t.Fatalf("loading returned %v, want ErrInvalidSchema", err)
	}
}

// TestNilScalarPointerIsALeaf checks that nil scalar pointer is a leaf.
func TestNilScalarPointerIsALeaf(t *testing.T) {
	type carrier struct {
		Retries *int
	}
	byPath := expandOne(t, Schema{Mounts: mount(&carrier{})}, "")
	item := byPath["retries"]
	if item == nil {
		t.Fatalf("expansion produced paths %v, want the single path retries", pathsOf(byPath))
	}
	if item.kind != kindScalar {
		t.Fatalf("retries is kind %d, want a scalar", item.kind)
	}
}
