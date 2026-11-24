package authn

import "context"

// Action names a thing this capability asks a Decider about. A published value
// is never renamed, and a Decider that meets an Action it does not know decides
// itself whether to refuse it.
type Action string

// The actions a Decider is asked about.
const (
	ActionAccountRead         Action = "authn.account.read"
	ActionAccountCreate       Action = "authn.account.create"
	ActionAccountUpdate       Action = "authn.account.update"  // display name, attributes, protected, mfa_required, an administrator's password reset, adding an identifier
	ActionAccountDisable      Action = "authn.account.disable" // disabling and restoring
	ActionAccountDelete       Action = "authn.account.delete"
	ActionMembershipManage    Action = "authn.membership.manage" // reading and writing memberships, sso_exempt included
	ActionSessionRead         Action = "authn.session.read"      // reading another account's sessions
	ActionSessionRevoke       Action = "authn.session.revoke"    // revoking another account's sessions
	ActionMFAReset            Action = "authn.mfa.reset"
	ActionClientManage        Action = "authn.client.manage" // creating and revoking a service account's secrets
	ActionIDPManage           Action = "authn.idp.manage"    // domain claims and metadata refresh included
	ActionImpersonateStart    Action = "authn.impersonation.start"
	ActionImpersonationRead   Action = "authn.impersonation.read"
	ActionImpersonationRevoke Action = "authn.impersonation.revoke"
	ActionTenantAccess        Action = "authn.tenant.access" // a non-member entering a tenant: the admission layer asks, and the default is to refuse
)

// Decision is one question put to a Decider. Tenant is the tenant the action is
// about, empty for platform scope and for account-level actions only; a
// tenant-level action always names one. Params never carries a credential.
type Decision struct {
	// Action is the action being asked about.
	Action Action
	// Actor is the account whose hands are on the action.
	Actor Identity
	// Tenant is the tenant the action is about; empty means platform scope.
	Tenant string
	// Target names the object the action is on.
	Target string
	// TargetAccount is the account the action is on, where it is one.
	TargetAccount string
	// Params carries the action's extra parameters such as reason or ttl; never a credential.
	Params map[string]string
}

// Decider answers whether an actor may do one thing within this capability. Every
// module takes it as optional, and with none every question is refused. allow
// permits, false refuses, and a non-nil err or a panic in Decide is a 503.
type Decider interface {
	// Decide answers one Decision. It returns once ctx ends and is safe to call
	// from many goroutines.
	Decide(ctx context.Context, d Decision) (allow bool, err error)
}
