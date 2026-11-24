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

func emptyEnvContainer(item *manifestItem, fv reflect.Value) error {
	switch fv.Kind() {
	case reflect.Map:
		fv.Set(reflect.MakeMapWithSize(fv.Type(), 0))
		return nil
	case reflect.Slice:
		fv.Set(reflect.MakeSlice(fv.Type(), 0, 0))
		return nil
	case reflect.Array:
		fv.Set(reflect.Zero(fv.Type()))
		return nil
	}
	return fmt.Errorf("%w: environment variable %s is empty, and %q is %s. An empty variable "+
		"means the empty list or map, and a struct has no empty form: write its members as JSON",
		ErrTypeMismatch, item.envName, item.path, typeName(fv.Type()))
}

func envContainerFailure(item *manifestItem, fv reflect.Value, err error) error {
	if item.item.Sensitive {
		return fmt.Errorf("%w: environment variable %s gives the value of %q, and it does not "+
			"decode to %s. The value is not repeated here, since the item is sensitive",
			ErrTypeMismatch, item.envName, item.path, typeName(fv.Type()))
	}
	return fmt.Errorf("environment variable %s gives the value of %q: %w",
		item.envName, item.path, err)
}

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

func onlyWhitespace(dec *json.Decoder) error {
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		if err != nil {
			return err
		}
		return errors.New("the data goes on after the JSON value")
	}
	return nil
}

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
