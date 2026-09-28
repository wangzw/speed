package catalog

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/vislake/speed/pkg/core"
	"github.com/vislake/speed/pkg/db"
	"github.com/vislake/speed/pkg/log"
	"github.com/vislake/speed/pkg/tenant"
	"github.com/vislake/speed/pkg/tenant/internal/tenantctx"
)

// roster is this module's product. It implements tenant.Catalog.
type roster struct {
	handle  *gorm.DB
	refresh time.Duration

	// snap is the roster as of the last read, published whole. A reader
	// takes the pointer once and works from a consistent roster for as long
	// as it holds it, which is what makes two resolutions inside one
	// request agree with each other.
	//
	// It is an atomic pointer rather than a plain one because the refresher
	// publishes from its own goroutine while request goroutines read. A
	// plain pointer assignment is not a synchronisation point: the race
	// detector would report it, and a reader could see a pointer to a
	// snapshot whose contents it never observed being written.
	snap atomic.Pointer[snapshot]

	// logger is where the startup account goes, and where a failed refresh
	// is reported. It starts at the process root so that a build with no
	// logging module still says something, and is replaced by the named
	// logger in Init when there is one.
	logger *slog.Logger

	// stopCh ends the refresher. It is written in Start and read in Stop,
	// which the lifecycle runs in that order on one goroutine; once keeps a
	// second Stop from closing a channel twice.
	stopCh chan struct{}
	once   sync.Once
}

// newRoster builds the product. It reads nothing: the table it reads does not
// exist until Migrate has run, which is after every product is constructed.
func newRoster(set settings, database db.Database) *roster {
	return &roster{
		handle:  database.DB(),
		refresh: set.refresh,
		logger:  log.Default(),
	}
}

// takeLogger takes up the logging capability for the startup account. It is not
// a declared dependency: resolving it here costs nothing, and a module that
// declares one it does not need is a module whose absence changes behaviour it
// has no business changing.
func (r *roster) takeLogger(reg *core.Registry) {
	delivered, err := core.Resolve[log.Logger](reg)
	if err != nil {
		return
	}
	r.logger = delivered.Named(ModuleName)
}

// load reads the table for the first time. A failure here ends the startup.
//
// The alternative — carrying on with an empty roster — would refuse every
// request on every guarded endpoint while telling the operator nothing about
// why, which is the shape this capability directory refuses everywhere else it
func (r *roster) load(ctx context.Context) error {
	if err := r.read(ctx); err != nil {
		return err
	}
	active, disabled := r.snap.Load().counts()
	r.logger.Info(diagnosticPrefix+ModuleName+": roster loaded",
		"tenants", active+disabled, "active", active, "disabled", disabled,
		"refresh", r.refresh.String())
	return nil
}

// start begins the refresher. A refresh period of zero is a roster loaded once
// and never reread, which is a legitimate choice for a deployment whose tenants
// do not change while it runs.
//
// The cancellation function stays inside the goroutine that owns it, so its
// lifetime is one the reader can see: it is released when that goroutine
// returns, and the goroutine returns when the stop channel closes.
func (r *roster) start() {
	if r.refresh <= 0 {
		return
	}
	stop := make(chan struct{})
	r.stopCh = stop
	go func() {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		ticker := time.NewTicker(r.refresh)
		defer ticker.Stop()
		for {
			select {
			case <-stop:
				return
			case <-ticker.C:
				r.refreshOnce(ctx)
			}
		}
	}()
}

// stop ends the refresher. It does not wait for an in-flight read: a read is
// idempotent and produces nothing that has to be released, so a process on its
// way out does not stop here.
func (r *roster) stop() {
	r.once.Do(func() {
		if r.stopCh != nil {
			close(r.stopCh)
		}
	})
}

// refreshOnce rereads the table, keeping the previous snapshot if the read
// fails. Dropping it would refuse every request, and one failed read is not a
// reason to do that.
func (r *roster) refreshOnce(ctx context.Context) {
	if err := r.read(ctx); err != nil {
		r.logger.Warn(diagnosticPrefix+ModuleName+
			": the roster could not be reread; the previous one stays in force",
			"error", err)
	}
}

