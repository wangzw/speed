package session

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/authn/internal/clock"
)

const tableName = "authn_sessions"

// rowColumns is the column list every read of a row selects, in the order of
// sessionRow.
const rowColumns = "id, token_hash, account_id, kind, credential_id, impersonation_id, data, client_ip, " +
	"user_agent, created_at, last_seen_at, idle_expires_at, expires_at, revoked_at, revoke_reason"

type sessionRow struct {
	ID              string         `gorm:"column:id"`
	TokenHash       sql.NullString `gorm:"column:token_hash"`
	AccountID       string         `gorm:"column:account_id"`
	Kind            string         `gorm:"column:kind"`
	CredentialID    sql.NullString `gorm:"column:credential_id"`
	ImpersonationID sql.NullString `gorm:"column:impersonation_id"`
	Data            string         `gorm:"column:data"`
	ClientIP        string         `gorm:"column:client_ip"`
	UserAgent       string         `gorm:"column:user_agent"`
	CreatedAt       time.Time      `gorm:"column:created_at"`
	LastSeenAt      time.Time      `gorm:"column:last_seen_at"`
	IdleExpiresAt   sql.NullTime   `gorm:"column:idle_expires_at"`
	ExpiresAt       sql.NullTime   `gorm:"column:expires_at"`
	RevokedAt       sql.NullTime   `gorm:"column:revoked_at"`
	RevokeReason    sql.NullString `gorm:"column:revoke_reason"`
}

func (r sessionRow) live(now time.Time) bool {
	if r.RevokedAt.Valid {
		return false
	}
	switch authn.SessionKind(r.Kind) {
	case authn.SessionBrowser:
		return r.IdleExpiresAt.Valid && r.IdleExpiresAt.Time.After(now) &&
			r.ExpiresAt.Valid && r.ExpiresAt.Time.After(now)
	case authn.SessionToken:
		return r.ExpiresAt.Valid && r.ExpiresAt.Time.After(now)
	case authn.SessionService:
		return !r.ExpiresAt.Valid || r.ExpiresAt.Time.After(now)
	}
	return false
}

func optionalTime(t time.Time) any {
	if t.IsZero() {
		return nil
	}
	return clock.UTC(t)
}

func optionalText(s string) any {
	if s == "" {
		return nil
	}
	return s
}

// unavailable wraps a failure of the database in ErrUnavailable. The text names
// the operation and carries the driver's error, which holds no credential: a
// token is only ever bound as its digest.
func unavailable(op string, err error) error {
	return fmt.Errorf("%w: %s: %w", authn.ErrUnavailable, op, err)
}

var errNoRow = errors.New("session: no such row")

func (p *product) exec(ctx context.Context, op, statement string, args ...any) (int64, error) {
	res := p.handle.WithContext(ctx).Exec(statement, args...)
	if res.Error != nil {
		return 0, unavailable(op, res.Error)
	}
	return res.RowsAffected, nil
}

func (p *product) returnedIDs(ctx context.Context, op, statement string, args ...any) ([]authn.SessionID, error) {
	var rows []struct{ ID string }
	if err := p.handle.WithContext(ctx).Raw(statement, args...).Scan(&rows).Error; err != nil {
		return nil, unavailable(op, err)
	}
	out := make([]authn.SessionID, len(rows))
	for i, r := range rows {
		out[i] = authn.SessionID(r.ID)
	}
	return out, nil
}

func (p *product) rows(ctx context.Context, op, statement string, args ...any) ([]sessionRow, error) {
	var out []sessionRow
	if err := p.handle.WithContext(ctx).Raw(statement, args...).Scan(&out).Error; err != nil {
		return nil, unavailable(op, err)
	}
	for i := range out {
		out[i].normalize()
	}
	return out, nil
}

func (r *sessionRow) normalize() {
	r.CreatedAt = clock.UTC(r.CreatedAt)
	r.LastSeenAt = clock.UTC(r.LastSeenAt)
	r.IdleExpiresAt.Time = clock.UTC(r.IdleExpiresAt.Time)
	r.ExpiresAt.Time = clock.UTC(r.ExpiresAt.Time)
	r.RevokedAt.Time = clock.UTC(r.RevokedAt.Time)
}

func (p *product) one(ctx context.Context, op, statement string, args ...any) (sessionRow, error) {
	got, err := p.rows(ctx, op, statement, args...)
	if err != nil {
		return sessionRow{}, err
	}
	if len(got) == 0 {
		return sessionRow{}, errNoRow
	}
	return got[0], nil
}

