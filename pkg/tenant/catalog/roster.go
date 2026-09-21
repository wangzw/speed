package catalog

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/vislake/speed/pkg/core"
	"github.com/vislake/speed/pkg/db"
	"github.com/vislake/speed/pkg/log"
	"github.com/vislake/speed/pkg/tenant"
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
// why, which is the shape this release unit refuses everywhere else it can.
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
	r.snap.Store(buildSnapshot(rows))
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

// Put implements tenant.Catalog.
//
// The rewrite is by identifier, so registering a tenant twice is one row. The
// alias is checked inside the same transaction as the write: the column's
// unique index would catch a collision too, but it would report it as a
// constraint violation rather than as which tenant holds the name.
func (r *roster) Put(ctx context.Context, t tenant.Tenant) error {
	if err := checkTenant(t); err != nil {
		return err
	}
	err := r.handle.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var held row
		err := tx.Where("alias = ?", t.Alias).First(&held).Error
		switch {
		case err == nil && held.ID != string(t.ID):
			return fmt.Errorf("%w: alias %q is held by tenant %q",
				tenant.ErrAliasConflict, t.Alias, held.ID)
		case err != nil && !errors.Is(err, gorm.ErrRecordNotFound):
			return err
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
		return err
	}
	return r.afterWrite(ctx)
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
		var held row
		err := tx.Where("id = ?", string(id)).First(&held).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return fmt.Errorf("%w: no tenant has identifier %q", tenant.ErrInvalidTenant, id)
		}
		if err != nil {
			return err
		}
		return tx.Model(&row{}).Where("id = ?", string(id)).
			Update("status", int(s)).Error
	})
	if err != nil {
		return err
	}
	return r.afterWrite(ctx)
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
func checkField(what, value string) error {
	if value == "" {
		return fmt.Errorf("%w: the %s is empty", tenant.ErrInvalidTenant, what)
	}
	if strings.IndexFunc(value, func(r rune) bool {
		return unicode.IsSpace(r) || unicode.IsControl(r)
	}) >= 0 {
		return fmt.Errorf("%w: the %s carries a blank or a control character",
			tenant.ErrInvalidTenant, what)
	}
	return nil
}
