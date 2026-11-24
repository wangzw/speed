package ownership

import (
	"context"
	"errors"

	"github.com/vislake/speed/pkg/notification"
	"github.com/vislake/speed/pkg/tenant"
)

const (
	notificationTransferRequested = "tenant.owner.transfer_requested"
	notificationTransferConfirmed = "tenant.owner.transfer_confirmed"
	notificationTransferDeclined  = "tenant.owner.transfer_declined"
	notificationTransferExpired   = "tenant.owner.transfer_expired"
)

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
	locale, ok := p.noticeLocale(dispatchCtx, id, typeKey, recipient)
	if !ok {
		return
	}
	_, err := p.dispatcher.Dispatch(dispatchCtx, notification.Dispatch{
		TypeKey:      typeKey,
		Recipient:    notification.Recipient{Class: notification.RecipientUser, UserID: recipient},
		Locale:       locale,
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

func (p *product) noticeLocale(ctx context.Context, id tenant.ID, typeKey, recipient string) (string, bool) {
	readFailed := false
	for _, profile := range p.profiles {
		locale, ok, err := profile.LocaleFor(ctx, recipient)
		if err != nil {
			if !readFailed {
				readFailed = true
				p.logger.Warn(ModuleName+": the recipient's language preference could not be read through "+
					"one profile; the notice takes the next answer, or the default language when none answers",
					"type", typeKey, "tenant", string(id), "cause", err.Error())
			}
			continue
		}
		if ok && locale != "" {
			return string(locale), true
		}
	}
	if p.resolver == nil {
		p.logger.Warn(ModuleName+": no i18n engine is assembled, so the notice has no default language "+
			"to fall back to; the notice was not sent", "type", typeKey, "tenant", string(id))
		return "", false
	}
	return string(p.resolver.Default()), true
}
