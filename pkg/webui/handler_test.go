package webui

import (
	"errors"
	"io/fs"
	nethttp "net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"testing/fstest"
	"time"

	"github.com/gin-gonic/gin"
)

const (
	entryDoc  = "<!doctype html><title>app</title>"
	assetName = "assets/app.abc123.js"
)

func bundle() fstest.MapFS {
	return fstest.MapFS{
		entryDocument:          &fstest.MapFile{Data: []byte(entryDoc), ModTime: time.Unix(1_700_000_000, 0)},
		assetName:              &fstest.MapFile{Data: []byte("console.log(1)")},
		"favicon.ico":          &fstest.MapFile{Data: []byte{0, 1, 2, 3}},
		"sub/page.txt":         &fstest.MapFile{Data: []byte("a sub page")},
		"assets/other.def.css": &fstest.MapFile{Data: []byte("body{}")},
	}
}

func testEngine(fsys fs.FS, cfg Config) *gin.Engine {
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	h := newHandler(fsys, cfg)
	engine.Handle(nethttp.MethodGet, wildcardRoute, h.handle)
	engine.Handle(nethttp.MethodHead, wildcardRoute, h.handle)
	return engine
}

func ask(t *testing.T, engine *gin.Engine, method, target string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, target, nil)
	rec := httptest.NewRecorder()
	engine.ServeHTTP(rec, req)
	return rec
}

func defaultConfig() Config { return Config{Endpoint: "public"} }

// TestAFileIsAnsweredWithItsBytesAndTheRightCacheHeader checks a content-addressed
// file's bytes, its long cache header and its media type.
func TestAFileIsAnsweredWithItsBytesAndTheRightCacheHeader(t *testing.T) {
	engine := testEngine(bundle(), defaultConfig())

	rec := ask(t, engine, nethttp.MethodGet, "/"+assetName)
	if rec.Code != nethttp.StatusOK {
		t.Fatalf("GET /%s answered %d, want 200", assetName, rec.Code)
	}
	if got := rec.Body.String(); got != "console.log(1)" {
		t.Fatalf("GET /%s answered %q, want the file's bytes", assetName, got)
	}
	if got := rec.Header().Get("Cache-Control"); got != longCache {
		t.Fatalf("GET /%s carries Cache-Control %q, want %q", assetName, got, longCache)
	}
	if ct := rec.Header().Get("Content-Type"); ct == "" {
		t.Fatalf("GET /%s carries no Content-Type, want one ServeContent derived", assetName)
	}
}

// TestAFileOutsideTheContentAddressedSubtreeIsNotCached checks that a file beside
// the entry document is answered no-cache.
func TestAFileOutsideTheContentAddressedSubtreeIsNotCached(t *testing.T) {
	engine := testEngine(bundle(), defaultConfig())

	rec := ask(t, engine, nethttp.MethodGet, "/favicon.ico")
	if rec.Code != nethttp.StatusOK {
		t.Fatalf("GET /favicon.ico answered %d, want 200", rec.Code)
	}
	if got := rec.Header().Get("Cache-Control"); got != noCache {
		t.Fatalf("GET /favicon.ico carries Cache-Control %q, want %q", got, noCache)
	}
}

// TestAnUnknownPathFallsBackToTheEntryDocument checks that the root and a
// client-side route fall back to the entry document with no-cache.
func TestAnUnknownPathFallsBackToTheEntryDocument(t *testing.T) {
	engine := testEngine(bundle(), defaultConfig())

	for _, target := range []string{"/", "/a/client/route", "/a/deep/link/that/is/not/here"} {
		rec := ask(t, engine, nethttp.MethodGet, target)
		if rec.Code != nethttp.StatusOK {
			t.Fatalf("GET %s answered %d, want 200 (the entry document)", target, rec.Code)
		}
		if got := rec.Body.String(); got != entryDoc {
			t.Fatalf("GET %s answered %q, want the entry document", target, got)
		}
		if got := rec.Header().Get("Cache-Control"); got != noCache {
			t.Fatalf("GET %s carries Cache-Control %q, want %q", target, got, noCache)
		}
	}
}

