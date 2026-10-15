// Package webauthn is the WebAuthn factor of the authentication capability and,
// in the same module, the passkey login method. It delivers authn.Factor with
// the kind webauthn and authn.LoginMethod under the name passkey.
//
// As a factor it proves a second factor with an authenticator-bound credential:
// it registers a credential and verifies an assertion against it, and it keeps
// the credential's own state, its counter, its backup flags and whether it is a
// passkey. As a login method it proves a first factor the same way, through a
// discoverable credential the client chooses, which needs no password and can
// itself count as two factors when the assertion verifies the user.
//
// The library that speaks the protocol is at version 0.x and its types change
// between releases, so none of its types leaves this package: the root
// package's Factor, Step and LoginMethod are the surface, and the library is
// named only inside this package. The failure back-off, the audit row and the
// event around a factor call are done by the multi-factor module, not here; this
// module does cryptography and the state of its own.
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

// methodName is the name this module reports as a login method. It is a constant
// of the code: it appears in routes, flow records and metric attributes, and a
// published name never changes.
const methodName = "passkey"

// The module's own names for the steps the engine keeps in the flow record, and
// the field a client submits its WebAuthn response in.
const (
	stepRegistration = "webauthn"
	stepAssertion    = "webauthn"
	stepPasskey      = "passkey"
)

// diagnosticPrefix starts every line this module writes to the startup
// diagnostics, which have several writers.
const diagnosticPrefix = "authn: "

//go:embed migrations
var migrationRoot embed.FS

// migrationsFS is migrationRoot with the wrapping directory removed, so that the
// tree the database module sees has one directory per dialect at its root.
var migrationsFS = func() fs.FS {
	sub, err := fs.Sub(migrationRoot, "migrations")
	if err != nil {
		// The embed directive is what makes this unreachable: the directory is
		// part of the source tree or the build fails first.
		panic(ModuleName + ": the embedded migration tree: " + err.Error())
	}
	return sub
}()

// The module registers itself. Importing this package is what puts it in a
// binary's available set.
func init() { core.ProcessRegistry.Register(Module()) }

// product must deliver what the descriptor declares.
var (
	_ authn.Factor      = (*product)(nil)
	_ authn.LoginMethod = (*product)(nil)
)

// Module is the descriptor a host registers.
//
// The database, the multi-factor module and the account reads are required, and
// each is delivered exclusively by another module, so the closure of a command
// that names this one does not pull them in: the host writes them down. The
// multi-factor module is required because this module's factor is taken in by it
// and its passkey login reaches a second factor through it; a command that named
// this module without it could register a credential no login could use.
//
// The stance is StateEnabled and never StateAuto: the module delivers both
// capabilities without claiming either exclusively, and a standalone provider
// that states StateAuto is the cell resolution disables.
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

		// New builds the relying party from configuration and reads no table of
		// its own: this stage runs before the migrations are applied, so on a
		// first start the tables are not there yet.
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

		// The handle belongs to the database module, which closes it.
		Close: func(context.Context, *core.Registry, any) error { return nil },
	}
}

// productOf narrows an instance to this module's product. The callbacks are also
// handed an instance a host supplied for the module, which can be any value that
// delivers the capabilities; they act on the module's own product and leave a
// supplied one as it is.
func productOf(instance any) (*product, bool) {
	p, ok := instance.(*product)
	return p, ok && p != nil
}

// readerOf takes up the configuration reader. Its absence is the host's omission
// and is carried out as it stands, not read as "nothing was configured".
func readerOf(reg *core.Registry) (config.Reader, error) {
	reader, err := core.Resolve[config.Reader](reg)
	if err != nil {
		return nil, fmt.Errorf("%s%s: the relying party is built from configuration, and no reader was handed to "+
			"the registry before the assembly began: %w", diagnosticPrefix, ModuleName, err)
	}
	return reader, nil
}

// product is the module's instance. It delivers the factor and the login method.
type product struct {
	register *register
	accounts authn.Accounts
	mfa      authn.MFA
	wa       *libwebauthn.WebAuthn
	config   resolved

	logger *slog.Logger
}

// newProduct takes up what the descriptor requires and builds the relying party.
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

// build assembles a product from its parts. The logger starts as the process's,
// so that a product that has not been through Init is still one that does not
// panic.
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

// wire runs in Init, where every instance exists: it takes up the logging
// capability.
func (p *product) wire(reg *core.Registry) error {
	if delivered, err := core.Resolve[log.Logger](reg); err == nil {
		p.logger = delivered.Named(ModuleName)
	}
	return nil
}

// start runs in Start: the two tables must be there, and the multi-factor module
// must have taken this factor's kind in, or a credential this module registers
// would never be offered.
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
