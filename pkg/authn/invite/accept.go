package invite

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"gorm.io/gorm"

	"github.com/vislake/speed/pkg/authn"
)

// VerifiedViaInvite is the channel an invitation leaves on a row. It is a known
// value of the account capability's closed set, but it is not a proof of the
// invited address: the token was delivered to the address and presented by its
// holder, which grants the membership the invitation names and says nothing about
// who owns the address. An account that accepts must prove the invited address
// through an ordinary proving channel first.
const VerifiedViaInvite = "invite"

// maxClaimAttempts bounds how many times Accept re-reads and re-attempts the
// conditional write before giving up on contention. A winner that settles a
// lost race back to pending is what makes more than one attempt possible.
const maxClaimAttempts = 3

// Accept redeems an invitation with its token.
//
// The caller must be authenticated: the token grants a membership, and the
// requesting account is the one the membership is written for. The holder of the
// token alone is not enough — the inviter holds it too — so the caller must also
// hold the invited address as a proven email identifier. An account without that
// proof is refused with ErrInvitationEmailUnproved so the client can show the
// proof step; acceptance never creates an account and never makes the invited
// address a sign-in identifier.
//
// It reads the row by the token's digest, reports a terminal state, checks the
// caller and the existing membership, and then wins the row with a conditional
// write; two concurrent acceptances of one token leave at most one winner, and a
// loser writes nothing. A membership that already exists but is suspended is
// refused with ErrInvitationMembershipInactive and the pending invitation is
// revoked, so a suspended member cannot use a live token. An existing active
// membership keeps its source and its SSO exemption. A write failure after the
// row was won is settled by a second conditional write: a transient failure
// returns the row to pending, and only a failure no retry can complete revokes it.
func (p *product) Accept(ctx context.Context, in AcceptInvite) (authn.Membership, error) {
	caller, ok := authn.FromContext(ctx)
	if !ok {
		return authn.Membership{}, fmt.Errorf("%w", ErrInvitationUnauthenticated)
	}
	if in.Token == "" {
		return authn.Membership{}, fmt.Errorf("%w", ErrInvitationNotFound)
	}
	digest := tokenDigest(in.Token)
	now := p.at()

	var held invitationRow
	var existing authn.Membership
	var exists bool
	claimed := false
	for attempt := 0; attempt < maxClaimAttempts && !claimed; attempt++ {
		row, found, err := p.findByDigest(p.conn(ctx), digest)
		if err != nil {
			return authn.Membership{}, err
		}
		if !found {
			return authn.Membership{}, fmt.Errorf("%w", ErrInvitationNotFound)
		}
		if terr := terminalState(row, now); terr != nil {
			return authn.Membership{}, terr
		}
		if stateErr := p.refuseUnservedTenant(ctx, row); stateErr != nil {
			return authn.Membership{}, stateErr
		}
		if proofErr := p.requireProvenInvitedEmail(ctx, caller.Account(), row.Email); proofErr != nil {
			return authn.Membership{}, proofErr
		}
		existing, exists, err = p.memberships.Membership(ctx, row.TenantID, caller.Account())
		if err != nil {
			return authn.Membership{}, failed("reading a membership", err)
		}
		if exists && !existing.Active {
			p.revokePending(ctx, row.ID)
			return authn.Membership{}, fmt.Errorf("%w", ErrInvitationMembershipInactive)
		}
		won, err := p.claim(p.conn(ctx), row.ID, now)
		if err != nil {
			return authn.Membership{}, err
		}
		if won {
			held = row
			claimed = true
		}
	}
	if !claimed {
		return authn.Membership{}, failed("redeeming an invitation", authn.ErrUnavailable)
	}

	mem := acceptedMembership(caller.Account(), held.TenantID, existing, exists)
	joined, err := p.memberships.PutJoining(ctx, mem, exists)
	if err != nil {
		p.settle(ctx, held, err)
		return authn.Membership{}, failed("writing a membership", err)
	}
	if !joined {
		// The membership was suspended or removed between the read and the
		// conditional write. The invitation is settled as revoked, so the token
		// cannot be tried again against a membership that is no longer active.
		p.withdraw(ctx, held.ID)
		return authn.Membership{}, fmt.Errorf("%w", ErrInvitationMembershipInactive)
	}
	return mem, nil
}

