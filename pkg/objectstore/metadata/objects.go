package metadata

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/vislake/speed/pkg/objectstore"
	"github.com/vislake/speed/pkg/tenant"
)

// tableName is the objects table. It is written here rather than derived from a
// Go type because the model also names it, and the two migrations name it in
// turn, and the four are kept in step by hand.
const tableName = "objects"

// The list paging bounds. A limit of zero reads as the default; a limit above
// the maximum is refused rather than silently truncated, so a caller that asked
// for more rows than one page holds is told, not served a short page.
const (
	defaultListLimit = 50
	maxListLimit     = 200
)

// The internal key grammar. A key is two random hexadecimal segments joined by
// the separator: a shard and a name, the latter carrying the whole 128 bits of
// randomness. The caps are the widths the key column's own length check
// declares.
//
// The key is this module's private convention. It is generated at declaration,
// never handed back through any API, and the module never parses it: the object
// identifier is read from its own column, and the key is only ever passed to
// the byte seam. It carries no tenant and no object identifier, so a backend
// address that embeds the key (an S3 pre-signed URL is one) reveals nothing
// internal. A row written before this grammar keeps its old key; both syntaxes
// are opaque to every reader here, which never parses either.
const (
	keySeparator  = "/"
	keySegmentMax = 128
	keyMaxLen     = 256

	shardBytes = 4
	nameBytes  = 16
)

// object is the row shape of the objects table.
//
// It embeds the tenant convention, which is both the declaration that a row
// belongs to a tenant and the column that carries it: a statement issued through
// this model is scoped to the tenant of its context, filled with it on insert
// and judged against it on write. The key leads with that column, as every
// scoped table's does.
//
// The name column holds the declaration's name after Create normalized it
// once; it is not the value the caller sent, and no lookup reads it.
//
// Every instant is an epoch count in milliseconds, matching the columns the
// migrations declare and the native statements the sweep runs, so a row the
// model path reads and a row the sweep touches carry the same value. The
// declared values a caller gave and the observed values an upload recorded are
// kept apart, which is what lets an upload see that a byte count differs from
// the declaration without overwriting the declaration.
type object struct {
	tenant.Model
	ID               string `gorm:"column:id;primaryKey"`
	Key              string `gorm:"column:key;not null"`
	Name             string `gorm:"column:name;not null"`
	State            string `gorm:"column:state;not null"`
	DeclaredSize     int64  `gorm:"column:declared_size;not null"`
	DeclaredType     string `gorm:"column:declared_type;not null"`
	DeclaredChecksum string `gorm:"column:declared_checksum;not null"`
	Size             int64  `gorm:"column:size;not null"`
	MIME             string `gorm:"column:mime;not null"`
	ChecksumSHA256   string `gorm:"column:checksum_sha256;not null"`
	UploadExpiresAt  int64  `gorm:"column:upload_expires_at;not null"`
	ExpiresAt        *int64 `gorm:"column:expires_at"`
	// The row's own timestamps are written by this module, so GORM's own
	// auto-timestamp management is turned off and the epoch-millisecond values
	// stand as set.
	CreatedAt int64 `gorm:"column:created_at;not null;autoCreateTime:false;autoUpdateTime:false"`
	UpdatedAt int64 `gorm:"column:updated_at;not null;autoCreateTime:false;autoUpdateTime:false"`
}

// TableName is the table the objects live in.
func (object) TableName() string { return tableName }

// view builds the read-only view a caller sees out of a row. The internal key is
// carried on the Object value but reaches no API response: the view the HTTP
// surface renders drops it, and only this method and the module's own statements
// ever read it.
func (r *object) view() objectstore.Object {
	return objectstore.Object{
		ID:        r.ID,
		Key:       r.Key,
		Name:      r.Name,
		State:     objectstore.State(r.State),
		Size:      r.Size,
		MIME:      r.MIME,
		Checksum:  r.ChecksumSHA256,
		ExpiresAt: msTimePtr(r.ExpiresAt),
		CreatedAt: msTime(r.CreatedAt),
		UpdatedAt: msTime(r.UpdatedAt),
	}
}

