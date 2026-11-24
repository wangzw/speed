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

const tableName = "objects"

const (
	defaultListLimit = 50
	maxListLimit     = 200
)

const (
	keySeparator  = "/"
	keySegmentMax = 128
	keyMaxLen     = 256

	shardBytes = 4
	nameBytes  = 16
)

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
	CreatedAt        int64  `gorm:"column:created_at;not null;autoCreateTime:false;autoUpdateTime:false"`
	UpdatedAt        int64  `gorm:"column:updated_at;not null;autoCreateTime:false;autoUpdateTime:false"`
}

func (object) TableName() string { return tableName }

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

func msTime(ms int64) time.Time { return time.UnixMilli(ms).UTC() }

func msTimePtr(ms *int64) *time.Time {
	if ms == nil {
		return nil
	}
	t := msTime(*ms)
	return &t
}

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

func randomHex(n int) (string, error) {
	buf := make([]byte, n)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("%w: minting an internal key: %w", objectstore.ErrStoreUnavailable, err)
	}
	return hex.EncodeToString(buf), nil
}

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

func encodeCursor(createdAtMS int64, id string) string {
	raw := strconv.FormatInt(createdAtMS, 10) + "|" + id
	return base64.RawURLEncoding.EncodeToString([]byte(raw))
}

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
