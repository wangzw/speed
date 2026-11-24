// Package totp is the TOTP second factor: it generates and seals a seed, answers the otpauth address once, and verifies codes with replay protection.
package totp

import (
	"context"
	"embed"
	"fmt"
	"io/fs"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/authn/internal/clock"
	"github.com/vislake/speed/pkg/config"
	"github.com/vislake/speed/pkg/core"
	"github.com/vislake/speed/pkg/db"
)

// ModuleName is this module's name in the registry. A host writes it into the
// command whose users register a TOTP factor.
const ModuleName = "authn.mfa.totp"

// ConfigNamespace is where this module's input items are mounted, which is the
// module name.
const ConfigNamespace = ModuleName

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

var _ authn.Factor = (*factor)(nil)

// Module is the descriptor a host registers. It delivers the factor without claiming it and requires the database, the multi-factor module and the account reads.
func Module() core.Module {
	return core.Module{
		Name: ModuleName,
		Requires: []core.Requirement{
			{Token: (*db.Database)(nil)},
			{Token: (*authn.MFA)(nil)},
			{Token: (*authn.Accounts)(nil)},
		},
		Provides: []core.Provision{
			{Token: (*authn.Factor)(nil)},
		},
		Resources: []any{schema(), db.Migrations{FS: migrationsFS}},

		Prepare: func(_ context.Context, reg *core.Registry) (core.Enablement, error) {
			reader, err := readerOf(reg)
			if err != nil {
				return core.Enablement{}, err
			}
			if _, err := readConfig(reader); err != nil {
				return core.Enablement{}, err
			}
			return core.Enablement{State: core.StateEnabled}, nil
		},

		New: func(_ context.Context, reg *core.Registry) (any, error) {
			return newFactor(reg)
		},

		Start: func(ctx context.Context, _ *core.Registry, instance any) error {
			f, ok := factorOf(instance)
			if !ok {
				return nil
			}
			return f.start(ctx)
		},

		Stop:  func(context.Context, *core.Registry, any) error { return nil },
		Close: func(context.Context, *core.Registry, any) error { return nil },
	}
}

func factorOf(instance any) (*factor, bool) {
	f, ok := instance.(*factor)
	return f, ok && f != nil
}

func readerOf(reg *core.Registry) (config.Reader, error) {
	reader, err := core.Resolve[config.Reader](reg)
	if err != nil {
		return nil, fmt.Errorf("%s: the issuer is read from configuration and no reader was handed to the "+
			"registry before the assembly began: %w", ModuleName, err)
	}
	return reader, nil
}

func newFactor(reg *core.Registry) (*factor, error) {
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
		return nil, fmt.Errorf("%s: taking up the database: %w", ModuleName, err)
	}
	if _, err = core.Resolve[authn.MFA](reg); err != nil {
		return nil, fmt.Errorf("%s: taking up the multi-factor module: %w", ModuleName, err)
	}
	accounts, err := core.Resolve[authn.Accounts](reg)
	if err != nil {
		return nil, fmt.Errorf("%s: taking up the account reads: %w", ModuleName, err)
	}
	return &factor{
		register: &register{handle: database.DB()},
		accounts: accounts,
		issuer:   cfg.issuer,
		clock:    clock.Now,
	}, nil
}

func (f *factor) start(ctx context.Context) error {
	if err := f.register.probe(ctx); err != nil {
		return err
	}
	if err := f.register.roundTrip(ctx); err != nil {
		return fmt.Errorf("%w: %s: %w", authn.ErrNoEncryptionKey, ModuleName, err)
	}
	return nil
}
