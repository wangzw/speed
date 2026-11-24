package http

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/url"
	"reflect"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
)

// The media types an operation's request body may declare.
const (
	requestMediaTypeJSON        = "application/json"
	requestMediaTypeForm        = "application/x-www-form-urlencoded"
	requestMediaTypeMultipart   = "multipart/form-data"
	requestMediaTypeOctetStream = "application/octet-stream"
)

const multipartMemory = 32 << 20

// Failure is an error that names the status a handler wants answered.
type Failure interface {
	error
	// StatusCode is the response status the failure belongs to.
	StatusCode() int
}

// bind builds the entry point's handler for one declared operation: it binds the declared input from the request, calls the handler and writes the declared success response. A handler never sees the request, so a parameter the declaration does not carry cannot be read and a body field it does not describe cannot be decoded.
func bind[In, Out any](
	handle func(context.Context, In) (Out, error),
	operationID string,
	input reflect.Type,
	request *RequestBody,
	responses []Response,
) gin.HandlerFunc {
	status := successStatus(responses)
	if status == 0 {
		panic("http: an operation must declare exactly one 2xx response")
	}
	checkResponseBodies(responses)
	if noBodyStatus(status) && bodyType[Out]() != nil {
		panic(fmt.Sprintf(
			"http: the success response %d carries no body, but the handler returns one; use an empty Out type",
			status,
		))
	}
	success, ok := responseOf(responses, status)
	if !ok {
		panic("http: the declared success response is missing from the response set")
	}
	// The simple form always answers its success body as JSON, so a
	// declaration of any other media type would publish a contract the entry
	// point cannot keep. An empty media type defaults to JSON.
	if success.Body != nil {
		if mediaType := normaliseMediaType(success.Body.MediaType); mediaType != "" && mediaType != requestMediaTypeJSON {
			panic(fmt.Sprintf(
				"http: a simple operation's success response must be application/json, not %q; use ReplyOperation to answer another media type",
				success.Body.MediaType,
			))
		}
	}
	binding := newRequestBinding(input, request)
	return func(c *gin.Context) {
		c.Set(declaredFailuresContextKey, declaredResponses(responses))
		in, err := bindInput[In](c, binding)
		if err != nil {
			refuseBinding(c, responses, err)
			return
		}
		out, err := handle(c.Request.Context(), in)
		if err != nil {
			writeFailure(c, operationID, responses, err)
			return
		}
		for _, header := range success.Headers {
			c.Header(header.Name, header.Value)
		}
		if success.Status == http.StatusNoContent {
			c.Status(success.Status)
			c.Writer.WriteHeaderNow()
			return
		}
		c.JSON(success.Status, out)
	}
}

type requestBinding struct {
	input     reflect.Type
	mediaType string
	required  bool
	json      map[string]int
	form      map[string]int
	file      map[string]int
	raw       int
}

