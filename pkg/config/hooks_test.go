package config

import (
	"errors"
	"math"
	"net"
	"reflect"
	"strings"
	"testing"
	"time"
)

// decodeTo converts a value into T and fails the test when it cannot.
func decodeTo[T any](t *testing.T, raw any) T {
	t.Helper()
	out := reflect.New(reflect.TypeFor[T]()).Elem()
	if err := decodeInto("some.path", raw, out); err != nil {
		t.Fatalf("converting %#v to %s failed: %v", raw, reflect.TypeFor[T](), err)
	}
	return out.Interface().(T)
}

// decodeRejects converts a value that is expected to be refused.
func decodeRejects[T any](t *testing.T, raw any) error {
	t.Helper()
	out := reflect.New(reflect.TypeFor[T]()).Elem()
	err := decodeInto("some.path", raw, out)
	if err == nil {
		t.Fatalf("converting %#v to %s produced %#v, want a rejection",
			raw, reflect.TypeFor[T](), out.Interface())
	}
	if !errors.Is(err, ErrTypeMismatch) {
		t.Fatalf("conversion returned %v, want ErrTypeMismatch", err)
	}
	return err
}

func TestDurationFromString(t *testing.T) {
	if got := decodeTo[time.Duration](t, "5m"); got != 5*time.Minute {
		t.Fatalf("\"5m\" converted to %v, want 5m0s", got)
	}
	if got := decodeTo[time.Duration](t, "1h30m"); got != 90*time.Minute {
		t.Fatalf("\"1h30m\" converted to %v, want 1h30m0s", got)
	}
}

// TestDurationPassesThroughFromTheDefaultLayer pins the one numeric source a
// duration does take: the carrier struct's own field, which already is one.
// Refusing it would make every declared default unreadable.
func TestDurationPassesThroughFromTheDefaultLayer(t *testing.T) {
	if got := decodeTo[time.Duration](t, 5*time.Minute); got != 5*time.Minute {
		t.Fatalf("a declared duration converted to %v, want 5m0s", got)
	}
}

// TestBareNumberDurationRejected pins the one rule this mechanism has to give
// itself: a Duration is an int64 count of nanoseconds, so a bare 300 would
// read as five minutes and mean three hundred nanoseconds. The library's own
// duration hook covers the string leg alone and would take the number.
func TestBareNumberDurationRejected(t *testing.T) {
	for name, raw := range map[string]any{
		"a JSON number the primary source gives": float64(300),
		"an integer":                             300,
		"a digit string the command line gives":  "300",
	} {
		t.Run(name, func(t *testing.T) {
			decodeRejects[time.Duration](t, raw)
		})
	}
}

func TestIntFromJSONNumber(t *testing.T) {
	if got := decodeTo[int](t, float64(10)); got != 10 {
		t.Fatalf("the JSON number 10 converted to %d, want 10", got)
	}
}

// TestFractionalNumberIntoAnIntegerFieldRejected is the guard on the library's
// quietest behaviour: it writes a float into an integer field by truncating
// it, so 10.5 would silently become 10 and the config file would say one thing
// while the run did another.
func TestFractionalNumberIntoAnIntegerFieldRejected(t *testing.T) {
	err := decodeRejects[int](t, float64(10.5))
	if !strings.Contains(err.Error(), "10.5") {
		t.Fatalf("the rejection reads %q, which does not name the value that was given", err)
	}
}

func TestTextUnmarshalerSupported(t *testing.T) {
	want := time.Date(2026, 9, 13, 10, 30, 0, 0, time.UTC)
	if got := decodeTo[time.Time](t, "2026-09-13T10:30:00Z"); !got.Equal(want) {
		t.Fatalf("the timestamp converted to %v, want %v", got, want)
	}
	if got := decodeTo[net.IP](t, "10.0.0.1"); !got.Equal(net.ParseIP("10.0.0.1")) {
		t.Fatalf("the address converted to %v, want 10.0.0.1", got)
	}
	// A type that decodes itself from text takes a string and nothing else.
	decodeRejects[time.Time](t, float64(17))
	// Its own rejection reaches the caller rather than being swallowed.
	err := decodeRejects[time.Time](t, "not a timestamp")
	if !strings.Contains(err.Error(), "not a timestamp") {
		t.Fatalf("the rejection reads %q, which does not name the value that was given", err)
	}
}

