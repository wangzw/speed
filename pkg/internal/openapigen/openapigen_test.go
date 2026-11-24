package openapigen

import (
	"bytes"
	"encoding/json"
	"net/http"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	speedhttp "github.com/vislake/speed/pkg/http"
)

type document struct {
	Items []string `json:"items" required:"true" description:"The items."`
}

type nullableFields struct {
	Marked *time.Time `json:"marked" nullable:"true" description:"An explicitly nullable instant."`
	Plain  *time.Time `json:"plain,omitempty" description:"An instant the tag omits when it is nil."`
}

type unmarkedResponsePointer struct {
	Count *int `json:"count" description:"A count with no declaration for its absent state."`
}

type nestedUnmarkedPointer struct {
	Inner struct {
		Count *int `json:"count" description:"A count with no declaration for its absent state."`
	} `json:"inner" required:"true"`
}

type pointerInput struct {
	Enabled *bool `json:"enabled" description:"Absent leaves the flag as it is."`
}

type wronglyNullable struct {
	Value string `json:"value" nullable:"true" description:"A value that cannot be null."`
}

type nestedDocument struct {
	Text string `json:"text" required:"true" description:"A field of the nested structure."`
}

type nullableStructPointer struct {
	Nested *nestedDocument `json:"nested" nullable:"true" description:"A structure published by $ref."`
}

type nullableOmitEmpty struct {
	Marked *time.Time `json:"marked,omitempty" nullable:"true" description:"An instant the tag omits."`
}

type explicitNotNull struct {
	Items []string `json:"items" required:"true" nullable:"false" description:"A slice that is never null."`
}

type pointerItems struct {
	Instants []*time.Time `json:"instants" required:"true" description:"Instants that are always present."`
}

type pointerElementItems struct {
	Counts []*int `json:"counts" required:"true" description:"Counts that may be nil."`
}

type pointerInMapValue struct {
	ByName map[string]*int `json:"by_name" required:"true" description:"Values that may be nil."`
}

type pointerInArray struct {
	Counts [3]*int `json:"counts" required:"true" description:"Counts reached through an array."`
}

type pointerInNestedSlice struct {
	Counts [][]*int `json:"counts" required:"true" description:"Counts reached through nested slices."`
}

type pointerInMapOfSlices struct {
	ByName map[string][]*int `json:"by_name" required:"true" description:"Values reached through a map of slices."`
}

type pointerInSliceOfMaps struct {
	ByName []map[string]*int `json:"by_name" required:"true" description:"Values reached through a slice of maps."`
}

type pointerInSliceOfMapOfArrays struct {
	Counts []map[string][2]*int `json:"counts" required:"true" description:"Counts reached through a slice of maps of arrays."`
}

type inlineStruct struct {
	Text string `json:"text" required:"true" description:"An inlined field."`
}

type inlineAccepted struct {
	*inlineStruct
	Named string `json:"named" required:"true" description:"A named field."`
}

type dashedKeyPointer struct {
	Value *int `json:"-," description:"A value under the key -."` //nolint:staticcheck // SA5008 suggests json:"'-',", but encoding/json rejects that name and falls back to the Go field name; json:"-," is the only spelling that publishes "-".
}

type innerPointer struct {
	Count *int `json:"count" description:"A count with no declaration for its absent state."`
}

type pointerInSlice struct {
	Items []innerPointer `json:"items" required:"true"`
}

type pointerBehindPointer struct {
	Inner *innerPointer `json:"inner,omitempty"`
}

type pointerInMap struct {
	ByName map[string]innerPointer `json:"by_name" required:"true"`
}

type embeddedUnexported struct {
	innerPointer
}

type taggedEmbedded struct {
	*innerPointer `json:"inner"`
}

// EmbeddedScalar is an exported non-struct type.
// Embedded as a pointer with no json tag, encoding/json publishes it as a property named after the
// type, so a nil one is written as null rather than inlined away.
type EmbeddedScalar int

type embeddedScalarPointer struct {
	*EmbeddedScalar
}

type sharedShape struct {
	Value *int `json:"value" description:"A value with no declaration for its absent state."`
}

type formOmitEmpty struct {
	Value *int `json:"value" form:"value,omitempty" description:"A value with no json omitempty."`
}

type omitZeroPointer struct {
	Value *int `json:"value,omitzero" description:"A value the tag omits when nil."`
}

type nullableOmitZero struct {
	Value *int `json:"value,omitzero" nullable:"true" description:"An explicitly nullable value."`
}

type outerNestedFields struct {
	Inner struct {
		Marked *time.Time `json:"marked" nullable:"true" description:"An explicitly nullable instant."`
		Plain  *time.Time `json:"plain,omitempty" description:"An instant the tag omits when it is nil."`
	} `json:"inner" required:"true" description:"The nested object."`
}

type query struct {
	Limit int    `query:"limit" minimum:"0" description:"Page size."`
	ID    string `path:"id" description:"The item id."`
}

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

// TestAPathParameterIsRewritten pins the gin-to-OpenAPI spelling: a declared route writes ":id"
// and the document writes "{id}".
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

// TestParametersComeFromTheInputStruct checks that path and query tags publish their parameters, the
// path one is required, and the schema omits the description.
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

type doubleTagged struct {
	Value string `query:"value" header:"Value"`
}

// TestAParameterIsPublishedOnceUnderTheBoundSource holds the precedence: pkg/http binds a field
// carrying two parameter tags from the first one only, so the fragment must not promise the other.
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

