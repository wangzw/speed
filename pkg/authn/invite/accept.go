package invite

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"gorm.io/gorm"

	"github.com/vislake/speed/pkg/authn"
)

// VerifiedViaInvite is the way an address comes to be verified through an
// invitation: the link was delivered to the address and the holder presented
// the token. It is a proof of ownership, of the same class as an SMS code, and
// the account capability's Verification set admits it as one.
const VerifiedViaInvite = "invite"

// maxClaimAttempts bounds how many times Accept re-reads and re-attempts the
// conditional write before giving up on contention. A winner that settles a
// lost race back to pending is what makes more than one attempt possible.
const maxClaimAttempts = 3

// Accept redeems an invitation with its token.
//
// It reads the row by the token's digest, reports a terminal state, and then
// wins the row with a conditional write; two concurrent acceptances of one
// token leave at most one winner, and a loser writes nothing. The winner looks
// the address up: an address that is already an account gets the membership
// only, and one that is not gets an account created first. Both writes are the
// account capability's, in its own transaction, so a failure after the row was
// won is settled by a second conditional write: a transient failure returns the
// row to pending, and a permanent one revokes it.
func (p *product) Accept(ctx context.Context, in AcceptInvite) (authn.Membership, error) {
	digest := tokenDigest(in.Token)
	if in.Token == "" {
		return authn.Membership{}, fmt.Errorf("%w", ErrInvitationNotFound)
	}
	now := p.at()

	var held invitationRow
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

	account, err := p.redeem(ctx, held, in)
	if err != nil {
		p.settle(ctx, held.ID, err)
		return authn.Membership{}, err
	}
	return authn.Membership{Account: account, Tenant: held.TenantID, Active: true, Source: SourceInvite}, nil
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

// redeem gives the won invitation its account and membership. The address is
// looked up first: one that is already an account gets the membership only, and
// one that is not gets an enabled user account first, with the address as its
// identifier and the invitation as the way it was verified.
func (p *product) redeem(ctx context.Context, held invitationRow, in AcceptInvite) (authn.AccountID, error) {
	id, err := p.accounts.Lookup(ctx, authn.IdentifierEmail, held.Email)
	switch {
	case err == nil:
		// The token was delivered to the address and presented by its holder,
		// so it proves the address. An account that holds the address as an
		// unproved identifier is brought up to proved; one that already proves
		// it keeps the proof it has.
		identifiers, readErr := p.accounts.Identifiers(ctx, id)
		if readErr != nil {
			return "", failed("reading the account's identifiers", readErr)
		}
		for _, identifier := range identifiers {
			if identifier.Kind != authn.IdentifierEmail || identifier.Value != held.Email {
				continue
			}
			if (authn.Verification{Via: identifier.VerifiedVia}).Proves() {
				break
			}
			if addErr := p.accountAdm.AddIdentifier(ctx, id, authn.IdentifierEmail, held.Email,
				authn.Verification{Via: VerifiedViaInvite}); addErr != nil {
				return "", failed("proving the invitation's address", addErr)
			}
			break
		}
		return p.putMembership(ctx, id, held.TenantID)
	case errors.Is(err, authn.ErrAccountNotFound):
		// No account holds the address as an identifier. It may still be the
		// pending contact of one that self-registered: the token was delivered
		// to the address and presented by its holder, so the address is proved,
		// and the acceptance writes it through invite and hangs the membership
		// on that account instead of opening a second one. A read failure is
		// neither a hit nor a miss: it fails the redemption, which the caller
		// settles back to pending, and no account is created.
		owner, ok, perr := p.verification.PendingAccountByContact(ctx, string(authn.IdentifierEmail), held.Email)
		if perr != nil {
			return "", failed("finding a pending account", perr)
		}
		if ok {
			return p.verifyPending(ctx, authn.AccountID(owner), held)
		}
		created, cerr := p.createAccount(ctx, held.Email, in)
		if cerr != nil {
			return "", failed("creating an account", cerr)
		}
		return p.putMembership(ctx, created, held.TenantID)
	default:
		return "", failed("looking up an account", err)
	}
}

// verifyPending writes the invitation's address onto the pending account that
// already lists it, as an identifier proved through invite. The account module
// moves the account's signup state to verified in the same transaction, and the
// membership hangs on that account.
func (p *product) verifyPending(ctx context.Context, id authn.AccountID, held invitationRow) (authn.AccountID, error) {
	if err := p.accountAdm.AddIdentifier(ctx, id, authn.IdentifierEmail, held.Email,
		authn.Verification{Via: VerifiedViaInvite}); err != nil {
		return "", failed("writing an identifier", err)
	}
	return p.putMembership(ctx, id, held.TenantID)
}

// putMembership writes the membership an accepted invitation stands for, its
// source always invite.
func (p *product) putMembership(ctx context.Context, id authn.AccountID, tenant string) (authn.AccountID, error) {
	mem := authn.Membership{Account: id, Tenant: tenant, Active: true, Source: SourceInvite}
	if err := p.memberships.Put(ctx, mem); err != nil {
		return "", failed("writing a membership", err)
	}
	return id, nil
}

// createAccount creates the account an invitation for an address with none
// stands for. The username is required: an account is opened with a login name,
// and a request that names none creates nothing. The username is written as the
// holder's own, through self.
//
// The email's verification channel is the invitation, which the account
// capability's set admits as a proof of ownership. Via is ReasonAdmin: the
// account capability's closed set of account origins names no invitation value,
// and an invitation is an administrator-initiated grant, so the way the account
// came about is recorded as an administrative one.
func (p *product) createAccount(ctx context.Context, email string, in AcceptInvite) (authn.AccountID, error) {
	if strings.TrimSpace(in.Username) == "" {
		return "", fmt.Errorf("%w: accepting an invitation for a new account needs a username",
			authn.ErrInvalidArgument)
	}
	account, err := p.accountAdm.Create(ctx, authn.CreateAccount{
		Kind:                authn.KindUser,
		DisplayName:         in.DisplayName,
		Username:            in.Username,
		UsernameVerifiedVia: authn.Verification{Via: authn.VerifiedViaSelf},
		Email:               email,
		EmailVerifiedVia:    authn.Verification{Via: VerifiedViaInvite},
		Password:            in.Password,
		Via:                 authn.ReasonAdmin,
	})
	if err != nil {
		return "", err
	}
	return account.ID, nil
}

// settle closes a won invitation whose redemption failed. A transient failure
// returns the row to pending, so a retry is not locked out; a permanent one
// revokes it, so a token that can never be redeemed does not stay live until
// its deadline. The write is conditional on the row still being accepted and
// never overwrites it unconditionally; its own failure is an Error line and
// changes nothing about the error handed back.
func (p *product) settle(ctx context.Context, id string, cause error) {
	now := p.at()
	target := map[string]any{"status": statusRevoked, "updated_at": now}
	if !permanent(cause) {
		target = map[string]any{"status": statusPending, "accepted_at": nil, "updated_at": now}
	}
	res := p.conn(ctx).Model(&invitationRow{}).
		Where("id = ? AND status = ?", id, statusAccepted).
		Updates(target)
	if res.Error != nil {
		p.logger.Error(ModuleName+": the settlement of an invitation that could not be redeemed failed",
			slog.String("cause", res.Error.Error()))
	}
}

// permanent reports whether a redemption failure cannot be fixed by retrying:
// an address that conflicts with an existing account or a reservation, or a
// binding another account holds.
func permanent(cause error) bool {
	return errors.Is(cause, authn.ErrAccountExists) || errors.Is(cause, authn.ErrContactTaken) ||
		errors.Is(cause, authn.ErrBindingConflict)
}
