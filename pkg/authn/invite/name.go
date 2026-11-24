package invite

import (
	"context"
	"errors"
	"log/slog"

	"github.com/vislake/speed/pkg/authn"
)

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

func (p *product) descriptionOf(ctx context.Context, id string) (authn.TenantDescription, error) {
	description, err := p.tenantDescs.TenantDescriptionOf(ctx, id)
	if err != nil {
		p.logger.Error(ModuleName+": the tenant's description could not be read",
			slog.String("cause", err.Error()))
		return authn.TenantDescription{}, err
	}
	return description, nil
}

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

type inviterNames struct {
	displayName string
	username    string
}

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
