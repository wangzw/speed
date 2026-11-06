package profile

import (
	"github.com/google/uuid"

	"github.com/vislake/speed/pkg/audit"
)

// The audit action this module writes. A read writes no audit row; the one
// write is the profile update.
const actionTenantProfileUpdate = "tenant.profile.update"

// The outcomes this module writes. The permitted value of a mandatory row is
// success; a write the roster refused is closed with a failed row that carries
// the same action identifier.
const (
	outcomeSuccess = "success"
	outcomeFailed  = "failed"
)

// The detail keys this module writes, a closed set: the action identifier that
// links a write's rows, the tenant the write is on, and the surface the write
// came through. A tenant is named by its alias, the name a reader of the trail
// can act on; the internal identifier stays out of the row.
const (
	detailActionID = "action_id"
	detailTenant   = "tenant"
	detailSurface  = "surface"
)

// surfaceTenant is the surface a tenant administrator's write comes through.
// The platform management surface writes the same action with the value
// platform, so an audit reader can tell the two apart.
const surfaceTenant = "tenant"

// newActionID mints the action identifier a write's audit rows are linked by.
func newActionID() string { return uuid.NewString() }

// mandatory builds the entry of a write's mandatory audit row.
func mandatory(action, object string, detail map[string]string) audit.Entry {
	if detail == nil {
		detail = map[string]string{}
	}
	return audit.Entry{Action: action, Object: object, Outcome: outcomeSuccess, Detail: detail}
}

// failedAudit builds the failed row that closes a write whose action returned
// an error, linked to the permitted row by the action identifier. It carries
// the surface as well, so both rows of one write name where it came through.
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
