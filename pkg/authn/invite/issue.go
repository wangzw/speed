package invite

import (
	"context"
	"fmt"
	"strings"
	"time"
	"unicode"

	"gorm.io/gorm"

	"github.com/vislake/speed/pkg/authn"
)

// tenantMax is the longest a tenant identifier may be.
const tenantMax = 255

// maxExplicitLifetime is the longest an explicitly stated deadline may lie
// beyond the moment of issuance. It is the widest lifetime the ttl setting
// accepts, so no explicit deadline can outlive what the setting could name.
const maxExplicitLifetime = 90 * 24 * time.Hour

// checkTenant refuses a tenant that cannot name one: empty, too long, or
// holding a blank or a control character. The refusal is ErrInvalidArgument and
// names no value.
func checkTenant(tenant string) error {
	if tenant == "" || len(tenant) > tenantMax || strings.IndexFunc(tenant, func(r rune) bool {
		return unicode.IsSpace(r) || unicode.IsControl(r)
	}) >= 0 {
		return fmt.Errorf("%w: the tenant is not an identifier of at most 255 bytes without blanks "+
			"or control characters", authn.ErrInvalidArgument)
	}
	return nil
}

// Issue issues one invitation. The address is normalized first, and an address
// that is not well formed is refused before any row is written. The issuing
// account's send budget is charged next, before the target checks, so an attempt
// the checks refuse still costs the account that made it. Only after every check
// has passed are the invited address's and the deployment's budgets charged, and
// a charge whose row is not written is given back; a refused attempt therefore
// never spends an address's allowance. An issuance past any budget is throttled
// before a row is written. A resend spends the same send budgets but is not a new
// issuance: it re-judges nothing about the target.
//
// Before the row is written the target is judged: the system tenant and a
// service account's address cannot be invited (ErrInvitationTargetInvalid), an
// address that already holds a membership in the tenant — active or suspended —
// is refused (ErrInvitationMember), an address with an unexpired pending
// invitation is refused (ErrInvitationPending), and an explicit deadline beyond
// maxExplicitLifetime is refused (ErrInvitationExpiryTooLong). A non-empty role
// is judged by the invitation role policy: the role has to exist in the tenant
// (authn.ErrRoleUnknown), the caller has to be allowed to grant it
// (authn.ErrForbidden, authn.ErrRoleProtected), and an assembly without the
// policy fails the issuance rather than writing an unchecked role. A host
// command issuing without an acting account may name a role too: the judgement
// is then the role's existence, the platform-administrator refusal and the
// host's write guard, because the host may bind roles directly.
//
// One transaction then supersedes an expired pending invitation for the same
// tenant and address, recording the replacement in superseded_at while leaving
// the old row's stored state pending so it still reads expired, and inserts the
// new row. The partial unique index arbitrates a concurrent issuance of the same
// pair, which is ErrInvitationPending. The first issuance counts as one send and
// records it in last_sent_at and send_count.
//
// The issuer budget is keyed by the request's acting account, which during an
// impersonation is the operator, so an impersonation spends the operator's
// allowance rather than the impersonated account's. The invitation's issuer is
// the effective account instead: a host's own command issuing an invitation has
// no HTTP identity and records none.
func (p *product) Issue(ctx context.Context, in IssueInvite) (Issued, error) {
	return p.issue(ctx, in, "")
}

// issue issues one invitation under id, or mints the identifier when id is
// empty. The management handler mints it before its mandatory audit row, so that
// row can name the invitation the action creates; every other caller leaves the
// minting here.
func (p *product) issue(ctx context.Context, in IssueInvite, id string) (Issued, error) {
	if err := checkTenant(in.Tenant); err != nil {
		return Issued{}, err
	}
	email, err := normalizeEmail(in.Email)
	if err != nil {
		return Issued{}, err
	}
	now := p.at()
	if budgetErr := p.chargeIssuer(ctx, now); budgetErr != nil {
		return Issued{}, budgetErr
	}
	deadline := in.ExpiresAt
	if deadline.IsZero() {
		deadline = now.Add(p.ttl())
	} else {
		deadline = utc(deadline)
		switch {
		case !deadline.After(now):
			return Issued{}, fmt.Errorf("%w: the deadline is not later than now", authn.ErrInvalidArgument)
		case deadline.After(now.Add(maxExplicitLifetime)):
			return Issued{}, fmt.Errorf("%w", ErrInvitationExpiryTooLong)
		}
	}
	if systemErr := p.refuseSystemTenant(ctx, in.Tenant); systemErr != nil {
		return Issued{}, systemErr
	}
	if addressErr := p.refuseIneligibleAddress(ctx, in.Tenant, email); addressErr != nil {
		return Issued{}, addressErr
	}
	if pendingErr := p.refusePendingInvitation(ctx, in.Tenant, email, now); pendingErr != nil {
		return Issued{}, pendingErr
	}
	if roleErr := p.requireGrantableRole(ctx, in.Tenant, in.Role); roleErr != nil {
		return Issued{}, roleErr
	}
	// The invitation's issuer is the effective account of the request: during
	// an impersonation that is the account impersonated, which is the identity
	// every view and notice shows. The operator is kept by the audit journal,
	// which fills the acting account and the impersonated one from the same
	// identity; the invitation does not record the operator as its issuer. A
	// request identity that carries no account is refused: recording it as an
	// empty issuer would make the invitation read as a host command and skip the
	// inviter checks at acceptance.
	invitedBy, _, issuerErr := issuerOf(authn.FromContext(ctx))
	if issuerErr != nil {
		return Issued{}, issuerErr
	}
	if id == "" {
		id, err = newID()
		if err != nil {
			return Issued{}, err
		}
	}
	token, err := newToken()
	if err != nil {
		return Issued{}, err
	}
	// Every check has passed and the row is ready, so this attempt will send:
	// the address's and the deployment's budgets are what it spends. A charge
	// whose insert does not land is given back below.
	release, budgetErr := p.chargeAttempt(ctx, email, now)
	if budgetErr != nil {
		return Issued{}, budgetErr
	}
	sent := now
	row := invitationRow{
		ID: id, TenantID: in.Tenant, Email: email, TokenDigest: tokenDigest(token),
		Status: statusPending, InvitedBy: invitedBy, Role: in.Role, ExpiresAt: utc(deadline),
		LastSentAt: sent, SendCount: 1,
		CreatedAt: now, UpdatedAt: now,
	}
	err = p.conn(ctx).Transaction(func(tx *gorm.DB) error {
		// An expired pending invitation for the same tenant and address is
		// replaced by this one. Its stored state stays pending, so it keeps
		// reading expired rather than becoming revoked; superseded_at records
		// that a later invitation took its place and frees the live slot the
		// partial unique index guards.
		if supErr := tx.Model(&invitationRow{}).
			Where("tenant_id = ? AND email = ? AND status = ? AND expires_at <= ? AND superseded_at IS NULL",
				in.Tenant, email, statusPending, now).
			Updates(map[string]any{"superseded_at": now, "updated_at": now}).Error; supErr != nil {
			return supErr
		}
		return tx.Create(&row).Error
	})
	switch {
	case duplicate(err):
		release()
		return Issued{}, fmt.Errorf("%w", ErrInvitationPending)
	case err != nil:
		release()
		return Issued{}, failed("issuing an invitation", err)
	}
	return Issued{Invitation: toInvitation(row), Token: token}, nil
}
