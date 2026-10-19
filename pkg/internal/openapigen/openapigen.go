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
	"mime"
	"mime/multipart"
	"net/http"
	"reflect"
	"strconv"
	"strings"

	"github.com/swaggest/jsonschema-go"
	"github.com/swaggest/openapi-go"
	"github.com/swaggest/openapi-go/openapi31"

	speedhttp "github.com/vislake/speed/pkg/http"
)

// The request body media types an operation's input is read for. They mirror
// the media types pkg/http binds, which is the contract the fragment describes.
const (
	mediaTypeJSON        = "application/json"
	mediaTypeForm        = "application/x-www-form-urlencoded"
	mediaTypeMultipart   = "multipart/form-data"
	mediaTypeOctetStream = "application/octet-stream"
	mediaTypeEventStream = "text/event-stream"
)

// parameterTags are the tags that read a value from the request line, headers
// or cookies, in the order pkg/http checks them: a field carrying more than one
// is bound from the first and must be published only as that one.
var parameterTags = []string{"path", "query", "header", "cookie"}

// The tags each request source drops when a structure is built for it. Every
// structure keeps only the tags its own source reads, so the reflector does
// not publish a shape the entry point does not bind.
var (
	nonParameterTags = []string{"path", "query", "header", "cookie", "json", "form", "file", "raw", "formData", "contentType"}
	nonJSONTags      = []string{"path", "query", "header", "cookie", "form", "file", "raw", "formData", "contentType"}
	nonFormTags      = []string{"path", "query", "header", "cookie", "json", "form", "file", "raw", "formData", "contentType"}
	nonMultipartTags = []string{"path", "query", "header", "cookie", "json", "form", "file", "raw", "formData", "contentType"}
)

// problemDocumentType is the entry point's default error shape. Its component
// is published as Problem; a host type that happens to share its Go name is
// not renamed.
var problemDocumentType = reflect.TypeOf(speedhttp.ProblemDocument{})

// Generate builds the OpenAPI fragment of one module from its declarations.
// Title and Version become the fragment's info section; the fragment's paths
// stay relative to the API root, as the platform's fragments are.
func Generate(title, version string, operations speedhttp.Operations) ([]byte, error) {
	r := openapi31.NewReflector()
	// Name each component by its Go type, without the declaring package's
	// prefix: the platform publishes "FeatureBoardDocument", not
	// "BoardFeatureBoardDocument". The one component the platform renames is
	// the default problem document, published as "Problem".
	r.DefaultOptions = append(r.DefaultOptions, jsonschema.InterceptDefName(func(t reflect.Type, _ string) string {
		if isProblemDocument(t) {
			return "Problem"
		}
		return t.Name()
	}))
	r.Spec.Info.WithTitle(title).WithVersion(version)

	for index, declared := range operations {
		operation := declared.Descriptor()
		ctx, err := r.NewOperationContext(operation.Method, ginPathToOpenAPI(operation.Path))
		if err != nil {
			return nil, fmt.Errorf("openapigen: %s %s: %w", operation.Method, operation.Path, err)
		}
		ctx.SetID(operation.ID)
		ctx.SetSummary(operation.Summary)
		ctx.SetDescription(operation.Description)

		if err := addParameters(ctx, operation); err != nil {
			return nil, fmt.Errorf("openapigen: %s %s: %w", operation.Method, operation.Path, err)
		}
		for _, response := range operation.Responses {
			if err := addResponse(ctx, operation, response); err != nil {
				return nil, fmt.Errorf("openapigen: %s %s: %w", operation.Method, operation.Path, err)
			}
		}
		if err := r.AddOperation(ctx); err != nil {
			return nil, fmt.Errorf("openapigen: %s %s: %w", operation.Method, operation.Path, err)
		}
		// The request body is built after the operation so that it does not
		// depend on the reflector's method rules: the entry point reads a body
		// on any method, while the library suppresses one on GET, HEAD, DELETE
		// and TRACE.
		if err := addRequestBody(r, index, operation); err != nil {
			return nil, fmt.Errorf("openapigen: %s %s: %w", operation.Method, operation.Path, err)
		}
	}

	raw, err := r.Spec.MarshalJSON()
	if err != nil {
		return nil, fmt.Errorf("openapigen: writing the fragment: %w", err)
	}
	return normalise(raw)
}

