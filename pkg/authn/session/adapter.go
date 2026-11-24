package session

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/alexedwards/scs/v2"

	"github.com/vislake/speed/pkg/authn"
)

// storeAdapter is the library's store over the table. FindCtx finds only a live
// browser row; CommitCtx ignores the library's expiry and writes the periods the
// module computed; DeleteCtx unlinks the token and nothing else.
type storeAdapter struct {
	p *product
}

var (
	_ scs.CtxStore = storeAdapter{}
	_ scs.Store    = storeAdapter{}
)

type contextKey int

const (
	sinkKey contextKey = iota
	clientKey
)

// rowSink is where FindCtx leaves the row it found, for the caller of the
// library's Load that needs columns the payload does not carry.
type rowSink struct {
	row *sessionRow
}

type client struct {
	ip, userAgent string
}

// FindCtx reads the browser row whose token has the store key, puts it in ctx's
// sink when there is one, and returns its payload for the library to decode.
func (a storeAdapter) FindCtx(ctx context.Context, key string) ([]byte, bool, error) {
	row, err := a.p.byStoreKey(ctx, key, a.p.now())
	if errors.Is(err, errNoRow) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	if s, ok := ctx.Value(sinkKey).(*rowSink); ok {
		s.row = &row
	}
	return []byte(row.Data), true, nil
}

// CommitCtx creates the row of a browser session from the payload; the expiry
// argument is not used.
func (a storeAdapter) CommitCtx(ctx context.Context, key string, b []byte, _ time.Time) error {
	if len(b) > maxDataBytes {
		return fmt.Errorf("%w: the payload is longer than %d bytes", authn.ErrInvalidArgument, maxDataBytes)
	}
	pl, ok, err := decodePayload(b)
	if err != nil || !ok {
		return fmt.Errorf("%w: the payload cannot be read", authn.ErrInvalidArgument)
	}
	if authn.SessionKind(pl.Kind) != authn.SessionBrowser {
		return fmt.Errorf("%w: only a browser session is committed through the store", authn.ErrInvalidArgument)
	}
	now := a.p.now()
	// A period already over is not written: the session is read as expired.
	if !pl.Expires.After(now) {
		return nil
	}
	c, _ := ctx.Value(clientKey).(client)
	return a.p.insert(ctx, sessionRow{
		ID:            pl.SID,
		TokenHash:     sql.NullString{String: key, Valid: true},
		AccountID:     pl.Account,
		Kind:          pl.Kind,
		Data:          string(b),
		ClientIP:      c.ip,
		UserAgent:     c.userAgent,
		CreatedAt:     now,
		LastSeenAt:    now,
		IdleExpiresAt: sql.NullTime{Time: pl.Idle, Valid: !pl.Idle.IsZero()},
		ExpiresAt:     sql.NullTime{Time: pl.Expires, Valid: true},
	})
}

// DeleteCtx unlinks a token from its session; the row and its revocation state stay.
func (a storeAdapter) DeleteCtx(ctx context.Context, key string) error {
	_, err := a.p.exec(ctx, "unlinking a token",
		"UPDATE "+tableName+" SET token_hash = NULL WHERE token_hash = ?", key)
	return err
}

// Find, Commit and Delete are the library's store interface without a context;
// the library prefers the context forms, and these carry the background context.
func (a storeAdapter) Find(key string) ([]byte, bool, error) {
	return a.FindCtx(context.Background(), key)
}

func (a storeAdapter) Commit(key string, b []byte, expiry time.Time) error {
	return a.CommitCtx(context.Background(), key, b, expiry)
}

func (a storeAdapter) Delete(key string) error { return a.DeleteCtx(context.Background(), key) }
