package db

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"reflect"
	"strings"
	"time"

	"gorm.io/gorm"

	"github.com/vislake/speed/pkg/config"
	"github.com/vislake/speed/pkg/core"
	"github.com/vislake/speed/pkg/internal/literal"
	"github.com/vislake/speed/pkg/log"
)

// Spec is what an implementation subpackage hands this package to become a module: its name, engine, configuration namespace, driver binding, constraint-refusal classification, connection-scoped locators, and a cross-process migration mutex when its engine appears in multi-replica deployments.
type Spec struct {
	// ModuleName is this implementation's name in the registry, its identity in
	// startup diagnostics and enablement lookups. A product carries the name of
	// the Spec its New was built from, and Migrate accepts only that one.
	ModuleName string
	// ConfigNamespace is where this implementation's input items are mounted in
	// the config data. Implementations give it the module name.
	ConfigNamespace string
	// Dialect is the engine behind the driver. It reaches dependants through
	// Database.Dialect and names the subdirectory of a migration set this
	// implementation reads.
	Dialect Dialect
	// Dialector binds this engine's driver to a connection locator.
	Dialector func(dsn string) gorm.Dialector
	// ConstraintViolation returns gorm.ErrDuplicatedKey when a unique constraint refused the statement, gorm.ErrForeignKeyViolated when a foreign key did, and nil for anything else, looking through wrapping as errors.As does. It is mandatory.
	ConstraintViolation func(err error) error
	// ConnectionScoped reports whether a locator names a database that belongs to the one connection opening it, such as SQLite's in-memory and temporary databases: every handle opened on it is held to a single connection that is never replaced. The locator is the trimmed one the driver receives; an engine with no such locator leaves it nil.
	ConnectionScoped func(dsn string) bool
	// NewMigrationLock builds the cross-process mutex the migration run is held
	// under, bounded by the configured timeout. Leaving it nil runs without a
	// mutex and keeps migration-lock-timeout out of the input items.
	NewMigrationLock func(timeout time.Duration) MigrationLock
	// DefaultMigrationLockTimeout is what migration-lock-timeout takes when the
	// configuration gives none. It is mandatory alongside NewMigrationLock and
	// has to be positive.
	DefaultMigrationLockTimeout time.Duration
}

// NewModule builds an implementation's module descriptor. A defective Spec
// panics here: a subpackage fills it in at init, so the fault is deterministic
// and leaves a host nothing to handle.
func NewModule(spec Spec) core.Module {
	spec.check()
	return core.Module{
		Name:      spec.ModuleName,
		Requires:  []core.Requirement{{Token: (*log.Logger)(nil), Optional: true}},
		Provides:  []core.Provision{{Token: (*Database)(nil), Exclusive: true}},
		Resources: []any{spec.schema()},
		Prepare: func(_ context.Context, reg *core.Registry) (core.Enablement, error) {
			reader, err := spec.reader(reg, "whether this database runs")
			if err != nil {
				return core.Enablement{}, err
			}
			return spec.prepare(reader)
		},
		New: func(ctx context.Context, reg *core.Registry) (any, error) {
			reader, err := spec.reader(reg, "the connection locator")
			if err != nil {
				return nil, err
			}
			// Taken up before the connection is opened, so a failure here
			// leaves nothing to release.
			plugins, err := collectPlugins(reg)
			if err != nil {
				return nil, err
			}
			cfg, err := spec.read(reader)
			if err != nil {
				return nil, err
			}
			logger, err := spec.logger(reg, cfg.Config)
			if err != nil {
				return nil, err
			}
			return spec.newDatabase(ctx, reader, plugins, logger)
		},
		Migrate: func(ctx context.Context, reg *core.Registry, instance any) error {
			product, err := spec.ownProduct(instance)
			if err != nil {
				return err
			}
			reader, err := spec.reader(reg, "the bound on waiting for the migration mutex")
			if err != nil {
				return err
			}
			return spec.migrate(ctx, reg, reader, product)
		},
		Close: func(_ context.Context, _ *core.Registry, instance any) error {
			return closeInstance(instance)
		},
	}
}

func (s Spec) check() {
	switch {
	case s.ModuleName == "":
		panic("db: a Spec carries the module name this implementation registers under")
	case s.ConfigNamespace == "":
		panic(fmt.Sprintf("db: module %q gives no configuration namespace, and its input items "+
			"have to hang under one of its own: implementations sharing a namespace conflict "+
			"as soon as a host imports both", s.ModuleName))
	case s.Dialect == "":
		panic(fmt.Sprintf("db: module %q gives no dialect, which is what names the subdirectory "+
			"its migrations are read from", s.ModuleName))
	case s.Dialector == nil:
		panic(fmt.Sprintf("db: module %q gives no dialector, and there is no other way to reach "+
			"its driver", s.ModuleName))
	case s.ConstraintViolation == nil:
		panic(fmt.Sprintf("db: module %q gives no ConstraintViolation, so its handles would report a "+
			"unique or foreign key refusal as the driver's raw error while every other engine "+
			"reports it through gorm.ErrDuplicatedKey and gorm.ErrForeignKeyViolated", s.ModuleName))
	case s.NewMigrationLock != nil && s.DefaultMigrationLockTimeout <= 0:
		panic(fmt.Sprintf("db: module %q supplies a migration mutex and no default for %s.%s, "+
			"so a run that configured none would give up on the mutex before waiting",
			s.ModuleName, s.ConfigNamespace, MigrationLockTimeoutKey))
	}
}

