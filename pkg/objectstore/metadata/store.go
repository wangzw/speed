package metadata

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"hash"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"

	"github.com/vislake/speed/pkg/objectstore"
)

var (
	statePending   = string(objectstore.StatePending)
	stateUploaded  = string(objectstore.StateUploaded)
	stateCompleted = string(objectstore.StateCompleted)
	stateDeleted   = string(objectstore.StateDeleted)
)

func (p *product) Create(ctx context.Context, d objectstore.Declaration) (objectstore.Object, error) {
	if err := validateDeclaration(d); err != nil {
		return objectstore.Object{}, err
	}
	id := uuid.NewString()
	key, err := internalKey()
	if err != nil {
		return objectstore.Object{}, err
	}
	now := time.Now().UnixMilli()
	row := object{
		ID:               id,
		Key:              key,
		Name:             normalizeName(d.Name),
		State:            statePending,
		DeclaredSize:     d.Size,
		DeclaredType:     d.ContentType,
		DeclaredChecksum: d.ChecksumSHA256,
		UploadExpiresAt:  now + p.tuned.uploadWindow(p.logger).Milliseconds(),
		CreatedAt:        now,
		UpdatedAt:        now,
	}
	if err := p.handle.WithContext(ctx).Create(&row).Error; err != nil {
		return objectstore.Object{}, fmt.Errorf("%s: writing the pending object %s: %w", ModuleName, id, err)
	}
	return row.view(), nil
}

func (p *product) Upload(ctx context.Context, id string, r io.Reader, size int64) error {
	row, err := p.load(ctx, id)
	if err != nil {
		return err
	}
	switch objectstore.State(row.State) {
	case objectstore.StateDeleted:
		return fmt.Errorf("%w: the object %s is deleted", objectstore.ErrObjectNotFound, id)
	case objectstore.StateCompleted:
		return fmt.Errorf("%w: the object %s is already completed", objectstore.ErrStateConflict, id)
	}
	now := time.Now().UnixMilli()
	if row.UploadExpiresAt < now {
		return fmt.Errorf("%w: the upload window for the object %s has closed", objectstore.ErrStateConflict, id)
	}
	if size != row.DeclaredSize {
		return fmt.Errorf("%w: the upload carries %d bytes and the object %s declared %d",
			objectstore.ErrInvalidRequest, size, id, row.DeclaredSize)
	}
	return p.storeUpload(ctx, row, r, size)
}

func (p *product) storeUpload(ctx context.Context, row *object, source io.Reader, size int64) error {
	observed := newHashingReader(source)
	if err := p.bytes.Put(ctx, row.Key, observed, size); err != nil {
		return fmt.Errorf("%w: storing the object %s's bytes: %w", objectstore.ErrStoreUnavailable, row.ID, err)
	}
	if row.DeclaredChecksum != "" && !strings.EqualFold(row.DeclaredChecksum, observed.sum()) {
		if delErr := p.bytes.Delete(ctx, row.Key); delErr != nil {
			p.logger.Warn(ModuleName+": the bytes of an object whose declared checksum did not match could "+
				"not be removed; the sweep will try again", "object", row.ID, "error", delErr)
		}
		return fmt.Errorf("%w: the object %s's bytes do not match its declared checksum",
			objectstore.ErrInvalidRequest, row.ID)
	}
	advanced := p.handle.WithContext(ctx).Model(&object{}).
		Where("id = ? AND state IN ?", row.ID, []string{statePending, stateUploaded}).
		Updates(map[string]any{
			"state":           stateUploaded,
			"size":            observed.count,
			"checksum_sha256": observed.sum(),
			"mime":            row.DeclaredType,
			"updated_at":      time.Now().UnixMilli(),
		})
	if advanced.Error != nil {
		return fmt.Errorf("%s: advancing the object %s to uploaded: %w", ModuleName, row.ID, advanced.Error)
	}
	if advanced.RowsAffected == 0 {
		return fmt.Errorf("%w: the object %s changed state while its bytes were being stored",
			objectstore.ErrStateConflict, row.ID)
	}
	return nil
}

