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
// The value is first held to the declared type. A value that is not already of
// that type is refused rather than converted, with one exception the two
// modules share: a whole number written into an integer item is accepted, and
// a fractional one or one the item's type cannot hold is not. That is the
// discipline configuration applies to the same conversion, and a parameter
// moving between the two modules has to behave the same way in both.
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
// A value already of that type passes through. A number of another Go type is
// converted under the discipline configuration applies to the same situation:
// JSON has no integer type, so a 10 written as a float belongs in an integer
// item, while 10.5 does not, and neither does a value the item's type cannot
// hold. Nothing else converts, because a declaration is what the column is read
// back under and a value of an unrelated type would produce a row no read of
// that key could decode.
func coerce(value any, entry keyEntry) (reflect.Value, error) {
	rv := reflect.ValueOf(value)
	if !rv.IsValid() {
		return reflect.Value{}, writeMismatch(entry, "nothing")
	}
	if rv.Type().AssignableTo(entry.typ) {
		return rv, nil
	}
	switch entry.typ.Kind() {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		switch rv.Kind() {
		case reflect.Float32, reflect.Float64:
			f := rv.Float()
			whole := int64(f)
			if float64(whole) != f {
				break
			}
			if fitted, ok := fitInteger(whole, entry.typ); ok {
				return fitted, nil
			}
		case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
			if fitted, ok := fitInteger(rv.Int(), entry.typ); ok {
				return fitted, nil
			}
		case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
			if fitted, ok := fitUnsigned(rv.Uint(), entry.typ); ok {
				return fitted, nil
			}
		}
	case reflect.Float32, reflect.Float64:
		if rv.Kind() == reflect.Float32 || rv.Kind() == reflect.Float64 {
			if fitted, ok := fitFloat(rv.Float(), entry.typ); ok {
				return fitted, nil
			}
		}
	}
	return reflect.Value{}, writeMismatch(entry, typeName(rv.Type()))
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

// writeMismatch reports a value that cannot be held to its declaration.
func writeMismatch(entry keyEntry, got string) error {
	return fmt.Errorf("%w: key %q is declared as %s, and the value written is %s",
		ErrTypeMismatch, entry.key, typeName(entry.typ), got)
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

// assignTarget writes a decoded value into the pointer a caller passed to Get.
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
	rv := reflect.ValueOf(target)
	if rv.Kind() != reflect.Pointer || rv.IsNil() {
		// A target that is not a non-nil pointer is a mistake at the call
		// site, not a fact about the data: it is settled the first time the
		// line runs, and no caller has anything to do with the error. The
		// judgement is configuration's, on its value-reading side.
		panic(fmt.Sprintf("settings: Get was given %s as its target for key %q, and a target is "+
			"a non-nil pointer to the declared type %s",
			describeTarget(target), entry.key, typeName(entry.typ)))
	}
	elem := rv.Elem()
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
