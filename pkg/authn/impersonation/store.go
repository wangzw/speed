package impersonation

import (
	"context"
	"fmt"
	"time"

	"gorm.io/gorm"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/authn/internal/clock"
)

const grantTable = "authn_impersonations"

const grantColumns = "id, actor_id, target_id, tenant_id, session_id, reason, started_at, ends_at, ended_at, ended_by, end_reason"

type grantRow struct {
	ID        string
	ActorID   string
	TargetID  string
	TenantID  string
	SessionID string
	Reason    string
	StartedAt time.Time
	EndsAt    time.Time
	EndedAt   *time.Time
	EndedBy   *string
	EndReason *string
}

func (r grantRow) grant() authn.ImpersonationGrant {
	g := authn.ImpersonationGrant{
		ID:        r.ID,
		Actor:     authn.AccountID(r.ActorID),
		Target:    authn.AccountID(r.TargetID),
		Tenant:    r.TenantID,
		Reason:    r.Reason,
		SessionID: authn.SessionID(r.SessionID),
		StartedAt: clock.UTC(r.StartedAt),
		EndsAt:    clock.UTC(r.EndsAt),
	}
	if r.EndedAt != nil {
		g.EndedAt = clock.UTC(*r.EndedAt)
	}
	if r.EndedBy != nil {
		g.EndedBy = *r.EndedBy
	}
	if r.EndReason != nil {
		g.EndReason = *r.EndReason
	}
	return g
}

type store struct {
	handle *gorm.DB
}

func (s *store) session(ctx context.Context) *gorm.DB { return s.handle.WithContext(ctx) }

func (s *store) insert(tx *gorm.DB, g authn.ImpersonationGrant) error {
	err := tx.Exec("INSERT INTO "+grantTable+
		" (id, actor_id, target_id, tenant_id, session_id, reason, started_at, ends_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?)",
		g.ID, string(g.Actor), string(g.Target), g.Tenant, string(g.SessionID), g.Reason,
		clock.UTC(g.StartedAt), clock.UTC(g.EndsAt)).Error
	if err != nil {
		return fmt.Errorf("writing an impersonation record: %w", err)
	}
	return nil
}

func (s *store) find(ctx context.Context, id string) (authn.ImpersonationGrant, bool, error) {
	var rows []grantRow
	err := s.session(ctx).Table(grantTable).Select(grantColumns).Where("id = ?", id).Limit(1).Scan(&rows).Error
	if err != nil {
		return authn.ImpersonationGrant{}, false, fmt.Errorf("reading an impersonation record: %w", err)
	}
	if len(rows) == 0 {
		return authn.ImpersonationGrant{}, false, nil
	}
	return rows[0].grant(), true, nil
}

func (s *store) unendedForSession(ctx context.Context, session authn.SessionID) (authn.ImpersonationGrant, bool, error) {
	var rows []grantRow
	err := s.session(ctx).Table(grantTable).Select(grantColumns).
		Where("session_id = ? AND ended_at IS NULL", string(session)).Limit(1).Scan(&rows).Error
	if err != nil {
		return authn.ImpersonationGrant{}, false, fmt.Errorf("reading the impersonation of a session: %w", err)
	}
	if len(rows) == 0 {
		return authn.ImpersonationGrant{}, false, nil
	}
	return rows[0].grant(), true, nil
}

func (s *store) unendedForSessionTx(tx *gorm.DB, session authn.SessionID) (authn.ImpersonationGrant, bool, error) {
	var rows []grantRow
	err := tx.Table(grantTable).Select(grantColumns).
		Where("session_id = ? AND ended_at IS NULL", string(session)).Limit(1).Scan(&rows).Error
	if err != nil {
		return authn.ImpersonationGrant{}, false, fmt.Errorf("reading the impersonation of a session: %w", err)
	}
	if len(rows) == 0 {
		return authn.ImpersonationGrant{}, false, nil
	}
	return rows[0].grant(), true, nil
}

// end decides its winner by the rows affected and not by RETURNING, so two
// replicas that read the same lapsed record are told apart by nothing else.
func (s *store) end(ctx context.Context, id, endedBy, endReason string, at time.Time) (bool, error) {
	return s.endTx(s.session(ctx), id, endedBy, endReason, at)
}

func (s *store) endTx(tx *gorm.DB, id, endedBy, endReason string, at time.Time) (bool, error) {
	res := tx.Exec("UPDATE "+grantTable+" SET ended_at = ?, ended_by = ?, end_reason = ? WHERE id = ? AND ended_at IS NULL",
		clock.UTC(at), nullIfEmpty(endedBy), endReason, id)
	if res.Error != nil {
		return false, fmt.Errorf("ending an impersonation record: %w", res.Error)
	}
	return res.RowsAffected == 1, nil
}

func (s *store) list(ctx context.Context, q authn.GrantQuery) ([]authn.ImpersonationGrant, string, error) {
	limit := q.EffectiveLimit()
	tx := s.session(ctx).Table(grantTable).Select(grantColumns)
	if q.Tenant != "" {
		tx = tx.Where("tenant_id = ?", q.Tenant)
	}
	if q.Actor != "" {
		tx = tx.Where("actor_id = ?", string(q.Actor))
	}
	if q.Target != "" {
		tx = tx.Where("target_id = ?", string(q.Target))
	}
	if q.After != "" {
		tx = tx.Where("id > ?", q.After)
	}
	var rows []grantRow
	if err := tx.Order("id").Limit(limit + 1).Scan(&rows).Error; err != nil {
		return nil, "", fmt.Errorf("listing impersonation records: %w", err)
	}
	next := ""
	if len(rows) > limit {
		rows = rows[:limit]
		next = rows[limit-1].ID
	}
	out := make([]authn.ImpersonationGrant, len(rows))
	for i, r := range rows {
		out[i] = r.grant()
	}
	return out, next, nil
}

func (s *store) probe(ctx context.Context) error {
	err := s.session(ctx).Exec("SELECT 1 FROM " + grantTable + " WHERE 1 = 0").Error
	if err == nil {
		return nil
	}
	if !s.session(ctx).Migrator().HasTable(grantTable) {
		return fmt.Errorf("%w: %s is not there: %w", authn.ErrNotMigrated, grantTable, err)
	}
	return fmt.Errorf("reading %s: %w", grantTable, err)
}

func nullIfEmpty(s string) any {
	if s == "" {
		return nil
	}
	return s
}
