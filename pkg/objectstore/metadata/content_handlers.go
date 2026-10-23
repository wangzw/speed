package metadata

import (
	"context"
	"errors"
	"io"
	"io/fs"
	"mime"
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/vislake/speed/pkg/objectstore"
	"github.com/vislake/speed/pkg/tenant"
)

// serveRead answers GET and HEAD /content: it turns a token into the bytes of
// the completed object it grants.
//
// The order of the checks is the design's: a token of the wrong shape, a token
// no grant carries, a grant minted for the other direction, a grant the tenant
// roster will not enter, an object the tenant cannot see or that is not
// completed, and a backend that holds no bytes all answer 404; a grant past its
// expiry answers 410; a backend that cannot be reached answers 503. A read path
// says nothing about why an object is not readable, because the state machine
// is not the holder's business.
//
// The response carries the object's recorded media type, its declared name as an
// inline disposition when it has one, a private cache lifetime no longer than the
// grant's, and the headers that keep a browser from treating the bytes as
// anything but bytes.
func (p *product) serveRead(c *gin.Context) {
	token := c.Query(contentTokenParam)
	if !validTokenShape(token) {
		c.AbortWithStatus(http.StatusNotFound)
		return
	}
	grant, ctx, ok := p.granted(c, token, objectstore.URLAccessRead)
	if !ok {
		return
	}
	row, err := p.load(ctx, grant.ObjectID)
	if err != nil {
		if errors.Is(err, objectstore.ErrObjectNotFound) {
			c.AbortWithStatus(http.StatusNotFound)
			return
		}
		p.logger.Warn(ModuleName+": reading the object a content grant names failed", "error", err)
		c.AbortWithStatus(http.StatusServiceUnavailable)
		return
	}
	if objectstore.State(row.State) != objectstore.StateCompleted {
		c.AbortWithStatus(http.StatusNotFound)
		return
	}
	content, err := p.bytes.Open(ctx, row.Key)
	if err != nil {
		// The local-directory backend reports a missing file with the file
		// system's own not-exist error, and leaves telling it apart from an
		// unavailable backend to this module.
		if errors.Is(err, objectstore.ErrContentMissing) || errors.Is(err, fs.ErrNotExist) {
			c.AbortWithStatus(http.StatusNotFound)
			return
		}
		p.logger.Warn(ModuleName+": opening the bytes a content grant names failed", "error", err)
		c.AbortWithStatus(http.StatusServiceUnavailable)
		return
	}
	defer func() { _ = content.Close() }()

	p.setReadHeaders(c, row, grant)

	if seeker, ok := content.(io.ReadSeeker); ok {
		// ServeContent answers Content-Length, ranges and HEAD itself. The zero
		// modification time keeps it from writing or honouring a Last-Modified
		// the row does not have.
		http.ServeContent(c.Writer, c.Request, row.Name, time.Time{}, seeker)
		return
	}

	c.Header("Accept-Ranges", "none")
	c.Header("Content-Length", strconv.FormatInt(row.Size, 10))
	c.Status(http.StatusOK)
	if c.Request.Method == http.MethodHead {
		return
	}
	if _, err := io.Copy(c.Writer, content); err != nil {
		p.logger.Warn(ModuleName+": streaming an object's bytes failed", "object", row.ID, "error", err)
	}
}