// read replaces the snapshot with the table's current contents.
func (r *roster) read(ctx context.Context) error {
	var rows []row
	if err := r.handle.WithContext(ctx).Model(&row{}).Find(&rows).Error; err != nil {
		return fmt.Errorf(
			"%w: reading table %s failed, and a table that was never created reads "+
				"the same way: %w. This module's migration set creates it, and a command "+
				"whose work is applying migrations collects that set only when the host "+
				"reaches this module for its declarations",
			tenant.ErrRosterMissing, tableName, err)
	}
	if err := checkRows(rows); err != nil {
		return err
	}
	r.snap.Store(buildSnapshot(rows))
	return nil
}

// checkRows refuses a read holding a tenant this module will not act on.
//
// The rows are put through the same predicate a write passes, so a row that
// arrived by some other route — a statement outside this module, a row from a
// database this module shares — is refused where it is read, and the
// identifier it carries reaches no caller.
//
// It fails the read rather than dropping the row in passing. A snapshot that
// quietly left a tenant out would answer every request for it as one that does
// not exist, with nothing said; the identifier's domain is checked here
// because a caller downstream cannot check it, and a silent omission is the
// shape that makes the check invisible.
//
// The failure takes the read's own semantics: the load at Init ends the
// startup, and a later refresh keeps the previous snapshot and reports it.
//
// What is named is the table and the field. Never the value: keeping the value
// out of log lines and error text is what the domain is for.
func checkRows(rows []row) error {
	for _, r := range rows {
		t := tenant.Tenant{ID: tenant.ID(r.ID), Alias: r.Alias, Status: tenant.Status(r.Status)}
		if err := checkTenant(t); err != nil {
			return fmt.Errorf("%w: table %s holds a row this module will not act on: %w",
				tenant.ErrInvalidTenant, tableName, err)
		}
	}
	return nil
}

// current returns the published snapshot, or says that none was ever published.
// Once Init has returned there always is one; the check is here so that a path
// reaching this product before then reports itself rather than reading a nil.
func (r *roster) current() (*snapshot, error) {
	s := r.snap.Load()
	if s == nil {
		return nil, fmt.Errorf("%w: the roster has not been read", tenant.ErrRosterMissing)
	}
	return s, nil
}

// Get implements tenant.Catalog.
func (r *roster) Get(_ context.Context, id tenant.ID) (tenant.Tenant, bool, error) {
	s, err := r.current()
	if err != nil {
		return tenant.Tenant{}, false, err
	}
	t, ok := s.byID[id]
	return t, ok, nil
}

// ByAlias implements tenant.Catalog. It is the lookup the resolving layer makes
// on every request, and it touches no table.
func (r *roster) ByAlias(_ context.Context, alias string) (tenant.Tenant, bool, error) {
	s, err := r.current()
	if err != nil {
		return tenant.Tenant{}, false, err
	}
	t, ok := s.byAlias[alias]
	return t, ok, nil
}

// List implements tenant.Catalog. The slice is a copy, so a caller sorting or
// trimming it cannot disturb what the next caller sees.
func (r *roster) List(_ context.Context) ([]tenant.Tenant, error) {
	s, err := r.current()
	if err != nil {
		return nil, err
	}
	out := make([]tenant.Tenant, len(s.ordered))
	copy(out, s.ordered)
	return out, nil
}

// Enter implements tenant.Catalog.
//
// It reads the snapshot the resolving layer reads, so a path entering a tenant
// gets the answer a request naming it would: a tenant disabled on this replica
// is refused at once, and one disabled on another replica once this replica's
// snapshot is next reread. The identifier's shape is judged first, because a
// value outside the domain is no tenant's and the message says which of the two
// fixes it needs.
//
// What it writes is what the resolving layer writes on admitting a request —
// the identifier into the context and onto the logger, replacing a tenant the
// context carried in both — through the same internal package, which is what
// keeps the writing side closed to everything outside this capability
// directory. A refusal writes the empty identifier instead, which lifts a
// cross-tenant mark the context carried and takes a tenant bound on its logger
// off: a caller that drops the error and goes on with the context has its
// scoped statements refused, not let across, and its records name no tenant.
func (r *roster) Enter(ctx context.Context, id tenant.ID) (context.Context, error) {
	refused := tenantctx.Bind(ctx, "", contextLogger)
	if err := checkField("identifier", string(id)); err != nil {
		return refused, err
	}
	s, err := r.current()
	if err != nil {
		return refused, err
	}
	t, ok := s.byID[id]
	switch {
	case !ok:
		return refused, fmt.Errorf("%w: no tenant has identifier %q", tenant.ErrInvalidTenant, id)
	case t.Status != tenant.StatusActive:
		return refused, fmt.Errorf("%w: tenant %q is %s", tenant.ErrTenantDisabled, id, t.Status)
	}
	return tenantctx.Bind(ctx, string(t.ID), contextLogger), nil
}

