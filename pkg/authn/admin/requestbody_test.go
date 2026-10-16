package admin

import (
	"encoding/json"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"strconv"
	"strings"
	"testing"
)

// TestTheDeclaredRequestBodiesAreTheOnesTheHandlersDecode holds the request-body
// half of the fragment/registration contract. The route-level check
// (openapi_test.go, pkg/internal/routecontract) compares the operations a
// fragment declares against the routes the module mounts; it reads methods and
// paths only, so a write whose handler decodes a JSON body off a route the
// fragment declares without a requestBody is served and documented and still
// answers a body the client never sends -- a write unreachable from a generated
// client, the failure this check exists to catch.
//
// The two facts are separately maintained: the fragment's requestBody (what a
// generated client sends) and whether the handler behind a route decodes a body
// with mount.Decode (what the process reads). This check reads both from their
// sources -- the embedded fragment, and the package's own compiled sources
// through the AST -- pairs each registered route with its declared operation by
// method and path, and reports every route whose handler decodes a body but
// whose declared operation carries no requestBody.
//
// The pairing is by path suffix: the fragment writes the endpoint-prefixed path
// ("/auth/admin/accounts") and the registrations write the module-relative one
// ("/admin/accounts"), and each module-relative path is a unique suffix of its
// fragment path. A parameter is "{name}" in the fragment and ":name" in the
// registration, so the fragment path is rewritten before the comparison.
func TestTheDeclaredRequestBodiesAreTheOnesTheHandlersDecode(t *testing.T) {
	decoding := handlersThatDecode(t)
	registered := registeredRoutes(t)
	operations := declaredOperationsWithBody(t)

	if len(decoding) == 0 {
		t.Fatalf("no handler is seen to decode a request body; the check is not reading what it thinks it is")
	}
	if len(operations) == 0 {
		t.Fatalf("the fragment declares no requestBody on any operation; " +
			"the check is not reading what it thinks it is")
	}

	for _, route := range registered {
		if !decoding[route.handler] {
			continue
		}
		operation, found := findDeclaredOperation(operations, route.method, route.path)
		if !found {
			t.Errorf("the module registers %s %s (its handler %s decodes a request body), "+
				"and the fragment declares no such operation", route.method, route.path, route.handler)
			continue
		}
		if !operation.hasBody {
			t.Errorf("the handler %s for %s %s decodes a request body with mount.Decode, but the "+
				"fragment declares no requestBody for that operation. A generated client sends no "+
				"body, so the write is unreachable from a client built on the contract",
				route.handler, route.method, route.path)
		}
	}
}

// registeredRoute is one route the module mounts: the method and module-relative
// path it is registered at, and the handler method's name.
type registeredRoute struct {
	method  string
	path    string
	handler string
}

// declaredOperation is one operation the fragment declares: the method and path
// a generated client addresses, and whether it carries a requestBody.
type declaredOperation struct {
	method  string
	path    string // the fragment path, "{name}" already rewritten to ":name"
	hasBody bool
}

// registeredRoutes returns every route the package registers, parsed from the
// package's compiled sources. A registration is a call with three arguments --
// an http.Method selector, a path string literal, and a handler selector -- and
// the handler is the selector's name.
func registeredRoutes(t *testing.T) []registeredRoute {
	t.Helper()

	entries := packageGoFiles(t)
	var out []registeredRoute
	fset := token.NewFileSet()
	for _, name := range entries {
		file, err := parser.ParseFile(fset, name, nil, 0)
		if err != nil {
			t.Fatalf("parsing %s: %v", name, err)
		}
		ast.Inspect(file, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok || len(call.Args) != 3 {
				return true
			}
			methodSel, ok := call.Args[0].(*ast.SelectorExpr)
			if !ok {
				return true
			}
			httpIdent, ok := methodSel.X.(*ast.Ident)
			if !ok || httpIdent.Name != "http" || !strings.HasPrefix(methodSel.Sel.Name, "Method") {
				return true
			}
			pathLit, ok := call.Args[1].(*ast.BasicLit)
			if !ok || pathLit.Kind != token.STRING {
				return true
			}
			path, err := strconv.Unquote(pathLit.Value)
			if err != nil {
				return true
			}
			handlerSel, ok := call.Args[2].(*ast.SelectorExpr)
			if !ok {
				return true
			}
			out = append(out, registeredRoute{
				method:  strings.ToUpper(strings.TrimPrefix(methodSel.Sel.Name, "Method")),
				path:    path,
				handler: handlerSel.Sel.Name,
			})
			return true
		})
	}
	return out
}