// addParameters publishes the request's parameters. They are added as a
// structure of their own, with no media type: the reflector reads that as the
// parameter surface, and the structure carries only the parameter tags, so a
// body field is never promised as a query parameter. A parameter type the entry
// point cannot bind is refused rather than published.
func addParameters(ctx openapi.OperationContext, operation speedhttp.OperationDescriptor) error {
	if operation.Input == nil {
		return nil
	}
	if err := checkParameterBinding(operation.Input); err != nil {
		return err
	}
	parameters := parameterStructure(operation.Input)
	if parameters == nil {
		return nil
	}
	ctx.AddReqStructure(instance(parameters))
	return nil
}

// addRequestBody publishes the request body the declared media type reads.
//
// The body is generated through a temporary operation and moved onto the real
// one. The library refuses to reflect a request body for GET, HEAD, DELETE and
// TRACE, while pkg/http binds one on every method; building the body under a
// POST that is discarded afterwards is what keeps the two in step.
func addRequestBody(r *openapi31.Reflector, index int, operation speedhttp.OperationDescriptor) error {
	mediaType := normaliseMediaType(operation.RequestMediaType)
	if err := checkRequestBinding(operation.Input, mediaType); err != nil {
		return err
	}
	if mediaType == mediaTypeForm || mediaType == mediaTypeMultipart {
		if err := checkFormBinding(operation.Input); err != nil {
			return err
		}
	}
	if mediaType == "" {
		return nil
	}

	tempPath := requestBodyPath(r, index)
	ctx, err := r.NewOperationContext(http.MethodPost, tempPath)
	if err != nil {
		return err
	}
	switch mediaType {
	case mediaTypeJSON:
		addBody(ctx, jsonBodyStructure(operation.Input), mediaType, nil)
	case mediaTypeForm:
		addBody(ctx, formBodyStructure(operation.Input), mediaType, nil)
	case mediaTypeMultipart:
		addBody(ctx, multipartBodyStructure(operation.Input), mediaType, multipartContentType)
	case mediaTypeOctetStream:
		ctx.AddReqStructure(nil,
			openapi.WithContentType(mediaType),
			openapi.WithCustomize(binaryRequestBody(mediaType)))
	default:
		return fmt.Errorf("unsupported request media type %q", operation.RequestMediaType)
	}
	if err := r.AddOperation(ctx); err != nil {
		return err
	}
	item := r.Spec.Paths.MapOfPathItemValues[tempPath]
	delete(r.Spec.Paths.MapOfPathItemValues, tempPath)
	if item.Post == nil || item.Post.RequestBody == nil {
		return fmt.Errorf("the %s request body was not generated", mediaType)
	}
	target := operationOfItem(r.Spec.Paths.MapOfPathItemValues[ginPathToOpenAPI(operation.Path)], operation.Method)
	if target == nil {
		return fmt.Errorf("the generated %s operation is missing", operation.Method)
	}
	target.RequestBody = item.Post.RequestBody
	return nil
}

// requestBodyPath returns a path no declared operation uses, for the temporary
// operation a request body is built on.
func requestBodyPath(r *openapi31.Reflector, index int) string {
	path := fmt.Sprintf("/__openapigen_request_body_%d", index)
	for {
		if _, exists := r.Spec.Paths.MapOfPathItemValues[path]; !exists {
			return path
		}
		path += "_"
	}
}

// operationOfItem returns one method of a path item, or nil.
func operationOfItem(item openapi31.PathItem, method string) *openapi31.Operation {
	switch strings.ToUpper(method) {
	case http.MethodGet:
		return item.Get
	case http.MethodPut:
		return item.Put
	case http.MethodPost:
		return item.Post
	case http.MethodDelete:
		return item.Delete
	case http.MethodOptions:
		return item.Options
	case http.MethodHead:
		return item.Head
	case http.MethodPatch:
		return item.Patch
	case http.MethodTrace:
		return item.Trace
	}
	return nil
}

// addBody adds one request body structure under its media type.
func addBody(ctx openapi.OperationContext, structure reflect.Type, mediaType string, customize func(openapi.ContentOrReference)) {
	if structure == nil {
		return
	}
	options := []openapi.ContentOption{openapi.WithContentType(mediaType)}
	if customize != nil {
		options = append(options, openapi.WithCustomize(customize))
	}
	ctx.AddReqStructure(instance(structure), options...)
}

