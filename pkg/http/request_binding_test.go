package http

import (
	"bytes"
	"context"
	"errors"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// formInput carries parameters beside a urlencoded body.
type formInput struct {
	ID   string   `path:"id"`
	Name string   `form:"name"`
	Age  int      `form:"age"`
	Tags []string `form:"tag"`
}

// TestAFormBodyFillsTheFormTaggedFields holds form binding: a urlencoded body
// is read into the form-tagged fields, a repeated key fills a slice, and the
// parameter tags still work.
func TestAFormBodyFillsTheFormTaggedFields(t *testing.T) {
	operation := NewOperation[formInput, struct{}](
		"the.module.form", http.MethodPost, "/items/:id", "", "",
		requestBody("application/x-www-form-urlencoded"),
		[]Response{{Status: http.StatusNoContent, Description: "Accepted"}},
	)
	handler := operation.Handle(func(_ context.Context, in formInput) (struct{}, error) {
		want := formInput{ID: "abc", Name: "alice", Age: 30, Tags: []string{"x", "y"}}
		if in.ID != want.ID || in.Name != want.Name || in.Age != want.Age || strings.Join(in.Tags, ",") != strings.Join(want.Tags, ",") {
			t.Errorf("the handler saw %+v, want %+v", in, want)
		}
		return struct{}{}, nil
	})
	request := httptest.NewRequest(http.MethodPost, "/items/abc",
		strings.NewReader("name=alice&age=30&tag=x&tag=y"))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	recorder := serve(t, http.MethodPost, "/items/:id", handler, request)
	if recorder.Code != http.StatusNoContent {
		t.Errorf("the operation answered %d, want 204", recorder.Code)
	}
}

// TestAnUnknownFormKeyIsRefused holds the form refusal.
func TestAnUnknownFormKeyIsRefused(t *testing.T) {
	operation := NewOperation[formInput, struct{}](
		"the.module.form", http.MethodPost, "/items/:id", "", "",
		requestBody("application/x-www-form-urlencoded"),
		[]Response{{Status: http.StatusNoContent, Description: "Accepted"}},
	)
	handler := operation.Handle(func(context.Context, formInput) (struct{}, error) {
		t.Error("the handler ran with an undeclared form key")
		return struct{}{}, nil
	})
	request := httptest.NewRequest(http.MethodPost, "/items/abc", strings.NewReader("name=alice&smuggled=1"))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	recorder := serve(t, http.MethodPost, "/items/:id", handler, request)
	if recorder.Code != http.StatusBadRequest {
		t.Errorf("an unknown form key answered %d, want 400", recorder.Code)
	}
}

// TestANonFormBodyIsUnsupportedMediaType holds the form media-type refusal.
func TestANonFormBodyIsUnsupportedMediaType(t *testing.T) {
	operation := NewOperation[formInput, struct{}](
		"the.module.form", http.MethodPost, "/items/:id", "", "",
		requestBody("application/x-www-form-urlencoded"),
		[]Response{{Status: http.StatusNoContent, Description: "Accepted"}},
	)
	handler := operation.Handle(func(context.Context, formInput) (struct{}, error) {
		t.Error("the handler ran with a body it could not decode")
		return struct{}{}, nil
	})
	request := httptest.NewRequest(http.MethodPost, "/items/abc", strings.NewReader(`{"name":"alice"}`))
	request.Header.Set("Content-Type", "application/json")

	recorder := serve(t, http.MethodPost, "/items/:id", handler, request)
	if recorder.Code != http.StatusUnsupportedMediaType {
		t.Errorf("a JSON body answered %d, want 415", recorder.Code)
	}
}

// multipartInput carries a form field and a file field beside a parameter.
type multipartInput struct {
	ID   string `query:"id"`
	Name string `form:"name"`
	Data []byte `file:"upload"`
}

// multipartBody builds one multipart request body.
func multipartBody(t *testing.T, build func(*multipart.Writer)) (*bytes.Buffer, string) {
	t.Helper()
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	build(writer)
	if err := writer.Close(); err != nil {
		t.Fatalf("closing the multipart body: %v", err)
	}
	return &body, writer.FormDataContentType()
}

// TestAMultipartBodyFillsTheFormAndFileFields holds multipart binding.
func TestAMultipartBodyFillsTheFormAndFileFields(t *testing.T) {
	operation := NewOperation[multipartInput, struct{}](
		"the.module.multipart", http.MethodPost, "/items", "", "",
		requestBody("multipart/form-data"),
		[]Response{{Status: http.StatusNoContent, Description: "Accepted"}},
	)
	handler := operation.Handle(func(_ context.Context, in multipartInput) (struct{}, error) {
		if in.ID != "abc" || in.Name != "alice" || string(in.Data) != "file-content" {
			t.Errorf("the handler saw %+v, want id=abc name=alice data=file-content", in)
		}
		return struct{}{}, nil
	})
	body, contentType := multipartBody(t, func(writer *multipart.Writer) {
		if err := writer.WriteField("name", "alice"); err != nil {
			t.Fatalf("writing the form field: %v", err)
		}
		part, err := writer.CreateFormFile("upload", "a.bin")
		if err != nil {
			t.Fatalf("creating the file part: %v", err)
		}
		if _, err := part.Write([]byte("file-content")); err != nil {
			t.Fatalf("writing the file part: %v", err)
		}
	})
	request := httptest.NewRequest(http.MethodPost, "/items?id=abc", body)
	request.Header.Set("Content-Type", contentType)

	recorder := serve(t, http.MethodPost, "/items", handler, request)
	if recorder.Code != http.StatusNoContent {
		t.Errorf("the operation answered %d, want 204", recorder.Code)
	}
}

// TestAnUnknownMultipartPartIsRefused holds the multipart refusal for both a
// value part and a file part.
func TestAnUnknownMultipartPartIsRefused(t *testing.T) {
	for _, part := range []string{"value", "file"} {
		t.Run(part, func(t *testing.T) {
			operation := NewOperation[multipartInput, struct{}](
				"the.module.multipart", http.MethodPost, "/items", "", "",
				requestBody("multipart/form-data"),
				[]Response{{Status: http.StatusNoContent, Description: "Accepted"}},
			)
			handler := operation.Handle(func(context.Context, multipartInput) (struct{}, error) {
				t.Error("the handler ran with an undeclared multipart part")
				return struct{}{}, nil
			})
			body, contentType := multipartBody(t, func(writer *multipart.Writer) {
				if part == "value" {
					_ = writer.WriteField("smuggled", "1")
					return
				}
				unknown, err := writer.CreateFormFile("smuggled", "x.bin")
				if err != nil {
					t.Fatalf("creating the unknown part: %v", err)
				}
				_, _ = unknown.Write([]byte("x"))
			})
			request := httptest.NewRequest(http.MethodPost, "/items", body)
			request.Header.Set("Content-Type", contentType)

			recorder := serve(t, http.MethodPost, "/items", handler, request)
			if recorder.Code != http.StatusBadRequest {
				t.Errorf("an unknown %s part answered %d, want 400", part, recorder.Code)
			}
		})
	}
}

// TestANonMultipartBodyIsUnsupportedMediaType holds the multipart media-type
// refusal.
func TestANonMultipartBodyIsUnsupportedMediaType(t *testing.T) {
	operation := NewOperation[multipartInput, struct{}](
		"the.module.multipart", http.MethodPost, "/items", "", "",
		requestBody("multipart/form-data"),
		[]Response{{Status: http.StatusNoContent, Description: "Accepted"}},
	)
	handler := operation.Handle(func(context.Context, multipartInput) (struct{}, error) {
		t.Error("the handler ran with a body it could not decode")
		return struct{}{}, nil
	})
	request := httptest.NewRequest(http.MethodPost, "/items", strings.NewReader("name=alice"))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	recorder := serve(t, http.MethodPost, "/items", handler, request)
	if recorder.Code != http.StatusUnsupportedMediaType {
		t.Errorf("a form body answered %d, want 415", recorder.Code)
	}
}

// rawInput carries one raw body field beside a parameter.
type rawInput struct {
	ID   string `path:"id"`
	Body []byte `raw:"true"`
}

// TestARawBodyFillsTheRawTaggedField holds octet-stream binding.
func TestARawBodyFillsTheRawTaggedField(t *testing.T) {
	operation := NewOperation[rawInput, struct{}](
		"the.module.raw", http.MethodPut, "/blobs/:id", "", "",
		requestBody("application/octet-stream"),
		[]Response{{Status: http.StatusNoContent, Description: "Stored"}},
	)
	handler := operation.Handle(func(_ context.Context, in rawInput) (struct{}, error) {
		if in.ID != "abc" || string(in.Body) != "\x00raw\xff" {
			t.Errorf("the handler saw id=%q body=%q, want abc and the raw bytes", in.ID, in.Body)
		}
		return struct{}{}, nil
	})
	request := httptest.NewRequest(http.MethodPut, "/blobs/abc", strings.NewReader("\x00raw\xff"))
	request.Header.Set("Content-Type", "application/octet-stream")

	recorder := serve(t, http.MethodPut, "/blobs/:id", handler, request)
	if recorder.Code != http.StatusNoContent {
		t.Errorf("the operation answered %d, want 204", recorder.Code)
	}
}

// TestANonOctetStreamBodyIsUnsupportedMediaType holds the raw body's media-type
// refusal.
func TestANonOctetStreamBodyIsUnsupportedMediaType(t *testing.T) {
	operation := NewOperation[rawInput, struct{}](
		"the.module.raw", http.MethodPut, "/blobs/:id", "", "",
		requestBody("application/octet-stream"),
		[]Response{{Status: http.StatusNoContent, Description: "Stored"}},
	)
	handler := operation.Handle(func(context.Context, rawInput) (struct{}, error) {
		t.Error("the handler ran with a body it could not decode")
		return struct{}{}, nil
	})
	request := httptest.NewRequest(http.MethodPut, "/blobs/abc", strings.NewReader("raw"))
	request.Header.Set("Content-Type", "text/plain")

	recorder := serve(t, http.MethodPut, "/blobs/:id", handler, request)
	if recorder.Code != http.StatusUnsupportedMediaType {
		t.Errorf("a text body answered %d, want 415", recorder.Code)
	}
}

// TestARawBodyOverTheLimitIsRefused holds the body-limit classification for a
// body whose length was unknown: the bounded read ends with a 413.
func TestARawBodyOverTheLimitIsRefused(t *testing.T) {
	operation := NewOperation[rawInput, struct{}](
		"the.module.raw", http.MethodPut, "/blobs/:id", "", "",
		requestBody("application/octet-stream"),
		[]Response{{Status: http.StatusNoContent, Description: "Stored"}},
	)
	handler := operation.Handle(func(context.Context, rawInput) (struct{}, error) {
		t.Error("the handler ran with a body over the limit")
		return struct{}{}, nil
	})
	request := httptest.NewRequest(http.MethodPut, "/blobs/abc", strings.NewReader("0123456789"))
	request.Header.Set("Content-Type", "application/octet-stream")
	request.Body = http.MaxBytesReader(httptest.NewRecorder(), request.Body, 4)

	recorder := serve(t, http.MethodPut, "/blobs/:id", handler, request)
	if recorder.Code != http.StatusRequestEntityTooLarge {
		t.Errorf("an over-limit raw body answered %d, want 413", recorder.Code)
	}
}

// TestAMultipartBodyOverTheLimitIsRefused holds the body-limit classification
// for the form parser's read.
func TestAMultipartBodyOverTheLimitIsRefused(t *testing.T) {
	operation := NewOperation[multipartInput, struct{}](
		"the.module.multipart", http.MethodPost, "/items", "", "",
		requestBody("multipart/form-data"),
		[]Response{{Status: http.StatusNoContent, Description: "Accepted"}},
	)
	handler := operation.Handle(func(context.Context, multipartInput) (struct{}, error) {
		t.Error("the handler ran with a body over the limit")
		return struct{}{}, nil
	})
	body, contentType := multipartBody(t, func(writer *multipart.Writer) {
		_ = writer.WriteField("name", "alice")
	})
	request := httptest.NewRequest(http.MethodPost, "/items", body)
	request.Header.Set("Content-Type", contentType)
	request.Body = http.MaxBytesReader(httptest.NewRecorder(), request.Body, 4)

	recorder := serve(t, http.MethodPost, "/items", handler, request)
	if recorder.Code != http.StatusRequestEntityTooLarge {
		t.Errorf("an over-limit multipart body answered %d, want 413", recorder.Code)
	}
}

// noRawInput declares no raw field.
type noRawInput struct {
	Name string `json:"name"`
}

// rawWithJSONInput declares a raw field under a JSON media type.
type rawWithJSONInput struct {
	Name string `json:"name"`
	Body []byte `raw:"true"`
}

// twoRawInput declares two raw fields.
type twoRawInput struct {
	First  []byte `raw:"true"`
	Second []byte `raw:"true"`
}

// textFileInput declares a file field that is not []byte.
type textFileInput struct {
	Upload string `file:"upload"`
}

// jsonBytesFileInput declares a file field beside a JSON body member, so the
// only declaration defect is the file tag itself.
type jsonBytesFileInput struct {
	Name   string `json:"name"`
	Upload []byte `file:"upload"`
}

// formBytesFileInput declares a file field beside a form member, so the only
// declaration defect is the file tag itself.
type formBytesFileInput struct {
	Name   string `form:"name"`
	Upload []byte `file:"upload"`
}

// jsonWithFormInput declares a form field beside a JSON member.
type jsonWithFormInput struct {
	Name string `json:"name"`
	Mode string `form:"mode"`
}

// formWithJSONInput declares a JSON field beside a form member.
type formWithJSONInput struct {
	Name string `form:"name"`
	Mode string `json:"mode"`
}

// rawWithFormInput declares a form field beside the raw field.
type rawWithFormInput struct {
	Body []byte `raw:"true"`
	Mode string `form:"mode"`
}

// TestAnUnbindableRequestDeclarationPanics holds the build-time checks: a
// declaration no request could satisfy panics when the handler is built.
func TestAnUnbindableRequestDeclarationPanics(t *testing.T) {
	cases := []struct {
		name    string
		build   func()
		message string
	}{
		{
			name: "octet-stream without a raw field",
			build: func() {
				NewOperation[noRawInput, struct{}]("the.module.raw", http.MethodPut, "/blobs", "", "",
					requestBody("application/octet-stream"), []Response{{Status: http.StatusNoContent}}).Handle(
					func(context.Context, noRawInput) (struct{}, error) { return struct{}{}, nil })
			},
			message: "requires exactly one raw-tagged field",
		},
		{
			name: "raw field under another media type",
			build: func() {
				NewOperation[rawWithJSONInput, struct{}]("the.module.raw", http.MethodPut, "/blobs", "", "",
					requestBody("application/json"), []Response{{Status: http.StatusNoContent}}).Handle(
					func(context.Context, rawWithJSONInput) (struct{}, error) { return struct{}{}, nil })
			},
			message: "a raw-tagged field requires the application/octet-stream media type",
		},
		{
			name: "two raw fields",
			build: func() {
				NewOperation[twoRawInput, struct{}]("the.module.raw", http.MethodPut, "/blobs", "", "",
					requestBody("application/octet-stream"), []Response{{Status: http.StatusNoContent}}).Handle(
					func(context.Context, twoRawInput) (struct{}, error) { return struct{}{}, nil })
			},
			message: "more than one raw-tagged field",
		},
		{
			name: "a file field that is not bytes",
			build: func() {
				NewOperation[textFileInput, struct{}]("the.module.multipart", http.MethodPost, "/items", "", "",
					requestBody("multipart/form-data"), []Response{{Status: http.StatusNoContent}}).Handle(
					func(context.Context, textFileInput) (struct{}, error) { return struct{}{}, nil })
			},
			message: "must be []byte",
		},
		{
			name: "a file field under JSON",
			build: func() {
				NewOperation[jsonBytesFileInput, struct{}]("the.module.file", http.MethodPost, "/items", "", "",
					requestBody("application/json"), []Response{{Status: http.StatusNoContent}}).Handle(
					func(context.Context, jsonBytesFileInput) (struct{}, error) { return struct{}{}, nil })
			},
			message: "a file-tagged field requires the multipart/form-data media type",
		},
		{
			name: "a file field under form data",
			build: func() {
				NewOperation[formBytesFileInput, struct{}]("the.module.file", http.MethodPost, "/items", "", "",
					requestBody("application/x-www-form-urlencoded"), []Response{{Status: http.StatusNoContent}}).Handle(
					func(context.Context, formBytesFileInput) (struct{}, error) { return struct{}{}, nil })
			},
			message: "a file-tagged field requires the multipart/form-data media type",
		},
		{
			name: "a form field under JSON",
			build: func() {
				NewOperation[jsonWithFormInput, struct{}]("the.module.body", http.MethodPost, "/items", "", "",
					requestBody("application/json"), []Response{{Status: http.StatusNoContent}}).Handle(
					func(context.Context, jsonWithFormInput) (struct{}, error) { return struct{}{}, nil })
			},
			message: "a form-tagged field requires the application/x-www-form-urlencoded or multipart/form-data media type",
		},
		{
			name: "a JSON field under form data",
			build: func() {
				NewOperation[formWithJSONInput, struct{}]("the.module.body", http.MethodPost, "/items", "", "",
					requestBody("application/x-www-form-urlencoded"), []Response{{Status: http.StatusNoContent}}).Handle(
					func(context.Context, formWithJSONInput) (struct{}, error) { return struct{}{}, nil })
			},
			message: "a json-tagged field requires the application/json media type",
		},
		{
			name: "a JSON field under multipart",
			build: func() {
				NewOperation[formWithJSONInput, struct{}]("the.module.body", http.MethodPost, "/items", "", "",
					requestBody("multipart/form-data"), []Response{{Status: http.StatusNoContent}}).Handle(
					func(context.Context, formWithJSONInput) (struct{}, error) { return struct{}{}, nil })
			},
			message: "a json-tagged field requires the application/json media type",
		},
		{
			name: "a JSON field under octet-stream",
			build: func() {
				NewOperation[rawWithJSONInput, struct{}]("the.module.body", http.MethodPost, "/items", "", "",
					requestBody("application/octet-stream"), []Response{{Status: http.StatusNoContent}}).Handle(
					func(context.Context, rawWithJSONInput) (struct{}, error) { return struct{}{}, nil })
			},
			message: "a json-tagged field requires the application/json media type",
		},
		{
			name: "a form field under octet-stream",
			build: func() {
				NewOperation[rawWithFormInput, struct{}]("the.module.body", http.MethodPost, "/items", "", "",
					requestBody("application/octet-stream"), []Response{{Status: http.StatusNoContent}}).Handle(
					func(context.Context, rawWithFormInput) (struct{}, error) { return struct{}{}, nil })
			},
			message: "a form-tagged field requires the application/x-www-form-urlencoded or multipart/form-data media type",
		},
		{
			name: "an unsupported media type",
			build: func() {
				NewOperation[noRawInput, struct{}]("the.module.body", http.MethodPost, "/items", "", "",
					requestBody("text/plain"), []Response{{Status: http.StatusNoContent}}).Handle(
					func(context.Context, noRawInput) (struct{}, error) { return struct{}{}, nil })
			},
			message: "unsupported request media type",
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			defer func() {
				thrown := recover()
				if thrown == nil {
					t.Fatal("building the handler did not panic on an unbindable declaration")
				}
				if message := errString(thrown); !strings.Contains(message, testCase.message) {
					t.Errorf("the panic reads %q, want it to contain %q", message, testCase.message)
				}
			}()
			testCase.build()
		})
	}
}

