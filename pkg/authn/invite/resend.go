package invite

import (
	"context"
	"fmt"

	"gorm.io/gorm"
)

// Resend sends the invitation again on the same row: its identity and deadline
// stay, and only its token, last send time and send count change. The old token
// stops working at once.
func (p *product) Resend(ctx context.Context, tenant, id string) (Issued, error) {
	if tenant == "" || id == "" {
		return Issued{}, fmt.Errorf("%w", ErrInvitationNotFound)
	}
	now := p.at()
	held, found, err := p.findByID(p.conn(ctx), tenant, id)
	if err != nil {
		return Issued{}, err
	}
	if !found {
		return Issued{}, fmt.Errorf("%w", ErrInvitationNotFound)
	}
	// The account's budget is spent before the terminal check.
	if budgetErr := p.chargeIssuer(ctx, now); budgetErr != nil {
		return Issued{}, budgetErr
	}
	if terminal := terminalState(held, now); terminal != nil {
		return Issued{}, terminal
	}
	release, budgetErr := p.chargeAttempt(ctx, held.Email, now)
	if budgetErr != nil {
		return Issued{}, budgetErr
	}
	token, err := newToken()
	if err != nil {
		release()
		return Issued{}, err
	}
	res := p.conn(ctx).Model(&invitationRow{}).
		Where("tenant_id = ? AND id = ? AND status = ? AND expires_at > ? AND superseded_at IS NULL",
			tenant, id, statusPending, now).
		Updates(map[string]any{
			"token_digest": tokenDigest(token),
			"last_sent_at": now,
			"send_count":   gorm.Expr("send_count + 1"),
			"updated_at":   now,
		})
	if res.Error != nil {
		release()
		return Issued{}, failed("resending an invitation", res.Error)
	}
	if res.RowsAffected == 1 {
		written := held
		written.TokenDigest = tokenDigest(token)
		written.LastSentAt = utc(now)
		written.SendCount++
		written.UpdatedAt = utc(now)
		return Issued{Invitation: toInvitation(written), Token: token}, nil
	}
	release()
	fresh, still, err := p.findByID(p.conn(ctx), tenant, id)
	if err != nil {
		return Issued{}, err
	}
	if !still {
		return Issued{}, fmt.Errorf("%w", ErrInvitationNotFound)
	}
	if terminal := terminalState(fresh, now); terminal != nil {
		return Issued{}, terminal
	}
	return Issued{}, fmt.Errorf("%w", ErrInvitationNotFound)
}