type parameterSources struct {
	ID     string `path:"id"`
	Limit  int    `query:"limit"`
	ETag   string `header:"If-None-Match"`
	Locale string `cookie:"locale"`
}

// TestEveryParameterSourceIsPublished holds the parameter surface: each of the four tags pkg/http
// binds is published under its source.
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

// TestResponseHeadersComeFromTheTags holds the header surface and the removal of the library's
// default header style.
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

// TestNullIsDroppedFromTypes holds the platform's default shape: no published field is declared
// nullable even though a Go slice reflects as one.
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

// TestAnExplicitlyNullablePointerKeepsNull checks that only the field tagged nullable:"true"
// keeps the string/null union, and the private marker reaches no fragment.
func TestAnExplicitlyNullablePointerKeepsNull(t *testing.T) {
	doc := generate(t, speedhttp.Operations{speedhttp.NewOperation[struct{}, nullableFields](
		"the.module.read", http.MethodGet, "/items", "", "", nil,
		[]speedhttp.Response{{
			Status: 200, Description: "The fields",
			Body: &speedhttp.Body{MediaType: "application/json"},
		}},
	)})
	properties := component(t, doc, "nullableFields")["properties"].(map[string]interface{})
	marked := properties["marked"].(map[string]interface{})
	types, ok := marked["type"].([]interface{})
	if !ok {
		t.Fatalf("the marked field's type is %v, want the [string null] union", marked["type"])
	}
	if !slices.Contains(types, "string") || !slices.Contains(types, "null") {
		t.Errorf("the marked field's type is %v, want string and null", types)
	}
	if _, leaked := marked[nullableMarker]; leaked {
		t.Errorf("the private nullable marker reaches the fragment: %v", marked)
	}
	plain := properties["plain"].(map[string]interface{})
	if plain["type"] != "string" {
		t.Errorf("the unmarked pointer's type is %v, want a single string", plain["type"])
	}
	if _, leaked := plain[nullableMarker]; leaked {
		t.Errorf("the unmarked pointer carries the private marker: %v", plain)
	}
}

// TestMarkingANonPointerNullableIsRefused holds that a nullable:"true" on a value no null can
// represent stops the build instead of publishing a union.
func TestMarkingANonPointerNullableIsRefused(t *testing.T) {
	_, err := Generate("the.module", "1.0.0", speedhttp.Operations{speedhttp.NewOperation[struct{}, wronglyNullable](
		"the.module.read", http.MethodGet, "/items", "", "", nil,
		[]speedhttp.Response{{
			Status: 200, Description: "The fields",
			Body: &speedhttp.Body{MediaType: "application/json"},
		}},
	)})
	if err == nil {
		t.Fatal("generating accepted nullable:\"true\" on a non-pointer field")
	}
	if !strings.Contains(err.Error(), "non-pointer") {
		t.Errorf("the error reads %v, want it to name the non-pointer field", err)
	}
}

// TestMarkingAPointerToAStructNullablePublishesAnyOf checks that a nullable pointer to a struct
// becomes an anyOf of the null type and the $ref, with the description on the outer schema.
func TestMarkingAPointerToAStructNullablePublishesAnyOf(t *testing.T) {
	doc := generate(t, speedhttp.Operations{speedhttp.NewOperation[struct{}, nullableStructPointer](
		"the.module.read", http.MethodGet, "/items", "", "", nil,
		[]speedhttp.Response{{
			Status: 200, Description: "The fields",
			Body: &speedhttp.Body{MediaType: "application/json"},
		}},
	)})
	nested := component(t, doc, "nullableStructPointer")["properties"].(map[string]interface{})["nested"].(map[string]interface{})
	if _, carried := nested["$ref"]; carried {
		t.Fatalf("the marked structure property is still a bare $ref: %v", nested)
	}
	anyOf, ok := nested["anyOf"].([]interface{})
	if !ok || len(anyOf) != 2 {
		t.Fatalf("the marked structure property's anyOf = %v, want null and the $ref", nested["anyOf"])
	}
	null := anyOf[0].(map[string]interface{})
	if null["type"] != "null" {
		t.Errorf("the anyOf's first member = %v, want the null type", null)
	}
	ref := anyOf[1].(map[string]interface{})
	if ref["$ref"] != "#/components/schemas/nestedDocument" {
		t.Errorf("the anyOf's second member = %v, want the nestedDocument $ref", ref)
	}
	if nested["description"] != "A structure published by $ref." {
		t.Errorf("the wrapper's description = %v, want the tag's description on the outer schema", nested["description"])
	}
}

// TestAnUnmarkedResponsePointerIsRefused checks that a response pointer with neither omitempty
// nor nullable:"true" is refused, naming the field.
func TestAnUnmarkedResponsePointerIsRefused(t *testing.T) {
	_, err := Generate("the.module", "1.0.0", speedhttp.Operations{speedhttp.NewOperation[struct{}, unmarkedResponsePointer](
		"the.module.read", http.MethodGet, "/items", "", "", nil,
		[]speedhttp.Response{{
			Status: 200, Description: "The fields",
			Body: &speedhttp.Body{MediaType: "application/json"},
		}},
	)})
	if err == nil {
		t.Fatal("generating accepted a response pointer with no omitempty and no nullable:\"true\"")
	}
	if !strings.Contains(err.Error(), "count") {
		t.Errorf("the error reads %v, want it to name the count field", err)
	}
}