// multipartContentType corrects the media type the library picks for a
// multipart body. The library names a form body's media type
// application/x-www-form-urlencoded and only switches to multipart when it
// recognizes a file upload through *multipart.FileHeader; a multipart
// declaration made only of form fields would otherwise be published under the
// wrong media type, which pkg/http then rejects at request time.
func multipartContentType(cor openapi.ContentOrReference) {
	body, ok := cor.(*openapi31.RequestBodyOrReference)
	if !ok || body.RequestBody == nil {
		return
	}
	form, ok := body.RequestBody.Content[mediaTypeForm]
	if !ok {
		return
	}
	delete(body.RequestBody.Content, mediaTypeForm)
	body.RequestBody.Content[mediaTypeMultipart] = form
}

// binaryRequestBody declares a raw request body: a string of bytes.
func binaryRequestBody(mediaType string) func(openapi.ContentOrReference) {
	return func(cor openapi.ContentOrReference) {
		body, ok := cor.(*openapi31.RequestBodyOrReference)
		if !ok || body.RequestBody == nil {
			return
		}
		body.RequestBody.Content[mediaType] = openapi31.MediaType{
			Schema: map[string]interface{}{"type": "string", "format": "binary"},
		}
	}
}

// parameterStructure returns the fields a request's parameters are read from:
// those carrying a path, query, header or cookie tag, without any body tag. A
// field carrying more than one parameter tag is published only under the one
// pkg/http binds it from. It returns nil when the input declares no parameter.
func parameterStructure(input reflect.Type) reflect.Type {
	fields := make([]reflect.StructField, 0, input.NumField())
	for i := 0; i < input.NumField(); i++ {
		field := input.Field(i)
		if field.PkgPath != "" {
			continue
		}
		source, name := parameterOf(field)
		if name == "" {
			continue
		}
		fields = append(fields, reflect.StructField{
			Name:      field.Name,
			Type:      field.Type,
			Tag:       rebuildTag(field.Tag, nonParameterTags, source, name),
			Anonymous: field.Anonymous,
		})
	}
	if len(fields) == 0 {
		return nil
	}
	return reflect.StructOf(fields)
}

// parameterOf returns the parameter source and name a field declares, matching
// pkg/http's precedence: path, then query, then header, then cookie.
func parameterOf(field reflect.StructField) (string, string) {
	for _, source := range parameterTags {
		if name := field.Tag.Get(source); name != "" {
			return source, name
		}
	}
	return "", ""
}

// jsonBodyStructure returns the fields a JSON body is decoded into: those
// carrying a json tag, without their parameter tags.
func jsonBodyStructure(input reflect.Type) reflect.Type {
	return bodyStructure(input, func(field reflect.StructField) (reflect.Type, string, bool) {
		if taggedName(field, "json") == "" {
			return nil, "", false
		}
		return field.Type, string(rebuildTag(field.Tag, nonJSONTags, "", "")), true
	})
}

// formBodyStructure returns the fields a urlencoded body is decoded into. The
// library reads a form body's members through `formData`, so the declaration's
// `form` name is republished under that tag; the `form` tag itself is dropped
// because the library also reads it as a query parameter.
func formBodyStructure(input reflect.Type) reflect.Type {
	return bodyStructure(input, func(field reflect.StructField) (reflect.Type, string, bool) {
		name := taggedName(field, "form")
		if name == "" {
			return nil, "", false
		}
		return field.Type, string(rebuildTag(field.Tag, nonFormTags, "formData", name)), true
	})
}

// multipartBodyStructure returns the fields a multipart body is decoded into:
// its form fields and its file fields. A file field is reflected as the
// library's file header type, which makes it a binary string in the schema,
// while pkg/http binds it into the declared []byte.
func multipartBodyStructure(input reflect.Type) reflect.Type {
	return bodyStructure(input, func(field reflect.StructField) (reflect.Type, string, bool) {
		if name := taggedName(field, "file"); name != "" {
			return reflect.TypeOf((*multipart.FileHeader)(nil)),
				string(rebuildTag(field.Tag, nonMultipartTags, "formData", name)), true
		}
		name := taggedName(field, "form")
		if name == "" {
			return nil, "", false
		}
		return field.Type, string(rebuildTag(field.Tag, nonMultipartTags, "formData", name)), true
	})
}

