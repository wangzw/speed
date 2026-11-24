package settings

import (
	"encoding"
	"fmt"
	"math"
	"reflect"
	"strconv"
	"time"
)

func encodeValue(entry keyEntry, value any) (string, error) {
	rv, err := coerce(value, entry)
	if err != nil {
		return "", err
	}
	text, err := textOf(entry, rv)
	if err != nil {
		return "", fmt.Errorf("%w: key %q as %s: %w",
			ErrTypeMismatch, entry.key, typeName(entry.typ), err)
	}
	if err := admit(entry, rv, text); err != nil {
		return "", err
	}
	return text, nil
}

func admit(entry keyEntry, rv reflect.Value, text string) error {
	judge := entry.item.Domain.judge
	if judge == nil {
		return nil
	}
	held := reflect.New(entry.typ).Elem()
	held.Set(rv)
	if judge.admits(held.Interface()) {
		return nil
	}
	if entry.item.Sensitive {
		return fmt.Errorf("%w: key %q takes %s, and the value written is not one of them. The "+
			"value is withheld: the item is marked sensitive", ErrInvalidValue, entry.key, judge.rule)
	}
	return fmt.Errorf("%w: key %q takes %s, and the value written, %q, is not one of them",
		ErrInvalidValue, entry.key, judge.rule, text)
}

func textOf(entry keyEntry, rv reflect.Value) (string, error) {
	switch entry.kind {
	case kindString:
		return rv.String(), nil
	case kindBool:
		return strconv.FormatBool(rv.Bool()), nil
	case kindInt:
		return strconv.FormatInt(rv.Int(), 10), nil
	case kindUint:
		return strconv.FormatUint(rv.Uint(), 10), nil
	case kindFloat:
		return strconv.FormatFloat(rv.Float(), 'g', -1, entry.typ.Bits()), nil
	case kindDuration:
		return time.Duration(rv.Int()).String(), nil
	case kindText:
		return marshalText(entry.typ, rv)
	}
	return "", fmt.Errorf("the declared type %s has no text form", typeName(entry.typ))
}

// marshalText writes a value through its MarshalText, copied into an addressable
// value of the declared type so a pointer-receiver method is reachable.
func marshalText(declared reflect.Type, rv reflect.Value) (string, error) {
	held := reflect.New(declared)
	held.Elem().Set(rv)
	marshaler, ok := held.Interface().(encoding.TextMarshaler)
	if !ok {
		return "", fmt.Errorf("%s has no MarshalText", typeName(declared))
	}
	text, err := marshaler.MarshalText()
	if err != nil {
		return "", err
	}
	return string(text), nil
}

func coerce(value any, entry keyEntry) (reflect.Value, error) {
	rv := reflect.ValueOf(value)
	if !rv.IsValid() {
		return reflect.Value{}, writeMismatch(entry, "nothing")
	}
	if rv.Type().AssignableTo(entry.typ) {
		return rv, nil
	}
	if entry.kind == kindDuration {
		return coerceDuration(rv, entry)
	}
	if rv.CanInt() || rv.CanUint() || rv.CanFloat() {
		if fitted, ok := fitNumber(rv, entry.typ); ok {
			return fitted, nil
		}
		return reflect.Value{}, writeMismatch(entry, describeNumber(rv, entry))
	}
	if rv.Kind() == reflect.String {
		return coerceText(rv.String(), entry)
	}
	if rv.Kind() == reflect.Bool && entry.typ.Kind() == reflect.Bool {
		return rv.Convert(entry.typ), nil
	}
	return reflect.Value{}, writeMismatch(entry, typeName(rv.Type()))
}

