package tenantadmin

import (
	"github.com/google/uuid"

	"github.com/vislake/speed/pkg/audit"
)

// The audit action names this module writes, one per write route.
const (
	actionRolePut       = "authz.tenant.role.put"
	actionRoleDelete    = "authz.tenant.role.delete"
	actionGrantPut      = "authz.tenant.grant.put"
	actionGrantDelete   = "authz.tenant.grant.delete"
	actionBindingPut    = "authz.tenant.binding.put"
	actionBindingDelete = "authz.tenant.binding.delete"
)

// The outcomes this module writes.
const (
	outcomeSuccess = "success"
	outcomeFailed  = "failed"
)

// The detail keys this module writes, a closed set. No credential and no policy
// secret ever enters one.
const (
	detailActionID   = "action_id"
	detailTenant     = "tenant"
	detailRole       = "role"
	detailPermission = "permission"
	detailAccount    = "account"
	detailChange     = "change"
)

// roleChange names the kind of record a role write changes.
const roleChange = "role"

// newActionID mints the action identifier a write's audit rows are linked by.
func newActionID() string { return uuid.NewString() }

// mandatory builds the entry of a write's mandatory audit row.
func mandatory(action, object string, detail map[string]string) audit.Entry {
	if detail == nil {
		detail = map[string]string{}
	}
	return audit.Entry{Action: action, Object: object, Outcome: outcomeSuccess, Detail: detail}
}

// failedAudit builds the failed row that closes a write whose action returned an
// error.
func failedAudit(action, object, actionID string) audit.Entry {
	return audit.Entry{
		Action:  action,
		Object:  object,
		Outcome: outcomeFailed,
		Detail:  map[string]string{detailActionID: actionID},
	}
}
