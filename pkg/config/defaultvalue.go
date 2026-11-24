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

const listSeparator = ","

var durationType = reflect.TypeFor[time.Duration]()

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

func isZeroDefault(def any) bool {
	v := reflect.ValueOf(def)
	if !v.IsValid() {
		return true
	}
	return v.IsZero()
}

func placeholder(item *manifestItem) string {
	if item.item.Placeholder != "" {
		return item.item.Placeholder
	}
	if booleanLeaf(item.typ) {
		return ""
	}
	return "VALUE"
}

const requiredMarker = "(required)"

func flagUsage(item *manifestItem, runs bool) string {
	if !runs || !item.item.Required || item.documented {
		return item.item.Description
	}
	if item.item.Description == "" {
		return requiredMarker
	}
	return item.item.Description + " " + requiredMarker
}

func formatDefault(item *manifestItem) string {
	v := reflect.ValueOf(item.def)
	if item.kind == kindStringList {
		return quoteForCommandLine(formatStringList(v))
	}
	return quoteForCommandLine(formatScalarDefault(v))
}

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

func listElementText(v reflect.Value) string {
	for v.Kind() == reflect.Interface {
		if v.IsNil() {
			return ""
		}
		v = v.Elem()
	}
	return fmt.Sprint(v.Interface())
}

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

func meansUnset(v reflect.Value) bool {
	for v.Kind() == reflect.Pointer {
		if v.IsNil() {
			return true
		}
		v = v.Elem()
	}
	return false
}

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

func residualDefault(v reflect.Value) string {
	return fmt.Sprintf("%v", v)
}

// bareOnACommandLine holds the punctuation that stands for itself in an unquoted word.
const bareOnACommandLine = "_@%+=:,./-"

// quoteForCommandLine renders text as one word of a POSIX command line, so the program receives exactly that text in argv.
func quoteForCommandLine(text string) string {
	if !printableText(text) {
		return strconv.Quote(text)
	}
	if text == "" || strings.ContainsFunc(text, func(r rune) bool { return !bareRune(r) }) {
		return "'" + strings.ReplaceAll(text, "'", `'\''`) + "'"
	}
	return text
}

func bareRune(r rune) bool {
	switch {
	case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		return true
	}
	return strings.ContainsRune(bareOnACommandLine, r)
}

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
