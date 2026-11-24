// Package yaml parses YAML configuration documents; it is the one place in the configuration module that carries a third-party parser.
package yaml

import (
	"bytes"
	"errors"
	"fmt"
	"io"

	yamlv3 "go.yaml.in/yaml/v3"

	"github.com/vislake/speed/pkg/config"
	"github.com/vislake/speed/pkg/core"
)

// ModuleName is the name this parser registers under.
const ModuleName = "config.format.yaml"

const (
	stringTag = "!!str"
	mergeTag  = "!!merge"
)

func init() { core.ProcessRegistry.Register(Module()) }

// Module is this module's descriptor, for registries that do not inherit the
// process-level registrations. It holds a resource and nothing else.
func Module() core.Module {
	return core.Module{Name: ModuleName, Resources: []any{format{}}}
}

var _ config.Format = format{}

type format struct{}

func (format) Name() string { return "yaml" }

func (format) Unmarshal(data []byte) (map[string]any, error) {
	decoder := yamlv3.NewDecoder(bytes.NewReader(data))

	var document yamlv3.Node
	switch err := decoder.Decode(&document); {
	case errors.Is(err, io.EOF):
		return map[string]any{}, nil
	case err != nil:
		return nil, err
	}
	if err := refuseSecondDocument(decoder); err != nil {
		return nil, err
	}

	top := &document
	if top.Kind == yamlv3.DocumentNode {
		if len(top.Content) == 0 {
			return map[string]any{}, nil
		}
		top = top.Content[0]
	}

	value, err := newConverter().value(top)
	if err != nil {
		return nil, err
	}
	switch root := value.(type) {
	case map[string]any:
		return root, nil
	case nil:
		return map[string]any{}, nil
	default:
		return nil, fmt.Errorf("line %d: the document is %s at the top level, and a config "+
			"source is a mapping of keys to values, with a key such as cache: carrying the "+
			"settings beneath it", top.Line, describe(value))
	}
}

func refuseSecondDocument(decoder *yamlv3.Decoder) error {
	var extra yamlv3.Node
	switch err := decoder.Decode(&extra); {
	case errors.Is(err, io.EOF):
		return nil
	case err != nil:
		return err
	}
	return fmt.Errorf("line %d: the data holds a second YAML document, and a config source is "+
		"one document. Remove the --- separator and merge the settings into one", extra.Line)
}

// converter walks the document tree while remembering the current path, because an alias may point back into it.
type converter struct {
	onPath map[*yamlv3.Node]bool
}

func newConverter() *converter {
	return &converter{onPath: map[*yamlv3.Node]bool{}}
}

func (c *converter) value(node *yamlv3.Node) (any, error) {
	if c.onPath[node] {
		return nil, fmt.Errorf("line %d: an anchor holds itself through an alias, and a value "+
			"that contains itself cannot be written out", node.Line)
	}
	c.onPath[node] = true
	defer delete(c.onPath, node)

	switch node.Kind {
	case yamlv3.AliasNode:
		target, err := resolveAlias(node)
		if err != nil {
			return nil, err
		}
		return c.value(target)
	case yamlv3.MappingNode:
		return c.mapping(node)
	case yamlv3.SequenceNode:
		items := make([]any, 0, len(node.Content))
		for _, item := range node.Content {
			converted, err := c.value(item)
			if err != nil {
				return nil, err
			}
			items = append(items, converted)
		}
		return items, nil
	case yamlv3.ScalarNode:
		var scalar any
		if err := node.Decode(&scalar); err != nil {
			return nil, fmt.Errorf("line %d: %w", node.Line, err)
		}
		return scalar, nil
	}
	return nil, nil
}

func (c *converter) mapping(node *yamlv3.Node) (map[string]any, error) {
	out := make(map[string]any, len(node.Content)/2)
	lines := make(map[string]int, len(node.Content)/2)
	for i := 0; i+1 < len(node.Content); i += 2 {
		key, err := stringKey(node.Content[i])
		if err != nil {
			return nil, err
		}
		if first, repeated := lines[key]; repeated {
			return nil, fmt.Errorf("line %d: the key %q is given twice in one mapping, first at "+
				"line %d. Only one of the two could take effect; give it once, with every "+
				"setting it is meant to carry", node.Content[i].Line, key, first)
		}
		lines[key] = node.Content[i].Line
		value, err := c.value(node.Content[i+1])
		if err != nil {
			return nil, err
		}
		out[key] = value
	}
	return out, nil
}

func stringKey(node *yamlv3.Node) (string, error) {
	resolved := node
	seen := map[*yamlv3.Node]bool{}
	for resolved.Kind == yamlv3.AliasNode {
		if seen[resolved] {
			return "", fmt.Errorf("line %d: the alias this key is written as holds itself", resolved.Line)
		}
		seen[resolved] = true
		target, err := resolveAlias(resolved)
		if err != nil {
			return "", err
		}
		resolved = target
	}
	if resolved.Tag == mergeTag {
		return "", fmt.Errorf("line %d: the merge key << is not a string key and is not "+
			"supported. Write the keys it would have merged out in full", resolved.Line)
	}
	if resolved.Kind != yamlv3.ScalarNode || resolved.Tag != stringTag {
		return "", fmt.Errorf("line %d: the key %s is %s, and a config key is a string. Quote "+
			"it if a string is what it is meant to be", resolved.Line, keyText(resolved), describeTag(resolved))
	}
	return resolved.Value, nil
}

func resolveAlias(node *yamlv3.Node) (*yamlv3.Node, error) {
	if node.Alias == nil {
		return nil, fmt.Errorf("line %d: the alias *%s refers to an anchor that is not defined",
			node.Line, node.Value)
	}
	return node.Alias, nil
}

func keyText(node *yamlv3.Node) string {
	if node.Kind == yamlv3.ScalarNode {
		return fmt.Sprintf("%q", node.Value)
	}
	return "written here"
}

func describeTag(node *yamlv3.Node) string {
	switch node.Kind {
	case yamlv3.MappingNode:
		return "a mapping"
	case yamlv3.SequenceNode:
		return "a sequence"
	}
	if node.Tag == "" {
		return "not a string"
	}
	return "of type " + node.Tag
}

func describe(value any) string {
	switch value.(type) {
	case []any:
		return "a sequence"
	case string:
		return "a string"
	case bool:
		return "a boolean"
	case int, int64, uint64, float64:
		return "a number"
	}
	return fmt.Sprintf("%T", value)
}