// handlersThatDecode returns the names of the package's handler methods whose
// body decodes a JSON request with mount.Decode, parsed from the package's
// compiled sources.
func handlersThatDecode(t *testing.T) map[string]bool {
	t.Helper()

	entries := packageGoFiles(t)
	out := map[string]bool{}
	fset := token.NewFileSet()
	for _, name := range entries {
		file, err := parser.ParseFile(fset, name, nil, 0)
		if err != nil {
			t.Fatalf("parsing %s: %v", name, err)
		}
		for _, decl := range file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Body == nil {
				continue
			}
			if callsMountDecode(fn.Body) {
				out[fn.Name.Name] = true
			}
		}
	}
	return out
}

// callsMountDecode reports whether a function body calls mount.Decode.
func callsMountDecode(body *ast.BlockStmt) bool {
	found := false
	ast.Inspect(body, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok || sel.Sel.Name != "Decode" {
			return true
		}
		if ident, ok := sel.X.(*ast.Ident); ok && ident.Name == "mount" {
			found = true
		}
		return true
	})
	return found
}

// declaredOperationsWithBody returns the fragment's operations, each with the
// path rewritten into the registration's form and whether it declares a
// requestBody.
func declaredOperationsWithBody(t *testing.T) []declaredOperation {
	t.Helper()

	var doc struct {
		Paths map[string]map[string]struct {
			RequestBody json.RawMessage `json:"requestBody"`
		} `json:"paths"`
	}
	if err := json.Unmarshal(adminFragment, &doc); err != nil {
		t.Fatalf("the embedded OpenAPI fragment is not a JSON object: %v", err)
	}

	var out []declaredOperation
	for path, item := range doc.Paths {
		for method, operation := range item {
			upper := strings.ToUpper(method)
			if !isHTTPMethod(upper) {
				continue
			}
			out = append(out, declaredOperation{
				method:  upper,
				path:    ginPath(path),
				hasBody: len(operation.RequestBody) > 0 && string(operation.RequestBody) != "null",
			})
		}
	}
	return out
}

// findDeclaredOperation returns the operation an http method and
// module-relative path address, pairing by the path being a suffix of the
// fragment path.
func findDeclaredOperation(
	operations []declaredOperation,
	method, relativePath string,
) (declaredOperation, bool) {
	for _, operation := range operations {
		if operation.method != method {
			continue
		}
		if operation.path == relativePath || strings.HasSuffix(operation.path, relativePath) {
			return operation, true
		}
	}
	return declaredOperation{}, false
}

// packageGoFiles returns the names of the package's compiled (non-test) Go
// source files.
func packageGoFiles(t *testing.T) []string {
	t.Helper()

	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("reading the package directory: %v", err)
	}
	var names []string
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		names = append(names, name)
	}
	return names
}

// isHTTPMethod reports whether a method is one a route can be registered under.
func isHTTPMethod(method string) bool {
	switch method {
	case "GET", "HEAD", "POST", "PUT", "PATCH", "DELETE", "OPTIONS", "TRACE":
		return true
	}
	return false
}

// ginPath rewrites an OpenAPI path into the form the registrations use: a
// parameter is "{name}" in the one and ":name" in the other. A path with no
// parameter is returned as written.
func ginPath(p string) string {
	if !strings.ContainsAny(p, "{}") {
		return p
	}
	var b strings.Builder
	for i := 0; i < len(p); i++ {
		if p[i] != '{' {
			if p[i] != '}' {
				b.WriteByte(p[i])
			}
			continue
		}
		b.WriteByte(':')
		for i++; i < len(p) && p[i] != '}'; i++ {
			b.WriteByte(p[i])
		}
	}
	return b.String()
}
