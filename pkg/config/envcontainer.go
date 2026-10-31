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
// A map's keys are its data and are taken as written; only the target's key
// type can make the shape impossible, and a key type other than string is
// refused because a JSON object names its members with strings.
//
// A failure names the variable and the item. A sensitive item's failure does
// not repeat the value: the text is a credential like any other, and the
// diagnostic goes to standard error.
func envContainer(item *manifestItem, text string, fv reflect.Value) error {
	if t := nonStringMapKey(fv.Type(), make(map[reflect.Type]bool)); t != nil {
		return envContainerFailure(item, fv,
			fmt.Errorf("%w: the target maps its keys to %s, and a JSON object names its members "+
				"with strings, so only a string-keyed map can take this value",
				ErrTypeMismatch, typeName(t.Key())))
	}
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

// nonStringMapKey returns the first map type reached from t whose keys are not
// strings, or nil when every map in the value is string-keyed. A JSON object
// names its members with strings, so a map with any other key type would have
// the decoder turn those names into keys the source never wrote.
//
// The walk follows the shapes a container can hold -- list elements, map
// values, struct fields -- and stops at a type that reads itself from text,
// which is a leaf rather than a section. seen keeps a cyclic element type from
// being walked without end, the same reason the decoder's key rewrite keeps a
// chain.
func nonStringMapKey(t reflect.Type, seen map[reflect.Type]bool) reflect.Type {
	if seen[t] {
		return nil
	}
	seen[t] = true
	switch t.Kind() {
	case reflect.Pointer, reflect.Slice, reflect.Array:
		return nonStringMapKey(t.Elem(), seen)
	case reflect.Map:
		if t.Key().Kind() != reflect.String {
			return t
		}
		return nonStringMapKey(t.Elem(), seen)
	case reflect.Struct:
		if decodesFromText(t) {
			return nil
		}
		for i := range t.NumField() {
			f := t.Field(i)
			if f.PkgPath != "" {
				continue
			}
			if _, _, skip := fieldName(f); skip {
				continue
			}
			if bad := nonStringMapKey(f.Type, seen); bad != nil {
				return bad
			}
		}
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

// parseEnvJSON reads a container variable's value. The text is read token by
// token rather than decoded in one call: the standard library keeps the last of
// two equal keys in an object and drops the first without a word, and an
// environment variable is written by hand, where a repeated key means the
// writer lost track rather than that the second is meant.
//
// Numbers keep the width they were written with, the way the JSON config format
// delivers them: decoding every one as a float64 would round an integer above
// 2^53 before any field saw it. A null is refused wherever it stands, because an
// environment variable that means nothing is better left unset than written as
// a value no container type has. Text that holds no value at all is refused: an
// empty variable already means the empty container, so whitespace alone is a
// mistake rather than a second spelling of it.
func parseEnvJSON(text string) (any, error) {
	dec := json.NewDecoder(strings.NewReader(text))
	dec.UseNumber()
	value, err := parseJSONValue(dec)
	if err != nil {
		if errors.Is(err, io.EOF) {
			return nil, errors.New("the value is empty or whitespace alone, and a container is " +
				"written as a JSON list or map")
		}
		return nil, err
	}
	if err := onlyWhitespace(dec); err != nil {
		return nil, err
	}
	return value, nil
}

// parseJSONValue reads the value that starts at the decoder's next token.
func parseJSONValue(dec *json.Decoder) (any, error) {
	token, err := dec.Token()
	if err != nil {
		return nil, err
	}
	switch value := token.(type) {
	case json.Delim:
		switch value {
		case '{':
			return parseJSONObject(dec)
		case '[':
			return parseJSONArray(dec)
		}
		return nil, fmt.Errorf("the value closes a container that was never opened")
	case json.Number:
		return jsonNumber(value)
	case nil:
		return nil, errors.New("the value is null, and a container is written as a JSON list or map")
	}
	return token, nil
}

// parseJSONObject reads the members of an object whose opening brace has been
// read, refusing a key given twice.
func parseJSONObject(dec *json.Decoder) (any, error) {
	object := make(map[string]any)
	for dec.More() {
		token, err := dec.Token()
		if err != nil {
			return nil, err
		}
		key, ok := token.(string)
		if !ok {
			return nil, fmt.Errorf("a JSON object names its members with strings, and the value "+
				"gave %T as a name", token)
		}
		if _, repeated := object[key]; repeated {
			return nil, fmt.Errorf("the key %q is given twice in one object, and only one of the "+
				"two could take effect", key)
		}
		value, err := parseJSONValue(dec)
		if err != nil {
			return nil, err
		}
		object[key] = value
	}
	if _, err := dec.Token(); err != nil {
		return nil, err
	}
	return object, nil
}

// parseJSONArray reads the elements of an array whose opening bracket has been
// read.
func parseJSONArray(dec *json.Decoder) (any, error) {
	array := make([]any, 0)
	for dec.More() {
		value, err := parseJSONValue(dec)
		if err != nil {
			return nil, err
		}
		array = append(array, value)
	}
	if _, err := dec.Token(); err != nil {
		return nil, err
	}
	return array, nil
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
