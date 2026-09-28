package config

import (
	"fmt"
	"maps"
	"math"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/go-viper/mapstructure/v2"
)

// decodeHook is the chain of conversions this module puts on top of the
// decoding library. The order is the order a value is decided in: a section is
// keyed to the target's fields before anything looks at the fields, a duration
// is settled before any other int64 is, a number is held to what its field can
// hold before the library truncates or overflows it, a type that reads itself
// from text takes the text before the scalar rules do, and the flat list form
// is the last thing a string can still mean.
func decodeHook() mapstructure.DecodeHookFunc {
	return mapstructure.ComposeDecodeHookFunc(
		containerKeyHook,
		durationHook,
		exactNumberHook,
		mapstructure.TextUnmarshallerHookFunc(),
		textScalarHook,
		mapstructure.StringToSliceHookFunc(listSeparator),
	)
}

// decodeInto writes one source value into one target field, naming the path
// and the target type on failure. The target is filled through a fresh value
// rather than in place, so a container arrives as a copy and two holders of
// the same configuration cannot write into each other.
func decodeInto(path string, raw any, target reflect.Value) error {
	out := reflect.New(target.Type())
	decoder, err := mapstructure.NewDecoder(&mapstructure.DecoderConfig{
		DecodeHook: decodeHook(),
		Result:     out.Interface(),
		// The three settings below are the field-name rule of this module,
		// expressed in the library's terms: the config tag names a field, an
		// untagged field is named by its own name in lower kebab case, and an
		// embedded struct joins the enclosing section without a name of its
		// own. The remaining leg, a field named by a json tag, has no setting
		// to express it and is carried by containerKeyHook.
		TagName:      "config",
		MapFieldName: camelToKebab,
		Squash:       true,
		// A container is replaced as a whole rather than merged into what the
		// target already holds, which is the rule its keys not being in the
		// manifest follows from.
		ZeroFields: true,
	})
	if err != nil {
		return fmt.Errorf("%w: %q takes %s, and the decoder for it could not be built: %w",
			ErrTypeMismatch, path, typeName(target.Type()), err)
	}
	if err := decoder.Decode(raw); err != nil {
		return fmt.Errorf("%w: %q takes %s, and %w", ErrTypeMismatch, path, typeName(target.Type()), err)
	}
	target.Set(out.Elem())
	return nil
}

// containerKeyHook addresses the fields of a struct inside a container by the
// same names the manifest expansion derives, so that a config file reads the
// same inside a container as it does outside one.
//
// It carries the one leg of that rule the decoder has no setting for: a field
// that names itself with a json tag. The key the source wrote is moved to the
// name the decoder looks the field up by, and a field tagged "-" has its key
// dropped, since such a field takes part in no source.
//
// Keys matching no field are left where they are: the keys inside a container
// do not take part in the unknown-key check.
func containerKeyHook(from, to reflect.Value) (any, error) {
	data := from.Interface()
	if to.Kind() != reflect.Struct || decodesFromText(to.Type()) {
		return data, nil
	}
	section, ok := data.(map[string]any)
	if !ok {
		return data, nil
	}
	out := make(map[string]any, len(section))
	maps.Copy(out, section)
	if err := renameSectionKeys(to.Type(), out, []reflect.Type{to.Type()}); err != nil {
		return nil, err
	}
	return out, nil
}

