package metadata

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"mime"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/vislake/speed/pkg/core"
	speedhttp "github.com/vislake/speed/pkg/http"
	"github.com/vislake/speed/pkg/objectstore"
	"github.com/vislake/speed/pkg/tenant"
)

// mountedRoute is one (method, path) a registration recorded.
type mountedRoute struct{ method, path string }

// recordingEndpoint records the routes registered on it.
type recordingEndpoint struct {
	mu     sync.Mutex
	routes []mountedRoute
}

func (e *recordingEndpoint) Route(method, path string, _ ...gin.HandlerFunc) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.routes = append(e.routes, mountedRoute{method: method, path: path})
}

func (e *recordingEndpoint) Use(speedhttp.Middleware) {}

func (e *recordingEndpoint) Accepting() bool { return true }

func (e *recordingEndpoint) registered() []mountedRoute {
	e.mu.Lock()
	defer e.mu.Unlock()
	return append([]mountedRoute(nil), e.routes...)
}

// recordingRouter is a stand-in http.Router over a fixed set of endpoints.
type recordingRouter struct {
	mu        sync.Mutex
	endpoints map[string]*recordingEndpoint
	order     []string
}

func newRecordingRouter(names ...string) *recordingRouter {
	r := &recordingRouter{endpoints: make(map[string]*recordingEndpoint, len(names))}
	for _, name := range names {
		r.endpoints[name] = &recordingEndpoint{}
		r.order = append(r.order, name)
	}
	return r
}

func (r *recordingRouter) Endpoint(name string) (speedhttp.Endpoint, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	endpoint, ok := r.endpoints[name]
	if !ok {
		return nil, fmt.Errorf("%w: %q", speedhttp.ErrUnknownEndpoint, name)
	}
	return endpoint, nil
}

func (r *recordingRouter) Endpoints() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.order...)
}

func (r *recordingRouter) endpoint(name string) *recordingEndpoint {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.endpoints[name]
}

// contentEngine mounts this module's content routes on a gin engine.
func contentEngine(p *product) *gin.Engine {
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	for _, r := range p.contentRoutes() {
		engine.Handle(r.method, r.path, r.handler)
	}
	return engine
}

// serveContent sends one request through a content engine.
func serveContent(engine *gin.Engine, method, target string, body io.Reader) *httptest.ResponseRecorder {
	return serveContentWith(engine, method, target, body, nil)
}

// serveContentWith sends one request and lets the case adjust it first.
func serveContentWith(engine *gin.Engine, method, target string, body io.Reader, edit func(*http.Request)) *httptest.ResponseRecorder {
	request := httptest.NewRequest(method, target, body)
	if edit != nil {
		edit(request)
	}
	recorder := httptest.NewRecorder()
	engine.ServeHTTP(recorder, request)
	return recorder
}

// contentToken mints one address and answers its token.
func contentToken(t *testing.T, objects objectstore.Objects, ctx context.Context, id string, access objectstore.URLAccess) string {
	t.Helper()
	return tokenOf(t, mintURL(t, objects, ctx, id, access, 0).URL)
}

// targetFor builds the path a token is used through.
func targetFor(token string) string {
	return contentPath + "?" + contentTokenParam + "=" + token
}

