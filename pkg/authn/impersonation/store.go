package impersonation

import (
	"context"
	"fmt"
	"time"

	"gorm.io/gorm"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/authn/internal/clock"
)

// grantTable is the table of the impersonation records, the one this module
// owns exclusively.
const grantTable = "authn_impersonations"

// grantColumns is the record's columns as a read names them.
const grantColumns = "id, actor_id, target_id, tenant_id, session_id, reason, started_at, ends_at, ended_at, ended_by, end_reason"

// grantRow is one row as a read returns it. The nullable columns are pointers,
// and every time read is converted to UTC on the way out.
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

// grant is the row as the root package's ImpersonationGrant. An unset end reads
// as the zero time on EndedAt, which is what "not ended" is told by.
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

// store is the record store: the statements on authn_impersonations and nothing
// else. It decides nothing; the module around it does.
type store struct {
	handle *gorm.DB
}

func (s *store) session(ctx context.Context) *gorm.DB { return s.handle.WithContext(ctx) }

// insert writes one record. It is called from inside the start transaction, on
// the handle that transaction carries, so that the write of the record and the
// end of the session's lapsed records commit together. A partial unique index
// on the session with an unset end refuses the insert when another, still valid
// record is there, and the caller reads that refusal as ErrImpersonationActive.
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

// find reads one record by its identifier. found is false when there is none.
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

// unendedForSession reads the record on a session whose end is unset. There is
// at most one, by the partial unique index, so the read needs no order. found is
// false when the session has none.
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

// unendedForSessionTx is unendedForSession on the start transaction's handle.
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

// end writes the end of a record with a condition that it is still unended, and
// says whether this call is the one that ended it. A zero endedBy, which a
// lapse read from a record writes, leaves the column empty: the cause has no
// initiator, and the cause is in end_reason. The rows affected and not
// RETURNING are what decides the winner, so two replicas that read the same
// lapsed record are told apart by nothing else.
func (s *store) end(ctx context.Context, id, endedBy, endReason string, at time.Time) (bool, error) {
	return s.endTx(s.session(ctx), id, endedBy, endReason, at)
}

// endTx is end on a caller's handle, so the start transaction can end the
// session's lapsed records and insert the new one in one unit of work.
func (s *store) endTx(tx *gorm.DB, id, endedBy, endReason string, at time.Time) (bool, error) {
	res := tx.Exec("UPDATE "+grantTable+" SET ended_at = ?, ended_by = ?, end_reason = ? WHERE id = ? AND ended_at IS NULL",
		clock.UTC(at), nullIfEmpty(endedBy), endReason, id)
	if res.Error != nil {
		return false, fmt.Errorf("ending an impersonation record: %w", res.Error)
	}
	return res.RowsAffected == 1, nil
}

// list reads one page of records. The records are ordered by their identifier,
// which is a UUIDv7 text and so close to the order they were started, and a page
// is read by that key and not by an offset. A query by target is the complete
// answer to who impersonated whom, because a record is never deleted.
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

// probe reports whether the record table can be read. An error that is not
// ErrNotMigrated says the table is there and the read failed.
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

// nullIfEmpty is a nullable text column's value: the empty string is stored as
// NULL, which is what "no initiator" is told by.
func nullIfEmpty(s string) any {
	if s == "" {
		return nil
	}
	return s
}
