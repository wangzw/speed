package tenantadmin

import (
	"github.com/google/uuid"

	"github.com/vislake/speed/pkg/audit"
)

// The audit action names this module writes, one per write route. They carry
// the tenant role vocabulary so a reader can tell a tenant-surface write from
// the platform surface's authz.role.put and its siblings. A read writes no
// audit row of its own, and every write is one of these six. A name is written
// for the mandatory row a write lands before its action and, when the action
// fails, for the failed row that links to it.
const (
	actionRolePut       = "authz.tenant.role.put"
	actionRoleDelete    = "authz.tenant.role.delete"
	actionGrantPut      = "authz.tenant.grant.put"
	actionGrantDelete   = "authz.tenant.grant.delete"
	actionBindingPut    = "authz.tenant.binding.put"
	actionBindingDelete = "authz.tenant.binding.delete"
)

// The outcomes this module writes. The permitted value of a mandatory row is
// success; a write the manager refuses is closed with a failed row that carries
// the same action identifier.
const (
	outcomeSuccess = "success"
	outcomeFailed  = "failed"
)

// The detail keys this module writes, a closed set: the action identifier that
// links a write's rows, the tenant and the natural key of the row. No credential
// and no policy secret ever enters one.
const (
	detailActionID   = "action_id"
	detailTenant     = "tenant"
	detailRole       = "role"
	detailPermission = "permission"
	detailAccount    = "account"
	detailChange     = "change"
)

// roleChange names the kind of record a role write changes, for the audit row's
// change key.
const roleChange = "role"

// newActionID mints the action identifier a write's audit rows are linked by. It
// is made after the decision and before the mandatory row, so the permitted row
// and any failed row beside it carry the same value.
func newActionID() string { return uuid.NewString() }

// mandatory builds the entry of a write's mandatory audit row: the action, the
// object it is on, the success outcome and the detail.
func mandatory(action, object string, detail map[string]string) audit.Entry {
	if detail == nil {
		detail = map[string]string{}
	}
	return audit.Entry{Action: action, Object: object, Outcome: outcomeSuccess, Detail: detail}
}

// failedAudit builds the failed row that closes a write whose action returned an
// error, linked to the permitted row by the action identifier.
func failedAudit(action, object, actionID string) audit.Entry {
	return audit.Entry{
		Action:  action,
		Object:  object,
		Outcome: outcomeFailed,
		Detail:  map[string]string{detailActionID: actionID},
	}
}
