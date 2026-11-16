package authn

import (
	"context"
	"errors"
)

// InviteRolePolicy is the seam an invitation asks about the role it carries. It
// is declared here because the invitation module imports no package of the
// authorization capability; the policy module implements it and delivers it.
//
// The two questions are the two moments a role needs a judgement. Grantable is
// asked before an invitation is issued: the role has to exist in the tenant, the
// caller has to hold the tenant's role-management permission, the role must not
// be the platform administrator, every permission it carries must be within the
// caller's held set, and the host's write guard has to admit the binding. An
// empty account is a host command: it may bind roles directly, so the judgement
// is the role's existence, the platform-administrator refusal and the write
// guard, with no held set to compare against.
//
// StillGrantable is asked before an acceptance binds the role, and replays the
// issue-time judgement against the inviter: the inviter has to still be an
// active member of the tenant, still hold the tenant's role-management
// permission, and still hold every permission the role carries, and the role has
// to still exist and not be the platform administrator. An empty inviter checks
// only that the role exists, because a host command issued the invitation
// without an account whose standing could be replayed.
//
// The seam is optional where the invitation module takes it up. An invitation
// without a role needs neither question, so an assembly with no policy module
// still issues and accepts plain invitations. An invitation with a role in such
// an assembly fails closed: the module refuses to issue it, and refuses to
// accept one that somehow carries it.
type InviteRolePolicy interface {
	// Grantable reports whether account may grant role in tenant. It returns nil
	// when the role may be granted, ErrRoleUnknown when the tenant has no such
	// role, ErrRoleProtected when the role is the platform administrator or
	// carries a permission the account does not hold or the host's write guard
	// refuses the binding, and ErrForbidden when the account does not hold the
	// tenant's role-management permission. An empty account is a host command
	// and skips the held-set checks. Any other error is the judgement not being
	// available, which the caller treats as unavailable and never as a
	// permission.
	Grantable(ctx context.Context, tenant, account, role string) error
	// StillGrantable reports whether role may still be bound for inviter. It
	// returns nil when the role may be bound, ErrRoleUnknown when the tenant no
	// longer has the role, ErrRoleProtected when the role is the platform
	// administrator or a permission it carries is outside inviter's current held
	// set, and ErrForbidden when inviter is no longer an active member of the
	// tenant or no longer holds its role-management permission. An empty inviter
	// checks only that the role exists. Any other error is the judgement not
	// being available, which the caller treats as unavailable rather than as a
	// refusal.
	StillGrantable(ctx context.Context, tenant, role, inviter string) error
}

// The two refusals of a role judgement. They are declared beside the seam they
// belong to, as ErrInvalidAccountID sits beside AccountID. A caller tells them
// apart with errors.Is, and every wrap on the way out uses %w so the chain stays
// whole. The text names the class and never the role, the tenant or the account.
var (
	// ErrRoleUnknown reports a role that the tenant does not have. Outwardly an
	// invitation issue answers 400 invitation-role-unknown.
	ErrRoleUnknown = errors.New("authn: the tenant does not have this role")
	// ErrRoleProtected reports a role that may not be granted: the platform
	// administrator, one carrying a permission the granter does not hold, or one
	// the host's write guard protects. Outwardly an invitation issue answers 403
	// invitation-role-protected.
	ErrRoleProtected = errors.New("authn: the role may not be granted by this account")
)
