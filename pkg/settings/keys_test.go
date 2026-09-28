package settings

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/vislake/speed/pkg/core"
)

// derivation exercises every branch of the key rule: the config tag, the json
// tag, a derived name, a skipped field, an acronym and a two-word name.
type derivation struct {
	FromConfig string `config:"from-config-tag"`
	FromJSON   string `json:"from-json-tag"`
	Neither    string
	HTTPPort   string
	PoolSize   int
	Skipped    string `config:"-"`
}

// textList is a declared type that carries its own text form and is a slice
// underneath, which is the shape net.IP has. The closed type set admits it,
// since it decodes itself from text.
type textList []string

func (t textList) MarshalText() ([]byte, error) { return []byte(strings.Join(t, ",")), nil }

func (t *textList) UnmarshalText(text []byte) error {
	if len(text) == 0 {
		*t = nil
		return nil
	}
	*t = strings.Split(string(text), ",")
	return nil
}

// A default that is a slice underneath is copied out of the prototype rather
// than shared with it.
//
// The prototype is the declaration, and two registries may decode against one.
// A caller that modified what it read out of the key table would otherwise be
// editing the declaration itself, and the next read of that key would see the
// edit.
func TestADefaultIsCopiedOutOfThePrototype(t *testing.T) {
	prototype := struct {
		Addrs textList
	}{Addrs: textList{"a", "b"}}
	entries := keysOf(t, Schema{Namespace: "ns", Mounts: []Mount{{Value: &prototype}}})

	got, ok := entries["ns.addrs"].def.(textList)
	if !ok {
		t.Fatalf("the declared default is %T, want textList", entries["ns.addrs"].def)
	}
	got[0] = "changed"
	if prototype.Addrs[0] != "a" {
		t.Errorf("the prototype's field became %q, so the default shares its storage with the "+
			"declaration", prototype.Addrs[0])
	}
}

// Base is embedded to pin the inlining of an anonymous field.
type Base struct {
	Inherited string
}

type withBase struct {
	Base
	Own string
}

// keysOf collects the keys one declaration contributes, as a set.
func keysOf(t *testing.T, s Schema) map[string]keyEntry {
	t.Helper()
	table := tableOf(t, declaring("test", s))
	return table.entries
}

// The path rule is configuration's, so a parameter moving between the two
// modules keeps its key.
func TestKeysFollowTheDerivationRule(t *testing.T) {
	entries := keysOf(t, Schema{Namespace: "ns", Mounts: []Mount{{Value: &derivation{}}}})
	cases := []struct {
		key  string
		want bool
	}{
		{"ns.from-config-tag", true},
		{"ns.from-json-tag", true},
		{"ns.neither", true},
		{"ns.http-port", true},
		{"ns.pool-size", true},
		{"ns.skipped", false},
		{"ns.from-config-tag-tag", false},
	}
	for _, c := range cases {
		if _, found := entries[c.key]; found != c.want {
			t.Errorf("key %q present = %v, want %v", c.key, found, c.want)
		}
	}
}

// An anonymous struct field is inlined: its fields join the enclosing path
// without a segment of their own.
func TestAnEmbeddedStructIsInlined(t *testing.T) {
	entries := keysOf(t, Schema{Namespace: "ns", Mounts: []Mount{{Value: &withBase{}}}})
	if _, found := entries["ns.inherited"]; !found {
		t.Errorf("the embedded field did not join the enclosing path; keys are %v", sortedKeys(entries))
	}
	if _, found := entries["ns.base.inherited"]; found {
		t.Error("the embedded type contributed a segment of its own")
	}
	if _, found := entries["ns.own"]; !found {
		t.Error("the declaring struct's own field is missing")
	}
}

// The namespace and the mount path are part of the key, and both may be more
// than one segment.
func TestNamespaceAndMountPathComposeTheKey(t *testing.T) {
	entries := keysOf(t, Schema{
		Namespace: "a.b",
		Mounts:    []Mount{{Path: "c", Value: &derivation{}}},
	})
	if _, found := entries["a.b.c.pool-size"]; !found {
		t.Errorf("the composed key is missing; keys are %v", sortedKeys(entries))
	}
}

// The declared default is the current value of the prototype's field, which is
// what a read falls back to when no row holds the key.
func TestTheDefaultComesFromThePrototype(t *testing.T) {
	type withDefault struct {
		Window time.Duration
		Limit  int
	}
	entries := keysOf(t, Schema{
		Namespace: "ns",
		Mounts:    []Mount{{Value: &withDefault{Window: 90 * time.Second, Limit: 7}}},
	})
	if got := entries["ns.window"].def; got != 90*time.Second {
		t.Errorf("the duration default is %v, want 1m30s", got)
	}
	if got := entries["ns.limit"].def; got != 7 {
		t.Errorf("the integer default is %v, want 7", got)
	}
}