// TestTheContentReadServesTheObjectBytes holds the successful read: the bytes,
// the recorded media type, the RFC 6266 disposition, the safety headers, a HEAD
// with no body, and a range answered as 206.
func TestTheContentReadServesTheObjectBytes(t *testing.T) {
	forEachEngine(t, func(t *testing.T, e engine) {
		store := newFakeStore()
		h := e.on(t, harness{store: store, router: newRecordingRouter("public")})
		h.drive(t, func(ctx context.Context, reg *core.Registry) error {
			objects := objectsOf(t, reg)
			tctx := tenantCtx(t, reg, "acme")
			body := "hello bytes"
			const name = "résumé.pdf"
			obj, err := objects.Create(tctx, objectstore.Declaration{
				Name: name, Size: int64(len(body)), ContentType: "text/plain",
			})
			if err != nil {
				t.Fatalf("declaring: %v", err)
			}
			if upErr := objects.Upload(tctx, obj.ID, strings.NewReader(body), int64(len(body))); upErr != nil {
				t.Fatalf("uploading: %v", upErr)
			}
			if _, cErr := objects.Complete(tctx, obj.ID); cErr != nil {
				t.Fatalf("completing: %v", cErr)
			}
			token := contentToken(t, objects, tctx, obj.ID, objectstore.URLAccessRead)
			engine := contentEngine(productOfAssembly(t, reg))

			read := serveContent(engine, http.MethodGet, targetFor(token), nil)
			if read.Code != http.StatusOK {
				t.Fatalf("the read answered %d, want 200: %s", read.Code, read.Body.String())
			}
			if got := read.Body.String(); got != body {
				t.Errorf("the bytes read %q, want %q", got, body)
			}
			if got := read.Header().Get("Content-Type"); got != "text/plain" {
				t.Errorf("the media type is %q, want text/plain", got)
			}
			wantDisposition := mime.FormatMediaType("inline", map[string]string{"filename": name})
			if got := read.Header().Get("Content-Disposition"); got != wantDisposition {
				t.Errorf("the disposition is %q, want %q", got, wantDisposition)
			}
			for header, want := range map[string]string{
				"X-Content-Type-Options":  "nosniff",
				"Content-Security-Policy": "sandbox",
				"Referrer-Policy":         "no-referrer",
			} {
				if got := read.Header().Get(header); got != want {
					t.Errorf("the header %s is %q, want %q", header, got, want)
				}
			}
			if cache := read.Header().Get("Cache-Control"); !strings.HasPrefix(cache, "private, max-age=") {
				t.Errorf("the cache directive is %q, want a private max-age", cache)
			}

			head := serveContent(engine, http.MethodHead, targetFor(token), nil)
			if head.Code != http.StatusOK {
				t.Errorf("HEAD answered %d, want 200", head.Code)
			}
			if head.Body.Len() != 0 {
				t.Errorf("HEAD carried a body of %d bytes, want none", head.Body.Len())
			}
			if got := head.Header().Get("Content-Length"); got != strconv.Itoa(len(body)) {
				t.Errorf("HEAD answered Content-Length %q, want %d", got, len(body))
			}

			ranged := serveContentWith(engine, http.MethodGet, targetFor(token), nil, func(r *http.Request) {
				r.Header.Set("Range", "bytes=0-4")
			})
			if ranged.Code != http.StatusPartialContent {
				t.Errorf("a range request answered %d, want 206", ranged.Code)
			}
			if got := ranged.Body.String(); got != body[:5] {
				t.Errorf("the ranged bytes read %q, want %q", got, body[:5])
			}
			return nil
		})
	})
}

// TestTheContentReadFallsBackToOctetStream holds that a completed row with no
// recorded MIME is served as application/octet-stream.
func TestTheContentReadFallsBackToOctetStream(t *testing.T) {
	forEachEngine(t, func(t *testing.T, e engine) {
		h := e.on(t, harness{store: newFakeStore(), router: newRecordingRouter("public")})
		h.drive(t, func(ctx context.Context, reg *core.Registry) error {
			objects := objectsOf(t, reg)
			tctx := tenantCtx(t, reg, "acme")
			id := declare(t, tctx, objects, "unnamed-type", "hello")
			if err := dbHandle(t, reg).Exec(
				"UPDATE objects SET mime = '' WHERE tenant_id = ? AND id = ?", "acme", id).Error; err != nil {
				t.Fatalf("clearing the recorded mime: %v", err)
			}
			token := contentToken(t, objects, tctx, id, objectstore.URLAccessRead)
			read := serveContent(contentEngine(productOfAssembly(t, reg)), http.MethodGet, targetFor(token), nil)
			if read.Code != http.StatusOK {
				t.Fatalf("the read answered %d, want 200", read.Code)
			}
			if got := read.Header().Get("Content-Type"); got != fallbackContentType {
				t.Errorf("the media type is %q, want the fallback %q", got, fallbackContentType)
			}
			return nil
		})
	})
}

