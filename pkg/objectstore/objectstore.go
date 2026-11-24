// Package objectstore stores an object's bytes and carries its metadata through a lifecycle.
// It is the interface package: it declares the seams, common types, audit action names and sentinels, and registers no module.
package objectstore

import (
	"context"
	"errors"
	"io"
	"time"
)

// Store is the byte seam: an object's bytes move through it.
type Store interface {
	// Name reports the implementation's name, for diagnostics.
	Name() string
	// Put stores size bytes read from r under key.
	// The reader is read once; an implementation does not hold onto it past the call.
	Put(ctx context.Context, key string, r io.Reader, size int64) error
	// Open returns a read-only stream over the bytes stored under key.
	// The caller closes it.
	Open(ctx context.Context, key string) (io.ReadCloser, error)
	// Stat reports what the backend holds under key without reading its body.
	// A key the backend holds no bytes under reports an error wrapping ErrContentMissing; any other
	// failure keeps the backend's own error.
	Stat(ctx context.Context, key string) (Blob, error)
	// Delete removes the bytes stored under key.
	// It is idempotent: deleting bytes that are not there is not an error.
	Delete(ctx context.Context, key string) error
}

// Blob is what a backend observes about the bytes stored under a key, without reading them.
type Blob struct {
	// Size is the number of bytes the backend holds.
	Size int64
}

// DirectURL is the capability a backend delivers when a browser can reach its bytes without this
// process.
type DirectURL interface {
	// PresignRead answers an address that serves the bytes under key with the given media type and
	// file name.
	// No network call is made: the address is signed locally.
	PresignRead(ctx context.Context, key string, p PresignParams) (IssuedURL, error)
	// PresignWrite answers an address a client can PUT the bytes under key to.
	PresignWrite(ctx context.Context, key string, p PresignParams) (IssuedURL, error)
}

// PresignParams is what a direct address is bound to.
// ContentType and Filename apply to a read address only.
type PresignParams struct {
	// TTL is the lifetime asked for.
	// A backend caps it at the maximum it honours and reports the granted instant in
	// IssuedURL.Expires.
	TTL time.Duration
	// ContentType is the media type a read address serves the bytes as.
	ContentType string
	// Filename is the file name a read address offers the bytes under.
	Filename string
}

// State is where an object stands in its lifecycle.
// The set is closed: an object is in exactly one of these states.
type State string

const (
	// StatePending is an object that has been declared and whose upload window has not yet closed.
	StatePending State = "pending"
	// StateUploaded is an object whose bytes have arrived and that is waiting to be completed.
	StateUploaded State = "uploaded"
	// StateCompleted is an object that can be read.
	StateCompleted State = "completed"
	// StateDeleted is an object whose metadata row is kept as a tombstone and whose bytes have been
	// removed from the backend.
	StateDeleted State = "deleted"
)

// Object is one object's view, as the metadata seam hands it out.
type Object struct {
	// ID is the object identifier a caller addresses it by.
	ID string
	// Key is the internal key the bytes are stored under.
	// It is module bookkeeping: it reaches no API response, no error text and no log line.
	Key string
	// Name is the label the caller declared, normalized once when the object was created.
	// It is empty when no name was declared or the declared name normalized to nothing.
	// It identifies nothing: it is not unique and no lookup uses it.
	Name string
	// State is where the object stands in its lifecycle.
	State State
	// Size is the number of bytes observed at upload.
	// It is zero before an upload.
	Size int64
	// MIME is the content type recorded at upload, taken from the declaration.
	MIME string
	// Checksum is the SHA-256 recorded at upload, computed from the bytes.
	// It is empty before an upload, and stays empty for an object completed from a direct-to-S3
	// upload, whose bytes never pass through this process.
	Checksum string
	// ExpiresAt is when the object expires, set at completion.
	// It is nil when the object has no expiry.
	ExpiresAt *time.Time
	// CreatedAt is when the object was declared.
	CreatedAt time.Time
	// UpdatedAt is when the object's row last changed.
	UpdatedAt time.Time
}

// URLAccess is what a minted URL lets its holder do.
type URLAccess string

const (
	// URLAccessRead is a URL that serves an object's bytes.
	URLAccessRead URLAccess = "read"
	// URLAccessWrite is a URL a client may PUT an object's bytes to.
	URLAccessWrite URLAccess = "write"
)

// URLRequest is what a caller states when it asks for a URL.
type URLRequest struct {
	// Access is which way the URL moves bytes.
	Access URLAccess
	// Expiry is how long the caller wants the URL to live.
	// Zero takes the configured lifetime (objectstore.metadata.url-ttl).
	Expiry time.Duration
}

