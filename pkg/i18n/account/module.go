// Package account bridges the i18n capability and the authn accounts: it
// registers the i18n.account module, which delivers i18n.Profile and implements
// authn.Provisioner to seed the locale and time zone at sign-up.
package account

import (
	"context"
	"errors"
	"fmt"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/core"
	"github.com/vislake/speed/pkg/i18n"
)

// ModuleName is this module's name in the registry.
const ModuleName = "i18n.account"

func init() { core.ProcessRegistry.Register(Module()) }

var (
	_ i18n.Profile      = (*bridge)(nil)
	_ authn.Provisioner = (*bridge)(nil)
)

// Module is this module's descriptor. The account store is required, and both
// delivered capabilities are non-exclusive.
func Module() core.Module {
	return core.Module{
		Name: ModuleName,
		Requires: []core.Requirement{
			{Token: (*authn.Accounts)(nil)},
		},
		Provides: []core.Provision{
			{Token: (*i18n.Profile)(nil)},
			{Token: (*authn.Provisioner)(nil)},
		},

		New: func(_ context.Context, reg *core.Registry) (any, error) {
			accounts, err := core.Resolve[authn.Accounts](reg)
			if err != nil {
				return nil, fmt.Errorf("i18n.account: taking up the account store the profile reads: %w", err)
			}
			return &bridge{accounts: accounts}, nil
		},
	}
}

type bridge struct {
	accounts authn.Accounts
}

func (b *bridge) LocaleFor(ctx context.Context, account string) (i18n.Locale, bool, error) {
	value, ok, err := b.attribute(ctx, account, i18n.LocaleAttributeKey)
	if err != nil {
		return "", false, err
	}
	if !ok {
		return "", false, nil
	}
	return i18n.Locale(value), true, nil
}

func (b *bridge) TimeZoneFor(ctx context.Context, account string) (string, bool, error) {
	return b.attribute(ctx, account, i18n.TimeZoneAttributeKey)
}

func (b *bridge) attribute(ctx context.Context, account, key string) (string, bool, error) {
	id, err := authn.ParseAccountID(account)
	if err != nil {
		return "", false, nil
	}
	acct, err := b.accounts.Get(ctx, id)
	if errors.Is(err, authn.ErrAccountNotFound) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	value, ok := acct.Attributes[key]
	return value, ok && value != "", nil
}

func (b *bridge) Provision(ctx context.Context, p authn.CreateAccount, _ string) (authn.CreateAccount, error) {
	locale, hasLocale := i18n.FromContext(ctx)
	zone, hasZone := i18n.TimeZoneFromContext(ctx)
	if !hasLocale && !hasZone {
		return p, nil
	}
	if p.Attributes == nil {
		p.Attributes = map[string]string{}
	}
	if hasLocale {
		p.Attributes[i18n.LocaleAttributeKey] = string(locale)
	}
	if hasZone {
		p.Attributes[i18n.TimeZoneAttributeKey] = zone
	}
	return p, nil
}