// refuseUnservedTenant refuses an acceptance into a tenant that is deleted or
// disabled. A deleted tenant's row is revoked here in case the deletion path's
// cleanup did not reach it; a disabled tenant's invitation is left pending, so
// enabling the tenant makes the same token redeemable again. An assembly with
// no tenant state resolver has no such state to judge and accepts.
func (p *product) refuseUnservedTenant(ctx context.Context, row invitationRow) error {
	if p.tenantState == nil {
		return nil
	}
	state, err := p.tenantState.TenantStateOf(ctx, row.TenantID)
	if err != nil {
		return failed("reading the tenant's state", err)
	}
	switch state {
	case authn.TenantServing:
		return nil
	case authn.TenantDisabled:
		return fmt.Errorf("%w", ErrInvitationTenantInactive)
	default:
		p.revokePending(ctx, row.ID)
		return fmt.Errorf("%w", ErrInvitationNotFound)
	}
}

// requireProvenInvitedEmail reports whether the account holds the invited
// address as a proven email identifier. Both values are normalized, so equality
// is the comparison. An account that does not exist, or that holds the address
// only through an unproved channel or through an invitation, is not a holder:
// the caller must prove the address first. A read failure is not a refusal and
// is left unavailable.
func (p *product) requireProvenInvitedEmail(ctx context.Context, id authn.AccountID, email string) error {
	identifiers, err := p.accounts.Identifiers(ctx, id)
	if err != nil {
		if errors.Is(err, authn.ErrAccountNotFound) {
			return fmt.Errorf("%w", ErrInvitationEmailUnproved)
		}
		return failed("reading the account's identifiers", err)
	}
	for _, identifier := range identifiers {
		if identifier.Kind != authn.IdentifierEmail || identifier.Value != email {
			continue
		}
		if (authn.Verification{Via: identifier.VerifiedVia}).Proves() {
			return nil
		}
	}
	return fmt.Errorf("%w", ErrInvitationEmailUnproved)
}

// acceptedMembership is the membership an acceptance stands for. A membership
// that already exists keeps its source and its SSO exemption: an acceptance adds
// a member, it does not rewrite how the member came to be one.
func acceptedMembership(id authn.AccountID, tenant string, existing authn.Membership, exists bool) authn.Membership {
	mem := authn.Membership{Account: id, Tenant: tenant, Active: true, Source: SourceInvite}
	if exists {
		mem.Source = existing.Source
		mem.SSOExempt = existing.SSOExempt
	}
	return mem
}

// terminalState reports a row that cannot be redeemed, or nil for a pending one
// that is not yet expired. The order follows the design: a terminal state is
// told first, and expiry is judged last, from the clock.
func terminalState(row invitationRow, now time.Time) error {
	switch row.Status {
	case statusAccepted:
		return fmt.Errorf("%w", ErrInvitationAlreadyAccepted)
	case statusRevoked:
		return fmt.Errorf("%w", ErrInvitationRevoked)
	}
	if !utc(row.ExpiresAt).After(now) {
		return fmt.Errorf("%w", ErrInvitationExpired)
	}
	return nil
}

// claim is the conditional write that wins a pending invitation: the row is
// flipped to accepted only while it is still pending and not expired.
// RowsAffected of one is the win.
func (p *product) claim(tx *gorm.DB, id string, now time.Time) (bool, error) {
	res := tx.Model(&invitationRow{}).
		Where("id = ? AND status = ? AND expires_at > ?", id, statusPending, now).
		Updates(map[string]any{"status": statusAccepted, "accepted_at": now, "updated_at": now})
	if res.Error != nil {
		return false, failed("redeeming an invitation", res.Error)
	}
	return res.RowsAffected == 1, nil
}

