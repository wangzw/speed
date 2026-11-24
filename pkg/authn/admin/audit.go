package admin

import (
	"time"

	"github.com/google/uuid"

	"github.com/vislake/speed/pkg/audit"
)

const (
	actionAccountCreate    = "authn.account.create"
	actionAccountUpdate    = "authn.account.update"
	actionAccountDisable   = "authn.account.disable"
	actionAccountDelete    = "authn.account.delete"
	actionAccountAuthReset = "authn.account.password"
	actionMFAReset         = "authn.mfa.reset"
	actionSessionRevoke    = "authn.session.revoke"
	actionMembershipSet    = "authn.membership.set"
	actionMembershipRemove = "authn.membership.remove"
	actionIDPPut           = "authn.idp.put"
	actionIDPDelete        = "authn.idp.delete"
	actionIDPDomain        = "authn.idp.domain"
	actionServiceKeyCreate = "authn.secret.create"
	actionServiceKeyRevoke = "authn.secret.revoke"
)

const (
	outcomeSuccess = "success"
	outcomeFailed  = "failed"
)

const (
	detailActionID = "action_id"
	detailTenant   = "tenant"
	detailKind     = "kind"
	detailChange   = "change"
	detailSource   = "source"
	detailSecretID = "secret_id"
	detailProvider = "provider"
)

func newActionID() string { return uuid.NewString() }

func (m *product) now() time.Time { return time.Now() }

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
