package invite

import (
	"context"
	"errors"
	"log/slog"

	"github.com/vislake/speed/pkg/authn"
)

// nameOf answers the name a tenant identifier is addressed by. With no
// resolver assembled it answers the empty string: the identifier is internal
// and must not stand in for the name.
func (p *product) nameOf(ctx context.Context, id string) (string, error) {
	if p.tenantNames == nil {
		return "", nil
	}
	name, nameErr := p.tenantNames.TenantNameOf(ctx, id)
	if nameErr != nil {
		p.logger.Error(ModuleName+": the tenant name could not be read",
			slog.String("cause", nameErr.Error()))
		return "", nameErr
	}
	return name, nil
}

// descriptionOf answers what a tenant is called, what it is shown by and whether
// it is the system tenant. The resolver is required, so there is no absent case
// to fall back on: a read that fails is logged and handed back, so the route
// answers unavailable rather than showing a view with a guessed name.
func (p *product) descriptionOf(ctx context.Context, id string) (authn.TenantDescription, error) {
	description, err := p.tenantDescs.TenantDescriptionOf(ctx, id)
	if err != nil {
		p.logger.Error(ModuleName+": the tenant's description could not be read",
			slog.String("cause", err.Error()))
		return authn.TenantDescription{}, err
	}
	return description, nil
}

// inviterNameOf answers the display name the account that issued an invitation
// is recognized by. An empty identifier is a host command's invitation, which
// has no inviter to name, and an account that no longer exists is a tombstone
// with no name; both answer the empty string. Any other read failure is handed
// back, so the notice and the link do not carry a name the store could not
// produce.
func (p *product) inviterNameOf(ctx context.Context, id authn.AccountID) (string, error) {
	if id == "" {
		return "", nil
	}
	account, err := p.accounts.Get(ctx, id)
	switch {
	case errors.Is(err, authn.ErrAccountNotFound):
		return "", nil
	case err != nil:
		return "", err
	}
	return account.DisplayName, nil
}

// inviterNames is what a management view says about the account that issued an
// invitation. Either part can be empty: an account need not have chosen a
// display name, and an account imported without a username has none.
type inviterNames struct {
	displayName string
	username    string
}

// inviterNamesOf resolves the issuer of each invitation, once per distinct
// account across the whole page. An invitation a host command issued (empty
// invited_by) and an account that no longer exists have no entry, so a view
// shows neither name; the issuer identifier stays in the view.
//
// Names are decoration on a management view, not something the page needs to be
// correct: a read that cannot answer omits the affected names and is logged
// rather than failing the page. Failing the page would hide a written
// invitation whose one-time token is already the only copy of its acceptance
// link, so a store that cannot name an inviter must not take the invitation
// down with it. A bulk username read that fails leaves the display names that
// were already read in place and omits only the usernames.
func (p *product) inviterNamesOf(ctx context.Context, invitations []Invitation) map[authn.AccountID]inviterNames {
	names := make(map[authn.AccountID]inviterNames, len(invitations))
	live := make([]authn.AccountID, 0, len(invitations))
	seen := make(map[authn.AccountID]bool, len(invitations))
	for _, invitation := range invitations {
		if invitation.InvitedBy == "" || seen[invitation.InvitedBy] {
			continue
		}
		seen[invitation.InvitedBy] = true
		account, err := p.accounts.Get(ctx, invitation.InvitedBy)
		switch {
		case errors.Is(err, authn.ErrAccountNotFound):
			continue
		case err != nil:
			p.logger.Error(ModuleName+": the inviter's display name could not be read, so the view names no inviter",
				slog.String("invitation", invitation.ID),
				slog.String("cause", err.Error()))
			continue
		}
		names[invitation.InvitedBy] = inviterNames{displayName: account.DisplayName}
		live = append(live, invitation.InvitedBy)
	}
	if len(live) == 0 {
		return names
	}
	identifiers, err := p.accounts.IdentifiersMany(ctx, live)
	if err != nil {
		p.logger.Error(ModuleName+": the inviters' usernames could not be read, so the views name no username",
			slog.Int("inviters", len(live)),
			slog.String("cause", err.Error()))
		return names
	}
	for id, list := range identifiers {
		for _, identifier := range list {
			if identifier.Kind == authn.IdentifierUsername {
				names[id] = inviterNames{displayName: names[id].displayName, username: identifier.Value}
				break
			}
		}
	}
	return names
}
