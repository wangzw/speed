package openapigen

import (
	"bytes"
	"encoding/json"
	"net/http"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

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

// requestBody is one declared request body with the given media type.
func requestBody(mediaType string) *speedhttp.RequestBody {
	return &speedhttp.RequestBody{MediaType: mediaType}
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
		"the.module.read", http.MethodGet, "/items/:id", "", "", nil,
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
		"the.module.read", http.MethodGet, "/items/:id", "", "", nil,
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

// doubleTagged declares a field carrying two parameter sources.
type doubleTagged struct {
	Value string `query:"value" header:"Value"`
}

// TestAParameterIsPublishedOnceUnderTheBoundSource holds the precedence:
// pkg/http binds a field carrying two parameter tags from the first one only,
// so the fragment must not promise the other.
func TestAParameterIsPublishedOnceUnderTheBoundSource(t *testing.T) {
	doc := generate(t, speedhttp.Operations{speedhttp.NewOperation[doubleTagged, struct{}](
		"the.module.read", http.MethodGet, "/items", "", "", nil,
		[]speedhttp.Response{{Status: 204, Description: "No content"}},
	)})
	parameters := parametersOf(t, operationOf(t, doc))
	if len(parameters) != 1 {
		t.Fatalf("the operation publishes %d parameters, want 1: %v", len(parameters), parameters)
	}
	if parameters["value"] == nil || parameters["value"]["in"] != "query" {
		t.Errorf("the parameter reads %v, want the query source", parameters)
	}
	if parameters["Value"] != nil {
		t.Errorf("the ignored header tag is published too: %v", parameters)
	}
}

// parameterSources declares one field per parameter source.
type parameterSources struct {
	ID     string `path:"id"`
	Limit  int    `query:"limit"`
	ETag   string `header:"If-None-Match"`
	Locale string `cookie:"locale"`
}

// TestEveryParameterSourceIsPublished holds the parameter surface: each of the
// four tags pkg/http binds is published under its source.
func TestEveryParameterSourceIsPublished(t *testing.T) {
	doc := generate(t, speedhttp.Operations{speedhttp.NewOperation[parameterSources, struct{}](
		"the.module.read", http.MethodGet, "/items/:id", "", "", nil,
		[]speedhttp.Response{{Status: 204, Description: "No content"}},
	)})
	parameters := parametersOf(t, operationOf(t, doc))
	want := map[string]string{
		"id":            "path",
		"limit":         "query",
		"If-None-Match": "header",
		"locale":        "cookie",
	}
	for name, source := range want {
		parameter, ok := parameters[name]
		if !ok {
			t.Errorf("the operation publishes no %q parameter: %v", name, parameters)
			continue
		}
		if parameter["in"] != source {
			t.Errorf("the %q parameter reads %v, want in=%s", name, parameter["in"], source)
		}
	}
}

// TestResponseHeadersComeFromTheTags holds the header surface and the removal
// of the library's default header style.
func TestResponseHeadersComeFromTheTags(t *testing.T) {
	doc := generate(t, speedhttp.Operations{speedhttp.NewOperation[struct{}, document](
		"the.module.read", http.MethodGet, "/items", "", "", nil,
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
		"the.module.read", http.MethodGet, "/items", "", "", nil,
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
		"the.module.read", http.MethodGet, "/items", "", "", nil,
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

// ProblemDocument is a host type that happens to share its name with the entry
// point's default error shape.
type ProblemDocument struct {
	Host string `json:"host" required:"true"`
}

// TestTheProblemRenameAppliesOnlyToTheDefaultShape holds the boundary: a host
// type named ProblemDocument keeps its own component name; only the entry
// point's default shape is published as Problem.
func TestTheProblemRenameAppliesOnlyToTheDefaultShape(t *testing.T) {
	doc := generate(t, speedhttp.Operations{speedhttp.NewOperation[struct{}, ProblemDocument](
		"the.module.read", http.MethodGet, "/items", "", "", nil,
		[]speedhttp.Response{{
			Status: 200, Description: "The host's document",
			Body: &speedhttp.Body{MediaType: "application/json"},
		}},
	)})
	schemas := doc["components"].(map[string]interface{})["schemas"].(map[string]interface{})
	if _, ok := schemas["ProblemDocument"]; !ok {
		t.Fatalf("the host's ProblemDocument schema is missing: %v", schemas)
	}
	if _, wrong := schemas["Problem"]; wrong {
		t.Errorf("a host type was renamed to Problem: %v", schemas)
	}
	body := contentOf(t, operationOf(t, doc), "200", "application/json")
	if ref := body["schema"].(map[string]interface{})["$ref"]; ref != "#/components/schemas/ProblemDocument" {
		t.Errorf("the host answer references %v, want its own component", ref)
	}
}

// TestTheSameDeclarationsGiveTheSameBytes holds determinism: the merged
// contract is a build artifact, so generating twice must not differ.
func TestTheSameDeclarationsGiveTheSameBytes(t *testing.T) {
	operations := speedhttp.Operations{speedhttp.NewOperation[struct{}, struct{}](
		"the.module.read", http.MethodGet, "/items", "", "", nil,
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
		"the.module.read", http.MethodGet, "/items", "", "", nil,
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

// jsonInput declares a parameter, a JSON body field and a second parameter.
type jsonInput struct {
	ID     string `path:"id"`
	Q      string `query:"q"`
	Name   string `json:"name" required:"true" description:"The name."`
	Hidden string `json:"hidden"`
}

// TestAJSONRequestBodyPublishesOnlyJSONFields holds the JSON request body: the
// json-tagged fields become the object schema, their required and description
// tags travel with them, and the parameter fields stay parameters.
// TestABodyOperationPublishesTheLimitRefusal holds the platform answer every
// body-bearing operation can receive: the entry point refuses a body over the
// endpoint's limit with 413, so the fragment carries it even when the
// declaration's own set omits it.
func TestABodyOperationPublishesTheLimitRefusal(t *testing.T) {
	doc := generate(t, speedhttp.Operations{speedhttp.NewOperation[jsonInput, struct{}](
		"the.module.write", http.MethodPost, "/items/:id", "", "", requestBody("application/json"),
		[]speedhttp.Response{{Status: 204, Description: "No content"}},
	)})
	responses := operationOfMethod(t, doc, "post")["responses"].(map[string]interface{})
	if _, ok := responses["413"]; !ok {
		t.Errorf("the body operation publishes no 413 response: %v", responses)
	}
}

func TestAJSONRequestBodyPublishesOnlyJSONFields(t *testing.T) {
	doc := generate(t, speedhttp.Operations{speedhttp.NewOperation[jsonInput, struct{}](
		"the.module.write", http.MethodPost, "/items/:id", "", "", requestBody("application/json"),
		[]speedhttp.Response{{Status: 204, Description: "No content"}},
	)})
	operation := operationOfMethod(t, doc, "post")
	content := requestContentOf(t, operation)
	media, ok := content["application/json"].(map[string]interface{})
	if !ok {
		t.Fatalf("the request body carries %v, want application/json", content)
	}
	schema := media["schema"].(map[string]interface{})
	properties := schema["properties"].(map[string]interface{})
	if properties["name"] == nil || properties["hidden"] == nil {
		t.Errorf("the JSON schema carries %v, want the json-tagged fields", properties)
	}
	if properties["id"] != nil || properties["q"] != nil {
		t.Errorf("a parameter field is published in the body schema: %v", properties)
	}
	required, _ := schema["required"].([]interface{})
	if len(required) != 1 || required[0] != "name" {
		t.Errorf("the required list reads %v, want [name]", schema["required"])
	}
	if description := properties["name"].(map[string]interface{})["description"]; description != "The name." {
		t.Errorf("the body field description reads %v", description)
	}
	parameters := parametersOf(t, operation)
	if parameters["id"] == nil || parameters["q"] == nil {
		t.Errorf("the request parameters read %v, want id and q", parameters)
	}
	if parameters["name"] != nil || parameters["hidden"] != nil {
		t.Errorf("a body field is published as a parameter: %v", parameters)
	}
}

// describedJSONInput declares the body schema's own description the way an
// answer structure does: on a blank field.
type describedJSONInput struct {
	_    struct{} `description:"The body of one update."`
	Name string   `json:"name" required:"true"`
}

// TestAJSONRequestBodyPublishesItsSchemaDescription holds the struct-level
// description: a blank field on the input is published as the body schema's
// description, on the named component the operation references.
func TestAJSONRequestBodyPublishesItsSchemaDescription(t *testing.T) {
	doc := generate(t, speedhttp.Operations{speedhttp.NewOperation[describedJSONInput, struct{}](
		"the.module.write", http.MethodPost, "/items", "", "",
		&speedhttp.RequestBody{MediaType: "application/json", Required: true, Name: "DescribedBody"},
		[]speedhttp.Response{{Status: 204, Description: "No content"}},
	)})
	schemas := doc["components"].(map[string]interface{})["schemas"].(map[string]interface{})
	schema, ok := schemas["DescribedBody"].(map[string]interface{})
	if !ok {
		t.Fatalf("the components carry no DescribedBody: %v", schemas)
	}
	if schema["description"] != "The body of one update." {
		t.Errorf("the body schema description reads %v, want the declared one", schema["description"])
	}
}

// openJSONInput declares a body whose route tolerates members the declaration
// does not describe: the blank field opens the object the way an answer
// structure does.
type openJSONInput struct {
	_    struct{} `additionalProperties:"true" description:"The body of one update."`
	Name string   `json:"name" required:"true"`
}

// TestAJSONRequestBodyCanDeclareAnOpenSchema holds the escape hatch for a route
// that drops the members its declaration does not carry rather than refusing
// them: the body schema is published open, so it does not claim a refusal the
// route does not make.
func TestAJSONRequestBodyCanDeclareAnOpenSchema(t *testing.T) {
	doc := generate(t, speedhttp.Operations{speedhttp.NewOperation[openJSONInput, struct{}](
		"the.module.write", http.MethodPost, "/items", "", "",
		&speedhttp.RequestBody{MediaType: "application/json", Required: true, Name: "OpenBody"},
		[]speedhttp.Response{{Status: 204, Description: "No content"}},
	)})
	schemas := doc["components"].(map[string]interface{})["schemas"].(map[string]interface{})
	schema, ok := schemas["OpenBody"].(map[string]interface{})
	if !ok {
		t.Fatalf("the components carry no OpenBody: %v", schemas)
	}
	if open, ok := schema["additionalProperties"].(bool); !ok || !open {
		t.Errorf("the body schema reads %v, want additionalProperties true", schema)
	}
	if schema["description"] != "The body of one update." {
		t.Errorf("the body schema description reads %v, want the declared one", schema["description"])
	}
}

// parameterOnly declares parameters and no body.
type parameterOnly struct {
	ID string `path:"id"`
	Q  string `query:"q"`
}

// TestANamedRequestBodyPublishesAComponent holds a named request body: its
// schema moves into the components under the declared name and the operation
// references it, so the generated client gets a stable type name.
func TestANamedRequestBodyPublishesAComponent(t *testing.T) {
	doc := generate(t, speedhttp.Operations{speedhttp.NewOperation[jsonInput, struct{}](
		"the.module.write", http.MethodPost, "/items/:id", "", "",
		&speedhttp.RequestBody{MediaType: "application/json", Required: true, Name: "SharedDeclaration"},
		[]speedhttp.Response{{Status: 204, Description: "No content"}},
	)})
	content := requestContentOf(t, operationOfMethod(t, doc, "post"))
	media := content["application/json"].(map[string]interface{})
	schema := media["schema"].(map[string]interface{})
	if schema["$ref"] != "#/components/schemas/SharedDeclaration" {
		t.Fatalf("the request body schema reads %v, want a reference to the named component", schema)
	}
	schemas := doc["components"].(map[string]interface{})["schemas"].(map[string]interface{})
	if _, ok := schemas["SharedDeclaration"]; !ok {
		t.Fatalf("the components carry no SharedDeclaration: %v", schemas)
	}
}

// TestAnEmptyMediaTypePublishesNoRequestBody holds the no-body declaration: the
// parameters are published and there is no request body at all.
func TestAnEmptyMediaTypePublishesNoRequestBody(t *testing.T) {
	doc := generate(t, speedhttp.Operations{speedhttp.NewOperation[parameterOnly, struct{}](
		"the.module.read", http.MethodGet, "/items/:id", "", "", nil,
		[]speedhttp.Response{{Status: 204, Description: "No content"}},
	)})
	operation := operationOf(t, doc)
	if body, ok := operation["requestBody"]; ok {
		t.Errorf("the operation publishes a request body: %v", body)
	}
	parameters := parametersOf(t, operation)
	if parameters["id"] == nil || parameters["q"] == nil {
		t.Errorf("the request parameters read %v, want id and q", parameters)
	}
}

// TestAJSONBodyIsPublishedOnEveryMethod holds the entry point's method
// surface: pkg/http binds a declared body on GET as it does on POST, so the
// fragment must carry it there too.
func TestAJSONBodyIsPublishedOnEveryMethod(t *testing.T) {
	for _, method := range []string{http.MethodGet, http.MethodDelete, http.MethodPost} {
		t.Run(method, func(t *testing.T) {
			doc := generate(t, speedhttp.Operations{speedhttp.NewOperation[jsonInput, struct{}](
				"the.module.write", method, "/items/:id", "", "", requestBody("application/json"),
				[]speedhttp.Response{{Status: 204, Description: "No content"}},
			)})
			operation := operationOfMethod(t, doc, strings.ToLower(method))
			media, ok := requestContentOf(t, operation)["application/json"].(map[string]interface{})
			if !ok {
				t.Fatalf("%s publishes no application/json request body", method)
			}
			if media["schema"].(map[string]interface{})["properties"].(map[string]interface{})["name"] == nil {
				t.Errorf("the %s request body carries no json field: %v", method, media)
			}
		})
	}
}

// TestAnUnsupportedMediaTypeIsRefused holds the closed set: pkg/http refuses
// an unsupported request media type when the handler is built, so the
// generator refuses it instead of publishing a body the entry point rejects.
func TestAnUnsupportedMediaTypeIsRefused(t *testing.T) {
	_, err := Generate("the.module", "1.0.0", speedhttp.Operations{speedhttp.NewOperation[jsonInput, struct{}](
		"the.module.write", http.MethodPost, "/items/:id", "", "", requestBody("text/plain"),
		[]speedhttp.Response{{Status: 204, Description: "No content"}},
	)})
	if err == nil {
		t.Fatal("generating an unsupported request media type succeeded")
	}
	if message := err.Error(); !strings.Contains(message, "unsupported request media type") {
		t.Errorf("the error reads %q, want it to name the unsupported media type", message)
	}
}

// multipartInput declares one parameter and both kinds of multipart member.
type multipartInput struct {
	ID   string `path:"id"`
	Q    string `query:"q"`
	Name string `form:"name" description:"The name."`
	File []byte `file:"upload"`
}

// TestAMultipartBodyPublishesFormAndFileFields holds the multipart shape: the
// request body is multipart/form-data with an object schema, the file field is
// a binary string, and the form field is not published as a query parameter.
func TestAMultipartBodyPublishesFormAndFileFields(t *testing.T) {
	doc := generate(t, speedhttp.Operations{speedhttp.NewOperation[multipartInput, struct{}](
		"the.module.upload", http.MethodPost, "/items/:id", "", "", requestBody("multipart/form-data"),
		[]speedhttp.Response{{Status: 204, Description: "No content"}},
	)})
	operation := operationOfMethod(t, doc, "post")
	content := requestContentOf(t, operation)
	if _, wrong := content["application/x-www-form-urlencoded"]; wrong {
		t.Fatalf("the multipart body is published as form data: %v", content)
	}
	media, ok := content["multipart/form-data"].(map[string]interface{})
	if !ok {
		t.Fatalf("the request body carries %v, want multipart/form-data", content)
	}
	properties := media["schema"].(map[string]interface{})["properties"].(map[string]interface{})
	if properties["name"] == nil {
		t.Errorf("the multipart schema carries no form field: %v", properties)
	}
	upload, ok := properties["upload"].(map[string]interface{})
	if !ok {
		t.Fatalf("the multipart schema carries no file field: %v", properties)
	}
	fileSchema := resolveSchema(t, doc, upload)
	if fileSchema["type"] != "string" || fileSchema["format"] != "binary" {
		t.Errorf("the file field reads %v, want a binary string", fileSchema)
	}

	parameters := parametersOf(t, operation)
	if parameters["id"] == nil || parameters["q"] == nil {
		t.Errorf("the request parameters read %v, want id and q", parameters)
	}
	if parameters["name"] != nil {
		t.Errorf("the form field is also published as a query parameter: %v", parameters)
	}
}

// multipartFormInput declares multipart with no file field.
type multipartFormInput struct {
	ID   string `path:"id"`
	Name string `form:"name" required:"true"`
}

// TestAMultipartBodyWithoutAFileStaysMultipart holds the media type: the
// library only switches to multipart when it recognizes a file upload, so a
// form-only multipart declaration must be corrected rather than published as
// application/x-www-form-urlencoded.
func TestAMultipartBodyWithoutAFileStaysMultipart(t *testing.T) {
	doc := generate(t, speedhttp.Operations{speedhttp.NewOperation[multipartFormInput, struct{}](
		"the.module.upload", http.MethodPost, "/items/:id", "", "", requestBody("multipart/form-data"),
		[]speedhttp.Response{{Status: 204, Description: "No content"}},
	)})
	content := requestContentOf(t, operationOfMethod(t, doc, "post"))
	media, ok := content["multipart/form-data"].(map[string]interface{})
	if !ok {
		t.Fatalf("the request body carries %v, want multipart/form-data", content)
	}
	if media["schema"].(map[string]interface{})["properties"].(map[string]interface{})["name"] == nil {
		t.Errorf("the multipart schema carries no form field: %v", media)
	}
}

// formInput declares a query parameter and a form field.
type formInput struct {
	ID   string `path:"id"`
	Q    string `query:"q"`
	Name string `form:"name"`
}

// TestAFormFieldIsNotPublishedAsAQueryParameter holds the binding boundary: the
// entry point reads a `form`-tagged field from the body only, so the fragment
// must not promise it as a query parameter too.
func TestAFormFieldIsNotPublishedAsAQueryParameter(t *testing.T) {
	doc := generate(t, speedhttp.Operations{speedhttp.NewOperation[formInput, struct{}](
		"the.module.form", http.MethodPost, "/items/:id", "", "", requestBody("application/x-www-form-urlencoded"),
		[]speedhttp.Response{{Status: 204, Description: "No content"}},
	)})
	operation := operationOfMethod(t, doc, "post")
	content := requestContentOf(t, operation)
	media, ok := content["application/x-www-form-urlencoded"].(map[string]interface{})
	if !ok {
		t.Fatalf("the request body carries %v, want application/x-www-form-urlencoded", content)
	}
	if media["schema"].(map[string]interface{})["properties"].(map[string]interface{})["name"] == nil {
		t.Errorf("the form schema carries no form field: %v", media)
	}
	parameters := parametersOf(t, operation)
	if parameters["q"] == nil || parameters["id"] == nil {
		t.Errorf("the request parameters read %v, want id and q", parameters)
	}
	if parameters["name"] != nil {
		t.Errorf("the form field is also published as a query parameter: %v", parameters)
	}
}

// rawInput declares a binary body and two parameters.
type rawInput struct {
	ID   string `path:"id"`
	Q    string `query:"q"`
	Body []byte `raw:"true"`
}

// TestABinaryRequestBodyPublishesItsParameters holds the raw shape: the body is
// an application/octet-stream binary string, and the parameters are published
// as they are for every other media type.
func TestABinaryRequestBodyPublishesItsParameters(t *testing.T) {
	doc := generate(t, speedhttp.Operations{speedhttp.NewOperation[rawInput, struct{}](
		"the.module.upload", http.MethodPut, "/items/:id", "", "", requestBody("application/octet-stream"),
		[]speedhttp.Response{{Status: 204, Description: "No content"}},
	)})
	operation := operationOfMethod(t, doc, "put")
	parameters := parametersOf(t, operation)
	if parameters["id"] == nil || parameters["q"] == nil {
		t.Errorf("the request parameters read %v, want id and q", parameters)
	}
	content := requestContentOf(t, operation)
	media, ok := content["application/octet-stream"].(map[string]interface{})
	if !ok {
		t.Fatalf("the request body carries %v, want application/octet-stream", content)
	}
	schema := media["schema"].(map[string]interface{})
	if schema["type"] != "string" || schema["format"] != "binary" {
		t.Errorf("the request body schema reads %v, want a binary string", schema)
	}
}

// TestAParameterlessBinaryRequestStaysBinary holds the no-input side: an
// octet-stream declaration whose input carries only the raw field publishes a
// binary body and no parameters.
func TestAParameterlessBinaryRequestStaysBinary(t *testing.T) {
	type onlyRaw struct {
		Body []byte `raw:"true"`
	}
	doc := generate(t, speedhttp.Operations{speedhttp.NewOperation[onlyRaw, struct{}](
		"the.module.upload", http.MethodPost, "/blob", "", "", requestBody("application/octet-stream"),
		[]speedhttp.Response{{Status: 204, Description: "No content"}},
	)})
	operation := operationOfMethod(t, doc, "post")
	if parameters := parametersOf(t, operation); len(parameters) != 0 {
		t.Errorf("the operation publishes parameters: %v", parameters)
	}
	schema := requestContentOf(t, operation)["application/octet-stream"].(map[string]interface{})["schema"].(map[string]interface{})
	if schema["type"] != "string" || schema["format"] != "binary" {
		t.Errorf("the request body schema reads %v, want a binary string", schema)
	}
}

// TestReplyResponsesPublishTheirBodies holds the generator's view of a reply
// operation: the success body is not a type parameter, so each answer's own
// declared structure and media type must reach the fragment.
func TestReplyResponsesPublishTheirBodies(t *testing.T) {
	cases := []struct {
		name      string
		mediaType string
		structure reflect.Type
		ref       string
		typ       string
		format    string
	}{
		{
			name:      "json",
			mediaType: "application/json",
			structure: reflect.TypeOf(document{}),
			ref:       "#/components/schemas/document",
		},
		{
			name:      "bytes",
			mediaType: "application/octet-stream",
			typ:       "string",
			format:    "binary",
		},
		{
			name:      "stream",
			mediaType: "text/csv",
			typ:       "string",
		},
		{
			name:      "event stream",
			mediaType: "text/event-stream",
			typ:       "",
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			doc := generate(t, speedhttp.Operations{speedhttp.NewReplyOperation[struct{}](
				"the.module.reply", http.MethodGet, "/items", "", "", nil,
				[]speedhttp.Response{{
					Status: 200, Description: "The answer",
					Body: &speedhttp.Body{MediaType: testCase.mediaType, Structure: testCase.structure},
				}},
			)})
			media := contentOf(t, operationOf(t, doc), "200", testCase.mediaType)
			schema, declared := media["schema"].(map[string]interface{})
			if testCase.ref != "" {
				if !declared {
					t.Fatalf("the %s media type carries no schema: %v", testCase.mediaType, media)
				}
				if schema["$ref"] != testCase.ref {
					t.Errorf("the schema reads %v, want $ref %s", schema, testCase.ref)
				}
				return
			}
			if testCase.typ == "" {
				if declared {
					t.Errorf("the %s media type carries a body schema: %v", testCase.mediaType, schema)
				}
				return
			}
			if !declared {
				t.Fatalf("the %s media type carries no schema: %v", testCase.mediaType, media)
			}
			if schema["type"] != testCase.typ {
				t.Errorf("the schema type reads %v, want %s", schema["type"], testCase.typ)
			}
			if testCase.format != "" && schema["format"] != testCase.format {
				t.Errorf("the schema format reads %v, want %s", schema["format"], testCase.format)
			}
		})
	}
}

// TestAnEventStreamWithAStructurePublishesNoBodySchema holds the stream side:
// a declared Structure does not describe the frames the entry point writes, so
// the event stream carries its media type and no schema.
func TestAnEventStreamWithAStructurePublishesNoBodySchema(t *testing.T) {
	doc := generate(t, speedhttp.Operations{speedhttp.NewReplyOperation[struct{}](
		"the.module.reply", http.MethodGet, "/events", "", "", nil,
		[]speedhttp.Response{{
			Status: 200, Description: "The events",
			Body: &speedhttp.Body{MediaType: "text/event-stream", Structure: reflect.TypeOf(document{})},
		}},
	)})
	media := contentOf(t, operationOf(t, doc), "200", "text/event-stream")
	if _, declared := media["schema"]; declared {
		t.Errorf("the event stream carries a body schema: %v", media)
	}
}

// TestAnEventStreamMediaTypeIsNotStripped holds normalisation: a declared
// media type with parameters is published as the event stream media type, not
// dropped or rewritten.
func TestAnEventStreamMediaTypeIsNotStripped(t *testing.T) {
	doc := generate(t, speedhttp.Operations{speedhttp.NewReplyOperation[struct{}](
		"the.module.reply", http.MethodGet, "/events", "", "", nil,
		[]speedhttp.Response{{
			Status: 200, Description: "The events",
			Body: &speedhttp.Body{MediaType: "text/event-stream; charset=utf-8"},
		}},
	)})
	media := contentOf(t, operationOf(t, doc), "200", "text/event-stream")
	if _, declared := media["schema"]; declared {
		t.Errorf("the event stream carries a body schema: %v", media)
	}
}

// TestAnOctetStreamAnswerIsBinaryWhateverItsStructureNames holds the binary
// row: the entry point writes an octet-stream answer as raw bytes, so the
// fragment must publish a binary string even when the declaration names a
// Structure.
func TestAnOctetStreamAnswerIsBinaryWhateverItsStructureNames(t *testing.T) {
	doc := generate(t, speedhttp.Operations{speedhttp.NewReplyOperation[struct{}](
		"the.module.reply", http.MethodGet, "/blob", "", "", nil,
		[]speedhttp.Response{{
			Status: 200, Description: "The bytes",
			Body: &speedhttp.Body{MediaType: "application/octet-stream", Structure: reflect.TypeOf(document{})},
		}},
	)})
	schema := contentOf(t, operationOf(t, doc), "200", "application/octet-stream")["schema"].(map[string]interface{})
	if schema["type"] != "string" || schema["format"] != "binary" {
		t.Errorf("the octet-stream schema reads %v, want a binary string", schema)
	}
	if _, referenced := schema["$ref"]; referenced {
		t.Errorf("the octet-stream schema references a structure: %v", schema)
	}
}

// TestABinaryAnswerOfAnotherMediaTypeIsBinary holds the general binary row:
// any media type the entry point writes as bytes is published as a binary
// string, not as text.
func TestABinaryAnswerOfAnotherMediaTypeIsBinary(t *testing.T) {
	doc := generate(t, speedhttp.Operations{speedhttp.NewReplyOperation[struct{}](
		"the.module.reply", http.MethodGet, "/image", "", "", nil,
		[]speedhttp.Response{{
			Status: 200, Description: "The image",
			Body: &speedhttp.Body{MediaType: "image/png"},
		}},
	)})
	schema := contentOf(t, operationOf(t, doc), "200", "image/png")["schema"].(map[string]interface{})
	if schema["type"] != "string" || schema["format"] != "binary" {
		t.Errorf("the image schema reads %v, want a binary string", schema)
	}
}

// TestATextAnswerOfAnotherMediaTypeIsAString holds the text side: a text media
// type is a string, not binary octets.
func TestATextAnswerOfAnotherMediaTypeIsAString(t *testing.T) {
	doc := generate(t, speedhttp.Operations{speedhttp.NewReplyOperation[struct{}](
		"the.module.reply", http.MethodGet, "/text", "", "", nil,
		[]speedhttp.Response{{
			Status: 200, Description: "The text",
			Body: &speedhttp.Body{MediaType: "text/plain"},
		}},
	)})
	schema := contentOf(t, operationOf(t, doc), "200", "text/plain")["schema"].(map[string]interface{})
	if schema["type"] != "string" {
		t.Errorf("the text schema reads %v, want a string", schema)
	}
	if _, binary := schema["format"]; binary {
		t.Errorf("the text schema is published as binary: %v", schema)
	}
}

// TestAJSONAnswerWithoutAStructureIsRefused holds the other side of the JSON
// row: the entry point accepts any JSON value when the declaration names no
// structure, so such a declaration is refused at generation rather than
// published with the reflector's unconstrained default.
func TestAJSONAnswerWithoutAStructureIsRefused(t *testing.T) {
	_, err := Generate("the.module", "1.0.0", speedhttp.Operations{speedhttp.NewReplyOperation[struct{}](
		"the.module.reply", http.MethodGet, "/items", "", "", nil,
		[]speedhttp.Response{{
			Status: 200, Description: "The answer",
			Body: &speedhttp.Body{MediaType: "application/json"},
		}},
	)})
	if err == nil {
		t.Fatal("generating a JSON answer with no Structure succeeded")
	}
	if message := err.Error(); !strings.Contains(message, "without a Structure") {
		t.Errorf("the error reads %q, want it to name the missing Structure", message)
	}
}

// TestARedirectPublishesItsLocationAndNoContent holds the redirect shape: the
// status and Location header are published and the answer carries no content.
func TestARedirectPublishesItsLocationAndNoContent(t *testing.T) {
	doc := generate(t, speedhttp.Operations{speedhttp.NewReplyOperation[struct{}](
		"the.module.reply", http.MethodGet, "/old", "", "", nil,
		[]speedhttp.Response{{
			Status: 303, Description: "See the new location",
			Headers: []speedhttp.Header{{Name: "Location", Description: "Where the client goes next.", Value: "/new"}},
		}},
	)})
	response := operationOf(t, doc)["responses"].(map[string]interface{})["303"].(map[string]interface{})
	if content, ok := response["content"]; ok {
		t.Errorf("the redirect publishes content: %v", content)
	}
	header := response["headers"].(map[string]interface{})["Location"].(map[string]interface{})
	if header["description"] != "Where the client goes next." {
		t.Errorf("the Location description reads %v", header["description"])
	}
}

// TestANoBodyStatusPublishesNoContent holds the empty answers: a 204 and a 304
// carry a status and no content.
func TestANoBodyStatusPublishesNoContent(t *testing.T) {
	for _, status := range []int{http.StatusNoContent, http.StatusNotModified} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			doc := generate(t, speedhttp.Operations{speedhttp.NewReplyOperation[struct{}](
				"the.module.reply", http.MethodGet, "/items", "", "", nil,
				[]speedhttp.Response{{Status: status, Description: "No body"}},
			)})
			response := operationOf(t, doc)["responses"].(map[string]interface{})[strconv.Itoa(status)].(map[string]interface{})
			if content, ok := response["content"]; ok {
				t.Errorf("the %d response publishes content: %v", status, content)
			}
		})
	}
}

// TestABodyOnANoBodyStatusIsRefused holds the other side: pkg/http refuses a
// body on a status that carries none, so the generator refuses it too.
func TestABodyOnANoBodyStatusIsRefused(t *testing.T) {
	_, err := Generate("the.module", "1.0.0", speedhttp.Operations{speedhttp.NewReplyOperation[struct{}](
		"the.module.reply", http.MethodGet, "/items", "", "", nil,
		[]speedhttp.Response{{
			Status: http.StatusNoContent, Description: "No body",
			Body: &speedhttp.Body{MediaType: "application/json", Structure: reflect.TypeOf(document{})},
		}},
	)})
	if err == nil {
		t.Fatal("generating a body on a 204 succeeded")
	}
}

// simpleInput is the simple form's input: a parameter and a JSON body member.
type simpleInput struct {
	Q    string `query:"q"`
	Name string `json:"name" required:"true" description:"The name."`
}

// TestTheSimpleFormPublishesParametersBodyAndSuccess holds the simple form: the
// parameters, the request body and the success body taken from Out are all
// published, and the success body is JSON even when the declaration leaves the
// media type out.
func TestTheSimpleFormPublishesParametersBodyAndSuccess(t *testing.T) {
	doc := generate(t, speedhttp.Operations{speedhttp.NewOperation[simpleInput, document](
		"the.module.write", http.MethodPost, "/items", "", "", requestBody("application/json"),
		[]speedhttp.Response{{Status: 200, Description: "The document"}},
	)})
	operation := operationOfMethod(t, doc, "post")
	if parameters := parametersOf(t, operation); parameters["q"] == nil {
		t.Errorf("the request parameters read %v, want q", parameters)
	}
	request := requestContentOf(t, operation)["application/json"].(map[string]interface{})
	if request["schema"].(map[string]interface{})["properties"].(map[string]interface{})["name"] == nil {
		t.Errorf("the request body carries no json field: %v", request)
	}
	body := contentOf(t, operation, "200", "application/json")
	if ref := body["schema"].(map[string]interface{})["$ref"]; ref != "#/components/schemas/document" {
		t.Errorf("the success body references %v, want Out's schema", ref)
	}
}

// TestASimpleSuccessOfAnotherMediaTypeIsRefused holds the simple form's one
// media type: the entry point writes it as JSON, so a declaration naming
// another media type is refused rather than published.
func TestASimpleSuccessOfAnotherMediaTypeIsRefused(t *testing.T) {
	_, err := Generate("the.module", "1.0.0", speedhttp.Operations{speedhttp.NewOperation[struct{}, document](
		"the.module.write", http.MethodPost, "/items", "", "", nil,
		[]speedhttp.Response{{
			Status: 200, Description: "The document",
			Body: &speedhttp.Body{MediaType: "text/event-stream"},
		}},
	)})
	if err == nil {
		t.Fatal("generating a simple success of another media type succeeded")
	}
}

// TestAnEmptySimpleOutPublishesTheEmptyObjectBody holds the simple form's
// empty success body: the entry point writes an empty Out as {} for every
// success status but a no-body one, whatever body the declaration names. The
// fragment must publish that JSON object rather than no content, or a body the
// entry point never writes.
func TestAnEmptySimpleOutPublishesTheEmptyObjectBody(t *testing.T) {
	cases := []struct {
		name string
		body *speedhttp.Body
	}{
		{name: "no body declared"},
		{
			name: "a JSON body naming a structure",
			body: &speedhttp.Body{MediaType: "application/json", Structure: reflect.TypeOf(document{})},
		},
		{
			name: "a JSON body naming no structure",
			body: &speedhttp.Body{MediaType: "application/json"},
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			doc := generate(t, speedhttp.Operations{speedhttp.NewOperation[struct{}, struct{}](
				"the.module.write", http.MethodPost, "/items", "", "", nil,
				[]speedhttp.Response{{Status: http.StatusOK, Description: "Written", Body: testCase.body}},
			)})
			media := contentOf(t, operationOfMethod(t, doc, "post"), "200", "application/json")
			schema := media["schema"].(map[string]interface{})
			if schema["type"] != "object" {
				t.Errorf("the success body schema reads %v, want an object", schema)
			}
			if _, referenced := schema["$ref"]; referenced {
				t.Errorf("the success body references another structure: %v", schema)
			}
		})
	}
}

// TestAReplyBodyWithoutAMediaTypeIsRefused holds the reply side: the entry
// point writes a reply body with its declared media type, and a JSON reply
// whose declaration leaves the media type out is refused at run time. A
// fragment cannot name an empty media type, so the declaration is refused
// instead of published as JSON.
func TestAReplyBodyWithoutAMediaTypeIsRefused(t *testing.T) {
	cases := []struct {
		name string
		body *speedhttp.Body
	}{
		{name: "with a structure", body: &speedhttp.Body{Structure: reflect.TypeOf(document{})}},
		{name: "without a structure", body: &speedhttp.Body{}},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			_, err := Generate("the.module", "1.0.0", speedhttp.Operations{speedhttp.NewReplyOperation[struct{}](
				"the.module.reply", http.MethodGet, "/items", "", "", nil,
				[]speedhttp.Response{{Status: http.StatusOK, Description: "The answer", Body: testCase.body}},
			)})
			if err == nil {
				t.Fatal("generating a reply body with no media type succeeded")
			}
			if message := err.Error(); !strings.Contains(message, "no media type") {
				t.Errorf("the error reads %q, want it to name the missing media type", message)
			}
		})
	}
}

// unbindableFloatQuery declares a float parameter.
type unbindableFloatQuery struct {
	Ratio float64 `query:"ratio"`
}

// unbindableUintQuery declares an unsigned parameter.
type unbindableUintQuery struct {
	Count uint `query:"count"`
}

// unbindableTimeQuery declares a struct-valued parameter.
type unbindableTimeQuery struct {
	At time.Time `query:"at"`
}

// unbindableSliceHeader declares a repeated header parameter.
type unbindableSliceHeader struct {
	Values []string `header:"X-Values"`
}

// bindableSliceQuery declares a repeated query parameter.
type bindableSliceQuery struct {
	Values []string `query:"values"`
}

// unbindableFloatForm declares a float form field.
type unbindableFloatForm struct {
	Ratio float64 `form:"ratio"`
}

// TestAParameterTypeTheEntryPointCannotBindIsRefused holds the parameter
// surface: pkg/http binds a string, a signed integer or a bool, and a slice of
// those for a query parameter only. Any other type answers 400 for a request
// carrying the parameter, so the fragment must not publish it.
func TestAParameterTypeTheEntryPointCannotBindIsRefused(t *testing.T) {
	cases := []struct {
		name      string
		operation speedhttp.Declared
		message   string
	}{
		{
			name: "a float query parameter",
			operation: speedhttp.NewOperation[unbindableFloatQuery, struct{}](
				"the.module.read", http.MethodGet, "/items", "", "", nil,
				[]speedhttp.Response{{Status: http.StatusNoContent, Description: "No content"}},
			),
			message: "query parameter",
		},
		{
			name: "an unsigned query parameter",
			operation: speedhttp.NewOperation[unbindableUintQuery, struct{}](
				"the.module.read", http.MethodGet, "/items", "", "", nil,
				[]speedhttp.Response{{Status: http.StatusNoContent, Description: "No content"}},
			),
			message: "query parameter",
		},
		{
			name: "a time query parameter",
			operation: speedhttp.NewOperation[unbindableTimeQuery, struct{}](
				"the.module.read", http.MethodGet, "/items", "", "", nil,
				[]speedhttp.Response{{Status: http.StatusNoContent, Description: "No content"}},
			),
			message: "query parameter",
		},
		{
			name: "a repeated header parameter",
			operation: speedhttp.NewOperation[unbindableSliceHeader, struct{}](
				"the.module.read", http.MethodGet, "/items", "", "", nil,
				[]speedhttp.Response{{Status: http.StatusNoContent, Description: "No content"}},
			),
			message: "header parameter",
		},
		{
			name: "a float form field",
			operation: speedhttp.NewOperation[unbindableFloatForm, struct{}](
				"the.module.write", http.MethodPost, "/items", "", "", requestBody(mediaTypeForm),
				[]speedhttp.Response{{Status: http.StatusNoContent, Description: "No content"}},
			),
			message: "form field",
		},
		{
			name: "a float multipart form field",
			operation: speedhttp.NewOperation[unbindableFloatForm, struct{}](
				"the.module.write", http.MethodPost, "/items", "", "", requestBody(mediaTypeMultipart),
				[]speedhttp.Response{{Status: http.StatusNoContent, Description: "No content"}},
			),
			message: "form field",
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			_, err := Generate("the.module", "1.0.0", speedhttp.Operations{testCase.operation})
			if err == nil {
				t.Fatal("generating an unbindable declaration succeeded")
			}
			if message := err.Error(); !strings.Contains(message, testCase.message) {
				t.Errorf("the error reads %q, want it to name the %s", message, testCase.message)
			}
		})
	}
}

// TestARepeatedQueryParameterPublishesItsSliceType holds the supported side: a
// repeated query parameter is a slice of a bindable scalar and is published as
// an array.
func TestARepeatedQueryParameterPublishesItsSliceType(t *testing.T) {
	doc := generate(t, speedhttp.Operations{speedhttp.NewOperation[bindableSliceQuery, struct{}](
		"the.module.read", http.MethodGet, "/items", "", "", nil,
		[]speedhttp.Response{{Status: http.StatusNoContent, Description: "No content"}},
	)})
	schema := parametersOf(t, operationOf(t, doc))["values"]["schema"].(map[string]interface{})
	if schema["type"] != "array" {
		t.Fatalf("the parameter schema reads %v, want an array", schema)
	}
	items := schema["items"].(map[string]interface{})
	if items["type"] != "string" {
		t.Errorf("the parameter items read %v, want strings", items)
	}
}

// collisionDocument declares members whose names are also the members a header
// or parameter object carries.
type collisionDocument struct {
	In     string `json:"in" description:"The in member."`
	Name   string `json:"name" description:"The name member."`
	Schema string `json:"schema" description:"The schema member."`
	Style  string `json:"style" description:"The style member."`
}

// TestABusinessSchemaKeepsMembersNamedLikeAParameterOrHeader holds the
// normalisation boundary: the rules that strip a parameter's duplicated
// description and a header's default style belong to those objects, and a
// business schema's properties map must keep every member and description even
// when the member names match. The entry point binds and writes all of these
// members, so dropping one publishes a shape narrower than the contract.
func TestABusinessSchemaKeepsMembersNamedLikeAParameterOrHeader(t *testing.T) {
	doc := generate(t, speedhttp.Operations{speedhttp.NewOperation[struct{}, collisionDocument](
		"the.module.read", http.MethodGet, "/items", "", "", nil,
		[]speedhttp.Response{{
			Status: 200, Description: "The item",
			Body: &speedhttp.Body{MediaType: "application/json"},
		}},
	)})
	properties := component(t, doc, "collisionDocument")["properties"].(map[string]interface{})
	for _, name := range []string{"in", "name", "schema", "style"} {
		if properties[name] == nil {
			t.Errorf("the schema drops the %q member: %v", name, properties)
		}
	}
	schema, ok := properties["schema"].(map[string]interface{})
	if !ok {
		t.Fatalf("the schema member reads %v", properties["schema"])
	}
	if schema["description"] != "The schema member." {
		t.Errorf("the schema member's description reads %v, want the declared one", schema["description"])
	}
}

// TestABusinessRequestSchemaKeepsMembersNamedLikeAParameterOrHeader holds the
// same boundary on the structured request side, where the entry point binds
// every described member.
func TestABusinessRequestSchemaKeepsMembersNamedLikeAParameterOrHeader(t *testing.T) {
	doc := generate(t, speedhttp.Operations{speedhttp.NewOperation[collisionDocument, struct{}](
		"the.module.write", http.MethodPost, "/items", "", "", requestBody(mediaTypeJSON),
		[]speedhttp.Response{{Status: http.StatusNoContent, Description: "No content"}},
	)})
	media := requestContentOf(t, operationOfMethod(t, doc, "post"))[mediaTypeJSON].(map[string]interface{})
	properties := media["schema"].(map[string]interface{})["properties"].(map[string]interface{})
	for _, name := range []string{"in", "name", "schema", "style"} {
		if properties[name] == nil {
			t.Errorf("the request schema drops the %q member: %v", name, properties)
		}
	}
	schema, ok := properties["schema"].(map[string]interface{})
	if !ok {
		t.Fatalf("the schema member reads %v", properties["schema"])
	}
	if schema["description"] != "The schema member." {
		t.Errorf("the schema member's description reads %v, want the declared one", schema["description"])
	}
}

// TestASimpleOperationsFailurePublishesItsDeclaredHeaders holds the write side:
// the entry point writes the declared headers of the response a failure names,
// so a failure's headers are part of the contract like any other.
func TestASimpleOperationsFailurePublishesItsDeclaredHeaders(t *testing.T) {
	doc := generate(t, speedhttp.Operations{speedhttp.NewOperation[struct{}, document](
		"the.module.read", http.MethodGet, "/items", "", "", nil,
		[]speedhttp.Response{
			{
				Status: 200, Description: "The items",
				Headers: []speedhttp.Header{{Name: "ETag", Description: "The version.", Value: `"1"`}},
				Body:    &speedhttp.Body{MediaType: "application/json"},
			},
			{
				Status: http.StatusTooManyRequests, Description: "Too many requests",
				Headers: []speedhttp.Header{{Name: "Retry-After", Description: "When to retry.", Value: "60"}},
			},
		},
	)})
	responses := operationOf(t, doc)["responses"].(map[string]interface{})
	success := responses["200"].(map[string]interface{})
	if headers, ok := success["headers"].(map[string]interface{}); !ok || headers["ETag"] == nil {
		t.Errorf("the success response drops its declared header: %v", success)
	}
	failure := responses["429"].(map[string]interface{})
	if headers, ok := failure["headers"].(map[string]interface{}); !ok || headers["Retry-After"] == nil {
		t.Errorf("the failure response drops its declared header: %v", failure)
	}
}

// TestASimpleOperationWithTwoSuccessStatusesIsRefused holds the declaration the
// entry point refuses when the handler is built: the simple writer answers one
// success status, so the generator must not publish two.
func TestASimpleOperationWithTwoSuccessStatusesIsRefused(t *testing.T) {
	operations := speedhttp.Operations{speedhttp.NewOperation[struct{}, document](
		"the.module.read", http.MethodGet, "/items", "", "", nil,
		[]speedhttp.Response{
			{Status: 200, Description: "The items", Body: &speedhttp.Body{MediaType: "application/json"}},
			{Status: 201, Description: "The items, created", Body: &speedhttp.Body{MediaType: "application/json"}},
		},
	)}
	if _, err := Generate("the.module", "1.0.0", operations); err == nil {
		t.Fatal("generating an operation with two success statuses succeeded")
	}
}

// TestAReplyOperationsFailurePublishesItsDeclaredHeaders holds the other form:
// a reply operation's handler builds the answer for a declared status, and the
// entry point writes that response's declared headers.
func TestAReplyOperationsFailurePublishesItsDeclaredHeaders(t *testing.T) {
	doc := generate(t, speedhttp.Operations{speedhttp.NewReplyOperation[struct{}](
		"the.module.reply", http.MethodGet, "/items", "", "", nil,
		[]speedhttp.Response{
			{
				Status: 200, Description: "The items",
				Body: &speedhttp.Body{MediaType: "application/json", Structure: reflect.TypeOf(document{})},
			},
			{
				Status: http.StatusTooManyRequests, Description: "Too many requests",
				Headers: []speedhttp.Header{{Name: "Retry-After", Description: "When to retry.", Value: "60"}},
			},
		},
	)})
	failure := operationOf(t, doc)["responses"].(map[string]interface{})["429"].(map[string]interface{})
	headers, ok := failure["headers"].(map[string]interface{})
	if !ok || headers["Retry-After"] == nil {
		t.Errorf("the failure response drops its declared header: %v", failure)
	}
}

// TestAnEventStreamPublishesTheNoStoreDefault holds the write side of an event
// stream: the entry point supplies Cache-Control: no-store whenever the
// response declares none, so the fragment must publish it.
func TestAnEventStreamPublishesTheNoStoreDefault(t *testing.T) {
	doc := generate(t, speedhttp.Operations{speedhttp.NewReplyOperation[struct{}](
		"the.module.reply", http.MethodGet, "/events", "", "", nil,
		[]speedhttp.Response{{
			Status: 200, Description: "The events",
			Body: &speedhttp.Body{MediaType: "text/event-stream"},
		}},
	)})
	response := operationOf(t, doc)["responses"].(map[string]interface{})["200"].(map[string]interface{})
	headers, ok := response["headers"].(map[string]interface{})
	if !ok {
		t.Fatalf("the event stream publishes no headers: %v", response)
	}
	cacheControl, ok := headers["Cache-Control"].(map[string]interface{})
	if !ok {
		t.Fatalf("the event stream publishes no Cache-Control: %v", headers)
	}
	schema, ok := cacheControl["schema"].(map[string]interface{})
	if !ok || schema["type"] != "string" {
		t.Errorf("the Cache-Control schema reads %v, want a string", cacheControl["schema"])
	}
}

// TestADeclaredCacheControlStandsOnAnEventStream holds the default's limit: a
// declaration that names Cache-Control is the one published, and no second
// entry replaces it.
func TestADeclaredCacheControlStandsOnAnEventStream(t *testing.T) {
	doc := generate(t, speedhttp.Operations{speedhttp.NewReplyOperation[struct{}](
		"the.module.reply", http.MethodGet, "/events", "", "", nil,
		[]speedhttp.Response{{
			Status: 200, Description: "The events",
			Headers: []speedhttp.Header{{Name: "Cache-Control", Description: "No cache, but revalidate.", Value: "no-cache"}},
			Body:    &speedhttp.Body{MediaType: "text/event-stream"},
		}},
	)})
	response := operationOf(t, doc)["responses"].(map[string]interface{})["200"].(map[string]interface{})
	headers := response["headers"].(map[string]interface{})
	if len(headers) != 1 {
		t.Errorf("the event stream publishes %d headers, want the declared one: %v", len(headers), headers)
	}
	cacheControl := headers["Cache-Control"].(map[string]interface{})
	if cacheControl["description"] != "No cache, but revalidate." {
		t.Errorf("the Cache-Control reads %v, want the declared header", cacheControl)
	}
}

// TestEveryRequestBodyObjectIsClosed holds the binding boundary: the entry
// point refuses a JSON member, form key or multipart part the declaration does
// not carry, so every generated request body object forbids additional
// properties.
func TestEveryRequestBodyObjectIsClosed(t *testing.T) {
	cases := []struct {
		name      string
		operation speedhttp.Declared
		mediaType string
	}{
		{
			name: "JSON",
			operation: speedhttp.NewOperation[jsonInput, struct{}](
				"the.module.write", http.MethodPost, "/items/:id", "", "", requestBody(mediaTypeJSON),
				[]speedhttp.Response{{Status: http.StatusNoContent, Description: "No content"}},
			),
			mediaType: mediaTypeJSON,
		},
		{
			name: "form",
			operation: speedhttp.NewOperation[formInput, struct{}](
				"the.module.write", http.MethodPost, "/items/:id", "", "", requestBody(mediaTypeForm),
				[]speedhttp.Response{{Status: http.StatusNoContent, Description: "No content"}},
			),
			mediaType: mediaTypeForm,
		},
		{
			name: "multipart",
			operation: speedhttp.NewOperation[multipartInput, struct{}](
				"the.module.write", http.MethodPost, "/items/:id", "", "", requestBody(mediaTypeMultipart),
				[]speedhttp.Response{{Status: http.StatusNoContent, Description: "No content"}},
			),
			mediaType: mediaTypeMultipart,
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			doc := generate(t, speedhttp.Operations{testCase.operation})
			media := requestContentOf(t, operationOfMethod(t, doc, "post"))[testCase.mediaType].(map[string]interface{})
			schema := media["schema"].(map[string]interface{})
			if closed, ok := schema["additionalProperties"].(bool); !ok || closed {
				t.Errorf("the request body schema reads %v, want additionalProperties false", schema)
			}
		})
	}
}

