package profile

import (
	"github.com/google/uuid"

	"github.com/vislake/speed/pkg/audit"
)

const actionTenantProfileUpdate = "tenant.profile.update"

const (
	outcomeSuccess = "success"
	outcomeFailed  = "failed"
)

const (
	detailActionID = "action_id"
	detailTenant   = "tenant"
	detailSurface  = "surface"
)

const surfaceTenant = "tenant"

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
		Detail: map[string]string{
			detailActionID: actionID,
			detailSurface:  surfaceTenant,
			detailTenant:   object,
		},
	}
}
