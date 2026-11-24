package invite

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/i18n"
)

// notifyTimeout bounds one notice; the issue answer waits for it.
const notifyTimeout = 5 * time.Second

var errNotifierPanicked = errors.New("authn.invite: the invitation notifier panicked")

func (p *product) deliverInvite(ctx context.Context, issued Issued, actionID string) authn.InviteDelivery {
	if p.notifier == nil {
		return authn.InviteNotConfigured
	}
	locale := ""
	if named, ok := i18n.FromContext(ctx); ok {
		locale = string(named)
	}
	tenantName, _ := p.nameOf(ctx, issued.Invitation.Tenant)
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

func (p *product) notify(ctx context.Context, notice authn.InviteNotice) (delivery authn.InviteDelivery, err error) {
	defer func() {
		if recover() != nil {
			delivery, err = authn.InviteFailed, errNotifierPanicked
		}
	}()
	return p.notifier.Notify(ctx, notice)
}