// errString renders a recovered value.
func errString(value any) string {
	if err, ok := value.(error); ok {
		return err.Error()
	}
	if text, ok := value.(string); ok {
		return text
	}
	return ""
}

// validatedInput refuses an empty name through its own Validate.
type validatedInput struct {
	Name string `json:"name"`
}

func (v *validatedInput) Validate() error {
	if v.Name == "" {
		return errors.New("the name is required")
	}
	return nil
}

// TestAFailedValidateIsAnsweredUnprocessable holds the input's own check: a
// value it refuses is a 422, and the validator's text is the problem's detail.
func TestAFailedValidateIsAnsweredUnprocessable(t *testing.T) {
	operation := NewOperation[validatedInput, struct{}](
		"the.module.validated", http.MethodPost, "/items", "", "",
		requestBody("application/json"),
		[]Response{{Status: http.StatusNoContent, Description: "Accepted"}},
	)
	handler := operation.Handle(func(context.Context, validatedInput) (struct{}, error) {
		t.Error("the handler ran with a value its own Validate refused")
		return struct{}{}, nil
	})
	request := httptest.NewRequest(http.MethodPost, "/items", strings.NewReader(`{"name":""}`))
	request.Header.Set("Content-Type", "application/json")

	recorder := serve(t, http.MethodPost, "/items", handler, request)
	if recorder.Code != http.StatusUnprocessableEntity {
		t.Fatalf("a refused value answered %d, want 422", recorder.Code)
	}
	if !strings.Contains(recorder.Body.String(), "the name is required") {
		t.Errorf("the problem does not carry the validator's text: %s", recorder.Body.String())
	}
}

