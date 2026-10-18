// Package openapigen turns a module's operation declarations into the OpenAPI
// fragment the module embeds.
//
// A module declares its operations in Go — method, path, parameters, request
// and response bodies, descriptions — and the fragment is a build product
// generated from them, so a route's contract cannot be forgotten when the
// route changes. The declaration types live in pkg/http; this package is the
// one place that binds them to an OpenAPI writer, and it is a build-time
// dependency of the modules that generate fragments, never a runtime one.
//
// The generator bridges github.com/swaggest/openapi-go: the library reflects
// Go types into JSON schemas and assembles an OpenAPI 3.1 document, and this
// package maps the declarations onto it and normalises the output to the
// shape the platform publishes.
package openapigen

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"

	"github.com/swaggest/jsonschema-go"
	"github.com/swaggest/openapi-go"
	"github.com/swaggest/openapi-go/openapi31"

	speedhttp "github.com/vislake/speed/pkg/http"
)

// Generate builds the OpenAPI fragment of one module from its declarations.
// Title and Version become the fragment's info section; the fragment's paths
// stay relative to the API root, as the platform's fragments are.
func Generate(title, version string, operations speedhttp.Operations) ([]byte, error) {
	r := openapi31.NewReflector()
	// Name each component by its Go type, without the declaring package's
	// prefix: the platform publishes "FeatureBoardDocument", not
	// "BoardFeatureBoardDocument".
	r.DefaultOptions = append(r.DefaultOptions,
		jsonschema.InterceptDefName(func(t reflect.Type, _ string) string { return t.Name() }))
	r.Spec.Info.WithTitle(title).WithVersion(version)

	for _, operation := range operations {
		ctx, err := r.NewOperationContext(operation.Method, ginPathToOpenAPI(operation.Path))
		if err != nil {
			return nil, fmt.Errorf("openapigen: %s %s: %w", operation.Method, operation.Path, err)
		}
		ctx.SetID(operation.ID)
		ctx.SetSummary(operation.Summary)
		ctx.SetDescription(operation.Description)

		if operation.Input != nil {
			ctx.AddReqStructure(instance(operation.Input))
		}
		if operation.Request != nil {
			ctx.AddReqStructure(instance(operation.Request.Structure),
				openapi.WithContentType(operation.Request.MediaType))
		}
		for _, response := range operation.Responses {
			addResponse(ctx, response)
		}
		if err := r.AddOperation(ctx); err != nil {
			return nil, fmt.Errorf("openapigen: %s %s: %w", operation.Method, operation.Path, err)
		}
	}

	raw, err := r.Spec.MarshalJSON()
	if err != nil {
		return nil, fmt.Errorf("openapigen: writing the fragment: %w", err)
	}
	return normalise(raw)
}

// addResponse adds one declared answer to the operation.
func addResponse(ctx openapi.OperationContext, response speedhttp.Response) {
	options := []openapi.ContentOption{func(cu *openapi.ContentUnit) {
		cu.HTTPStatus = response.Status
		cu.Description = response.Description
	}}
	if response.Headers != nil {
		headers := responseHeaders(response.Headers)
		options = append(options, openapi.WithCustomize(func(cor openapi.ContentOrReference) {
			resp, ok := cor.(*openapi31.ResponseOrReference)
			if !ok || resp.Response == nil {
				return
			}
			for name, header := range headers {
				resp.Response.WithHeadersItem(name, header)
			}
		}))
	}
	if response.Body != nil {
		options = append(options, openapi.WithContentType(response.Body.MediaType))
		ctx.AddRespStructure(instance(response.Body.Structure), options...)
		return
	}
	// A response with no body still needs an answer entry: the library adds
	// one from a nil structure, with the description and the status.
	ctx.AddRespStructure(nil, options...)
}

// responseHeaders maps a declaration struct to the response's header entries.
func responseHeaders(structure reflect.Type) map[string]openapi31.HeaderOrReference {
	headers := map[string]openapi31.HeaderOrReference{}
	for i := 0; i < structure.NumField(); i++ {
		field := structure.Field(i)
		name := field.Tag.Get("header")
		if name == "" {
			continue
		}
		description := field.Tag.Get("description")
		headers[name] = openapi31.HeaderOrReference{Header: &openapi31.Header{
			Description: &description,
			Schema:      map[string]interface{}{"type": schemaType(field.Type)},
		}}
	}
	return headers
}