// revokePending revokes one pending invitation. It is the settlement of an
// invitation a suspended member can no longer redeem: the token is void from
// here on and a retry answers the same terminal state. The write is conditional
// on the row still being pending, and its own failure is an Error line that
// changes nothing about the answer handed back.
func (p *product) revokePending(ctx context.Context, id string) {
	now := p.at()
	res := p.conn(ctx).Model(&invitationRow{}).
		Where("id = ? AND status = ?", id, statusPending).
		Updates(map[string]any{"status": statusRevoked, "updated_at": now})
	if res.Error != nil {
		p.logger.Error(ModuleName+": revoking the invitation of a suspended member failed",
			slog.String("cause", res.Error.Error()))
	}
}

// settle closes a won invitation whose redemption failed. A transient failure
// returns the row to pending, so a retry is not locked out; only a failure that
// no retry can complete revokes it, so a token that can never be redeemed does
// not stay live until its deadline. Returning a row to pending must not collide
// with a newer pending invitation for the same address: a resend issued while
// the acceptance held the old row has already written one, so the old row is
// revoked instead, and a collision that still happens is settled the same way.
// Every write is conditional on the row still being accepted and never
// overwrites it unconditionally; its own failure is an Error line and changes
// nothing about the error handed back.
func (p *product) settle(ctx context.Context, row invitationRow, cause error) {
	if !permanent(cause) {
		fresh, err := p.pendingExists(ctx, row.TenantID, row.Email)
		if err != nil {
			p.logger.Error(ModuleName+": the settlement of an invitation that could not be redeemed failed",
				slog.String("cause", err.Error()))
			return
		}
		if !fresh {
			now := p.at()
			res := p.conn(ctx).Model(&invitationRow{}).
				Where("id = ? AND status = ?", row.ID, statusAccepted).
				Updates(map[string]any{"status": statusPending, "accepted_at": nil, "updated_at": now})
			if res.Error == nil {
				return
			}
			if !duplicate(res.Error) {
				p.logger.Error(ModuleName+": the settlement of an invitation that could not be redeemed failed",
					slog.String("cause", res.Error.Error()))
				return
			}
		}
	}
	p.withdraw(ctx, row.ID)
}

// pendingExists reports whether a pending invitation for the tenant and address
// exists. It is the newer row a resend writes while an acceptance holds the old
// one.
func (p *product) pendingExists(ctx context.Context, tenant, email string) (bool, error) {
	var n int64
	err := p.conn(ctx).Model(&invitationRow{}).
		Where("tenant_id = ? AND email = ? AND status = ?", tenant, email, statusPending).
		Count(&n).Error
	if err != nil {
		return false, err
	}
	return n > 0, nil
}

// withdraw revokes a won invitation whose redemption failed. The write is
// conditional on the row still being accepted; its own failure is an Error line
// and changes nothing about the error handed back.
func (p *product) withdraw(ctx context.Context, id string) {
	res := p.conn(ctx).Model(&invitationRow{}).
		Where("id = ? AND status = ?", id, statusAccepted).
		Updates(map[string]any{"status": statusRevoked, "updated_at": p.at()})
	if res.Error != nil {
		p.logger.Error(ModuleName+": the withdrawal of an invitation that could not be redeemed failed",
			slog.String("cause", res.Error.Error()))
	}
}

// permanent reports whether a redemption failure is one no retry can complete. A
// membership store that reports a contact or binding another account holds, or
// an account that no longer exists, are such failures. A membership store that
// is merely unavailable is not: the accepting party still holds the token and
// can retry.
func permanent(cause error) bool {
	return errors.Is(cause, authn.ErrContactTaken) || errors.Is(cause, authn.ErrBindingConflict) ||
		errors.Is(cause, authn.ErrAccountNotFound)
}
