package admin

import (
	"encoding/json"
	"sort"
	"strings"
	"testing"
)

// TestTheDeclaredQueryParametersAreTheOnesTheInputsCarry checks that the fragment's query parameters and the inputs' query-tagged fields agree.
func TestTheDeclaredQueryParametersAreTheOnesTheInputsCarry(t *testing.T) {
	declared := declaredQueryParams(t)
	carried := inputQueryParams()

	if len(declared) == 0 || len(carried) == 0 {
		t.Fatalf("the check would compare %d declared names against %d carried names; "+
			"one side being empty means the check is not reading what it thinks it is",
			len(declared), len(carried))
	}

	for _, name := range missingFrom(declared, carried) {
		t.Errorf("the fragment declares the query parameter %q, which no declared input carries. "+
			"A generated client would send it and the process would ignore it", name)
	}
	for _, name := range missingFrom(carried, declared) {
		t.Errorf("a declared input carries the query parameter %q, which the fragment does not declare. "+
			"A generated client has no way to send it", name)
	}
}

func declaredQueryParams(t *testing.T) []string {
	t.Helper()

	type document struct {
		Paths map[string]map[string]struct {
			Parameters []struct {
				Name string `json:"name"`
				In   string `json:"in"`
			} `json:"parameters"`
		} `json:"paths"`
	}

	names := map[string]bool{}
	for _, raw := range [][]byte{adminFragment, adminTenantsFragment} {
		var doc document
		if err := json.Unmarshal(raw, &doc); err != nil {
			t.Fatalf("the embedded OpenAPI fragment is not a JSON object: %v", err)
		}
		for _, item := range doc.Paths {
			for _, op := range item {
				for _, p := range op.Parameters {
					if p.In == "query" {
						names[p.Name] = true
					}
				}
			}
		}
	}
	return sortedUnique(names)
}

func inputQueryParams() []string {
	names := map[string]bool{}
	for _, declared := range operations {
		input := declared.Descriptor().Input
		if input == nil {
			continue
		}
		for i := 0; i < input.NumField(); i++ {
			field := input.Field(i)
			if field.PkgPath != "" {
				continue
			}
			name := strings.Split(field.Tag.Get("query"), ",")[0]
			if name != "" && name != "-" {
				names[name] = true
			}
		}
	}
	return sortedUnique(names)
}

func missingFrom(want, have []string) []string {
	set := make(map[string]bool, len(have))
	for _, name := range have {
		set[name] = true
	}
	var out []string
	for _, name := range want {
		if !set[name] {
			out = append(out, name)
		}
	}
	return out
}

func sortedUnique(set map[string]bool) []string {
	out := make([]string, 0, len(set))
	for name := range set {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}
