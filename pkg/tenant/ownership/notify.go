package ownership

import (
	"context"
	"errors"

	"github.com/vislake/speed/pkg/i18n"
	"github.com/vislake/speed/pkg/notification"
	"github.com/vislake/speed/pkg/tenant"
)

// The notification types this module declares and dispatches. The request
// notice goes to the target; the results go to the account that made the
// request.
const (
	notificationTransferRequested = "tenant.owner.transfer_requested"
	notificationTransferConfirmed = "tenant.owner.transfer_confirmed"
	notificationTransferDeclined  = "tenant.owner.transfer_declined"
	notificationTransferExpired   = "tenant.owner.transfer_expired"
)

// notify dispatches one in-app notice. The whole path is best effort: a missing
// dispatcher, a tenant the roster cannot enter, and a failed dispatch are logged
// as warnings and change nothing about the write that already committed. The
// tenant is entered with EnterRegistered so a notice about a disabled
// organization still lands its inbox row.
func (p *product) notify(ctx context.Context, id tenant.ID, typeKey, recipient string, params map[string]any, occurrenceID string) {
	if p.dispatcher == nil {
		p.logger.Warn(ModuleName+": no notification dispatcher is assembled; the notice was not sent",
			"type", typeKey, "tenant", string(id))
		return
	}
	dispatchCtx := ctx
	if p.roster != nil {
		entered, err := p.roster.EnterRegistered(ctx, id)
		if err != nil {
			p.logger.Warn(ModuleName+": the notice's tenant could not be entered; the notice was not sent",
				"type", typeKey, "tenant", string(id), "cause", err.Error())
			return
		}
		dispatchCtx = entered
	}
	locale := i18n.Locale("en-US")
	if current, ok := i18n.FromContext(ctx); ok && current != "" {
		locale = current
	}
	_, err := p.dispatcher.Dispatch(dispatchCtx, notification.Dispatch{
		TypeKey:      typeKey,
		Recipient:    notification.Recipient{Class: notification.RecipientUser, UserID: recipient},
		Locale:       string(locale),
		Params:       params,
		OccurrenceID: occurrenceID,
	})
	if err != nil {
		if errors.Is(err, notification.ErrUnknownType) {
			p.logger.Warn(ModuleName+": the notice's type is not declared in the assembled taxonomy; "+
				"the notice was not sent", "type", typeKey, "cause", err.Error())
			return
		}
		p.logger.Warn(ModuleName+": the notice could not be dispatched; the write it describes stands",
			"type", typeKey, "tenant", string(id), "cause", err.Error())
	}
}
