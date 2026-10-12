package admin

import (
	"time"

	"github.com/google/uuid"

	"github.com/vislake/speed/pkg/audit"
)

// The audit action names this module writes, taken from the capability's closed
// action table. A name is written for the mandatory row a write lands before its
// action and, when the action fails, for the failed row that links to it.
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

// The outcomes this module writes. The permitted value of a mandatory row is
// success; a write that fails is closed with a failed row that carries the same
// action identifier.
const (
	outcomeSuccess = "success"
	outcomeFailed  = "failed"
)

// The detail keys this module writes, all of them from the capability's closed
// set. No credential, temporary password, secret or token ever enters one.
const (
	detailActionID = "action_id"
	detailTenant   = "tenant"
	detailKind     = "kind"
	detailChange   = "change"
	detailSource   = "source"
	detailSecretID = "secret_id"
	detailProvider = "provider"
)

// newActionID mints the action identifier a write's audit rows are linked by. It
// is made after the decision and before the mandatory row, so the permitted row
// and any failed row beside it carry the same value.
func newActionID() string { return uuid.NewString() }

// now is the instant a strength check is judged against.
func (m *product) now() time.Time { return time.Now() }

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