func (s Spec) reader(reg *core.Registry, what string) (config.Reader, error) {
	reader, err := core.Resolve[config.Reader](reg)
	if err != nil {
		return nil, fmt.Errorf("%s: %s is read from configuration, and no reading face had been "+
			"supplied to the %s module before assembly began: %w",
			s.ModuleName, what, config.ModuleName, err)
	}
	return reader, nil
}

// logger takes up the logger the statements of this implementation's handles
// are written through, naming it after this module, and falls back to the
// process default logger when no logging capability is delivered.
func (s Spec) logger(reg *core.Registry, cfg Config) (*slog.Logger, error) {
	delivered, err := core.Resolve[log.Logger](reg)
	if err != nil {
		if errors.Is(err, core.ErrMissingProvider) {
			return log.Default().With(log.ModuleAttrKey, s.ModuleName), nil
		}
		return nil, fmt.Errorf("%s: taking up the logger the statements of this implementation's handles "+
			"are written through: %w", s.ModuleName, err)
	}
	// Registered before Named: a redaction rule governs what is bound after it.
	literal.Register(delivered.Redaction(), s.ModuleName+".credentials", secretValues(cfg)...)
	named := delivered.Named(s.ModuleName)
	if named == nil {
		return nil, fmt.Errorf("%s: the provider of the log capability gave no logger when asked to name one for "+
			"this module, and the statements of this implementation's handles have nowhere to be written", s.ModuleName)
	}
	return named, nil
}

func secretValues(cfg Config) []string {
	values := make([]string, 0, 2+len(cfg.EncryptionRetiredKeys))
	values = append(values, cfg.DSN, cfg.EncryptionKey)
	return append(values, cfg.EncryptionRetiredKeys...)
}

func (s Spec) prepare(reader config.Reader) (core.Enablement, error) {
	cfg, err := s.read(reader)
	if err != nil {
		return core.Enablement{}, err
	}
	if strings.TrimSpace(cfg.DSN) == "" {
		return core.Enablement{}, fmt.Errorf("%s: %s.%s gives no connection locator, and a module "+
			"taken in at run level has to start: set it to run on %s, or leave %s out of the "+
			"command tree", s.ModuleName, s.ConfigNamespace, dsnKey, s.Dialect, s.ModuleName)
	}
	// A zero or negative wait is a single attempt, so on a first start every
	// replica but the one holding the mutex would give up at once.
	if s.NewMigrationLock != nil && cfg.MigrationLockTimeout <= 0 {
		return core.Enablement{}, fmt.Errorf("%s: %s.%s is %s, and a replica given no time to wait "+
			"gives up on the migration mutex at its first attempt, so on a first start every replica "+
			"but one fails: set it to a positive duration", s.ModuleName, s.ConfigNamespace,
			MigrationLockTimeoutKey, cfg.MigrationLockTimeout)
	}
	return core.Enablement{State: core.StateEnabled}, nil
}

func (s Spec) ownProduct(instance any) (*database, error) {
	product, ok := instance.(*database)
	if ok && product != nil && product.spec.ModuleName == s.ModuleName {
		return product, nil
	}

	what := fmt.Sprintf("an instance of type %T", instance)
	if ok && product != nil {
		what += fmt.Sprintf(", built by the New of module %q", product.spec.ModuleName)
	}
	return nil, fmt.Errorf("%w: module %q was supplied %s, and its Migrate accepts only an instance "+
		"built by its own New, the one place that installs plugins on a handle, tenant filtering and "+
		"statement auditing among them. No migration was applied. Supply an instance built by the New "+
		"of module %q, or supply none for it; a test that needs a stand-in for the database capability "+
		"registers a placeholder module that declares only db.Database and supplies the stand-in to "+
		"that one", ErrForeignInstance, s.ModuleName, what, s.ModuleName)
}

// migrate reads the timeout from the run being migrated, not from the section
// the product was built from: the bound on waiting for the mutex belongs to
// this run.
func (s Spec) migrate(ctx context.Context, reg *core.Registry, reader config.Reader, product *database) error {
	cfg, err := s.read(reader)
	if err != nil {
		return err
	}
	return applyMigrations(ctx, reg, product.handle, s.Dialect, s.lock(cfg))
}

func (s Spec) lock(cfg lockingConfig) MigrationLock {
	if s.NewMigrationLock == nil {
		return nil
	}
	return s.NewMigrationLock(cfg.MigrationLockTimeout)
}

func closeInstance(instance any) error {
	handle, ok := deliveredHandle(instance)
	if !ok {
		return nil
	}
	return Close(handle)
}

// deliveredHandle takes the handle out of an instance of the capability, or reports nothing to release. The nil test is by reflection because a typed nil pointer in an interface is not the nil interface. The host's DB may panic, and a panic out of this cleanup would take the rollback's own error with it, so it is guarded here.
func deliveredHandle(instance any) (handle *gorm.DB, ok bool) {
	product, isDatabase := instance.(Database)
	if !isDatabase {
		return nil, false
	}
	if v := reflect.ValueOf(product); v.Kind() == reflect.Pointer && v.IsNil() {
		return nil, false
	}
	defer func() {
		if recover() != nil {
			handle, ok = nil, false
		}
	}()
	handle = product.DB()
	if handle == nil {
		return nil, false
	}
	if _, err := handle.DB(); err != nil {
		return nil, false
	}
	return handle, true
}
