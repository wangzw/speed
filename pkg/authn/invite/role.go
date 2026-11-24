package invite

import (
	"context"
	"errors"
	"fmt"

	"github.com/vislake/speed/pkg/authn"
)

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

// issuerOf reads the issuer. No identity is a host command, with no issuer. An
// identity without an account is refused: reading it as a command would skip the
// inviter checks.
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

func (p *product) requireInviterActive(ctx context.Context, row invitationRow) error {
	if row.InvitedBy == "" {
		return nil
	}
	inviter := authn.AccountID(row.InvitedBy)
	if inviter.IsStatic() {
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