// TestAnUnmarkedNestedResponsePointerIsRefused holds that the response walk recurses: a pointer
// inside an inlined response object is refused too.
func TestAnUnmarkedNestedResponsePointerIsRefused(t *testing.T) {
	_, err := Generate("the.module", "1.0.0", speedhttp.Operations{speedhttp.NewOperation[struct{}, nestedUnmarkedPointer](
		"the.module.read", http.MethodGet, "/items", "", "", nil,
		[]speedhttp.Response{{
			Status: 200, Description: "The fields",
			Body: &speedhttp.Body{MediaType: "application/json"},
		}},
	)})
	if err == nil {
		t.Fatal("generating accepted a nested response pointer with no omitempty and no nullable:\"true\"")
	}
	if !strings.Contains(err.Error(), "inner.count") {
		t.Errorf("the error reads %v, want it to name the nested field", err)
	}
}

// TestARequestPointerNeedsNoNullableDeclaration checks that a request pointer is published as a
// single type, since an absent request value is never written as null.
func TestARequestPointerNeedsNoNullableDeclaration(t *testing.T) {
	doc := generate(t, speedhttp.Operations{speedhttp.NewOperation[pointerInput, struct{}](
		"the.module.write", http.MethodPost, "/items", "", "", requestBody("application/json"),
		[]speedhttp.Response{{Status: 204, Description: "No content"}},
	)})
	properties := requestContentOf(t, operationOfMethod(t, doc, "post"))["application/json"].(map[string]interface{})["schema"].(map[string]interface{})["properties"].(map[string]interface{})
	enabled := properties["enabled"].(map[string]interface{})
	if enabled["type"] != "boolean" {
		t.Errorf("the request pointer's type = %v, want a single boolean", enabled["type"])
	}
}

// TestRequestArrayItemsCarryNoNullUnion checks that a request array's items carry no null member
// in their type.
func TestRequestArrayItemsCarryNoNullUnion(t *testing.T) {
	doc := generate(t, speedhttp.Operations{speedhttp.NewOperation[pointerItems, struct{}](
		"the.module.write", http.MethodPost, "/items", "", "", requestBody("application/json"),
		[]speedhttp.Response{{Status: 204, Description: "No content"}},
	)})
	properties := requestContentOf(t, operationOfMethod(t, doc, "post"))["application/json"].(map[string]interface{})["schema"].(map[string]interface{})["properties"].(map[string]interface{})
	instants := properties["instants"].(map[string]interface{})
	if instants["type"] != "array" {
		t.Errorf("the request property's type is %v, want a single array", instants["type"])
	}
	item := instants["items"].(map[string]interface{})
	if types, union := item["type"].([]interface{}); union && slices.Contains(types, "null") {
		t.Errorf("the request item's type is the union %v, want no null member", types)
	}
}

// TestMarkingANullableFieldOmitEmptyIsRefused holds that a marker on a field whose tag omits it
// stops the build: the contract would declare null while the answer only ever leaves the key out.
func TestMarkingANullableFieldOmitEmptyIsRefused(t *testing.T) {
	_, err := Generate("the.module", "1.0.0", speedhttp.Operations{speedhttp.NewOperation[struct{}, nullableOmitEmpty](
		"the.module.read", http.MethodGet, "/items", "", "", nil,
		[]speedhttp.Response{{
			Status: 200, Description: "The fields",
			Body: &speedhttp.Body{MediaType: "application/json"},
		}},
	)})
	if err == nil {
		t.Fatal("generating accepted nullable:\"true\" on a field with omitempty")
	}
	if !strings.Contains(err.Error(), "omitempty") {
		t.Errorf("the error reads %v, want it to name omitempty", err)
	}
}

// TestAnExplicitlyNonNullableSliceStaysAnArray holds the declaration the library reads for a slice
// that must not join a null union.
func TestAnExplicitlyNonNullableSliceStaysAnArray(t *testing.T) {
	doc := generate(t, speedhttp.Operations{speedhttp.NewOperation[struct{}, explicitNotNull](
		"the.module.read", http.MethodGet, "/items", "", "", nil,
		[]speedhttp.Response{{
			Status: 200, Description: "The fields",
			Body: &speedhttp.Body{MediaType: "application/json"},
		}},
	)})
	items := component(t, doc, "explicitNotNull")["properties"].(map[string]interface{})["items"].(map[string]interface{})
	if items["type"] != "array" {
		t.Errorf("the marked slice's type is %v, want a single array", items["type"])
	}
}

// TestPointerSliceItemsAreRefused checks that a slice whose element is a pointer is refused,
// naming the field.
func TestPointerSliceItemsAreRefused(t *testing.T) {
	_, err := Generate("the.module", "1.0.0", speedhttp.Operations{speedhttp.NewOperation[struct{}, pointerItems](
		"the.module.read", http.MethodGet, "/items", "", "", nil,
		[]speedhttp.Response{{
			Status: 200, Description: "The fields",
			Body: &speedhttp.Body{MediaType: "application/json"},
		}},
	)})
	if err == nil {
		t.Fatal("generating accepted a slice whose element is a pointer")
	}
	if !strings.Contains(err.Error(), "instants") {
		t.Errorf("the error reads %v, want it to name the instants field", err)
	}
}

