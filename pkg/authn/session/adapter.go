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

// storeAdapter is the library's store over the table. It does what the library
// asks of a store and no more, in the shapes the design fixes:
//
//   - FindCtx finds only a live browser row, so a revoked, idle or expired one is
//     not found whatever the cache held.
//   - CommitCtx creates a row and ignores the expiry the library computed, which
//     comes from a Lifetime that is the settings' upper bound and not their value.
//     The periods written are the ones this module computed from the settings and
//     carried in the payload.
//   - DeleteCtx unlinks the token and nothing else. Revocation is a decision of
//     this module and goes through Sessions.Revoke alone.
//
// It caches nothing: what a read costs is the verifying path's to decide.
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
// library's Load that needs the columns the payload does not carry. It is put in
// the context of that one call.
type rowSink struct {
	row *sessionRow
}

// client is the client information a Commit writes into the row's columns, put
// in the context of that one call.
type client struct {
	ip, userAgent string
}

// FindCtx reads the browser row whose token has the store key. The row is put in
// the sink of ctx, when there is one, and its payload is returned for the
// library to decode.
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

// CommitCtx creates the row of a browser session. The payload is decoded for the
// facts the row is made from; the expiry is not used.
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
	// A period that is over by the time it would be written is not written: the
	// session is read as expired, as it would be a moment later.
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

// DeleteCtx unlinks a token from its session: the row stays, and so does its
// revocation state.
func (a storeAdapter) DeleteCtx(ctx context.Context, key string) error {
	_, err := a.p.exec(ctx, "unlinking a token",
		"UPDATE "+tableName+" SET token_hash = NULL WHERE token_hash = ?", key)
	return err
}

// Find, Commit and Delete are the library's store interface without a context.
// The library calls the context forms, which it prefers when the store has them;
// these are for the interface and carry the background context.
func (a storeAdapter) Find(key string) ([]byte, bool, error) {
	return a.FindCtx(context.Background(), key)
}

func (a storeAdapter) Commit(key string, b []byte, expiry time.Time) error {
	return a.CommitCtx(context.Background(), key, b, expiry)
}

func (a storeAdapter) Delete(key string) error { return a.DeleteCtx(context.Background(), key) }
