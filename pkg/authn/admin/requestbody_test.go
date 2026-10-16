package admin

import (
	"encoding/json"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"reflect"
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
// Presence is not enough. A requestBody that names the wrong schema is as
// unreachable as one that is missing: the client sends a body of the wrong
// shape and the handler drops or refuses every field it needed. So the check
// also recovers what each handler decodes -- the struct its `var body` names
// and the JSON keys that struct marshals -- and requires the referenced
// schema's property set to equal it. A create mispointed at the patch schema
// then fails here rather than silently shipping a client that sends three
// fields where ten are needed.
//
// The pairing is by path suffix: the fragment writes the endpoint-prefixed path
// ("/auth/admin/accounts") and the registrations write the module-relative one
// ("/admin/accounts"), and each module-relative path is a unique suffix of its
// fragment path. A parameter is "{name}" in the fragment and ":name" in the
// registration, so the fragment path is rewritten before the comparison.
func TestTheDeclaredRequestBodiesAreTheOnesTheHandlersDecode(t *testing.T) {
	decoding := handlersThatDecode(t)
	structs := structJSONFields(t)
	registered := registeredRoutes(t)
	operations := declaredOperationsWithBody(t)

	if len(decoding) == 0 {
		t.Fatalf("no handler is seen to decode a request body; the check is not reading what it thinks it is")
	}
	if len(operations) == 0 {
		t.Fatalf("the fragment declares no requestBody on any operation; " +
			"the check is not reading what it thinks it is")
	}

	compared := 0
	for _, route := range registered {
		bodyStruct, decodes := decoding[route.handler]
		if !decodes {
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
			continue
		}
		if operation.bodyProperties == nil {
			t.Errorf("the fragment's requestBody for %s %s does not resolve to a schema with "+
				"properties (its $ref %q names no schema the fragment defines, or its content "+
				"carries none), so the shape the client sends cannot be checked",
				route.method, route.path, operation.bodySchema)
			continue
		}
		want, known := structs[bodyStruct]
		if !known {
			t.Errorf("the handler %s for %s %s decodes %s, whose fields could not be recovered "+
				"from the package sources; the shape it reads cannot be checked",
				route.handler, route.method, route.path, bodyStruct)
			continue
		}
		compared++
		if !sameStringSet(want, operation.bodyProperties) {
			t.Errorf("the handler %s for %s %s decodes %s, whose JSON fields are %v, but the "+
				"fragment points that operation's requestBody at schema %s, whose properties are "+
				"%v. A generated client sends the schema's shape, so the fields only the struct "+
				"reads are dropped and the write cannot succeed from a client built on the contract",
				route.handler, route.method, route.path, bodyStruct,
				sortedUnique(want), operation.bodySchema, sortedUnique(operation.bodyProperties))
		}
	}
	if compared == 0 {
		t.Fatalf("no declared requestBody's shape was compared against a handler; " +
			"the check is not reading what it thinks it is")
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
// a generated client addresses, whether it carries a requestBody, and, when it
// does, the schema it names and the property set that schema declares. A nil
// property set means the requestBody carries no schema the fragment resolves.
type declaredOperation struct {
	method         string
	path           string // the fragment path, "{name}" already rewritten to ":name"
	hasBody        bool
	bodySchema     string
	bodyProperties map[string]bool
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

// handlersThatDecode returns, for every handler method whose body decodes a JSON
// request with mount.Decode, the name of the struct type it decodes into. Both
// are parsed from the package's compiled sources.
func handlersThatDecode(t *testing.T) map[string]string {
	t.Helper()

	entries := packageGoFiles(t)
	out := map[string]string{}
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
			if target, ok := decodeTarget(fn.Body); ok {
				out[fn.Name.Name] = target
			}
		}
	}
	return out
}

// decodeTarget returns the type name of the variable a function body decodes
// into with mount.Decode. The variable is the one the call passes by address
// (mount.Decode(c, &body)), and its type is read from the `var body T`
// declaration beside the call. It reports false when the body has no mount.Decode
// call or the decoded variable is not a plain var of a named type.
func decodeTarget(body *ast.BlockStmt) (string, bool) {
	varName := ""
	ast.Inspect(body, func(n ast.Node) bool {
		if varName != "" {
			return false
		}
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok || sel.Sel.Name != "Decode" {
			return true
		}
		ident, ok := sel.X.(*ast.Ident)
		if !ok || ident.Name != "mount" || len(call.Args) != 2 {
			return true
		}
		unary, ok := call.Args[1].(*ast.UnaryExpr)
		if !ok || unary.Op != token.AND {
			return true
		}
		argIdent, ok := unary.X.(*ast.Ident)
		if !ok {
			return true
		}
		varName = argIdent.Name
		return false
	})
	if varName == "" {
		return "", false
	}

	target := ""
	ast.Inspect(body, func(n ast.Node) bool {
		if target != "" {
			return false
		}
		decl, ok := n.(*ast.GenDecl)
		if !ok || decl.Tok != token.VAR {
			return true
		}
		for _, spec := range decl.Specs {
			vs, ok := spec.(*ast.ValueSpec)
			if !ok {
				continue
			}
			for _, specName := range vs.Names {
				if specName.Name != varName {
					continue
				}
				if typeIdent, ok := vs.Type.(*ast.Ident); ok {
					target = typeIdent.Name
				}
			}
		}
		return true
	})
	if target == "" {
		return "", false
	}
	return target, true
}

// structJSONFields returns, for every struct type the package declares, the set
// of JSON keys encoding/json marshals it under: each field's json tag name, or
// the field's own name when it carries no json tag. A field tagged `json:"-"`
// contributes no key.
func structJSONFields(t *testing.T) map[string]map[string]bool {
	t.Helper()

	entries := packageGoFiles(t)
	out := map[string]map[string]bool{}
	fset := token.NewFileSet()
	for _, name := range entries {
		file, err := parser.ParseFile(fset, name, nil, 0)
		if err != nil {
			t.Fatalf("parsing %s: %v", name, err)
		}
		ast.Inspect(file, func(n ast.Node) bool {
			ts, ok := n.(*ast.TypeSpec)
			if !ok {
				return true
			}
			st, ok := ts.Type.(*ast.StructType)
			if !ok {
				return true
			}
			fields := map[string]bool{}
			for _, field := range st.Fields.List {
				key, include := jsonFieldName(field)
				if include {
					fields[key] = true
				}
			}
			out[ts.Name.Name] = fields
			return true
		})
	}
	return out
}

// jsonFieldName returns the JSON key a struct field marshals under, following
// encoding/json's rules: the name part of its json tag, the field's own name
// when the tag is absent or names nothing, and no key (include false) for an
// embedded field or one tagged `json:"-"`.
func jsonFieldName(field *ast.Field) (string, bool) {
	if len(field.Names) != 1 {
		return "", false
	}
	name := field.Names[0].Name
	if field.Tag == nil {
		return name, true
	}
	raw, err := strconv.Unquote(field.Tag.Value)
	if err != nil {
		return name, true
	}
	tag, ok := reflect.StructTag(raw).Lookup("json")
	if !ok {
		return name, true
	}
	tagName := strings.Split(tag, ",")[0]
	switch tagName {
	case "-":
		return "", false
	case "":
		return name, true
	default:
		return tagName, true
	}
}

// declaredOperationsWithBody returns the fragment's operations, each with the
// path rewritten into the registration's form, whether it declares a
// requestBody, and the property set of the schema that requestBody names.
func declaredOperationsWithBody(t *testing.T) []declaredOperation {
	t.Helper()

	var doc struct {
		Paths map[string]map[string]struct {
			RequestBody json.RawMessage `json:"requestBody"`
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

	schemas := map[string]map[string]bool{}
	for name, schema := range doc.Components.Schemas {
		props := map[string]bool{}
		for key := range schema.Properties {
			props[key] = true
		}
		schemas[name] = props
	}

	var out []declaredOperation
	for path, item := range doc.Paths {
		for method, operation := range item {
			upper := strings.ToUpper(method)
			if !isHTTPMethod(upper) {
				continue
			}
			declared := declaredOperation{
				method:  upper,
				path:    ginPath(path),
				hasBody: len(operation.RequestBody) > 0 && string(operation.RequestBody) != "null",
			}
			if declared.hasBody {
				declared.bodySchema, declared.bodyProperties = requestBodyShape(operation.RequestBody, schemas)
			}
			out = append(out, declared)
		}
	}
	return out
}

// requestBodyShape returns the name and property set of the schema a requestBody
// names. The schema is read from its application/json content: a $ref into
// components/schemas is resolved against the fragment's schemas, and an inline
// schema's own properties are read directly. A nil property set means the body
// names no schema the fragment resolves.
func requestBodyShape(body json.RawMessage, schemas map[string]map[string]bool) (string, map[string]bool) {
	var ref struct {
		Content map[string]struct {
			Schema struct {
				Ref        string                     `json:"$ref"`
				Properties map[string]json.RawMessage `json:"properties"`
			} `json:"schema"`
		} `json:"content"`
	}
	if err := json.Unmarshal(body, &ref); err != nil {
		return "", nil
	}
	media, ok := ref.Content["application/json"]
	if !ok {
		return "", nil
	}
	const prefix = "#/components/schemas/"
	switch {
	case strings.HasPrefix(media.Schema.Ref, prefix):
		name := strings.TrimPrefix(media.Schema.Ref, prefix)
		props, ok := schemas[name]
		if !ok {
			return name, nil
		}
		return name, props
	case media.Schema.Ref != "":
		return media.Schema.Ref, nil
	default:
		props := map[string]bool{}
		for key := range media.Schema.Properties {
			props[key] = true
		}
		return "(inline)", props
	}
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

// sameStringSet reports whether two sets hold the same members.
func sameStringSet(a, b map[string]bool) bool {
	if len(a) != len(b) {
		return false
	}
	for member := range a {
		if !b[member] {
			return false
		}
	}
	return true
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