// TestAPointerElementInASliceIsRefused holds the same rule for a slice of pointers to a scalar.
func TestAPointerElementInASliceIsRefused(t *testing.T) {
	_, err := Generate("the.module", "1.0.0", speedhttp.Operations{speedhttp.NewOperation[struct{}, pointerElementItems](
		"the.module.read", http.MethodGet, "/items", "", "", nil,
		[]speedhttp.Response{{
			Status: 200, Description: "The fields",
			Body: &speedhttp.Body{MediaType: "application/json"},
		}},
	)})
	if err == nil {
		t.Fatal("generating accepted a response containing []*int")
	}
	if !strings.Contains(err.Error(), "counts") {
		t.Errorf("the error reads %v, want it to name the counts field", err)
	}
}

// TestAPointerReachedThroughASliceIsRefused holds that the walk recurses into a slice element
// structure: removing the traversal lets the bare pointer through.
func TestAPointerReachedThroughASliceIsRefused(t *testing.T) {
	_, err := Generate("the.module", "1.0.0", speedhttp.Operations{speedhttp.NewOperation[struct{}, pointerInSlice](
		"the.module.read", http.MethodGet, "/items", "", "", nil,
		[]speedhttp.Response{{
			Status: 200, Description: "The fields",
			Body: &speedhttp.Body{MediaType: "application/json"},
		}},
	)})
	if err == nil {
		t.Fatal("generating accepted a bare pointer inside a slice element")
	}
	if !strings.Contains(err.Error(), "items.count") {
		t.Errorf("the error reads %v, want it to name items.count", err)
	}
}

// TestAPointerReachedThroughAPointerIsRefused holds that the walk recurses behind a declared field
// pointer.
func TestAPointerReachedThroughAPointerIsRefused(t *testing.T) {
	_, err := Generate("the.module", "1.0.0", speedhttp.Operations{speedhttp.NewOperation[struct{}, pointerBehindPointer](
		"the.module.read", http.MethodGet, "/items", "", "", nil,
		[]speedhttp.Response{{
			Status: 200, Description: "The fields",
			Body: &speedhttp.Body{MediaType: "application/json"},
		}},
	)})
	if err == nil {
		t.Fatal("generating accepted a bare pointer behind a field pointer")
	}
	if !strings.Contains(err.Error(), "inner.count") {
		t.Errorf("the error reads %v, want it to name inner.count", err)
	}
}

// TestAPointerReachedThroughAMapIsRefused holds that the walk recurses into a map value.
func TestAPointerReachedThroughAMapIsRefused(t *testing.T) {
	_, err := Generate("the.module", "1.0.0", speedhttp.Operations{speedhttp.NewOperation[struct{}, pointerInMap](
		"the.module.read", http.MethodGet, "/items", "", "", nil,
		[]speedhttp.Response{{
			Status: 200, Description: "The fields",
			Body: &speedhttp.Body{MediaType: "application/json"},
		}},
	)})
	if err == nil {
		t.Fatal("generating accepted a bare pointer inside a map value")
	}
	if !strings.Contains(err.Error(), "by_name.count") {
		t.Errorf("the error reads %v, want it to name by_name.count", err)
	}
}

// TestAPointerValueInAMapIsRefused holds the element rule for a map whose value is a pointer: a
// nil value is written as null and no per-value tag can mark it.
func TestAPointerValueInAMapIsRefused(t *testing.T) {
	_, err := Generate("the.module", "1.0.0", speedhttp.Operations{speedhttp.NewOperation[struct{}, pointerInMapValue](
		"the.module.read", http.MethodGet, "/items", "", "", nil,
		[]speedhttp.Response{{
			Status: 200, Description: "The fields",
			Body: &speedhttp.Body{MediaType: "application/json"},
		}},
	)})
	if err == nil {
		t.Fatal("generating accepted a map[K]*T value")
	}
	if !strings.Contains(err.Error(), "by_name") {
		t.Errorf("the error reads %v, want it to name by_name", err)
	}
}

// TestAPointerElementInAnArrayIsRefused holds the element rule for an array, the container kind a
// slice-only walk would miss.
func TestAPointerElementInAnArrayIsRefused(t *testing.T) {
	_, err := Generate("the.module", "1.0.0", speedhttp.Operations{speedhttp.NewOperation[struct{}, pointerInArray](
		"the.module.read", http.MethodGet, "/items", "", "", nil,
		[]speedhttp.Response{{
			Status: 200, Description: "The fields",
			Body: &speedhttp.Body{MediaType: "application/json"},
		}},
	)})
	if err == nil {
		t.Fatal("generating accepted an array of pointers")
	}
	if !strings.Contains(err.Error(), "counts") {
		t.Errorf("the error reads %v, want it to name counts", err)
	}
}

// TestAPointerInANestedSliceIsRefused holds that the unwrapping repeats: a walk that looks one
// level deep accepts a slice of slices of pointers.
func TestAPointerInANestedSliceIsRefused(t *testing.T) {
	_, err := Generate("the.module", "1.0.0", speedhttp.Operations{speedhttp.NewOperation[struct{}, pointerInNestedSlice](
		"the.module.read", http.MethodGet, "/items", "", "", nil,
		[]speedhttp.Response{{
			Status: 200, Description: "The fields",
			Body: &speedhttp.Body{MediaType: "application/json"},
		}},
	)})
	if err == nil {
		t.Fatal("generating accepted a slice of slices of pointers")
	}
	if !strings.Contains(err.Error(), "counts") {
		t.Errorf("the error reads %v, want it to name counts", err)
	}
}

