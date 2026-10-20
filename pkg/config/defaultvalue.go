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
// strings. It carries no escape: an element holding a comma cannot be written
// this way and has to come from the primary config source. An escape would be
// a quoting rule only this project knows, which is the very thing the flat
// form is meant to avoid.
const listSeparator = ","

var durationType = reflect.TypeFor[time.Duration]()

// flagDefaultString is the default the command-line library is handed.
//
// Several items hand over nothing. A sensitive one, because the library
// prints what it receives and a credential in the help output is a leak; its
// real default still reaches the value of the run, through the layering
// tool's default layer alone. One whose value stands for being unset, because
// there is no default to write and a rendered stand-in would become the value
// the layering tool falls back on when every layer is silent. A required one
// the host defaults document does not supply, because a run cannot start
// without it and printing a default would say it could. And one whose default
// is the zero of its type, because the library omits the default of its own
// types on the same test, and a rendered zero reads as a value that was
// chosen.
//
// A required item the document does supply hands over the document's value
// like any other default: naming it in the document is what makes the run
// start without the argument, and withholding the value while leaving the
// default column empty would hide the one fact the reader needs.
//
// The last two matter to the reader rather than to the value: an empty string
// rendered in the form it would be typed back is a bare pair of quotes, which
// the library then prints as a default the item does not have.
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

// placeholder is the value placeholder of an item: the declared one, or a
// neutral stand-in. A boolean has none, since its argument may be written
// without a value.
func placeholder(item *manifestItem) string {
	if item.item.Placeholder != "" {
		return item.item.Placeholder
	}
	if booleanLeaf(item.typ) {
		return ""
	}
	return "VALUE"
}

// requiredMarker tells an argument nobody may omit from an optional one. The
// command-line library has no column of its own for it, and a required item no
// document supplies hands over no default, so the place left for the marker is
// the usage text - and without it the two kinds of argument read alike on the
// page, while which of the two an argument is decides whether the run starts
// at all.
const requiredMarker = "(required)"

// flagUsage is the text the command-line library prints beside an argument:
// the declared description, carrying the marker when the item is required and
// this command is one that will ask for it.
//
// The library prints this string as it stands, which is what makes it the
// marker's seat.
//
// runs is whether the item's module is one this command runs. An item of a
// module taken in for its declarations alone is never decoded -- the module is
// not constructed -- so no run will come up short of its value, and marking it
// would say the opposite on the one page where the reader is deciding whether
// a run can start without it.
//
// An item the host defaults document supplies is not marked for the same
// reason: the document gave the value, so this command starts without the
// argument, and the marker promises otherwise. The two cases are told apart by
// what the document named, not by the declaration alone.
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
// back. The help output is a human interface, so the criterion is that a
// reader copying this value into the command line gets exactly this default
// again: 5m rather than a count of nanoseconds, a,b rather than [a b].
//
// The syntax that criterion is read against is the command line, and only it.
// Help renders the items exposed as arguments, so that syntax is settled;
// a config source has a type system of its own, where the same text is not
// verbatim-true - YAML reads 42 as a number, and a field that decodes itself
// from a string needs "42" written there - and one rendering cannot hold for
// both.
//
// The shapes are decided in the order the conversion on the way in decides
// them, or the two ends would disagree about what a value is. A list is
// settled first, because a list of strings takes the comma-separated form
// without ever reaching the text path; a duration is settled by its type
// rather than by its kind, or every other int64 would be printed as a span of
// time.
//
// Quoting comes last and is decided on the rendered text alone. It is the only
// place it can be decided: needing quotes is a property of the characters, and
// a rule reading the field's Go kind leaves a type that carries its own text
// form bare however many spaces that form holds.
func formatDefault(item *manifestItem) string {
	v := reflect.ValueOf(item.def)
	if item.kind == kindStringList {
		return quoteForCommandLine(formatStringList(v))
	}
	return quoteForCommandLine(formatScalarDefault(v))
}

// formatStringList renders a list in the flat comma-separated form the command
// line gives it. The separator carries no escape on the way in, so an element
// holding a comma has no flat form at all, and quoting does not rescue one:
// whatever quotes surrounded the text, what is read back is split on the comma.
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

// listElementText renders one element of a list default.
//
// The element is read out of whatever interface it arrived in. A host defaults
// document reaches the item as its parser gave it, and both parsers read a
// sequence as a list of interface values; reflect's own text form of one is
// "<interface {} Value>" rather than the value inside it. The declared type
// still decides that the item is a list of strings, so an element that is not
// one stays a value Decode refuses - rendering it here does not change what the
// run does.
func listElementText(v reflect.Value) string {
	for v.Kind() == reflect.Interface {
		if v.IsNil() {
			// The document wrote nothing where a string goes. There is no text
			// to render, and Go's spelling of nil would read as one.
			return ""
		}
		v = v.Elem()
	}
	return fmt.Sprint(v.Interface())
}