// TestTheContentReadCopiesWhenTheStreamCannotSeek holds the branch for a backend
// whose stream does not implement io.ReadSeeker: a full copy with an explicit
// length and no range support.
func TestTheContentReadCopiesWhenTheStreamCannotSeek(t *testing.T) {
	forEachEngine(t, func(t *testing.T, e engine) {
		store := newFakeStore()
		store.notSeekable = true
		h := e.on(t, harness{store: store, router: newRecordingRouter("public")})
		h.drive(t, func(ctx context.Context, reg *core.Registry) error {
			objects := objectsOf(t, reg)
			tctx := tenantCtx(t, reg, "acme")
			id := declare(t, tctx, objects, "unseekable", "hello")
			token := contentToken(t, objects, tctx, id, objectstore.URLAccessRead)
			engine := contentEngine(productOfAssembly(t, reg))

			read := serveContent(engine, http.MethodGet, targetFor(token), nil)
			if read.Code != http.StatusOK || read.Body.String() != "hello" {
				t.Errorf("the copy answered %d with %q, want 200 and the bytes", read.Code, read.Body.String())
			}
			if got := read.Header().Get("Accept-Ranges"); got != "none" {
				t.Errorf("Accept-Ranges is %q, want none", got)
			}
			head := serveContent(engine, http.MethodHead, targetFor(token), nil)
			if head.Code != http.StatusOK || head.Body.Len() != 0 {
				t.Errorf("HEAD answered %d with %d bytes, want 200 and no body", head.Code, head.Body.Len())
			}
			return nil
		})
	})
}

