package settings

import (
	"encoding"
	"fmt"
	"math"
	"reflect"
	"strconv"
	"time"
)

// A value crosses between the typed world and the text column exactly twice:
// once on the way in, when Set encodes what the caller handed it, and once at
// snapshot load, when every row is decoded under its declaration. Reads then
// serve the decoded value with no text conversion on the path at all.
//
// The two directions are inverses of one another, and the round trip is what
// the closed type set buys: every type in it has a text form both sides agree
// on, so a value written once reads back the same.

// encodeValue turns a declared value into the text to store.
//
// The value is first held to the declared type under configuration's value
// discipline (coerce), and the text is then written in the declared type's own
// form. What is stored is therefore always that form, whatever the caller
// handed in: a duration written as "300s" is stored as "5m0s".
func encodeValue(entry keyEntry, value any) (string, error) {
	rv, err := coerce(value, entry)
	if err != nil {
		return "", err
	}
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
		marshaler, ok := rv.Interface().(encoding.TextMarshaler)
		if !ok {
			return "", fmt.Errorf("%w: key %q is declared as %s, which decodes itself from "+
				"text but cannot encode itself back to it: give the type a MarshalText, or "+
				"declare the item as the string it is written as",
				ErrTypeMismatch, entry.key, typeName(entry.typ))
		}
		text, err := marshaler.MarshalText()
		if err != nil {
			return "", fmt.Errorf("%w: key %q as %s: %w",
				ErrTypeMismatch, entry.key, typeName(entry.typ), err)
		}
		return string(text), nil
	}
	// Unreachable: classifyLeaf admits only the kinds above, and a declaration
	// failing it never reaches the table.
	return "", fmt.Errorf("%w: key %q has an unencodable declared type %s",
		ErrTypeMismatch, entry.key, typeName(entry.typ))
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
//     hundred nanoseconds.
//   - A number converts into a numeric item of another Go type when the item
//     holds it. JSON has no integer type, so a 10 decoded as a float belongs in
//     an integer item, while 10.5 does not; an integer goes into a float item;
//     a value outside the item type's range is refused.
//   - A text goes into a type that decodes itself from text through that
//     type's own UnmarshalText.
//   - A text goes into a boolean or a numeric item when it parses as one.
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
			fmt.Errorf("the number is outside what %s holds", typeName(entry.typ)))
	}
	return fitted, nil
}

// fitNumber writes a number into a numeric item of another Go type, and reports
// false when the item is not numeric, when a fractional value meets an integer
// item, or when the value is outside the item type's range.
func fitNumber(rv reflect.Value, to reflect.Type) (reflect.Value, bool) {
	switch to.Kind() {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		switch {
		case rv.CanFloat():
			f := rv.Float()
			whole := int64(f)
			if float64(whole) != f {
				return reflect.Value{}, false
			}
			return fitInteger(whole, to)
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
			return fitFloat(float64(rv.Int()), to)
		case rv.CanUint():
			return fitFloat(float64(rv.Uint()), to)
		}
	}
	return reflect.Value{}, false
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

// fitFloat writes a number into the target's own float type, refusing a value
// the type cannot hold.
func fitFloat(f float64, to reflect.Type) (reflect.Value, bool) {
	out := reflect.New(to).Elem()
	if out.OverflowFloat(f) {
		return reflect.Value{}, false
	}
	out.SetFloat(f)
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
// The value is detached on the way out. A read hands out what the module
// holds — the key table's declared default, or the snapshot's stored value —
// and for a declared type that is a slice underneath, assigning it would share
// that slice with the caller.
func assignTarget(entry keyEntry, target any, value any) error {
	elem := reflect.ValueOf(target).Elem()
	if elem.Type() != entry.typ {
		return fmt.Errorf("%w: key %q is declared as %s, and it was read into %s",
			ErrTypeMismatch, entry.key, typeName(entry.typ), typeName(elem.Type()))
	}
	elem.Set(detached(reflect.ValueOf(value)))
	return nil
}

// describeTarget names a target for the panic message.
func describeTarget(target any) string {
	if target == nil {
		return "nil"
	}
	return typeName(reflect.TypeOf(target))
}