// newRequestBinding reads an operation's request declaration and panics on a
// declaration no request could ever be bound against. The check is at handler
// build time because it is a defect in the declaration, not in a request.
func newRequestBinding(input reflect.Type, request *RequestBody) requestBinding {
	requestMediaType := ""
	required := false
	if request != nil {
		requestMediaType = request.MediaType
		required = request.Required
	}
	binding := requestBinding{
		input:     input,
		mediaType: normaliseMediaType(requestMediaType),
		required:  required,
		raw:       -1,
	}
	for i := 0; input != nil && i < input.NumField(); i++ {
		field := input.Field(i)
		if field.PkgPath != "" {
			continue
		}
		if field.Tag.Get("raw") == "true" {
			if binding.raw != -1 {
				panic("http: the input carries more than one raw-tagged field")
			}
			if !isBytes(field.Type) {
				panic(fmt.Sprintf("http: the raw-tagged field %q must be []byte", field.Name))
			}
			binding.raw = i
		}
		if name := field.Tag.Get("file"); name != "" && !isBytes(field.Type) {
			panic(fmt.Sprintf("http: the file-tagged field %q must be []byte", field.Name))
		}
	}
	binding.json = jsonFields(input)
	binding.form = taggedFields(input, "form")
	binding.file = taggedFields(input, "file")

	// A file-tagged field is bound by the multipart reader only. Under any
	// other media type the field would be declared and never read, so the
	// declaration is refused here rather than silently ignored.
	if len(binding.file) > 0 && binding.mediaType != requestMediaTypeMultipart {
		panic("http: a file-tagged field requires the multipart/form-data media type")
	}

	switch binding.mediaType {
	case "":
		if binding.raw != -1 {
			panic("http: a raw-tagged field requires the application/octet-stream media type")
		}
		// A body-tagged field under an empty media type would be declared and
		// never read. A file-tagged field is refused above, with the media
		// type it requires.
		if len(binding.json) > 0 || len(binding.form) > 0 {
			panic("http: the input carries body-tagged fields but the operation declares no request media type")
		}
	case requestMediaTypeJSON:
		if binding.raw != -1 {
			panic("http: a raw-tagged field requires the application/octet-stream media type")
		}
		if len(binding.form) > 0 {
			panic("http: a form-tagged field requires the application/x-www-form-urlencoded or multipart/form-data media type")
		}
		if len(binding.json) == 0 {
			panic("http: a request body media type is declared but the input carries no json-tagged field")
		}
	case requestMediaTypeForm:
		if binding.raw != -1 {
			panic("http: a raw-tagged field requires the application/octet-stream media type")
		}
		if len(binding.json) > 0 {
			panic("http: a json-tagged field requires the application/json media type")
		}
		if len(binding.form) == 0 {
			panic("http: a form media type is declared but the input carries no form-tagged field")
		}
	case requestMediaTypeMultipart:
		if binding.raw != -1 {
			panic("http: a raw-tagged field requires the application/octet-stream media type")
		}
		if len(binding.json) > 0 {
			panic("http: a json-tagged field requires the application/json media type")
		}
		if len(binding.form)+len(binding.file) == 0 {
			panic("http: a multipart media type is declared but the input carries no form-tagged or file-tagged field")
		}
	case requestMediaTypeOctetStream:
		if binding.raw == -1 {
			panic("http: the application/octet-stream media type requires exactly one raw-tagged field")
		}
		if len(binding.json) > 0 {
			panic("http: a json-tagged field requires the application/json media type")
		}
		if len(binding.form) > 0 {
			panic("http: a form-tagged field requires the application/x-www-form-urlencoded or multipart/form-data media type")
		}
	default:
		panic(fmt.Sprintf("http: unsupported request media type %q", requestMediaType))
	}
	return binding
}

func isBytes(t reflect.Type) bool {
	return t.Kind() == reflect.Slice && t.Elem().Kind() == reflect.Uint8
}

func bindInput[In any](c *gin.Context, binding requestBinding) (In, error) {
	var in In
	if binding.input != nil {
		value := reflect.ValueOf(&in).Elem()
		if err := bindParameters(c, value, binding.input); err != nil {
			return in, err
		}
		if binding.mediaType != "" {
			if err := binding.bindBody(c, value); err != nil {
				return in, err
			}
		}
	}
	if err := validateInput(in); err != nil {
		return in, &bindingError{status: http.StatusUnprocessableEntity, reason: err.Error()}
	}
	return in, nil
}

// validateInput runs the input's own check, when it has one. Both the value and
// its address are tried, because a Validate with a pointer receiver does not
// belong to the value's method set.
func validateInput[In any](in In) error {
	if validator, ok := any(&in).(interface{ Validate() error }); ok {
		return validator.Validate()
	}
	if validator, ok := any(in).(interface{ Validate() error }); ok {
		return validator.Validate()
	}
	return nil
}

func refuseBinding(c *gin.Context, responses []Response, err error) {
	status := statusOf(err)
	problem := Problem{Status: status}
	if response, ok := responseOf(responses, status); ok {
		problem.Type = response.ProblemType
	}
	if status == http.StatusUnprocessableEntity {
		problem.Detail = err.Error()
	}
	Fail(c, problem)
}

func normaliseMediaType(value string) string {
	mediaType, _, err := mime.ParseMediaType(value)
	if err != nil {
		return strings.ToLower(strings.TrimSpace(value))
	}
	return strings.ToLower(mediaType)
}

func contentTypeMatches(header, declared string) bool {
	mediaType, params, err := mime.ParseMediaType(header)
	if err != nil {
		return false
	}
	if !strings.EqualFold(mediaType, declared) {
		return false
	}
	if declared != requestMediaTypeJSON {
		return true
	}
	for name, value := range params {
		if !strings.EqualFold(name, "charset") || !strings.EqualFold(value, "utf-8") {
			return false
		}
	}
	return true
}