// TestTheContentReadRefusesWhatItCannotServe walks the read refusals: a wrong or
// unknown token, the wrong direction, an expired grant, a deleted object,
// missing bytes and an unreachable backend.
func TestTheContentReadRefusesWhatItCannotServe(t *testing.T) {
	forEachEngine(t, func(t *testing.T, e engine) {
		store := newFakeStore()
		h := e.on(t, harness{store: store, router: newRecordingRouter("public")})
		h.drive(t, func(ctx context.Context, reg *core.Registry) error {
			objects := objectsOf(t, reg)
			tctx := tenantCtx(t, reg, "acme")
			engine := contentEngine(productOfAssembly(t, reg))

			if got := serveContent(engine, http.MethodGet, contentPath, nil).Code; got != http.StatusNotFound {
				t.Errorf("a request with no token answered %d, want 404", got)
			}
			if got := serveContent(engine, http.MethodGet, targetFor("short"), nil).Code; got != http.StatusNotFound {
				t.Errorf("a malformed token answered %d, want 404", got)
			}
			if got := serveContent(engine, http.MethodGet, targetFor(strings.Repeat("A", tokenChars)), nil).Code; got != http.StatusNotFound {
				t.Errorf("an unknown token answered %d, want 404", got)
			}

			// A token minted for the other direction names no read grant.
			pending, err := objects.Create(tctx, objectstore.Declaration{Size: 4, ContentType: "text/plain"})
			if err != nil {
				t.Fatalf("declaring: %v", err)
			}
			writeToken := contentToken(t, objects, tctx, pending.ID, objectstore.URLAccessWrite)
			if got := serveContent(engine, http.MethodGet, targetFor(writeToken), nil).Code; got != http.StatusNotFound {
				t.Errorf("a write token on a read answered %d, want 404", got)
			}

			// An expired grant is 410, and its object is still readable with a
			// fresh one: the grant, not the object, expired.
			expiredID := declare(t, tctx, objects, "expiring", "body")
			expiredToken := contentToken(t, objects, tctx, expiredID, objectstore.URLAccessRead)
			if setErr := dbHandle(t, reg).Exec(
				"UPDATE object_grants SET expires_at = ? WHERE token_hash = ?",
				time.Now().Add(-time.Minute).UnixMilli(), hashToken(expiredToken)).Error; setErr != nil {
				t.Fatalf("expiring the grant: %v", setErr)
			}
			if got := serveContent(engine, http.MethodGet, targetFor(expiredToken), nil).Code; got != http.StatusGone {
				t.Errorf("an expired grant answered %d, want 410", got)
			}

			// A deleted object is 404, even though its grant has not expired.
			deletedID := declare(t, tctx, objects, "gone", "body")
			deletedToken := contentToken(t, objects, tctx, deletedID, objectstore.URLAccessRead)
			if delErr := objects.Delete(tctx, deletedID); delErr != nil {
				t.Fatalf("deleting: %v", delErr)
			}
			if got := serveContent(engine, http.MethodGet, targetFor(deletedToken), nil).Code; got != http.StatusNotFound {
				t.Errorf("a deleted object answered %d, want 404", got)
			}

			// A row that says completed while the backend holds no bytes is 404.
			missingID := declare(t, tctx, objects, "missing", "body")
			missingToken := contentToken(t, objects, tctx, missingID, objectstore.URLAccessRead)
			missingRow, err := objects.Get(tctx, missingID)
			if err != nil {
				t.Fatalf("reading the object: %v", err)
			}
			store.mu.Lock()
			delete(store.blobs, missingRow.Key)
			store.mu.Unlock()
			if got := serveContent(engine, http.MethodGet, targetFor(missingToken), nil).Code; got != http.StatusNotFound {
				t.Errorf("missing bytes answered %d, want 404", got)
			}

			// An unreachable backend is 503.
			unreachableID := declare(t, tctx, objects, "unreachable", "body")
			unreachableToken := contentToken(t, objects, tctx, unreachableID, objectstore.URLAccessRead)
			store.mu.Lock()
			store.openErr = errors.New("the backend is unreachable")
			store.mu.Unlock()
			if got := serveContent(engine, http.MethodGet, targetFor(unreachableToken), nil).Code; got != http.StatusServiceUnavailable {
				t.Errorf("an unreachable backend answered %d, want 503", got)
			}
			store.mu.Lock()
			store.openErr = nil
			store.mu.Unlock()

			// A tenant the roster will not enter is 404.
			suspended := tenantCtx(t, reg, "suspended")
			suspendedObj, err := objects.Create(suspended, objectstore.Declaration{Size: 4, ContentType: "text/plain"})
			if err != nil {
				t.Fatalf("declaring for the suspended tenant: %v", err)
			}
			if upErr := objects.Upload(suspended, suspendedObj.ID, strings.NewReader("body"), 4); upErr != nil {
				t.Fatalf("uploading: %v", upErr)
			}
			if _, cErr := objects.Complete(suspended, suspendedObj.ID); cErr != nil {
				t.Fatalf("completing: %v", cErr)
			}
			suspendedToken := contentToken(t, objects, suspended, suspendedObj.ID, objectstore.URLAccessRead)
			roster, err := core.Resolve[tenant.Catalog](reg)
			if err != nil {
				t.Fatalf("resolving the roster: %v", err)
			}
			if sErr := roster.SetStatus(ctx, tenant.ID("suspended"), tenant.StatusDisabled); sErr != nil {
				t.Fatalf("disabling the tenant: %v", sErr)
			}
			if got := serveContent(engine, http.MethodGet, targetFor(suspendedToken), nil).Code; got != http.StatusNotFound {
				t.Errorf("a disabled tenant answered %d, want 404", got)
			}
			return nil
		})
	})
}

