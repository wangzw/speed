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
	"github.com/vislake/speed/pkg/log"
)

// Spec is what an implementation subpackage hands this package to become a
// module. It is the whole of what an implementation contributes: a name, an
// engine, the namespace its input items hang under, the driver binding, how
// its driver reports a constraint refusal, which locators name a database
// private to one connection, and a cross-process migration mutex when its
// engine appears in multi-replica deployments.
//
// Everything else — the input item declaration, the stance it states in
// Prepare, the connection, the plugins, the encryption serializer, the logging
// of statements (the logger the statements of the implementation's handles are
// written through, taken up in New), the migration run and the release of the
// pool — is this package's, shared by every implementation. That split is what
// keeps the implementations from drifting apart in the behaviour a host sees:
// an engine that put its own Prepare together would be one edit away from
// failing the startup on a different condition than its neighbour.
type Spec struct {
	// ModuleName is this implementation's name in the registry: its identity
	// in the startup diagnostics and in an enablement lookup. It is also what
	// marks a product as this implementation's own: a product carries the name
	// of the Spec its New was built from, and Migrate accepts an instance that
	// carries this one.
	ModuleName string
	// ConfigNamespace is where this implementation's input items are mounted
	// in the config data. Implementations give it the module name, so a
	// diagnostic naming the module and an error naming a key read as the same
	// thing; each pins that for itself.
	ConfigNamespace string
	// Dialect is the engine behind the driver. It reaches dependants through
	// Database.Dialect, and it names the subdirectory of a migration set this
	// implementation reads.
	Dialect Dialect
	// Dialector binds this engine's driver to a connection locator. It is the
	// one place a third-party driver enters, which is what lets a host move
	// between engines without touching the code that uses the database.
	Dialector func(dsn string) gorm.Dialector
	// ConstraintViolation classifies an error this engine's driver reports:
	// gorm.ErrDuplicatedKey when a unique constraint refused the statement —
	// a primary key is one — gorm.ErrForeignKeyViolated when a foreign key
	// did, and nil for anything else. It looks through wrapping, the way
	// errors.As does. Every handle this package opens attaches that class to
	// the driver's error, which stays in the chain beside it, so a host tells
	// the two refusals apart with errors.Is on either engine.
	//
	// It is mandatory: an engine without it would report those refusals as
	// its driver's raw error, and the same check would find a duplicate on
	// one engine and nothing on the other.
	ConstraintViolation func(err error) error
	// ConnectionScoped reports whether a locator names a database that
	// belongs to the one connection opening it — SQLite's in-memory and
	// temporary databases are — so that over a pool of several connections
	// one handle would stand for several databases. Every connection this
	// package opens on such a locator, the delivered one and each one Open
	// builds, is held to a single connection that is never replaced. The
	// locator it is handed is the trimmed one the driver receives. An
	// implementation whose engine has no such locator leaves it nil.
	ConnectionScoped func(dsn string) bool
	// NewMigrationLock builds the cross-process mutex the migration run is
	// held under, bounded by the configured timeout. An implementation whose
	// engine does not appear in multi-replica deployments leaves it nil, and
	// the run proceeds without a mutex.
	//
	// Leaving it nil also keeps migration-lock-timeout out of this
	// implementation's input items: a dial that changes nothing is worse than
	// no dial, because setting it looks like it did something.
	NewMigrationLock func(timeout time.Duration) MigrationLock
	// DefaultMigrationLockTimeout is what migration-lock-timeout takes when
	// the configuration gives none. It is mandatory alongside
	// NewMigrationLock: the value travels into the mutex, and a zero one
	// would give up before it had waited at all.
	DefaultMigrationLockTimeout time.Duration
}

