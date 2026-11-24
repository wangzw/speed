// Package json parses JSON configuration documents with the standard library alone.
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

// ModuleName is the name this parser registers under.
const ModuleName = "config.format.json"

func init() { core.ProcessRegistry.Register(Module()) }

// Module is this module's descriptor, for registries that do not inherit the
// process-level registrations. It holds a resource and nothing else.
func Module() core.Module {
	return core.Module{Name: ModuleName, Resources: []any{format{}}}
}

var _ config.Format = format{}

type format struct{}

func (format) Name() string { return "json" }

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

type walker struct {
	data []byte
	dec  *stdjson.Decoder
}

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
	return tok, nil
}

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

func (w *walker) object() (map[string]any, error) {
	out := map[string]any{}
	lines := map[string]int{}
	for w.dec.More() {
		tok, err := w.dec.Token()
		if err != nil {
			return nil, err
		}
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

func (w *walker) line() int {
	return 1 + bytes.Count(w.data[:w.dec.InputOffset()], []byte("\n"))
}

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
