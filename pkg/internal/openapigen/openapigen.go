// Package openapigen turns a module's operation declarations into the OpenAPI fragment the module embeds; it is a build-time dependency only.
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

const (
	mediaTypeJSON        = "application/json"
	mediaTypeForm        = "application/x-www-form-urlencoded"
	mediaTypeMultipart   = "multipart/form-data"
	mediaTypeOctetStream = "application/octet-stream"
	mediaTypeEventStream = "text/event-stream"
	mediaTypeAny         = "*/*"
)

var parameterTags = []string{"path", "query", "header", "cookie"}

var (
	nonParameterTags = []string{"path", "query", "header", "cookie", "json", "form", "file", "raw", "formData", "contentType"}
	nonJSONTags      = []string{"path", "query", "header", "cookie", "form", "file", "raw", "formData", "contentType"}
	nonFormTags      = []string{"path", "query", "header", "cookie", "json", "form", "file", "raw", "formData", "contentType"}
	nonMultipartTags = []string{"path", "query", "header", "cookie", "json", "form", "file", "raw", "formData", "contentType"}
)

var problemDocumentType = reflect.TypeOf(speedhttp.ProblemDocument{})

const (
	bearerAuthName    = "bearerAuth"
	sessionCookieName = "sessionCookie"
)

func securityFor(access speedhttp.Access) []interface{} {
	switch access {
	case speedhttp.AccessPublic:
		return []interface{}{}
	case speedhttp.AccessOptional:
		return []interface{}{
			map[string]interface{}{},
			map[string]interface{}{bearerAuthName: []interface{}{}},
			map[string]interface{}{sessionCookieName: []interface{}{}},
		}
	default:
		return []interface{}{
			map[string]interface{}{bearerAuthName: []interface{}{}},
			map[string]interface{}{sessionCookieName: []interface{}{}},
		}
	}
}

// Generate builds the OpenAPI fragment of one module from its declarations.
// Title and Version become the fragment's info section; the fragment's paths stay relative to the
// API root, as the platform's fragments are.
func Generate(title, version string, operations speedhttp.Operations) ([]byte, error) {
	r := openapi31.NewReflector()
	r.DefaultOptions = append(r.DefaultOptions, jsonschema.InterceptDefName(func(t reflect.Type, _ string) string {
		if isProblemDocument(t) {
			return "Problem"
		}
		return t.Name()
	}))
	r.DefaultOptions = append(r.DefaultOptions, jsonschema.InterceptProp(func(params jsonschema.InterceptPropParams) error {
		if !params.Processed {
			return nil
		}
		if value, declared := params.Field.Tag.Lookup("nullable"); !declared || value != "true" {
			return nil
		}
		if params.Field.Type.Kind() != reflect.Pointer {
			return fmt.Errorf("openapigen: the field %q marks a non-pointer type nullable", params.Name)
		}
		if omitsEmpty(params.Field) {
			return fmt.Errorf("openapigen: the field %q marks a nullable field omitempty or omitzero, which leaves the key out instead of writing null", params.Name)
		}
		if params.PropertySchema.Ref != nil {
			ref := *params.PropertySchema.Ref
			refSchema := jsonschema.Schema{Ref: &ref}
			params.PropertySchema.Ref = nil
			params.PropertySchema.WithAnyOf(
				jsonschema.Null.ToSchemaOrBool(),
				refSchema.ToSchemaOrBool(),
			)
			return nil
		}
		params.PropertySchema.WithExtraPropertiesItem(nullableMarker, true)
		return nil
	}))
	r.Spec.Info.WithTitle(title).WithVersion(version)

	bodyNames := map[string]string{}
	access := map[string]speedhttp.Access{}
	for index, declared := range operations {
		operation := declared.Descriptor()
		if operation.Request != nil && operation.Request.Name != "" {
			bodyNames[operation.ID] = operation.Request.Name
		}
		access[operation.ID] = operation.Access
		if operation.Simple && countSuccess(operation.Responses) != 1 {
			return nil, fmt.Errorf("openapigen: %s: a simple operation must declare exactly one 2xx response", operation.ID)
		}
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
		if operation.Request != nil && !declaresStatus(operation.Responses, http.StatusRequestEntityTooLarge) {
			if err := addResponse(ctx, operation, speedhttp.ProblemResponse(http.StatusRequestEntityTooLarge,
				"The request body is over the endpoint's limit")); err != nil {
				return nil, fmt.Errorf("openapigen: %s %s: %w", operation.Method, operation.Path, err)
			}
		}
		if err := r.AddOperation(ctx); err != nil {
			return nil, fmt.Errorf("openapigen: %s %s: %w", operation.Method, operation.Path, err)
		}
		if err := addRequestBody(r, index, operation); err != nil {
			return nil, fmt.Errorf("openapigen: %s %s: %w", operation.Method, operation.Path, err)
		}
	}

	raw, err := r.Spec.MarshalJSON()
	if err != nil {
		return nil, fmt.Errorf("openapigen: writing the fragment: %w", err)
	}
	return normalise(raw, bodyNames, access)
}

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

