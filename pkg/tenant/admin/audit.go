package admin

import (
	"github.com/google/uuid"

	"github.com/vislake/speed/pkg/audit"
)

const (
	actionTenantCreate               = "tenant.create"
	actionTenantStatus               = "tenant.status"
	actionTenantProfileUpdate        = "tenant.profile.update"
	actionTenantOwnerTransfer        = "tenant.owner.transfer"
	actionTenantOwnerTransferRequest = "tenant.owner.transfer.request"
	actionTenantOwnerTransferCancel  = "tenant.owner.transfer.cancel"
	actionTenantOwnerTransferDecline = "tenant.owner.transfer.decline"
	actionTenantDelete               = "tenant.delete"
)

const (
	outcomeSuccess = "success"
	outcomeFailed  = "failed"
)

const (
	detailActionID = "action_id"
	detailTenant   = "tenant"
	detailSurface  = "surface"
	detailTarget   = "target"
	detailRequest  = "request"
)

const surfacePlatform = "platform"

const surfaceTenant = "tenant"

const surfaceTarget = "target"

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

func failedProfileAudit(action, object, actionID string) audit.Entry {
	return audit.Entry{
		Action:  action,
		Object:  object,
		Outcome: outcomeFailed,
		Detail: map[string]string{
			detailActionID: actionID,
			detailTenant:   object,
			detailSurface:  surfacePlatform,
		},
	}
}
