package invite

import (
	"context"
	"errors"
	"fmt"
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
// conditional write before giving up on contention. A failed claim is the only
// retried outcome: every write of the acceptance shares one transaction, so a
// write that fails rolls the claim back with it.
const maxClaimAttempts = 3

// The two control-flow outcomes of one redemption attempt. Neither reaches a
// caller: the claim losing its race makes Accept re-read and try again, and a
// join the store refuses is answered as the inactive membership it stands for
// after the transaction has rolled back.
var (
	errClaimLost   = errors.New("invite: the conditional claim matched no live row")
	errJoinRefused = errors.New("invite: the membership write refused the join")
)

// membershipWriterTx is the part of the account module's transaction-aware
// membership surface an acceptance joins: the read that holds the account's row
// and the conditional join. The account module delivers the whole
// provisioning.MembershipWriter, which this narrower interface is satisfied by;
// the acceptance takes only what it writes.
type membershipWriterTx interface {
	MembershipTx(ctx context.Context, tx *gorm.DB, tenant string, account authn.AccountID) (authn.Membership, bool, error)
	PutJoiningTx(ctx context.Context, tx *gorm.DB, m authn.Membership, wasActive bool) (bool, func(context.Context) error, error)
}

// Accept redeems an invitation with its token.
//
// The caller must be authenticated: the token grants a membership, and the
// requesting account is the one the membership is written for. The account must
// be a user account: a service account signs in with a service secret and has
// no acceptable membership, and a subject with no account row has no account to
// grant it to, so both are refused with ErrInvitationTargetInvalid before the
// token is read. The holder of the token alone is not enough — the inviter holds
// it too — so the caller must also hold the invited address as a proven email
// identifier. An account without that proof is refused with
// ErrInvitationEmailUnproved so the client can show the proof step; acceptance
// never creates an account and never makes the invited address a sign-in
// identifier.
//
// The acceptance is one transaction: the row is won with a conditional write
// that includes the presented digest, the membership is written through the
// account module's transaction-aware join, and the role the invitation carries
// is bound through the policy module's transaction-aware write. Any of the three
// failing rolls all three back, so the invitation stays pending and neither the
// membership nor the binding is written. The transaction is what makes the
// invitation's acceptance and the membership it grants one fact.
//
// Everything that judges rather than writes runs before the transaction opens.
// The role review is the reason: it reads the policy tables through the policy
// module, and a read that took a second connection could wait on the transaction
// forever where the database has one writer.
//
// A membership that already exists keeps its source and its SSO exemption: an
// acceptance adds a member, it does not rewrite how the member came to be one,
// and it still binds the invitation's role. A membership that is suspended is
// refused with ErrInvitationMembershipInactive and the invitation is revoked, so
// a suspended member cannot use a live token.
func (p *product) Accept(ctx context.Context, in AcceptInvite) (Accepted, error) {
	caller, ok := authn.FromContext(ctx)
	if !ok {
		return Accepted{}, fmt.Errorf("%w", ErrInvitationUnauthenticated)
	}
	if callerErr := p.requireUserCaller(ctx, caller); callerErr != nil {
		return Accepted{}, callerErr
	}
	if in.Token == "" {
		return Accepted{}, fmt.Errorf("%w", ErrInvitationNotFound)
	}
	digest := tokenDigest(in.Token)
	now := p.at()

	auditedID := ""
	actionID := ""
	succeeded := false
	// A mandatory row written for an attempt that then failed is closed with a
	// failed row sharing its action identifier. The row is written once, before
	// the first transaction; a retry does not write another.
	defer func() {
		if actionID != "" && !succeeded {
			p.acceptFailed(ctx, auditedID, actionID)
		}
	}()
	for attempt := 0; attempt < maxClaimAttempts; attempt++ {
		row, found, err := p.findByDigest(p.conn(ctx), digest)
		if err != nil {
			return Accepted{}, err
		}
		if !found {
			return Accepted{}, fmt.Errorf("%w", ErrInvitationNotFound)
		}
		if terr := terminalState(row, now); terr != nil {
			return Accepted{}, terr
		}
		if stateErr := p.refuseUnservedTenant(ctx, row); stateErr != nil {
			return Accepted{}, stateErr
		}
		if proofErr := p.requireProvenInvitedEmail(ctx, caller.Account(), row.Email); proofErr != nil {
			return Accepted{}, proofErr
		}
		if roleErr := p.requireRoleStillGrantable(ctx, row); roleErr != nil {
			return Accepted{}, roleErr
		}
		if actionID == "" {
			actionID, err = p.acceptAudit(ctx, row)
			if err != nil {
				return Accepted{}, err
			}
			auditedID = row.ID
		}

		accepted, after, redeemErr := p.redeem(ctx, caller, row, digest, now)
		switch {
		case errors.Is(redeemErr, errClaimLost):
			// Another acceptance or a resend changed the row between the read
			// and the claim. Re-read and judge it again.
			continue
		case errors.Is(redeemErr, errJoinRefused):
			p.revokePending(ctx, row, authn.ReasonInvitationMembershipInactive)
			return Accepted{}, fmt.Errorf("%w", ErrInvitationMembershipInactive)
		case redeemErr != nil:
			return Accepted{}, redeemErr
		}
		p.runAfter(ctx, after)
		succeeded = true
		return accepted, nil
	}
	return Accepted{}, failed("redeeming an invitation", authn.ErrUnavailable)
}

// redeem runs one acceptance's write transaction. The claim, the membership
// write and the role binding commit together or not at all; the returned
// closures are the work the two module writes owe after that commit.
func (p *product) redeem(ctx context.Context, caller authn.Identity, row invitationRow, digest string, now time.Time) (Accepted, []func(context.Context) error, error) {
	if p.membershipWriter == nil {
		return Accepted{}, nil, fmt.Errorf("%w: accepting an invitation needs the transaction-aware "+
			"membership writer", authn.ErrUnavailable)
	}
	var accepted Accepted
	var after []func(context.Context) error
	err := p.conn(ctx).Transaction(func(tx *gorm.DB) error {
		won, claimErr := p.claim(tx, row.ID, digest, now)
		if claimErr != nil {
			return claimErr
		}
		if !won {
			return errClaimLost
		}
		existing, exists, readErr := p.membershipWriter.MembershipTx(ctx, tx, row.TenantID, caller.Account())
		if readErr != nil {
			return failed("reading a membership", readErr)
		}
		if exists && !existing.Active {
			return errJoinRefused
		}
		mem := acceptedMembership(caller.Account(), row.TenantID, existing, exists)
		joined, membershipAfter, joinErr := p.membershipWriter.PutJoiningTx(ctx, tx, mem, exists)
		if joinErr != nil {
			return failed("writing a membership", joinErr)
		}
		if !joined {
			return errJoinRefused
		}
		if membershipAfter != nil {
			after = append(after, membershipAfter)
		}
		accepted = Accepted{Membership: mem}
		if row.Role == "" {
			return nil
		}
		if p.roleBinder == nil {
			return fmt.Errorf("%w: an invitation that grants a role needs the transaction-aware role "+
				"binder", authn.ErrUnavailable)
		}
		bindingAfter, bindErr := p.roleBinder.BindRoleTx(ctx, tx, row.TenantID, string(caller.Account()), row.Role)
		if bindErr != nil {
			return failed("writing the invitation's role", bindErr)
		}
		if bindingAfter != nil {
			after = append(after, bindingAfter)
		}
		accepted.Role = row.Role
		return nil
	})
	if err != nil {
		return Accepted{}, nil, err
	}
	return accepted, after, nil
}

// acceptAudit writes the acceptance's mandatory audit row and answers the action
// identifier its failed row would share. It runs after every refusal that is
// about the invitation or the caller and before the transaction, so a refused
// acceptance leaves no success row, and a row that cannot land means the
// acceptance does not happen. The detail names the tenant, the role when the
// invitation carries one, and the inviter; the object is the invitation
// identifier.
func (p *product) acceptAudit(ctx context.Context, row invitationRow) (string, error) {
	tenantName, err := p.nameOf(ctx, row.TenantID)
	if err != nil {
		return "", p.cause("reading the tenant's name", err)
	}
	actionID := newActionID()
	detail := map[string]string{
		detailActionID:  actionID,
		detailTenant:    tenantName,
		detailInvitedBy: string(row.InvitedBy),
	}
	if row.Role != "" {
		detail[detailRole] = row.Role
	}
	if err := p.auditRequired(ctx, mandatory(actionAccept, row.ID, detail)); err != nil {
		return "", err
	}
	return actionID, nil
}

// acceptFailed closes a success audit row an attempt did not live up to: the
// attempt wrote its mandatory row and then lost the race or failed its write.
// With no action identifier there is no row to close, and nothing is written.
func (p *product) acceptFailed(ctx context.Context, invitationID, actionID string) {
	if actionID == "" {
		return
	}
	p.auditBestEffort(ctx, failedAudit(actionAccept, invitationID, actionID))
}

// runAfter runs the closures the transaction-aware writes returned, once the
// acceptance's transaction has committed.
func (p *product) runAfter(ctx context.Context, after []func(context.Context) error) {
	for _, fn := range after {
		if fn == nil {
			continue
		}
		if err := fn(ctx); err != nil {
			p.logger.Error(ModuleName+": the work an acceptance owed after its commit failed",
				"cause", err.Error())
		}
	}
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
		p.revokePending(ctx, row, authn.ReasonInvitationTenantDeleted)
		return fmt.Errorf("%w", ErrInvitationNotFound)
	}
}

