package admin

import (
	"time"

	"github.com/vislake/speed/pkg/sms"
)

type templateView = SmsAdminTemplate

type approvalView = SmsAdminApproval

type carrierView = SmsAdminCarrier

func templateViewOf(r sms.Record) templateView {
	approvals := make(map[string]approvalView, len(r.Approvals))
	for locale, approval := range r.Approvals {
		approvals[locale] = approvalView{State: string(approval.State), Reason: approval.Reason}
	}
	return templateView{
		Name:       r.Name,
		Carrier:    r.Carrier,
		Kind:       string(r.Kind),
		Source:     string(r.Source),
		Params:     append([]string(nil), r.Params...),
		Locales:    append([]string(nil), r.Locales...),
		Bodies:     copyStrings(r.Bodies),
		Attributes: copyStrings(r.Attributes),
		Approvals:  approvals,
		Enabled:    r.Enabled,
		Version:    r.Version,
		CreatedAt:  utcPtr(r.CreatedAt),
		UpdatedAt:  utcPtr(r.UpdatedAt),
	}
}

func utcPtr(t time.Time) *time.Time {
	if t.IsZero() {
		return nil
	}
	at := t.UTC()
	return &at
}
