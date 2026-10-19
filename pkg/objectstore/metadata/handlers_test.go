package metadata

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/vislake/speed/pkg/core"
)

// handlerEngine mounts this module's own route set on a gin engine. Every
// request carries the tenant context the assembly was entered under, which is
// what the tenant filter reads.
func handlerEngine(t *testing.T, p *product, ctx context.Context) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	engine.Use(func(c *gin.Context) { c.Request = c.Request.WithContext(ctx) })
	for _, r := range p.routes() {
		engine.Handle(r.method, metadataSpec.ResolvePath(r.path), r.handler)
	}
	return engine
}

// serve sends one request through a mounted engine.
func serve(engine *gin.Engine, method, path, contentType string, body []byte) *httptest.ResponseRecorder {
	request := httptest.NewRequest(method, path, bytes.NewReader(body))
	if contentType != "" {
		request.Header.Set("Content-Type", contentType)
	}
	recorder := httptest.NewRecorder()
	engine.ServeHTTP(recorder, request)
	return recorder
}

// objectOf decodes one object envelope from a response.
func objectOf(t *testing.T, recorder *httptest.ResponseRecorder) ObjectstoreObject {
	t.Helper()
	var envelope struct {
		Object ObjectstoreObject `json:"object"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("decoding the object response %q: %v", recorder.Body.String(), err)
	}
	return envelope.Object
}

// problemType reads a problem document's type, or "" when the body is not one.
func problemType(t *testing.T, recorder *httptest.ResponseRecorder) string {
	t.Helper()
	var body struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		return ""
	}
	return body.Type
}

// TestTheMigratedRoutesServeTheDeclaredSurface walks the whole route set through
// the entry point's own binding and writing: the typed handlers receive the
// declared input, the entry point writes the declared success answers, and a
// failure is the declared problem document.
func TestTheMigratedRoutesServeTheDeclaredSurface(t *testing.T) {
	forEachEngine(t, func(t *testing.T, e engine) {
		store := newFakeStore()
		h := e.on(t, harness{store: store, withRouter: true})
		h.drive(t, func(_ context.Context, reg *core.Registry) error {
			p := productOfAssembly(t, reg)
			engine := handlerEngine(t, p, tenantCtx(t, reg, "acme"))

			const content = "hello objects"

			created := serve(engine, http.MethodPost, "/api/v1/objects", "application/json",
				[]byte(`{"name":"greeting","size":13,"content_type":"text/plain"}`))
			if created.Code != http.StatusCreated {
				t.Fatalf("creating an object answered %d, want 201: %s", created.Code, created.Body.String())
			}
			declared := objectOf(t, created)
			if declared.State != "pending" || declared.ID == "" {
				t.Fatalf("the declared object reads %+v, want a pending object with an id", declared)
			}

			uploaded := serve(engine, http.MethodPut,
				"/api/v1/objects/"+declared.ID+"/content", "application/octet-stream", []byte(content))
			if uploaded.Code != http.StatusNoContent {
				t.Fatalf("uploading answered %d, want 204: %s", uploaded.Code, uploaded.Body.String())
			}

			completed := serve(engine, http.MethodPost,
				"/api/v1/objects/"+declared.ID+"/complete", "", nil)
			if completed.Code != http.StatusOK {
				t.Fatalf("completing answered %d, want 200: %s", completed.Code, completed.Body.String())
			}
			if state := objectOf(t, completed).State; state != "completed" {
				t.Errorf("the completed object is %q, want completed", state)
			}

			read := serve(engine, http.MethodGet, "/api/v1/objects/"+declared.ID, "", nil)
			if read.Code != http.StatusOK {
				t.Fatalf("reading answered %d, want 200: %s", read.Code, read.Body.String())
			}

			bytesRead := serve(engine, http.MethodGet, "/api/v1/objects/"+declared.ID+"/content", "", nil)
			if bytesRead.Code != http.StatusOK {
				t.Fatalf("reading the bytes answered %d, want 200: %s", bytesRead.Code, bytesRead.Body.String())
			}
			if got := bytesRead.Body.String(); got != content {
				t.Errorf("the bytes read %q, want %q", got, content)
			}
			if mediaType := bytesRead.Header().Get("Content-Type"); mediaType != "text/plain" {
				t.Errorf("the bytes carry the media type %q, want the object's recorded %q", mediaType, "text/plain")
			}

			listed := serve(engine, http.MethodGet, "/api/v1/objects", "", nil)
			if listed.Code != http.StatusOK {
				t.Fatalf("listing answered %d, want 200: %s", listed.Code, listed.Body.String())
			}
			var page struct {
				Objects    []ObjectstoreObject `json:"objects"`
				NextCursor string              `json:"next_cursor"`
			}
			if err := json.Unmarshal(listed.Body.Bytes(), &page); err != nil {
				t.Fatalf("decoding the page %q: %v", listed.Body.String(), err)
			}
			if len(page.Objects) != 1 || page.Objects[0].ID != declared.ID || page.NextCursor != "" {
				t.Errorf("the page reads %+v, want the one object and no next cursor", page)
			}

			deleted := serve(engine, http.MethodDelete, "/api/v1/objects/"+declared.ID, "", nil)
			if deleted.Code != http.StatusNoContent {
				t.Fatalf("deleting answered %d, want 204: %s", deleted.Code, deleted.Body.String())
			}

			gone := serve(engine, http.MethodGet, "/api/v1/objects/"+declared.ID, "", nil)
			if gone.Code != http.StatusNotFound {
				t.Fatalf("reading a deleted object answered %d, want 404: %s", gone.Code, gone.Body.String())
			}
			if got := problemType(t, gone); got != typePrefix+notFoundCode {
				t.Errorf("the refusal reads the problem type %q, want %q", got, typePrefix+notFoundCode)
			}

			return nil
		})
	})
}

// TestTheUploadChecksTheDeclaredContentLength holds the upload's rule: the
// request's Content-Length is what the declaration is compared with, and a
// request that names no length is refused 400.
func TestTheUploadChecksTheDeclaredContentLength(t *testing.T) {
	forEachEngine(t, func(t *testing.T, e engine) {
		h := e.on(t, harness{store: newFakeStore(), withRouter: true})
		h.drive(t, func(_ context.Context, reg *core.Registry) error {
			p := productOfAssembly(t, reg)
			engine := handlerEngine(t, p, tenantCtx(t, reg, "acme"))

			const content = "hello objects"

			declared := objectOf(t, serve(engine, http.MethodPost, "/api/v1/objects", "application/json",
				[]byte(`{"size":13,"content_type":"text/plain"}`)))
			if declared.ID == "" {
				t.Fatalf("the declaration answered no object")
			}
			path := "/api/v1/objects/" + declared.ID + "/content"

			// A request that names no length cannot be checked against the
			// declaration. The reader's anonymous type is what makes the test
			// request carry an unknown Content-Length.
			unmeasured := httptest.NewRequest(http.MethodPut, path,
				struct{ io.Reader }{strings.NewReader(content)})
			unmeasured.Header.Set("Content-Type", "application/octet-stream")
			refused := httptest.NewRecorder()
			engine.ServeHTTP(refused, unmeasured)
			if refused.Code != http.StatusBadRequest {
				t.Errorf("an upload with no Content-Length answered %d, want 400: %s",
					refused.Code, refused.Body.String())
			}
			if got := problemType(t, refused); got != typePrefix+badRequestCode {
				t.Errorf("the refusal reads the problem type %q, want %q", got, typePrefix+badRequestCode)
			}

			// The count compared with the declaration is Content-Length: a
			// request announcing one byte more than it carries is refused even
			// though the bytes it bound match the declaration exactly.
			overstated := httptest.NewRequest(http.MethodPut, path, bytes.NewReader([]byte(content)))
			overstated.ContentLength = int64(len(content)) + 1
			overstated.Header.Set("Content-Type", "application/octet-stream")
			refused = httptest.NewRecorder()
			engine.ServeHTTP(refused, overstated)
			if refused.Code != http.StatusBadRequest {
				t.Errorf("an upload whose Content-Length differs from the declaration answered %d, want 400: %s",
					refused.Code, refused.Body.String())
			}

			return nil
		})
	})
}

// TestTheContentTypeFallsBackWhenNoMIMEIsRecorded holds that the byte read
// answers application/octet-stream when the object records no mime. An upload
// always records one, so the row is planted.
func TestTheContentTypeFallsBackWhenNoMIMEIsRecorded(t *testing.T) {
	forEachEngine(t, func(t *testing.T, e engine) {
		h := e.on(t, harness{store: newFakeStore(), withRouter: true})
		h.drive(t, func(_ context.Context, reg *core.Registry) error {
			p := productOfAssembly(t, reg)
			engine := handlerEngine(t, p, tenantCtx(t, reg, "acme"))

			declared := objectOf(t, serve(engine, http.MethodPost, "/api/v1/objects", "application/json",
				[]byte(`{"size":5,"content_type":"text/plain"}`)))
			path := "/api/v1/objects/" + declared.ID

			if uploaded := serve(engine, http.MethodPut, path+"/content",
				"application/octet-stream", []byte("hello")); uploaded.Code != http.StatusNoContent {
				t.Fatalf("uploading answered %d, want 204: %s", uploaded.Code, uploaded.Body.String())
			}
			if completed := serve(engine, http.MethodPost, path+"/complete", "", nil); completed.Code != http.StatusOK {
				t.Fatalf("completing answered %d, want 200: %s", completed.Code, completed.Body.String())
			}
			if err := dbHandle(t, reg).Exec(
				"UPDATE objects SET mime = '' WHERE tenant_id = ? AND id = ?", "acme", declared.ID).Error; err != nil {
				t.Fatalf("clearing the recorded mime: %v", err)
			}

			read := serve(engine, http.MethodGet, path+"/content", "", nil)
			if read.Code != http.StatusOK {
				t.Fatalf("reading the bytes answered %d, want 200: %s", read.Code, read.Body.String())
			}
			if mediaType := read.Header().Get("Content-Type"); mediaType != fallbackContentType {
				t.Errorf("the bytes carry the media type %q, want the fallback %q", mediaType, fallbackContentType)
			}
			return nil
		})
	})
}

// TestTheBindingRefusalsUseTheDeclaredStatuses holds the entry point's own
// refusals: a JSON body member the declaration does not carry is a 400, and a
// body whose content type is not the declared one is a 415.
func TestTheBindingRefusalsUseTheDeclaredStatuses(t *testing.T) {
	forEachEngine(t, func(t *testing.T, e engine) {
		h := e.on(t, harness{store: newFakeStore(), withRouter: true})
		h.drive(t, func(_ context.Context, reg *core.Registry) error {
			p := productOfAssembly(t, reg)
			engine := handlerEngine(t, p, tenantCtx(t, reg, "acme"))

			unknown := serve(engine, http.MethodPost, "/api/v1/objects", "application/json",
				[]byte(`{"size":1,"content_type":"text/plain","bogus":true}`))
			if unknown.Code != http.StatusBadRequest {
				t.Errorf("a body with an undeclared member answered %d, want 400", unknown.Code)
			}

			wrongType := serve(engine, http.MethodPut, "/api/v1/objects/unknown/content", "", []byte("x"))
			if wrongType.Code != http.StatusUnsupportedMediaType {
				t.Errorf("a raw body with no content type answered %d, want 415", wrongType.Code)
			}

			return nil
		})
	})
}
