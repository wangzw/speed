package metadata

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"

	speedhttp "github.com/vislake/speed/pkg/http"
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

// createObject answers POST /objects: it declares an object and returns its
// view. The entry point has already decoded the JSON body and refused a body
// member the declaration does not carry.
func (p *product) createObject(ctx context.Context, in CreateObjectInput) (objectCreatedBody, error) {
	obj, err := p.Create(ctx, objectstore.Declaration{
		Name:           in.Name,
		Size:           int64(in.Size),
		ContentType:    in.ContentType,
		ChecksumSHA256: in.ChecksumSHA256,
	})
	if err != nil {
		return objectCreatedBody{}, p.answer(ctx, "declaring an object", err)
	}
	return objectCreatedBody{Object: objectViewOf(obj)}, nil
}

// uploadContent answers PUT /objects/:id/content: it stores the request body as
// the object's bytes. The byte count it checks against is the request's
// Content-Length, which the entry point snapshots in Meta; a request that names
// no length cannot be checked and is a 400. A successful upload answers 204
// with no body.
func (p *product) uploadContent(ctx context.Context, in UploadObjectContentInput, meta *speedhttp.Meta) (speedhttp.Reply, error) {
	if meta.ContentLength < 0 {
		return nil, p.refusal(ctx, http.StatusBadRequest, badRequestCode)
	}
	if err := p.Upload(ctx, in.ID, bytes.NewReader(in.Body), meta.ContentLength); err != nil {
		return nil, p.answer(ctx, "uploading an object's bytes", err)
	}
	return speedhttp.EmptyReply(http.StatusNoContent), nil
}

// completeObject answers POST /objects/:id/complete: it moves an object to
// completed and returns its view.
func (p *product) completeObject(ctx context.Context, in CompleteObjectInput) (objectCompletedBody, error) {
	obj, err := p.Complete(ctx, in.ID)
	if err != nil {
		return objectCompletedBody{}, p.answer(ctx, "completing an object", err)
	}
	return objectCompletedBody{Object: objectViewOf(obj)}, nil
}

// getObject answers GET /objects/:id: one object's view.
func (p *product) getObject(ctx context.Context, in GetObjectInput) (objectReadBody, error) {
	obj, err := p.Get(ctx, in.ID)
	if err != nil {
		return objectReadBody{}, p.answer(ctx, "reading an object", err)
	}
	return objectReadBody{Object: objectViewOf(obj)}, nil
}

// listObjects answers GET /objects: one page of this tenant's undeleted objects.
// The cursor continues from the last row of the previous page and the limit caps
// the page; the cursor of the page's last row is returned for the next call.
func (p *product) listObjects(ctx context.Context, in ListObjectsInput) (objectPageBody, error) {
	q := objectstore.ListQuery{Cursor: in.Cursor, Limit: in.Limit}
	objs, err := p.List(ctx, q)
	if err != nil {
		return objectPageBody{}, p.answer(ctx, "listing the objects", err)
	}
	views := make([]ObjectstoreObject, len(objs))
	for i, o := range objs {
		views[i] = objectViewOf(o)
	}
	next := ""
	if limit, err := effectiveLimit(q.Limit); err == nil && len(objs) == limit && len(objs) > 0 {
		last := objs[len(objs)-1]
		next = encodeCursor(last.CreatedAt.UnixMilli(), last.ID)
	}
	return objectPageBody{Objects: views, NextCursor: next}, nil
}

// getContent answers GET /objects/:id/content: the object's bytes, streamed with
// the media type the object recorded. The view carries the MIME the Content-Type
// is taken from; OpenContent carries the bytes. A failure that happens after the
// status and headers are on the wire cannot be answered, so it is logged and the
// connection is abandoned by the entry point.
func (p *product) getContent(ctx context.Context, in GetObjectContentInput, _ *speedhttp.Meta) (speedhttp.Reply, error) {
	obj, err := p.Get(ctx, in.ID)
	if err != nil {
		return nil, p.answer(ctx, "reading an object", err)
	}
	content, err := p.OpenContent(ctx, in.ID)
	if err != nil {
		return nil, p.answer(ctx, "opening an object's bytes", err)
	}
	mediaType := obj.MIME
	if mediaType == "" {
		mediaType = fallbackContentType
	}
	return speedhttp.StreamReply(http.StatusOK, mediaType, func(_ context.Context, w io.Writer) error {
		defer func() { _ = content.Close() }()
		if _, copyErr := io.Copy(w, content); copyErr != nil {
			p.logger.Error(ModuleName+": streaming an object's bytes failed",
				"object", in.ID, "cause", copyErr.Error())
			return copyErr
		}
		return nil
	}), nil
}

// deleteObject answers DELETE /objects/:id: it removes an object's bytes and
// tombstones its row. It is idempotent on an object already deleted and answers
// 204 with no body.
func (p *product) deleteObject(ctx context.Context, in DeleteObjectInput) (struct{}, error) {
	if err := p.Delete(ctx, in.ID); err != nil {
		return struct{}{}, p.answer(ctx, "deleting an object", err)
	}
	return struct{}{}, nil
}

// answer maps a failure an Objects method returned to the fault it belongs to.
// The mapping is closed and fail-closed: a request whose shape cannot be read is
// a 400, an object this tenant cannot see is a 404, a state that does not allow
// the action is a 409, and every other failure — an unavailable byte seam,
// missing content, anything unnamed — is a 503 whose cause stays in the log and
// never reaches the caller.
func (p *product) answer(ctx context.Context, op string, err error) speedhttp.Fault {
	switch {
	case errors.Is(err, objectstore.ErrInvalidRequest):
		return p.refusal(ctx, http.StatusBadRequest, badRequestCode)
	case errors.Is(err, objectstore.ErrObjectNotFound):
		return p.refusal(ctx, http.StatusNotFound, notFoundCode)
	case errors.Is(err, objectstore.ErrStateConflict):
		return p.refusal(ctx, http.StatusConflict, conflictCode)
	default:
		p.logger.Error(ModuleName+": "+op+" failed", "cause", err.Error())
		return p.refusal(ctx, http.StatusServiceUnavailable, "")
	}
}