// contextLogger is how an entered tenant reaches the logger a context carries.
var contextLogger = tenantctx.Logger{With: log.WithAttrs, Without: log.WithoutAttrs}

// Put implements tenant.Catalog.
//
// The rewrite is by identifier, so registering a tenant twice is one row. The
// alias is checked inside the same transaction as the write: the column's
// unique index would catch a collision too, but it would report it as a
// constraint violation rather than as which tenant holds the name.
//
// The check does not close the race on its own. Two writes putting one alias
// on two tenants at once can both find it free, and the later insert then
// meets the unique index. That is the same conflict, and it is answered the
// same way: a write that failed is read again against the table, and an alias
// another tenant now holds is reported as ErrAliasConflict naming it, with the
// driver's error kept in the chain.
func (r *roster) Put(ctx context.Context, t tenant.Tenant) error {
	if err := checkTenant(t); err != nil {
		return err
	}
	err := r.handle.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		held, err := lookUp(tx, "alias", t.Alias)
		if err != nil {
			return err
		}
		if len(held) > 0 && held[0].ID != string(t.ID) {
			return fmt.Errorf("%w: alias %q is held by tenant %q",
				tenant.ErrAliasConflict, t.Alias, held[0].ID)
		}
		return tx.Clauses(clause.OnConflict{
			Columns:   []clause.Column{{Name: "id"}},
			DoUpdates: clause.AssignmentColumns([]string{"alias", "status"}),
		}).Create(&row{
			ID:     string(t.ID),
			Alias:  t.Alias,
			Status: int(t.Status),
		}).Error
	})
	if err != nil {
		return r.failedPut(ctx, t, err)
	}
	return r.afterWrite(ctx)
}

// failedPut names the conflict a failed Put ran into when the alias turns out
// to be held by another tenant, and returns the failure as it stands otherwise.
// The read runs outside the failed transaction, which a PostgreSQL transaction
// that met a constraint no longer accepts statements in.
func (r *roster) failedPut(ctx context.Context, t tenant.Tenant, err error) error {
	if errors.Is(err, tenant.ErrAliasConflict) {
		return err
	}
	held, lookErr := lookUp(r.handle.WithContext(ctx), "alias", t.Alias)
	if lookErr != nil || len(held) == 0 || held[0].ID == string(t.ID) {
		return err
	}
	return fmt.Errorf("%w: alias %q is held by tenant %q, which took it while this write was "+
		"in flight: %w", tenant.ErrAliasConflict, t.Alias, held[0].ID, err)
}

// Create implements tenant.Catalog.
//
// Whether the identifier is taken is asked of the table inside the write, not
// of the snapshot: a replica whose snapshot predates another replica's
// registration would otherwise take the identifier for free, and the write
// would land on that tenant's row. The insert itself does nothing when the
// identifier is already there, and a write that inserted nothing is the
// refusal — so two creations of one identifier at once end with one row and
// one ErrTenantExists, the later one having waited on the earlier's row rather
// than overwriting it. The alias is checked and its race answered as Put
// answers it.
func (r *roster) Create(ctx context.Context, t tenant.Tenant) error {
	if err := checkTenant(t); err != nil {
		return err
	}
	err := r.handle.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		held, err := lookUp(tx, "id", string(t.ID))
		if err != nil {
			return err
		}
		if len(held) > 0 {
			return tenantExists(t.ID)
		}
		held, err = lookUp(tx, "alias", t.Alias)
		if err != nil {
			return err
		}
		if len(held) > 0 {
			return fmt.Errorf("%w: alias %q is held by tenant %q",
				tenant.ErrAliasConflict, t.Alias, held[0].ID)
		}
		written := tx.Clauses(clause.OnConflict{
			Columns:   []clause.Column{{Name: "id"}},
			DoNothing: true,
		}).Create(&row{
			ID:     string(t.ID),
			Alias:  t.Alias,
			Status: int(t.Status),
		})
		if written.Error != nil {
			return written.Error
		}
		if written.RowsAffected == 0 {
			return tenantExists(t.ID)
		}
		return nil
	})
	if err != nil {
		return r.failedCreate(ctx, t, err)
	}
	return r.afterWrite(ctx)
}