// serveWrite answers PUT /content: it stores the request body as the bytes of
// the object the token grants a write to.
//
// The order is the design's: the token checks are the read path's; a completed
// object answers 409; a closed upload window 410; a request with no
// Content-Length 411; a length that differs from the declaration 400. The bytes
// then go through the same writer the in-process Upload uses. Every response
// carries Cache-Control: no-store, which is on the header before any check runs.
func (p *product) serveWrite(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	token := c.Query(contentTokenParam)
	if !validTokenShape(token) {
		c.AbortWithStatus(http.StatusNotFound)
		return
	}
	grant, ctx, ok := p.granted(c, token, objectstore.URLAccessWrite)
	if !ok {
		return
	}
	row, err := p.load(ctx, grant.ObjectID)
	if err != nil {
		if errors.Is(err, objectstore.ErrObjectNotFound) {
			c.AbortWithStatus(http.StatusNotFound)
			return
		}
		p.logger.Warn(ModuleName+": reading the object a content grant names failed", "error", err)
		c.AbortWithStatus(http.StatusServiceUnavailable)
		return
	}
	switch objectstore.State(row.State) {
	case objectstore.StateDeleted:
		c.AbortWithStatus(http.StatusNotFound)
		return
	case objectstore.StateCompleted:
		c.AbortWithStatus(http.StatusConflict)
		return
	}
	if row.UploadExpiresAt < time.Now().UnixMilli() {
		c.AbortWithStatus(http.StatusGone)
		return
	}
	size := c.Request.ContentLength
	if size < 0 {
		c.AbortWithStatus(http.StatusLengthRequired)
		return
	}
	if size != row.DeclaredSize {
		c.AbortWithStatus(http.StatusBadRequest)
		return
	}
	if err := p.storeUpload(ctx, row, c.Request.Body, size); err != nil {
		switch {
		case errors.Is(err, objectstore.ErrInvalidRequest):
			c.AbortWithStatus(http.StatusBadRequest)
		case errors.Is(err, objectstore.ErrStateConflict):
			c.AbortWithStatus(http.StatusConflict)
		default:
			p.logger.Warn(ModuleName+": storing the bytes a content grant names failed", "error", err)
			c.AbortWithStatus(http.StatusServiceUnavailable)
		}
		return
	}
	c.Status(http.StatusNoContent)
}

// granted resolves a token into the grant it names and the tenant context the
// object can be read in. It answers false when it has already written the
// refusal.
//
// The grant is read across tenants, because the request arrives with no tenant
// and the token itself names the one it was minted for. The tenant is then
// entered, so the object row is read under the ordinary scoped statement.
func (p *product) granted(c *gin.Context, token string, want objectstore.URLAccess) (*grantRow, context.Context, bool) {
	ctx := c.Request.Context()
	found, err := p.lookupGrant(tenant.CrossTenant(ctx), hashToken(token))
	if err != nil {
		p.logger.Warn(ModuleName+": reading a content grant failed", "error", err)
		c.AbortWithStatus(http.StatusServiceUnavailable)
		return nil, nil, false
	}
	if found == nil || found.Use != string(want) {
		c.AbortWithStatus(http.StatusNotFound)
		return nil, nil, false
	}
	if found.Live == 0 {
		c.AbortWithStatus(http.StatusGone)
		return nil, nil, false
	}
	entered, err := p.catalog.Enter(ctx, tenant.ID(found.TenantID))
	switch {
	case err == nil:
		return found, entered, true
	case errors.Is(err, tenant.ErrInvalidTenant), errors.Is(err, tenant.ErrTenantDisabled):
		c.AbortWithStatus(http.StatusNotFound)
		return nil, nil, false
	default:
		p.logger.Warn(ModuleName+": entering the tenant of a content grant failed", "error", err)
		c.AbortWithStatus(http.StatusServiceUnavailable)
		return nil, nil, false
	}
}

// setReadHeaders writes the response headers a byte read carries besides the
// bytes.
//
// Content-Disposition is built with mime.FormatMediaType, so an RFC 6266/5987
// filename* parameter is generated where one is needed and never assembled by
// hand. The cache lifetime is the grant's remaining seconds, so a browser keeps
// the bytes no longer than the address itself lives.
func (p *product) setReadHeaders(c *gin.Context, row *object, grant *grantRow) {
	c.Header("Content-Type", mimeOrDefault(row.MIME))
	if row.Name != "" {
		c.Header("Content-Disposition", mime.FormatMediaType("inline", map[string]string{"filename": row.Name}))
	}
	remaining := (grant.ExpiresAt - time.Now().UnixMilli()) / int64(time.Second/time.Millisecond)
	if remaining < 0 {
		remaining = 0
	}
	c.Header("Cache-Control", "private, max-age="+strconv.FormatInt(remaining, 10))
	c.Header("X-Content-Type-Options", "nosniff")
	c.Header("Content-Security-Policy", "sandbox")
	c.Header("Referrer-Policy", "no-referrer")
}
