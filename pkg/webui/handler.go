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
	wildcardParam = "path"
	wildcardRoute = "/*" + wildcardParam

	entryDocument = "index.html"

	longCache = "public, max-age=31536000, immutable"
	noCache   = "no-cache"
)

type handler struct {
	fsys fs.FS
	cfg  Config
}

func newHandler(fsys fs.FS, cfg Config) *handler {
	if cfg.AssetPrefix == "" {
		cfg.AssetPrefix = defaultAssetPrefix
	}
	cfg.AssetPrefix = strings.Trim(cfg.AssetPrefix, "/")

	if len(cfg.ResourcePrefixes) == 0 {
		cfg.ResourcePrefixes = []string{defaultResourcePrefix}
	}
	prefixes := make([]string, 0, len(cfg.ResourcePrefixes))
	for _, prefix := range cfg.ResourcePrefixes {
		if prefix = strings.Trim(prefix, "/"); prefix != "" {
			prefixes = append(prefixes, prefix)
		}
	}
	cfg.ResourcePrefixes = prefixes

	return &handler{fsys: fsys, cfg: cfg}
}

func (h *handler) handle(c *gin.Context) {
	key := lookupKey(c.Param(wildcardParam))
	switch {
	case key == ".":
		h.serveEntry(c)
	case !fs.ValidPath(key):
		h.refuse(c)
	default:
		h.serveLookup(c, key)
	}
}

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
		h.miss(c, key)
		return
	}
	h.serve(c, key, file, info)
}

func (h *handler) miss(c *gin.Context, key string) {
	if h.contentAddressed(key) || h.underResourcePrefix(key) || resourceShaped(key) {
		h.refuse(c)
		return
	}
	h.serveEntry(c)
}

func resourceShaped(key string) bool {
	return pathpkg.Ext(key) != ""
}

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

func (h *handler) serve(c *gin.Context, name string, file fs.File, info fs.FileInfo) {
	content, err := contents(file, info)
	if err != nil {
		h.fail(c, name, err)
		return
	}
	c.Header("Cache-Control", h.cacheControl(name))
	nethttp.ServeContent(c.Writer, c.Request, name, info.ModTime(), content)
}

func (h *handler) cacheControl(name string) string {
	if h.contentAddressed(name) {
		return longCache
	}
	return noCache
}

func (h *handler) contentAddressed(key string) bool {
	prefix := h.cfg.AssetPrefix
	if prefix == "" {
		return false
	}
	return key == prefix || strings.HasPrefix(key, prefix+"/")
}

func (h *handler) underResourcePrefix(key string) bool {
	for _, prefix := range h.cfg.ResourcePrefixes {
		if key == prefix || strings.HasPrefix(key, prefix+"/") {
			return true
		}
	}
	return false
}

func (h *handler) refuse(c *gin.Context) {
	speedhttp.Fail(c, speedhttp.Problem{Status: nethttp.StatusNotFound})
}

func (h *handler) fail(c *gin.Context, name string, err error) {
	speedhttp.Fail(c, speedhttp.Problem{
		Status: nethttp.StatusInternalServerError,
		Detail: fmt.Sprintf("the frontend bundle could not answer a read of %q", name),
		Cause:  err,
	})
}

// lookupKey cleans the catch-all value into a bundle-relative key; the root
// cleans to ".".
func lookupKey(param string) string {
	return pathpkg.Clean(strings.TrimPrefix(param, "/"))
}

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
