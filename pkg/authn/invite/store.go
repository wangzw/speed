package invite

import (
	"errors"
	"time"

	"gorm.io/gorm"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/authn/internal/clock"
)

// tableInvitations is the one table this module owns; a migration file, the
// model and a raw statement all name it.
const tableInvitations = "authn_invitations"

const (
	statusPending  int16 = 0
	statusAccepted int16 = 1
	statusRevoked  int16 = 2
)

// invitationRow is one row of authn_invitations. Its two time columns are filled
// explicitly in UTC, through internal/clock, so the gorm auto hooks are off.
type invitationRow struct {
	ID           string     `gorm:"column:id;primaryKey"`
	TenantID     string     `gorm:"column:tenant_id"`
	Email        string     `gorm:"column:email"`
	TokenDigest  string     `gorm:"column:token_digest"`
	Status       int16      `gorm:"column:status"`
	InvitedBy    string     `gorm:"column:invited_by"`
	Role         string     `gorm:"column:role"`
	ExpiresAt    time.Time  `gorm:"column:expires_at"`
	AcceptedAt   *time.Time `gorm:"column:accepted_at"`
	RevokedAt    *time.Time `gorm:"column:revoked_at"`
	LastSentAt   time.Time  `gorm:"column:last_sent_at"`
	SendCount    int        `gorm:"column:send_count"`
	SupersededAt *time.Time `gorm:"column:superseded_at"`
	CreatedAt    time.Time  `gorm:"column:created_at;autoCreateTime:false"`
	UpdatedAt    time.Time  `gorm:"column:updated_at;autoUpdateTime:false"`
}

func (invitationRow) TableName() string { return tableInvitations }

func toInvitation(r invitationRow) Invitation {
	s := InvitationPending
	switch r.Status {
	case statusAccepted:
		s = InvitationAccepted
	case statusRevoked:
		s = InvitationRevoked
	}
	return Invitation{
		ID:           r.ID,
		Tenant:       r.TenantID,
		Email:        r.Email,
		Status:       s,
		InvitedBy:    authn.AccountID(r.InvitedBy),
		Role:         r.Role,
		ExpiresAt:    clock.UTC(r.ExpiresAt),
		AcceptedAt:   nullableUTC(r.AcceptedAt),
		RevokedAt:    nullableUTC(r.RevokedAt),
		LastSentAt:   clock.UTC(r.LastSentAt),
		SendCount:    r.SendCount,
		SupersededAt: nullableUTC(r.SupersededAt),
		CreatedAt:    clock.UTC(r.CreatedAt),
	}
}

func nullableUTC(t *time.Time) *time.Time {
	if t == nil {
		return nil
	}
	at := clock.UTC(*t)
	return &at
}

func utc(t time.Time) time.Time { return clock.UTC(t).Truncate(time.Microsecond) }

// findByDigest reads the invitation a digest points to. It carries no tenant
// predicate: an acceptor has no tenant yet, which token_digest's global
// uniqueness makes safe.
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

// findByID reads one invitation of a tenant; the tenant is part of the key.
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

func duplicate(err error) bool { return errors.Is(err, gorm.ErrDuplicatedKey) }
