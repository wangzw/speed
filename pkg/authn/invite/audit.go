package invite

import (
	"github.com/google/uuid"

	"github.com/vislake/speed/pkg/audit"
)

// The audit action names this module writes, one per write route. A read writes
// no audit row of its own.
const (
	actionIssue  = "authn.invitation.issue"
	actionRevoke = "authn.invitation.revoke"
)

// The outcomes this module writes. The permitted value of a mandatory row is
// success; a write that fails is closed with a failed row carrying the same
// action identifier.
const (
	outcomeSuccess = "success"
	outcomeFailed  = "failed"
)

// The detail keys this module writes, drawn from the capability's closed set.
// No address and no token ever enters one.
const (
	detailActionID = "action_id"
	detailTenant   = "tenant"
)

// newActionID mints the action identifier a write's audit rows are linked by. It
// is made after the decision and before the mandatory row.
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
