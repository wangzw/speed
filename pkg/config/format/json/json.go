// Package json parses the JSON configuration format. It is written on the
// standard library alone, which is the whole point of keeping it apart from
// the YAML parser: a host that configures itself in JSON carries no parsing
// dependency at all.
//
// Importing the package is all a host does with it, because the parser travels
// as a resource of a module registered in init:
//
//	import _ "github.com/vislake/speed/pkg/config/format/json"
package json

import (
	"bytes"
	stdjson "encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/vislake/speed/pkg/config"
	"github.com/vislake/speed/pkg/core"
)

// moduleName is the name this parser registers under. Transports and formats
// are named after where they live, so a registry listing says what a module is
// without anybody opening it.
const moduleName = "config.format.json"

// init registers the module with the process registry, which is what makes a
// blank import of this package enough to have JSON config sources parsed.
func init() { core.ProcessRegistry.Register(Module()) }

// Module is this module's descriptor, exported for registries that do not
// inherit the process-level registrations, such as the ones tests build with
// core.New.
//
// It holds a resource and nothing else: a parser is stateless, has no
// lifecycle and delivers no capability. The configuration module collects it
// with core.Resources and never imports this package.
func Module() core.Module {
	return core.Module{Name: moduleName, Resources: []any{format{}}}
}

// Compile-time proof that this parser satisfies the extension point it is
// collected by: the resource is stored as any, so nothing else would catch a
// signature that drifted from the interface.
var _ config.Format = format{}

// format is the parser itself. It holds no state: every call is answered out
// of the data it is given.
type format struct{}

// Name reports the format name this parser is selected by. The name is matched
// exactly, so it is the same string a file source derives from the .json
// extension.
func (format) Name() string { return "json" }

// Unmarshal decodes a JSON document into a tree of string-keyed maps. JSON has
// string keys by construction, so the rule against non-string keys costs this
// parser nothing. A number written as an integer arrives as one, with every
// digit it was written with; any other number arrives as a float64, which the
// conversion layer writes into an integer field when it is whole.
//
// An object that gives one key twice is refused. The standard library keeps
// the last of them, so a second section under one key would drop every setting
// of the first that it does not repeat, and nothing would say so. Data after
// the document is refused for the same reason. The document is therefore read
// token by token rather than decoded in one call, which is the one way the
// standard library leaves the keys of an object in view.
//
// A document holding null is an empty configuration rather than a failure: a
// source that carries no settings is a legal way to run, and the layers below
// it decide the values.
func (format) Unmarshal(data []byte) (map[string]any, error) {
	w := &walker{data: data, dec: stdjson.NewDecoder(bytes.NewReader(data))}
	w.dec.UseNumber()
	document, err := w.value()
	if err != nil {
		if errors.Is(err, io.EOF) {
			return nil, fmt.Errorf("the data holds no JSON value: %w", io.ErrUnexpectedEOF)
		}
		return nil, err
	}
	switch _, err := w.dec.Token(); {
	case errors.Is(err, io.EOF):
	case err != nil:
		return nil, err
	default:
		return nil, fmt.Errorf("line %d: the data goes on after the document, and a config "+
			"source is one JSON value", w.line())
	}
	switch top := document.(type) {
	case map[string]any:
		return top, nil
	case nil:
		return map[string]any{}, nil
	default:
		return nil, fmt.Errorf("the document is %s at the top level, and a config source is a "+
			"mapping of keys to values, written {\"cache\": {\"ttl\": \"5m\"}}", describe(document))
	}
}

// walker reads one document off the decoder's token stream. The decoder holds
// the syntax to the standard; the walker builds the tree and judges the keys.
type walker struct {
	data []byte
	dec  *stdjson.Decoder
}

// value reads the value that starts at the next token.
func (w *walker) value() (any, error) {
	tok, err := w.dec.Token()
	if err != nil {
		return nil, err
	}
	switch tok {
	case stdjson.Delim('{'):
		return w.object()
	case stdjson.Delim('['):
		return w.array()
	}
	if n, isNumber := tok.(stdjson.Number); isNumber {
		return number(n)
	}
	// A closing delimiter where a value belongs is a syntax error the
	// decoder reports itself, so what arrives here is a scalar.
	return tok, nil
}

// number gives a number the Go value that holds what the document wrote.
//
// An integer literal, one written with neither a fraction nor an exponent, is
// held as an integer: an int when it fits one, which is how YAML resolves it
// too, and otherwise the 64-bit type that holds it. Held as a float64 it would
// be rounded to 53 bits before any field saw it, and a config file writing
// 9007199254740993 would run with 9007199254740992. Any other number, and an
// integer literal no 64-bit type holds, is a float64, which is all it can be.
func number(n stdjson.Number) (any, error) {
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

// object reads the members of an object whose opening brace has been read,
// refusing a key given twice.
func (w *walker) object() (map[string]any, error) {
	out := map[string]any{}
	lines := map[string]int{}
	for w.dec.More() {
		tok, err := w.dec.Token()
		if err != nil {
			return nil, err
		}
		// Inside an object the decoder yields a key as a string or fails.
		key, _ := tok.(string)
		if first, repeated := lines[key]; repeated {
			return nil, fmt.Errorf("line %d: the key %q is given twice in one object, first at "+
				"line %d. Only one of the two could take effect; give it once, with every "+
				"setting it is meant to carry", w.line(), key, first)
		}
		lines[key] = w.line()
		value, err := w.value()
		if err != nil {
			return nil, err
		}
		out[key] = value
	}
	if _, err := w.dec.Token(); err != nil {
		return nil, err
	}
	return out, nil
}

// array reads the elements of an array whose opening bracket has been read.
func (w *walker) array() ([]any, error) {
	out := []any{}
	for w.dec.More() {
		value, err := w.value()
		if err != nil {
			return nil, err
		}
		out = append(out, value)
	}
	if _, err := w.dec.Token(); err != nil {
		return nil, err
	}
	return out, nil
}

// line is the line the decoder has read up to, counted from one, which is the
// line of the token it returned last.
func (w *walker) line() int {
	return 1 + bytes.Count(w.data[:w.dec.InputOffset()], []byte("\n"))
}

// describe names what a value is in the words of the format, so the error says
// what was found rather than what Go called it.
func describe(value any) string {
	switch value.(type) {
	case []any:
		return "an array"
	case string:
		return "a string"
	case int, int64, uint64, float64:
		return "a number"
	case bool:
		return "a boolean"
	}
	return fmt.Sprintf("%T", value)
}