func TestScalarConversions(t *testing.T) {
	if got := decodeTo[bool](t, "true"); !got {
		t.Fatal("the string \"true\" converted to false")
	}
	if got := decodeTo[bool](t, false); got {
		t.Fatal("the boolean false converted to true")
	}
	if got := decodeTo[int32](t, "-7"); got != -7 {
		t.Fatalf("the string \"-7\" converted to %d, want -7", got)
	}
	if got := decodeTo[uint16](t, float64(8080)); got != 8080 {
		t.Fatalf("the JSON number 8080 converted to %d, want 8080", got)
	}
	if got := decodeTo[float64](t, "2.5"); got != 2.5 {
		t.Fatalf("the string \"2.5\" converted to %v, want 2.5", got)
	}
	if got := decodeTo[float64](t, 3); got != 3 {
		t.Fatalf("the integer 3 converted to %v, want 3", got)
	}
	if got := decodeTo[string](t, "plain"); got != "plain" {
		t.Fatalf("the string converted to %q", got)
	}
	// A number is not silently rendered into a string field: 1 and "1" stay
	// apart, which is the same reason a numeric key is refused. It is also why
	// the library's weakly typed mode is not the way the text layers are read.
	decodeRejects[string](t, float64(1))
	decodeRejects[bool](t, "yes please")
	decodeRejects[uint8](t, -1)
}

func TestIntegerOverflowRejected(t *testing.T) {
	err := decodeRejects[int8](t, float64(300))
	if !strings.Contains(err.Error(), "int8") {
		t.Fatalf("the rejection reads %q, which does not name the target type", err)
	}
	decodeRejects[int8](t, 300)
	decodeRejects[uint32](t, float64(1e20))
}

func TestStringListCommaSeparatedReplaces(t *testing.T) {
	want := []string{"a", "b", "c"}
	if got := decodeTo[[]string](t, "a,b,c"); !reflect.DeepEqual(got, want) {
		t.Fatalf("the list converted to %#v, want %#v", got, want)
	}
	// The layer replaces rather than appends: a second value does not carry
	// the first one along.
	if got := decodeTo[[]string](t, "only"); !reflect.DeepEqual(got, []string{"only"}) {
		t.Fatalf("a second value produced %#v, want one element", got)
	}
	if got := decodeTo[[]string](t, ""); len(got) != 0 {
		t.Fatalf("an empty value produced %#v, want an empty list", got)
	}
}

// TestCommaInsideElementIsNotEscapable pins the accepted cost of the flat
// form: the separator carries no escape, so an element holding a comma has to
// come from the primary config source. A quoting rule of this project's own is
// exactly what the flat form is meant to avoid.
func TestCommaInsideElementIsNotEscapable(t *testing.T) {
	for _, written := range []string{`a\,b`, `"a,b"`} {
		got := decodeTo[[]string](t, written)
		if len(got) != 2 {
			t.Fatalf("%q produced %#v, and the separator was expected to split it in two "+
				"whatever quoting was attempted", written, got)
		}
	}
}

// TestEmbeddedFieldInsideContainerElement pins that the keys of a container
// element address an embedded field directly, the way the manifest expansion
// and Decode address it. A config file would otherwise read differently inside
// a container than outside one.
func TestEmbeddedFieldInsideContainerElement(t *testing.T) {
	type element struct {
		EmbeddedOptions
		Port int
	}
	list := decodeTo[[]element](t, []any{map[string]any{"addr": "a:1", "port": float64(1)}})
	want := []element{{EmbeddedOptions: EmbeddedOptions{Addr: "a:1"}, Port: 1}}
	if !reflect.DeepEqual(list, want) {
		t.Fatalf("the list of structs converted to %#v, want %#v", list, want)
	}
}

