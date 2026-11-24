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
