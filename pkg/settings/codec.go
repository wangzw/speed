package settings

import (
	"encoding"
	"fmt"
	"math"
	"reflect"
	"strconv"
	"time"
)

// A value crosses between the typed world and the text column on the way in,
// when Set encodes what the caller handed it, and at snapshot load, when every
// row is decoded under its declaration. A read of a scalar kind then serves
// the decoded value with no text conversion on the path. A read of a type that
// carries its own text form decodes the text once more, so that what it hands
// out shares no storage with the module (Get says why).
//
// The two directions are inverses of one another, and the round trip is what
// the closed type set buys: every type in it has a text form both sides agree
// on, so a value written once reads back the same.

// encodeValue turns a declared value into the text to store.
//
// The value is first held to the declared type under configuration's value
// discipline (coerce), the text is then written in the declared type's own
// form, and the value is last held to the item's declared domain (admit). What
// is stored is therefore always that form, whatever the caller handed in: a
// duration written as "300s" is stored as "5m0s". The domain is judged on the
// value held to the declared type rather than on the form it arrived in, so
// the number 0, the text "0" and a 0 decoded from JSON are one value to it.
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

// admit holds a value already held to the declared type to the item's
// declared domain, and refuses one outside it with ErrInvalidValue naming the
// key and the rule. The zero Domain admits every value.
//
// The predicate is handed a value of exactly the declared type. coerce may
// pass a value that is only assignable to it — an unnamed slice for a type
// defined over one — so the value is first copied into one of the declared
// type. The refusal quotes the text the value would have been stored as, the
// form List reports it in, unless the item is sensitive.
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

// textOf writes a value of the declared type in that type's own text form,
// which is what a row holds and what decodeValue reads back. It is the one
// place the text form is decided, for a write and for a declared default
// alike.
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
		// The shortest text that parses back to the same float, so a value
		// survives the round trip rather than being rounded by a fixed
		// precision.
		return strconv.FormatFloat(rv.Float(), 'g', -1, entry.typ.Bits()), nil
	case kindDuration:
		// ParseDuration's own format, which is what a duration is written as
		// everywhere else in this repository.
		return time.Duration(rv.Int()).String(), nil
	case kindText:
		return marshalText(entry.typ, rv)
	}
	// Unreachable: classifyLeaf admits only the kinds above, and a declaration
	// failing it never reaches the table.
	return "", fmt.Errorf("the declared type %s has no text form", typeName(entry.typ))
}

// marshalText writes a value that carries its own text form through its
// MarshalText.
//
// The value is first copied into an addressable value of the declared type,
// for two reasons. MarshalText may be declared on the pointer — math/big.Int
// declares it there — and a value's own method set lacks the methods of its
// pointer; and a value that coerce passed because it is assignable to the
// declared type may be of an unnamed type that has no methods at all, where
// what is written is the declared type's text.
func marshalText(declared reflect.Type, rv reflect.Value) (string, error) {
	held := reflect.New(declared)
	held.Elem().Set(rv)
	marshaler, ok := held.Interface().(encoding.TextMarshaler)
	if !ok {
		// Unreachable: a declaration whose type has no MarshalText on either
		// receiver is refused when the key table is built.
		return "", fmt.Errorf("%s has no MarshalText", typeName(declared))
	}
	text, err := marshaler.MarshalText()
	if err != nil {
		return "", err
	}
	return string(text), nil
}

// coerce holds a written value to the declared type.
//
// The discipline is configuration's, restated here because nothing in
// pkg/config exports it: a parameter moving between the two modules has to
// accept the same values in both, and a write takes whatever a configuration
// source would give for the same item — a value decoded out of a JSON or YAML
// document an administrative endpoint received is the ordinary case. The steps
// run in the order configuration decides them in, and the first that applies
// settles the value:
//
//   - A value already of the declared type passes.
//   - A time.Duration takes a text time.ParseDuration accepts, and nothing
//     else. A bare number is refused: its unit is the nanosecond, so a 300
//     that reads as five minutes to whoever wrote it would be stored as three
//     hundred nanoseconds. A type defined over time.Duration is not one: to
//     reflection it is an int64 of its own, as it is to configuration, and
//     the integer rules hold it.
//   - A number converts into a numeric item of another Go type when the item
//     holds it as the same number. JSON has no integer type, so a 10 decoded
//     as a float belongs in an integer item, while 10.5 does not; an integer
//     goes into a float item whose type holds it exactly; a float goes into a
//     float32 item when it reads back as the same number, which 0.1 does and
//     16777217 does not; a value outside the item type's range is refused.
//   - A text goes into a type that decodes itself from text through that
//     type's own UnmarshalText.
//   - A text goes into a boolean or a numeric item when it parses as one, and
//     the number it parses as is held to the rule above. An integer item takes
//     a decimal integer only, so "10.0" and "1e3" are refused where the numbers
//     10.0 and 1e3 are not; a float item holds a text that parses as a whole
//     number to the integer rule, so "16777217" does not go into a float32.
//   - A string or a boolean goes into a defined type built on the same kind.
//
// Nothing else converts. A declaration is what the column is read back under,
// and a value of an unrelated type would produce a row no read of that key
// could decode. TestTheWriteDisciplineAgreesWithConfiguration drives one table
// of values through configuration's decoding and through this function, which
// is what holds the two statements of the discipline in step.
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

