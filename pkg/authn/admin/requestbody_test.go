package admin

import (
	"encoding/json"
	"reflect"
	"sort"
	"strings"
	"testing"
)

// TestTheDeclaredRequestBodiesAreTheOnesTheInputsCarry checks that the fragment's request bodies and the inputs' body-tagged fields agree.
func TestTheDeclaredRequestBodiesAreTheOnesTheInputsCarry(t *testing.T) {
	fragment := fragmentBodies(t)
	compared := 0
	withBody := 0
	for _, declared := range operations {
		operation := declared.Descriptor()
		fields := jsonFieldNames(operation.Input)
		published, declaredBody := fragment[operationKey{operation.Method, ginPath(operation.Path)}]

		switch {
		case operation.Request != nil:
			withBody++
			if !declaredBody {
				t.Errorf("%s %s declares a request body, but the fragment publishes none",
					operation.Method, operation.Path)
				continue
			}
			compared++
			if !sameStringSet(fields, published) {
				t.Errorf("%s %s declares body fields %v but the fragment's schema has %v",
					operation.Method, operation.Path, sortedSet(fields), sortedSet(published))
			}
		case len(fields) > 0:
			t.Errorf("%s %s carries body-tagged fields %v but declares no request media type",
				operation.Method, operation.Path, sortedSet(fields))
		case declaredBody:
			t.Errorf("%s %s declares no request body, but the fragment publishes one",
				operation.Method, operation.Path)
		}
	}
	if compared == 0 || withBody == 0 {
		t.Fatalf("the check compared %d bodies across %d body-carrying operations; "+
			"either side being empty means the check is not reading what it thinks it is", compared, withBody)
	}
}

type operationKey struct {
	method string
	path   string
}

func ginPath(path string) string {
	path = strings.ReplaceAll(path, "{", ":")
	return strings.ReplaceAll(path, "}", "")
}

func fragmentBodies(t *testing.T) map[operationKey]map[string]bool {
	t.Helper()

	type document struct {
		Paths map[string]map[string]struct {
			RequestBody *struct {
				Content map[string]struct {
					Schema struct {
						Ref        string                     `json:"$ref"`
						Properties map[string]json.RawMessage `json:"properties"`
					} `json:"schema"`
				} `json:"content"`
			} `json:"requestBody"`
		} `json:"paths"`
		Components struct {
			Schemas map[string]struct {
				Properties map[string]json.RawMessage `json:"properties"`
			} `json:"schemas"`
		} `json:"components"`
	}

	out := map[operationKey]map[string]bool{}
	for _, raw := range [][]byte{adminFragment, adminTenantsFragment} {
		var doc document
		if err := json.Unmarshal(raw, &doc); err != nil {
			t.Fatalf("an embedded OpenAPI fragment is not a JSON object: %v", err)
		}
		for path, item := range doc.Paths {
			for method, op := range item {
				if method == "parameters" {
					continue
				}
				key := operationKey{strings.ToUpper(method), ginPath(path)}
				if op.RequestBody == nil {
					continue
				}
				properties := map[string]json.RawMessage{}
				for _, media := range op.RequestBody.Content {
					properties = media.Schema.Properties
					if ref := media.Schema.Ref; ref != "" {
						name := ref[strings.LastIndex(ref, "/")+1:]
						schema, ok := doc.Components.Schemas[name]
						if !ok {
							t.Fatalf("the requestBody of %s %s references the missing schema %q", method, path, name)
						}
						properties = schema.Properties
					}
				}
				names := map[string]bool{}
				for name := range properties {
					names[name] = true
				}
				out[key] = names
			}
		}
	}
	return out
}

func jsonFieldNames(input reflect.Type) map[string]bool {
	names := map[string]bool{}
	if input == nil {
		return names
	}
	for i := 0; i < input.NumField(); i++ {
		field := input.Field(i)
		if field.PkgPath != "" {
			continue
		}
		name := strings.Split(field.Tag.Get("json"), ",")[0]
		if name != "" && name != "-" {
			names[name] = true
		}
	}
	return names
}

func sameStringSet(a, b map[string]bool) bool {
	if len(a) != len(b) {
		return false
	}
	for name := range a {
		if !b[name] {
			return false
		}
	}
	return true
}

func sortedSet(set map[string]bool) []string {
	out := make([]string, 0, len(set))
	for name := range set {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}
