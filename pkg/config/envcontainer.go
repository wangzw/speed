package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"reflect"
	"strconv"
	"strings"
)

// envContainer writes the JSON text an environment variable gave into a
// container leaf's field.
//
// The value is replaced as a whole: the variable is the container, not a patch
// on one, so neither the prototype's value nor the primary source's survives.
// That is the same rule the primary source and the host defaults document
// follow, and it is why a container's keys can stay out of the manifest.
//
// The decoding is strict. A member name no field answers for is an error rather
// than a value dropped, and a name is matched exactly: no case folding and no
// separator rewriting. A container written by hand in a variable has no
// unknown-key pass behind it, so a misspelling has to be visible at startup
// instead of leaving a field at its zero value. The names themselves are the
// ones the container's own decoder uses -- the config tag, the json tag, or the
// lower-case kebab form of the field name.
//
// A failure names the variable and the item. A sensitive item's failure does
// not repeat the value: the text is a credential like any other, and the
// diagnostic goes to standard error.
func envContainer(item *manifestItem, text string, fv reflect.Value) error {
	if text == "" {
		return emptyEnvContainer(item, fv)
	}
	value, err := parseEnvJSON(text)
	if err != nil {
		return envContainerFailure(item, fv,
			fmt.Errorf("%w: the value is not a JSON container: %w", ErrTypeMismatch, err))
	}
	if err := decodeIntoStrict(item.path, value, fv); err != nil {
		return envContainerFailure(item, fv, err)
	}
	return nil
}

// emptyEnvContainer gives the value an empty variable means for a container.
// A list or a map has one: the container with no entries, which is what the
// same text means for a string list. A struct has none -- there is no empty
// form to read out of nothing -- so it is refused rather than silently zeroed.
func emptyEnvContainer(item *manifestItem, fv reflect.Value) error {
	switch fv.Kind() {
	case reflect.Map:
		fv.Set(reflect.MakeMapWithSize(fv.Type(), 0))
		return nil
	case reflect.Slice:
		fv.Set(reflect.MakeSlice(fv.Type(), 0, 0))
		return nil
	case reflect.Array:
		// A fixed-size array has no length to empty; every element takes its
		// zero value, which is what decoding an empty JSON array into it does.
		fv.Set(reflect.Zero(fv.Type()))
		return nil
	}
	return fmt.Errorf("%w: environment variable %s is empty, and %q is %s. An empty variable "+
		"means the empty list or map, and a struct has no empty form: write its members as JSON",
		ErrTypeMismatch, item.envName, item.path, typeName(fv.Type()))
}

// envContainerFailure wraps a failed decode, naming the variable and the item.
// A sensitive item's failure states the type alone: the parser and the decoder
// both quote what they were given, and that text must not reach the diagnostic.
func envContainerFailure(item *manifestItem, fv reflect.Value, err error) error {
	if item.item.Sensitive {
		return fmt.Errorf("%w: environment variable %s gives the value of %q, and it does not "+
			"decode to %s. The value is not repeated here, since the item is sensitive",
			ErrTypeMismatch, item.envName, item.path, typeName(fv.Type()))
	}
	return fmt.Errorf("environment variable %s gives the value of %q: %w",
		item.envName, item.path, err)
}

// parseEnvJSON reads a container variable's value. Numbers keep the width they
// were written with, the way the JSON config format delivers them: decoding
// every one as a float64 would round an integer above 2^53 before any field
// saw it. A bare null is refused, because an environment variable that means
// nothing is better left unset than written as a value no container type has.
func parseEnvJSON(text string) (any, error) {
	dec := json.NewDecoder(strings.NewReader(text))
	dec.UseNumber()
	var value any
	if err := dec.Decode(&value); err != nil {
		return nil, err
	}
	if err := onlyWhitespace(dec); err != nil {
		return nil, err
	}
	if value == nil {
		return nil, errors.New("the value is null, and a container is written as a JSON list or map")
	}
	return jsonValue(value)
}

// onlyWhitespace reports whether the decoder has reached the end of the data.
func onlyWhitespace(dec *json.Decoder) error {
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		if err != nil {
			return err
		}
		return errors.New("the data goes on after the JSON value")
	}
	return nil
}

// jsonValue converts the numbers a json.Decoder with UseNumber produced into
// the Go numbers the conversion layer expects, and leaves every other value as
// the decoder gave it.
func jsonValue(v any) (any, error) {
	switch value := v.(type) {
	case json.Number:
		return jsonNumber(value)
	case map[string]any:
		for key, inner := range value {
			converted, err := jsonValue(inner)
			if err != nil {
				return nil, err
			}
			value[key] = converted
		}
		return value, nil
	case []any:
		for i, inner := range value {
			converted, err := jsonValue(inner)
			if err != nil {
				return nil, err
			}
			value[i] = converted
		}
		return value, nil
	}
	return v, nil
}

// jsonNumber gives a JSON number the Go value that holds what it was written
// with: an int when the literal fits one, the 64-bit type holding it otherwise,
// and a float64 only when the literal has a fraction or an exponent or no
// 64-bit integer holds it. The config format's JSON parser follows the same
// rule, so one value reads the same from either source.
func jsonNumber(n json.Number) (any, error) {
	text := n.String()
	if !strings.ContainsAny(text, ".eE") {
		if i, err := strconv.ParseInt(text, 10, 64); err == nil {
			if i == int64(int(i)) {
				return int(i), nil
			}
			return i, nil
		}
		if u, err := strconv.ParseUint(text, 10, 64); err == nil {
			return u, nil
		}
	}
	return n.Float64()
}