// bodyStructure builds the structure a body is read into from the fields the
// pick function selects. It returns nil when none is selected. The reflected
// object is closed: the entry point refuses a JSON member, form key or
// multipart part the declaration does not carry.
func bodyStructure(input reflect.Type, pick func(reflect.StructField) (reflect.Type, string, bool)) reflect.Type {
	fields := make([]reflect.StructField, 0, input.NumField())
	for i := 0; i < input.NumField(); i++ {
		field := input.Field(i)
		if field.PkgPath != "" {
			continue
		}
		typ, tag, ok := pick(field)
		if !ok {
			continue
		}
		fields = append(fields, reflect.StructField{
			Name:      field.Name,
			Type:      typ,
			Tag:       reflect.StructTag(tag),
			Anonymous: field.Anonymous,
		})
	}
	if len(fields) == 0 {
		return nil
	}
	fields = append(fields, closedObjectField())
	return reflect.StructOf(fields)
}

// closedObjectField is the unnamed field the schema reflector reads as
// additionalProperties=false for the object it is a member of. StructOf wants
// the package path an unnamed field would carry.
func closedObjectField() reflect.StructField {
	return reflect.StructField{
		Name:    "_",
		PkgPath: "openapigen",
		Type:    reflect.TypeOf(struct{}{}),
		Tag:     `additionalProperties:"false"`,
	}
}

// checkRequestBinding refuses a request declaration pkg/http would refuse when
// the handler is built, so a fragment is never generated for a body the entry
// point cannot bind. It mirrors the checks in pkg/http's request-binding
// constructor.
func checkRequestBinding(input reflect.Type, mediaType string) error {
	if input == nil {
		if mediaType == "" {
			return nil
		}
		return fmt.Errorf("the %s request media type is declared but the input carries no body field", mediaType)
	}
	rawCount := 0
	for i := 0; i < input.NumField(); i++ {
		field := input.Field(i)
		if field.PkgPath != "" {
			continue
		}
		if field.Tag.Get("raw") == "true" {
			rawCount++
			if !isBytes(field.Type) {
				return fmt.Errorf("the raw-tagged field %q must be []byte", field.Name)
			}
		}
		if field.Tag.Get("file") != "" && !isBytes(field.Type) {
			return fmt.Errorf("the file-tagged field %q must be []byte", field.Name)
		}
	}
	if rawCount > 1 {
		return fmt.Errorf("the input carries more than one raw-tagged field")
	}
	jsonFields := len(taggedFields(input, "json"))
	formFields := len(taggedFields(input, "form"))
	fileFields := len(taggedFields(input, "file"))
	if fileFields > 0 && mediaType != mediaTypeMultipart {
		return fmt.Errorf("a file-tagged field requires the multipart/form-data media type")
	}
	switch mediaType {
	case "":
		if rawCount > 0 {
			return fmt.Errorf("a raw-tagged field requires the application/octet-stream media type")
		}
		if jsonFields > 0 || formFields > 0 {
			return fmt.Errorf("the input carries body-tagged fields but the operation declares no request media type")
		}
	case mediaTypeJSON:
		if rawCount > 0 {
			return fmt.Errorf("a raw-tagged field requires the application/octet-stream media type")
		}
		if formFields > 0 {
			return fmt.Errorf("a form-tagged field requires the application/x-www-form-urlencoded or multipart/form-data media type")
		}
		if jsonFields == 0 {
			return fmt.Errorf("the application/json media type is declared but the input carries no json-tagged field")
		}
	case mediaTypeForm:
		if rawCount > 0 {
			return fmt.Errorf("a raw-tagged field requires the application/octet-stream media type")
		}
		if jsonFields > 0 {
			return fmt.Errorf("a json-tagged field requires the application/json media type")
		}
		if formFields == 0 {
			return fmt.Errorf("the application/x-www-form-urlencoded media type is declared but the input carries no form-tagged field")
		}
	case mediaTypeMultipart:
		if rawCount > 0 {
			return fmt.Errorf("a raw-tagged field requires the application/octet-stream media type")
		}
		if jsonFields > 0 {
			return fmt.Errorf("a json-tagged field requires the application/json media type")
		}
		if formFields+fileFields == 0 {
			return fmt.Errorf("the multipart/form-data media type is declared but the input carries no form-tagged or file-tagged field")
		}
	case mediaTypeOctetStream:
		if rawCount != 1 {
			return fmt.Errorf("the application/octet-stream media type requires exactly one raw-tagged field")
		}
		if jsonFields > 0 {
			return fmt.Errorf("a json-tagged field requires the application/json media type")
		}
		if formFields > 0 {
			return fmt.Errorf("a form-tagged field requires the application/x-www-form-urlencoded or multipart/form-data media type")
		}
	default:
		return fmt.Errorf("unsupported request media type %q", mediaType)
	}
	return nil
}

