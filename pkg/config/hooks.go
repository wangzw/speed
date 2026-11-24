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

// decodeHook is the chain of conversions this module puts on top of the decoding
// library, in the order a value is decided: section keys, duration, exact
// numbers, text-decoding types, then scalar text, then the flat list form.
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

// decodeInto writes one source value into one target field, naming the path and
// the target type on failure. The target is filled through a fresh value, so a
// container arrives as a copy.
func decodeInto(path string, raw any, target reflect.Value) error {
	return decodeValue(path, raw, target, false)
}

// decodeIntoStrict writes a JSON value the environment gave into one target
// field, matching a member name exactly rather than case-insensitively.
func decodeIntoStrict(path string, raw any, target reflect.Value) error {
	return decodeValue(path, raw, target, true)
}

func decodeValue(path string, raw any, target reflect.Value, exactNames bool) error {
	out := reflect.New(target.Type())
	config := &mapstructure.DecoderConfig{
		DecodeHook:   decodeHook(),
		Result:       out.Interface(),
		TagName:      "config",
		MapFieldName: camelToKebab,
		Squash:       true,
		ZeroFields:   true,
		ErrorUnused:  true,
	}
	if exactNames {
		config.MatchName = func(mapKey, fieldName string) bool { return mapKey == fieldName }
	}
	decoder, err := mapstructure.NewDecoder(config)
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
// same names the manifest expansion derives. It carries the one leg the decoder
// has no setting for: a field named by a json tag.
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

// renameSectionKeys rewrites one section in place for one struct type. A
// section that writes both of a field's two names is refused rather than
// renamed. chain holds the struct types on the way in, so a cycle ends.
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

// durationHook keeps time.Duration on the string form alone: a bare number is
// refused, since it would read as nanoseconds to the machine. A type defined
// over time.Duration follows the integer rules unless it implements
// encoding.TextUnmarshaler.
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
// survives the trip. The library would truncate, wrap, overflow to infinity or
// round it silently; these checks refuse those outcomes.
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
// number the field's type can hold. The range is checked on the float before
// conversion, since converting an out-of-range float is implementation-defined.
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

// fitFloat writes a float into the target's own float type when the field reads
// it back as the same number, never storing a value as its neighbour. A float
// whose nearest float32 is an infinity is refused before conversion.
func fitFloat(f float64, to reflect.Type) (any, error) {
	out := reflect.New(to).Elem()
	if to.Kind() == reflect.Float32 && math.Abs(f) >= float32Infinite && !math.IsInf(f, 0) {
		return nil, fmt.Errorf("%v does not fit", f)
	}
	out.SetFloat(f)
	if held := out.Float(); held != f && !math.IsNaN(f) && !readsBackAs(held, to, f) {
		return nil, fmt.Errorf("%v does not fit without rounding: the field would hold %v",
			f, strconv.FormatFloat(held, 'g', -1, to.Bits()))
	}
	return out.Interface(), nil
}

// float32Infinite is the smallest magnitude whose nearest float32 is an
// infinity: halfway between math.MaxFloat32 and 2^128.
const float32Infinite = 0x1p128 - 0x1p103

// readsBackAs reports whether a float held at a narrower precision, written in
// that precision's shortest form, reads back as want.
func readsBackAs(held float64, to reflect.Type, want float64) bool {
	back, err := strconv.ParseFloat(strconv.FormatFloat(held, 'g', -1, to.Bits()), 64)
	return err == nil && back == want
}

// fitIntegerAsFloat writes a whole number into a float field when the field's
// type holds it exactly.
func fitIntegerAsFloat(n int64, to reflect.Type) (any, error) {
	out := reflect.New(to).Elem()
	if to.Kind() == reflect.Float32 {
		out.SetFloat(float64(float32(n)))
	} else {
		out.SetFloat(float64(n))
	}
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

// textScalarHook reads the flat string form the environment and the command line
// give into a boolean or a number. It is not the library's weakly typed mode,
// which would also render a number into a string field.
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