func requiredBodyError() error {
	return &bindingError{status: http.StatusBadRequest, reason: "http: the request body is required"}
}

func responseOf(responses []Response, status int) (Response, bool) {
	for _, response := range responses {
		if response.Status == status {
			return response, true
		}
	}
	return Response{}, false
}

// checkResponseBodies refuses a declaration that gives a body to a status that
// carries none. Such a body could never be written, so the declaration would
// publish a contract the entry point cannot keep.
func checkResponseBodies(responses []Response) {
	for _, response := range responses {
		if response.Body != nil && noBodyStatus(response.Status) {
			panic(fmt.Sprintf(
				"http: the response %d declares a body, but a %d response carries none",
				response.Status, response.Status,
			))
		}
	}
}

func noBodyStatus(status int) bool {
	return status == http.StatusNoContent || status == http.StatusNotModified
}

func statusOf(err error) int {
	var failure Failure
	if errors.As(err, &failure) {
		return failure.StatusCode()
	}
	return http.StatusBadRequest
}

type bindingError struct {
	status int
	reason string
}

func (e *bindingError) Error() string   { return e.reason }
func (e *bindingError) StatusCode() int { return e.status }

func bindParameters(c *gin.Context, value reflect.Value, input reflect.Type) error {
	for i := 0; i < input.NumField(); i++ {
		field := input.Field(i)
		if field.PkgPath != "" {
			continue
		}
		target := value.Field(i)
		name, source := parameterOf(field)
		if name == "" {
			continue
		}
		var raw string
		var present bool
		switch source {
		case "path":
			raw = c.Param(name)
			present = raw != ""
		case "query":
			values := c.QueryArray(name)
			if len(values) > 0 {
				raw, present = values[0], true
			}
		case "header":
			raw = c.GetHeader(name)
			present = raw != ""
		case "cookie":
			cookie, err := c.Request.Cookie(name)
			if err == nil {
				raw = cookie.Value
				present = true
			}
		default:
			return &bindingError{
				status: http.StatusInternalServerError,
				reason: fmt.Sprintf("http: the field %q names an unsupported parameter source %q", field.Name, source),
			}
		}
		if !present {
			continue
		}
		// An empty query value means the parameter is absent unless the field holds text:
		// "?limit=" is how a client leaves an integer or a boolean unset, while "?q=" is a
		// real empty string.
		if source == "query" && raw == "" && !textual(target.Type()) {
			continue
		}
		if target.Kind() == reflect.Slice && source == "query" {
			if err := setValues(target, c.QueryArray(name)); err != nil {
				return &bindingError{
					status: http.StatusBadRequest,
					reason: fmt.Sprintf("http: the parameter %q is not a valid %s", name, target.Type().Elem()),
				}
			}
			continue
		}
		// A scalar field holds one value, so a repeated parameter fills it from the first
		// one; a slice field above reads every value.
		if err := setScalar(target, raw); err != nil {
			return &bindingError{
				status: http.StatusBadRequest,
				reason: fmt.Sprintf("http: the parameter %q is not a valid %s", name, target.Kind()),
			}
		}
	}
	return nil
}

func parameterOf(field reflect.StructField) (string, string) {
	for _, source := range []string{"path", "query", "header", "cookie"} {
		if name := field.Tag.Get(source); name != "" {
			return name, source
		}
	}
	return "", ""
}

func textual(t reflect.Type) bool {
	if t.Kind() == reflect.Slice {
		return t.Elem().Kind() == reflect.String
	}
	return t.Kind() == reflect.String
}

func setScalar(target reflect.Value, raw string) error {
	switch target.Kind() {
	case reflect.String:
		target.SetString(raw)
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		n, err := strconv.ParseInt(raw, 10, 64)
		if err != nil {
			return err
		}
		target.SetInt(n)
	case reflect.Bool:
		b, err := strconv.ParseBool(raw)
		if err != nil {
			return err
		}
		target.SetBool(b)
	default:
		return fmt.Errorf("unsupported parameter kind")
	}
	return nil
}

