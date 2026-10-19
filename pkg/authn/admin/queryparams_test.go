package admin

import (
	"encoding/json"
	"sort"
	"strings"
	"testing"
)

// TestTheDeclaredQueryParametersAreTheOnesTheInputsCarry holds the two facts a
// query-parameter contract is made of: the fragment's parameters, which a
// generated client sends, and the query-tagged fields of the declared inputs,
// which the entry point binds. A name that appears in one and not the other is
// either a parameter the process ignores or a read no client can drive.
//
// The check is package-scoped: the union of the module's declared query names
// against the union its inputs carry. The fragment is generated from the same
// declarations, so a drift here can only mean a hand-written fragment or an
// input the generator reads differently, and both are defects.
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

// declaredQueryParams returns the query-parameter names the embedded fragment
// declares, across every operation, ascending and unique.
func declaredQueryParams(t *testing.T) []string {
	t.Helper()

	var doc struct {
		Paths map[string]map[string]struct {
			Parameters []struct {
				Name string `json:"name"`
				In   string `json:"in"`
			} `json:"parameters"`
		} `json:"paths"`
	}
	if err := json.Unmarshal(adminFragment, &doc); err != nil {
		t.Fatalf("the embedded OpenAPI fragment is not a JSON object: %v", err)
	}

	names := map[string]bool{}
	for _, item := range doc.Paths {
		for _, op := range item {
			for _, p := range op.Parameters {
				if p.In == "query" {
					names[p.Name] = true
				}
			}
		}
	}
	return sortedUnique(names)
}

// inputQueryParams returns the query-parameter names the declared operations'
// inputs carry, ascending and unique.
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

// missingFrom returns the names of want that are absent from have, ascending.
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

// sortedUnique returns the set's members, ascending.
func sortedUnique(set map[string]bool) []string {
	out := make([]string, 0, len(set))
	for name := range set {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}