// schemaType names the JSON schema type of one header field.
func schemaType(t reflect.Type) string {
	switch t.Kind() {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return "integer"
	case reflect.Bool:
		return "boolean"
	default:
		return "string"
	}
}

// instance returns a zero value of t to hand the reflector.
func instance(t reflect.Type) interface{} {
	return reflect.New(t).Interface()
}

// ginPathToOpenAPI rewrites a gin path parameter to OpenAPI's form.
func ginPathToOpenAPI(path string) string {
	segments := strings.Split(path, "/")
	for i, segment := range segments {
		if strings.HasPrefix(segment, ":") {
			segments[i] = "{" + strings.TrimPrefix(segment, ":") + "}"
		}
	}
	return strings.Join(segments, "/")
}

// normalise brings the library's output to the shape the platform publishes:
// no null in type unions, no duplicated parameter descriptions, no default
// header style, and the problem document under the component name the rest of
// the platform uses.
func normalise(raw []byte) ([]byte, error) {
	var doc map[string]interface{}
	if err := json.Unmarshal(raw, &doc); err != nil {
		return nil, fmt.Errorf("openapigen: reading the generated fragment: %w", err)
	}
	renameProblem(doc)
	walk(doc)
	var out strings.Builder
	encoder := json.NewEncoder(&out)
	encoder.SetEscapeHTML(false)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(doc); err != nil {
		return nil, fmt.Errorf("openapigen: writing the fragment: %w", err)
	}
	return []byte(out.String()), nil
}

// renameProblem moves the problem document to the component name the platform
// publishes, and every reference with it.
func renameProblem(value interface{}) {
	switch node := value.(type) {
	case map[string]interface{}:
		if ref, ok := node["$ref"].(string); ok {
			node["$ref"] = strings.ReplaceAll(ref, "/ProblemDocument", "/Problem")
		}
		for key, child := range node {
			if key == "schemas" {
				if schemas, ok := child.(map[string]interface{}); ok {
					if problem, found := schemas["ProblemDocument"]; found {
						delete(schemas, "ProblemDocument")
						schemas["Problem"] = problem
					}
				}
			}
			renameProblem(child)
		}
	case []interface{}:
		for _, child := range node {
			renameProblem(child)
		}
	}
}

// walk normalises every node in place.
func walk(value interface{}) {
	switch node := value.(type) {
	case map[string]interface{}:
		dropNullType(node)
		dropHeaderStyle(node)
		dropParameterSchemaDescription(node)
		for _, child := range node {
			walk(child)
		}
	case []interface{}:
		for _, child := range node {
			walk(child)
		}
	}
}

// dropNullType removes the null member from a type union: no fragment this
// platform publishes declares a nullable field.
func dropNullType(node map[string]interface{}) {
	types, ok := node["type"].([]interface{})
	if !ok {
		return
	}
	kept := make([]interface{}, 0, len(types))
	for _, kind := range types {
		if kind != "null" {
			kept = append(kept, kind)
		}
	}
	switch len(kept) {
	case 0:
		delete(node, "type")
	case 1:
		node["type"] = kept[0]
	default:
		node["type"] = kept
	}
}

// dropHeaderStyle removes the library's default header style, which the
// platform's fragments do not carry.
func dropHeaderStyle(node map[string]interface{}) {
	if _, ok := node["schema"]; ok {
		delete(node, "style")
	}
}

// dropParameterSchemaDescription removes the description the library repeats
// inside a parameter's schema; the parameter carries it once.
func dropParameterSchemaDescription(node map[string]interface{}) {
	if _, ok := node["in"]; !ok {
		return
	}
	if _, ok := node["name"]; !ok {
		return
	}
	schema, ok := node["schema"].(map[string]interface{})
	if !ok {
		return
	}
	delete(schema, "description")
}