func setValues(target reflect.Value, values []string) error {
	if target.Kind() != reflect.Slice {
		if len(values) == 0 {
			return nil
		}
		return setScalar(target, values[0])
	}
	if target.Type().Elem().Kind() == reflect.String {
		slice := reflect.MakeSlice(target.Type(), len(values), len(values))
		for i, value := range values {
			slice.Index(i).SetString(value)
		}
		target.Set(slice)
		return nil
	}
	slice := reflect.MakeSlice(target.Type(), 0, len(values))
	for _, value := range values {
		item := reflect.New(target.Type().Elem()).Elem()
		if err := setScalar(item, value); err != nil {
			return err
		}
		slice = reflect.Append(slice, item)
	}
	target.Set(slice)
	return nil
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

func jsonFields(input reflect.Type) map[string]int {
	return taggedFields(input, "json")
}

// bindBody reads the request body into the input's fields for the declared media type. A Content-Type that is not the declared one is refused before the body is read, so a handler never sees a body decoded by a reader the declaration did not ask for.
func (b requestBinding) bindBody(c *gin.Context, value reflect.Value) error {
	if !contentTypeMatches(c.GetHeader("Content-Type"), b.mediaType) {
		return &bindingError{
			status: http.StatusUnsupportedMediaType,
			reason: fmt.Sprintf("http: the request body must be %s", b.mediaType),
		}
	}
	switch b.mediaType {
	case requestMediaTypeJSON:
		return b.bindJSON(c, value)
	case requestMediaTypeForm:
		return b.bindForm(c, value)
	case requestMediaTypeMultipart:
		return b.bindMultipart(c, value)
	case requestMediaTypeOctetStream:
		return b.bindRaw(c, value)
	}
	return nil
}

// bindJSON reads a JSON object into the input's json-tagged fields. Unknown members are refused, so a body carrying an undescribed field is a 400 rather than a silently dropped value; an empty body is the zero input when the declaration does not require one, and a 400 when it does.
func (b requestBinding) bindJSON(c *gin.Context, value reflect.Value) error {
	raw, err := readRequestBody(c)
	if err != nil {
		return err
	}
	if len(raw) == 0 {
		if b.required {
			return requiredBodyError()
		}
		return nil
	}
	var members map[string]json.RawMessage
	if err := json.Unmarshal(raw, &members); err != nil {
		return &bindingError{status: http.StatusBadRequest, reason: "http: the request body is not a JSON object"}
	}
	// A literal null decodes into a nil map without an error and binds as the empty
	// object, which is how the declaration reads an absent body.
	for name, member := range members {
		index, ok := b.jsonField(name)
		if !ok {
			return &bindingError{
				status: http.StatusBadRequest,
				reason: fmt.Sprintf("http: the request body carries the unknown member %q", name),
			}
		}
		if err := json.Unmarshal(member, value.Field(index).Addr().Interface()); err != nil {
			return &bindingError{
				status: http.StatusBadRequest,
				reason: fmt.Sprintf("http: the request body member %q has the wrong shape", name),
			}
		}
	}
	return nil
}

func (b requestBinding) jsonField(name string) (int, bool) {
	if index, ok := b.json[name]; ok {
		return index, true
	}
	folded := strings.ToLower(name)
	for field, index := range b.json {
		if strings.ToLower(field) == folded {
			return index, true
		}
	}
	return 0, false
}

func (b requestBinding) bindForm(c *gin.Context, value reflect.Value) error {
	raw, err := readRequestBody(c)
	if err != nil {
		return err
	}
	if len(raw) == 0 {
		if b.required {
			return requiredBodyError()
		}
		return nil
	}
	values, err := url.ParseQuery(string(raw))
	if err != nil {
		return &bindingError{status: http.StatusBadRequest, reason: "http: the request body is not valid form data"}
	}
	return setFormValues(values, value, b.form)
}

// bindMultipart reads a multipart body into the input's form- and file-tagged fields. The endpoint's body limit is already on the request's body, so the parser's own read ends with a *http.MaxBytesError when the upload is over it.
func (b requestBinding) bindMultipart(c *gin.Context, value reflect.Value) error {
	if err := c.Request.ParseMultipartForm(multipartMemory); err != nil {
		return multipartError(err)
	}
	if c.Request.MultipartForm == nil ||
		len(c.Request.MultipartForm.Value)+len(c.Request.MultipartForm.File) == 0 {
		if b.required {
			return requiredBodyError()
		}
		return nil
	}
	if err := setFormValues(url.Values(c.Request.MultipartForm.Value), value, b.form); err != nil {
		return err
	}
	for name, headers := range c.Request.MultipartForm.File {
		index, ok := b.file[name]
		if !ok {
			return &bindingError{
				status: http.StatusBadRequest,
				reason: fmt.Sprintf("http: the request body carries the unknown part %q", name),
			}
		}
		if len(headers) > 1 {
			return &bindingError{
				status: http.StatusBadRequest,
				reason: fmt.Sprintf("http: the multipart part %q is repeated", name),
			}
		}
		content, err := readFilePart(headers)
		if err != nil {
			return err
		}
		value.Field(index).SetBytes(content)
	}
	return nil
}

// setFormValues stores every form value in its declared field and refuses a
// key the declaration does not carry, and a repeated value for a field that is
// not a slice.
func setFormValues(values url.Values, value reflect.Value, fields map[string]int) error {
	for name, items := range values {
		index, ok := fields[name]
		if !ok {
			return &bindingError{
				status: http.StatusBadRequest,
				reason: fmt.Sprintf("http: the request body carries the unknown key %q", name),
			}
		}
		field := value.Field(index)
		if field.Kind() != reflect.Slice && len(items) > 1 {
			return &bindingError{
				status: http.StatusBadRequest,
				reason: fmt.Sprintf("http: the form field %q is repeated but the field is not a slice", name),
			}
		}
		if err := setValues(field, items); err != nil {
			return &bindingError{
				status: http.StatusBadRequest,
				reason: fmt.Sprintf("http: the form field %q has the wrong shape", name),
			}
		}
	}
	return nil
}

func readFilePart(headers []*multipart.FileHeader) ([]byte, error) {
	if len(headers) == 0 {
		return nil, nil
	}
	file, err := headers[0].Open()
	if err != nil {
		return nil, &bindingError{status: http.StatusBadRequest, reason: "http: a multipart part could not be read"}
	}
	defer func() { _ = file.Close() }()
	content, err := io.ReadAll(file)
	if err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			return nil, &bindingError{status: http.StatusRequestEntityTooLarge, reason: "http: the request body is over the endpoint's limit"}
		}
		return nil, &bindingError{status: http.StatusBadRequest, reason: "http: a multipart part could not be read"}
	}
	return content, nil
}

