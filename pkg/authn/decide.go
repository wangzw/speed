package authn

import "context"

// Action names a thing this capability asks a Decider about. The set of
// constants is the whole of what is asked, and the values are a promise to the
// permission model: a published value is never renamed, and a new constant never
// changes an existing one. A constant belongs here when it is an administration
// action on another account or on a tenant-level resource, the start or the
// reading and writing of an impersonation, or a non-member entering a tenant.
//
// A Decider that meets an Action it does not know decides whether to refuse or
// allow it; this capability cannot tighten that for it.
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

// Decision is one question put to a Decider.
//
// Tenant is the tenant the action is about. The empty string means platform
// scope and is for account-level actions only: for a tenant-level action the
// administration module resolves the tenant of the object itself and fills it
// in, so a Decider never receives a tenant-level action with no tenant. The
// tenant is the object the action operates on, not by itself the policy domain:
// the Decider decides which domain to judge in, and the three impersonation
// actions are platform capabilities, decided in the platform domain whatever
// tenant they name. Target names the object the action is on, TargetAccount the
// account it is on where there is one, and Params carries a few more parameters
// such as reason or ttl, never a credential.
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

// Decider answers whether an actor may do one thing within this capability. The
// host or the permission model delivers it, exclusively.
//
// Every module that asks takes it as optional, and an assembly with no Decider
// is a valid one in which every question is refused: the default is in the
// caller and not in a module that refuses. This package holds no allow list.
//
// allow true permits and false refuses. A non-nil err is a 503 and is neither a
// refusal nor a permission; the caller takes a panic in Decide to be an err, and
// the action is not carried out.
type Decider interface {
	// Decide answers one Decision. It returns once ctx ends and is safe to call
	// from many goroutines.
	Decide(ctx context.Context, d Decision) (allow bool, err error)
}
