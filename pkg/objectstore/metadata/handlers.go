package metadata

import (
	"errors"
	"io"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"

	"github.com/vislake/speed/pkg/objectstore"
)

// The problem types this module writes. typePrefix is the capability's
// namespace; the three codes are this surface's kinds within it. A 404 and a 503
// carry no type of their own beyond what the design fixes, and every refusal is
// rendered from this module's own message catalog.
const (
	typePrefix     = "urn:speed:objectstore:"
	badRequestCode = "bad-request"
	notFoundCode   = "not-found"
	conflictCode   = "conflict"
)

// createBody is the request of a declaration: the fields of a Declaration.
type createBody struct {
	Name           string `json:"name"`
	Size           int64  `json:"size"`
	ContentType    string `json:"content_type"`
	ChecksumSHA256 string `json:"checksum_sha256"`
}

// handleCreate answers POST /api/v1/objects: it declares an object and returns
// its view. A body that is not a JSON object, or a declaration whose shape is
// not readable, is a 400.
func (p *product) handleCreate(c *gin.Context) {
	var body createBody
	if err := c.ShouldBindJSON(&body); err != nil {
		p.refuse(c, http.StatusBadRequest, badRequestCode)
		return
	}
	obj, err := p.Create(c.Request.Context(), objectstore.Declaration{
		Name:           body.Name,
		Size:           body.Size,
		ContentType:    body.ContentType,
		ChecksumSHA256: body.ChecksumSHA256,
	})
	if err != nil {
		p.answer(c, "declaring an object", err)
		return
	}
	c.JSON(http.StatusCreated, gin.H{"object": objectViewOf(obj)})
}

// handleUpload answers PUT /api/v1/objects/:id/content: it stores the request
// body as the object's bytes. The declared size it checks against is the
// request's own content length; a request that carries none cannot be checked
// and is a 400. A successful upload answers 204 with no body.
func (p *product) handleUpload(c *gin.Context) {
	size := c.Request.ContentLength
	if size < 0 {
		p.refuse(c, http.StatusBadRequest, badRequestCode)
		return
	}
	if err := p.Upload(c.Request.Context(), c.Param("id"), c.Request.Body, size); err != nil {
		p.answer(c, "uploading an object's bytes", err)
		return
	}
	p.noContent(c)
}

// handleComplete answers POST /api/v1/objects/:id/complete: it moves an object to
// completed and returns its view.
func (p *product) handleComplete(c *gin.Context) {
	obj, err := p.Complete(c.Request.Context(), c.Param("id"))
	if err != nil {
		p.answer(c, "completing an object", err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"object": objectViewOf(obj)})
}

// handleGet answers GET /api/v1/objects/:id: one object's view.
func (p *product) handleGet(c *gin.Context) {
	obj, err := p.Get(c.Request.Context(), c.Param("id"))
	if err != nil {
		p.answer(c, "reading an object", err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"object": objectViewOf(obj)})
}

// handleList answers GET /api/v1/objects: one page of this tenant's undeleted
// objects. The cursor continues from the last row of the previous page and the
// limit caps the page; the cursor of the page's last row is returned for the
// next call.
func (p *product) handleList(c *gin.Context) {
	q := objectstore.ListQuery{Cursor: c.Query("cursor")}
	if s := c.Query("limit"); s != "" {
		n, err := strconv.Atoi(s)
		if err != nil {
			p.refuse(c, http.StatusBadRequest, badRequestCode)
			return
		}
		q.Limit = n
	}
	objs, err := p.List(c.Request.Context(), q)
	if err != nil {
		p.answer(c, "listing the objects", err)
		return
	}
	views := make([]objectView, len(objs))
	for i, o := range objs {
		views[i] = objectViewOf(o)
	}
	next := ""
	if limit, err := effectiveLimit(q.Limit); err == nil && len(objs) == limit && len(objs) > 0 {
		last := objs[len(objs)-1]
		next = encodeCursor(last.CreatedAt.UnixMilli(), last.ID)
	}
	c.JSON(http.StatusOK, gin.H{"objects": views, "next_cursor": next})
}

// handleContent answers GET /api/v1/objects/:id/content: the object's bytes, with
// the content type taken from its recorded MIME. The object must be completed. A
// failure that happens after the status and headers are on the wire cannot be
// answered, so it is logged.
func (p *product) handleContent(c *gin.Context) {
	ctx := c.Request.Context()
	id := c.Param("id")
	// The view carries the MIME the Content-Type is taken from; OpenContent
	// carries the bytes. The two are read together because the seam returns the
	// bytes alone.
	obj, err := p.Get(ctx, id)
	if err != nil {
		p.answer(c, "reading an object", err)
		return
	}
	content, err := p.OpenContent(ctx, id)
	if err != nil {
		p.answer(c, "opening an object's bytes", err)
		return
	}
	defer func() { _ = content.Close() }()
	mime := obj.MIME
	if mime == "" {
		mime = "application/octet-stream"
	}
	c.Header("Content-Type", mime)
	c.Status(http.StatusOK)
	if _, err := io.Copy(c.Writer, content); err != nil {
		p.logger.Error(ModuleName+": streaming an object's bytes failed", "object", id, "cause", err.Error())
	}
}

// handleDelete answers DELETE /api/v1/objects/:id: it removes an object's bytes
// and tombstones its row. It is idempotent on an object already deleted and
// answers 204 with no body.
func (p *product) handleDelete(c *gin.Context) {
	if err := p.Delete(c.Request.Context(), c.Param("id")); err != nil {
		p.answer(c, "deleting an object", err)
		return
	}
	p.noContent(c)
}

// answer maps a failure an Objects method returned to the response it belongs
// to. The mapping is closed and fail-closed: a request whose shape cannot be
// read is a 400, an object this tenant cannot see is a 404, a state that does
// not allow the action is a 409, and every other failure — an unavailable byte
// seam, missing content, anything unnamed — is a 503 whose cause stays in the
// log and never reaches the caller.
func (p *product) answer(c *gin.Context, op string, err error) {
	switch {
	case errors.Is(err, objectstore.ErrInvalidRequest):
		p.refuse(c, http.StatusBadRequest, badRequestCode)
	case errors.Is(err, objectstore.ErrObjectNotFound):
		p.refuse(c, http.StatusNotFound, notFoundCode)
	case errors.Is(err, objectstore.ErrStateConflict):
		p.refuse(c, http.StatusConflict, conflictCode)
	default:
		p.logger.Error(ModuleName+": "+op+" failed", "cause", err.Error())
		p.refuse(c, http.StatusServiceUnavailable, "")
	}
}

// noContent answers a write that landed: 204 with no body.
func (p *product) noContent(c *gin.Context) {
	c.Status(http.StatusNoContent)
	c.Writer.WriteHeaderNow()
}