func (p *product) Complete(ctx context.Context, id string) (objectstore.Object, error) {
	row, err := p.load(ctx, id)
	if err != nil {
		return objectstore.Object{}, err
	}
	switch objectstore.State(row.State) {
	case objectstore.StateDeleted:
		return objectstore.Object{}, fmt.Errorf("%w: the object %s is deleted", objectstore.ErrObjectNotFound, id)
	case objectstore.StateCompleted:
		return row.view(), nil
	}

	blob, err := p.bytes.Stat(ctx, row.Key)
	switch {
	case errors.Is(err, objectstore.ErrContentMissing):
		return objectstore.Object{}, fmt.Errorf("%w: the object %s was never uploaded",
			objectstore.ErrContentMissing, id)
	case err != nil:
		return objectstore.Object{}, fmt.Errorf("%w: observing the object %s's bytes: %w",
			objectstore.ErrStoreUnavailable, id, err)
	case blob.Size != row.DeclaredSize:
		if delErr := p.bytes.Delete(ctx, row.Key); delErr != nil {
			p.logger.Warn(ModuleName+": the bytes of an object whose size did not match its declaration "+
				"could not be removed; the sweep will try again", "object", id, "error", delErr)
		}
		return objectstore.Object{}, fmt.Errorf("%w: the backend holds %d bytes for the object %s and the "+
			"declaration named %d", objectstore.ErrInvalidRequest, blob.Size, id, row.DeclaredSize)
	}

	now := time.Now().UnixMilli()
	var expires *int64
	if ttl := p.tuned.objectTTL(p.logger); ttl > 0 {
		at := now + ttl.Milliseconds()
		expires = &at
	}
	advanced := p.handle.WithContext(ctx).Model(&object{}).
		Where("id = ? AND state IN ?", id, []string{statePending, stateUploaded}).
		Updates(map[string]any{
			"state":      stateCompleted,
			"size":       blob.Size,
			"mime":       row.DeclaredType,
			"expires_at": expires,
			"updated_at": now,
		})
	if advanced.Error != nil {
		return objectstore.Object{}, fmt.Errorf("%s: completing the object %s: %w", ModuleName, id, advanced.Error)
	}
	if advanced.RowsAffected == 0 {
		return objectstore.Object{}, fmt.Errorf("%w: the object %s changed state while it was being completed",
			objectstore.ErrStateConflict, id)
	}
	fresh, err := p.load(ctx, id)
	if err != nil {
		return objectstore.Object{}, err
	}
	return fresh.view(), nil
}

func (p *product) Get(ctx context.Context, id string) (objectstore.Object, error) {
	row, err := p.load(ctx, id)
	if err != nil {
		return objectstore.Object{}, err
	}
	if objectstore.State(row.State) == objectstore.StateDeleted {
		return objectstore.Object{}, fmt.Errorf("%w: %s", objectstore.ErrObjectNotFound, id)
	}
	return row.view(), nil
}

func (p *product) OpenContent(ctx context.Context, id string) (io.ReadCloser, error) {
	row, err := p.load(ctx, id)
	if err != nil {
		return nil, err
	}
	switch objectstore.State(row.State) {
	case objectstore.StateDeleted:
		return nil, fmt.Errorf("%w: %s", objectstore.ErrObjectNotFound, id)
	case objectstore.StatePending, objectstore.StateUploaded:
		return nil, fmt.Errorf("%w: the object %s is not completed", objectstore.ErrStateConflict, id)
	}
	content, err := p.bytes.Open(ctx, row.Key)
	if err != nil {
		return nil, fmt.Errorf("%w: opening the bytes of the completed object %s: %w",
			objectstore.ErrContentMissing, id, err)
	}
	return content, nil
}

const maxURLTTL = 7 * 24 * time.Hour

