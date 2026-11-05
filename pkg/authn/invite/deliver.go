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
	notice := authn.InviteNotice{
		Tenant:    issued.Invitation.Tenant,
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
		slog.String("tenant", issued.Invitation.Tenant),
		slog.String("invitation", issued.Invitation.ID),
		slog.String("delivery", delivery.String()),
		slog.String("cause", cause))
	p.auditBestEffort(ctx, deliveryAudit(issued.Invitation.ID, issued.Invitation.Tenant, actionID))
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
