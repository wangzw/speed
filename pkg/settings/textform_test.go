package settings

import (
	"context"
	"errors"
	"math/big"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"
)

// decodeOnly decodes itself from text and has no way to write itself back.
type decodeOnly struct{ text string }

func (d *decodeOnly) UnmarshalText(text []byte) error {
	d.text = string(text)
	return nil
}

// hashed writes itself as a hash sign followed by its digits and reads back
// the digits alone, so the text it writes is not one it reads.
type hashed int

func (h hashed) MarshalText() ([]byte, error) { return []byte("#" + strconv.Itoa(int(h))), nil }

func (h *hashed) UnmarshalText(text []byte) error {
	n, err := strconv.Atoi(string(text))
	if err != nil {
		return err
	}
	*h = hashed(n)
	return nil
}

// A type that decodes itself from text and cannot write itself back is refused
// when the key table is built.
//
// A write stores a value as the text the declared type writes, so no write of
// such a key could store anything. Admitted, the declaration would start the
// process and leave an item every Set refuses, found at the first write rather
// than at startup.
func TestATypeThatCannotWriteItselfBackIsRefused(t *testing.T) {
	prototype := struct{ Token decodeOnly }{}
	_, err := buildKeyTable(registryWith(t, declaring("test.decl", Schema{
		Namespace: "ns",
		Mounts:    []Mount{{Value: &prototype}},
	})))
	if !errors.Is(err, ErrInvalidDeclaration) {
		t.Fatalf("building the key table returned %v, want ErrInvalidDeclaration", err)
	}
	for _, want := range []string{`"token"`, "MarshalText"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal does not mention %s: %v", want, err)
		}
	}
}

// A default that does not survive the text round trip is refused when the key
// table is built, and a sensitive item's refusal carries neither its text nor
// the type's own reason.
//
// The type set is closed so that every declared value round-trips through
// text. A default that does not is one no write could restore once a row has
// replaced it, and one no listing of the declared items could show.
func TestADefaultThatDoesNotRoundTripIsRefused(t *testing.T) {
	cases := []struct {
		name      string
		prototype any
		items     map[string]Item
		mentions  []string
		withholds []string
	}{
		{
			name:      "a default the type cannot write",
			prototype: &struct{ Starts time.Time }{Starts: time.Date(10000, 1, 1, 0, 0, 0, 0, time.UTC)},
			mentions:  []string{`"starts"`, "year outside of range"},
		},
		{
			name:      "a default whose text the type does not read",
			prototype: &struct{ Code hashed }{Code: 42},
			mentions:  []string{`"code"`, "#42"},
		},
		{
			name:      "a sensitive default whose text the type does not read",
			prototype: &struct{ Code hashed }{Code: 42},
			items:     map[string]Item{"code": {Sensitive: true}},
			mentions:  []string{`"code"`, "withheld"},
			withholds: []string{"42"},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := buildKeyTable(registryWith(t, declaring("test.decl", Schema{
				Namespace: "ns",
				Mounts:    []Mount{{Value: c.prototype}},
				Items:     c.items,
			})))
			if !errors.Is(err, ErrInvalidDeclaration) {
				t.Fatalf("building the key table returned %v, want ErrInvalidDeclaration", err)
			}
			if errors.Is(err, ErrTypeMismatch) {
				t.Errorf("the refusal is also ErrTypeMismatch, which names a value written or "+
					"stored rather than a declaration: %v", err)
			}
			for _, want := range c.mentions {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("the refusal does not mention %s: %v", want, err)
				}
			}
			for _, secret := range c.withholds {
				if strings.Contains(err.Error(), secret) {
					t.Errorf("the refusal carries the sensitive default %q: %v", secret, err)
				}
			}
		})
	}
}

