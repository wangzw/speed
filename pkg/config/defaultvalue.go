package config

import (
	"encoding"
	"fmt"
	"reflect"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

// listSeparator is how the environment and the command line give a list of
// strings. It carries no escape: an element holding a comma has to come from the
// primary config source.
const listSeparator = ","

var durationType = reflect.TypeFor[time.Duration]()

// flagDefaultString is the default the command-line library is handed. A
// sensitive item, one whose value stands for being unset, and a required item no
// document supplies hand over nothing.
func flagDefaultString(item *manifestItem) string {
	if item.item.Sensitive || (item.item.Required && !item.documented) ||
		meansUnset(reflect.ValueOf(item.def)) {
		return ""
	}
	if isZeroDefault(item.def) {
		return ""
	}
	return formatDefault(item)
}

// isZeroDefault reports whether a default is the zero value of its type.
func isZeroDefault(def any) bool {
	v := reflect.ValueOf(def)
	if !v.IsValid() {
		return true
	}
	return v.IsZero()
}

// placeholder is the value placeholder of an item. A boolean has none.
func placeholder(item *manifestItem) string {
	if item.item.Placeholder != "" {
		return item.item.Placeholder
	}
	if booleanLeaf(item.typ) {
		return ""
	}
	return "VALUE"
}

// requiredMarker tells an argument nobody may omit from an optional one.
const requiredMarker = "(required)"

// flagUsage is the text the command-line library prints beside an argument,
// carrying the marker when the item is required and this command will ask for
// it.
func flagUsage(item *manifestItem, runs bool) string {
	if !runs || !item.item.Required || item.documented {
		return item.item.Description
	}
	if item.item.Description == "" {
		return requiredMarker
	}
	return item.item.Description + " " + requiredMarker
}

// formatDefault renders the declared default in the form a reader can type
// back: 5m rather than a count of nanoseconds, a,b rather than [a b]. The shapes
// are decided in the order the conversion on the way in decides them.
func formatDefault(item *manifestItem) string {
	v := reflect.ValueOf(item.def)
	if item.kind == kindStringList {
		return quoteForCommandLine(formatStringList(v))
	}
	return quoteForCommandLine(formatScalarDefault(v))
}

// formatStringList renders a list in the flat comma-separated form the command
// line gives it. An element holding a comma has no flat form.
func formatStringList(v reflect.Value) string {
	if !v.IsValid() {
		return ""
	}
	parts := make([]string, v.Len())
	for i := range v.Len() {
		parts[i] = listElementText(v.Index(i))
	}
	return strings.Join(parts, listSeparator)
}

// listElementText renders one element of a list default, reading it out of the
// interface it arrived in.
func listElementText(v reflect.Value) string {
	for v.Kind() == reflect.Interface {
		if v.IsNil() {
			return ""
		}
		v = v.Elem()
	}
	return fmt.Sprint(v.Interface())
}

// formatScalarDefault renders a single value in the form the command line would
// give it.
func formatScalarDefault(v reflect.Value) string {
	for v.Kind() == reflect.Pointer && !meansUnset(v) {
		v = v.Elem()
	}
	t := v.Type()
	if t == durationType {
		return time.Duration(v.Int()).String()
	}
	if decodesFromText(t) {
		if text, ok := textForm(v); ok {
			return text
		}
		return residualDefault(v)
	}
	switch t.Kind() {
	case reflect.Bool:
		return strconv.FormatBool(v.Bool())
	case reflect.String:
		return v.String()
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return strconv.FormatInt(v.Int(), 10)
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return strconv.FormatUint(v.Uint(), 10)
	case reflect.Float32, reflect.Float64:
		return strconv.FormatFloat(v.Float(), 'g', -1, t.Bits())
	}
	return residualDefault(v)
}

// meansUnset reports whether a field's current value stands for "nobody
// supplied one" rather than for a value of its own.
func meansUnset(v reflect.Value) bool {
	for v.Kind() == reflect.Pointer {
		if v.IsNil() {
			return true
		}
		v = v.Elem()
	}
	return false
}

// textForm renders a value through its own text output form. The value is
// copied into an addressable one first, so a method on the pointer receiver is
// reached.
func textForm(v reflect.Value) (string, bool) {
	held := reflect.New(v.Type())
	held.Elem().Set(v)
	m, ok := held.Interface().(encoding.TextMarshaler)
	if !ok {
		return "", false
	}
	text, err := m.MarshalText()
	if err != nil {
		return "", false
	}
	return string(text), true
}

// residualDefault renders a value that has no form a reader could type back,
// using Go's own printed form.
func residualDefault(v reflect.Value) string {
	return fmt.Sprintf("%v", v)
}

// bareOnACommandLine holds the punctuation that stands for itself in an unquoted
// word. It is written as what is allowed, since an incomplete enumeration of
// harmful characters would let the shell rewrite a value.
const bareOnACommandLine = "_@%+=:,./-"

// quoteForCommandLine renders text as one word of a POSIX command line: what the
// program receives in argv is this text again, byte for byte. Text carrying a
// character with no printable form is given Go's escaped form; empty text or
// text outside the bare set is wrapped in single quotes.
func quoteForCommandLine(text string) string {
	if !printableText(text) {
		return strconv.Quote(text)
	}
	if text == "" || strings.ContainsFunc(text, func(r rune) bool { return !bareRune(r) }) {
		return "'" + strings.ReplaceAll(text, "'", `'\''`) + "'"
	}
	return text
}

// bareRune reports whether one character stands for itself in an unquoted word.
func bareRune(r rune) bool {
	switch {
	case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		return true
	}
	return strings.ContainsRune(bareOnACommandLine, r)
}

// printableText reports whether every character of the text has a printable
// form.
func printableText(text string) bool {
	for i := 0; i < len(text); {
		r, size := utf8.DecodeRuneInString(text[i:])
		if r == utf8.RuneError && size == 1 {
			return false
		}
		if !strconv.IsPrint(r) {
			return false
		}
		i += size
	}
	return true
}