// coerceDuration holds a value to time.Duration: a text time.ParseDuration
// accepts, and nothing else. The value is not of the declared type, or coerce
// would have passed it already.
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

// coerceText holds a text to the declared type: through the type's own
// UnmarshalText when it has one, by parsing it when the item is a boolean or a
// number, and as the same string otherwise when the item is a string.
func coerceText(text string, entry keyEntry) (reflect.Value, error) {
	if entry.kind == kindText {
		v := reflect.New(entry.typ)
		unmarshaler, ok := v.Interface().(encoding.TextUnmarshaler)
		if !ok {
			// Unreachable: classifyLeaf admits a type as kindText because it
			// decodes itself from text.
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
		// A text that is a whole number is held to the integer rule, the way
		// configuration holds the same text and the same number written in a
		// document.
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

// fitNumber writes a number into a numeric item of another Go type, and reports
// false when the item is not numeric, when a fractional value meets an integer
// item, when the value is outside the item type's range, or when a float item
// would hold a number other than the one written.
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

// fitWholeFloat writes a float into an integer item when it holds a whole
// number the item's type can hold.
//
// The range is judged on the float, against the bounds of a 64-bit integer of
// the item's signedness, before anything is converted. Converting a float an
// integer type cannot hold is implementation-defined in Go: where it
// saturates, 2^63 turns into the largest int64 and a check made after the
// conversion would store a number one past the range as a different one. An
// unsigned item is converted as unsigned, so the half of its range above the
// largest signed value is reachable.
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

// describeNumber names a number in a refusal. The number itself is shown, as
// the nearest thing to what the caller wrote, unless the item is sensitive:
// then only its Go type is.
func describeNumber(rv reflect.Value, entry keyEntry) string {
	if entry.item.Sensitive {
		return typeName(rv.Type())
	}
	return fmt.Sprintf("the %s %v", typeName(rv.Type()), rv.Interface())
}

// fitInteger writes a whole number into the target's own type, refusing a
// value the type cannot hold and a negative one where the type has no sign.
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

// fitUnsigned writes a whole number with no sign into the target's own type.
// It is a leg of its own because the range above the largest signed value is
// reachable, and a field wide enough for it takes it.
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

// fitFloat writes a float into the target's own float type when the item reads
// it back as the same number: a float64 item holds every float, and a float32
// item holds one whose nearest float32 is either that value or, written in
// float32's own shortest form, the same number. 0.1 passes; 16777217 would
// come back as 16777216 and 1e-50 as zero, and both are refused. NaN and the
// infinities pass as they are.
//
// A float whose nearest float32 is an infinity is refused before it is
// converted, since Go leaves the result of converting a float the target type
// cannot hold to the implementation. The line is where the nearest float32
// stops being finite, not math.MaxFloat32 itself: float32 writes its largest
// value as 3.4028235e+38, a number a little above it whose nearest float32 is
// that value, so the text a float32 item's largest value is stored as goes
// back in like any other.
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

// float32Infinite is the smallest magnitude whose nearest float32 is an
// infinity: halfway between math.MaxFloat32 and 2^128, the next step up, where
// rounding to nearest even goes to the step, since math.MaxFloat32 is odd.
const float32Infinite = 0x1p128 - 0x1p103

// fitIntegerAsFloat writes a whole number into a float item when the item's
// type holds it exactly; the float nearest to one it cannot hold is a
// different number.
func fitIntegerAsFloat(n int64, to reflect.Type) (reflect.Value, bool) {
	out := reflect.New(to).Elem()
	if to.Kind() == reflect.Float32 {
		out.SetFloat(float64(float32(n)))
	} else {
		out.SetFloat(float64(n))
	}
	// The held value is below 2^63 whenever it is exact, since n is; testing
	// the bound first keeps the conversion back inside int64's range.
	if held := out.Float(); held >= 1<<63 || int64(held) != n {
		return reflect.Value{}, false
	}
	return out, true
}

// fitUnsignedAsFloat is fitIntegerAsFloat for a whole number with no sign.
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

// writeMismatch reports a value that cannot be held to its declaration. got
// describes the value without its content where the item is sensitive.
func writeMismatch(entry keyEntry, got string) error {
	return fmt.Errorf("%w: key %q is declared as %s, and the value written is %s",
		ErrTypeMismatch, entry.key, typeName(entry.typ), got)
}

// writeRefused reports a value of a kind the declaration takes that still does
// not convert to it: a text that does not parse, a number a duration refuses.
//
// A sensitive item's value is withheld, and so is the reason, for the reason
// mismatch gives: a parser's error quotes the text it failed on.
func writeRefused(entry keyEntry, shown string, reason error) error {
	if entry.item.Sensitive {
		return fmt.Errorf("%w: key %q is declared as %s, and the value written does not convert "+
			"to it. The value is withheld: the item is marked sensitive",
			ErrTypeMismatch, entry.key, typeName(entry.typ))
	}
	return fmt.Errorf("%w: key %q is declared as %s, and the value written, %s, does not convert "+
		"to it: %w", ErrTypeMismatch, entry.key, typeName(entry.typ), shown, reason)
}

// decodeValue turns the text of a stored row into a value of the declared
// type. It is called at snapshot load for every row.
func decodeValue(entry keyEntry, text string) (any, error) {
	switch entry.kind {
	case kindString:
		// The value is rebuilt as the declared type rather than handed back as
		// the builtin it was parsed into: a declaration may name a defined
		// string type, and a builtin string is not assignable to it.
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
	// Unreachable, for the reason encodeValue's last line gives.
	return nil, mismatch(entry, text, fmt.Errorf("the declared type %s has no decoder", typeName(entry.typ)))
}

// mismatch wraps a decoding failure as ErrTypeMismatch and names the key and
// the declared type, which is what the reader needs to reconcile the
// declaration with the value already in the database.
//
// A sensitive item's value is withheld, and so is the cause: a conversion
// error quotes the text it failed on, so keeping the cause would put the value
// back into the message the marker exists to keep it out of. The key and the
// declared type are what the reader acts on either way; the value is available
// from List for an item that is not sensitive.
func mismatch(entry keyEntry, text string, cause error) error {
	if entry.item.Sensitive {
		return fmt.Errorf("%w: key %q is declared as %s and the value stored for it does not "+
			"decode under it. The value is withheld: the item is marked sensitive",
			ErrTypeMismatch, entry.key, typeName(entry.typ))
	}
	return fmt.Errorf("%w: key %q is declared as %s and its stored value %q does not decode "+
		"under it: %w", ErrTypeMismatch, entry.key, typeName(entry.typ), text, cause)
}

// requireTarget panics unless the target of a read is a non-nil pointer.
//
// A target of any other shape is a mistake at the call site, not a fact about
// the data: it is settled the first time the line runs, whatever key the line
// reads and whatever the snapshot holds for it, and no caller has anything to
// do with the error. The judgement is configuration's, on its value-reading
// side. The declared type is named when the key has one, since that is the
// type the pointer has to point to.
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

// assignTarget writes a decoded value into the pointer a caller passed to Get,
// which requireTarget has already held to being a non-nil pointer.
//
// The target's element type must be the declared type exactly: a caller asking
// for a key declared as one type through a pointer to another is the shape
// ErrTypeMismatch is for, and it is caught here rather than at the assignment,
// which would panic.
//
// The value is the caller's own already: Get hands a scalar kind over as it
// is, since it carries no reference, and decodes a type that carries its own
// text form afresh.
func assignTarget(entry keyEntry, target any, value any) error {
	elem := reflect.ValueOf(target).Elem()
	if elem.Type() != entry.typ {
		return fmt.Errorf("%w: key %q is declared as %s, and it was read into %s",
			ErrTypeMismatch, entry.key, typeName(entry.typ), typeName(elem.Type()))
	}
	elem.Set(reflect.ValueOf(value))
	return nil
}

// describeTarget names a target for the panic message.
func describeTarget(target any) string {
	if target == nil {
		return "nil"
	}
	return typeName(reflect.TypeOf(target))
}