// TestAnAcceptedValueReachesTheHandler holds the other side of Validate.
func TestAnAcceptedValueReachesTheHandler(t *testing.T) {
	operation := NewOperation[validatedInput, struct{}](
		"the.module.validated", http.MethodPost, "/items", "", "",
		requestBody("application/json"),
		[]Response{{Status: http.StatusNoContent, Description: "Accepted"}},
	)
	ran := false
	handler := operation.Handle(func(_ context.Context, in validatedInput) (struct{}, error) {
		ran = true
		if in.Name != "alice" {
			t.Errorf("the handler saw %q, want alice", in.Name)
		}
		return struct{}{}, nil
	})
	request := httptest.NewRequest(http.MethodPost, "/items", strings.NewReader(`{"name":"alice"}`))
	request.Header.Set("Content-Type", "application/json")

	recorder := serve(t, http.MethodPost, "/items", handler, request)
	if recorder.Code != http.StatusNoContent {
		t.Errorf("the operation answered %d, want 204", recorder.Code)
	}
	if !ran {
		t.Error("the handler did not run for an accepted value")
	}
}

// queryValuesInput carries an integer and a string slice from the query.
type queryValuesInput struct {
	Limit int      `query:"limit"`
	Tags  []string `query:"tag"`
}

// TestAnEmptyQueryValueLeavesANonStringParameterUnset holds the empty value:
// "?limit=" is how a client leaves an integer unset, so the handler sees zero,
// while "?tag=" is a real empty string value in the slice.
func TestAnEmptyQueryValueLeavesANonStringParameterUnset(t *testing.T) {
	operation := NewOperation[queryValuesInput, struct{}](
		"the.module.read", http.MethodGet, "/items", "", "", nil,
		[]Response{{Status: http.StatusNoContent, Description: "No content"}},
	)
	handler := operation.Handle(func(_ context.Context, in queryValuesInput) (struct{}, error) {
		if in.Limit != 0 {
			t.Errorf("the handler saw limit=%d, want the zero value", in.Limit)
		}
		if len(in.Tags) != 1 || in.Tags[0] != "" {
			t.Errorf("the handler saw %v, want the empty string as one value", in.Tags)
		}
		return struct{}{}, nil
	})
	recorder := serve(t, http.MethodGet, "/items", handler,
		httptest.NewRequest(http.MethodGet, "/items?limit=&tag=", nil))

	if recorder.Code != http.StatusNoContent {
		t.Errorf("an empty query value answered %d, want 204", recorder.Code)
	}
}

