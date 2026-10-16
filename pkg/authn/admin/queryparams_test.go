package admin

import (
	"encoding/json"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// TestTheDeclaredQueryParametersAreTheOnesTheHandlersRead holds the query
// half of the fragment/registration contract. The route-level check
// (openapi_test.go, pkg/internal/routecontract) compares the operations a
// fragment declares against the routes the module mounts; it reads methods
// and paths only, so a route that declares one query-parameter name and reads
// another is served and documented and still answers the wrong rows -- the
// failure a generated client never sees.
//
// The two facts are separately maintained: the fragment's query parameters
// (what a generated client sends) and the names the handlers read with
// gin.Context.Query / QueryArray (what the process honors). This check reads
// both from their sources -- the embedded fragment, and the package's own
// compiled sources through the AST -- and reports every name appearing in one
// and not the other, in either direction.
//
// It is package-scoped: the union of the module's declared query names against
// the union of the names its handlers read. That is the granularity the drift
// has taken here (the account list's identifier filter, the account, member and
// impersonation cursors), and it is what keeps a fragment whose parameter is
// misspelled against the read from passing.
func TestTheDeclaredQueryParametersAreTheOnesTheHandlersRead(t *testing.T) {
	declared := declaredQueryParams(t)
	read := handlerQueryParams(t)

	if len(declared) == 0 || len(read) == 0 {
		t.Fatalf("the check would compare %d declared names against %d read names; "+
			"one side being empty means the check is not reading what it thinks it is",
			len(declared), len(read))
	}

	declaredOnly := missingFrom(declared, read)
	readOnly := missingFrom(read, declared)

	for _, name := range declaredOnly {
		t.Errorf("the fragment declares the query parameter %q, which no handler reads. "+
			"A generated client would send it and the process would ignore it, so the request "+
			"answers an unfiltered or unpaged result under a name that looks honored", name)
	}
	for _, name := range readOnly {
		t.Errorf("a handler reads the query parameter %q, which the fragment does not declare. "+
			"A generated client has no way to send it, so the read it drives is unreachable", name)
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

// handlerQueryParams returns the query-parameter names the package's handlers
// read, parsed from the package's compiled sources, ascending and unique. A
// name is a string literal passed to gin.Context.Query or QueryArray; the
// receiver is not checked, because the package reads no other Query method.
func handlerQueryParams(t *testing.T) []string {
	t.Helper()

	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("reading the package directory: %v", err)
	}

	names := map[string]bool{}
	fset := token.NewFileSet()
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(fset, name, nil, 0)
		if err != nil {
			t.Fatalf("parsing %s: %v", name, err)
		}
		ast.Inspect(file, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok || (sel.Sel.Name != "Query" && sel.Sel.Name != "QueryArray") {
				return true
			}
			if len(call.Args) == 0 {
				return true
			}
			lit, ok := call.Args[0].(*ast.BasicLit)
			if !ok || lit.Kind != token.STRING {
				return true
			}
			if value, err := strconv.Unquote(lit.Value); err == nil {
				names[value] = true
			}
			return true
		})
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
