package admin

import (
	"encoding/json"
	"reflect"
	"sort"
	"strings"
	"testing"
)

// TestTheDeclaredRequestBodiesAreTheOnesTheInputsCarry holds the request-body
// half of the contract: the fragment's requestBody, which a generated client
// sends, and the body-tagged fields of the declared input, which the entry point
// binds. Presence and shape are both checked in both directions: an operation
// whose declaration carries no body must have an input with no body fields, and
// one that declares a body must publish exactly the JSON fields its input holds.
//
// The fragment is generated from the declarations, so a drift here means a
// hand-written fragment or an input the generator reads differently.
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

// operationKey addresses one operation the fragment declares.
type operationKey struct {
	method string
	path   string
}

// ginPath rewrites an OpenAPI path's parameters into the form the declarations
// write, so the two are comparable.
func ginPath(path string) string {
	path = strings.ReplaceAll(path, "{", ":")
	return strings.ReplaceAll(path, "}", "")
}

// fragmentBodies returns, for every operation the embedded fragment declares, the
// property set of its requestBody's JSON schema, and whether it carries one at
// all. A schema referenced by $ref is resolved against the fragment's components.
func fragmentBodies(t *testing.T) map[operationKey]map[string]bool {
	t.Helper()

	var doc struct {
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
	if err := json.Unmarshal(adminFragment, &doc); err != nil {
		t.Fatalf("the embedded OpenAPI fragment is not a JSON object: %v", err)
	}

	out := map[operationKey]map[string]bool{}
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
	return out
}

// jsonFieldNames returns the JSON member names an input type binds, or an empty
// set for an input with no body fields.
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

// sameStringSet reports whether two sets hold the same members.
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

// sortedSet returns the set's members, ascending, for a failure message.
func sortedSet(set map[string]bool) []string {
	out := make([]string, 0, len(set))
	for name := range set {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}
