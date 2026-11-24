package settings

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/vislake/speed/pkg/core"
)

type derivation struct {
	FromConfig string `config:"from-config-tag"`
	FromJSON   string `json:"from-json-tag"`
	Neither    string
	HTTPPort   string
	PoolSize   int
	Skipped    string `config:"-"`
}

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

// TestADefaultIsCopiedOutOfThePrototype checks that a reference-carrying default is copied out of the prototype, not shared with it.
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
	// Inherited is a field of the embedded struct.
	Inherited string
}

type withBase struct {
	Base
	Own string
}

func keysOf(t *testing.T, s Schema) map[string]keyEntry {
	t.Helper()
	table := tableOf(t, declaring("test", s))
	return table.entries
}

// TestKeysFollowTheDerivationRule checks that the key rule is configuration's.
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

// TestAnEmbeddedStructIsInlined checks that an anonymous struct field is inlined.
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

// TestNamespaceAndMountPathComposeTheKey checks that namespace and mount path both form part of the key.
func TestNamespaceAndMountPathComposeTheKey(t *testing.T) {
	entries := keysOf(t, Schema{
		Namespace: "a.b",
		Mounts:    []Mount{{Path: "c", Value: &derivation{}}},
	})
	if _, found := entries["a.b.c.pool-size"]; !found {
		t.Errorf("the composed key is missing; keys are %v", sortedKeys(entries))
	}
}

// TestTheDefaultComesFromThePrototype checks that the default is the prototype field's current value.
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

// TestAContainerTypeIsRefused checks that a map or slice is refused at collection.
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

// TestAPointerLeafIsRefused checks that a pointer leaf is refused.
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

// TestAPointerToATextTypeIsRefused checks that a pointer to a text-carrying type is refused like any other pointer.
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

// TestATextUnmarshalerIsALeaf checks that a text-carrying type is a leaf.
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

// TestTwoModulesClaimingOneKeyConflict checks the conflict and that the message names both modules.
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

// TestOneDeclarationReachingAKeyTwiceIsInvalid checks that it is a declaration defect, not a conflict.
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

// TestTwoSchemasOfOneModuleReachingOneKeyAreInvalid checks that two schemas of one module reaching one key are that module defect.
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

// TestAnItemKeyMatchingNoLeafIsInvalid checks that an Items key reaching no field is refused.
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

// TestABadKeySegmentIsInvalid checks that a segment outside the character set is refused.
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

// TestATypeCycleIsInvalid checks that a self-referencing declaration is refused rather than hanging the build.
func TestATypeCycleIsInvalid(t *testing.T) {
	c := &cyclic{}
	c.Next = c
	reg := registryWith(t, declaring("test", Schema{Namespace: "ns", Mounts: []Mount{{Value: c}}}))
	_, err := buildKeyTable(reg)
	if !errors.Is(err, ErrInvalidDeclaration) {
		t.Fatalf("building the key table returned %v, want ErrInvalidDeclaration", err)
	}
}

type cyclic struct {
	Next *cyclic
}

// TestAMountThatIsNotAStructIsInvalid checks that a mount with no fields is refused.
func TestAMountThatIsNotAStructIsInvalid(t *testing.T) {
	reg := registryWith(t, declaring("test", Schema{Namespace: "ns", Mounts: []Mount{{Value: 7}}}))
	_, err := buildKeyTable(reg)
	if !errors.Is(err, ErrInvalidDeclaration) {
		t.Fatalf("building the key table returned %v, want ErrInvalidDeclaration", err)
	}
}

// TestItemMetadataReachesTheTable checks that item metadata reaches the table.
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