// TestAPointerInAMapOfSlicesIsRefused holds the unwrapping through two container kinds at once: a
// map value that is a slice of pointers.
func TestAPointerInAMapOfSlicesIsRefused(t *testing.T) {
	_, err := Generate("the.module", "1.0.0", speedhttp.Operations{speedhttp.NewOperation[struct{}, pointerInMapOfSlices](
		"the.module.read", http.MethodGet, "/items", "", "", nil,
		[]speedhttp.Response{{
			Status: 200, Description: "The fields",
			Body: &speedhttp.Body{MediaType: "application/json"},
		}},
	)})
	if err == nil {
		t.Fatal("generating accepted a map of slices of pointers")
	}
	if !strings.Contains(err.Error(), "by_name") {
		t.Errorf("the error reads %v, want it to name by_name", err)
	}
}

// TestAPointerInASliceOfMapsIsRefused holds that the walk unwraps a map after the first container
// level: the map arm is only reachable inside the loop.
func TestAPointerInASliceOfMapsIsRefused(t *testing.T) {
	_, err := Generate("the.module", "1.0.0", speedhttp.Operations{speedhttp.NewOperation[struct{}, pointerInSliceOfMaps](
		"the.module.read", http.MethodGet, "/items", "", "", nil,
		[]speedhttp.Response{{
			Status: 200, Description: "The fields",
			Body: &speedhttp.Body{MediaType: "application/json"},
		}},
	)})
	if err == nil {
		t.Fatal("generating accepted a slice of maps of pointers")
	}
	if !strings.Contains(err.Error(), "by_name") {
		t.Errorf("the error reads %v, want it to name by_name", err)
	}
}

// TestAPointerInASliceOfMapOfArraysIsRefused holds that the walk unwraps an array after the first
// container level: the array arm is only reachable inside the loop.
func TestAPointerInASliceOfMapOfArraysIsRefused(t *testing.T) {
	_, err := Generate("the.module", "1.0.0", speedhttp.Operations{speedhttp.NewOperation[struct{}, pointerInSliceOfMapOfArrays](
		"the.module.read", http.MethodGet, "/items", "", "", nil,
		[]speedhttp.Response{{
			Status: 200, Description: "The fields",
			Body: &speedhttp.Body{MediaType: "application/json"},
		}},
	)})
	if err == nil {
		t.Fatal("generating accepted a slice of maps of arrays of pointers")
	}
	if !strings.Contains(err.Error(), "counts") {
		t.Errorf("the error reads %v, want it to name counts", err)
	}
}

// TestAnUntaggedAnonymousStructPointerIsAcceptedInline checks that an anonymous struct pointer
// with no json tag is inlined, its fields published beside the named ones.
func TestAnUntaggedAnonymousStructPointerIsAcceptedInline(t *testing.T) {
	doc := generate(t, speedhttp.Operations{speedhttp.NewOperation[struct{}, inlineAccepted](
		"the.module.read", http.MethodGet, "/items", "", "", nil,
		[]speedhttp.Response{{
			Status: 200, Description: "The fields",
			Body: &speedhttp.Body{MediaType: "application/json"},
		}},
	)})
	properties := component(t, doc, "inlineAccepted")["properties"].(map[string]interface{})
	if _, ok := properties["text"]; !ok {
		t.Errorf("the inlined field is missing from the schema: %v", properties)
	}
	if _, ok := properties["named"]; !ok {
		t.Errorf("the named field is missing from the schema: %v", properties)
	}
}

// TestAFieldNamedDashIsChecked holds that only the tag "-" alone skips a field: "-," publishes a
// member literally named "-", so an unmarked pointer under it is written as null and refused.
func TestAFieldNamedDashIsChecked(t *testing.T) {
	_, err := Generate("the.module", "1.0.0", speedhttp.Operations{speedhttp.NewOperation[struct{}, dashedKeyPointer](
		"the.module.read", http.MethodGet, "/items", "", "", nil,
		[]speedhttp.Response{{
			Status: 200, Description: "The fields",
			Body: &speedhttp.Body{MediaType: "application/json"},
		}},
	)})
	if err == nil {
		t.Fatal("generating accepted a pointer published under the key -")
	}
	if !strings.Contains(err.Error(), `"-"`) {
		t.Errorf("the error reads %v, want it to name the - field", err)
	}
}

// TestAnUnexportedEmbeddedStructIsChecked checks that a bare pointer inside an unexported
// embedded struct is still found and refused.
func TestAnUnexportedEmbeddedStructIsChecked(t *testing.T) {
	_, err := Generate("the.module", "1.0.0", speedhttp.Operations{speedhttp.NewOperation[struct{}, embeddedUnexported](
		"the.module.read", http.MethodGet, "/items", "", "", nil,
		[]speedhttp.Response{{
			Status: 200, Description: "The fields",
			Body: &speedhttp.Body{MediaType: "application/json"},
		}},
	)})
	if err == nil {
		t.Fatal("generating accepted a bare pointer inside an unexported embedded struct")
	}
	if !strings.Contains(err.Error(), "count") {
		t.Errorf("the error reads %v, want it to name the count field", err)
	}
}

