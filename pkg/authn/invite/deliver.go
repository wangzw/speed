package invite

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/i18n"
)

// notifyTimeout bounds one notice. The issue answer waits for it: a carrier
// that stops answering delays the answer by no more than this, and the outcome
// is reported as failed rather than the issuance failing.
const notifyTimeout = 5 * time.Second

// errNotifierPanicked reports a notifier that stopped without returning. It is
// a delivery failure like any other, so it is logged and audited and never
// reaches the issue answer.
var errNotifierPanicked = errors.New("authn.invite: the invitation notifier panicked")

// deliverInvite asks the optional notifier to deliver the acceptance notice and
// reports the outcome the issue answer carries. A missing notifier, and a
// notifier that declares the deployment sends no mail, are InviteNotConfigured;
// nothing was attempted, so neither is a failure and neither writes an audit
// row. Any other outcome that is not a send is InviteFailed: it is logged and
// closed with a best-effort delivery audit row, and the invitation stays issued.
//
// The token is the whole credential of acceptance and must not reach a log, an
// audit row or an error text; this function writes none of it.
func (p *product) deliverInvite(ctx context.Context, issued Issued, actionID string) authn.InviteDelivery {
	if p.notifier == nil {
		return authn.InviteNotConfigured
	}
	locale := ""
	if named, ok := i18n.FromContext(ctx); ok {
		locale = string(named)
	}
	// The tenant the invitation names is the name a recipient can read, never
	// the internal identifier the row stores. With no resolver assembled, or one
	// that cannot answer, the notice names no tenant: the identifier must not
	// stand in for the name.
	tenantName, _ := p.nameOf(ctx, issued.Invitation.Tenant)
	// The inviter is named by the display name they are recognized by. A read
	// that fails does not stop the notice: an invitation without a named inviter
	// is still the link the recipient needs. The public token read is the one
	// caller that answers unavailable instead; the management views omit the two
	// name keys.
	inviter, inviterErr := p.inviterNameOf(ctx, issued.Invitation.InvitedBy)
	if inviterErr != nil {
		p.logger.Error(ModuleName+": the inviter's display name could not be read, so the notice names no inviter",
			slog.String("invitation", issued.Invitation.ID),
			slog.String("cause", inviterErr.Error()))
	}
	notice := authn.InviteNotice{
		Tenant:    tenantName,
		Inviter:   inviter,
		Email:     issued.Invitation.Email,
		Token:     issued.Token,
		ExpiresAt: issued.Invitation.ExpiresAt,
		Locale:    locale,
	}
	callCtx, cancel := context.WithTimeout(ctx, notifyTimeout)
	defer cancel()
	delivery, err := p.notify(callCtx, notice)
	switch {
	case err == nil && delivery == authn.InviteSent:
		return authn.InviteSent
	case err == nil && delivery == authn.InviteNotConfigured:
		return authn.InviteNotConfigured
	}
	cause := "the delivery outcome is outside the set"
	if err != nil {
		cause = err.Error()
	}
	p.logger.Error(ModuleName+": the invitation notice was not delivered, so the acceptance link is only in the answer",
		slog.String("tenant", tenantName),
		slog.String("invitation", issued.Invitation.ID),
		slog.String("delivery", delivery.String()),
		slog.String("cause", cause))
	p.auditBestEffort(ctx, deliveryAudit(issued.Invitation.ID, tenantName, actionID))
	return authn.InviteFailed
}

// notify calls the notifier and turns a panic into a delivery failure, so a
// defective notifier cannot turn a completed issuance into a 500.
func (p *product) notify(ctx context.Context, notice authn.InviteNotice) (delivery authn.InviteDelivery, err error) {
	defer func() {
		if recover() != nil {
			delivery, err = authn.InviteFailed, errNotifierPanicked
		}
	}()
	return p.notifier.Notify(ctx, notice)
}
