package invite

import (
	"errors"
	"time"

	"gorm.io/gorm"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/authn/internal/clock"
)

// tableInvitations is the one table this module owns. A migration file, the
// model below and a raw statement name it, and a change to one is a change to
// all three.
const tableInvitations = "authn_invitations"

// The status column's numbers, which are InvitationStatus's.
const (
	statusPending  int16 = 0
	statusAccepted int16 = 1
	statusRevoked  int16 = 2
)

// invitationRow is one row of authn_invitations. The two time columns the
// database module's model path would fill itself are switched off: every time
// is written explicitly, in UTC, through internal/clock. accepted_at is NULL
// until the invitation is accepted.
type invitationRow struct {
	ID          string     `gorm:"column:id;primaryKey"`
	TenantID    string     `gorm:"column:tenant_id"`
	Email       string     `gorm:"column:email"`
	TokenDigest string     `gorm:"column:token_digest"`
	Status      int16      `gorm:"column:status"`
	InvitedBy   string     `gorm:"column:invited_by"`
	ExpiresAt   time.Time  `gorm:"column:expires_at"`
	AcceptedAt  *time.Time `gorm:"column:accepted_at"`
	CreatedAt   time.Time  `gorm:"column:created_at;autoCreateTime:false"`
	UpdatedAt   time.Time  `gorm:"column:updated_at;autoUpdateTime:false"`
}

func (invitationRow) TableName() string { return tableInvitations }

// toInvitation is the view a row stands for. accepted_at's NULL reads as the
// zero time.
func toInvitation(r invitationRow) Invitation {
	s := InvitationPending
	switch r.Status {
	case statusAccepted:
		s = InvitationAccepted
	case statusRevoked:
		s = InvitationRevoked
	}
	return Invitation{
		ID:         r.ID,
		Tenant:     r.TenantID,
		Email:      r.Email,
		Status:     s,
		InvitedBy:  authn.AccountID(r.InvitedBy),
		ExpiresAt:  clock.UTC(r.ExpiresAt),
		AcceptedAt: timeOf(r.AcceptedAt),
		CreatedAt:  clock.UTC(r.CreatedAt),
	}
}

// timeOf is a nullable column read as the zero time when it is NULL.
func timeOf(t *time.Time) time.Time {
	if t == nil {
		return time.Time{}
	}
	return clock.UTC(*t)
}

// utc is a time as the database stores and compares it.
func utc(t time.Time) time.Time { return clock.UTC(t).Truncate(time.Microsecond) }

// findByDigest reads the invitation a token digest points to. The second result
// is false when no row carries it. The lookup carries no tenant predicate: an
// acceptor has no tenant yet, which is why token_digest is globally unique.
func (p *product) findByDigest(tx *gorm.DB, digest string) (invitationRow, bool, error) {
	var rows []invitationRow
	if err := tx.Where("token_digest = ?", digest).Limit(1).Find(&rows).Error; err != nil {
		return invitationRow{}, false, failed("reading an invitation", err)
	}
	if len(rows) == 0 {
		return invitationRow{}, false, nil
	}
	return rows[0], true, nil
}

// findByID reads one invitation of a tenant by its identifier. The tenant is
// part of the key, so one tenant's identifier never reaches another's row.
func (p *product) findByID(tx *gorm.DB, tenant, id string) (invitationRow, bool, error) {
	var rows []invitationRow
	if err := tx.Where("tenant_id = ? AND id = ?", tenant, id).Limit(1).Find(&rows).Error; err != nil {
		return invitationRow{}, false, failed("reading an invitation", err)
	}
	if len(rows) == 0 {
		return invitationRow{}, false, nil
	}
	return rows[0], true, nil
}

// duplicate reports whether a statement was refused by a unique constraint,
// which the database module classifies the same way on both engines.
func duplicate(err error) bool { return errors.Is(err, gorm.ErrDuplicatedKey) }
