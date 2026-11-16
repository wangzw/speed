package invite

import (
	"context"
	"errors"
	"fmt"

	"github.com/vislake/speed/pkg/authn"
)

// requireGrantableRole judges a role before an invitation that carries it is
// written. The judgement is the policy seam's: the role exists in the tenant,
// is not the platform administrator, the acting account may grant it, and the
// host's write guard admits the binding. An assembly without the seam refuses
// an invitation with a role rather than writing one nobody checked.
//
// A host command issuing an invitation has no acting account. It may bind roles
// directly, so the same call is made with an empty account and the seam judges
// the role's own rules and the write guard without a held set to compare it to.
func (p *product) requireGrantableRole(ctx context.Context, tenant, role string) error {
	if role == "" {
		return nil
	}
	if p.rolePolicy == nil {
		return fmt.Errorf("%w: an invitation that grants a role needs the invitation role policy", authn.ErrUnavailable)
	}
	account, _, err := issuerOf(authn.FromContext(ctx))
	if err != nil {
		return err
	}
	return p.rolePolicy.Grantable(ctx, tenant, account, role)
}

// issuerOf reads the account an invitation names as its issuer. A command that
// runs without a request identity is a host: its invitations record no issuer
// and skip the checks that need one. An identity without an account is neither
// a host nor an account -- the gateway stamps an account on every identity it
// creates -- so it is refused rather than read as a command, which would let it
// skip the inviter checks entirely.
func issuerOf(identity authn.Identity, hasIdentity bool) (account string, host bool, err error) {
	if !hasIdentity {
		return "", true, nil
	}
	account = string(identity.Account())
	if account == "" {
		return "", false, fmt.Errorf("%w: the request carries an identity with no account", authn.ErrUnavailable)
	}
	return account, false, nil
}

// requireRoleStillGrantable re-judges an invitation's role before acceptance.
// The review is the issue-time judgement replayed against the inviter: the role
// has to still exist, and, when an account issued the invitation, that account
// has to still be enabled, still be an active member of the tenant and still
// hold the tenant's role-management permission and every permission the role
// carries.
//
// The account's enabled state is read here rather than in the policy seam
// because an account-level disable leaves the account's memberships and its
// policy bindings in place, so neither the active-member check nor the held set
// can see it. The gateway refuses a disabled account the request that issues an
// invitation, but the invitation outlives that moment: the same account may be
// disabled afterwards while the token is still pending. A membership that is
// suspended keeps its bindings, so the held set alone would not notice that
// either.
//
// Any refusal by the seam or by the account read revokes the invitation, so the
// token stops granting anything, and is answered as the terminal refusal; the
// membership and the binding are not written. The revocation writes its own
// audit row.
//
// The judgement runs before the acceptance's transaction opens. The policy read
// takes a connection of its own, and the database's single writer would wait on
// the transaction it is not part of. A seam that cannot answer, or an account
// read that fails, leaves the invitation pending: a dependency being unavailable
// is not a decision about the role.
func (p *product) requireRoleStillGrantable(ctx context.Context, row invitationRow) error {
	if row.Role == "" {
		return nil
	}
	if p.rolePolicy == nil {
		return fmt.Errorf("%w: an invitation that grants a role needs the invitation role policy", authn.ErrUnavailable)
	}
	err := p.rolePolicy.StillGrantable(ctx, row.TenantID, row.Role, string(row.InvitedBy))
	if err == nil {
		err = p.requireInviterActive(ctx, row)
	}
	switch {
	case err == nil:
		return nil
	case errors.Is(err, authn.ErrRoleUnknown), errors.Is(err, authn.ErrRoleProtected),
		errors.Is(err, authn.ErrForbidden):
		p.revokePending(ctx, row, authn.ReasonInvitationRoleWithdrawn)
		return fmt.Errorf("%w", ErrInvitationRoleWithdrawn)
	default:
		return failed("judging the invitation's role", err)
	}
}

// requireInviterActive refuses an invitation whose inviter account is no longer
// enabled. An account that no longer exists cannot have its standing replayed
// and is refused the same way. An empty inviter is a host command, which issued
// the invitation without an account whose state could be read, and is skipped.
//
// A static subject is skipped too: its permissions come from the deployment's
// credential table rather than an account row, so removing it means changing the
// configuration and restarting, and judging it by the account store would revoke
// every invitation it issued. The tradeoff is that a static subject removed from
// the configuration can still have its pending invitations grant their role
// until they expire or an administrator revokes them.
//
// A read that fails is the judgement being unavailable, never a refusal: the
// caller keeps the invitation pending rather than revoking it on a dependency
// that is down.
func (p *product) requireInviterActive(ctx context.Context, row invitationRow) error {
	if row.InvitedBy == "" {
		return nil
	}
	inviter := authn.AccountID(row.InvitedBy)
	if inviter.IsStatic() {
		// A static subject has no account row, so this account-state read is
		// skipped. The role review before it still ran: the subject's permissions
		// come from the deployment configuration rather than from a membership and
		// its bindings.
		return nil
	}
	account, err := p.accounts.Get(ctx, inviter)
	if err != nil {
		if errors.Is(err, authn.ErrAccountNotFound) {
			return fmt.Errorf("%w: the inviter account no longer exists", authn.ErrForbidden)
		}
		return fmt.Errorf("invite: reading the inviter account %q: %w", inviter, err)
	}
	if account.Status != authn.StatusActive {
		return fmt.Errorf("%w: the inviter's account is not enabled", authn.ErrForbidden)
	}
	return nil
}