// renameSectionKeys rewrites one section in place for one struct type.
//
// A section that writes both of a field's two names is refused rather than
// renamed: moving the one to the other would drop a value the source gave, and
// since the keys inside a container take no part in the unknown-key check, no
// later step would report it.
//
// chain holds the struct types on the way in. The element type of a container
// is never expanded by collection and may hold a cycle, which would otherwise
// recurse without end on one and the same section.
func renameSectionKeys(t reflect.Type, section map[string]any, chain []reflect.Type) error {
	for i := range t.NumField() {
		f := t.Field(i)
		if f.PkgPath != "" {
			continue
		}
		name, tagged, skip := fieldName(f)
		if skip {
			delete(section, decoderKey(f))
			continue
		}
		if inner, inline := inlinedStruct(f, tagged); inline {
			if slices.Contains(chain, inner) {
				continue
			}
			if err := renameSectionKeys(inner, section, append(chain, inner)); err != nil {
				return err
			}
			continue
		}
		key := decoderKey(f)
		if key == name {
			continue
		}
		value, given := section[name]
		if !given {
			continue
		}
		if _, taken := section[key]; taken {
			return fmt.Errorf("%q and %q both reach the same field of this element, and taking "+
				"one would drop the other: give the value once, under %q", name, key, name)
		}
		delete(section, name)
		section[key] = value
	}
	return nil
}

// decoderKey is the name the decoder looks one field up by: the config tag
// when there is one, the field name in lower kebab case otherwise.
func decoderKey(f reflect.StructField) string {
	if tag, ok := f.Tag.Lookup("config"); ok {
		if value, _, _ := strings.Cut(tag, ","); value != "" {
			return value
		}
	}
	return camelToKebab(f.Name)
}

// inlinedStruct reports the struct an anonymous field contributes to the
// enclosing section without a name of its own, which is the embedding
// semantics of encoding/json and the one the decoder is configured for.
func inlinedStruct(f reflect.StructField, tagged bool) (reflect.Type, bool) {
	if !f.Anonymous || tagged || decodesFromText(f.Type) {
		return nil, false
	}
	t := f.Type
	if t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	if t.Kind() != reflect.Struct {
		return nil, false
	}
	return t, true
}

// durationHook keeps time.Duration on the string form alone. Its underlying
// type is an int64 count of nanoseconds, so accepting a bare 300 would make
// the value five minutes to a reader and three hundred nanoseconds to the
// machine. Duration does not read itself from text, so this rule has nowhere
// else to live: the library's own duration hook covers the string leg only and
// lets a number through to be taken as nanoseconds.
//
// A value that already is a duration passes: that is the default layer handing
// back what the carrier struct declared.
//
// The rule is time.Duration's own. A type defined over it, such as
// type Timeout time.Duration, keeps nothing of Duration that reflection can
// see: it is an int64 of its own, the same as any integer type, and its items
// follow the integer rules. A type that is to read as a duration implements
// encoding.TextUnmarshaler, which makes it a leaf that parses itself.
func durationHook(from, to reflect.Type, data any) (any, error) {
	if to != durationType || from == durationType {
		return data, nil
	}
	switch from.Kind() {
	case reflect.String:
		text := reflect.ValueOf(data).String()
		parsed, err := time.ParseDuration(text)
		if err != nil {
			return nil, fmt.Errorf("%q is not a duration: it is written as in \"5m\", "+
				"\"1h30m\" or \"200ms\"", text)
		}
		return parsed, nil
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64,
		reflect.Float32, reflect.Float64:
		return nil, fmt.Errorf("the source gave the number %v, and a duration is written as a "+
			"string time.ParseDuration accepts, such as \"5m\": a bare number counts "+
			"nanoseconds, so 300 would read as five minutes and mean three hundred nanoseconds",
			data)
	}
	return data, nil
}

// exactNumberHook holds every number written into a numeric field to one that
// survives the trip. JSON has no integer type, so a 10 written in a config
// file arrives as a float and belongs in an int field; 10.5 does not, and a
// value wider than the field does not either.
//
// Every one of those refusals has to be made here. The library truncates a
// float into an integer field without a word, it writes an integer in with no
// width check at all, so a value out of range wraps round instead of failing,
// it converts a float too wide for the field into an infinity, which reads
// back as a value rather than as a failure, and it rounds an integer or a
// float into a float field that cannot hold it, so 16777217 lands in a
// float32 as 16777216.
func exactNumberHook(from, to reflect.Type, data any) (any, error) {
	if to == durationType {
		return data, nil // settled by durationHook
	}
	v := reflect.ValueOf(data)
	switch to.Kind() {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		switch from.Kind() {
		case reflect.Float32, reflect.Float64:
			return fitWholeFloat(v.Float(), to)
		case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
			return fitInteger(v.Int(), to)
		case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
			return fitUnsigned(v.Uint(), to)
		}
	case reflect.Float32, reflect.Float64:
		switch from.Kind() {
		case reflect.Float32, reflect.Float64:
			return fitFloat(v.Float(), to)
		case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
			return fitIntegerAsFloat(v.Int(), to)
		case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
			return fitUnsignedAsFloat(v.Uint(), to)
		}
	}
	return data, nil
}

