package webui

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	nethttp "net/http"
	pathpkg "path"
	"strings"

	"github.com/gin-gonic/gin"

	speedhttp "github.com/vislake/speed/pkg/http"
)

const (
	// wildcardParam is the gin wildcard parameter name the route declares.
	wildcardParam = "path"
	// wildcardRoute is the route this module binds for GET and for HEAD: gin's
	// catch-all, covering the whole path space of the endpoint, the root
	// included.
	wildcardRoute = "/*" + wildcardParam

	// entryDocument is the answer to a path the bundle does not carry and the
	// target of the root path's lookup.
	entryDocument = "index.html"

	// longCache is the Cache-Control of a file under the content-addressed
	// subtree: its name changes with its content, so a cached copy never goes
	// stale and it is held for a year without revalidation.
	longCache = "public, max-age=31536000, immutable"
	// noCache is the Cache-Control of the entry document and of every other
	// file: a client may keep a copy but must revalidate it before using it.
	noCache = "no-cache"
)

// handler answers one request from the bundle. It holds the bundle's root and
// this module's decoded configuration, both fixed at construction.
type handler struct {
	fsys fs.FS
	cfg  Config
}

// newHandler builds the request handler. A configuration that names no asset
// prefix, or names one with slashes around it, is normalised here so the
// subtree comparison that follows reads one form.
func newHandler(fsys fs.FS, cfg Config) *handler {
	if cfg.AssetPrefix == "" {
		cfg.AssetPrefix = defaultAssetPrefix
	}
	cfg.AssetPrefix = strings.Trim(cfg.AssetPrefix, "/")
	return &handler{fsys: fsys, cfg: cfg}
}

// handle is the wildcard route's handler. It is bound for GET and for HEAD;
// the two walk the same path and differ only in whether a body is written,
// which net/http's ServeContent decides.
//
// The request path is taken from the wildcard parameter, cleaned into a key
// inside the bundle, and looked up. A key the bundle does not satisfy is
// answered by the fallback; a file the bundle carries is answered with the
// cache header its path earns.
func (h *handler) handle(c *gin.Context) {
	key := lookupKey(c.Param(wildcardParam))
	switch {
	case key == ".":
		// The root is the entry document, reached directly rather than through
		// a lookup that would find the bundle's root directory.
		h.serveEntry(c)
	case !fs.ValidPath(key):
		// A key that is not a valid bundle-relative path names nothing inside
		// the bundle — a traversal folded onto the root rather than climbing
		// above it, or some other malformed form. It is refused.
		h.refuse(c)
	default:
		h.serveLookup(c, key)
	}
}

// serveLookup opens one cleaned key and answers it, or falls back.
func (h *handler) serveLookup(c *gin.Context, key string) {
	file, err := h.fsys.Open(key)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			h.miss(c, key)
			return
		}
		h.fail(c, key, err)
		return
	}
	defer func() { _ = file.Close() }()

	info, err := file.Stat()
	if err != nil {
		h.fail(c, key, err)
		return
	}
	if info.IsDir() {
		// A directory is not a file to answer. It is treated as an absence, so
		// the path falls back (or, under the content-addressed subtree, is
		// refused) the way any other missing file is.
		h.miss(c, key)
		return
	}
	h.serve(c, key, file, info)
}

// miss answers a lookup that found no file. Inside the content-addressed
// subtree it is a 404 and never the entry document: a missing hashed file means
// the bundle is not the one the entry document was built against, and answering
// the request with HTML would let a browser run a document as a script behind a
// 200. Everywhere else it is the entry document, which is the client-side
// router's deep link.
func (h *handler) miss(c *gin.Context, key string) {
	if h.contentAddressed(key) {
		h.refuse(c)
		return
	}
	h.serveEntry(c)
}

