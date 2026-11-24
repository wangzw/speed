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

func decodeTo[T any](t *testing.T, raw any) T {
	t.Helper()
	out := reflect.New(reflect.TypeFor[T]()).Elem()
	if err := decodeInto("some.path", raw, out); err != nil {
		t.Fatalf("converting %#v to %s failed: %v", raw, reflect.TypeFor[T](), err)
	}
	return out.Interface().(T)
}

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

// TestDurationFromString checks that duration from string.
func TestDurationFromString(t *testing.T) {
	if got := decodeTo[time.Duration](t, "5m"); got != 5*time.Minute {
		t.Fatalf("\"5m\" converted to %v, want 5m0s", got)
	}
	if got := decodeTo[time.Duration](t, "1h30m"); got != 90*time.Minute {
		t.Fatalf("\"1h30m\" converted to %v, want 1h30m0s", got)
	}
}

// TestDurationPassesThroughFromTheDefaultLayer checks that duration passes through from the default layer.
func TestDurationPassesThroughFromTheDefaultLayer(t *testing.T) {
	if got := decodeTo[time.Duration](t, 5*time.Minute); got != 5*time.Minute {
		t.Fatalf("a declared duration converted to %v, want 5m0s", got)
	}
}

// TestBareNumberDurationRejected checks that bare number duration rejected.
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

// TestIntFromJSONNumber checks that int from JSON number.
func TestIntFromJSONNumber(t *testing.T) {
	if got := decodeTo[int](t, float64(10)); got != 10 {
		t.Fatalf("the JSON number 10 converted to %d, want 10", got)
	}
}

// TestFractionalNumberIntoAnIntegerFieldRejected checks that fractional number into an integer field rejected.
func TestFractionalNumberIntoAnIntegerFieldRejected(t *testing.T) {
	err := decodeRejects[int](t, float64(10.5))
	if !strings.Contains(err.Error(), "10.5") {
		t.Fatalf("the rejection reads %q, which does not name the value that was given", err)
	}
}

// TestTextUnmarshalerSupported checks that text unmarshaler supported.
func TestTextUnmarshalerSupported(t *testing.T) {
	want := time.Date(2026, 9, 13, 10, 30, 0, 0, time.UTC)
	if got := decodeTo[time.Time](t, "2026-09-13T10:30:00Z"); !got.Equal(want) {
		t.Fatalf("the timestamp converted to %v, want %v", got, want)
	}
	if got := decodeTo[net.IP](t, "10.0.0.1"); !got.Equal(net.ParseIP("10.0.0.1")) {
		t.Fatalf("the address converted to %v, want 10.0.0.1", got)
	}
	decodeRejects[time.Time](t, float64(17))
	err := decodeRejects[time.Time](t, "not a timestamp")
	if !strings.Contains(err.Error(), "not a timestamp") {
		t.Fatalf("the rejection reads %q, which does not name the value that was given", err)
	}
}

// TestScalarConversions checks that scalar conversions.
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
	decodeRejects[string](t, float64(1))
	decodeRejects[bool](t, "yes please")
	decodeRejects[uint8](t, -1)
}

// TestIntegerOverflowRejected checks that integer overflow rejected.
func TestIntegerOverflowRejected(t *testing.T) {
	err := decodeRejects[int8](t, float64(300))
	if !strings.Contains(err.Error(), "int8") {
		t.Fatalf("the rejection reads %q, which does not name the target type", err)
	}
	decodeRejects[int8](t, 300)
	decodeRejects[uint32](t, float64(1e20))
}

// TestStringListCommaSeparatedReplaces checks that string list comma separated replaces.
func TestStringListCommaSeparatedReplaces(t *testing.T) {
	want := []string{"a", "b", "c"}
	if got := decodeTo[[]string](t, "a,b,c"); !reflect.DeepEqual(got, want) {
		t.Fatalf("the list converted to %#v, want %#v", got, want)
	}
	if got := decodeTo[[]string](t, "only"); !reflect.DeepEqual(got, []string{"only"}) {
		t.Fatalf("a second value produced %#v, want one element", got)
	}
	if got := decodeTo[[]string](t, ""); len(got) != 0 {
		t.Fatalf("an empty value produced %#v, want an empty list", got)
	}
}