// TestAMissUnderTheContentAddressedSubtreeDoesNotFallBack checks that a missing
// asset answers 404 rather than the entry document.
func TestAMissUnderTheContentAddressedSubtreeDoesNotFallBack(t *testing.T) {
	engine := testEngine(bundle(), defaultConfig())

	rec := ask(t, engine, nethttp.MethodGet, "/assets/missing.deadbeef.js")
	if rec.Code != nethttp.StatusNotFound {
		t.Fatalf("GET a missing asset answered %d, want 404 (no fallback)", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/problem+json" {
		t.Fatalf("GET a missing asset answered with media type %q, want a problem document", ct)
	}
	if body := rec.Body.String(); body == entryDoc {
		t.Fatalf("GET a missing asset answered the entry document; an asset miss must not fall back")
	}
}

// TestAMissingStaticResourceDoesNotFallBack checks that a missing extensioned
// path answers 404 rather than the entry document.
func TestAMissingStaticResourceDoesNotFallBack(t *testing.T) {
	engine := testEngine(bundle(), defaultConfig())

	for _, target := range []string{
		"/runtime-config.json",
		"/favicon-missing.ico",
		"/sub/missing.js",
		"/.well-known/appspecific.json",
	} {
		rec := ask(t, engine, nethttp.MethodGet, target)
		if rec.Code != nethttp.StatusNotFound {
			t.Fatalf("GET %s answered %d, want 404 (a missing static resource does not fall back)",
				target, rec.Code)
		}
		if ct := rec.Header().Get("Content-Type"); ct != "application/problem+json" {
			t.Fatalf("GET %s answered media type %q, want a problem document", target, ct)
		}
		if body := rec.Body.String(); body == entryDoc {
			t.Fatalf("GET %s answered the entry document; a missing static resource must not fall back",
				target)
		}
	}
}

// TestAMissingResourceUnderAReservedPrefixDoesNotFallBack checks that an
// extensionless resource under a reserved prefix answers 404.
func TestAMissingResourceUnderAReservedPrefixDoesNotFallBack(t *testing.T) {
	engine := testEngine(bundle(), defaultConfig())

	for _, target := range []string{
		"/.well-known/acme-challenge/sometoken",
		"/.well-known/apple-app-site-association",
	} {
		rec := ask(t, engine, nethttp.MethodGet, target)
		if rec.Code != nethttp.StatusNotFound {
			t.Fatalf("GET %s answered %d, want 404 (a resource under a reserved prefix does not fall back)",
				target, rec.Code)
		}
		if ct := rec.Header().Get("Content-Type"); ct != "application/problem+json" {
			t.Fatalf("GET %s answered media type %q, want a problem document", target, ct)
		}
		if body := rec.Body.String(); body == entryDoc {
			t.Fatalf("GET %s answered the entry document; a resource miss must not fall back", target)
		}
	}
}

// TestAConfiguredResourcePrefixDoesNotFallBack checks that a configured prefix
// answers 404 while a bare path outside it still falls back.
func TestAConfiguredResourcePrefixDoesNotFallBack(t *testing.T) {
	cfg := defaultConfig()
	cfg.ResourcePrefixes = []string{"CNAME", "runtime-config"}
	engine := testEngine(bundle(), cfg)

	for _, target := range []string{"/CNAME", "/runtime-config", "/CNAME/nested"} {
		rec := ask(t, engine, nethttp.MethodGet, target)
		if rec.Code != nethttp.StatusNotFound {
			t.Fatalf("GET %s answered %d, want 404 (a configured resource prefix does not fall back)",
				target, rec.Code)
		}
		if body := rec.Body.String(); body == entryDoc {
			t.Fatalf("GET %s answered the entry document; a resource miss must not fall back", target)
		}
	}

	rec := ask(t, engine, nethttp.MethodGet, "/favicon")
	if rec.Code != nethttp.StatusOK || rec.Body.String() != entryDoc {
		t.Fatalf("GET /favicon answered %d %q, want the entry document: an extensionless path "+
			"outside every resource prefix is a client-side route", rec.Code, rec.Body.String())
	}
}

// TestADirectoryIsTreatedAsAMissAndFallsBack checks that a directory falls back to
// the entry document as a miss.
func TestADirectoryIsTreatedAsAMissAndFallsBack(t *testing.T) {
	engine := testEngine(bundle(), defaultConfig())

	rec := ask(t, engine, nethttp.MethodGet, "/sub")
	if rec.Code != nethttp.StatusOK {
		t.Fatalf("GET /sub answered %d, want 200 (a directory is not a file, so it falls back)", rec.Code)
	}
	if got := rec.Body.String(); got != entryDoc {
		t.Fatalf("GET /sub answered %q, want the entry document", got)
	}
}

// TestHeadAnswersTheHeadersAndNoBody checks that HEAD answers the headers with no
// body for a file and for the fallback.
func TestHeadAnswersTheHeadersAndNoBody(t *testing.T) {
	engine := testEngine(bundle(), defaultConfig())

	t.Run("a file under the content-addressed subtree", func(t *testing.T) {
		rec := ask(t, engine, nethttp.MethodHead, "/"+assetName)
		if rec.Code != nethttp.StatusOK {
			t.Fatalf("HEAD /%s answered %d, want 200", assetName, rec.Code)
		}
		if rec.Body.Len() != 0 {
			t.Fatalf("HEAD /%s answered a body of %d bytes, want none", assetName, rec.Body.Len())
		}
		if got := rec.Header().Get("Cache-Control"); got != longCache {
			t.Fatalf("HEAD /%s carries Cache-Control %q, want %q", assetName, got, longCache)
		}
	})

	t.Run("the entry document through the fallback", func(t *testing.T) {
		rec := ask(t, engine, nethttp.MethodHead, "/a/client/route")
		if rec.Code != nethttp.StatusOK {
			t.Fatalf("HEAD a client route answered %d, want 200", rec.Code)
		}
		if rec.Body.Len() != 0 {
			t.Fatalf("HEAD a client route answered a body of %d bytes, want none", rec.Body.Len())
		}
		if ct := rec.Header().Get("Content-Type"); ct == "" {
			t.Fatalf("HEAD a client route carries no Content-Type, want the entry document's")
		}
	})
}

// TestAnEmptyBundleAnswers404AndNotAFailure checks that an empty bundle answers
// 404 rather than failing.
func TestAnEmptyBundleAnswers404AndNotAFailure(t *testing.T) {
	engine := testEngine(fstest.MapFS{}, defaultConfig())

	for _, target := range []string{"/", "/a/client/route", "/assets/app.js"} {
		rec := ask(t, engine, nethttp.MethodGet, target)
		if rec.Code != nethttp.StatusNotFound {
			t.Fatalf("GET %s against an empty bundle answered %d, want 404", target, rec.Code)
		}
		if ct := rec.Header().Get("Content-Type"); ct != "application/problem+json" {
			t.Fatalf("GET %s against an empty bundle answered media type %q, want a problem document",
				target, ct)
		}
	}
}

// TestAPathThatIsNotAValidBundlePathIsRefused checks that a traversal is refused.
func TestAPathThatIsNotAValidBundlePathIsRefused(t *testing.T) {
	engine := testEngine(bundle(), defaultConfig())

	rec := ask(t, engine, nethttp.MethodGet, "/../index.html")
	if rec.Code != nethttp.StatusNotFound {
		t.Fatalf("GET /../index.html answered %d, want 404", rec.Code)
	}
}

// TestAReadFailureOtherThanAbsenceAnswers500 checks that a non-absence read
// failure answers 500.
func TestAReadFailureOtherThanAbsenceAnswers500(t *testing.T) {
	engine := testEngine(failingFS{}, defaultConfig())

	rec := ask(t, engine, nethttp.MethodGet, "/anything")
	if rec.Code != nethttp.StatusInternalServerError {
		t.Fatalf("GET a path whose read failed answered %d, want 500", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/problem+json" {
		t.Fatalf("the 500 answered media type %q, want a problem document", ct)
	}
}

// TestTheSameBundleFromDiskAnswersTheSame checks the seekable os.DirFS branch.
func TestTheSameBundleFromDiskAnswersTheSame(t *testing.T) {
	dir := t.TempDir()
	for name, data := range map[string]string{
		entryDocument: entryDoc,
		assetName:     "console.log(1)",
	} {
		path := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
			t.Fatalf("creating %s: %v", filepath.Dir(path), err)
		}
		if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
			t.Fatalf("writing %s: %v", path, err)
		}
	}
	engine := testEngine(os.DirFS(dir), defaultConfig())

	rec := ask(t, engine, nethttp.MethodGet, "/"+assetName)
	if rec.Code != nethttp.StatusOK || rec.Body.String() != "console.log(1)" {
		t.Fatalf("GET /%s from disk answered %d %q, want 200 with the file's bytes",
			assetName, rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("Cache-Control"); got != longCache {
		t.Fatalf("GET /%s from disk carries Cache-Control %q, want %q", assetName, got, longCache)
	}

	rec = ask(t, engine, nethttp.MethodGet, "/a/client/route")
	if rec.Code != nethttp.StatusOK || rec.Body.String() != entryDoc {
		t.Fatalf("GET a client route from disk answered %d %q, want 200 with the entry document",
			rec.Code, rec.Body.String())
	}
}

// TestAnUnseekableBundleIsReadWhole checks the read-it-whole branch of contents.
func TestAnUnseekableBundleIsReadWhole(t *testing.T) {
	engine := testEngine(plainFS{bundle()}, defaultConfig())

	rec := ask(t, engine, nethttp.MethodGet, "/"+assetName)
	if rec.Code != nethttp.StatusOK {
		t.Fatalf("GET /%s from an unseekable bundle answered %d, want 200", assetName, rec.Code)
	}
	if got := rec.Body.String(); got != "console.log(1)" {
		t.Fatalf("GET /%s from an unseekable bundle answered %q, want the file's bytes", assetName, got)
	}
	if got := rec.Header().Get("Cache-Control"); got != longCache {
		t.Fatalf("GET /%s from an unseekable bundle carries Cache-Control %q, want %q",
			assetName, got, longCache)
	}
}

// TestLookupKeyFoldsAndNeverClimbsAboveTheRoot checks lookupKey's cleaning of the
// catch-all value.
func TestLookupKeyFoldsAndNeverClimbsAboveTheRoot(t *testing.T) {
	cases := map[string]string{
		"/":                 ".",
		"":                  ".",
		"/index.html":       "index.html",
		"/a/b/c.js":         "a/b/c.js",
		"/a/./b":            "a/b",
		"/a/../b":           "b",
		"/../../etc/passwd": "../../etc/passwd",
	}
	for param, want := range cases {
		if got := lookupKey(param); got != want {
			t.Errorf("lookupKey(%q) = %q, want %q", param, got, want)
		}
	}
}

type failingFS struct{}

func (failingFS) Open(string) (fs.File, error) { return nil, errBoom }

var errBoom = errors.New("boom")

type plainFS struct{ inner fs.FS }

func (p plainFS) Open(name string) (fs.File, error) {
	file, err := p.inner.Open(name)
	if err != nil {
		return nil, err
	}
	return plainFile{File: file}, nil
}

type plainFile struct{ fs.File }