// NewModule builds an implementation's module descriptor.
//
// A defective Spec panics here rather than being reported: a subpackage fills
// it in at init, so the fault is in this repository's own code, shows up
// deterministically on the first run of any host that imports it, and leaves a
// host nothing to handle. It is the treatment core gives an illegal token, for
// the same reason.
//
// The reading face the callbacks take up is absent from Requires, and that is
// the rule rather than an omission: it is the instance of the config module,
// which the host supplies before assembly begins, so it exists before the first
// Prepare runs and an edge to it would have nothing to order. What Requires
// does carry is the logging capability, as an optional requirement. It is
// optional because a database has to work in an assembly that never took
// logging in. It is declared because an assembly that did take it in has to have
// it constructed first, so that New finds the logger every statement of the
// handles it opens is written through. Prepare does not take it up: it opens no
// handle, and a logging module that is constructed has no instance until its own
// New has run.
//
// A host may supply an instance for this module in place of the one its New
// builds, and neither Prepare nor New then runs. Migrate accepts such an
// instance only when this module's own New built it, and refuses any other with
// ErrForeignInstance: the plugins and the encryption serializer are installed in
// New, so a handle that did not come through it has none of them. An instance
// built by this module's New in another registry keeps the logger that registry
// gave it: its statements, and those of the connections its Open builds, are
// written through that one and not through the logger of the registry the
// instance is supplied to. Once that registry's log module has been released,
// the records the instance writes are dropped by the log module and reach
// neither that registry's destinations nor those of the registry that received
// it. Close releases the pool behind the handle any
// instance reports, and leaves alone one that gives it no pool to release.
func NewModule(spec Spec) core.Module {
	spec.check()
	return core.Module{
		Name: spec.ModuleName,
		// Optional, because a database has to work in an assembly that never
		// took logging in, and the example hosts' migrate commands name only
		// the engine; declared, because an assembly that did take it in has to
		// have it constructed first. Without the edge nothing orders the two
		// but their names, which put the database ahead of the logging module,
		// and the lookup in New would find no logger and fall back to the
		// process default without a sign.
		Requires: []core.Requirement{{Token: (*log.Logger)(nil), Optional: true}},
		// Exclusive, because two engines behind one capability is not a
		// configuration this module could serve: the dependants would each
		// take up whichever one resolution happened to leave standing, and
		// their tables would be split across two databases. Two configured
		// implementations therefore fail the startup instead.
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
			// The declarations and the logger are taken up before the
			// connection is opened: taking them up is not a database action,
			// so a defective declaration or a failed lookup fails the startup
			// with nothing yet opened to release.
			plugins, err := collectPlugins(reg)
			if err != nil {
				return nil, err
			}
			logger, err := spec.logger(reg)
			if err != nil {
				return nil, err
			}
			return spec.newDatabase(ctx, reader, plugins, logger)
		},
		Migrate: func(ctx context.Context, reg *core.Registry, instance any) error {
			// The instance is judged before anything else is read or issued:
			// a refusal is about what the host supplied, and a registry that
			// also lacks the reading face should be told about the instance,
			// which is the mistake to undo first.
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

// check refuses a Spec that cannot produce a working module. Each condition is
// one this package would otherwise meet much later and much further from its
// cause: a missing driver binding as a nil call inside New, a mutex with no
// default timeout as a startup that gives up on the mutex before it has waited.
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

// reader takes up the configuration. what names the thing being read, so a
// host that supplied no reading face is told which of this module's decisions
// it broke rather than that something was unavailable.
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
// are written through: the logging capability's, named after this module, when
// the assembly delivers one, and the process default logger otherwise.
//
// The absence of the capability is a legal assembly and is told apart from a
// failed lookup by the sentinel: anything other than "no provider" is the
// lookup itself failing, two providers of the capability among them, and that,
// like a provider that names no logger, terminates the startup rather than quietly sending the statements somewhere
// the host did not configure.
//
// The fallback carries the module attribute Named would have bound. The
// records this module writes — "statement", "slow statement", "gorm warning" —
// are common words that name no writer on their own, so a record that reached
// the process default without the attribute would be anonymous.
func (s Spec) logger(reg *core.Registry) (*slog.Logger, error) {
	delivered, err := core.Resolve[log.Logger](reg)
	if err != nil {
		if errors.Is(err, core.ErrMissingProvider) {
			return log.Default().With(log.ModuleAttrKey, s.ModuleName), nil
		}
		return nil, fmt.Errorf("%s: taking up the logger the statements of this implementation's handles "+
			"are written through: %w", s.ModuleName, err)
	}
	named := delivered.Named(s.ModuleName)
	if named == nil {
		return nil, fmt.Errorf("%s: the provider of the log capability gave no logger when asked to name one for "+
			"this module, and the statements of this implementation's handles have nowhere to be written", s.ModuleName)
	}
	return named, nil
}

// prepare states this implementation's stance, or reports what it cannot start
// without, judging by its own section.
//
// A configured locator states StateEnabled rather than StateAuto, and the
// difference is the whole point of the stance here. Resolution stands down an
// exclusive provider that states StateAuto, so with StateAuto a host that
// configured two engines would get one of them silently, with half its tables
// in a database nobody mentioned. Stating enabled makes that assembly fail.
//
// An absent or blank locator fails the startup, naming the key. This
// implementation is in the run because the command tree named it, and a module
// taken in at run level has to start: it has no stance that takes it back out,
// so a database it cannot be pointed at is a startup failure rather than an
// assembly that goes on without one. A host that wants no database leaves this
// implementation out of the command tree — the capability is delivered
// exclusively, so the dependency closure does not pull a provider in on its own
// (ADR-dependency-resolution-2026-09-13, ADR-module-enablement-2026-09-21).
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
	// A configured timeout is held to the rule check holds the Spec's default
	// to, for the same reason: a wait bounded by zero or less is a single
	// attempt, so on a first start every replica but the one holding the
	// mutex would give up at once — the collision the mutex exists to turn
	// into a queue.
	if s.NewMigrationLock != nil && cfg.MigrationLockTimeout <= 0 {
		return core.Enablement{}, fmt.Errorf("%s: %s.%s is %s, and a replica given no time to wait "+
			"gives up on the migration mutex at its first attempt, so on a first start every replica "+
			"but one fails: set it to a positive duration", s.ModuleName, s.ConfigNamespace,
			MigrationLockTimeoutKey, cfg.MigrationLockTimeout)
	}
	return core.Enablement{State: core.StateEnabled}, nil
}

// ownProduct returns the instance as the product of this module's own New, or
// refuses it with ErrForeignInstance.
//
// What is judged is where the instance came from, not what its handle carries.
// New is the one place that installs plugins on a handle and registers the
// encryption serializer, so an instance it built is the only kind this module
// vouches for. An implementation of Database the host wrote, a test's stand-in
// and the product of another implementation's New are all refused, and the name
// a product carries is what says whose New built it. A typed nil pointer is not
// something New returns, so it is refused with the rest rather than dereferenced.
//
// The plugins on the handle are not compared with what this run declares: an
// instance built in another registry carries what that registry declared, and
// answering for that registry is the host's, who built the instance.
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

// migrate applies the declared migration sets to the handle of a product this
// module's New built, under this implementation's mutex when it has one.
//
// The timeout is read here, from the reader of the run being migrated, rather
// than taken from the section the product was built from: a product the host
// built in another registry carries that registry's section, and the bound on
// waiting for the mutex belongs to this run.
func (s Spec) migrate(ctx context.Context, reg *core.Registry, reader config.Reader, product *database) error {
	cfg, err := s.read(reader)
	if err != nil {
		return err
	}
	return applyMigrations(ctx, reg, product.handle, s.Dialect, s.lock(cfg))
}

// lock builds the mutex the migration run is held under, or reports none.
func (s Spec) lock(cfg lockingConfig) MigrationLock {
	if s.NewMigrationLock == nil {
		return nil
	}
	return s.NewMigrationLock(cfg.MigrationLockTimeout)
}

// closeInstance releases the connection pool behind an instance of the
// capability, whoever built it.
//
// A rollback hands it more than this module's own product. A startup that
// failed part-way closes every module whatever stage it reached, so this runs
// on a module whose New never returned a product and on one that returned a nil
// product of its own type. It also runs on an instance the host supplied that
// this module's New did not build: Migrate refuses such an instance and the
// rollback that follows reaches Close with it, as does a drive that ended
// before Migrate. The pool behind the handle it reports through Database is
// released, since the drive has begun and nothing else will release it. An
// instance that reports no handle, or a handle with no pool to reach, is left
// alone, and so is one whose DB, or whose handle when asked for its pool,
// panics: none of them gives this module a pool to release, and a pool the
// host's own code holds behind them is the host's. Reporting an error on any of
// them would bury the failure that started the rollback under one about the
// cleanup — and an unguarded type assertion or method call would panic there
// and lose it outright.
func closeInstance(instance any) error {
	handle, ok := deliveredHandle(instance)
	if !ok {
		return nil
	}
	return Close(handle)
}

// deliveredHandle takes the handle out of an instance of the capability, and
// reports whether there is a pool behind it to release.
//
// The nil test is by reflection because a typed nil pointer in an interface is
// not the nil interface: an instance that came back as a nil pointer of its own
// type satisfies the assertion and panics on the method call.
//
// An instance this module's New did not build runs the host's code in DB, and
// that code may panic: a stand-in that embeds the interface and overrides only
// some of its methods has a nil interface behind the rest, DB among them. The
// handle it reports is the host's too, built however the host built it: one
// written as a literal has no configuration to ask for a pool, and one opened on
// a driver that reaches no database has a configuration and no pool. So the
// handle is asked for its pool here, under the same guard as DB, and only a
// handle that answers is handed to Close. A DB or a handle that panics, and a
// handle with no pool, are an instance with nothing to release, and the panic
// goes no further: the rollback this runs in is already carrying the error the
// host has to read, and a panic out of the cleanup would take that error with
// it.
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