func coerceDuration(rv reflect.Value, entry keyEntry) (reflect.Value, error) {
	if rv.Kind() != reflect.String {
		if rv.CanInt() || rv.CanUint() || rv.CanFloat() {
			return reflect.Value{}, writeRefused(entry, describeNumber(rv, entry),
				fmt.Errorf("a duration is written as a text time.ParseDuration accepts, such as "+
					"\"5m\": a bare number counts nanoseconds, so 300 would read as five minutes "+
					"and mean three hundred nanoseconds"))
		}
		return reflect.Value{}, writeMismatch(entry, typeName(rv.Type()))
	}
	parsed, err := time.ParseDuration(rv.String())
	if err != nil {
		return reflect.Value{}, writeRefused(entry, strconv.Quote(rv.String()),
			fmt.Errorf("it is not a duration, which is written as in \"5m\", \"1h30m\" or \"200ms\""))
	}
	return reflect.ValueOf(parsed), nil
}

func coerceText(text string, entry keyEntry) (reflect.Value, error) {
	if entry.kind == kindText {
		v := reflect.New(entry.typ)
		unmarshaler, ok := v.Interface().(encoding.TextUnmarshaler)
		if !ok {
			return reflect.Value{}, writeMismatch(entry, "a string")
		}
		if err := unmarshaler.UnmarshalText([]byte(text)); err != nil {
			return reflect.Value{}, writeRefused(entry, strconv.Quote(text), err)
		}
		return v.Elem(), nil
	}
	var (
		fitted reflect.Value
		ok     bool
		err    error
	)
	switch entry.typ.Kind() {
	case reflect.String:
		return reflect.ValueOf(text).Convert(entry.typ), nil
	case reflect.Bool:
		var parsed bool
		if parsed, err = strconv.ParseBool(text); err == nil {
			fitted, ok = reflect.New(entry.typ).Elem(), true
			fitted.SetBool(parsed)
		}
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		var parsed int64
		if parsed, err = strconv.ParseInt(text, 10, 64); err == nil {
			fitted, ok = fitInteger(parsed, entry.typ)
		}
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		var parsed uint64
		if parsed, err = strconv.ParseUint(text, 10, 64); err == nil {
			fitted, ok = fitUnsigned(parsed, entry.typ)
		}
	case reflect.Float32, reflect.Float64:
		if whole, wholeErr := strconv.ParseInt(text, 10, 64); wholeErr == nil {
			fitted, ok = fitIntegerAsFloat(whole, entry.typ)
			break
		}
		if whole, wholeErr := strconv.ParseUint(text, 10, 64); wholeErr == nil {
			fitted, ok = fitUnsignedAsFloat(whole, entry.typ)
			break
		}
		var parsed float64
		if parsed, err = strconv.ParseFloat(text, 64); err == nil {
			fitted, ok = fitFloat(parsed, entry.typ)
		}
	default:
		return reflect.Value{}, writeMismatch(entry, "a string")
	}
	if err != nil {
		return reflect.Value{}, writeRefused(entry, strconv.Quote(text),
			fmt.Errorf("it does not parse as %s", typeName(entry.typ)))
	}
	if !ok {
		return reflect.Value{}, writeRefused(entry, strconv.Quote(text),
			fmt.Errorf("%s does not hold that number: it is out of range, or would be stored "+
				"as a neighbour of it", typeName(entry.typ)))
	}
	return fitted, nil
}

func fitNumber(rv reflect.Value, to reflect.Type) (reflect.Value, bool) {
	switch to.Kind() {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		switch {
		case rv.CanFloat():
			return fitWholeFloat(rv.Float(), to)
		case rv.CanInt():
			return fitInteger(rv.Int(), to)
		case rv.CanUint():
			return fitUnsigned(rv.Uint(), to)
		}
	case reflect.Float32, reflect.Float64:
		switch {
		case rv.CanFloat():
			return fitFloat(rv.Float(), to)
		case rv.CanInt():
			return fitIntegerAsFloat(rv.Int(), to)
		case rv.CanUint():
			return fitUnsignedAsFloat(rv.Uint(), to)
		}
	}
	return reflect.Value{}, false
}