func (p *product) byID(ctx context.Context, id authn.SessionID) (sessionRow, error) {
	return p.one(ctx, "reading a session", "SELECT "+rowColumns+" FROM "+tableName+" WHERE id = ?", string(id))
}

func (p *product) byCredential(ctx context.Context, credentialID string) (sessionRow, error) {
	return p.one(ctx, "reading a service session",
		"SELECT "+rowColumns+" FROM "+tableName+" WHERE credential_id = ?", credentialID)
}

// byStoreKey reads the live browser row whose token has the digest key: the
// conditions of the library's lookup, which are the only ones under which a
// browser row is found by its token.
func (p *product) byStoreKey(ctx context.Context, key string, now time.Time) (sessionRow, error) {
	return p.one(ctx, "reading a browser session",
		"SELECT "+rowColumns+" FROM "+tableName+" WHERE token_hash = ? AND kind = 'browser' "+
			"AND revoked_at IS NULL AND idle_expires_at > ? AND expires_at > ?",
		key, clock.UTC(now), clock.UTC(now))
}

func (r sessionRow) insertParams() []any {
	return []any{
		r.ID, nullable(r.TokenHash), r.AccountID, r.Kind, nullable(r.CredentialID), nullable(r.ImpersonationID),
		r.Data, r.ClientIP, r.UserAgent, clock.UTC(r.CreatedAt), clock.UTC(r.LastSeenAt),
		nullableTime(r.IdleExpiresAt), nullableTime(r.ExpiresAt),
	}
}

func nullable(s sql.NullString) any {
	if !s.Valid {
		return nil
	}
	return s.String
}

func nullableTime(t sql.NullTime) any {
	if !t.Valid {
		return nil
	}
	return clock.UTC(t.Time)
}

const insertColumns = "(id, token_hash, account_id, kind, credential_id, impersonation_id, data, client_ip, " +
	"user_agent, created_at, last_seen_at, idle_expires_at, expires_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)"

func (p *product) insert(ctx context.Context, r sessionRow) error {
	_, err := p.exec(ctx, "writing a session", "INSERT INTO "+tableName+" "+insertColumns, r.insertParams()...)
	return err
}

// insertService writes a service session's row unless one is there, and reports
// whether it wrote it, from the rows the statement changed rather than a
// returned row.
func (p *product) insertService(ctx context.Context, r sessionRow) (bool, error) {
	n, err := p.exec(ctx, "writing a service session",
		"INSERT INTO "+tableName+" "+insertColumns+" ON CONFLICT (credential_id) DO NOTHING", r.insertParams()...)
	return n == 1, err
}

// touchRow extends the idle period of a browser row, unless it is revoked, and
// returns the rows it changed.
func (p *product) touchRow(ctx context.Context, id authn.SessionID, seen, idleExpires time.Time) (int64, error) {
	return p.exec(ctx, "extending a session",
		"UPDATE "+tableName+" SET last_seen_at = ?, idle_expires_at = ? WHERE id = ? AND revoked_at IS NULL",
		clock.UTC(seen), clock.UTC(idleExpires), string(id))
}

// revokeRow revokes one session, unless it is revoked already, and returns the
// rows it changed.
func (p *product) revokeRow(ctx context.Context, id authn.SessionID, reason string, at time.Time) (int64, error) {
	return p.exec(ctx, "revoking a session",
		"UPDATE "+tableName+" SET revoked_at = ?, revoke_reason = ? WHERE id = ? AND revoked_at IS NULL",
		clock.UTC(at), reason, string(id))
}

const liveCondition = "account_id = ? AND kind IN ('browser', 'token') AND revoked_at IS NULL " +
	"AND expires_at > ? AND (kind = 'token' OR idle_expires_at > ?)"

// revocableCondition selects the rows RevokeAccount ends, the same set a caller
// reads to name them; a row past its deadline is among them.
const revocableCondition = "account_id = ? AND kind IN ('browser', 'token') AND revoked_at IS NULL"

func (p *product) liveRows(ctx context.Context, account authn.AccountID, now time.Time, order string) ([]sessionRow, error) {
	return p.rows(ctx, "listing sessions",
		"SELECT "+rowColumns+" FROM "+tableName+" WHERE "+liveCondition+" ORDER BY "+order,
		string(account), clock.UTC(now), clock.UTC(now))
}

func isMissingTable(err error) bool {
	if err == nil {
		return false
	}
	text := err.Error()
	return strings.Contains(text, "no such table") || strings.Contains(text, "does not exist")
}