// TestTheContentWriteStoresTheBytes holds the successful PUT: 204, the bytes
// behind the seam, the row uploaded with the observed values, and a completion
// that follows.
func TestTheContentWriteStoresTheBytes(t *testing.T) {
	forEachEngine(t, func(t *testing.T, e engine) {
		store := newFakeStore()
		h := e.on(t, harness{store: store, router: newRecordingRouter("public")})
		h.drive(t, func(ctx context.Context, reg *core.Registry) error {
			objects := objectsOf(t, reg)
			tctx := tenantCtx(t, reg, "acme")
			body := "body"
			obj, err := objects.Create(tctx, objectstore.Declaration{Size: int64(len(body)), ContentType: "text/plain"})
			if err != nil {
				t.Fatalf("declaring: %v", err)
			}
			token := contentToken(t, objects, tctx, obj.ID, objectstore.URLAccessWrite)
			engine := contentEngine(productOfAssembly(t, reg))

			stored := serveContent(engine, http.MethodPut, targetFor(token), bytes.NewReader([]byte(body)))
			if stored.Code != http.StatusNoContent {
				t.Fatalf("the write answered %d, want 204: %s", stored.Code, stored.Body.String())
			}
			if got := stored.Header().Get("Cache-Control"); got != "no-store" {
				t.Errorf("the write's cache directive is %q, want no-store", got)
			}
			if !store.has(obj.Key) {
				t.Error("the write stored no bytes behind the seam")
			}
			if state := rowState(t, reg, "acme", obj.ID); state != string(objectstore.StateUploaded) {
				t.Errorf("the row is %s after the write, want uploaded", state)
			}
			got, err := objects.Get(tctx, obj.ID)
			if err != nil {
				t.Fatalf("reading the row back: %v", err)
			}
			if got.MIME != "text/plain" || got.Size != int64(len(body)) || len(got.Checksum) != 64 {
				t.Errorf("the recorded values read %+v, want the declared type, the observed size and a checksum", got)
			}
			if _, cErr := objects.Complete(tctx, obj.ID); cErr != nil {
				t.Fatalf("completing after the direct write: %v", cErr)
			}
			return nil
		})
	})
}

// TestTheContentWriteRefusesWhatItCannotStore walks the write refusals: 411, 400
// on a length mismatch (with no byte stored), 410 on a closed window, 409 on a
// completed object, and 400 on a declared-checksum mismatch (with the bytes
// removed).
func TestTheContentWriteRefusesWhatItCannotStore(t *testing.T) {
	forEachEngine(t, func(t *testing.T, e engine) {
		store := newFakeStore()
		h := e.on(t, harness{store: store, router: newRecordingRouter("public")})
		h.drive(t, func(ctx context.Context, reg *core.Registry) error {
			objects := objectsOf(t, reg)
			tctx := tenantCtx(t, reg, "acme")
			engine := contentEngine(productOfAssembly(t, reg))

			// A request that names no length cannot be checked.
			pending, err := objects.Create(tctx, objectstore.Declaration{Size: 4, ContentType: "text/plain"})
			if err != nil {
				t.Fatalf("declaring: %v", err)
			}
			writeToken := contentToken(t, objects, tctx, pending.ID, objectstore.URLAccessWrite)
			unmeasured := serveContent(engine, http.MethodPut, targetFor(writeToken),
				struct{ io.Reader }{strings.NewReader("body")})
			if unmeasured.Code != http.StatusLengthRequired {
				t.Errorf("a body with no Content-Length answered %d, want 411", unmeasured.Code)
			}

			// A length that differs from the declaration is 400 and stores
			// nothing.
			mismatch := serveContent(engine, http.MethodPut, targetFor(writeToken), strings.NewReader("toolong"))
			if mismatch.Code != http.StatusBadRequest {
				t.Errorf("a mismatched length answered %d, want 400", mismatch.Code)
			}
			if store.has(pending.Key) {
				t.Error("a refused write left bytes behind the seam")
			}

			// A closed upload window is 410.
			closed, err := objects.Create(tctx, objectstore.Declaration{Size: 4, ContentType: "text/plain"})
			if err != nil {
				t.Fatalf("declaring: %v", err)
			}
			closedToken := contentToken(t, objects, tctx, closed.ID, objectstore.URLAccessWrite)
			setColumn(t, reg, "upload_expires_at", time.Now().Add(-time.Minute).UnixMilli(), "acme", closed.ID)
			if got := serveContent(engine, http.MethodPut, targetFor(closedToken), strings.NewReader("body")).Code; got != http.StatusGone {
				t.Errorf("a closed window answered %d, want 410", got)
			}

			// A declared checksum that the bytes do not match is 400, and the
			// bytes are removed.
			other := sha256.Sum256([]byte("other"))
			checksummed, err := objects.Create(tctx, objectstore.Declaration{
				Size: 4, ContentType: "text/plain", ChecksumSHA256: hex.EncodeToString(other[:]),
			})
			if err != nil {
				t.Fatalf("declaring with a checksum: %v", err)
			}
			checksumToken := contentToken(t, objects, tctx, checksummed.ID, objectstore.URLAccessWrite)
			badChecksum := serveContent(engine, http.MethodPut, targetFor(checksumToken), strings.NewReader("body"))
			if badChecksum.Code != http.StatusBadRequest {
				t.Errorf("a checksum mismatch answered %d, want 400", badChecksum.Code)
			}
			if store.has(checksummed.Key) {
				t.Error("a checksum mismatch left the bytes behind")
			}
			return nil
		})
	})
}

