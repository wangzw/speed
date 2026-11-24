package invite

import (
	"github.com/google/uuid"

	"github.com/vislake/speed/pkg/audit"
)

const (
	actionIssue   = "authn.invitation.issue"
	actionResend  = "authn.invitation.resend"
	actionRevoke  = "authn.invitation.revoke"
	actionAccept  = "authn.invitation.accept"
	actionDeliver = "authn.invitation.deliver"
)

const (
	outcomeSuccess = "success"
	outcomeFailed  = "failed"
)

// The detail keys this module writes. No address and no token ever enters one.
const (
	detailActionID  = "action_id"
	detailTenant    = "tenant"
	detailRole      = "role"
	detailInvitedBy = "invited_by"
	detailReason    = "reason_class"
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

func deliveryAudit(object, tenant, actionID string) audit.Entry {
	return audit.Entry{
		Action:  actionDeliver,
		Object:  object,
		Outcome: outcomeFailed,
		Detail:  map[string]string{detailActionID: actionID, detailTenant: tenant},
	}
}
