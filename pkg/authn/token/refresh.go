package token

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"

	"github.com/vislake/speed/pkg/audit"
	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/authn/internal/clock"
	"github.com/vislake/speed/pkg/event"
)

// The columns of a refresh-token row, in the order refreshRow reads them.
const refreshColumns = "id, session_id, token_hash, parent_id, issued_at, idle_expires_at, used_at, replaced_by"

// The audit action and outcome a detected reuse is recorded under, and how long
// a reuse event may take to publish once the response is on its way.
const (
	actionRefreshReuse = "authn.token.refresh-reuse"
	outcomeDetected    = "detected"
	publishTimeout     = 5 * time.Second
)

type refreshRow struct {
	ID            string         `gorm:"column:id"`
	SessionID     string         `gorm:"column:session_id"`
	TokenHash     string         `gorm:"column:token_hash"`
	ParentID      sql.NullString `gorm:"column:parent_id"`
	IssuedAt      time.Time      `gorm:"column:issued_at"`
	IdleExpiresAt time.Time      `gorm:"column:idle_expires_at"`
	UsedAt        sql.NullTime   `gorm:"column:used_at"`
	ReplacedBy    sql.NullString `gorm:"column:replaced_by"`
}

// errNoRefreshRow is what a lookup that found no row reports: a presented token
// that is not in the table answers invalid_grant and triggers no revocation.
var errNoRefreshRow = errors.New("token: no such refresh row")

func (r *refreshRow) normalize() {
	r.IssuedAt = clock.UTC(r.IssuedAt)
	r.IdleExpiresAt = clock.UTC(r.IdleExpiresAt)
	r.UsedAt.Time = clock.UTC(r.UsedAt.Time)
}

// refreshUnavailable wraps a failure of the refresh table in ErrUnavailable. The
// text names the operation and carries the driver's error, which holds no
// credential: a token is only ever bound as its digest.
func refreshUnavailable(op string, err error) error {
	return fmt.Errorf("%w: %s: %w", authn.ErrUnavailable, op, err)
}

func (s *refreshStore) one(ctx context.Context, op, where string, args ...any) (refreshRow, error) {
	var out []refreshRow
	if err := s.handle.WithContext(ctx).Raw("SELECT "+refreshColumns+" FROM "+tableName+" WHERE "+where,
		args...).Scan(&out).Error; err != nil {
		return refreshRow{}, refreshUnavailable(op, err)
	}
	if len(out) == 0 {
		return refreshRow{}, errNoRefreshRow
	}
	out[0].normalize()
	return out[0], nil
}

func (s *refreshStore) byHash(ctx context.Context, digest string) (refreshRow, error) {
	return s.one(ctx, "reading a refresh token", "token_hash = ?", digest)
}

// byID reads one row by its identifier. It is read afresh after a lost consume,
// to learn the moment the winner recorded.
func (s *refreshStore) byID(ctx context.Context, id string) (refreshRow, error) {
	return s.one(ctx, "reading a refresh token", "id = ?", id)
}

func (s *refreshStore) insert(ctx context.Context, r refreshRow) error {
	return insertRefreshRow(ctx, s.handle, r)
}

func insertRefreshRow(ctx context.Context, db *gorm.DB, r refreshRow) error {
	res := db.WithContext(ctx).Exec("INSERT INTO "+tableName+
		" (id, session_id, token_hash, parent_id, issued_at, idle_expires_at, used_at, replaced_by) "+
		"VALUES (?, ?, ?, ?, ?, ?, ?, ?)",
		r.ID, r.SessionID, r.TokenHash, nullText(r.ParentID), clock.UTC(r.IssuedAt),
		clock.UTC(r.IdleExpiresAt), nullTime(r.UsedAt), nullText(r.ReplacedBy))
	if res.Error != nil {
		return refreshUnavailable("writing a refresh token", res.Error)
	}
	return nil
}

func nullText(s sql.NullString) any {
	if !s.Valid {
		return nil
	}
	return s.String
}

func nullTime(t sql.NullTime) any {
	if !t.Valid {
		return nil
	}
	return clock.UTC(t.Time)
}

// rotate consumes row in one transaction: it marks the row used with replaced_by,
// calls sign, inserts the child and commits. The conditional update lets exactly
// one concurrent caller win; a failure rolls back and leaves the token unused.
func (s *refreshStore) rotate(ctx context.Context, row, child refreshRow, sign func() error) (bool, error) {
	tx := s.handle.WithContext(ctx).Begin()
	if tx.Error != nil {
		return false, refreshUnavailable("beginning a rotation", tx.Error)
	}
	committed := false
	defer func() {
		if !committed {
			tx.Rollback()
		}
	}()
	res := tx.Exec("UPDATE "+tableName+" SET used_at = ?, replaced_by = ? WHERE id = ? AND used_at IS NULL",
		clock.UTC(child.IssuedAt), child.ID, row.ID)
	if res.Error != nil {
		return false, refreshUnavailable("consuming a refresh token", res.Error)
	}
	if res.RowsAffected == 0 {
		return false, nil
	}
	if err := sign(); err != nil {
		return false, err
	}
	if err := insertRefreshRow(ctx, tx, child); err != nil {
		return false, err
	}
	if err := tx.Commit().Error; err != nil {
		return false, refreshUnavailable("committing a rotation", err)
	}
	committed = true
	return true, nil
}