// operationOf returns the one GET operation under /items or /items/{id}.
func operationOf(t *testing.T, doc map[string]interface{}) map[string]interface{} {
	t.Helper()
	return operationOfMethod(t, doc, "get")
}

// operationOfMethod returns the one operation with the given method.
func operationOfMethod(t *testing.T, doc map[string]interface{}, method string) map[string]interface{} {
	t.Helper()
	paths := doc["paths"].(map[string]interface{})
	for _, item := range paths {
		operations := item.(map[string]interface{})
		if operation, ok := operations[method].(map[string]interface{}); ok {
			return operation
		}
	}
	t.Fatalf("the document has no %s operation: %v", strings.ToUpper(method), paths)
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

// requestContentOf returns the request body's content map.
func requestContentOf(t *testing.T, operation map[string]interface{}) map[string]interface{} {
	t.Helper()
	body, ok := operation["requestBody"].(map[string]interface{})
	if !ok {
		t.Fatalf("the operation carries no request body: %v", operation)
	}
	return body["content"].(map[string]interface{})
}

// contentOf returns one response's media type entry.
func contentOf(t *testing.T, operation map[string]interface{}, status, mediaType string) map[string]interface{} {
	t.Helper()
	response := operation["responses"].(map[string]interface{})[status].(map[string]interface{})
	content, ok := response["content"].(map[string]interface{})
	if !ok {
		t.Fatalf("the %s response carries no content: %v", status, response)
	}
	media, ok := content[mediaType].(map[string]interface{})
	if !ok {
		t.Fatalf("the %s response carries %v, want %s", status, content, mediaType)
	}
	return media
}

// parametersOf returns an operation's parameters by name.
func parametersOf(t *testing.T, operation map[string]interface{}) map[string]map[string]interface{} {
	t.Helper()
	raw, ok := operation["parameters"].([]interface{})
	if !ok {
		return nil
	}
	parameters := make(map[string]map[string]interface{}, len(raw))
	for _, entry := range raw {
		parameter := entry.(map[string]interface{})
		parameters[parameter["name"].(string)] = parameter
	}
	return parameters
}

// resolveSchema follows a schema's component reference. A field with no
// reference is returned as it is.
func resolveSchema(t *testing.T, doc map[string]interface{}, schema map[string]interface{}) map[string]interface{} {
	t.Helper()
	ref, ok := schema["$ref"].(string)
	if !ok {
		return schema
	}
	const prefix = "#/components/schemas/"
	if !strings.HasPrefix(ref, prefix) {
		t.Fatalf("the schema reference reads %q, want a component schema", ref)
	}
	return component(t, doc, strings.TrimPrefix(ref, prefix))
}

// TestARequestBodyPublishesItsDescriptionAndRequired holds the request body's
// metadata: the media type, the description as the body's own description and
// the required flag are all published.
func TestARequestBodyPublishesItsDescriptionAndRequired(t *testing.T) {
	doc := generate(t, speedhttp.Operations{speedhttp.NewOperation[jsonInput, struct{}](
		"the.module.write", http.MethodPost, "/items/:id", "", "",
		&speedhttp.RequestBody{
			MediaType:   "application/json",
			Description: "The item to write.",
			Required:    true,
		},
		[]speedhttp.Response{{Status: http.StatusNoContent, Description: "No content"}},
	)})
	body := operationOfMethod(t, doc, "post")["requestBody"].(map[string]interface{})
	if body["description"] != "The item to write." {
		t.Errorf("the request body description reads %v, want the declared one", body["description"])
	}
	if body["required"] != true {
		t.Errorf("the request body required reads %v, want true", body["required"])
	}
	if _, ok := body["content"].(map[string]interface{})["application/json"]; !ok {
		t.Errorf("the request body carries %v, want application/json", body["content"])
	}
}

// TestADynamicMediaTypeAnswerPublishesABinaryContentEntry holds the "*/*"
// response body: the declared media type is published with a binary schema,
// because a handler-chosen media type can only describe bytes.
func TestADynamicMediaTypeAnswerPublishesABinaryContentEntry(t *testing.T) {
	doc := generate(t, speedhttp.Operations{speedhttp.NewReplyOperation[struct{}](
		"the.module.reply", http.MethodGet, "/image", "", "", nil,
		[]speedhttp.Response{{
			Status: 200, Description: "An image the handler chooses",
			Body: &speedhttp.Body{MediaType: "*/*"},
		}},
	)})
	response := operationOf(t, doc)["responses"].(map[string]interface{})["200"].(map[string]interface{})
	content := response["content"].(map[string]interface{})
	if len(content) != 1 {
		t.Fatalf("the answer publishes %v, want only the dynamic media type", content)
	}
	media, ok := content["*/*"].(map[string]interface{})
	if !ok {
		t.Fatalf("the answer carries %v, want a */* entry", content)
	}
	schema := media["schema"].(map[string]interface{})
	if schema["type"] != "string" || schema["format"] != "binary" {
		t.Errorf("the dynamic body schema reads %v, want a binary string", schema)
	}
}

// TestEachAccessLevelIsPublishedAsItsSecurityRequirement pins the mapping from
// a declaration's Access to the fragment's security: a guarded operation names
// both carriers, an optional one adds the alternative with no credential, and a
// public one carries the empty array that removes the requirement.
func TestEachAccessLevelIsPublishedAsItsSecurityRequirement(t *testing.T) {
	answer := []speedhttp.Response{{Status: 200, Description: "The answer"}}
	doc := generate(t, speedhttp.Operations{
		speedhttp.NewOperation[struct{}, document](
			"the.module.guarded", http.MethodGet, "/guarded", "", "", nil, answer),
		speedhttp.NewOperation[struct{}, document](
			"the.module.optional", http.MethodGet, "/optional", "", "", nil, answer, speedhttp.Optional()),
		speedhttp.NewOperation[struct{}, document](
			"the.module.public", http.MethodGet, "/public", "", "", nil, answer, speedhttp.Public()),
	})

	paths := doc["paths"].(map[string]interface{})
	security := func(path string) interface{} {
		t.Helper()
		operation, ok := paths[path].(map[string]interface{})["get"].(map[string]interface{})
		if !ok {
			t.Fatalf("the fragment carries no GET %s: %v", path, paths[path])
		}
		return operation["security"]
	}

	guarded, _ := json.Marshal(security("/guarded"))
	if string(guarded) != `[{"bearerAuth":[]},{"sessionCookie":[]}]` {
		t.Errorf("the guarded operation carries security %s, want both carriers", guarded)
	}
	optional, _ := json.Marshal(security("/optional"))
	if string(optional) != `[{},{"bearerAuth":[]},{"sessionCookie":[]}]` {
		t.Errorf("the optional operation carries security %s, want both carriers and the anonymous alternative", optional)
	}
	public, _ := json.Marshal(security("/public"))
	if string(public) != `[]` {
		t.Errorf("the public operation carries security %s, want the empty array", public)
	}
}