func declaresStatus(responses []speedhttp.Response, status int) bool {
	for _, response := range responses {
		if response.Status == status {
			return true
		}
	}
	return false
}

func countSuccess(responses []speedhttp.Response) int {
	count := 0
	for _, response := range responses {
		if response.Status >= 200 && response.Status < 300 {
			count++
		}
	}
	return count
}

func addRequestBody(r *openapi31.Reflector, index int, operation speedhttp.OperationDescriptor) error {
	mediaType := ""
	if operation.Request != nil {
		mediaType = normaliseMediaType(operation.Request.MediaType)
	}
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
		return fmt.Errorf("unsupported request media type %q", operation.Request.MediaType)
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
	if target.RequestBody.RequestBody != nil {
		if operation.Request.Description != "" {
			target.RequestBody.RequestBody.WithDescription(operation.Request.Description)
		}
		if operation.Request.Required {
			target.RequestBody.RequestBody.WithRequired(true)
		}
	}
	return nil
}

func requestBodyPath(r *openapi31.Reflector, index int) string {
	path := fmt.Sprintf("/__openapigen_request_body_%d", index)
	for {
		if _, exists := r.Spec.Paths.MapOfPathItemValues[path]; !exists {
			return path
		}
		path += "_"
	}
}

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

// multipartContentType corrects the media type the library picks: it names a
// form-only multipart body application/x-www-form-urlencoded, which pkg/http
// refuses at request time.
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

func parameterOf(field reflect.StructField) (string, string) {
	for _, source := range parameterTags {
		if name := field.Tag.Get(source); name != "" {
			return source, name
		}
	}
	return "", ""
}

func jsonBodyStructure(input reflect.Type) reflect.Type {
	return bodyStructure(input, func(field reflect.StructField) (reflect.Type, string, bool) {
		if taggedName(field, "json") == "" {
			return nil, "", false
		}
		return field.Type, string(rebuildTag(field.Tag, nonJSONTags, "", "")), true
	})
}

func formBodyStructure(input reflect.Type) reflect.Type {
	return bodyStructure(input, func(field reflect.StructField) (reflect.Type, string, bool) {
		name := taggedName(field, "form")
		if name == "" {
			return nil, "", false
		}
		return field.Type, string(rebuildTag(field.Tag, nonFormTags, "formData", name)), true
	})
}

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