// NewRefresh makes the first refresh token of a token session, reading the session
// fresh and refusing anything that is not a live token session as ErrInvalidGrant.
func (p *product) NewRefresh(ctx context.Context, session authn.SessionID) (authn.RefreshToken, error) {
	status, err := p.sessions.StatusFresh(ctx, session)
	if err != nil {
		if errors.Is(err, authn.ErrSessionNotFound) {
			return authn.RefreshToken{}, authn.ErrInvalidGrant
		}
		return authn.RefreshToken{}, refreshUnavailable("reading the session", err)
	}
	if !status.Active || status.Kind != authn.SessionToken || status.ImpersonationID != "" {
		return authn.RefreshToken{}, authn.ErrInvalidGrant
	}
	now := p.clock.Now()
	value, err := newRefreshToken()
	if err != nil {
		return authn.RefreshToken{}, err
	}
	id, err := uuid.NewV7()
	if err != nil {
		return authn.RefreshToken{}, fmt.Errorf("%s: making a refresh-token identifier: %w", ModuleName, err)
	}
	row := refreshRow{
		ID:            id.String(),
		SessionID:     string(session),
		TokenHash:     digestRefreshToken(value),
		IssuedAt:      now,
		IdleExpiresAt: now.Add(p.settings.refreshIdleTTL()),
	}
	if err := p.refresh.insert(ctx, row); err != nil {
		return authn.RefreshToken{}, err
	}
	return authn.RefreshToken{Value: value, ExpiresAt: row.IdleExpiresAt}, nil
}

// Refresh rotates a refresh token in one transaction and issues a new access token,
// consuming the row with a conditional update whose affected-row count decides the
// winner. A missing row is ErrInvalidGrant; one used past the grace is ErrRefreshReuse.
func (p *product) Refresh(ctx context.Context, presented string) (authn.AccessToken, authn.RefreshToken, error) {
	now := p.clock.Now()
	row, err := p.refresh.byHash(ctx, digestRefreshToken(presented))
	switch {
	case errors.Is(err, errNoRefreshRow):
		return p.refreshDenied(ctx, outcomeInvalid, authn.ErrInvalidGrant)
	case err != nil:
		p.recordOperation(ctx, operationRefreshed, outcomeUnavailable)
		return authn.AccessToken{}, authn.RefreshToken{}, err
	}

	status, err := p.sessions.StatusFresh(ctx, authn.SessionID(row.SessionID))
	if err != nil {
		if errors.Is(err, authn.ErrSessionNotFound) {
			return p.refreshDenied(ctx, outcomeInvalid, authn.ErrInvalidGrant)
		}
		p.recordOperation(ctx, operationRefreshed, outcomeUnavailable)
		return authn.AccessToken{}, authn.RefreshToken{}, refreshUnavailable("reading the session", err)
	}
	if !status.Active || status.Kind != authn.SessionToken || status.ImpersonationID != "" ||
		!row.IdleExpiresAt.After(now) {
		return p.refreshDenied(ctx, outcomeInvalid, authn.ErrInvalidGrant)
	}

	value, err := newRefreshToken()
	if err != nil {
		return authn.AccessToken{}, authn.RefreshToken{}, err
	}
	childID, err := uuid.NewV7()
	if err != nil {
		return authn.AccessToken{}, authn.RefreshToken{}, fmt.Errorf("%s: making a refresh-token identifier: %w",
			ModuleName, err)
	}
	child := refreshRow{
		ID:            childID.String(),
		SessionID:     row.SessionID,
		TokenHash:     digestRefreshToken(value),
		ParentID:      sql.NullString{String: row.ID, Valid: true},
		IssuedAt:      now,
		IdleExpiresAt: now.Add(p.settings.refreshIdleTTL()),
	}

	var access authn.AccessToken
	winner, err := p.refresh.rotate(ctx, row, child, func() error {
		var signErr error
		access, signErr = p.signAccess(ctx, issuedFromStatus(status, authn.SessionID(row.SessionID)))
		return signErr
	})
	if err != nil {
		p.recordOperation(ctx, operationRefreshed, outcomeUnavailable)
		return authn.AccessToken{}, authn.RefreshToken{}, err
	}
	if winner {
		p.recordOperation(ctx, operationRefreshed, outcomeOK)
		return access, authn.RefreshToken{Value: value, ExpiresAt: child.IdleExpiresAt}, nil
	}

	// The row was already used. Within the grace a second presentation is the
	// client retrying and gets a sibling child; past it, the token was reused.
	used, err := p.refresh.byID(ctx, row.ID)
	if err != nil {
		if errors.Is(err, errNoRefreshRow) {
			// The row went between the digest read and this one, most likely through
			// a concurrent revoke or sweep; a token not in the table is invalid_grant
			// here as on the byHash branch.
			return p.refreshDenied(ctx, outcomeInvalid, authn.ErrInvalidGrant)
		}
		p.recordOperation(ctx, operationRefreshed, outcomeUnavailable)
		return authn.AccessToken{}, authn.RefreshToken{}, err
	}
	if grace := p.settings.refreshGrace(); grace > 0 && used.UsedAt.Valid &&
		p.clock.Now().Sub(used.UsedAt.Time) <= grace {
		// A grace hit is a security signal an operator has to look at: a second
		// presentation inside the window may be a stolen token, and it is the
		// one mode where the window is open at all.
		p.logger.Warn(diagnosticPrefix+ModuleName+": a refresh token was presented again inside the grace window",
			slog.String("session", row.SessionID))
		renewal, err := p.issueSibling(ctx, row, status)
		if err != nil {
			p.recordOperation(ctx, operationRefreshed, outcomeUnavailable)
			return authn.AccessToken{}, authn.RefreshToken{}, err
		}
		p.recordOperation(ctx, operationRefreshed, outcomeOK)
		return renewal.access, renewal.refresh, nil
	}

	p.revokeFamily(ctx, row.SessionID, status.Account)
	p.recordOperation(ctx, operationRefreshed, outcomeReuseDetected)
	return authn.AccessToken{}, authn.RefreshToken{}, authn.ErrRefreshReuse
}

