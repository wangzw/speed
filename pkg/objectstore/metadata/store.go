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

// The states an object's row carries, as the strings the columns hold.
var (
	statePending   = string(objectstore.StatePending)
	stateUploaded  = string(objectstore.StateUploaded)
	stateCompleted = string(objectstore.StateCompleted)
	stateDeleted   = string(objectstore.StateDeleted)
)

// Create declares an object; see objectstore.Objects.
//
// It validates the declaration, normalizes the declared name, mints the object
// identifier and the internal key, and lands a pending row with the declared
// values and an upload window opened from now. The name is normalized here, the
// one place it is read for storage: every reader afterwards gets the stored
// value, and a name that is malformed, empty or reduced to nothing stores as
// the empty string rather than failing the declaration. A declaration that
// fails validation is refused and lands no row. The tenant column is filled
// from the context by the model's own filter, so a context carrying no tenant
// is refused by the insert.
//
// The upload window is read from the settings store here, at each declaration,
// so a value written while the process runs is what the next object records.
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

// Upload stores an object's bytes; see objectstore.Objects.
//
// The row must be pending or uploaded, its upload window must still be open, and
// the byte count the caller states must equal the declared size. Every
// precondition is checked before any byte is written: a request that fails one
// leaves no half-written byte and advances no state. The bytes are streamed to
// the byte seam through a reader that counts them and hashes them, and the row
// is then advanced to uploaded with the observed size, checksum and content type
// in one conditional statement.
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

// storeUpload streams bytes into the seam and advances the row to uploaded. It
// is the one write path the in-process Upload and the content route's PUT share,
// so the two record the same observed size, SHA-256 and MIME under the same
// rules.
//
// It computes the SHA-256 while it writes and compares it with a declared one
// when there is one: a body that hashes elsewhere is not the declared object, so
// its bytes are removed — best effort, the sweep would catch them otherwise —
// and the call is a request the module cannot act on. A byte-seam failure is
// ErrStoreUnavailable. The conditional advance covers a row that changed state
// while the bytes were being written.
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

// Complete moves an object to completed; see objectstore.Objects.
//
// A deleted row is answered as not found, the same as an unknown identifier: it
// is gone rather than in a state this call could move it out of. A completed row
// is returned unchanged, its expiry left as it was, and no byte is looked at:
// completion is idempotent. A pending or uploaded row is completed only when the
// backend really holds the declared number of bytes, which is what lets a
// direct-to-backend upload finish through this call.
//
// The bytes are observed with Stat, never read. A backend that holds none is
// ErrContentMissing, the bytes never having arrived. A byte count that differs
// from the declaration has its bytes removed — best effort, the sweep would catch
// them otherwise — and is a request the module cannot act on, with the row left
// in its state for a retry. An unreachable backend is ErrStoreUnavailable.
//
// The lifetime is read from the settings store here, at each completion, so a
// value written while the process runs is what the next completion records.
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

// Get returns an object's view; see objectstore.Objects. A deleted object is
// answered as not found, the same as an unknown identifier and another tenant's
// object, so the answer leaks nothing about existence.
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

// OpenContent returns a read-only stream over a completed object's bytes; see
// objectstore.Objects. A pending or uploaded object is a state conflict, a
// deleted one is not found, and a completed row whose bytes are not behind the
// seam is ErrContentMissing. The caller closes the stream.
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

// maxURLTTL is the longest lifetime either backend honours: 604800 seconds,
// the upper bound of an S3 SigV4 pre-signed address. The local-directory backend
// keeps the same bound so the two backends answer the same expiry semantics.
const maxURLTTL = 7 * 24 * time.Hour

// URL mints one direct-access address for an object's bytes; see
// objectstore.Objects.
//
// The address is either a pre-signed one minted by the backend that delivers
// DirectURL, or a token-carrying content address this process serves. The two
// differ in who holds the bytes: a pre-signed address carries the backend key, a
// content address carries an opaque token this module records in a grant row.
//
// Every path checks the same things. A read needs a completed object that has
// not passed its expiry; a write needs a pending or uploaded object whose upload
// window is still open. A remaining life or window under one second answers as
// though it had already passed. The lifetime is the configured url-ttl, may only
// be shortened by the caller, and is clamped again to the remaining life, the
// remaining window and the backend maximum. The caller's decision about which
// end user may hold the address is made before this call; nothing about the end
// user is checked here.
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

// List returns a page of this tenant's undeleted objects; see objectstore.Objects.
// The page is ordered by (created_at, id) and the cursor continues from the last
// row of the previous page; an empty cursor is the first page.
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

// Delete removes an object's bytes and tombstones its row; see objectstore.Objects.
//
// Any undeleted state has its bytes removed through the seam first, then its row
// conditionally tombstoned. A row already deleted matches no conditional update
// and is an idempotent success: a second delete removes no byte. A byte-seam
// failure is ErrStoreUnavailable and leaves the row as it was.
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

// load reads one row of the context's tenant, past the roster's filter. A row
// the tenant cannot see — an unknown identifier or another tenant's — is
// ErrObjectNotFound. A deleted row is returned: whether it reads as not found is
// each method's to decide.
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

// effectiveLimit settles the page size a list runs with: the default when none
// is named, the named value within bounds, and a refusal for one above the
// maximum so a caller is told rather than served a short page.
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

// validateDeclaration refuses a declaration whose shape cannot be read: a size
// that is not positive, a content type that is empty, or a checksum that is
// neither empty nor the 64 hex digits of a SHA-256.
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

// isSHA256Hex reports whether s is exactly 64 hexadecimal digits.
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

// hashingReader counts the bytes read through it and hashes them with SHA-256,
// so that one pass over the caller's stream gives both the observed size and the
// observed checksum. It reads the source once and holds nothing past the call.
type hashingReader struct {
	source io.Reader
	digest hash.Hash
	count  int64
}

// newHashingReader wraps a source reader.
func newHashingReader(source io.Reader) *hashingReader {
	return &hashingReader{source: source, digest: sha256.New()}
}

// Read reads from the source, feeding what it read into the hash and the count.
func (h *hashingReader) Read(b []byte) (int, error) {
	n, err := h.source.Read(b)
	if n > 0 {
		h.digest.Write(b[:n])
		h.count += int64(n)
	}
	return n, err
}

// sum reports the hex SHA-256 of the bytes read so far.
func (h *hashingReader) sum() string { return hex.EncodeToString(h.digest.Sum(nil)) }