// TestATaggedAnonymousPointerIsRefused holds the reverse of the anonymous-field exemption: a json
// tag names the pointer as a property, so a nil one is written as null rather than inlined away.
func TestATaggedAnonymousPointerIsRefused(t *testing.T) {
	_, err := Generate("the.module", "1.0.0", speedhttp.Operations{speedhttp.NewOperation[struct{}, taggedEmbedded](
		"the.module.read", http.MethodGet, "/items", "", "", nil,
		[]speedhttp.Response{{
			Status: 200, Description: "The fields",
			Body: &speedhttp.Body{MediaType: "application/json"},
		}},
	)})
	if err == nil {
		t.Fatal("generating accepted a tagged anonymous pointer with no nullable declaration")
	}
	if !strings.Contains(err.Error(), "inner") {
		t.Errorf("the error reads %v, want it to name the inner field", err)
	}
}

// TestAnAnonymousNonStructPointerIsRefused checks that an embedded pointer to a non-struct is
// refused, naming the field.
func TestAnAnonymousNonStructPointerIsRefused(t *testing.T) {
	_, err := Generate("the.module", "1.0.0", speedhttp.Operations{speedhttp.NewOperation[struct{}, embeddedScalarPointer](
		"the.module.read", http.MethodGet, "/items", "", "", nil,
		[]speedhttp.Response{{
			Status: 200, Description: "The fields",
			Body: &speedhttp.Body{MediaType: "application/json"},
		}},
	)})
	if err == nil {
		t.Fatal("generating accepted an embedded pointer to a non-struct")
	}
	if !strings.Contains(err.Error(), "EmbeddedScalar") {
		t.Errorf("the error reads %v, want it to name the embedded field", err)
	}
}

// TestAStructureSharedByRequestAndResponseIsChecked holds that the same structure used as a
// request body does not exempt its response use.
func TestAStructureSharedByRequestAndResponseIsChecked(t *testing.T) {
	_, err := Generate("the.module", "1.0.0", speedhttp.Operations{speedhttp.NewOperation[sharedShape, sharedShape](
		"the.module.write", http.MethodPost, "/items", "", "", requestBody("application/json"),
		[]speedhttp.Response{{
			Status: 200, Description: "The value",
			Body: &speedhttp.Body{MediaType: "application/json"},
		}},
	)})
	if err == nil {
		t.Fatal("generating accepted a shared structure whose pointer the response writes as null")
	}
	if !strings.Contains(err.Error(), "value") {
		t.Errorf("the error reads %v, want it to name the value field", err)
	}
}

// TestAFormTagOmitEmptyDoesNotSatisfyTheResponseCheck holds that only the json tag counts: the
// form binder's omitempty says nothing about what the JSON encoder writes.
func TestAFormTagOmitEmptyDoesNotSatisfyTheResponseCheck(t *testing.T) {
	_, err := Generate("the.module", "1.0.0", speedhttp.Operations{speedhttp.NewOperation[struct{}, formOmitEmpty](
		"the.module.read", http.MethodGet, "/items", "", "", nil,
		[]speedhttp.Response{{
			Status: 200, Description: "The fields",
			Body: &speedhttp.Body{MediaType: "application/json"},
		}},
	)})
	if err == nil {
		t.Fatal("generating accepted a response pointer whose omitempty is only on the form tag")
	}
	if !strings.Contains(err.Error(), "value") {
		t.Errorf("the error reads %v, want it to name the value field", err)
	}
}

// TestAPointerWithOmitZeroNeedsNoNullableDeclaration holds that omitzero is understood: nil is the
// zero value, so the key is left out and no null is written.
func TestAPointerWithOmitZeroNeedsNoNullableDeclaration(t *testing.T) {
	doc := generate(t, speedhttp.Operations{speedhttp.NewOperation[struct{}, omitZeroPointer](
		"the.module.read", http.MethodGet, "/items", "", "", nil,
		[]speedhttp.Response{{
			Status: 200, Description: "The fields",
			Body: &speedhttp.Body{MediaType: "application/json"},
		}},
	)})
	value := component(t, doc, "omitZeroPointer")["properties"].(map[string]interface{})["value"].(map[string]interface{})
	if value["type"] != "integer" {
		t.Errorf("the omitzero pointer's type is %v, want a single integer", value["type"])
	}
}

// TestMarkingANullableFieldOmitZeroIsRefused holds that nullable plus omitzero is refused like
// nullable plus omitempty: the key would be left out rather than written as null.
func TestMarkingANullableFieldOmitZeroIsRefused(t *testing.T) {
	_, err := Generate("the.module", "1.0.0", speedhttp.Operations{speedhttp.NewOperation[struct{}, nullableOmitZero](
		"the.module.read", http.MethodGet, "/items", "", "", nil,
		[]speedhttp.Response{{
			Status: 200, Description: "The fields",
			Body: &speedhttp.Body{MediaType: "application/json"},
		}},
	)})
	if err == nil {
		t.Fatal("generating accepted nullable:\"true\" on a field with omitzero")
	}
	if !strings.Contains(err.Error(), "omitzero") {
		t.Errorf("the error reads %v, want it to name omitzero", err)
	}
}

// TestMarkingANullableRequestFieldOmitZeroIsRefused checks that nullable:"true" on a request
// field with omitzero is refused, naming omitzero.
func TestMarkingANullableRequestFieldOmitZeroIsRefused(t *testing.T) {
	_, err := Generate("the.module", "1.0.0", speedhttp.Operations{speedhttp.NewOperation[nullableOmitZero, struct{}](
		"the.module.write", http.MethodPost, "/items", "", "", requestBody("application/json"),
		[]speedhttp.Response{{
			Status: 204, Description: "No content",
		}},
	)})
	if err == nil {
		t.Fatal("generating accepted nullable:\"true\" on a request field with omitzero")
	}
	if !strings.Contains(err.Error(), "omitzero") {
		t.Errorf("the error reads %v, want it to name omitzero", err)
	}
}

