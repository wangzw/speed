// Package token carries the identity system's access tokens, a short-lived JWT
// signed with a configured key, and refresh tokens, an opaque string whose digest
// is stored; a token never carries a secret into a log line, error, event or metric.
package token

import (
	"context"
	"embed"
	"fmt"
	"io/fs"
	"strings"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/authn/internal/mount"
	"github.com/vislake/speed/pkg/config"
	"github.com/vislake/speed/pkg/core"
	"github.com/vislake/speed/pkg/db"
	"github.com/vislake/speed/pkg/log"
	"github.com/vislake/speed/pkg/settings"
)

// ModuleName is this module's name in the registry. A host writes it into the
// commands whose users are handed tokens.
const ModuleName = "authn.token"

// ConfigNamespace is where this module's input items and settings are mounted.
const ConfigNamespace = ModuleName

// defaultEndpoint is the endpoint this module's OpenAPI fragment declares its
// routes for; the mounter resolves the endpoint actually used, so this is only
// the conventional name the static declaration carries.
const defaultEndpoint = "account"

//go:embed openapi/token.json
var tokenFragment []byte

const backendName = "token"

const diagnosticPrefix = "authn: "

const tableName = "authn_refresh_tokens"

//go:embed migrations
var migrationRoot embed.FS

var migrationsFS = func() fs.FS {
	sub, err := fs.Sub(migrationRoot, "migrations")
	if err != nil {
		// The embed directive above makes this unreachable: the directory is
		// part of the source tree or the build fails first.
		panic(ModuleName + ": the embedded migration tree: " + err.Error())
	}
	return sub
}()

func init() { core.ProcessRegistry.Register(Module()) }

// Module is the descriptor a host registers. Tokens is delivered exclusively and
// Verifier non-exclusively; the stance is StateEnabled, so a configuration the
// module cannot run on is an error from Prepare.
func Module() core.Module {
	return core.Module{
		Name: ModuleName,
		Requires: []core.Requirement{
			{Token: (*db.Database)(nil)},
			{Token: (*mount.Mounter)(nil)},
			{Token: (*authn.Accounts)(nil)},
			{Token: (*authn.Sessions)(nil)},
			{Token: (*authn.Passwords)(nil)},
			{Token: (*authn.Throttle)(nil)},
			{Token: (*settings.Store)(nil), Optional: true},
			{Token: (*log.Logger)(nil), Optional: true},
		},
		Provides: []core.Provision{
			{Token: (*authn.Tokens)(nil), Exclusive: true},
			{Token: (*authn.Verifier)(nil)},
		},
		Resources: []any{
			schema(), settingsSchema(),
			db.Migrations{FS: migrationsFS},
			tokenSpec,
		},

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

		// New loads the key set and builds the product. It reads none of its
		// tables: this stage runs before the migrations are applied.
		New: func(_ context.Context, reg *core.Registry) (any, error) {
			reader, err := readerOf(reg)
			if err != nil {
				return nil, err
			}
			cfg, err := readConfig(reader)
			if err != nil {
				return nil, err
			}
			keys, diags, err := loadKeySet(cfg)
			if err != nil {
				return nil, err
			}
			database, err := core.Resolve[db.Database](reg)
			if err != nil {
				return nil, err
			}
			mounter, err := core.Resolve[mount.Mounter](reg)
			if err != nil {
				return nil, err
			}
			accounts, err := core.Resolve[authn.Accounts](reg)
			if err != nil {
				return nil, err
			}
			sessions, err := core.Resolve[authn.Sessions](reg)
			if err != nil {
				return nil, err
			}
			passwords, err := core.Resolve[authn.Passwords](reg)
			if err != nil {
				return nil, err
			}
			throttle, err := core.Resolve[authn.Throttle](reg)
			if err != nil {
				return nil, err
			}
			p := newProduct(cfg, keys, database, mounter, accounts, sessions, passwords, throttle)
			p.diagnostics = diags
			return p, nil
		},

		Init: func(_ context.Context, reg *core.Registry, instance any) error {
			p, ok := productOf(instance)
			if !ok {
				return nil
			}
			return p.wire(reg)
		},

		Start: func(ctx context.Context, _ *core.Registry, instance any) error {
			p, ok := productOf(instance)
			if !ok {
				return nil
			}
			return p.start(ctx)
		},

		Stop: func(ctx context.Context, _ *core.Registry, instance any) error {
			if p, ok := productOf(instance); ok {
				p.stop(ctx)
			}
			return nil
		},
	}
}

// productOf recovers this module's product from what the registry hands back. A
// nil instance or one of another type is tolerated: the host may have supplied an
// instance of its own, and every callback leaves it alone.
func productOf(instance any) (*product, bool) {
	p, ok := instance.(*product)
	return p, ok && p != nil
}

func readerOf(reg *core.Registry) (config.Reader, error) {
	reader, err := core.Resolve[config.Reader](reg)
	if err != nil {
		return nil, fmt.Errorf("%s%s: the keys are read from configuration, and no reader was handed to the "+
			"registry before the assembly began: %w", diagnosticPrefix, ModuleName, err)
	}
	return reader, nil
}

// start probes the module's table and then launches the refresh-token sweep: the
// probe comes first because New does not touch the table, which a first start
// only has once the migration has run.
func (p *product) start(ctx context.Context) error {
	if err := p.probe(ctx); err != nil {
		return err
	}
	p.startSweep(ctx)
	return nil
}

func (p *product) stop(_ context.Context) {
	p.stopSweep()
}

func (p *product) probe(ctx context.Context) error {
	err := p.handle.WithContext(ctx).Exec("SELECT 1 FROM " + tableName + " WHERE 1 = 0").Error
	switch {
	case err == nil:
		return nil
	case isMissingTable(err):
		return fmt.Errorf("%w: the table %s does not exist. Apply this module's migrations: include it "+
			"in the command that migrates the database, with every declaration of it taken in "+
			"(IncludeAllDeclarations)", authn.ErrNotMigrated, tableName)
	}
	return fmt.Errorf("%s%s: probing the table %s: %w", diagnosticPrefix, ModuleName, tableName, err)
}

// isMissingTable reports whether a failure is the database saying the table does
// not exist. SQLite says "no such table: authn_refresh_tokens"; PostgreSQL says
// `relation "authn_refresh_tokens" does not exist`.
func isMissingTable(err error) bool {
	if err == nil {
		return false
	}
	text := err.Error()
	return strings.Contains(text, "no such table") || strings.Contains(text, "does not exist")
}