// requireUserCaller refuses an acceptance attempted by anything other than a
// user account. An invitation grants a membership to a person; a service
// account signs in with a service secret and has no acceptable membership, and
// a subject with no account row — a static password-table subject — has no
// account to grant it to. Both are refused as an invalid acceptance target (403
// invitation-target-invalid), not as an unauthenticated request: the caller is
// authenticated, and a 401 would make a client refresh and retry a permanent
// refusal. A read that fails is the judgement being unavailable, never a pass.
func (p *product) requireUserCaller(ctx context.Context, caller authn.Identity) error {
	account, err := p.accounts.Get(ctx, caller.Account())
	switch {
	case errors.Is(err, authn.ErrAccountNotFound):
		return fmt.Errorf("%w: the caller's account does not exist", ErrInvitationTargetInvalid)
	case err != nil:
		return failed("reading the accepting account", err)
	}
	if account.Kind != authn.KindUser {
		return fmt.Errorf("%w: the caller's account is not a user account", ErrInvitationTargetInvalid)
	}
	return nil
}

// requireProvenInvitedEmail refuses a caller who does not hold the invited
// address as a proven email identifier. It is the acceptance's use of the same
// question the eligibility read answers: the invitation stays pending, and the
// client shows the proof step.
func (p *product) requireProvenInvitedEmail(ctx context.Context, id authn.AccountID, email string) error {
	proved, err := p.provesInvitedEmail(ctx, id, email)
	if err != nil {
		return failed("reading the account's identifiers", err)
	}
	if !proved {
		return fmt.Errorf("%w", ErrInvitationEmailUnproved)
	}
	return nil
}