// A type that declares its text methods on its pointer — math/big.Int is one —
// is written and read back like any other that carries its own text form.
//
// The value's own method set has no MarshalText, so an encoder that asked the
// value alone would refuse every write of the key while the type encodes
// itself perfectly well.
func TestATypeWritingItselfThroughItsPointerRoundTrips(t *testing.T) {
	var prototype struct{ Amount big.Int }
	prototype.Amount.SetInt64(1 << 40)
	keys := tableOf(t, declaring("test.big", Schema{Namespace: "big", Mounts: []Mount{{Value: &prototype}}}))
	store, _ := storeOf(t, keys, newRecordingBus())
	ctx := context.Background()

	cases := []struct {
		name  string
		value any
		want  string
	}{
		{"as text", "123456789012345678901234567890", "123456789012345678901234567890"},
		{"as a value", *big.NewInt(7), "7"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if err := store.Set(ctx, Write{Layer: Global(), Key: "big.amount", Value: c.value}); err != nil {
				t.Fatalf("writing %v returned %v", c.value, err)
			}
			got, err := Get[big.Int](store, Global(), "big.amount")
			if err != nil || got.String() != c.want {
				t.Errorf("read back %s (%v), want %s", got.String(), err, c.want)
			}
		})
	}
}

// A read of a type that carries its own text form hands out a value of its
// own, even where copies of the type share storage in a way reflection cannot
// see.
//
// math/big.Int is such a type: a copy of one shares its digits, and its
// methods write into them in place. A caller that read one and computed with
// it — the ordinary way to use the type — would otherwise rewrite the declared
// default, or the snapshot's value, for every other reader in the process.
func TestAReadOfATextFormTypeSharesNothingWithTheModule(t *testing.T) {
	var prototype struct{ Amount big.Int }
	prototype.Amount.SetInt64(1 << 40)
	declared := prototype.Amount.String()
	keys := tableOf(t, declaring("test.big", Schema{Namespace: "big", Mounts: []Mount{{Value: &prototype}}}))
	store, _ := storeOf(t, keys, newRecordingBus())

	computeWithARead := func(t *testing.T, want string) {
		t.Helper()
		first, err := Get[big.Int](store, Global(), "big.amount")
		if err != nil {
			t.Fatalf("reading: %v", err)
		}
		first.SetInt64(3)
		second, err := Get[big.Int](store, Global(), "big.amount")
		if err != nil || second.String() != want {
			t.Errorf("after one reader computed with its value, the next read returned %s (%v), "+
				"want %s", second.String(), err, want)
		}
	}

	computeWithARead(t, declared)
	if prototype.Amount.String() != declared {
		t.Errorf("the declaration's prototype became %s, want %s", prototype.Amount.String(), declared)
	}

	if err := store.Set(context.Background(), Write{Layer: Global(), Key: "big.amount", Value: "12345"}); err != nil {
		t.Fatalf("writing: %v", err)
	}
	computeWithARead(t, "12345")
}

// A value assignable to the declared type without being of it — a []string for
// a textList item — is written in the declared type's own text form, the one a
// read decodes.
func TestAnAssignableValueIsWrittenInTheDeclaredTypesText(t *testing.T) {
	prototype := struct{ Addrs textList }{}
	keys := tableOf(t, declaring("test.list", Schema{Namespace: "list", Mounts: []Mount{{Value: &prototype}}}))
	store, _ := storeOf(t, keys, newRecordingBus())
	ctx := context.Background()

	if err := store.Set(ctx, Write{Layer: Global(), Key: "list.addrs", Value: []string{"x", "y"}}); err != nil {
		t.Fatalf("writing a []string into a textList item returned %v", err)
	}
	got, err := Get[textList](store, Global(), "list.addrs")
	if err != nil || !slices.Equal(got, textList{"x", "y"}) {
		t.Errorf("read back %v (%v), want [x y]", got, err)
	}
	entries, err := store.List(ctx, Global())
	if err != nil || len(entries) != 1 || entries[0].Value != "x,y" {
		t.Errorf("the row is %+v (%v), want the text x,y", entries, err)
	}
}