// checkParameterBinding refuses a parameter whose Go type pkg/http cannot bind.
// The entry point reads a parameter from the request text as a string, a signed
// integer or a bool, and a repeated query parameter as a slice of those; any
// other type answers 400 for a request that carries the parameter, so the
// fragment must not publish it as bindable. Only a query parameter may be a
// slice: the other sources are read as one value.
func checkParameterBinding(input reflect.Type) error {
	for i := 0; input != nil && i < input.NumField(); i++ {
		field := input.Field(i)
		if field.PkgPath != "" {
			continue
		}
		source, name := parameterOf(field)
		if name == "" {
			continue
		}
		if bindableScalar(field.Type) {
			continue
		}
		if source == "query" && bindableSlice(field.Type) {
			continue
		}
		return fmt.Errorf("the %s parameter %q has the type %s, which pkg/http cannot bind", source, name, field.Type)
	}
	return nil
}

// checkFormBinding refuses a form field whose Go type pkg/http cannot bind from
// a form key. The entry point reads a form key as a string, a signed integer or
// a bool, or a slice of those; a file field is bound separately and its []byte
// type is checked with the rest of the request declaration.
func checkFormBinding(input reflect.Type) error {
	for i := 0; input != nil && i < input.NumField(); i++ {
		field := input.Field(i)
		if field.PkgPath != "" {
			continue
		}
		if taggedName(field, "file") != "" {
			continue
		}
		name := taggedName(field, "form")
		if name == "" {
			continue
		}
		if bindableScalar(field.Type) || bindableSlice(field.Type) {
			continue
		}
		return fmt.Errorf("the form field %q has the type %s, which pkg/http cannot bind", name, field.Type)
	}
	return nil
}

// bindableScalar reports whether pkg/http's scalar binding stores t.
func bindableScalar(t reflect.Type) bool {
	switch t.Kind() {
	case reflect.String,
		reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Bool:
		return true
	}
	return false
}

// bindableSlice reports whether pkg/http's repeated-value binding stores t.
func bindableSlice(t reflect.Type) bool {
	return t.Kind() == reflect.Slice && bindableScalar(t.Elem())
}

// isEmptyStruct reports whether t is a struct with no fields, which the entry
// point writes as the empty JSON object.
func isEmptyStruct(t reflect.Type) bool {
	return t.Kind() == reflect.Struct && t.NumField() == 0
}

// isBytes reports whether t is []byte.
func isBytes(t reflect.Type) bool {
	return t.Kind() == reflect.Slice && t.Elem().Kind() == reflect.Uint8
}

// taggedFields maps each name a tag declares to its field index, skipping a
// field without the tag and the "-" name.
func taggedFields(input reflect.Type, tag string) map[string]int {
	fields := map[string]int{}
	if input == nil {
		return fields
	}
	for i := 0; i < input.NumField(); i++ {
		field := input.Field(i)
		if field.PkgPath != "" {
			continue
		}
		name := strings.Split(field.Tag.Get(tag), ",")[0]
		if name == "" || name == "-" {
			continue
		}
		fields[name] = i
	}
	return fields
}

// taggedName returns the name a body tag declares, or "" when the field is not
// tagged or carries the "-" name.
func taggedName(field reflect.StructField, tag string) string {
	name := strings.Split(field.Tag.Get(tag), ",")[0]
	if name == "-" {
		return ""
	}
	return name
}

// rebuildTag returns tag without the named keys, with name:"value" appended
// when name is non-empty. The tag's other pairs are kept verbatim, so
// descriptions and validation tags travel with the field.
func rebuildTag(tag reflect.StructTag, drop []string, name, value string) reflect.StructTag {
	pairs := splitTag(tag)
	kept := make([]string, 0, len(pairs)+1)
	for _, pair := range pairs {
		key, _, found := strings.Cut(pair, ":")
		if found && contains(drop, key) {
			continue
		}
		kept = append(kept, pair)
	}
	if name != "" {
		kept = append(kept, name+":"+strconv.Quote(value))
	}
	return reflect.StructTag(strings.Join(kept, " "))
}