func multipartError(err error) error {
	var tooLarge *http.MaxBytesError
	if errors.As(err, &tooLarge) {
		return &bindingError{status: http.StatusRequestEntityTooLarge, reason: "http: the request body is over the endpoint's limit"}
	}
	return &bindingError{status: http.StatusBadRequest, reason: "http: the request body is not valid multipart data"}
}

func (b requestBinding) bindRaw(c *gin.Context, value reflect.Value) error {
	raw, err := readRequestBody(c)
	if err != nil {
		return err
	}
	if b.required && len(raw) == 0 {
		return requiredBodyError()
	}
	value.Field(b.raw).SetBytes(raw)
	return nil
}

func readRequestBody(c *gin.Context) ([]byte, error) {
	if c.Request.Body == nil {
		return nil, nil
	}
	raw, err := io.ReadAll(c.Request.Body)
	if err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			return nil, &bindingError{status: http.StatusRequestEntityTooLarge, reason: "http: the request body is over the endpoint's limit"}
		}
		return nil, &bindingError{status: http.StatusBadRequest, reason: "http: the request body could not be read"}
	}
	return raw, nil
}

func writeFailure(c *gin.Context, operationID string, responses []Response, err error) {
	status := http.StatusInternalServerError
	problem := Problem{Status: status}
	matched := Response{}
	declared := false
	var failure Failure
	if errors.As(err, &failure) {
		if response, ok := responseOf(responses, failure.StatusCode()); ok {
			status = failure.StatusCode()
			problem = Problem{Status: status}
			matched, declared = response, true
		}
	}
	var carrier interface{ Problem() Problem }
	if errors.As(err, &carrier) {
		if named := carrier.Problem(); named.Status == status {
			problem = named
		}
	}
	if fault, ok := faultOf(err); ok && declared {
		if reason := checkDynamicHeaders(fault.Headers, matched); reason != "" {
			replyDefect(c, operationID, reason)
			return
		}
		for _, header := range fault.Headers {
			c.Header(header.Name, header.Value)
		}
	}
	Fail(c, problem)
}

const declaredFailuresContextKey = "speed.http.declared-failures"

func declaredResponses(responses []Response) map[int]Response {
	declared := make(map[int]Response, len(responses))
	for _, response := range responses {
		if _, exists := declared[response.Status]; !exists {
			declared[response.Status] = response
		}
	}
	return declared
}
