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

// tableName is the one table this module owns.
const tableName = "authn_sessions"

// rowColumns is the column list every read of a row selects, in the order of
// sessionRow.
const rowColumns = "id, token_hash, account_id, kind, credential_id, impersonation_id, data, client_ip, " +
	"user_agent, created_at, last_seen_at, idle_expires_at, expires_at, revoked_at, revoke_reason"

// sessionRow is one row of the table. Every time in it is UTC.
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

// live reports whether the row's own conditions hold at now: it is not revoked
// and the period of its kind has not run out. The account's state is judged
// separately.
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

// optionalTime is a time as a statement binds it: UTC, or NULL for the zero
// time.
func optionalTime(t time.Time) any {
	if t.IsZero() {
		return nil
	}
	return clock.UTC(t)
}

// optionalText is a string as a statement binds it: NULL for the empty one.
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

// errNoRow is what a lookup that found no row reports.
var errNoRow = errors.New("session: no such row")

// exec runs one statement and returns the rows it changed.
func (p *product) exec(ctx context.Context, op, statement string, args ...any) (int64, error) {
	res := p.handle.WithContext(ctx).Exec(statement, args...)
	if res.Error != nil {
		return 0, unavailable(op, res.Error)
	}
	return res.RowsAffected, nil
}

// rows runs one query and returns the rows it selected.
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

// normalize puts every time the row carries in UTC, whichever zone the driver
// read it in.
func (r *sessionRow) normalize() {
	r.CreatedAt = clock.UTC(r.CreatedAt)
	r.LastSeenAt = clock.UTC(r.LastSeenAt)
	r.IdleExpiresAt.Time = clock.UTC(r.IdleExpiresAt.Time)
	r.ExpiresAt.Time = clock.UTC(r.ExpiresAt.Time)
	r.RevokedAt.Time = clock.UTC(r.RevokedAt.Time)
}

// one runs a query that selects at most one row.
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

// byID reads the row with the public identifier id.
func (p *product) byID(ctx context.Context, id authn.SessionID) (sessionRow, error) {
	return p.one(ctx, "reading a session", "SELECT "+rowColumns+" FROM "+tableName+" WHERE id = ?", string(id))
}

// byCredential reads the service row of a secret.
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

// insertParams are the values of a row that is inserted.
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

// insert writes a new row.
func (p *product) insert(ctx context.Context, r sessionRow) error {
	_, err := p.exec(ctx, "writing a session", "INSERT INTO "+tableName+" "+insertColumns, r.insertParams()...)
	return err
}

// insertService writes the row of a secret unless one is there, and reports
// whether it wrote it. Which of two concurrent first calls wins is told by the
// rows the statement changed, so nothing depends on the statement returning a
// row.
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

// liveCondition is the part of a statement that selects the browser and token
// rows of an account that can still authenticate. now binds twice.
const liveCondition = "account_id = ? AND kind IN ('browser', 'token') AND revoked_at IS NULL " +
	"AND expires_at > ? AND (kind = 'token' OR idle_expires_at > ?)"

// liveRows lists the account's live browser and token rows, the least recently
// used first.
func (p *product) liveRows(ctx context.Context, account authn.AccountID, now time.Time, order string) ([]sessionRow, error) {
	return p.rows(ctx, "listing sessions",
		"SELECT "+rowColumns+" FROM "+tableName+" WHERE "+liveCondition+" ORDER BY "+order,
		string(account), clock.UTC(now), clock.UTC(now))
}

// isMissingTable reports whether a failure is the database saying the table
// does not exist. SQLite says "no such table: authn_sessions"; PostgreSQL says
// `relation "authn_sessions" does not exist`.
func isMissingTable(err error) bool {
	if err == nil {
		return false
	}
	text := err.Error()
	return strings.Contains(text, "no such table") || strings.Contains(text, "does not exist")
}
