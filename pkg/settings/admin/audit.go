package admin

import (
	"github.com/google/uuid"

	"github.com/vislake/speed/pkg/audit"
)

const (
	actionSet    = "settings.set"
	actionDelete = "settings.delete"
)

const (
	outcomeSuccess = "success"
	outcomeFailed  = "failed"
)

// The detail keys this module writes are a closed set; no value ever enters one.
const (
	detailActionID = "action_id"
	detailKey      = "key"
	detailLayer    = "layer"
	detailTenant   = "tenant"
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
