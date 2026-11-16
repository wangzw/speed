package ownership

import (
	"context"
	"errors"

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

// notify dispatches one in-app notice in the recipient's language. The whole
// path is best effort: a missing dispatcher, a tenant the roster cannot enter,
// a language nothing can answer, and a failed dispatch are logged as warnings
// and change nothing about the write that already committed. The tenant is
// entered with EnterRegistered so a notice about a disabled organization still
// lands its inbox row.
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

// noticeLocale answers the language a notice to recipient goes out in: the
// recipient's stored preference, and the deployment's configured default when
// none is stored. It never consults the language of ctx. The context belongs to
// the account that triggered the write, and the notice belongs to the account
// that receives it; confirming or declining a request is the case where the two
// accounts read different languages.
//
// A preference a profile cannot read leaves one warning and does not end the
// search: the remaining profiles are asked, because one seam's failed read says
// nothing about another's answer. The fallback is the deployment default when
// no profile answers, and the notification is skipped only when neither a stored
// preference nor a default is reachable. The warning names the notice's type and
// tenant, never the recipient.
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