// formatScalarDefault renders a single value in the form the command line
// would give it.
func formatScalarDefault(v reflect.Value) string {
	// A scalar pointer is a leaf of its own, and what the command line gives
	// it is the value behind it, never the address.
	for v.Kind() == reflect.Pointer && !meansUnset(v) {
		v = v.Elem()
	}
	t := v.Type()
	if t == durationType {
		return time.Duration(v.Int()).String()
	}
	if decodesFromText(t) {
		// A leaf that reads itself from a string is rendered by that same
		// type's output form, which is the one text its own parser is meant
		// to take back. Nothing else here knows how to write it.
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
		// The width is the field's own: read as 64 bits, a declared 0.1 in a
		// 32-bit field grows a tail of digits the module never wrote.
		return strconv.FormatFloat(v.Float(), 'g', -1, t.Bits())
	}
	return residualDefault(v)
}

// meansUnset reports whether a field's current value stands for "nobody
// supplied one" rather than for a value of its own. It is a question put to
// the value, not a list of types: the next type that expresses being unset
// gains a branch here and nothing else changes.
//
// A nil scalar pointer is the one shape that answers yes today. It is how a
// module tells "nobody gave it" from "it was given the zero value", so neither
// the zero nor Go's <nil> is the truth about it.
func meansUnset(v reflect.Value) bool {
	for v.Kind() == reflect.Pointer {
		if v.IsNil() {
			return true
		}
		v = v.Elem()
	}
	return false
}

// textForm renders a value through its own text output form.
//
// The value is copied into an addressable one first: a default is held in an
// interface and cannot be addressed, so asking it directly for the interface
// would miss every implementation carried on the pointer receiver - which is
// where math/big.Int carries both of its text methods. This is the mirror of
// how the conversion on the way in reaches the decoding half.
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

// residualDefault renders a value that has no form a reader could type back.
// Go's own printed form is what it gets, because a spelling invented here
// would be a word the help output offers and the command line does not take.
// Two kinds of value arrive:
//
//   - a leaf that reads itself from text but writes none. Its stored value is
//     not the text its own parser takes back - a percentage holding 50 reads
//     itself from "50%" - so neither the number nor the printed form is a
//     value this column can promise.
//   - a value the command line cannot give at all: a complex number, a
//     uintptr, a pointer to a list. These reach the help output because
//     collection lists them, and what they print is fixed here rather than
//     left to chance.
//
// Quoting reaches these along with every other rendered text, and it promises
// them no more than it promises the rest: the word arrives in argv unrewritten.
// Whether the parser then takes that word is a separate question, and for a
// residual value the answer stays no.
func residualDefault(v reflect.Value) string {
	return fmt.Sprintf("%v", v)
}

// bareOnACommandLine holds the punctuation that stands for itself wherever it
// appears in an unquoted word; letters and digits do too, and are decided by
// range. The set is written as what is allowed rather than as what is not, for
// the same reason the criterion is not written in terms of Go kinds: an
// enumeration of the harmful characters is the one that can be incomplete, and
// the two directions fail differently. A character missing from this set costs
// a pair of quotes around a value that would have survived without them, and
// the value still arrives verbatim. A harmful character missing from an
// enumeration reaches the reader as a value the shell rewrites.
//
// The same conservatism decides the three that are absent: ~ expands at the
// head of a word, ! is the history expansion of an interactive shell, and ^
// carries a meaning of its own in more than one shell. % is here, because a
// job spec is read where a command name goes and a default value never stands
// in that position. Anything outside ASCII is outside the set as well, so a
// default written in Chinese renders inside quotes it does not need - the
// harmless direction of the two.
const bareOnACommandLine = "_@%+=:,./-"

// quoteForCommandLine renders text as one word of a POSIX command line: what
// the program receives in argv is this text again, byte for byte. Three cases,
// decided in this order.
//
// Text carrying a character with no printable form is given Go's escaped form,
// and that form is the one thing here the reader cannot copy back. A POSIX
// command line has no single-line literal for a newline - $'a\nb' is an
// extension bash and zsh have and dash does not - while the help output gives
// each item a line of its own, so a value that really broke its line would
// take the layout with it. An escape character is worse than unreadable: put
// through raw it is read by the terminal rather than by the reader. What this
// case offers is a readable single line, not a value that can be typed back.
//
// Text that is empty, or that carries anything outside the bare set, is
// wrapped in single quotes, each quote of its own closed, escaped and
// reopened. Nothing is special inside single quotes, which makes this the one
// form that holds for arbitrary printable text - and the reason Go's quoting
// is not the one to use: "a $HOME" still expands inside double quotes, and the
// \t of "a\tb" is a backslash and a t rather than a tab. Empty text is quoted
// for a reason of its own, since no shell would touch it either way:
// (default: ) leaves the reader unable to tell an empty default from a column
// that failed to print.
//
// Everything else stands for itself.
//
// The form is a POSIX shell's. A command interpreter with rules of its own,
// cmd.exe among them, is not served by inventing a second spelling here.
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
// form. A byte that is not valid UTF-8 answers no along with the control
// characters: neither is something a reader could read off the line.
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