// tenantExists is the refusal of a creation whose identifier is taken.
func tenantExists(id tenant.ID) error {
	return fmt.Errorf("%w: identifier %q", tenant.ErrTenantExists, id)
}

// failedCreate names what a failed Create ran into: its own refusals as they
// stand, and a driver failure as the conflict it turns out to be once the table
// is read again — the identifier registered, or the alias taken, by a write
// that got there first.
func (r *roster) failedCreate(ctx context.Context, t tenant.Tenant, err error) error {
	if errors.Is(err, tenant.ErrTenantExists) || errors.Is(err, tenant.ErrAliasConflict) {
		return err
	}
	held, lookErr := lookUp(r.handle.WithContext(ctx), "id", string(t.ID))
	if lookErr == nil && len(held) > 0 {
		return fmt.Errorf("%w, by a write that got there while this one was in flight: %w",
			tenantExists(t.ID), err)
	}
	return r.failedPut(ctx, t, err)
}

// SetStatus implements tenant.Catalog, and is the only way to disable a tenant.
//
// Existence is checked with its own read rather than inferred from the update's
// affected-row count: a driver is free to report zero rows for an update that
// set a value to what it already was, and this call would then report a tenant
// that exists as one that does not.
func (r *roster) SetStatus(ctx context.Context, id tenant.ID, s tenant.Status) error {
	if err := checkField("identifier", string(id)); err != nil {
		return err
	}
	err := r.handle.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		held, err := lookUp(tx, "id", string(id))
		if err != nil {
			return err
		}
		if len(held) == 0 {
			return fmt.Errorf("%w: no tenant has identifier %q", tenant.ErrInvalidTenant, id)
		}
		return tx.Model(&row{}).Where("id = ?", string(id)).
			Update("status", int(s)).Error
	})
	if err != nil {
		return err
	}
	return r.afterWrite(ctx)
}

// lookUp reads the row, if any, whose unique column holds this value.
//
// Finding none is an answer, and it is read as one: an empty result rather than
// the not-found failure GORM's First reports. That failure reaches GORM's
// logger as a failed statement, which its default logger prints to standard
// output as an error, outside the logging module — on every tenant a new alias
// is put for, and on every status change naming a tenant that is not there.
func lookUp(tx *gorm.DB, column, value string) ([]row, error) {
	var held []row
	if err := tx.Where(clause.Eq{Column: clause.Column{Name: column}, Value: value}).
		Limit(1).Find(&held).Error; err != nil {
		return nil, err
	}
	return held, nil
}

// afterWrite rereads the table so that the writer sees its own write. Writes are
// an operator's action rather than a request-path one, so paying a read for one
// is cheaper than making the caller wait out a refresh period.
//
// A failed reread is reported rather than swallowed: the write is durable, but
// this process is now serving a roster that does not contain it, and the caller
// is the only one in a position to know that.
func (r *roster) afterWrite(ctx context.Context) error {
	if err := r.read(ctx); err != nil {
		return fmt.Errorf(
			"%s%s: the write was applied, but the roster could not be reread, so this "+
				"process is serving the roster as it was before it: %w",
			diagnosticPrefix, ModuleName, err)
	}
	return nil
}

// checkTenant refuses a tenant this module will not act on.
//
// The shape it judges is the one this module can judge: not empty, and free of
// blanks and control characters. How long an identifier may be is the declaring
// party's to say, in the column its migration declares — this module defines no
// length and so cannot check one.
func checkTenant(t tenant.Tenant) error {
	if err := checkField("identifier", string(t.ID)); err != nil {
		return err
	}
	return checkField("alias", t.Alias)
}

// checkField refuses a field that is empty or carries a character an alias
// could not survive.
//
// Blanks and control characters are refused in both fields and anywhere in
// them. An identifier ends up in log lines, diagnostics and error text, where a
// newline splits one record into two; an alias arrives from a subdomain label,
// a path segment or a header, and none of those can carry a blank.
//
// The domain itself is tenant.IsIdentifier, so this module states the rule
// once. The empty case is judged first anyway, because the two failures have
// different fixes and the message says which one the field carried.
func checkField(what, value string) error {
	if value == "" {
		return fmt.Errorf("%w: the %s is empty", tenant.ErrInvalidTenant, what)
	}
	if !tenant.IsIdentifier(value) {
		return fmt.Errorf("%w: the %s carries a blank or a control character",
			tenant.ErrInvalidTenant, what)
	}
	return nil
}