// Judges the range on the float before converting.
func fitWholeFloat(f float64, to reflect.Type) (reflect.Value, bool) {
	if f != math.Trunc(f) {
		return reflect.Value{}, false
	}
	switch to.Kind() {
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		if f < 0 || f >= 1<<64 {
			return reflect.Value{}, false
		}
		return fitUnsigned(uint64(f), to)
	default:
		if f < -1<<63 || f >= 1<<63 {
			return reflect.Value{}, false
		}
		return fitInteger(int64(f), to)
	}
}

func describeNumber(rv reflect.Value, entry keyEntry) string {
	if entry.item.Sensitive {
		return typeName(rv.Type())
	}
	return fmt.Sprintf("the %s %v", typeName(rv.Type()), rv.Interface())
}

func fitInteger(n int64, to reflect.Type) (reflect.Value, bool) {
	out := reflect.New(to).Elem()
	switch to.Kind() {
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		if n < 0 || out.OverflowUint(uint64(n)) {
			return reflect.Value{}, false
		}
		out.SetUint(uint64(n))
	default:
		if out.OverflowInt(n) {
			return reflect.Value{}, false
		}
		out.SetInt(n)
	}
	return out, true
}

func fitUnsigned(u uint64, to reflect.Type) (reflect.Value, bool) {
	out := reflect.New(to).Elem()
	switch to.Kind() {
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		if out.OverflowUint(u) {
			return reflect.Value{}, false
		}
		out.SetUint(u)
	default:
		if u > math.MaxInt64 || out.OverflowInt(int64(u)) {
			return reflect.Value{}, false
		}
		out.SetInt(int64(u))
	}
	return out, true
}

// fitFloat writes a float into the target type when the item reads it back as the
// same number. A float whose nearest float32 is infinite is refused before it is
// converted, since Go leaves that conversion to the implementation.
func fitFloat(f float64, to reflect.Type) (reflect.Value, bool) {
	out := reflect.New(to).Elem()
	if to.Kind() == reflect.Float32 && math.Abs(f) >= float32Infinite && !math.IsInf(f, 0) {
		return reflect.Value{}, false
	}
	out.SetFloat(f)
	if held := out.Float(); held != f && !math.IsNaN(f) {
		back, err := strconv.ParseFloat(strconv.FormatFloat(held, 'g', -1, to.Bits()), 64)
		if err != nil || back != f {
			return reflect.Value{}, false
		}
	}
	return out, true
}

const float32Infinite = 0x1p128 - 0x1p103

func fitIntegerAsFloat(n int64, to reflect.Type) (reflect.Value, bool) {
	out := reflect.New(to).Elem()
	if to.Kind() == reflect.Float32 {
		out.SetFloat(float64(float32(n)))
	} else {
		out.SetFloat(float64(n))
	}
	if held := out.Float(); held >= 1<<63 || int64(held) != n {
		return reflect.Value{}, false
	}
	return out, true
}

func fitUnsignedAsFloat(u uint64, to reflect.Type) (reflect.Value, bool) {
	out := reflect.New(to).Elem()
	if to.Kind() == reflect.Float32 {
		out.SetFloat(float64(float32(u)))
	} else {
		out.SetFloat(float64(u))
	}
	if held := out.Float(); held >= 1<<64 || uint64(held) != u {
		return reflect.Value{}, false
	}
	return out, true
}

func writeMismatch(entry keyEntry, got string) error {
	return fmt.Errorf("%w: key %q is declared as %s, and the value written is %s",
		ErrTypeMismatch, entry.key, typeName(entry.typ), got)
}

func writeRefused(entry keyEntry, shown string, reason error) error {
	if entry.item.Sensitive {
		return fmt.Errorf("%w: key %q is declared as %s, and the value written does not convert "+
			"to it. The value is withheld: the item is marked sensitive",
			ErrTypeMismatch, entry.key, typeName(entry.typ))
	}
	return fmt.Errorf("%w: key %q is declared as %s, and the value written, %s, does not convert "+
		"to it: %w", ErrTypeMismatch, entry.key, typeName(entry.typ), shown, reason)
}

