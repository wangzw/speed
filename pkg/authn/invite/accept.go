package invite

import (
	"context"
	"errors"
	"fmt"
	"time"

	"gorm.io/gorm"

	"github.com/vislake/speed/pkg/authn"
)

// VerifiedViaInvite is the verification channel an invitation leaves on a row.
// It is a member of the account capability's closed set but does not prove the
// invited address; acceptance requires an ordinary proving channel.
const VerifiedViaInvite = "invite"

const maxClaimAttempts = 3

var (
	errClaimLost   = errors.New("invite: the conditional claim matched no live row")
	errJoinRefused = errors.New("invite: the membership write refused the join")
)

type membershipWriterTx interface {
	MembershipTx(ctx context.Context, tx *gorm.DB, tenant string, account authn.AccountID) (authn.Membership, bool, error)
	PutJoiningTx(ctx context.Context, tx *gorm.DB, m authn.Membership, wasActive bool) (bool, func(context.Context) error, error)
}

// Accept redeems an invitation with its token. The caller must be
// authenticated; the membership is written for the caller's account.
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

func (p *product) acceptFailed(ctx context.Context, invitationID, actionID string) {
	if actionID == "" {
		return
	}
	p.auditBestEffort(ctx, failedAudit(actionAccept, invitationID, actionID))
}

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

func acceptedMembership(id authn.AccountID, tenant string, existing authn.Membership, exists bool) authn.Membership {
	mem := authn.Membership{Account: id, Tenant: tenant, Active: true, Source: SourceInvite}
	if exists {
		mem.Source = existing.Source
		mem.SSOExempt = existing.SSOExempt
	}
	return mem
}

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
		tenantName = row.TenantID
	}
	p.auditBestEffort(ctx, mandatory(actionRevoke, row.ID, map[string]string{
		detailActionID: newActionID(),
		detailTenant:   tenantName,
		detailReason:   reason,
	}))
}
