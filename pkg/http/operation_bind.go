package http

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"reflect"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
)

// Failure is an error that names the status a handler wants answered. A
// handler returns one to answer a refusal it can produce; the entry point
// writes that status as a problem document.
type Failure interface {
	error
	// StatusCode is the response status the failure belongs to.
	StatusCode() int
}

// bind builds the entry point's handler for one declared operation: it binds
// the declared input from the request, calls the handler and writes the
// declared success response. A handler never sees the request, so a parameter
// the declaration does not carry cannot be read, and a body field it does not
// describe cannot be decoded.
func bind[In, Out any](
	handle func(context.Context, In) (Out, error),
	input reflect.Type,
	requestMediaType string,
	responses []Response,
) gin.HandlerFunc {
	status := successStatus(responses)
	if status == 0 {
		panic("http: an operation must declare exactly one 2xx response")
	}
	success, ok := responseOf(responses, status)
	if !ok {
		panic("http: the declared success response is missing from the response set")
	}
	bodyFields := jsonFields(input)
	if requestMediaType != "" && len(bodyFields) == 0 {
		panic("http: a request body media type is declared but the input carries no json-tagged field")
	}
	return func(c *gin.Context) {
		c.Set(declaredFailuresContextKey, declaredStatuses(responses))
		var in In
		if input != nil {
			if err := bindParameters(c, reflect.ValueOf(&in).Elem(), input); err != nil {
				Fail(c, Problem{Status: statusOf(err)})
				return
			}
			if requestMediaType != "" {
				if err := decodeRequestBody(c, reflect.ValueOf(&in).Elem(), bodyFields); err != nil {
					Fail(c, Problem{Status: statusOf(err)})
					return
				}
			}
		}
		out, err := handle(c.Request.Context(), in)
		if err != nil {
			writeFailure(c, responses, err)
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

// responseOf returns the declared response with the given status.
func responseOf(responses []Response, status int) (Response, bool) {
	for _, response := range responses {
		if response.Status == status {
			return response, true
		}
	}
	return Response{}, false
}

// statusOf is the status a binding failure is answered with; a binding failure
// carries its status through bindingError, and anything else is a bad request.
func statusOf(err error) int {
	var failure Failure
	if errors.As(err, &failure) {
		return failure.StatusCode()
	}
	return http.StatusBadRequest
}

// bindingError is one input-binding refusal: the status it is answered with and
// what the caller should fix.
type bindingError struct {
	status int
	reason string
}

func (e *bindingError) Error() string   { return e.reason }
func (e *bindingError) StatusCode() int { return e.status }

// bindParameters fills the parameter-tagged fields of the input from the
// request. A field without one of the parameter tags is not read here: it is
// either part of the body or not request input at all.
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
			raw, present = c.GetQuery(name)
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
		if target.Kind() == reflect.Slice && source == "query" {
			target.Set(reflect.ValueOf(c.QueryArray(name)))
			continue
		}
		if err := setScalar(target, raw); err != nil {
			return &bindingError{
				status: http.StatusBadRequest,
				reason: fmt.Sprintf("http: the parameter %q is not a valid %s", name, target.Kind()),
			}
		}
	}
	return nil
}

// parameterOf returns the parameter name and source a field declares, or "".
func parameterOf(field reflect.StructField) (string, string) {
	for _, source := range []string{"path", "query", "header", "cookie"} {
		if name := field.Tag.Get(source); name != "" {
			return name, source
		}
	}
	return "", ""
}

// setScalar stores one request value in a field of a supported kind.
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

// jsonFields maps each json-tagged field name to its field index.
func jsonFields(input reflect.Type) map[string]int {
	fields := map[string]int{}
	if input == nil {
		return fields
	}
	for i := 0; i < input.NumField(); i++ {
		field := input.Field(i)
		if field.PkgPath != "" {
			continue
		}
		name := strings.Split(field.Tag.Get("json"), ",")[0]
		if name == "" || name == "-" {
			continue
		}
		fields[name] = i
	}
	return fields
}

// decodeRequestBody reads the request body into the input's json-tagged fields.
// Unknown members are refused, so a body carrying a field the declaration does
// not describe is a 400 rather than a silently dropped value.
func decodeRequestBody(c *gin.Context, value reflect.Value, fields map[string]int) error {
	if mediaType := c.GetHeader("Content-Type"); !strings.HasPrefix(mediaType, "application/json") {
		return &bindingError{
			status: http.StatusUnsupportedMediaType,
			reason: "http: the request body must be application/json",
		}
	}
	raw, err := io.ReadAll(c.Request.Body)
	if err != nil {
		return &bindingError{status: http.StatusBadRequest, reason: "http: the request body could not be read"}
	}
	var members map[string]json.RawMessage
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &members); err != nil {
			return &bindingError{status: http.StatusBadRequest, reason: "http: the request body is not a JSON object"}
		}
	}
	for name, member := range members {
		index, ok := fields[name]
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

// writeFailure answers a handler's error: the problem the failure names, or a
// bare 500, and never a status the operation did not declare.
func writeFailure(c *gin.Context, responses []Response, err error) {
	status := http.StatusInternalServerError
	problem := Problem{Status: status}
	var failure Failure
	if errors.As(err, &failure) {
		if _, declared := responseOf(responses, failure.StatusCode()); declared {
			status = failure.StatusCode()
			problem = Problem{Status: status}
		}
	}
	var carrier interface{ Problem() Problem }
	if errors.As(err, &carrier) {
		if named := carrier.Problem(); named.Status == status {
			problem = named
		}
	}
	Fail(c, problem)
}

// declaredFailuresContextKey is where the entry point leaves the statuses an
// operation declared, so the recovery layer can tell a refusal a handler meant
// from a defect.
const declaredFailuresContextKey = "speed.http.declared-failures"

// declaredStatuses is the set of statuses an operation's answers declare.
func declaredStatuses(responses []Response) map[int]struct{} {
	statuses := make(map[int]struct{}, len(responses))
	for _, response := range responses {
		statuses[response.Status] = struct{}{}
	}
	return statuses
}
