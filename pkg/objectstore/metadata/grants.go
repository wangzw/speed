package metadata

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"time"

	"github.com/vislake/speed/pkg/objectstore"
	"github.com/vislake/speed/pkg/tenant"
)

const grantTableName = "object_grants"

const (
	tokenBytes = 32
	tokenChars = 43
)

type grant struct {
	tenant.Model
	TokenHash string `gorm:"column:token_hash;primaryKey"`
	ObjectID  string `gorm:"column:object_id;not null"`
	Use       string `gorm:"column:use;not null"`
	ExpiresAt int64  `gorm:"column:expires_at;not null"`
	CreatedAt int64  `gorm:"column:created_at;not null;autoCreateTime:false;autoUpdateTime:false"`
}

func (grant) TableName() string { return grantTableName }

func mintToken() (token, hash string, err error) {
	buf := make([]byte, tokenBytes)
	if _, err := rand.Read(buf); err != nil {
		return "", "", fmt.Errorf("%w: minting a content token: %w", objectstore.ErrStoreUnavailable, err)
	}
	token = base64.RawURLEncoding.EncodeToString(buf)
	return token, hashToken(token), nil
}

func hashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

func validTokenShape(token string) bool {
	if len(token) != tokenChars {
		return false
	}
	for i := 0; i < len(token); i++ {
		c := token[i]
		switch {
		case 'A' <= c && c <= 'Z', 'a' <= c && c <= 'z', '0' <= c && c <= '9', c == '-', c == '_':
		default:
			return false
		}
	}
	return true
}

func (p *product) insertGrant(ctx context.Context, row *object, use objectstore.URLAccess, hash string, expiresAtMS int64) error {
	record := grant{
		TokenHash: hash,
		ObjectID:  row.ID,
		Use:       string(use),
		ExpiresAt: expiresAtMS,
		CreatedAt: time.Now().UnixMilli(),
	}
	if err := p.handle.WithContext(ctx).Create(&record).Error; err != nil {
		return fmt.Errorf("%w: recording the grant of the object %s: %w", objectstore.ErrStoreUnavailable, row.ID, err)
	}
	return nil
}

type grantRow struct {
	TenantID  string `gorm:"column:tenant_id"`
	ObjectID  string `gorm:"column:object_id"`
	Use       string `gorm:"column:use"`
	ExpiresAt int64  `gorm:"column:expires_at"`
	Live      int64  `gorm:"column:live"`
}

func (p *product) lookupGrant(ctx context.Context, hash string) (*grantRow, error) {
	var rows []grantRow
	if err := p.handle.WithContext(ctx).Raw(p.stmts.grant, hash).Scan(&rows).Error; err != nil {
		return nil, fmt.Errorf("%s: reading a content grant: %w", ModuleName, err)
	}
	if len(rows) == 0 {
		return nil, nil
	}
	return &rows[0], nil
}
