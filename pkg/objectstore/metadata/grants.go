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

// grantTableName is the object_grants table. It is written here rather than
// derived from a Go type because the model also names it, and the two migrations
// name it in turn, and the three are kept in step by hand.
const grantTableName = "object_grants"

// The token's shape. A token is 32 random bytes rendered in unpadded base64url,
// which is 43 characters drawn from A-Z, a-z, 0-9, '-' and '_'. Only its SHA-256
// is stored, so the table holds no value that can be used as a credential.
const (
	tokenBytes = 32
	tokenChars = 43
)

// grant is the row shape of the object_grants table: one direct-access address
// this process minted.
//
// It embeds the tenant convention, so a statement issued through this model is
// scoped to the tenant of its context and filled with it on insert. The primary
// key is the tenant and the token hash together; the token hash alone is
// unique, which is what lets the content route look a grant up before it knows
// the tenant.
//
// The row records no end user and no object state: a grant is a bearer
// capability, and whether it still serves is judged each time it is used
// against the object's own state. ExpiresAt is the instant the address stops
// working; the sweep removes rows past it so the table does not grow without
// bound, but correctness does not depend on that pass.
type grant struct {
	tenant.Model
	TokenHash string `gorm:"column:token_hash;primaryKey"`
	ObjectID  string `gorm:"column:object_id;not null"`
	Use       string `gorm:"column:use;not null"`
	ExpiresAt int64  `gorm:"column:expires_at;not null"`
	// The row's own creation instant is written by this module, so GORM's own
	// auto-timestamp management is turned off and the epoch-millisecond value
	// stands as set.
	CreatedAt int64 `gorm:"column:created_at;not null;autoCreateTime:false;autoUpdateTime:false"`
}

// TableName is the table the grants live in.
func (grant) TableName() string { return grantTableName }

// mintToken draws a fresh token and answers it together with the hash the table
// stores. A random source that cannot be read is the store being unavailable: no
// token can be minted without it.
func mintToken() (token, hash string, err error) {
	buf := make([]byte, tokenBytes)
	if _, err := rand.Read(buf); err != nil {
		return "", "", fmt.Errorf("%w: minting a content token: %w", objectstore.ErrStoreUnavailable, err)
	}
	token = base64.RawURLEncoding.EncodeToString(buf)
	return token, hashToken(token), nil
}

// hashToken answers the lowercase hex SHA-256 of a token: the only form of it
// that is stored.
func hashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

// validTokenShape reports whether a presented token has the shape mintToken
// writes: 43 characters of unpadded base64url. A value that is not one of those
// cannot name a grant, and answer the same refusal an unknown token does.
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

// insertGrant records one minted address. The tenant column is filled from the
// context by the model's own filter. A row that cannot be written is the store
// being unavailable: the address would be unusable without it.
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

// grantRow is one grant as the content route reads it: the tenant the object
// belongs to, the object's identifier, which way the address moves bytes, when
// it expires, and whether it is still live.
//
// Live is computed by the lookup statement from the database's own clock, so
// every replica judges an expiry against one clock.
type grantRow struct {
	TenantID  string `gorm:"column:tenant_id"`
	ObjectID  string `gorm:"column:object_id"`
	Use       string `gorm:"column:use"`
	ExpiresAt int64  `gorm:"column:expires_at"`
	Live      int64  `gorm:"column:live"`
}

// lookupGrant reads the grant a token hash names. The lookup crosses tenants: a
// token names the tenant it was minted for, and the request arrives without one.
// A token no grant carries answers nil and no error; a table that cannot be read
// answers the read's own failure.
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
