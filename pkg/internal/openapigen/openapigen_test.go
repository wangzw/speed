package openapigen

import (
	"bytes"
	"encoding/json"
	"net/http"
	"reflect"
	"testing"

	speedhttp "github.com/vislake/speed/pkg/http"
)

// document is one answer body used by the cases.
type document struct {
	Items []string `json:"items" required:"true" description:"The items."`
}

// query is one operation's parameters.
type query struct {
	Limit int    `query:"limit" minimum:"0" description:"Page size."`
	ID    string `path:"id" description:"The item id."`
}

func generate(t *testing.T, operations speedhttp.Operations) map[string]interface{} {
	t.Helper()
	raw, err := Generate("the.module", "1.0.0", operations)
	if err != nil {
		t.Fatalf("generating: %v", err)
	}
	var doc map[string]interface{}
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("the generated fragment is not JSON: %v", err)
	}
	return doc
}

// TestAPathParameterIsRewritten pins the gin-to-OpenAPI spelling: a declared
// route writes ":id" and the document writes "{id}".
func TestAPathParameterIsRewritten(t *testing.T) {
	doc := generate(t, speedhttp.Operations{speedhttp.NewOperation[query, struct{}](
		"the.module.read", http.MethodGet, "/items/:id", "", "", "",
		[]speedhttp.Response{{Status: 204, Description: "No content"}},
	)})
	paths := doc["paths"].(map[string]interface{})
	if _, ok := paths["/items/{id}"]; !ok {
		t.Fatalf("the document has no /items/{id} path: %v", paths)
	}
}

// TestParametersComeFromTheInputStruct holds the parameter surface: the fields
// and their tags become parameters, a path parameter is required, and the
// library's duplicated schema description is removed.
func TestParametersComeFromTheInputStruct(t *testing.T) {
	doc := generate(t, speedhttp.Operations{speedhttp.NewOperation[query, struct{}](
		"the.module.read", http.MethodGet, "/items/:id", "", "", "",
		[]speedhttp.Response{{Status: 204, Description: "No content"}},
	)})
	params := operationOf(t, doc)["parameters"].([]interface{})
	byName := map[string]map[string]interface{}{}
	for _, raw := range params {
		param := raw.(map[string]interface{})
		byName[param["name"].(string)] = param
	}
	if byName["id"]["in"] != "path" || byName["id"]["required"] != true {
		t.Errorf("the path parameter reads %v, want in=path and required", byName["id"])
	}
	if byName["limit"]["in"] != "query" {
		t.Errorf("limit reads %v, want in=query", byName["limit"])
	}
	schema := byName["limit"]["schema"].(map[string]interface{})
	if _, duplicated := schema["description"]; duplicated {
		t.Errorf("the parameter's schema repeats the description: %v", schema)
	}
	if byName["limit"]["description"] != "Page size." {
		t.Errorf("the parameter description reads %v", byName["limit"]["description"])
	}
}

// TestResponseHeadersComeFromTheTags holds the header surface and the removal
// of the library's default header style.
func TestResponseHeadersComeFromTheTags(t *testing.T) {
	doc := generate(t, speedhttp.Operations{speedhttp.NewOperation[struct{}, document](
		"the.module.read", http.MethodGet, "/items", "", "", "",
		[]speedhttp.Response{{
			Status: 200, Description: "The items",
			Headers: []speedhttp.Header{{Name: "Cache-Control", Description: "Always no-store.", Value: "no-store"}},
			Body:    &speedhttp.Body{MediaType: "application/json"},
		}},
	)})
	response := operationOf(t, doc)["responses"].(map[string]interface{})["200"].(map[string]interface{})
	header := response["headers"].(map[string]interface{})["Cache-Control"].(map[string]interface{})
	if header["description"] != "Always no-store." {
		t.Errorf("the header description reads %v", header["description"])
	}
	if _, styled := header["style"]; styled {
		t.Errorf("the header carries the library's default style: %v", header)
	}
}

// TestNullIsDroppedFromTypes holds the platform's shape: no published field is
// declared nullable even though a Go slice reflects as one.
func TestNullIsDroppedFromTypes(t *testing.T) {
	doc := generate(t, speedhttp.Operations{speedhttp.NewOperation[struct{}, document](
		"the.module.read", http.MethodGet, "/items", "", "", "",
		[]speedhttp.Response{{
			Status: 200, Description: "The items",
			Body: &speedhttp.Body{MediaType: "application/json"},
		}},
	)})
	schema := component(t, doc, "document")
	items := schema["properties"].(map[string]interface{})["items"].(map[string]interface{})
	if kind, ok := items["type"].([]interface{}); ok {
		t.Errorf("the items type is the union %v, want a single type", kind)
	}
	if items["type"] != "array" {
		t.Errorf("the items type is %v, want array", items["type"])
	}
}

