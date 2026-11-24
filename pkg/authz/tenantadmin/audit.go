package tenantadmin

import (
	"github.com/google/uuid"

	"github.com/vislake/speed/pkg/audit"
)

const (
	actionRolePut       = "authz.tenant.role.put"
	actionRoleDelete    = "authz.tenant.role.delete"
	actionGrantPut      = "authz.tenant.grant.put"
	actionGrantDelete   = "authz.tenant.grant.delete"
	actionBindingPut    = "authz.tenant.binding.put"
	actionBindingDelete = "authz.tenant.binding.delete"
)

const (
	outcomeSuccess = "success"
	outcomeFailed  = "failed"
)

const (
	detailActionID   = "action_id"
	detailTenant     = "tenant"
	detailRole       = "role"
	detailPermission = "permission"
	detailAccount    = "account"
	detailChange     = "change"
)

const roleChange = "role"

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