// splitTag splits a struct tag into its key:"value" pairs, keeping malformed
// input as one pair rather than dropping it.
func splitTag(tag reflect.StructTag) []string {
	raw := string(tag)
	var pairs []string
	for raw != "" {
		raw = strings.TrimLeft(raw, " ")
		if raw == "" {
			break
		}
		colon := strings.IndexByte(raw, ':')
		if colon <= 0 || colon+1 >= len(raw) || raw[colon+1] != '"' {
			pairs = append(pairs, raw)
			break
		}
		end := colon + 2
		for end < len(raw) {
			if raw[end] == '\\' {
				end += 2
				continue
			}
			if raw[end] == '"' {
				end++
				break
			}
			end++
		}
		if end > len(raw) {
			end = len(raw)
		}
		pairs = append(pairs, raw[:end])
		raw = raw[end:]
	}
	return pairs
}

// contains reports whether values carries value.
func contains(values []string, value string) bool {
	for _, candidate := range values {
		if candidate == value {
			return true
		}
	}
	return false
}

// normaliseMediaType lowercases a media type and drops its parameters, matching
// how pkg/http compares a declaration with a Content-Type header.
func normaliseMediaType(value string) string {
	mediaType, _, err := mime.ParseMediaType(value)
	if err != nil {
		return strings.ToLower(strings.TrimSpace(value))
	}
	return strings.ToLower(mediaType)
}

// isJSONMediaType reports whether a media type carries a JSON body: the plain
// JSON type or one of the +json structured-suffix types.
func isJSONMediaType(mediaType string) bool {
	return mediaType == mediaTypeJSON || strings.HasSuffix(mediaType, "+json")
}

// isProblemDocument reports whether t is the entry point's default error shape.
func isProblemDocument(t reflect.Type) bool {
	return t == problemDocumentType || (t.Kind() == reflect.Ptr && t.Elem() == problemDocumentType)
}

// responseStructure is the body type an answer publishes: the operation's
// success type when the answer is the success response, and the answer's own
// declared structure otherwise. On a body-carrying success status the success
// type is the empty struct when the handler returns an empty Out, which the
// entry point writes as {}. A reply operation leaves SuccessBody nil, so every
// answer there is described by its own Structure.
func responseStructure(operation speedhttp.OperationDescriptor, response speedhttp.Response) reflect.Type {
	if operation.SuccessBody != nil && response.Status == operation.SuccessStatus {
		return operation.SuccessBody
	}
	if response.Body == nil {
		return nil
	}
	return response.Body.Structure
}

