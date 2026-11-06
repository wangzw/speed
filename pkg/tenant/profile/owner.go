package profile

import (
	"context"
	"errors"
	"log/slog"
	"net/http"

	"github.com/vislake/speed/pkg/authn"
	speedhttp "github.com/vislake/speed/pkg/http"
)

// ownerNames resolves the two fields a client reads the owner by: the account's
// display name and its username. Both routes share it, so the fallbacks and the
// failure rule are stated once.
//
// The account identifier is never a fallback for either field. An empty owner,
// an absent account read, an identifier that does not parse, a static
// credential-table subject (which has no account row) and an account the store
// does not know all leave both fields empty; the surface then shows unresolved
// copy rather than an internal identifier.
//
// A read that fails for any other reason is a 503 with no cause on the wire and
// the cause in the log, the shape the role-usage read uses. A display name
// without a username identifier is legal: the username stays empty.
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
