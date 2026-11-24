package config

import (
	"strconv"

	"github.com/spf13/pflag"
)

// boolNoOptDefVal is what a boolean argument written without a value means.
const boolNoOptDefVal = "true"

// magicValueTypes are the type names the layering tool reads a changed argument
// by; a placeholder spelled as one of these would take the value away from this
// module's own conversion.
var magicValueTypes = map[string]bool{
	"int": true, "int8": true, "int16": true, "int32": true, "int64": true,
	"bool": true, "stringSlice": true, "stringArray": true, "boolSlice": true,
	"intSlice": true, "uintSlice": true, "float64Slice": true, "durationSlice": true,
	"stringToString": true, "stringToInt": true,
}

// newFlagValue chooses the command-line form of one leaf and the value its
// argument means when written without one. Every form starts out holding the
// rendered default for the help output.
func newFlagValue(item *manifestItem) (pflag.Value, string) {
	if booleanLeaf(item.typ) {
		return &boolValue{text: flagDefaultString(item)}, boolNoOptDefVal
	}
	if item.kind == kindStringList {
		return &listValue{text: flagDefaultString(item), placeholder: placeholder(item)}, ""
	}
	return &rawValue{text: flagDefaultString(item), placeholder: placeholder(item)}, ""
}

// listValue is the command-line form of a list of strings: a repeated argument
// accumulates onto what came before it, separated by listSeparator.
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

// rawValue holds the text of an argument and converts nothing; conversion
// belongs to Decode.
type rawValue struct {
	text        string
	placeholder string
}

var _ pflag.Value = (*rawValue)(nil)

func (v *rawValue) String() string     { return v.text }
func (v *rawValue) Set(s string) error { v.text = s; return nil }

func (v *rawValue) Type() string { return v.placeholder }

// boolValue is the form of a leaf whose argument may be written without a
// value. It checks the text it is given rather than leaving it to the layering
// tool, which would read --verbose=maybe as false and say nothing.
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

func (v *boolValue) Type() string { return "bool" }

// errBoolValue is what a boolean argument reports for text that is not one.
type errBoolValue struct{ text string }

func (e errBoolValue) Error() string {
	return strconv.Quote(e.text) + " is not a boolean: write true or false"
}