// addResponse adds one declared answer to the operation. The body's shape is
// taken from the declaration: a JSON body publishes its structure, an
// octet-stream body is a binary string, a stream carries its media type with no
// JSON schema, and a response with no body publishes no content. A JSON answer
// must name its structure: the entry point accepts any JSON value for a JSON
// answer and cannot tell whether it matches a declaration that names none, so
// the reflector's unconstrained default would describe a body the contract
// does not fix.
func addResponse(ctx openapi.OperationContext, operation speedhttp.OperationDescriptor, response speedhttp.Response) error {
	if noBodyStatus(response.Status) && response.Body != nil {
		return fmt.Errorf("the %d response declares a body, but a %d response carries none", response.Status, response.Status)
	}

	structure := responseStructure(operation, response)
	mediaType := ""
	if response.Body != nil {
		mediaType = normaliseMediaType(response.Body.MediaType)
	}
	// The simple form writes its success body as JSON whatever the declaration
	// names, and the entry point refuses any other success media type.
	simpleSuccess := operation.SuccessBody != nil && response.Status == operation.SuccessStatus
	if simpleSuccess {
		if response.Body != nil && mediaType != "" && mediaType != mediaTypeJSON {
			return fmt.Errorf("a simple operation's success response must be application/json, not %q", response.Body.MediaType)
		}
		mediaType = mediaTypeJSON
	}
	// A reply answer's body is written with the declared media type, and an
	// empty one cannot be described: a JSON reply is refused at run time, and
	// the only reply that passes writes an empty Content-Type. Refuse the
	// declaration rather than publish a media type the entry point never
	// writes.
	if !simpleSuccess && response.Body != nil && mediaType == "" {
		return fmt.Errorf("the %d response declares a body with no media type", response.Status)
	}

	binary := false
	eventStream := false
	emptyObject := false
	content := response.Body != nil || simpleSuccess
	switch {
	case !content:
		structure = nil
	case mediaType == mediaTypeOctetStream:
		// An octet-stream answer is always a raw byte string. Whatever Structure
		// the declaration names describes a value the entry point writes as
		// bytes, so the binary schema is published instead.
		binary = true
		structure = nil
	case mediaType == mediaTypeEventStream:
		// An event stream is a stream of frames, not a JSON value: the media
		// type is published without a body schema.
		eventStream = true
		structure = nil
	case isJSONMediaType(mediaType):
		if structure == nil {
			return fmt.Errorf("the %d response declares %s without a Structure", response.Status, mediaType)
		}
		// The library reflects an empty struct as an unconstrained string. The
		// entry point writes it as the JSON object {}, so publish that instead.
		if isEmptyStruct(structure) {
			emptyObject = true
			structure = nil
		}
	case response.Body != nil:
		// A stream or byte answer of another media type carries the handler's
		// bytes; the declared Structure does not describe a JSON value. A text
		// media type is published as a string, anything else as binary octets.
		structure = nil
		binary = !isTextMediaType(mediaType)
	}

	options := []openapi.ContentOption{func(cu *openapi.ContentUnit) {
		cu.HTTPStatus = response.Status
		cu.Description = response.Description
	}}
	headers := responseHeaders(response.Headers)
	// The simple form writes the declared headers on its success response
	// only: a handler's failures are problem documents, and the entry point
	// writes no declared header on them.
	if operation.Simple && response.Status != operation.SuccessStatus {
		headers = nil
	}
	// An event stream is written through a reply, and its writer supplies
	// Cache-Control: no-store unless the declaration names a Cache-Control of
	// its own.
	if eventStream && !operation.Simple && !declaresHeader(response.Headers, "Cache-Control") {
		description := "Always no-store: an event stream must not be cached."
		headers["Cache-Control"] = openapi31.HeaderOrReference{Header: &openapi31.Header{
			Description: &description,
			Schema:      map[string]interface{}{"type": "string"},
		}}
	}
	if len(headers) > 0 || binary || eventStream || emptyObject {
		options = append(options, openapi.WithCustomize(func(cor openapi.ContentOrReference) {
			resp, ok := cor.(*openapi31.ResponseOrReference)
			if !ok || resp.Response == nil {
				return
			}
			for name, header := range headers {
				resp.Response.WithHeadersItem(name, header)
			}
			if binary {
				setResponseSchema(resp.Response, mediaType, map[string]interface{}{"type": "string", "format": "binary"})
			}
			if eventStream {
				clearResponseSchema(resp.Response, mediaType)
			}
			if emptyObject {
				setResponseSchema(resp.Response, mediaType, map[string]interface{}{
					"type":                 "object",
					"additionalProperties": false,
				})
			}
		}))
	}
	if content {
		// The declared spelling is what the entry point writes, except for the
		// simple form's success body, which the entry point always writes as
		// JSON.
		contentType := mediaType
		if !simpleSuccess && response.Body != nil {
			contentType = response.Body.MediaType
		}
		options = append(options, openapi.WithContentType(contentType))
	}
	if structure == nil {
		ctx.AddRespStructure(nil, options...)
		return nil
	}
	ctx.AddRespStructure(instance(structure), options...)
	return nil
}

// setResponseSchema replaces the schema of one media type in a response. The
// reflector may key the entry by the declared media type with its parameters,
// so entries are matched by their normalised media type.
func setResponseSchema(response *openapi31.Response, mediaType string, schema map[string]interface{}) {
	if response.Content == nil {
		response.Content = map[string]openapi31.MediaType{}
	}
	replaced := false
	for key := range response.Content {
		if normaliseMediaType(key) == mediaType {
			response.Content[key] = openapi31.MediaType{Schema: schema}
			replaced = true
		}
	}
	if !replaced {
		response.Content[mediaType] = openapi31.MediaType{Schema: schema}
	}
}