// TestContainersFromPrimaryConverted covers the shapes only the primary source
// can give: a map with a concrete value type, and a list of structs whose keys
// are derived by the same rule as a path segment.
func TestContainersFromPrimaryConverted(t *testing.T) {
	got := decodeTo[map[string]int](t, map[string]any{"a": float64(1), "b": float64(2)})
	if want := map[string]int{"a": 1, "b": 2}; !reflect.DeepEqual(got, want) {
		t.Fatalf("the map converted to %#v, want %#v", got, want)
	}

	type endpoint struct {
		Addr     string
		PoolSize int    `config:"pool"`
		Hidden   string `json:"-"`
	}
	list := decodeTo[[]endpoint](t, []any{
		map[string]any{"addr": "a:1", "pool": float64(3), "-": "ignored", "stray": true},
	})
	want := []endpoint{{Addr: "a:1", PoolSize: 3}}
	if !reflect.DeepEqual(list, want) {
		t.Fatalf("the list of structs converted to %#v, want %#v", list, want)
	}
}

// TestContainerKeyFromAJSONTag pins the leg of the field-name rule the
// decoding library has no setting for. Left to the library the json tag is
// invisible, and the key would fall through to the field's own name without a
// word - the same key reading one way outside a container and another inside
// one.
func TestContainerKeyFromAJSONTag(t *testing.T) {
	type endpoint struct {
		PoolSize int `json:"pool_size"`
	}
	list := decodeTo[[]endpoint](t, []any{map[string]any{"pool_size": float64(4)}})
	if want := []endpoint{{PoolSize: 4}}; !reflect.DeepEqual(list, want) {
		t.Fatalf("the list of structs converted to %#v, want %#v", list, want)
	}
}

// TestConvertedContainersDoNotAliasTheSource pins that a converted map is a
// fresh one: two holders of the same value must not be able to write into each
// other.
func TestConvertedContainersDoNotAliasTheSource(t *testing.T) {
	source := map[string]string{"a": "1"}
	got := decodeTo[map[string]string](t, source)
	got["a"] = "changed"
	if source["a"] != "1" {
		t.Fatal("writing into the converted map reached the value it was converted from")
	}
}

func TestTypeMismatchNamesPathAndType(t *testing.T) {
	out := reflect.New(reflect.TypeFor[time.Duration]()).Elem()
	err := decodeInto("cache.ttl", "abc", out)
	if !errors.Is(err, ErrTypeMismatch) {
		t.Fatalf("the conversion returned %v, want ErrTypeMismatch", err)
	}
	for _, want := range []string{"cache.ttl", "time.Duration"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("the rejection reads %q, which does not name %q", err, want)
		}
	}
}

// TestPointerFieldsAreAllocated pins that a source giving a value for a
// pointer field is asking for one to exist.
func TestPointerFieldsAreAllocated(t *testing.T) {
	got := decodeTo[*int](t, float64(5))
	if got == nil || *got != 5 {
		t.Fatalf("the value converted to %v, want a pointer to 5", got)
	}
	stamp := decodeTo[*time.Time](t, "2026-09-13T10:30:00Z")
	if stamp == nil || stamp.Year() != 2026 {
		t.Fatalf("the timestamp converted to %v, want a pointer to it", stamp)
	}
}

// TestFloatOverflowRejected pins the numeric leg of the range check. The
// library writes a float64 into a float32 field by conversion, so a value
// wider than the field arrives as an infinity and reads as a value rather
// than as a failure.
func TestFloatOverflowRejected(t *testing.T) {
	err := decodeRejects[float32](t, 1e300)
	if !strings.Contains(err.Error(), "float32") {
		t.Fatalf("the rejection reads %q, which does not name the target type", err)
	}
	// The same value written as text is refused as well: what a config file
	// may say and what the command line may say do not differ.
	decodeRejects[float32](t, "1e300")
	// A value the field holds still passes.
	if got := decodeTo[float32](t, 1.5); got != 1.5 {
		t.Fatalf("1.5 converted to %v, want 1.5", got)
	}
}

