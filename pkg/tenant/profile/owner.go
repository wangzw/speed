package profile

import (
	"context"
	"errors"
	"log/slog"
	"net/http"

	"github.com/vislake/speed/pkg/authn"
	speedhttp "github.com/vislake/speed/pkg/http"
)

func (p *product) ownerNames(ctx context.Context, ownerAccount string) (string, string, error) {
	if ownerAccount == "" || p.accounts == nil {
		return "", "", nil
	}
	id, err := authn.ParseAccountID(ownerAccount)
	if err != nil || id.IsStatic() {
		return "", "", nil
	}
	account, err := p.accounts.Get(ctx, id)
	if err != nil {
		if errors.Is(err, authn.ErrAccountNotFound) {
			return "", "", nil
		}
		p.logger.Error(ModuleName+": the owner's account could not be read, so the owner is unresolved",
			slog.String("cause", err.Error()))
		return "", "", speedhttp.Fault{Status: http.StatusServiceUnavailable}
	}
	identifiers, err := p.accounts.IdentifiersMany(ctx, []authn.AccountID{id})
	if err != nil {
		p.logger.Error(ModuleName+": the owner's identifiers could not be read, so the username is unresolved",
			slog.String("cause", err.Error()))
		return "", "", speedhttp.Fault{Status: http.StatusServiceUnavailable}
	}
	username := ""
	for _, identifier := range identifiers[id] {
		if identifier.Kind == authn.IdentifierUsername {
			username = identifier.Value
			break
		}
	}
	return account.DisplayName, username, nil
}
