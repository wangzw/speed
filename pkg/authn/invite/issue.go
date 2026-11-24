package invite

import (
	"context"
	"fmt"
	"strings"
	"time"
	"unicode"

	"gorm.io/gorm"

	"github.com/vislake/speed/pkg/authn"
)

const tenantMax = 255

const maxExplicitLifetime = 90 * 24 * time.Hour

func checkTenant(tenant string) error {
	if tenant == "" || len(tenant) > tenantMax || strings.IndexFunc(tenant, func(r rune) bool {
		return unicode.IsSpace(r) || unicode.IsControl(r)
	}) >= 0 {
		return fmt.Errorf("%w: the tenant is not an identifier of at most 255 bytes without blanks "+
			"or control characters", authn.ErrInvalidArgument)
	}
	return nil
}

// Issue issues one invitation. The address is normalized first, and an address
// that is not well formed is refused before any row is written.
func (p *product) Issue(ctx context.Context, in IssueInvite) (Issued, error) {
	return p.issue(ctx, in, "")
}

func (p *product) issue(ctx context.Context, in IssueInvite, id string) (Issued, error) {
	if err := checkTenant(in.Tenant); err != nil {
		return Issued{}, err
	}
	email, err := normalizeEmail(in.Email)
	if err != nil {
		return Issued{}, err
	}
	now := p.at()
	if budgetErr := p.chargeIssuer(ctx, now); budgetErr != nil {
		return Issued{}, budgetErr
	}
	deadline := in.ExpiresAt
	if deadline.IsZero() {
		deadline = now.Add(p.ttl())
	} else {
		deadline = utc(deadline)
		switch {
		case !deadline.After(now):
			return Issued{}, fmt.Errorf("%w: the deadline is not later than now", authn.ErrInvalidArgument)
		case deadline.After(now.Add(maxExplicitLifetime)):
			return Issued{}, fmt.Errorf("%w", ErrInvitationExpiryTooLong)
		}
	}
	if systemErr := p.refuseSystemTenant(ctx, in.Tenant); systemErr != nil {
		return Issued{}, systemErr
	}
	if addressErr := p.refuseIneligibleAddress(ctx, in.Tenant, email); addressErr != nil {
		return Issued{}, addressErr
	}
	if pendingErr := p.refusePendingInvitation(ctx, in.Tenant, email, now); pendingErr != nil {
		return Issued{}, pendingErr
	}
	if roleErr := p.requireGrantableRole(ctx, in.Tenant, in.Role); roleErr != nil {
		return Issued{}, roleErr
	}
	// A request identity with no account is refused: an empty issuer would skip
	// the inviter checks at acceptance.
	invitedBy, _, issuerErr := issuerOf(authn.FromContext(ctx))
	if issuerErr != nil {
		return Issued{}, issuerErr
	}
	if id == "" {
		id, err = newID()
		if err != nil {
			return Issued{}, err
		}
	}
	token, err := newToken()
	if err != nil {
		return Issued{}, err
	}
	release, budgetErr := p.chargeAttempt(ctx, email, now)
	if budgetErr != nil {
		return Issued{}, budgetErr
	}
	sent := now
	row := invitationRow{
		ID: id, TenantID: in.Tenant, Email: email, TokenDigest: tokenDigest(token),
		Status: statusPending, InvitedBy: invitedBy, Role: in.Role, ExpiresAt: utc(deadline),
		LastSentAt: sent, SendCount: 1,
		CreatedAt: now, UpdatedAt: now,
	}
	err = p.conn(ctx).Transaction(func(tx *gorm.DB) error {
		if supErr := tx.Model(&invitationRow{}).
			Where("tenant_id = ? AND email = ? AND status = ? AND expires_at <= ? AND superseded_at IS NULL",
				in.Tenant, email, statusPending, now).
			Updates(map[string]any{"superseded_at": now, "updated_at": now}).Error; supErr != nil {
			return supErr
		}
		return tx.Create(&row).Error
	})
	switch {
	case duplicate(err):
		release()
		return Issued{}, fmt.Errorf("%w", ErrInvitationPending)
	case err != nil:
		release()
		return Issued{}, failed("issuing an invitation", err)
	}
	return Issued{Invitation: toInvitation(row), Token: token}, nil
}