// TestAContentTokenCannotCrossTenants holds both halves of the tenant rule: the
// tenant is taken from the grant rather than from whatever context the request
// arrives in, and a grant that names one tenant cannot reach another tenant's
// object.
func TestAContentTokenCannotCrossTenants(t *testing.T) {
	forEachEngine(t, func(t *testing.T, e engine) {
		h := e.on(t, harness{store: newFakeStore(), router: newRecordingRouter("public")})
		h.drive(t, func(ctx context.Context, reg *core.Registry) error {
			objects := objectsOf(t, reg)
			acme := tenantCtx(t, reg, "acme")
			globex := tenantCtx(t, reg, "globex")
			acmeID := declare(t, acme, objects, "acme-doc", "acme body")
			_ = declare(t, globex, objects, "globex-doc", "globex body")
			token := contentToken(t, objects, acme, acmeID, objectstore.URLAccessRead)
			engine := contentEngine(productOfAssembly(t, reg))

			// An ambient tenant does not change whose object the token names.
			read := serveContentWith(engine, http.MethodGet, targetFor(token), nil, func(r *http.Request) {
				*r = *r.WithContext(globex)
			})
			if read.Code != http.StatusOK || read.Body.String() != "acme body" {
				t.Errorf("a request carrying another tenant read %d with %q, want acme's bytes",
					read.Code, read.Body.String())
			}

			// A grant whose tenant does not own the object reaches nothing.
			if err := dbHandle(t, reg).Exec(
				"UPDATE object_grants SET tenant_id = ? WHERE token_hash = ?",
				"globex", hashToken(token)).Error; err != nil {
				t.Fatalf("moving the grant to another tenant: %v", err)
			}
			crossed := serveContent(engine, http.MethodGet, targetFor(token), nil)
			if crossed.Code != http.StatusNotFound {
				t.Errorf("a grant naming the wrong tenant answered %d, want 404", crossed.Code)
			}
			return nil
		})
	})
}

// TestTheContentWriteOnACompletedObjectIsConflict holds the 409 branch: a grant
// minted while the object was writable is used after the object completed.
func TestTheContentWriteOnACompletedObjectIsConflict(t *testing.T) {
	forEachEngine(t, func(t *testing.T, e engine) {
		h := e.on(t, harness{store: newFakeStore(), router: newRecordingRouter("public")})
		h.drive(t, func(ctx context.Context, reg *core.Registry) error {
			objects := objectsOf(t, reg)
			tctx := tenantCtx(t, reg, "acme")
			obj, err := objects.Create(tctx, objectstore.Declaration{Size: 4, ContentType: "text/plain"})
			if err != nil {
				t.Fatalf("declaring: %v", err)
			}
			token := contentToken(t, objects, tctx, obj.ID, objectstore.URLAccessWrite)
			if upErr := objects.Upload(tctx, obj.ID, strings.NewReader("body"), 4); upErr != nil {
				t.Fatalf("uploading: %v", upErr)
			}
			if _, cErr := objects.Complete(tctx, obj.ID); cErr != nil {
				t.Fatalf("completing: %v", cErr)
			}
			got := serveContent(contentEngine(productOfAssembly(t, reg)), http.MethodPut, targetFor(token),
				strings.NewReader("body"))
			if got.Code != http.StatusConflict {
				t.Errorf("a write to a completed object answered %d, want 409", got.Code)
			}
			return nil
		})
	})
}