func decodeValue(entry keyEntry, text string) (any, error) {
	switch entry.kind {
	case kindString:
		v := reflect.New(entry.typ).Elem()
		v.SetString(text)
		return v.Interface(), nil
	case kindBool:
		parsed, err := strconv.ParseBool(text)
		if err != nil {
			return nil, mismatch(entry, text, err)
		}
		v := reflect.New(entry.typ).Elem()
		v.SetBool(parsed)
		return v.Interface(), nil
	case kindInt:
		parsed, err := strconv.ParseInt(text, 10, entry.typ.Bits())
		if err != nil {
			return nil, mismatch(entry, text, err)
		}
		v := reflect.New(entry.typ).Elem()
		v.SetInt(parsed)
		return v.Interface(), nil
	case kindUint:
		parsed, err := strconv.ParseUint(text, 10, entry.typ.Bits())
		if err != nil {
			return nil, mismatch(entry, text, err)
		}
		v := reflect.New(entry.typ).Elem()
		v.SetUint(parsed)
		return v.Interface(), nil
	case kindFloat:
		parsed, err := strconv.ParseFloat(text, entry.typ.Bits())
		if err != nil {
			return nil, mismatch(entry, text, err)
		}
		v := reflect.New(entry.typ).Elem()
		v.SetFloat(parsed)
		return v.Interface(), nil
	case kindDuration:
		parsed, err := time.ParseDuration(text)
		if err != nil {
			return nil, mismatch(entry, text, err)
		}
		return parsed, nil
	case kindText:
		v := reflect.New(entry.typ)
		unmarshaler, ok := v.Interface().(encoding.TextUnmarshaler)
		if !ok {
			return nil, mismatch(entry, text, fmt.Errorf("the type does not decode itself from text"))
		}
		if err := unmarshaler.UnmarshalText([]byte(text)); err != nil {
			return nil, mismatch(entry, text, err)
		}
		return v.Elem().Interface(), nil
	}
	return nil, mismatch(entry, text, fmt.Errorf("the declared type %s has no decoder", typeName(entry.typ)))
}

func mismatch(entry keyEntry, text string, cause error) error {
	if entry.item.Sensitive {
		return fmt.Errorf("%w: key %q is declared as %s and the value stored for it does not "+
			"decode under it. The value is withheld: the item is marked sensitive",
			ErrTypeMismatch, entry.key, typeName(entry.typ))
	}
	return fmt.Errorf("%w: key %q is declared as %s and its stored value %q does not decode "+
		"under it: %w", ErrTypeMismatch, entry.key, typeName(entry.typ), text, cause)
}

func requireTarget(target any, key string, entry keyEntry, declared bool) {
	rv := reflect.ValueOf(target)
	if rv.Kind() == reflect.Pointer && !rv.IsNil() {
		return
	}
	want := "the key's declared type"
	if declared {
		want = "the declared type " + typeName(entry.typ)
	}
	panic(fmt.Sprintf("settings: Get was given %s as its target for key %q, and a target is "+
		"a non-nil pointer to %s", describeTarget(target), key, want))
}

func assignTarget(entry keyEntry, target any, value any) error {
	elem := reflect.ValueOf(target).Elem()
	if elem.Type() != entry.typ {
		return fmt.Errorf("%w: key %q is declared as %s, and it was read into %s",
			ErrTypeMismatch, entry.key, typeName(entry.typ), typeName(elem.Type()))
	}
	elem.Set(reflect.ValueOf(value))
	return nil
}

func describeTarget(target any) string {
	if target == nil {
		return "nil"
	}
	return typeName(reflect.TypeOf(target))
}