// provesInvitedEmail reports whether the account holds the invited address as a
// proven email identifier. Both values are normalized, so equality is the
// comparison. An account that does not exist, or that holds the address only
// through an unproved channel or through an invitation, is not a holder: the
// caller must prove the address first. A read failure is not a refusal and is
// left to the caller as unavailable.
func (p *product) provesInvitedEmail(ctx context.Context, id authn.AccountID, email string) (bool, error) {
	identifiers, err := p.accounts.Identifiers(ctx, id)
	if err != nil {
		if errors.Is(err, authn.ErrAccountNotFound) {
			return false, nil
		}
		return false, err
	}
	for _, identifier := range identifiers {
		if identifier.Kind != authn.IdentifierEmail || identifier.Value != email {
			continue
		}
		if (authn.Verification{Via: identifier.VerifiedVia}).Proves() {
			return true, nil
		}
	}
	return false, nil
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
// that is neither expired nor superseded. The order follows the design: a
// terminal state is told first, and a superseded row and expiry are judged last,
// from the stored record and the clock. A superseded row reads expired even
// while its own deadline is ahead, because a later invitation took the live
// slot; the writes that share this judgement answer it the same way.
func terminalState(row invitationRow, now time.Time) error {
	switch row.Status {
	case statusAccepted:
		return fmt.Errorf("%w", ErrInvitationAlreadyAccepted)
	case statusRevoked:
		return fmt.Errorf("%w", ErrInvitationRevoked)
	}
	if row.SupersededAt != nil || !utc(row.ExpiresAt).After(now) {
		return fmt.Errorf("%w", ErrInvitationExpired)
	}
	return nil
}

// claim is the conditional write that wins a pending invitation: the row is
// flipped to accepted only while it is still pending, not expired, not
// superseded by a later invitation and still carries the digest of the token
// presented. The digest is part of the condition because a resend issued while
// the acceptance was in flight replaces the token: the old token no longer
// resolves, and the stale acceptance must not redeem the row the resend just
// refreshed. A superseded row is never a live slot, so the condition excludes it
// even when its deadline is still ahead. RowsAffected of one is the win.
func (p *product) claim(tx *gorm.DB, id, digest string, now time.Time) (bool, error) {
	res := tx.Model(&invitationRow{}).
		Where("id = ? AND token_digest = ? AND status = ? AND expires_at > ? AND superseded_at IS NULL",
			id, digest, statusPending, now).
		Updates(map[string]any{"status": statusAccepted, "accepted_at": now, "updated_at": now})
	if res.Error != nil {
		return false, failed("redeeming an invitation", res.Error)
	}
	return res.RowsAffected == 1, nil
}

// revokePending revokes one pending invitation that has not expired. It is the
// settlement of an invitation a suspended member can no longer redeem, of one
// whose tenant is gone and of one whose role can no longer be granted: the
// token is void from here on and a retry answers the same terminal state. The
// write is conditional on the row still being pending and live, so a row that
// expired between the terminal check and the settlement keeps reading expired
// rather than becoming revoked. Its own failure is an Error line that changes
// nothing about the answer handed back.
//
// A revocation that actually changed the row leaves one best-effort revoke
// audit row, so a token that was withdrawn without an administrator asking for
// it is still traceable. The row carries the reason class that made the
// acceptance settle it, drawn from the closed set; its actor is the account
// attempting the acceptance, the request the revocation happened in, not the
// invitation's issuer. The row is best effort because the refusal it settles is
// the answer the caller needs; an audit store that is down must not turn it
// into a different failure. A tenant name that cannot be read does not suppress
// the row: the tenant identifier stands in for the name.
func (p *product) revokePending(ctx context.Context, row invitationRow, reason string) {
	now := p.at()
	res := p.conn(ctx).Model(&invitationRow{}).
		Where("id = ? AND status = ? AND expires_at > ?", row.ID, statusPending, now).
		Updates(map[string]any{"status": statusRevoked, "revoked_at": now, "updated_at": now})
	if res.Error != nil {
		p.logger.Error(ModuleName+": revoking a pending invitation failed",
			"cause", res.Error.Error())
		return
	}
	if res.RowsAffected == 0 {
		return
	}
	tenantName, err := p.nameOf(ctx, row.TenantID)
	if err != nil {
		// The settlement's audit row does not depend on the name read: a
		// revocation the acceptance performed is traceable even when the
		// tenant cannot be named, so the identifier stands in for the name.
		tenantName = row.TenantID
	}
	p.auditBestEffort(ctx, mandatory(actionRevoke, row.ID, map[string]string{
		detailActionID: newActionID(),
		detailTenant:   tenantName,
		detailReason:   reason,
	}))
}