// fitWholeFloat writes a float into an integer field when it holds a whole
// number the field's type can hold.
//
// The range is checked on the float, against the bounds of a 64-bit integer of
// the target's signedness, before anything is converted. Converting a float an
// integer type cannot hold is implementation-defined in Go: where it saturates,
// 2^63 turns into the largest int64 and a check made after the conversion
// passes a value that is out of range as one that is not. An unsigned target
// is converted as unsigned, so the half of its range above the largest signed
// value is reachable.
func fitWholeFloat(f float64, to reflect.Type) (any, error) {
	if f != math.Trunc(f) {
		return nil, fmt.Errorf("%v is not a whole number this field can hold", f)
	}
	switch to.Kind() {
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		if f < 0 {
			return nil, fmt.Errorf("%v is negative", f)
		}
		if f >= 1<<64 {
			return nil, fmt.Errorf("%v does not fit", f)
		}
		return fitUnsigned(uint64(f), to)
	default:
		if f < -1<<63 || f >= 1<<63 {
			return nil, fmt.Errorf("%v does not fit", f)
		}
		return fitInteger(int64(f), to)
	}
}

// fitInteger writes a whole number into the target's own type, refusing a
// value the type cannot hold and a negative one where the type has no sign.
func fitInteger(n int64, to reflect.Type) (any, error) {
	out := reflect.New(to).Elem()
	switch to.Kind() {
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		if n < 0 {
			return nil, fmt.Errorf("%d is negative", n)
		}
		if out.OverflowUint(uint64(n)) {
			return nil, fmt.Errorf("%d does not fit", n)
		}
		out.SetUint(uint64(n))
	default:
		if out.OverflowInt(n) {
			return nil, fmt.Errorf("%d does not fit", n)
		}
		out.SetInt(n)
	}
	return out.Interface(), nil
}

// fitUnsigned writes a whole number with no sign into the target's own type.
// It is a leg of its own because the range above the largest signed value is
// reachable: YAML reads 18446744073709551615 as a uint64, and a field wide
// enough for it takes it.
func fitUnsigned(u uint64, to reflect.Type) (any, error) {
	out := reflect.New(to).Elem()
	switch to.Kind() {
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		if out.OverflowUint(u) {
			return nil, fmt.Errorf("%d does not fit", u)
		}
		out.SetUint(u)
	default:
		if u > math.MaxInt64 || out.OverflowInt(int64(u)) {
			return nil, fmt.Errorf("%d does not fit", u)
		}
		out.SetInt(int64(u))
	}
	return out.Interface(), nil
}

// fitFloat writes a float into the target's own float type when the field
// reads it back as the same number.
//
// A float64 field holds every float the sources deliver. A float32 field holds
// one when the float32 nearest to it is either that very value or, written in
// float32's own shortest form, the same number: 0.1 is both the float the
// source gave and what its float32 reads back as, while 16777217 comes back as
// 16777216, 0.123456789 as 0.12345679 and 1e-50 as zero. Refusing those is the
// float half of the rule the integer fields follow: a value is never stored as
// its neighbour. NaN and the infinities pass as they are, since every float
// type holds them.
func fitFloat(f float64, to reflect.Type) (any, error) {
	out := reflect.New(to).Elem()
	if out.OverflowFloat(f) {
		return nil, fmt.Errorf("%v does not fit", f)
	}
	out.SetFloat(f)
	if held := out.Float(); held != f && !math.IsNaN(f) && !readsBackAs(held, to, f) {
		return nil, fmt.Errorf("%v does not fit without rounding: the field would hold %v",
			f, strconv.FormatFloat(held, 'g', -1, to.Bits()))
	}
	return out.Interface(), nil
}

