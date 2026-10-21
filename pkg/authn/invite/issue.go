package invite

import (
	"context"
	"fmt"
	"strings"
	"unicode"

	"gorm.io/gorm"

	"github.com/vislake/speed/pkg/authn"
)

// tenantMax is the longest a tenant identifier may be.
const tenantMax = 255

// checkTenant refuses a tenant that cannot name one: empty, too long, or
// holding a blank or a control character. The refusal is ErrInvalidArgument and
// names no value.
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
// that is not well formed is refused before any row is written. In one
// transaction a pending invitation for the same tenant and address is
// superseded and the new row is inserted; the partial unique index arbitrates a
// concurrent issuance of the same pair, which is ErrInvitationPending.
//
// The issuer is read from the request's identity, when there is one: a host's
// own command issuing an invitation has no HTTP identity, and the issuer is then
// empty.
func (p *product) Issue(ctx context.Context, in IssueInvite) (Issued, error) {
	if err := checkTenant(in.Tenant); err != nil {
		return Issued{}, err
	}
	email, err := normalizeEmail(in.Email)
	if err != nil {
		return Issued{}, err
	}
	now := p.at()
	deadline := in.ExpiresAt
	if deadline.IsZero() {
		deadline = now.Add(p.ttl())
	} else if !utc(deadline).After(now) {
		return Issued{}, fmt.Errorf("%w: the deadline is not later than now", authn.ErrInvalidArgument)
	}
	invitedBy := ""
	if id, ok := authn.FromContext(ctx); ok {
		invitedBy = string(id.Actor())
	}
	id, err := newID()
	if err != nil {
		return Issued{}, err
	}
	token, err := newToken()
	if err != nil {
		return Issued{}, err
	}
	row := invitationRow{
		ID: id, TenantID: in.Tenant, Email: email, TokenDigest: tokenDigest(token),
		Status: statusPending, InvitedBy: invitedBy, ExpiresAt: utc(deadline),
		CreatedAt: now, UpdatedAt: now,
	}
	err = p.conn(ctx).Transaction(func(tx *gorm.DB) error {
		// Supersede a pending invitation for the same tenant and address: the
		// old link is void from here on.
		if supErr := tx.Model(&invitationRow{}).
			Where("tenant_id = ? AND email = ? AND status = ?", in.Tenant, email, statusPending).
			Updates(map[string]any{"status": statusRevoked, "updated_at": now}).Error; supErr != nil {
			return supErr
		}
		return tx.Create(&row).Error
	})
	switch {
	case duplicate(err):
		return Issued{}, fmt.Errorf("%w", ErrInvitationPending)
	case err != nil:
		return Issued{}, failed("issuing an invitation", err)
	}
	return Issued{Invitation: toInvitation(row), Token: token}, nil
}