// TestTheContentWriteRefusesTheOtherDirection holds that a read token names no
// write grant.
func TestTheContentWriteRefusesTheOtherDirection(t *testing.T) {
	forEachEngine(t, func(t *testing.T, e engine) {
		h := e.on(t, harness{store: newFakeStore(), router: newRecordingRouter("public")})
		h.drive(t, func(ctx context.Context, reg *core.Registry) error {
			objects := objectsOf(t, reg)
			tctx := tenantCtx(t, reg, "acme")
			id := declare(t, tctx, objects, "done", "body")
			token := contentToken(t, objects, tctx, id, objectstore.URLAccessRead)
			got := serveContent(contentEngine(productOfAssembly(t, reg)), http.MethodPut, targetFor(token),
				strings.NewReader("body"))
			if got.Code != http.StatusNotFound {
				t.Errorf("a read token on a write answered %d, want 404", got.Code)
			}
			return nil
		})
	})
}

// TestTheContentTokenTravelsOnlyInTheQuery holds the property the access log
// depends on: the token is in the query string, and the request's path is the
// bare content path. pkg/http's access log records r.URL.Path and no part of the
// query string, so this is what keeps a credential out of the log.
//
// It also holds that no line this module writes carries the token.
func TestTheContentTokenTravelsOnlyInTheQuery(t *testing.T) {
	forEachEngine(t, func(t *testing.T, e engine) {
		store := newFakeStore()
		h := e.on(t, harness{store: store, router: newRecordingRouter("public")})
		h.drive(t, func(ctx context.Context, reg *core.Registry) error {
			objects := objectsOf(t, reg)
			tctx := tenantCtx(t, reg, "acme")
			id := declare(t, tctx, objects, "done", "body")
			issued := mintURL(t, objects, tctx, id, objectstore.URLAccessRead, 0)
			parsed, err := url.Parse(issued.URL)
			if err != nil {
				t.Fatalf("parsing the minted address: %v", err)
			}
			token := parsed.Query().Get(contentTokenParam)
			if token == "" {
				t.Fatalf("the minted address %q carries no token", issued.URL)
			}
			if parsed.Path != contentPath {
				t.Errorf("the minted address's path is %q, want %q", parsed.Path, contentPath)
			}
			if strings.Contains(parsed.Path, token) {
				t.Error("the token appears in the path the access log records")
			}

			// The request the route sees carries the same shape: path only, token
			// in the query.
			request := httptest.NewRequest(http.MethodGet, issued.URL, nil)
			if request.URL.Path != contentPath || strings.Contains(request.URL.Path, token) {
				t.Errorf("the served request's path is %q, which is not the token-free %q",
					request.URL.Path, contentPath)
			}
			if request.URL.Query().Get(contentTokenParam) != token {
				t.Error("the served request does not carry the token in its query")
			}

			// This module writes no line carrying the token, not even on a
			// failing backend.
			capture := &lineCapture{}
			productOfAssembly(t, reg).logger = slog.New(capture)
			store.mu.Lock()
			store.openErr = errors.New("the backend is unreachable")
			store.mu.Unlock()
			serveContent(contentEngine(productOfAssembly(t, reg)), http.MethodGet, issued.URL, nil)
			store.mu.Lock()
			store.openErr = nil
			store.mu.Unlock()
			for _, line := range capture.lines() {
				if strings.Contains(line, token) {
					t.Errorf("a log line carries the token: %s", line)
				}
			}
			return nil
		})
	})
}

// lineCapture keeps every record written through it as one line of message and
// attributes.
type lineCapture struct {
	mu    sync.Mutex
	wrote []string
}

func (h *lineCapture) Enabled(context.Context, slog.Level) bool { return true }

func (h *lineCapture) Handle(_ context.Context, r slog.Record) error {
	line := r.Message
	r.Attrs(func(a slog.Attr) bool {
		line += " " + a.Key + "=" + fmt.Sprint(a.Value.Any())
		return true
	})
	h.mu.Lock()
	defer h.mu.Unlock()
	h.wrote = append(h.wrote, line)
	return nil
}

func (h *lineCapture) WithAttrs([]slog.Attr) slog.Handler { return h }
func (h *lineCapture) WithGroup(string) slog.Handler      { return h }

func (h *lineCapture) lines() []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]string(nil), h.wrote...)
}