// msTime reads an epoch count in milliseconds as an instant, in UTC.
func msTime(ms int64) time.Time { return time.UnixMilli(ms).UTC() }

// msTimePtr reads an optional epoch count as an optional instant, in UTC.
func msTimePtr(ms *int64) *time.Time {
	if ms == nil {
		return nil
	}
	t := msTime(*ms)
	return &t
}

// internalKey mints the internal key an object's bytes are stored under: a
// random shard and a random name, joined by the separator. It refuses a segment
// or a whole key that leaves the caps the key column declares; a randomly drawn
// key never does, and the checks stand so that the grammar is stated where it is
// built.
//
// The randomness is the point: the key carries no tenant and no object
// identifier, so a backend address that embeds it (an S3 pre-signed URL does)
// reveals nothing internal. A failure to read the system's random source is a
// failure of the store, not a request the module cannot act on.
func internalKey() (string, error) {
	shard, err := randomHex(shardBytes)
	if err != nil {
		return "", err
	}
	name, err := randomHex(nameBytes)
	if err != nil {
		return "", err
	}
	if err := checkSegment(shard); err != nil {
		return "", err
	}
	if err := checkSegment(name); err != nil {
		return "", err
	}
	key := shard + keySeparator + name
	if len(key) > keyMaxLen {
		return "", fmt.Errorf("%w: the internal key the module minted is %d bytes, above the %d the key "+
			"column admits", objectstore.ErrInvalidRequest, len(key), keyMaxLen)
	}
	return key, nil
}

// randomHex draws n random bytes and renders them as lowercase hexadecimal, so
// one byte becomes two characters. A random source that cannot be read is the
// store being unavailable: no key can be minted without it.
func randomHex(n int) (string, error) {
	buf := make([]byte, n)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("%w: minting an internal key: %w", objectstore.ErrStoreUnavailable, err)
	}
	return hex.EncodeToString(buf), nil
}

// checkSegment refuses a segment that is empty, carries the separator, or
// exceeds the segment cap. A segment is an identifier the roster or the module
// minted, so a refusal is a request the module cannot act on.
func checkSegment(segment string) error {
	switch {
	case segment == "":
		return fmt.Errorf("%w: an internal key segment is empty", objectstore.ErrInvalidRequest)
	case strings.Contains(segment, keySeparator):
		return fmt.Errorf("%w: an internal key segment carries the separator %q",
			objectstore.ErrInvalidRequest, keySeparator)
	case len(segment) > keySegmentMax:
		return fmt.Errorf("%w: an internal key segment is %d bytes, above the %d the grammar admits",
			objectstore.ErrInvalidRequest, len(segment), keySegmentMax)
	}
	return nil
}

// encodeCursor encodes the keyset cursor of a list page: the (created_at, id) of
// the page's last row, base64url-encoded. It is opaque to a caller; its encoding
// is this module's own convention.
func encodeCursor(createdAtMS int64, id string) string {
	raw := strconv.FormatInt(createdAtMS, 10) + "|" + id
	return base64.RawURLEncoding.EncodeToString([]byte(raw))
}

// decodeCursor reads a cursor back into the instant and identifier it names. A
// cursor this module did not write is a request whose shape cannot be read.
func decodeCursor(cursor string) (int64, string, error) {
	raw, err := base64.RawURLEncoding.DecodeString(cursor)
	if err != nil {
		return 0, "", fmt.Errorf("%w: the list cursor cannot be read", objectstore.ErrInvalidRequest)
	}
	at, id, ok := strings.Cut(string(raw), "|")
	if !ok || id == "" {
		return 0, "", fmt.Errorf("%w: the list cursor does not name a row", objectstore.ErrInvalidRequest)
	}
	ms, err := strconv.ParseInt(at, 10, 64)
	if err != nil {
		return 0, "", fmt.Errorf("%w: the list cursor does not name an instant", objectstore.ErrInvalidRequest)
	}
	return ms, id, nil
}