// The type set is closed: a mapping or a list is refused at collection, not
// silently accepted and then found unencodable at the first write.
func TestAContainerTypeIsRefused(t *testing.T) {
	cases := []struct {
		name string
		val  any
	}{
		{"a list of strings", &struct{ Items []string }{}},
		{"a map", &struct{ Lookup map[string]string }{}},
		{"a list of structs", &struct{ Rows []Base }{}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			reg := registryWith(t, declaring("test", Schema{Namespace: "ns", Mounts: []Mount{{Value: c.val}}}))
			_, err := buildKeyTable(reg)
			if !errors.Is(err, ErrInvalidDeclaration) {
				t.Fatalf("building the key table returned %v, want ErrInvalidDeclaration", err)
			}
		})
	}
}

// The type set names no pointer, and a pointer would buy nothing: whether a
// key was ever written is already answered by the layers, so a third state
// ("set to nothing") has no reader.
func TestAPointerLeafIsRefused(t *testing.T) {
	reg := registryWith(t, declaring("test", Schema{
		Namespace: "ns",
		Mounts:    []Mount{{Value: &struct{ Limit *int }{}}},
	}))
	_, err := buildKeyTable(reg)
	if !errors.Is(err, ErrInvalidDeclaration) {
		t.Fatalf("building the key table returned %v, want ErrInvalidDeclaration", err)
	}
}

// A pointer to a type that carries its own text form is refused like any other
// pointer.
//
// It would otherwise slip in: the method set of *T includes T's
// pointer-receiver methods, so *time.Time satisfies TextUnmarshaler, and a
// check that asked about that first would admit it. It cannot round-trip —
// reflect.New of a pointer type gives a pointer to a pointer, which decodes
// nothing — so such a declaration would be accepted at startup and then fail
// every read of the key it names, once anything was written to it.
func TestAPointerToATextTypeIsRefused(t *testing.T) {
	reg := registryWith(t, declaring("test", Schema{
		Namespace: "ns",
		Mounts:    []Mount{{Value: &struct{ StartsAt *time.Time }{}}},
	}))
	_, err := buildKeyTable(reg)
	if !errors.Is(err, ErrInvalidDeclaration) {
		t.Fatalf("building the key table returned %v, want ErrInvalidDeclaration", err)
	}
}

// A type carrying its own text form is a leaf, however deeply its fields nest.
func TestATextUnmarshalerIsALeaf(t *testing.T) {
	type withTime struct {
		StartsAt time.Time
	}
	entries := keysOf(t, Schema{Namespace: "ns", Mounts: []Mount{{Value: &withTime{}}}})
	entry, found := entries["ns.starts-at"]
	if !found {
		t.Fatalf("the time field is missing; keys are %v", sortedKeys(entries))
	}
	if entry.kind != kindText {
		t.Errorf("the time field is kind %d, want the text kind", entry.kind)
	}
}

// Two modules claiming one key is a startup failure, and the message names
// both of them: which one to change is the reader's decision, and it cannot be
// made without knowing who else is involved.
func TestTwoModulesClaimingOneKeyConflict(t *testing.T) {
	reg := registryWith(t,
		declaring("first", Schema{Namespace: "ns", Mounts: []Mount{{Value: &derivation{}}}}),
		declaring("second", Schema{Namespace: "ns", Mounts: []Mount{{Value: &derivation{}}}}),
	)
	_, err := buildKeyTable(reg)
	if !errors.Is(err, ErrDeclarationConflict) {
		t.Fatalf("building the key table returned %v, want ErrDeclarationConflict", err)
	}
	for _, name := range []string{"first", "second"} {
		if !strings.Contains(err.Error(), name) {
			t.Errorf("the conflict does not name %q: %v", name, err)
		}
	}
}

// One declaration reaching one key twice is defective in itself — no second
// party is involved — and it is refused as such rather than as a conflict.
func TestOneDeclarationReachingAKeyTwiceIsInvalid(t *testing.T) {
	reg := registryWith(t, declaring("test", Schema{
		Namespace: "ns",
		Mounts: []Mount{
			{Value: &derivation{}},
			{Value: &derivation{}},
		},
	}))
	_, err := buildKeyTable(reg)
	if !errors.Is(err, ErrInvalidDeclaration) {
		t.Fatalf("building the key table returned %v, want ErrInvalidDeclaration", err)
	}
}