func bodyStructure(input reflect.Type, pick func(reflect.StructField) (reflect.Type, string, bool)) reflect.Type {
	fields := make([]reflect.StructField, 0, input.NumField())
	description := ""
	open := false
	for i := 0; i < input.NumField(); i++ {
		field := input.Field(i)
		if field.Name == "_" && field.Type.Kind() == reflect.Struct {
			description = field.Tag.Get("description")
			open = field.Tag.Get("additionalProperties") == "true"
			continue
		}
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
	fields = append(fields, objectField(description, open))
	return reflect.StructOf(fields)
}

func objectField(description string, open bool) reflect.StructField {
	tag := `additionalProperties:"false"`
	if open {
		tag = `additionalProperties:"true"`
	}
	if description != "" {
		tag += " description:" + strconv.Quote(description)
	}
	return reflect.StructField{
		Name:    "_",
		PkgPath: "openapigen",
		Type:    reflect.TypeOf(struct{}{}),
		Tag:     reflect.StructTag(tag),
	}
}

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

func bindableScalar(t reflect.Type) bool {
	switch t.Kind() {
	case reflect.String,
		reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Bool:
		return true
	}
	return false
}

func bindableSlice(t reflect.Type) bool {
	return t.Kind() == reflect.Slice && bindableScalar(t.Elem())
}

func isEmptyStruct(t reflect.Type) bool {
	return t.Kind() == reflect.Struct && t.NumField() == 0
}

func isBytes(t reflect.Type) bool {
	return t.Kind() == reflect.Slice && t.Elem().Kind() == reflect.Uint8
}

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

func taggedName(field reflect.StructField, tag string) string {
	name := strings.Split(field.Tag.Get(tag), ",")[0]
	if name == "-" {
		return ""
	}
	return name
}

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

func contains(values []string, value string) bool {
	for _, candidate := range values {
		if candidate == value {
			return true
		}
	}
	return false
}

func normaliseMediaType(value string) string {
	mediaType, _, err := mime.ParseMediaType(value)
	if err != nil {
		return strings.ToLower(strings.TrimSpace(value))
	}
	return strings.ToLower(mediaType)
}

func isJSONMediaType(mediaType string) bool {
	return mediaType == mediaTypeJSON || strings.HasSuffix(mediaType, "+json")
}

func isProblemDocument(t reflect.Type) bool {
	return t == problemDocumentType || (t.Kind() == reflect.Pointer && t.Elem() == problemDocumentType)
}

func responseStructure(operation speedhttp.OperationDescriptor, response speedhttp.Response) reflect.Type {
	if operation.SuccessBody != nil && response.Status == operation.SuccessStatus {
		return operation.SuccessBody
	}
	if response.Body == nil {
		return nil
	}
	return response.Body.Structure
}

func addResponse(ctx openapi.OperationContext, operation speedhttp.OperationDescriptor, response speedhttp.Response) error {
	if noBodyStatus(response.Status) && response.Body != nil {
		return fmt.Errorf("the %d response declares a body, but a %d response carries none", response.Status, response.Status)
	}

	structure := responseStructure(operation, response)
	mediaType := ""
	if response.Body != nil {
		mediaType = normaliseMediaType(response.Body.MediaType)
	}
	simpleSuccess := operation.SuccessBody != nil && response.Status == operation.SuccessStatus
	if simpleSuccess {
		if response.Body != nil && mediaType != "" && mediaType != mediaTypeJSON {
			return fmt.Errorf("a simple operation's success response must be application/json, not %q", response.Body.MediaType)
		}
		mediaType = mediaTypeJSON
	}
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
	case mediaType == mediaTypeAny:
		binary = true
		structure = nil
	case mediaType == mediaTypeOctetStream:
		binary = true
		structure = nil
	case mediaType == mediaTypeEventStream:
		eventStream = true
		structure = nil
	case isJSONMediaType(mediaType):
		if structure == nil {
			return fmt.Errorf("the %d response declares %s without a Structure", response.Status, mediaType)
		}
		if err := checkResponsePointers(structure, "", map[reflect.Type]bool{}); err != nil {
			return err
		}
		// The library reflects an empty struct as an unconstrained string; the
		// entry point writes it as the JSON object {}.
		if isEmptyStruct(structure) {
			emptyObject = true
			structure = nil
		}
	case response.Body != nil:
		structure = nil
		binary = !isTextMediaType(mediaType)
	}

	options := []openapi.ContentOption{func(cu *openapi.ContentUnit) {
		cu.HTTPStatus = response.Status
		cu.Description = response.Description
	}}
	headers := responseHeaders(response.Headers)
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

func isTextMediaType(mediaType string) bool {
	return strings.HasPrefix(mediaType, "text/") || strings.HasSuffix(mediaType, "+xml")
}

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

func declaresHeader(headers []speedhttp.Header, name string) bool {
	for _, header := range headers {
		if strings.EqualFold(header.Name, name) {
			return true
		}
	}
	return false
}

func noBodyStatus(status int) bool {
	return status == http.StatusNoContent || status == http.StatusNotModified
}

func instance(t reflect.Type) interface{} {
	return reflect.New(t).Interface()
}

func ginPathToOpenAPI(path string) string {
	segments := strings.Split(path, "/")
	for i, segment := range segments {
		if strings.HasPrefix(segment, ":") {
			segments[i] = "{" + strings.TrimPrefix(segment, ":") + "}"
		}
	}
	return strings.Join(segments, "/")
}

func normalise(raw []byte, bodyNames map[string]string, access map[string]speedhttp.Access) ([]byte, error) {
	var doc map[string]interface{}
	if err := json.Unmarshal(raw, &doc); err != nil {
		return nil, fmt.Errorf("openapigen: reading the generated fragment: %w", err)
	}
	walk(doc)
	normaliseSurfaces(doc)
	if err := applySecurity(doc, access); err != nil {
		return nil, err
	}
	if err := nameRequestBodies(doc, bodyNames); err != nil {
		return nil, err
	}
	var out strings.Builder
	encoder := json.NewEncoder(&out)
	encoder.SetEscapeHTML(false)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(doc); err != nil {
		return nil, fmt.Errorf("openapigen: writing the fragment: %w", err)
	}
	return []byte(out.String()), nil
}

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

func applySecurity(doc map[string]interface{}, access map[string]speedhttp.Access) error {
	paths, ok := doc["paths"].(map[string]interface{})
	if !ok {
		return nil
	}
	for path, rawItem := range paths {
		item, ok := rawItem.(map[string]interface{})
		if !ok {
			continue
		}
		for _, method := range methodKeys {
			operation, ok := item[method].(map[string]interface{})
			if !ok {
				continue
			}
			id, _ := operation["operationId"].(string)
			level, declared := access[id]
			if !declared {
				return fmt.Errorf("openapigen: the %s %s operation carries no declared access level", strings.ToUpper(method), path)
			}
			operation["security"] = securityFor(level)
		}
	}
	return nil
}

func nameRequestBodies(doc map[string]interface{}, names map[string]string) error {
	if len(names) == 0 {
		return nil
	}
	paths, ok := doc["paths"].(map[string]interface{})
	if !ok {
		return nil
	}
	components, _ := doc["components"].(map[string]interface{})
	if components == nil {
		components = map[string]interface{}{}
		doc["components"] = components
	}
	schemas, _ := components["schemas"].(map[string]interface{})
	if schemas == nil {
		schemas = map[string]interface{}{}
		components["schemas"] = schemas
	}
	for _, item := range paths {
		operations, ok := item.(map[string]interface{})
		if !ok {
			continue
		}
		for _, method := range methodKeys {
			operation, ok := operations[method].(map[string]interface{})
			if !ok {
				continue
			}
			id, _ := operation["operationId"].(string)
			name, named := names[id]
			if !named {
				continue
			}
			body, _ := operation["requestBody"].(map[string]interface{})
			content, _ := body["content"].(map[string]interface{})
			for _, mediaValue := range content {
				media, ok := mediaValue.(map[string]interface{})
				if !ok {
					continue
				}
				schema, ok := media["schema"].(map[string]interface{})
				if !ok {
					continue
				}
				if existing, found := schemas[name]; found {
					if !equalJSON(existing, schema) {
						return fmt.Errorf("openapigen: the request body %q is declared with different shapes", name)
					}
				} else {
					schemas[name] = schema
				}
				media["schema"] = map[string]interface{}{"$ref": "#/components/schemas/" + name}
			}
		}
	}
	return nil
}

func equalJSON(a, b interface{}) bool {
	left, errLeft := json.Marshal(a)
	right, errRight := json.Marshal(b)
	return errLeft == nil && errRight == nil && string(left) == string(right)
}

var methodKeys = []string{"get", "put", "post", "delete", "options", "head", "patch", "trace"}

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

func checkResponsePointers(t reflect.Type, path string, seen map[reflect.Type]bool) error {
	switch t.Kind() {
	case reflect.Pointer:
		return checkResponseElements(t.Elem(), path, seen)
	case reflect.Slice, reflect.Array, reflect.Map:
		return checkResponseElements(t.Elem(), path, seen)
	case reflect.Struct:
		return checkResponseStruct(t, path, seen)
	default:
		return nil
	}
}

func checkResponseElements(t reflect.Type, path string, seen map[reflect.Type]bool) error {
	for {
		switch t.Kind() {
		case reflect.Slice, reflect.Array, reflect.Map:
			if seen[t] {
				return nil
			}
			seen[t] = true
			t = t.Elem()
		case reflect.Pointer:
			return fmt.Errorf("the response element %q is a pointer; a nil element is written as null and no per-item tag can mark it nullable", path)
		case reflect.Struct:
			return checkResponseStruct(t, path, seen)
		default:
			return nil
		}
	}
}

func checkResponseStruct(t reflect.Type, path string, seen map[reflect.Type]bool) error {
	if seen[t] {
		return nil
	}
	seen[t] = true
	for i := 0; i < t.NumField(); i++ {
		field := t.Field(i)
		name, named := jsonFieldName(field)
		embedded := field.Type
		if embedded.Kind() == reflect.Pointer {
			embedded = embedded.Elem()
		}
		inline := field.Anonymous && !named && embedded.Kind() == reflect.Struct
		if field.PkgPath != "" {
			if !field.Anonymous || embedded.Kind() != reflect.Struct {
				continue
			}
		}
		if tag, declared := field.Tag.Lookup("json"); declared && tag == "-" {
			continue
		}
		fieldPath := path
		if !inline {
			if path != "" {
				fieldPath = path + "." + name
			} else {
				fieldPath = name
			}
		}
		if field.Type.Kind() == reflect.Pointer && !inline &&
			!omitsEmpty(field) && field.Tag.Get("nullable") != "true" {
			return fmt.Errorf("the response field %q is a pointer with no omitempty, no omitzero and no nullable:\"true\"; a nil pointer is written as null, so the contract cannot declare it non-null", fieldPath)
		}
		if err := checkResponsePointers(field.Type, fieldPath, seen); err != nil {
			return err
		}
	}
	return nil
}

func jsonFieldName(field reflect.StructField) (string, bool) {
	tag, declared := field.Tag.Lookup("json")
	if !declared {
		return field.Name, false
	}
	name := strings.Split(tag, ",")[0]
	if name == "" {
		return field.Name, false
	}
	return name, true
}

func omitsEmpty(field reflect.StructField) bool {
	tag, declared := field.Tag.Lookup("json")
	if !declared {
		return false
	}
	for _, option := range strings.Split(tag, ",")[1:] {
		if option == "omitempty" || option == "omitzero" {
			return true
		}
	}
	return false
}

const nullableMarker = "x-openapigen-nullable"

func dropNullType(node map[string]interface{}) {
	keepNull := false
	if marker, marked := node[nullableMarker]; marked {
		delete(node, nullableMarker)
		keepNull, _ = marker.(bool)
	}
	if keepNull {
		return
	}
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

func dropHeaderStyle(node map[string]interface{}) {
	if _, ok := node["schema"]; ok {
		delete(node, "style")
	}
}

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