// TestCommaInsideElementIsNotEscapable checks that comma inside element is not escapable.
func TestCommaInsideElementIsNotEscapable(t *testing.T) {
	for _, written := range []string{`a\,b`, `"a,b"`} {
		got := decodeTo[[]string](t, written)
		if len(got) != 2 {
			t.Fatalf("%q produced %#v, and the separator was expected to split it in two "+
				"whatever quoting was attempted", written, got)
		}
	}
}

// TestEmbeddedFieldInsideContainerElement checks that embedded field inside container element.
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

// TestContainersFromPrimaryConverted checks that containers from primary converted.
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
		map[string]any{"addr": "a:1", "pool": float64(3)},
	})
	want := []endpoint{{Addr: "a:1", PoolSize: 3}}
	if !reflect.DeepEqual(list, want) {
		t.Fatalf("the list of structs converted to %#v, want %#v", list, want)
	}
}

// TestContainerKeyFromAJSONTag checks that container key from ajson tag.
func TestContainerKeyFromAJSONTag(t *testing.T) {
	type endpoint struct {
		PoolSize int `json:"pool_size"`
	}
	list := decodeTo[[]endpoint](t, []any{map[string]any{"pool_size": float64(4)}})
	if want := []endpoint{{PoolSize: 4}}; !reflect.DeepEqual(list, want) {
		t.Fatalf("the list of structs converted to %#v, want %#v", list, want)
	}
}

// TestConvertedContainersDoNotAliasTheSource checks that converted containers do not alias the source.
func TestConvertedContainersDoNotAliasTheSource(t *testing.T) {
	source := map[string]string{"a": "1"}
	got := decodeTo[map[string]string](t, source)
	got["a"] = "changed"
	if source["a"] != "1" {
		t.Fatal("writing into the converted map reached the value it was converted from")
	}
}

// TestTypeMismatchNamesPathAndType checks that type mismatch names path and type.
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

// TestPointerFieldsAreAllocated checks that pointer fields are allocated.
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

// TestFloatOverflowRejected checks that float overflow rejected.
func TestFloatOverflowRejected(t *testing.T) {
	err := decodeRejects[float32](t, 1e300)
	if !strings.Contains(err.Error(), "float32") {
		t.Fatalf("the rejection reads %q, which does not name the target type", err)
	}
	decodeRejects[float32](t, "1e300")
	if got := decodeTo[float32](t, 1.5); got != 1.5 {
		t.Fatalf("1.5 converted to %v, want 1.5", got)
	}
}

// TestTheLargestFloat32GoesInAsItIsWritten checks that the largest float 32 goes in as it is written.
func TestTheLargestFloat32GoesInAsItIsWritten(t *testing.T) {
	for _, c := range []struct {
		raw  any
		want float32
	}{
		{3.4028235e38, math.MaxFloat32},
		{"3.4028235e+38", math.MaxFloat32},
		{-3.4028235e38, -math.MaxFloat32},
		{"-3.4028235e+38", -math.MaxFloat32},
		{float64(math.MaxFloat32), math.MaxFloat32},
		{math.Inf(1), float32(math.Inf(1))},
		{math.Inf(-1), float32(math.Inf(-1))},
	} {
		if got := decodeTo[float32](t, c.raw); got != c.want {
			t.Errorf("%v converted to %v, want %v", c.raw, got, c.want)
		}
	}

	halfway := 0x1p128 - 0x1p103
	for _, raw := range []any{
		3.40282356e38, "3.40282356e+38",
		math.Nextafter(halfway, 0), halfway,
		3.4028236e38, "3.4028236e+38",
		-1e39, "-1e39",
	} {
		decodeRejects[float32](t, raw)
	}
}

