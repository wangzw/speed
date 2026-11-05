package policy

import (
	"context"
	"fmt"
	"sort"

	"github.com/vislake/speed/pkg/authn"
)

// MemberRoles implements authn.MemberRoleReader: one tenant's role names keyed
// by account, read through the manager's own binding listing.
//
// The read is bulk on purpose. A member page names every account it shows, and
// asking the policy table once per row would turn one page into as many reads as
// it has members. The names are the tenant's own role identifiers, ascending and
// without duplicates, as the seam promises; an account with no binding is absent
// from the answer, which the caller reads as no roles.
//
// The read is cross-tenant at the session level and names the tenant in the
// statement, the same shape every other read of these tables uses. An empty
// tenant names no rows and is refused rather than answered with an empty map,
// because an empty answer would read as "this tenant has no roles".
func (p *product) MemberRoles(ctx context.Context, tenant string) (map[authn.AccountID][]string, error) {
	if tenant == "" {
		return nil, fmt.Errorf("%w: the member role read names no tenant", authn.ErrInvalidArgument)
	}
	bindings, err := p.Bindings(ctx, tenant)
	if err != nil {
		return nil, err
	}
	roles := make(map[authn.AccountID][]string, len(bindings))
	for _, b := range bindings {
		id := authn.AccountID(b.Account)
		roles[id] = append(roles[id], string(b.Role))
	}
	for id, names := range roles {
		sort.Strings(names)
		roles[id] = names
	}
	return roles, nil
}