// IssuedURL is a direct-access address and the terms it was granted under.
// It is a bearer capability until it expires: holding it is enough to use it, and it dies only by
// expiring.
type IssuedURL struct {
	// URL is the address to hand to the client.
	URL string
	// Method is the HTTP method the holder uses: "GET" for a read URL, "PUT" for a write URL.
	Method string
	// Expires is the instant the address stops working.
	Expires time.Time
}

// Objects is the metadata and lifecycle seam, delivered exclusively by the metadata module.
type Objects interface {
	// Create validates the declaration, stores a pending object and returns it.
	Create(ctx context.Context, d Declaration) (Object, error)
	// Upload stores the object's bytes and moves it to uploaded.
	Upload(ctx context.Context, id string, r io.Reader, size int64) error
	// Complete checks the stored bytes and moves the object to completed.
	Complete(ctx context.Context, id string) (Object, error)
	// Get returns the object's current row.
	Get(ctx context.Context, id string) (Object, error)
	// OpenContent returns a reader over a completed object's bytes.
	OpenContent(ctx context.Context, id string) (io.ReadCloser, error)
	// URL mints one direct-access address for this object's bytes.
	// URLAccessRead requires a completed object that is not past its expiry.
	// URLAccessWrite requires a pending or uploaded object whose upload window is still open.
	URL(ctx context.Context, id string, req URLRequest) (IssuedURL, error)
	// List returns this tenant's undeleted objects in creation order.
	List(ctx context.Context, q ListQuery) ([]Object, error)
	// Delete removes the bytes and tombstones the object.
	Delete(ctx context.Context, id string) error
}

// Declaration is what a caller states about an object before any byte arrives.
type Declaration struct {
	// Name is the caller's own label for the object.
	// It is optional and is normalized once before it is stored; it never fails Create.
	// The rules are the delivering module's, and the object view carries the stored value.
	Name string
	// Size is the declared number of bytes.
	// An upload whose byte count differs from this is refused.
	Size int64
	// ContentType is the declared content type, recorded as the object's MIME at upload.
	ContentType string
	// ChecksumSHA256 is the declared SHA-256, as 64 hex digits.
	// It is optional: an empty value declares no checksum.
	ChecksumSHA256 string
}

// ListQuery narrows and pages a List.
type ListQuery struct {
	// Limit caps the rows returned.
	// The metadata module applies a default and an upper bound.
	Limit int
	// Cursor is the keyset cursor: the encoding of the previous page's last (created_at, id).
	// An empty cursor is the first page.
	// It is opaque to a caller; its encoding is this capability's own convention.
	Cursor string
}

const (
	// EventObjectCreated names the point at which an object has landed a pending row.
	EventObjectCreated = "objectstore.object.created"
	// EventObjectCompleted names the point at which an object has become readable.
	EventObjectCompleted = "objectstore.object.completed"
	// EventObjectDeleted names the point at which an object has become unrecoverable.
	EventObjectDeleted = "objectstore.object.deleted"
)

var (
	// ErrObjectNotFound reports an object this tenant cannot see: an unknown identifier, another
	// tenant's object and a deleted object all take this one shape, so the answer leaks nothing about
	// existence.
	ErrObjectNotFound = errors.New("objectstore: the object is not visible to this tenant")
	// ErrStateConflict reports an action the object's current state does not allow: completing an
	// object that was never uploaded, reading one that was never completed, or uploading past the
	// upload window.
	ErrStateConflict = errors.New("objectstore: the object's state does not allow this action")
	// ErrContentMissing reports a backend that holds no bytes for the object: a row that says
	// completed while the backend holds none, and the bytes never having arrived when Complete or Stat
	// looks for them.
	ErrContentMissing = errors.New("objectstore: the object's bytes are not in the backend")
	// ErrStoreUnavailable reports a byte seam that is absent or a backend that is unreachable.
	// A byte operation that fails keeps the backend's own error wrapped inside it.
	ErrStoreUnavailable = errors.New("objectstore: the byte store is unavailable")
	// ErrInvalidConfig reports a configuration the module cannot run on: an input item is missing, a
	// value is unusable, or the module contradicts its own nature.
	ErrInvalidConfig = errors.New("objectstore: the configuration holds a value the module cannot run on")
	// ErrInvalidRequest reports a request whose shape cannot be read: a Declaration whose shape is
	// illegal, or an upload whose byte count differs from the declared size.
	ErrInvalidRequest = errors.New("objectstore: the request's shape cannot be read")
	// ErrURLUnavailable reports an assembly that cannot answer a URL: the assembled backend mints none
	// and this process serves no content route.
	ErrURLUnavailable = errors.New("objectstore: this process cannot mint a URL for the object")
)