func (p *product) refreshDenied(ctx context.Context, outcome string, err error) (authn.AccessToken, authn.RefreshToken, error) {
	p.recordOperation(ctx, operationRefreshed, outcome)
	return authn.AccessToken{}, authn.RefreshToken{}, err
}

// issuedFromStatus is the issue facts of a refresh: the account, the
// authentication facts and the session, all taken from the session row, so the
// rotation widens neither the level nor the authentication time.
func issuedFromStatus(status authn.SessionStatus, session authn.SessionID) authn.IssueToken {
	return authn.IssueToken{
		Account:   status.Account,
		Session:   session,
		Methods:   status.Methods,
		Assurance: status.Assurance,
		AuthTime:  status.AuthTime,
	}
}

type renewal struct {
	access  authn.AccessToken
	refresh authn.RefreshToken
}

// issueSibling is the retry branch: a second child of the same used row, with no
// consume of its own, so a client that presented the same token twice inside the
// grace gets a fresh pair rather than tripping reuse detection.
func (p *product) issueSibling(ctx context.Context, row refreshRow, status authn.SessionStatus) (renewal, error) {
	value, err := newRefreshToken()
	if err != nil {
		return renewal{}, err
	}
	id, err := uuid.NewV7()
	if err != nil {
		return renewal{}, fmt.Errorf("%s: making a refresh-token identifier: %w", ModuleName, err)
	}
	now := p.clock.Now()
	child := refreshRow{
		ID:            id.String(),
		SessionID:     row.SessionID,
		TokenHash:     digestRefreshToken(value),
		ParentID:      sql.NullString{String: row.ID, Valid: true},
		IssuedAt:      now,
		IdleExpiresAt: now.Add(p.settings.refreshIdleTTL()),
	}
	access, err := p.signAccess(ctx, issuedFromStatus(status, authn.SessionID(row.SessionID)))
	if err != nil {
		return renewal{}, err
	}
	if err := p.refresh.insert(ctx, child); err != nil {
		return renewal{}, err
	}
	return renewal{access: access, refresh: authn.RefreshToken{Value: value, ExpiresAt: child.IdleExpiresAt}}, nil
}

// revokeFamily ends the whole family on a detected reuse: it revokes the token's
// session, publishes reuse-detected and writes the audit row, all on a context
// detached from the request and bounded by the module's short timeout.
func (p *product) revokeFamily(ctx context.Context, session string, account authn.AccountID) {
	writeCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), publishTimeout)
	defer cancel()
	if err := p.sessions.Revoke(writeCtx, authn.SessionID(session), authn.ReasonRefreshReuse); err != nil {
		p.logger.Warn(diagnosticPrefix+ModuleName+": a detected reuse could not revoke its session",
			slog.String("cause", err.Error()))
	}
	if p.bus != nil {
		evt := event.Event{Type: authn.EventTokenReuseDetected, Payload: authn.TokenReuseDetected{
			SessionID: session, AccountID: account.String(),
		}}
		if err := p.bus.Publish(writeCtx, evt); err != nil {
			p.logger.Warn(diagnosticPrefix+ModuleName+": a reuse-detected event could not be published",
				slog.String("cause", err.Error()))
		}
	}
	if p.audit != nil {
		if err := p.audit.Record(writeCtx, audit.Entry{
			Action:  actionRefreshReuse,
			Object:  session,
			Outcome: outcomeDetected,
		}); err != nil {
			p.logger.Error(diagnosticPrefix+ModuleName+": the audit row of a detected reuse could not be written",
				slog.String("cause", err.Error()))
			p.recordAuditFailure(ctx)
		}
	}
}