// TestAWholeFloatIsHeldToTheIntegerRangeExactly pins the edges of the 64-bit
// range for a whole number that arrives as a float, which is how a source
// writing one with an exponent delivers it.
//
// The range is the target's own on every platform. Converting a float an
// integer type cannot hold is implementation-defined in Go, and where it
// saturates to the bound, 2^63 would become the largest int64 and pass a
// check that converts first and compares after -- a value one past the range
// accepted as a different value. For an unsigned field the same conversion
// through int64 turns 2^63 into 2^63-1, and a value above it into a refusal
// of a number the field holds.
func TestAWholeFloatIsHeldToTheIntegerRangeExactly(t *testing.T) {
	decodeRejects[int64](t, float64(1<<63))
	decodeRejects[int64](t, math.Inf(1))
	decodeRejects[int64](t, math.NaN())
	if got := decodeTo[int64](t, float64(-1<<63)); got != math.MinInt64 {
		t.Errorf("-2^63 converted to %d, want %d", got, int64(math.MinInt64))
	}
	if got := decodeTo[uint64](t, float64(1<<63)); got != 1<<63 {
		t.Errorf("2^63 converted to %d for an unsigned field, want %d", got, uint64(1<<63))
	}
	if got := decodeTo[uint64](t, 1.8e19); got != 18_000_000_000_000_000_000 {
		t.Errorf("1.8e19 converted to %d for an unsigned field, want 18000000000000000000", got)
	}
	decodeRejects[uint64](t, float64(1<<64))
	decodeRejects[uint64](t, float64(-1))
}

// TestUnsignedAboveTheSignedRangeAccepted pins the upper end of an unsigned
// field. A source can reach it -- YAML reads 18446744073709551615 as a uint64
// -- and a field wide enough to hold it has to take it.
func TestUnsignedAboveTheSignedRangeAccepted(t *testing.T) {
	if got := decodeTo[uint64](t, uint64(math.MaxUint64)); got != math.MaxUint64 {
		t.Fatalf("the largest uint64 converted to %d, want %d", got, uint64(math.MaxUint64))
	}
	// The text form of the same value agrees with it.
	if got := decodeTo[uint64](t, "18446744073709551615"); got != math.MaxUint64 {
		t.Fatalf("the largest uint64 written as text converted to %d, want %d",
			got, uint64(math.MaxUint64))
	}
	// A narrower field still refuses it, and so does a signed one.
	decodeRejects[uint32](t, uint64(math.MaxUint64))
	decodeRejects[int64](t, uint64(math.MaxUint64))
}

// TestTwoKeysForOneContainerFieldRejected pins that renaming a key to the one
// the decoder looks the field up by does not overwrite a value already there.
// The keys inside a container take no part in the unknown-key check, so a
// dropped value would go unreported everywhere.
func TestTwoKeysForOneContainerFieldRejected(t *testing.T) {
	type element struct {
		MaxConns int `json:"maxConns"`
	}
	err := decodeRejects[[]element](t, []any{
		map[string]any{"maxConns": float64(1), "max-conns": float64(2)},
	})
	for _, want := range []string{"maxConns", "max-conns"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("the rejection reads %q, which does not name %q", err, want)
		}
	}
	// The key the field-name rule gives it is still taken on its own.
	got := decodeTo[[]element](t, []any{map[string]any{"maxConns": float64(1)}})
	if want := []element{{MaxConns: 1}}; !reflect.DeepEqual(got, want) {
		t.Fatalf("the list of structs converted to %#v, want %#v", got, want)
	}
}
