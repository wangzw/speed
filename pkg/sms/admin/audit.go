package admin

import (
	"github.com/google/uuid"

	"github.com/vislake/speed/pkg/audit"
)

const (
	actionTemplateRegister = "sms.template.register"
	actionTemplateOffer    = "sms.template.offer"
	actionTemplateEnable   = "sms.template.enable"
	actionTemplateRemove   = "sms.template.remove"
)

const (
	outcomeSuccess = "success"
	outcomeFailed  = "failed"
)

// The detail keys this module writes are a closed set; no credential and no
// template code ever enters one, and a template is named by its logical name.
const (
	detailActionID = "action_id"
	detailTemplate = "template"
	detailEnabled  = "enabled"
)

func newActionID() string { return uuid.NewString() }

func mandatory(action, object string, detail map[string]string) audit.Entry {
	if detail == nil {
		detail = map[string]string{}
	}
	return audit.Entry{Action: action, Object: object, Outcome: outcomeSuccess, Detail: detail}
}

func failedAudit(action, object, actionID string) audit.Entry {
	return audit.Entry{
		Action:  action,
		Object:  object,
		Outcome: outcomeFailed,
		Detail:  map[string]string{detailActionID: actionID},
	}
}