func (p *product) URL(ctx context.Context, id string, req objectstore.URLRequest) (objectstore.IssuedURL, error) {
	switch req.Access {
	case objectstore.URLAccessRead, objectstore.URLAccessWrite:
	default:
		return objectstore.IssuedURL{}, fmt.Errorf("%w: the URL access %q is neither read nor write",
			objectstore.ErrInvalidRequest, req.Access)
	}
	if req.Expiry < 0 {
		return objectstore.IssuedURL{}, fmt.Errorf("%w: the URL lifetime %s is negative",
			objectstore.ErrInvalidRequest, req.Expiry)
	}
	row, err := p.load(ctx, id)
	if err != nil {
		return objectstore.IssuedURL{}, err
	}
	if objectstore.State(row.State) == objectstore.StateDeleted {
		return objectstore.IssuedURL{}, fmt.Errorf("%w: %s", objectstore.ErrObjectNotFound, id)
	}

	now := time.Now()
	ttl := p.tuned.urlTTL(p.logger)
	if req.Expiry > 0 && req.Expiry < ttl {
		ttl = req.Expiry
	}

	var method string
	switch req.Access {
	case objectstore.URLAccessRead:
		if objectstore.State(row.State) != objectstore.StateCompleted {
			return objectstore.IssuedURL{}, fmt.Errorf("%w: the object %s is not completed",
				objectstore.ErrStateConflict, id)
		}
		if row.ExpiresAt != nil {
			remaining := time.UnixMilli(*row.ExpiresAt).Sub(now)
			if remaining < time.Second {
				return objectstore.IssuedURL{}, fmt.Errorf("%w: %s", objectstore.ErrObjectNotFound, id)
			}
			ttl = min(ttl, remaining)
		}
		method = http.MethodGet
	case objectstore.URLAccessWrite:
		if state := objectstore.State(row.State); state != objectstore.StatePending && state != objectstore.StateUploaded {
			return objectstore.IssuedURL{}, fmt.Errorf("%w: the object %s is not awaiting an upload",
				objectstore.ErrStateConflict, id)
		}
		remaining := time.UnixMilli(row.UploadExpiresAt).Sub(now)
		if remaining < time.Second {
			return objectstore.IssuedURL{}, fmt.Errorf("%w: the upload window of the object %s has closed",
				objectstore.ErrStateConflict, id)
		}
		ttl = min(ttl, remaining)
		method = http.MethodPut
	}
	ttl = min(ttl, maxURLTTL)
	expires := now.Add(ttl)

	if p.direct != nil {
		params := objectstore.PresignParams{TTL: ttl}
		var issued objectstore.IssuedURL
		var signErr error
		if req.Access == objectstore.URLAccessRead {
			params.ContentType = mimeOrDefault(row.MIME)
			params.Filename = row.Name
			issued, signErr = p.direct.PresignRead(ctx, row.Key, params)
		} else {
			issued, signErr = p.direct.PresignWrite(ctx, row.Key, params)
		}
		if signErr != nil {
			return objectstore.IssuedURL{}, fmt.Errorf("%w: signing the address of the object %s: %w",
				objectstore.ErrURLUnavailable, id, signErr)
		}
		return issued, nil
	}

	if !p.contentMounted {
		return objectstore.IssuedURL{}, fmt.Errorf("%w: no backend delivers direct addresses and this "+
			"process serves no content route", objectstore.ErrURLUnavailable)
	}
	token, hash, err := mintToken()
	if err != nil {
		return objectstore.IssuedURL{}, err
	}
	if err := p.insertGrant(ctx, row, req.Access, hash, expires.UnixMilli()); err != nil {
		return objectstore.IssuedURL{}, err
	}
	return objectstore.IssuedURL{URL: p.contentURL(token), Method: method, Expires: expires}, nil
}

