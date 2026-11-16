package invite

import (
	"context"
	"fmt"

	"gorm.io/gorm"
)

// Resend sends an invitation again without creating a new one: the same row
// keeps its identity and its deadline, and only its token, its last send time
// and its send count change. The old token stops working as soon as
// the new digest is written, and a lookup of it answers no invitation.
//
// Only a pending invitation that has not expired can be resent. A terminal row
// — accepted, revoked or expired — is refused, and an identifier that names no
// row of the tenant is ErrInvitationNotFound. The send budgets are charged the
// way an issuance charges them: the issuing account's budget before the terminal
// check, so a terminal row still costs the operator; and the addressed address's
// and the deployment's only once the row is known to be live, counted only when
// the resend's write lands. A resend spends the same send budgets but is not a
// new issuance: it re-judges nothing about the target, which was judged when the
// invitation was issued.
//
// The conditional write makes the check and the change one statement: a row
// that became terminal in between, or one a later invitation superseded, leaves
// the update affecting nothing, and the re-read answers the state it is in. The
// answer for a write that landed is built from the copy read before it and the
// values the write set, so a statement after the link replaced cannot turn a
// sent invitation into an error.
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
	// The account's budget is spent before the terminal check, so an attempt
	// against a row that cannot be sent still costs the operator.
	if budgetErr := p.chargeIssuer(ctx, now); budgetErr != nil {
		return Issued{}, budgetErr
	}
	if terminal := terminalState(held, now); terminal != nil {
		return Issued{}, terminal
	}
	// The row is live, so this resend sends: the address's and the deployment's
	// budgets are charged before the write and given back if the write lands
	// nowhere.
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
		// The write landed, so the answer describes the copy read before it with
		// the values this write set. send_count is that copy's count plus one, not
		// the stored count after the write: a concurrent resend that landed between
		// the read and this write leaves the answer one behind the stored count,
		// which stays authoritative. A second read would let a statement after the
		// token had already replaced the old link turn a sent invitation into an
		// error, losing the new token and the notice.
		written := held
		written.TokenDigest = tokenDigest(token)
		written.LastSentAt = utc(now)
		written.SendCount++
		written.UpdatedAt = utc(now)
		return Issued{Invitation: toInvitation(written), Token: token}, nil
	}
	// The conditional write met no live row: the resend did not send, so the
	// address and deployment charges are given back before re-reading.
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
	// A pending row that appeared again between the two statements is a race
	// this call does not loop on; report it as not found to keep the answer
	// closed.
	return Issued{}, fmt.Errorf("%w", ErrInvitationNotFound)
}
