// Package webauthn is the WebAuthn second factor and, in the same module, the passkey login method. No type of the pinned 0.x library leaves the package; the root package's Factor, Step and LoginMethod are its surface.
package webauthn

import (
	"context"
	"embed"
	"fmt"
	"io/fs"
	"log/slog"
	"slices"

	libwebauthn "github.com/go-webauthn/webauthn/webauthn"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/config"
	"github.com/vislake/speed/pkg/core"
	"github.com/vislake/speed/pkg/db"
	"github.com/vislake/speed/pkg/log"
)

// ModuleName is this module's name in the registry. A host writes it into the
// command whose accounts authenticate with a passkey or a security key.
const ModuleName = "authn.mfa.webauthn"

// ConfigNamespace is where this module's input items are mounted, which is the
// module name.
const ConfigNamespace = ModuleName

const methodName = "passkey"

const (
	stepRegistration = "webauthn"
	stepAssertion    = "webauthn"
	stepPasskey      = "passkey"
)

const diagnosticPrefix = "authn: "

//go:embed migrations
var migrationRoot embed.FS

var migrationsFS = func() fs.FS {
	sub, err := fs.Sub(migrationRoot, "migrations")
	if err != nil {
		panic(ModuleName + ": the embedded migration tree: " + err.Error())
	}
	return sub
}()

func init() { core.ProcessRegistry.Register(Module()) }

var (
	_ authn.Factor      = (*product)(nil)
	_ authn.LoginMethod = (*product)(nil)
)

// Module is the descriptor a host registers. It delivers the factor and the passkey login method without claiming either, and requires the database, the multi-factor module and the account reads.
func Module() core.Module {
	return core.Module{
		Name: ModuleName,
		Requires: []core.Requirement{
			{Token: (*db.Database)(nil)},
			{Token: (*authn.MFA)(nil)},
			{Token: (*authn.Accounts)(nil)},
			{Token: (*log.Logger)(nil), Optional: true},
		},
		Provides: []core.Provision{
			{Token: (*authn.Factor)(nil)},
			{Token: (*authn.LoginMethod)(nil)},
		},
		Resources: []any{schema(), db.Migrations{FS: migrationsFS}},

		Prepare: func(_ context.Context, reg *core.Registry) (core.Enablement, error) {
			reader, err := readerOf(reg)
			if err != nil {
				return core.Enablement{}, err
			}
			if _, err = readConfig(reader); err != nil {
				return core.Enablement{}, err
			}
			return core.Enablement{State: core.StateEnabled}, nil
		},

		New: func(_ context.Context, reg *core.Registry) (any, error) {
			return newProduct(reg)
		},

		Init: func(_ context.Context, reg *core.Registry, instance any) error {
			if p, ok := productOf(instance); ok {
				return p.wire(reg)
			}
			return nil
		},

		Start: func(ctx context.Context, _ *core.Registry, instance any) error {
			if p, ok := productOf(instance); ok {
				return p.start(ctx)
			}
			return nil
		},

		Close: func(context.Context, *core.Registry, any) error { return nil },
	}
}

func productOf(instance any) (*product, bool) {
	p, ok := instance.(*product)
	return p, ok && p != nil
}

func readerOf(reg *core.Registry) (config.Reader, error) {
	reader, err := core.Resolve[config.Reader](reg)
	if err != nil {
		return nil, fmt.Errorf("%s%s: the relying party is built from configuration, and no reader was handed to "+
			"the registry before the assembly began: %w", diagnosticPrefix, ModuleName, err)
	}
	return reader, nil
}

type product struct {
	register *register
	accounts authn.Accounts
	mfa      authn.MFA
	wa       *libwebauthn.WebAuthn
	config   resolved

	logger *slog.Logger
}

func newProduct(reg *core.Registry) (*product, error) {
	reader, err := readerOf(reg)
	if err != nil {
		return nil, err
	}
	cfg, err := readConfig(reader)
	if err != nil {
		return nil, err
	}
	database, err := core.Resolve[db.Database](reg)
	if err != nil {
		return nil, fmt.Errorf("%s%s: taking up the database: %w", diagnosticPrefix, ModuleName, err)
	}
	mfa, err := core.Resolve[authn.MFA](reg)
	if err != nil {
		return nil, fmt.Errorf("%s%s: taking up the multi-factor module: %w", diagnosticPrefix, ModuleName, err)
	}
	accounts, err := core.Resolve[authn.Accounts](reg)
	if err != nil {
		return nil, fmt.Errorf("%s%s: taking up the account reads: %w", diagnosticPrefix, ModuleName, err)
	}
	wa, err := newRelyingParty(cfg)
	if err != nil {
		return nil, fmt.Errorf("%s%s: building the relying party: %w", diagnosticPrefix, ModuleName, err)
	}
	return build(database, mfa, accounts, wa, cfg), nil
}

func build(database db.Database, mfa authn.MFA, accounts authn.Accounts, wa *libwebauthn.WebAuthn, cfg resolved) *product {
	return &product{
		register: &register{handle: database.DB()},
		accounts: accounts,
		mfa:      mfa,
		wa:       wa,
		config:   cfg,
		logger:   log.Default(),
	}
}

func (p *product) wire(reg *core.Registry) error {
	if delivered, err := core.Resolve[log.Logger](reg); err == nil {
		p.logger = delivered.Named(ModuleName)
	}
	return nil
}

func (p *product) start(ctx context.Context) error {
	if err := p.register.probe(ctx); err != nil {
		return err
	}
	if !slices.Contains(p.mfa.Kinds(), authn.FactorWebAuthn) {
		p.logger.Warn(diagnosticPrefix + ModuleName + " is taking part but authn.mfa" +
			" did not take the webauthn factor in, so a credential registered here is never offered as a second factor")
	}
	return nil
}