func (p *product) List(ctx context.Context, q objectstore.ListQuery) ([]objectstore.Object, error) {
	limit, err := effectiveLimit(q.Limit)
	if err != nil {
		return nil, err
	}
	query := p.handle.WithContext(ctx).Model(&object{}).Where("state <> ?", stateDeleted)
	if q.Cursor != "" {
		at, id, err := decodeCursor(q.Cursor)
		if err != nil {
			return nil, err
		}
		query = query.Where("created_at > ? OR (created_at = ? AND id > ?)", at, at, id)
	}
	var rows []object
	if err := query.Order("created_at ASC").Order("id ASC").Limit(limit).Find(&rows).Error; err != nil {
		return nil, fmt.Errorf("%s: listing the objects: %w", ModuleName, err)
	}
	out := make([]objectstore.Object, len(rows))
	for i := range rows {
		out[i] = rows[i].view()
	}
	return out, nil
}

func (p *product) Delete(ctx context.Context, id string) error {
	row, err := p.load(ctx, id)
	if err != nil {
		return err
	}
	if objectstore.State(row.State) == objectstore.StateDeleted {
		return nil
	}
	if err := p.bytes.Delete(ctx, row.Key); err != nil {
		return fmt.Errorf("%w: deleting the object %s's bytes: %w", objectstore.ErrStoreUnavailable, id, err)
	}
	buried := p.handle.WithContext(ctx).Model(&object{}).
		Where("id = ? AND state <> ?", id, stateDeleted).
		Updates(map[string]any{
			"state":      stateDeleted,
			"updated_at": time.Now().UnixMilli(),
		})
	if buried.Error != nil {
		return fmt.Errorf("%s: tombstoning the object %s: %w", ModuleName, id, buried.Error)
	}
	return nil
}

func (p *product) load(ctx context.Context, id string) (*object, error) {
	var row object
	err := p.handle.WithContext(ctx).First(&row, "id = ?", id).Error
	switch {
	case errors.Is(err, gorm.ErrRecordNotFound):
		return nil, fmt.Errorf("%w: %s", objectstore.ErrObjectNotFound, id)
	case err != nil:
		return nil, fmt.Errorf("%s: reading the object %s: %w", ModuleName, id, err)
	}
	return &row, nil
}

func effectiveLimit(n int) (int, error) {
	switch {
	case n == 0:
		return defaultListLimit, nil
	case n < 0:
		return 0, fmt.Errorf("%w: the list limit %d is negative", objectstore.ErrInvalidRequest, n)
	case n > maxListLimit:
		return 0, fmt.Errorf("%w: the list limit %d is above the maximum %d",
			objectstore.ErrInvalidRequest, n, maxListLimit)
	}
	return n, nil
}

func validateDeclaration(d objectstore.Declaration) error {
	switch {
	case d.Size <= 0:
		return fmt.Errorf("%w: the declared size is %d, and an object's size has to be positive",
			objectstore.ErrInvalidRequest, d.Size)
	case strings.TrimSpace(d.ContentType) == "":
		return fmt.Errorf("%w: the declaration names no content type", objectstore.ErrInvalidRequest)
	case d.ChecksumSHA256 != "" && !isSHA256Hex(d.ChecksumSHA256):
		return fmt.Errorf("%w: the declared checksum is neither empty nor 64 hex digits",
			objectstore.ErrInvalidRequest)
	}
	return nil
}

func isSHA256Hex(s string) bool {
	if len(s) != sha256.Size*2 {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case '0' <= c && c <= '9', 'a' <= c && c <= 'f', 'A' <= c && c <= 'F':
		default:
			return false
		}
	}
	return true
}

type hashingReader struct {
	source io.Reader
	digest hash.Hash
	count  int64
}

func newHashingReader(source io.Reader) *hashingReader {
	return &hashingReader{source: source, digest: sha256.New()}
}

func (h *hashingReader) Read(b []byte) (int, error) {
	n, err := h.source.Read(b)
	if n > 0 {
		h.digest.Write(b[:n])
		h.count += int64(n)
	}
	return n, err
}

func (h *hashingReader) sum() string { return hex.EncodeToString(h.digest.Sum(nil)) }