// TestAFloat32DefaultAtItsLargestTypesBack checks that a float 32 default at its largest types back.
func TestAFloat32DefaultAtItsLargestTypesBack(t *testing.T) {
	defaults := struct {
		Ceiling float32
		Floor   float32
	}{Ceiling: math.MaxFloat32, Floor: -math.MaxFloat32}
	for name, want := range map[string]string{"ceiling": "3.4028235e+38", "floor": "-3.4028235e+38"} {
		t.Run(name, func(t *testing.T) {
			l := loadOver(t, declaring("edge", Schema{
				Namespace: "edge",
				Mounts:    []Mount{{Value: &defaults}},
				Items: map[string]Item{
					"ceiling": {Origins: OriginFlag, FlagName: "ceiling"},
					"floor":   {Origins: OriginFlag, FlagName: "floor"},
				},
			}))
			item := corpusItem(t, l, name)
			typed := shellWord(t, formatDefault(item))
			if typed != want {
				t.Fatalf("--%s renders its default as %q, want float32's own %q", name, typed, want)
			}
			if err := giveFlags(t, l, []string{"edge"}, "--"+name+"="+typed); err != nil {
				t.Fatalf("--%s does not take %q back: %v", name, typed, err)
			}
			out := reflect.New(item.typ).Elem()
			if err := decodeInto(item.path, l.v.Get(item.path), out); err != nil {
				t.Fatalf("--%s does not take %q back: %v", name, typed, err)
			}
			if got := out.Interface(); got != item.def {
				t.Fatalf("--%s=%s reads back as %#v while the default is %#v", name, typed, got, item.def)
			}
		})
	}
}

// TestANumberIntoAFloatFieldIsNeverRounded checks that a number into a float field is never rounded.
func TestANumberIntoAFloatFieldIsNeverRounded(t *testing.T) {
	for _, raw := range []any{int64(16_777_217), float64(16_777_217), "16777217", 1e-50, "1e-50"} {
		decodeRejects[float32](t, raw)
	}
	for _, raw := range []any{int64(1<<53 + 1), uint64(1<<53 + 1), "9007199254740993", "-9007199254740993"} {
		decodeRejects[float64](t, raw)
	}

	if got := decodeTo[float32](t, int64(16_777_216)); got != 16_777_216 {
		t.Errorf("2^24 converted to %v, want it exactly", got)
	}
	if got := decodeTo[float32](t, 0.1); got != float32(0.1) {
		t.Errorf("0.1 converted to %v, want the float32 nearest to it", got)
	}
	if got := decodeTo[float32](t, "0.1"); got != float32(0.1) {
		t.Errorf("the text 0.1 converted to %v, want the float32 nearest to it", got)
	}
	if got := decodeTo[float32](t, float32(0.1)); got != float32(0.1) {
		t.Errorf("a float32 converted to %v, want itself", got)
	}
	if got := decodeTo[float64](t, int64(1<<53)); got != 1<<53 {
		t.Errorf("2^53 converted to %v, want it exactly", got)
	}
	if got := decodeTo[float64](t, "0.1"); got != 0.1 {
		t.Errorf("the text 0.1 converted to %v, want 0.1", got)
	}
}

// TestAWholeFloatIsHeldToTheIntegerRangeExactly checks that a whole float is held to the integer range exactly.
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

// TestUnsignedAboveTheSignedRangeAccepted checks that unsigned above the signed range accepted.
func TestUnsignedAboveTheSignedRangeAccepted(t *testing.T) {
	if got := decodeTo[uint64](t, uint64(math.MaxUint64)); got != math.MaxUint64 {
		t.Fatalf("the largest uint64 converted to %d, want %d", got, uint64(math.MaxUint64))
	}
	if got := decodeTo[uint64](t, "18446744073709551615"); got != math.MaxUint64 {
		t.Fatalf("the largest uint64 written as text converted to %d, want %d",
			got, uint64(math.MaxUint64))
	}
	decodeRejects[uint32](t, uint64(math.MaxUint64))
	decodeRejects[int64](t, uint64(math.MaxUint64))
}

// TestTwoKeysForOneContainerFieldRejected checks that two keys for one container field rejected.
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
	got := decodeTo[[]element](t, []any{map[string]any{"maxConns": float64(1)}})
	if want := []element{{MaxConns: 1}}; !reflect.DeepEqual(got, want) {
		t.Fatalf("the list of structs converted to %#v, want %#v", got, want)
	}
}
