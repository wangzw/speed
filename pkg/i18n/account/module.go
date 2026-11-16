// Package account is the bridge between the i18n capability and the accounts of
// the authentication capability. It registers a module named "i18n.account"
// that delivers i18n.Profile — the seam the engine resolves a user's stored
// language and time zone through — and implements authn.Provisioner, seeding
// those two preferences into the account a first login creates.
//
// The preferences live in the account's attribute set, under the keys the i18n
// package names. That set already reaches every layer through the identity and
// needs no schema of its own, so this bridge only reads it back and writes it
// once, at sign-up.
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

// init registers this module with the process registry.
func init() { core.ProcessRegistry.Register(Module()) }

// The bridge delivers a non-exclusive profile and implements the provisioning
// hook, both taken up by resolution rather than named.
var (
	_ i18n.Profile      = (*bridge)(nil)
	_ authn.Provisioner = (*bridge)(nil)
)

// Module is this module's descriptor, exported for the registries that do not
// inherit the process-level registrations.
//
// The account store is a required dependency: the profile reads an account's
// attributes through it, and without accounts there is nothing to read. Both
// delivered capabilities are non-exclusive — a host may stand its own profile
// in the engine's place, and the login engine takes every provisioner.
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

// bridge reads an account's stored preferences and seeds them at sign-up.
type bridge struct {
	accounts authn.Accounts
}

// LocaleFor returns the language stored for an account, and whether one is. An
// account that does not exist stores no preference: the store's answer is
// definite, so it reads as "none" rather than as a failed read. A store that
// cannot answer is reported as an error rather than folded into "no
// preference": a caller that logs a fallback needs to tell the two apart.
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

// TimeZoneFor returns the time zone stored for an account, whether one is, and
// the failure that kept the store from answering. A malformed identifier, a
// missing account and an absent or empty attribute all read as "none, no
// failure"; every other store failure is reported. The caller renders in UTC
// for the first and retries for the second: collapsing them would freeze a
// wrong instant into a message the recipient cannot re-render.
func (b *bridge) TimeZoneFor(ctx context.Context, account string) (string, bool, error) {
	return b.attribute(ctx, account, i18n.TimeZoneAttributeKey)
}

// attribute reads one attribute of an account. A malformed identifier, an
// account that does not exist, and an absent or empty attribute read as "none",
// so a preference a profile cannot reach falls back rather than failing the
// caller. Any other store error is returned: the account read failed, which is
// not the same answer as an account that stores nothing.
func (b *bridge) attribute(ctx context.Context, account, key string) (string, bool, error) {
	id, err := authn.ParseAccountID(account)
	if err != nil {
		return "", false, nil
	}
	acct, err := b.accounts.Get(ctx, id)
	if errors.Is(err, authn.ErrAccountNotFound) {
		// A deleted or never-created account is a definite answer from the
		// store, not a read that failed: there is no preference to find.
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	value, ok := acct.Attributes[key]
	return value, ok && value != "", nil
}

// Provision seeds the language and time zone the request carried into the
// account about to be created. It runs on the login engine's sign-up path,
// where the negotiation layer has already written the request's locale into the
// context; a request that named none seeds neither and the account falls back
// to the default. It changes nothing else, as the seam requires.
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