// serveEntry answers the entry document. A bundle with no entry document — one
// not built yet, or not yet deployed — answers 404 here too; that is the
// module's declared behaviour and not a startup failure, so a frontend may come
// up after the process.
func (h *handler) serveEntry(c *gin.Context) {
	file, err := h.fsys.Open(entryDocument)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			h.refuse(c)
			return
		}
		h.fail(c, entryDocument, err)
		return
	}
	defer func() { _ = file.Close() }()

	info, err := file.Stat()
	if err != nil {
		h.fail(c, entryDocument, err)
		return
	}
	if info.IsDir() {
		h.refuse(c)
		return
	}
	h.serve(c, entryDocument, file, info)
}

// serve answers one file: it sets the cache header the path earns, hands the
// content and the file's information to net/http's ServeContent, and lets it
// decide the media type, answer a conditional request and serve a range. For a
// HEAD request ServeContent writes the headers and no body.
func (h *handler) serve(c *gin.Context, name string, file fs.File, info fs.FileInfo) {
	content, err := contents(file, info)
	if err != nil {
		h.fail(c, name, err)
		return
	}
	c.Header("Cache-Control", h.cacheControl(name))
	nethttp.ServeContent(c.Writer, c.Request, name, info.ModTime(), content)
}

// cacheControl is the Cache-Control this module answers a path with: the long,
// immutable value for a file under the content-addressed subtree, no-cache for
// the entry document and every other file. It depends on the path alone and not
// on the request.
func (h *handler) cacheControl(name string) string {
	if h.contentAddressed(name) {
		return longCache
	}
	return noCache
}

// contentAddressed reports whether a bundle-relative key names the
// content-addressed subtree or something under it.
func (h *handler) contentAddressed(key string) bool {
	prefix := h.cfg.AssetPrefix
	if prefix == "" {
		return false
	}
	return key == prefix || strings.HasPrefix(key, prefix+"/")
}

// refuse answers 404 in the entry point's error shape. It is how this module
// answers a key the bundle does not carry and must not fall back for, and a key
// that is not a valid bundle path.
func (h *handler) refuse(c *gin.Context) {
	speedhttp.Fail(c, speedhttp.Problem{Status: nethttp.StatusNotFound})
}

// fail answers 500 for a read of the bundle that failed for a reason other than
// a plain absence. The bundle is read-only and the failure is that request's
// result: the module retries nothing and degrades nothing.
func (h *handler) fail(c *gin.Context, name string, err error) {
	speedhttp.Fail(c, speedhttp.Problem{
		Status: nethttp.StatusInternalServerError,
		Detail: fmt.Sprintf("the frontend bundle could not answer a read of %q", name),
		Cause:  err,
	})
}

// lookupKey turns the value gin hands a catch-all into the bundle-relative
// lookup key: the leading slash is dropped and the rest is cleaned with
// path.Clean. Cleaning folds a "." or ".." segment that reaches the root onto
// the root rather than climbing above the bundle, and a key that survives as
// something other than a valid bundle path is refused by the caller. The root
// path cleans to ".", which the caller maps to the entry document.
func lookupKey(param string) string {
	return pathpkg.Clean(strings.TrimPrefix(param, "/"))
}

// contents returns a ReadSeeker over the file's bytes for ServeContent. A file
// that is already seekable is handed over as it is; one that offers only random
// access is wrapped in a section reader over its whole length; anything else is
// read whole. Every fs.FS this module is assembled with — embed.FS, os.DirFS,
// the in-memory fixtures — yields a seekable file, so the first branch is the
// ordinary one and the other two are guards.
func contents(file fs.File, info fs.FileInfo) (io.ReadSeeker, error) {
	if rs, ok := file.(io.ReadSeeker); ok {
		return rs, nil
	}
	if ra, ok := file.(io.ReaderAt); ok {
		return io.NewSectionReader(ra, 0, info.Size()), nil
	}
	data, err := io.ReadAll(file)
	if err != nil {
		return nil, err
	}
	return bytes.NewReader(data), nil
}