// readsBackAs reports whether a float held at a narrower precision, written in
// that precision's shortest form, reads back as want.
func readsBackAs(held float64, to reflect.Type, want float64) bool {
	back, err := strconv.ParseFloat(strconv.FormatFloat(held, 'g', -1, to.Bits()), 64)
	return err == nil && back == want
}

// fitIntegerAsFloat writes a whole number into a float field when the field's
// type holds it exactly: an integer is an exact quantity, and the float
// nearest to one the type cannot hold is a different number.
func fitIntegerAsFloat(n int64, to reflect.Type) (any, error) {
	out := reflect.New(to).Elem()
	if to.Kind() == reflect.Float32 {
		out.SetFloat(float64(float32(n)))
	} else {
		out.SetFloat(float64(n))
	}
	// The held value is below 2^63 whenever it is exact, since n is; testing
	// the bound first keeps the conversion back inside int64's range.
	if held := out.Float(); held >= 1<<63 || int64(held) != n {
		return nil, fmt.Errorf("%d does not fit without rounding: the field would hold %v",
			n, strconv.FormatFloat(out.Float(), 'g', -1, to.Bits()))
	}
	return out.Interface(), nil
}

// fitUnsignedAsFloat is fitIntegerAsFloat for a whole number with no sign,
// whose range reaches past the largest signed value.
func fitUnsignedAsFloat(u uint64, to reflect.Type) (any, error) {
	out := reflect.New(to).Elem()
	if to.Kind() == reflect.Float32 {
		out.SetFloat(float64(float32(u)))
	} else {
		out.SetFloat(float64(u))
	}
	if held := out.Float(); held >= 1<<64 || uint64(held) != u {
		return nil, fmt.Errorf("%d does not fit without rounding: the field would hold %v",
			u, strconv.FormatFloat(out.Float(), 'g', -1, to.Bits()))
	}
	return out.Interface(), nil
}

// textScalarHook reads the flat string form the environment and the command
// line give into a boolean or a number. Those two layers carry text and
// nothing else, so without this leg every numeric item would be unreachable
// from them.
//
// It is a leg of its own rather than the library's weakly typed mode, which
// would also render a number into a string field and make 1 and "1" the same
// thing -- the very distinction a non-string config key is refused over.
func textScalarHook(from, to reflect.Type, data any) (any, error) {
	if from.Kind() != reflect.String || to == durationType {
		return data, nil
	}
	text := reflect.ValueOf(data).String()
	switch to.Kind() {
	case reflect.Bool:
		parsed, err := strconv.ParseBool(text)
		if err != nil {
			return nil, fmt.Errorf("%q is not a boolean: write true or false", text)
		}
		out := reflect.New(to).Elem()
		out.SetBool(parsed)
		return out.Interface(), nil
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		n, err := strconv.ParseInt(text, 10, 64)
		if err != nil {
			return nil, fmt.Errorf("%q is not a whole number", text)
		}
		return fitInteger(n, to)
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		u, err := strconv.ParseUint(text, 10, 64)
		if err != nil {
			return nil, fmt.Errorf("%q is not a whole number that is zero or above", text)
		}
		return fitUnsigned(u, to)
	case reflect.Float32, reflect.Float64:
		// A text that is a whole number is held to the integer rule, the way
		// the same number written in a config file arrives as an integer.
		if n, err := strconv.ParseInt(text, 10, 64); err == nil {
			return fitIntegerAsFloat(n, to)
		}
		if u, err := strconv.ParseUint(text, 10, 64); err == nil {
			return fitUnsignedAsFloat(u, to)
		}
		f, err := strconv.ParseFloat(text, 64)
		if err != nil {
			return nil, fmt.Errorf("%q is not a number", text)
		}
		return fitFloat(f, to)
	}
	return data, nil
}
