package admin

import (
	"github.com/google/uuid"

	"github.com/vislake/speed/pkg/audit"
)

// The audit action names this module writes, one per write route. The set
// mirrors the permission vocabulary: a read writes no audit row of its own, and
// every write is one of these five. A name is written for the mandatory row a
// write lands before its action and, when the action fails, for the failed row
// that links to it.
const (
	actionTenantCreate        = "tenant.create"
	actionTenantStatus        = "tenant.status"
	actionTenantProfileUpdate = "tenant.profile.update"
	actionTenantOwnerTransfer = "tenant.owner.transfer"
	actionTenantDelete        = "tenant.delete"
)

// The outcomes this module writes. The permitted value of a mandatory row is
// success; a write the roster refuses is closed with a failed row that carries
// the same action identifier.
const (
	outcomeSuccess = "success"
	outcomeFailed  = "failed"
)

// The detail keys this module writes, a closed set: the action identifier that
// links a write's rows, and the tenant the write is on. A tenant is named by its
// identifier — the value the roster keys a row by and the logs and diagnostics
// name — never by its alias, which may be the very thing being changed.
const (
	detailActionID = "action_id"
	detailTenant   = "tenant"
	// detailSurface names the surface a profile update came through. The tenant
	// profile module writes the same action with the value tenant, so an audit
	// reader can tell the two apart.
	detailSurface = "surface"
)

// surfacePlatform is the surface a platform staff member's write comes through.
const surfacePlatform = "platform"

// surfaceTenant is the surface an organization's own owner's write comes
// through: the tenant-surface routes under /api/v1/tenants.
const surfaceTenant = "tenant"

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

// failedProfileAudit closes a profile write whose roster call failed. Both rows
// of the write name the tenant and the surface, so the failed row is
// attributable without reading the permitted one.
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
