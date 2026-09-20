package config

import (
	"strconv"

	"github.com/spf13/pflag"
)

// boolNoOptDefVal is what a boolean argument written without a value means.
// Setting it is what tells the command-line library that the word after the
// argument is not its value.
const boolNoOptDefVal = "true"

// magicValueTypes are the type names the layering tool reads a changed
// argument by. It switches on the name the command-line Value reports and
// converts the text itself for the ones it recognises, swallowing whatever
// fails to convert, so a placeholder spelled as one of these would take the
// value away from this module's own conversion.
var magicValueTypes = map[string]bool{
	"int": true, "int8": true, "int16": true, "int32": true, "int64": true,
	"bool": true, "stringSlice": true, "stringArray": true, "boolSlice": true,
	"intSlice": true, "uintSlice": true, "float64Slice": true, "durationSlice": true,
	"stringToString": true, "stringToInt": true,
}

// newFlagValue chooses the command-line form of one leaf and the value its
// argument means when written without one.
//
// Every form starts out holding the rendered default, whichever form it is:
// the command-line library reads it back for the help output, so one renderer
// decides what every kind of item shows and which items show nothing. The
// text is not where the value of a run comes from - the layering tool holds
// each declared default in a layer of its own, and reads an argument only
// once it has been written - which is what lets the rendering be quoted for a
// reader rather than kept verbatim for a parser.
func newFlagValue(item *manifestItem) (pflag.Value, string) {
	if booleanLeaf(item.typ) {
		return &boolValue{text: flagDefaultString(item)}, boolNoOptDefVal
	}
	if item.kind == kindStringList {
		return &listValue{text: flagDefaultString(item), placeholder: placeholder(item)}, ""
	}
	return &rawValue{text: flagDefaultString(item), placeholder: placeholder(item)}, ""
}

// listValue is the command-line form of a list of strings. The library's own
// list argument names itself in the help output, which leaves the declared
// placeholder nowhere to go, so the form is this module's: the separator is
// the one every other layer uses, and a repeated argument accumulates onto
// what came before it.
//
// The text is handed on as the separated form rather than as a slice, which
// is what the layer below already reads: one rule turns separated text into a
// list, wherever the text came from.
type listValue struct {
	text        string
	placeholder string
	given       bool
}

var _ pflag.Value = (*listValue)(nil)

func (v *listValue) String() string { return v.text }

func (v *listValue) Set(s string) error {
	if v.given {
		v.text += listSeparator + s
		return nil
	}
	v.text, v.given = s, true
	return nil
}

func (v *listValue) Type() string { return v.placeholder }

// rawValue holds the text of an argument and converts nothing. Conversion
// belongs to Decode, where the target type is in hand and one rule set decides
// every layer; taking it here would give ErrTypeMismatch a second origin with
// rules of its own.
type rawValue struct {
	text        string
	placeholder string
}

var _ pflag.Value = (*rawValue)(nil)

func (v *rawValue) String() string     { return v.text }
func (v *rawValue) Set(s string) error { v.text = s; return nil }

// Type is the value placeholder the help output shows. The command-line
// library prints the type name beside the argument, which is what makes it
// the declared placeholder's seat.
func (v *rawValue) Type() string { return v.placeholder }

// boolValue is the form of a leaf whose argument may be written without a
// value. It checks the text it is given rather than leaving it to the
// layering tool, which would read --verbose=maybe as false and say nothing.
type boolValue struct {
	text string
}

var _ pflag.Value = (*boolValue)(nil)

func (v *boolValue) String() string { return v.text }

func (v *boolValue) Set(s string) error {
	parsed, err := strconv.ParseBool(s)
	if err != nil {
		return errBoolValue{text: s}
	}
	v.text = strconv.FormatBool(parsed)
	return nil
}

// Type is the name the layering tool reads a boolean argument by, and the name
// the help output leaves out: a boolean takes no placeholder, since its
// argument may be written without a value.
func (v *boolValue) Type() string { return "bool" }

// errBoolValue is what a boolean argument reports for text that is not one.
// The command-line library wraps it, and the wrapper is what this module
// classifies; the text states the accepted spelling rather than repeating the
// standard library's parse error.
type errBoolValue struct{ text string }

func (e errBoolValue) Error() string {
	return strconv.Quote(e.text) + " is not a boolean: write true or false"
}
