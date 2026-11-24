package policy

import (
	"context"
	"fmt"
	"sort"

	"github.com/vislake/speed/pkg/authn"
)

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
