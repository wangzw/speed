package config

import (
	"strconv"

	"github.com/spf13/pflag"
)

const boolNoOptDefVal = "true"

// magicValueTypes are the type names the layering tool switches on to read a changed argument.
var magicValueTypes = map[string]bool{
	"int": true, "int8": true, "int16": true, "int32": true, "int64": true,
	"bool": true, "stringSlice": true, "stringArray": true, "boolSlice": true,
	"intSlice": true, "uintSlice": true, "float64Slice": true, "durationSlice": true,
	"stringToString": true, "stringToInt": true,
}

func newFlagValue(item *manifestItem) (pflag.Value, string) {
	if booleanLeaf(item.typ) {
		return &boolValue{text: flagDefaultString(item)}, boolNoOptDefVal
	}
	if item.kind == kindStringList {
		return &listValue{text: flagDefaultString(item), placeholder: placeholder(item)}, ""
	}
	return &rawValue{text: flagDefaultString(item), placeholder: placeholder(item)}, ""
}

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

type rawValue struct {
	text        string
	placeholder string
}

var _ pflag.Value = (*rawValue)(nil)

func (v *rawValue) String() string     { return v.text }
func (v *rawValue) Set(s string) error { v.text = s; return nil }

func (v *rawValue) Type() string { return v.placeholder }

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

type errBoolValue struct{ text string }

func (e errBoolValue) Error() string {
	return strconv.Quote(e.text) + " is not a boolean: write true or false"
}