// Two schemas of one module reaching one key are that module's defect too.
// A module may put more than one Schema in its Resources, and nothing about
// the key changes when the two mounts come in two schemas rather than one: no
// second party is involved, so reporting it as a conflict between the module
// and itself would send the reader looking for a second declarer that does not
// exist.
func TestTwoSchemasOfOneModuleReachingOneKeyAreInvalid(t *testing.T) {
	reg := registryWith(t, core.Module{Name: "crm", Resources: []any{
		Schema{Namespace: "ns", Mounts: []Mount{{Value: &derivation{}}}},
		Schema{Namespace: "ns", Mounts: []Mount{{Value: &derivation{}}}},
	}})
	_, err := buildKeyTable(reg)
	if !errors.Is(err, ErrInvalidDeclaration) {
		t.Fatalf("building the key table returned %v, want ErrInvalidDeclaration", err)
	}
	if errors.Is(err, ErrDeclarationConflict) {
		t.Errorf("a single module's defect is reported as a conflict: %v", err)
	}
	if !strings.Contains(err.Error(), `"crm"`) {
		t.Errorf("the failure does not name the module: %v", err)
	}
}

// An Items key that reaches no field is a misspelling, and dropping it
// silently would lose a Description or a Sensitive without a word.
func TestAnItemKeyMatchingNoLeafIsInvalid(t *testing.T) {
	reg := registryWith(t, declaring("test", Schema{
		Namespace: "ns",
		Mounts:    []Mount{{Value: &derivation{}}},
		Items:     map[string]Item{"poolsize": {Description: "a misspelling"}},
	}))
	_, err := buildKeyTable(reg)
	if !errors.Is(err, ErrInvalidDeclaration) {
		t.Fatalf("building the key table returned %v, want ErrInvalidDeclaration", err)
	}
}

// A key segment outside the character set is refused, since the same rules
// govern the keys configuration derives.
func TestABadKeySegmentIsInvalid(t *testing.T) {
	type bad struct {
		Name string `config:"Not_Legal"`
	}
	reg := registryWith(t, declaring("test", Schema{Namespace: "ns", Mounts: []Mount{{Value: &bad{}}}}))
	_, err := buildKeyTable(reg)
	if !errors.Is(err, ErrInvalidDeclaration) {
		t.Fatalf("building the key table returned %v, want ErrInvalidDeclaration", err)
	}
}

// A declaration whose expansion does not terminate is refused rather than
// hanging the build.
//
// The link has to be non-nil for the walk to reach it at all: a struct pointer
// field that is nil contributes nothing to expand, so it is skipped, and a
// cycle that is not reachable is not a cycle the declaration has.
func TestATypeCycleIsInvalid(t *testing.T) {
	c := &cyclic{}
	c.Next = c
	reg := registryWith(t, declaring("test", Schema{Namespace: "ns", Mounts: []Mount{{Value: c}}}))
	_, err := buildKeyTable(reg)
	if !errors.Is(err, ErrInvalidDeclaration) {
		t.Fatalf("building the key table returned %v, want ErrInvalidDeclaration", err)
	}
}

// cyclic reaches itself, so walking it would never finish.
type cyclic struct {
	Next *cyclic
}

// A mount that is not a struct or a pointer to one has no fields to read, so
// there is nothing the declaration could mean.
func TestAMountThatIsNotAStructIsInvalid(t *testing.T) {
	reg := registryWith(t, declaring("test", Schema{Namespace: "ns", Mounts: []Mount{{Value: 7}}}))
	_, err := buildKeyTable(reg)
	if !errors.Is(err, ErrInvalidDeclaration) {
		t.Fatalf("building the key table returned %v, want ErrInvalidDeclaration", err)
	}
}

// The item metadata a declaration gives is carried into the table, since the
// sensitive marker is what decides whether a change event may carry a value.
func TestItemMetadataReachesTheTable(t *testing.T) {
	entries := keysOf(t, Schema{
		Namespace: "ns",
		Mounts:    []Mount{{Value: &derivation{}}},
		Items: map[string]Item{
			"pool-size": {Description: "how many", Sensitive: true},
		},
	})
	entry := entries["ns.pool-size"]
	if !entry.item.Sensitive || entry.item.Description != "how many" {
		t.Errorf("the item metadata is %+v, want the declared one", entry.item)
	}
}