// TestANestedObjectKeepsItsMarkedNullUnion holds that the stripping recurses: a marked pointer
// inside an inlined object keeps its null union and an unmarked one does not.
func TestANestedObjectKeepsItsMarkedNullUnion(t *testing.T) {
	doc := generate(t, speedhttp.Operations{speedhttp.NewOperation[struct{}, outerNestedFields](
		"the.module.read", http.MethodGet, "/items", "", "", nil,
		[]speedhttp.Response{{
			Status: 200, Description: "The fields",
			Body: &speedhttp.Body{MediaType: "application/json"},
		}},
	)})
	inner := component(t, doc, "outerNestedFields")["properties"].(map[string]interface{})["inner"].(map[string]interface{})
	properties := inner["properties"].(map[string]interface{})
	marked := properties["marked"].(map[string]interface{})
	types, ok := marked["type"].([]interface{})
	if !ok || !slices.Contains(types, "null") {
		t.Errorf("the nested marked field's type is %v, want the null union kept", marked["type"])
	}
	plain := properties["plain"].(map[string]interface{})
	if plain["type"] != "string" {
		t.Errorf("the nested unmarked field's type is %v, want a single string", plain["type"])
	}
}

// TestTheProblemDocumentIsPublishedAsProblem holds the shared component's name: every problem
// response references one component called Problem.
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

// ProblemDocument is a host type that happens to share its name with the entry point's default
// error shape.
type ProblemDocument struct {
	// Host is the document's host header.
	Host string `json:"host" required:"true"`
}

// TestTheProblemRenameAppliesOnlyToTheDefaultShape checks that a host type named ProblemDocument
// keeps its own component and is not renamed Problem.
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

// TestTheSameDeclarationsGiveTheSameBytes holds determinism: the merged contract is a build
// artifact, so generating twice must not differ.
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

type customError struct {
	Code string `json:"code" required:"true" description:"The host's error code."`
}

// TestACustomErrorShapeIsPublishedAsDeclared checks that a declared error shape is published
// under its own media type and component, with no Problem document added.
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

type jsonInput struct {
	ID     string `path:"id"`
	Q      string `query:"q"`
	Name   string `json:"name" required:"true" description:"The name."`
	Hidden string `json:"hidden"`
}

// TestABodyOperationPublishesTheLimitRefusal checks that an operation with a request body also
// declares its 413 response.
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

// TestAJSONRequestBodyPublishesOnlyJSONFields checks that the JSON request body carries the
// json-tagged fields and their required list while the parameter fields stay parameters.
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

type describedJSONInput struct {
	_    struct{} `description:"The body of one update."`
	Name string   `json:"name" required:"true"`
}

// TestAJSONRequestBodyPublishesItsSchemaDescription checks that a blank description field on the
// input becomes the named body component's description.
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

type openJSONInput struct {
	_    struct{} `additionalProperties:"true" description:"The body of one update."`
	Name string   `json:"name" required:"true"`
}

// TestAJSONRequestBodyCanDeclareAnOpenSchema checks that additionalProperties:"true" on the input
// publishes an open body schema and keeps its description.
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

type parameterOnly struct {
	ID string `path:"id"`
	Q  string `query:"q"`
}

// TestANamedRequestBodyPublishesAComponent checks that a named request body is published as a
// component the operation references.
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

// TestAnEmptyMediaTypePublishesNoRequestBody holds the no-body declaration: the parameters are
// published and there is no request body at all.
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

// TestAJSONBodyIsPublishedOnEveryMethod holds the entry point's method surface: pkg/http binds a
// declared body on GET as it does on POST, so the fragment must carry it there too.
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

// TestAnUnsupportedMediaTypeIsRefused checks that a request body of an unsupported media type is
// refused with an error naming it.
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

type multipartInput struct {
	ID   string `path:"id"`
	Q    string `query:"q"`
	Name string `form:"name" description:"The name."`
	File []byte `file:"upload"`
}

// TestAMultipartBodyPublishesFormAndFileFields checks that a multipart body stays multipart,
// publishing its form and binary file fields while the form field stays out of the parameters.
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

type multipartFormInput struct {
	ID   string `path:"id"`
	Name string `form:"name" required:"true"`
}

// TestAMultipartBodyWithoutAFileStaysMultipart checks that a multipart declaration made only of
// form fields is still published as multipart/form-data.
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

type formInput struct {
	ID   string `path:"id"`
	Q    string `query:"q"`
	Name string `form:"name"`
}

// TestAFormFieldIsNotPublishedAsAQueryParameter checks that a form field appears in the form body
// and not among the query parameters.
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

type rawInput struct {
	ID   string `path:"id"`
	Q    string `query:"q"`
	Body []byte `raw:"true"`
}

// TestABinaryRequestBodyPublishesItsParameters checks that an octet-stream body is a binary string
// and the path and query parameters beside it are still published.
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

// TestAParameterlessBinaryRequestStaysBinary holds the no-input side: an octet-stream declaration
// whose input carries only the raw field publishes a binary body and no parameters.
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

// TestReplyResponsesPublishTheirBodies checks each reply kind's body: its schema, its media type or
// its binary shape.
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

// TestAnEventStreamWithAStructurePublishesNoBodySchema checks that text/event-stream publishes its
// media type with no schema even when a structure is declared.
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