// TestARepeatedQueryValueForAScalarFieldIsRefused holds the parameter check: a
// scalar field cannot hold both values, and dropping the second one silently
// would answer from an input the caller did not send.
func TestARepeatedQueryValueForAScalarFieldIsRefused(t *testing.T) {
	operation := NewOperation[parameterOnlyInput, struct{}](
		"the.module.read", http.MethodGet, "/items/:id", "", "", nil,
		[]Response{{Status: http.StatusNoContent, Description: "No content"}},
	)
	handler := operation.Handle(func(context.Context, parameterOnlyInput) (struct{}, error) {
		t.Error("the handler ran with a repeated parameter")
		return struct{}{}, nil
	})
	recorder := serve(t, http.MethodGet, "/items/:id", handler,
		httptest.NewRequest(http.MethodGet, "/items/abc?limit=1&limit=2", nil))

	if recorder.Code != http.StatusBadRequest {
		t.Errorf("a repeated scalar parameter answered %d, want 400", recorder.Code)
	}
}

// TestARepeatedFormValueForAScalarFieldIsRefused holds the form half of the
// same check.
func TestARepeatedFormValueForAScalarFieldIsRefused(t *testing.T) {
	operation := NewOperation[formInput, struct{}](
		"the.module.form", http.MethodPost, "/items/:id", "", "",
		requestBody("application/x-www-form-urlencoded"),
		[]Response{{Status: http.StatusNoContent, Description: "Accepted"}},
	)
	handler := operation.Handle(func(context.Context, formInput) (struct{}, error) {
		t.Error("the handler ran with a repeated form value")
		return struct{}{}, nil
	})
	request := httptest.NewRequest(http.MethodPost, "/items/abc",
		strings.NewReader("name=alice&name=bob"))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	recorder := serve(t, http.MethodPost, "/items/:id", handler, request)
	if recorder.Code != http.StatusBadRequest {
		t.Errorf("a repeated scalar form value answered %d, want 400", recorder.Code)
	}
}