// clearResponseSchema removes the schema from one media type in a response,
// keeping the media type itself.
func clearResponseSchema(response *openapi31.Response, mediaType string) {
	replaced := false
	for key := range response.Content {
		if normaliseMediaType(key) == mediaType {
			response.Content[key] = openapi31.MediaType{}
			replaced = true
		}
	}
	if !replaced {
		if response.Content == nil {
			response.Content = map[string]openapi31.MediaType{}
		}
		response.Content[mediaType] = openapi31.MediaType{}
	}
}

// isTextMediaType reports whether a media type carries text rather than
// arbitrary octets.
func isTextMediaType(mediaType string) bool {
	return strings.HasPrefix(mediaType, "text/") || strings.HasSuffix(mediaType, "+xml")
}

// responseHeaders maps the declared headers to the response's header entries.
func responseHeaders(headers []speedhttp.Header) map[string]openapi31.HeaderOrReference {
	out := map[string]openapi31.HeaderOrReference{}
	for _, header := range headers {
		description := header.Description
		out[header.Name] = openapi31.HeaderOrReference{Header: &openapi31.Header{
			Description: &description,
			Schema:      map[string]interface{}{"type": "string"},
		}}
	}
	return out
}

// declaresHeader reports whether the declaration names a header, comparing
// names the way HTTP does.
func declaresHeader(headers []speedhttp.Header, name string) bool {
	for _, header := range headers {
		if strings.EqualFold(header.Name, name) {
			return true
		}
	}
	return false
}

// noBodyStatus reports whether a status's answer carries no message body.
func noBodyStatus(status int) bool {
	return status == http.StatusNoContent || status == http.StatusNotModified
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
// no null in type unions, no duplicated parameter descriptions, and no default
// header style. It keeps every media type and binary format as generated.
func normalise(raw []byte) ([]byte, error) {
	var doc map[string]interface{}
	if err := json.Unmarshal(raw, &doc); err != nil {
		return nil, fmt.Errorf("openapigen: reading the generated fragment: %w", err)
	}
	walk(doc)
	normaliseSurfaces(doc)
	var out strings.Builder
	encoder := json.NewEncoder(&out)
	encoder.SetEscapeHTML(false)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(doc); err != nil {
		return nil, fmt.Errorf("openapigen: writing the fragment: %w", err)
	}
	return []byte(out.String()), nil
}

// walk normalises every node in place.
func walk(value interface{}) {
	switch node := value.(type) {
	case map[string]interface{}:
		dropNullType(node)
		for _, child := range node {
			walk(child)
		}
	case []interface{}:
		for _, child := range node {
			walk(child)
		}
	}
}

// methodKeys are the members of a path item that hold an operation, as the
// OpenAPI document spells them.
var methodKeys = []string{"get", "put", "post", "delete", "options", "head", "patch", "trace"}

// normaliseSurfaces applies the two normalisations that belong to a parameter
// object and a response header object. It reaches those objects by their
// position in the document rather than by their members: a business schema's
// properties map is free to carry members named "schema", "style", "in" or
// "name", and it must not be read as a header or a parameter.
func normaliseSurfaces(doc map[string]interface{}) {
	paths, ok := doc["paths"].(map[string]interface{})
	if !ok {
		return
	}
	for _, rawItem := range paths {
		item, ok := rawItem.(map[string]interface{})
		if !ok {
			continue
		}
		for _, method := range methodKeys {
			operation, ok := item[method].(map[string]interface{})
			if !ok {
				continue
			}
			normaliseParameters(operation["parameters"])
			normaliseResponseHeaders(operation["responses"])
		}
	}
}

// normaliseParameters removes the duplicated description from each parameter
// object in one operation's parameter list.
func normaliseParameters(value interface{}) {
	parameters, ok := value.([]interface{})
	if !ok {
		return
	}
	for _, raw := range parameters {
		if parameter, ok := raw.(map[string]interface{}); ok {
			dropParameterSchemaDescription(parameter)
		}
	}
}

// normaliseResponseHeaders removes the default style from every header object
// on one operation's responses.
func normaliseResponseHeaders(value interface{}) {
	responses, ok := value.(map[string]interface{})
	if !ok {
		return
	}
	for _, rawResponse := range responses {
		response, ok := rawResponse.(map[string]interface{})
		if !ok {
			continue
		}
		headers, ok := response["headers"].(map[string]interface{})
		if !ok {
			continue
		}
		for _, rawHeader := range headers {
			if header, ok := rawHeader.(map[string]interface{}); ok {
				dropHeaderStyle(header)
			}
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