// TestAnEventStreamMediaTypeIsNotStripped holds normalisation: a declared media type with
// parameters is published as the event stream media type, not dropped or rewritten.
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

// TestAnOctetStreamAnswerIsBinaryWhateverItsStructureNames checks that an octet-stream answer is a
// binary string with no reference to a declared structure.
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

// TestABinaryAnswerOfAnotherMediaTypeIsBinary holds the general binary row: any media type the
// entry point writes as bytes is published as a binary string, not as text.
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

// TestATextAnswerOfAnotherMediaTypeIsAString holds the text side: a text media type is a string,
// not binary octets.
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

// TestAJSONAnswerWithoutAStructureIsRefused checks that a JSON answer declaring no Structure is
// refused with an error naming it.
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

// TestARedirectPublishesItsLocationAndNoContent holds the redirect shape: the status and Location
// header are published and the answer carries no content.
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

// TestANoBodyStatusPublishesNoContent holds the empty answers: a 204 and a 304 carry a status and
// no content.
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

// TestABodyOnANoBodyStatusIsRefused holds the other side: pkg/http refuses a body on a status that
// carries none, so the generator refuses it too.
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

type simpleInput struct {
	Q    string `query:"q"`
	Name string `json:"name" required:"true" description:"The name."`
}

// TestTheSimpleFormPublishesParametersBodyAndSuccess checks that the simple form publishes its
// query parameter, its JSON request body and its success body under Out's schema.
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

// TestASimpleSuccessOfAnotherMediaTypeIsRefused checks that a simple operation whose success names
// a media type other than JSON is refused.
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

// TestAnEmptySimpleOutPublishesTheEmptyObjectBody checks that a simple operation with an empty Out
// publishes the JSON object schema, however its body is declared.
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

// TestAReplyBodyWithoutAMediaTypeIsRefused checks that a reply body naming no media type is
// refused, with or without a structure.
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

type unbindableFloatQuery struct {
	Ratio float64 `query:"ratio"`
}

type unbindableUintQuery struct {
	Count uint `query:"count"`
}

type unbindableTimeQuery struct {
	At time.Time `query:"at"`
}

type unbindableSliceHeader struct {
	Values []string `header:"X-Values"`
}

type bindableSliceQuery struct {
	Values []string `query:"values"`
}

type unbindableFloatForm struct {
	Ratio float64 `form:"ratio"`
}

// TestAParameterTypeTheEntryPointCannotBindIsRefused holds the parameter surface: pkg/http binds a
// string, a signed integer or a bool, and a slice of those for a query parameter only.
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

// TestARepeatedQueryParameterPublishesItsSliceType holds the supported side: a repeated query
// parameter is a slice of a bindable scalar and is published as an array.
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

type collisionDocument struct {
	In     string `json:"in" description:"The in member."`
	Name   string `json:"name" description:"The name member."`
	Schema string `json:"schema" description:"The schema member."`
	Style  string `json:"style" description:"The style member."`
}

// TestABusinessSchemaKeepsMembersNamedLikeAParameterOrHeader checks that schema members named in,
// name, schema and style survive normalisation with their descriptions.
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

// TestABusinessRequestSchemaKeepsMembersNamedLikeAParameterOrHeader holds the same boundary on the
// structured request side, where the entry point binds every described member.
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

// TestASimpleOperationsFailurePublishesItsDeclaredHeaders checks that both the success and the
// failure response of a simple operation keep their declared headers.
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

// TestASimpleOperationWithTwoSuccessStatusesIsRefused checks that a simple operation declaring two
// success statuses is refused.
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

// TestAReplyOperationsFailurePublishesItsDeclaredHeaders checks that a reply operation's failure
// response keeps its declared header.
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

// TestAnEventStreamPublishesTheNoStoreDefault checks that an event stream response publishes a
// string Cache-Control header by default.
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

// TestADeclaredCacheControlStandsOnAnEventStream holds the default's limit: a declaration that
// names Cache-Control is the one published, and no second entry replaces it.
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

// TestEveryRequestBodyObjectIsClosed checks that JSON, form and multipart request body schemas are
// published with additionalProperties false.
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

func operationOf(t *testing.T, doc map[string]interface{}) map[string]interface{} {
	t.Helper()
	return operationOfMethod(t, doc, "get")
}

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

func component(t *testing.T, doc map[string]interface{}, name string) map[string]interface{} {
	t.Helper()
	schemas := doc["components"].(map[string]interface{})["schemas"].(map[string]interface{})
	schema, ok := schemas[name].(map[string]interface{})
	if !ok {
		t.Fatalf("the components carry no %s schema: %v", name, schemas)
	}
	return schema
}

func requestContentOf(t *testing.T, operation map[string]interface{}) map[string]interface{} {
	t.Helper()
	body, ok := operation["requestBody"].(map[string]interface{})
	if !ok {
		t.Fatalf("the operation carries no request body: %v", operation)
	}
	return body["content"].(map[string]interface{})
}

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

// TestARequestBodyPublishesItsDescriptionAndRequired holds the request body's metadata: the media
// type, the description as the body's own description and the required flag are all published.
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

// TestADynamicMediaTypeAnswerPublishesABinaryContentEntry checks that a */* answer publishes only
// that entry, as a binary string.
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

// TestEachAccessLevelIsPublishedAsItsSecurityRequirement checks the security of a guarded, an
// optional and a public operation: both carriers, both plus anonymity, and none.
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