// TestARepeatedMultipartFilePartIsRefused holds the file half: a file-tagged
// field holds one file, so a repeated part name is refused rather than read
// for its first part.
func TestARepeatedMultipartFilePartIsRefused(t *testing.T) {
	operation := NewOperation[multipartInput, struct{}](
		"the.module.multipart", http.MethodPost, "/items", "", "",
		requestBody("multipart/form-data"),
		[]Response{{Status: http.StatusNoContent, Description: "Accepted"}},
	)
	handler := operation.Handle(func(context.Context, multipartInput) (struct{}, error) {
		t.Error("the handler ran with a repeated file part")
		return struct{}{}, nil
	})
	body, contentType := multipartBody(t, func(writer *multipart.Writer) {
		for _, content := range []string{"first", "second"} {
			part, err := writer.CreateFormFile("upload", content+".bin")
			if err != nil {
				t.Fatalf("creating the file part: %v", err)
			}
			if _, err := part.Write([]byte(content)); err != nil {
				t.Fatalf("writing the file part: %v", err)
			}
		}
	})
	request := httptest.NewRequest(http.MethodPost, "/items", body)
	request.Header.Set("Content-Type", contentType)

	recorder := serve(t, http.MethodPost, "/items", handler, request)
	if recorder.Code != http.StatusBadRequest {
		t.Errorf("a repeated file part answered %d, want 400", recorder.Code)
	}
}

// TestANullJSONBodyIsRefused holds the JSON object check: null decodes into a
// nil map without an error, but the body is declared as an object.
func TestANullJSONBodyIsRefused(t *testing.T) {
	operation := NewOperation[readInput, struct{}](
		"the.module.write", http.MethodPost, "/items/:id", "", "",
		requestBody("application/json"),
		[]Response{{Status: http.StatusNoContent, Description: "No content"}},
	)
	handler := operation.Handle(func(context.Context, readInput) (struct{}, error) {
		t.Error("the handler ran with a null body")
		return struct{}{}, nil
	})
	request := httptest.NewRequest(http.MethodPost, "/items/abc", strings.NewReader("null"))
	request.Header.Set("Content-Type", "application/json")

	recorder := serve(t, http.MethodPost, "/items/:id", handler, request)
	if recorder.Code != http.StatusBadRequest {
		t.Errorf("a null JSON body answered %d, want 400", recorder.Code)
	}
}