// TestTheProblemDocumentIsPublishedAsProblem holds the shared component's
// name: every problem response references one component called Problem.
func TestTheProblemDocumentIsPublishedAsProblem(t *testing.T) {
	doc := generate(t, speedhttp.Operations{speedhttp.NewOperation[struct{}, struct{}](
		"the.module.read", http.MethodGet, "/items", "", "", "",
		[]speedhttp.Response{{
			Status: 204, Description: "No content",
		}, {
			Status: 401, Description: "No identity",
			Body: &speedhttp.Body{MediaType: "application/problem+json", Structure: reflect.TypeOf(speedhttp.ProblemDocument{})},
		}},
	)})
	schemas := doc["components"].(map[string]interface{})["schemas"].(map[string]interface{})
	if _, ok := schemas["Problem"]; !ok {
		t.Fatalf("the components carry no Problem schema: %v", schemas)
	}
	if _, stale := schemas["ProblemDocument"]; stale {
		t.Errorf("the components still carry ProblemDocument: %v", schemas)
	}
	response := operationOf(t, doc)["responses"].(map[string]interface{})["401"].(map[string]interface{})
	body := response["content"].(map[string]interface{})["application/problem+json"].(map[string]interface{})
	ref := body["schema"].(map[string]interface{})["$ref"]
	if ref != "#/components/schemas/Problem" {
		t.Errorf("the problem response references %v, want #/components/schemas/Problem", ref)
	}
}

// TestTheSameDeclarationsGiveTheSameBytes holds determinism: the merged
// contract is a build artifact, so generating twice must not differ.
func TestTheSameDeclarationsGiveTheSameBytes(t *testing.T) {
	operations := speedhttp.Operations{speedhttp.NewOperation[struct{}, struct{}](
		"the.module.read", http.MethodGet, "/items", "", "", "",
		[]speedhttp.Response{{Status: 204, Description: "No content"}},
	)}
	first, err := Generate("the.module", "1.0.0", operations)
	if err != nil {
		t.Fatalf("generating: %v", err)
	}
	second, err := Generate("the.module", "1.0.0", operations)
	if err != nil {
		t.Fatalf("generating again: %v", err)
	}
	if !bytes.Equal(first, second) {
		t.Errorf("two generations of the same declarations differ")
	}
}

// operationOf returns the one GET operation under /items or /items/{id}.
func operationOf(t *testing.T, doc map[string]interface{}) map[string]interface{} {
	t.Helper()
	paths := doc["paths"].(map[string]interface{})
	for _, item := range paths {
		operations := item.(map[string]interface{})
		if get, ok := operations["get"].(map[string]interface{}); ok {
			return get
		}
	}
	t.Fatalf("the document has no GET operation: %v", paths)
	return nil
}

// component returns one component schema.
func component(t *testing.T, doc map[string]interface{}, name string) map[string]interface{} {
	t.Helper()
	schemas := doc["components"].(map[string]interface{})["schemas"].(map[string]interface{})
	schema, ok := schemas[name].(map[string]interface{})
	if !ok {
		t.Fatalf("the components carry no %s schema: %v", name, schemas)
	}
	return schema
}

// customError is an error body in a host's own shape.
type customError struct {
	Code string `json:"code" required:"true" description:"The host's error code."`
}

// TestACustomErrorShapeIsPublishedAsDeclared holds the seam a host uses to
// replace the error response: the declared media type and structure are what
// the fragment carries, with no problem document assumed.
func TestACustomErrorShapeIsPublishedAsDeclared(t *testing.T) {
	shape := speedhttp.ErrorShape{
		MediaType: "application/vnd.test.error+json",
		Structure: reflect.TypeOf(customError{}),
	}
	doc := generate(t, speedhttp.Operations{speedhttp.NewOperation[struct{}, struct{}](
		"the.module.read", http.MethodGet, "/items", "", "", "",
		[]speedhttp.Response{
			{Status: 204, Description: "No content"},
			speedhttp.ErrorResponse(shape, 409, "Conflict"),
		},
	)})
	response := operationOf(t, doc)["responses"].(map[string]interface{})["409"].(map[string]interface{})
	content := response["content"].(map[string]interface{})
	if _, ok := content["application/vnd.test.error+json"]; !ok {
		t.Fatalf("the conflict carries %v, want the declared media type", content)
	}
	schemas := doc["components"].(map[string]interface{})["schemas"].(map[string]interface{})
	if _, ok := schemas["customError"]; !ok {
		t.Errorf("the components carry no customError schema: %v", schemas)
	}
	if _, ok := schemas["Problem"]; ok {
		t.Errorf("a problem document was published for a custom shape: %v", schemas)
	}
}
