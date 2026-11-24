package catalog

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/core"
	"github.com/vislake/speed/pkg/db"
	"github.com/vislake/speed/pkg/log"
	"github.com/vislake/speed/pkg/tenant"
	"github.com/vislake/speed/pkg/tenant/internal/tenantctx"
)

type roster struct {
	handle  *gorm.DB
	refresh time.Duration

	snap atomic.Pointer[snapshot]

	connectWithin time.Duration

	// reading is a channel of one slot rather than a mutex so that a read waiting for it gives up when its context ends.
	reading chan struct{}

	logger *slog.Logger

	// stopCh is written by start and read by stop without a lock: the lifecycle runs them in that order on one goroutine.
	stopCh chan struct{}
	once   sync.Once
}

// newRoster reads nothing: the table does not exist until Migrate has run, after every product is constructed.
func newRoster(set settings, database db.Database) *roster {
	return &roster{
		handle:        database.DB(),
		refresh:       set.refresh,
		reading:       make(chan struct{}, 1),
		logger:        log.Default(),
		connectWithin: connectionWait,
	}
}

// takeLogger resolves the logging capability without declaring it as a dependency, so its absence changes nothing.
func (r *roster) takeLogger(reg *core.Registry) {
	delivered, err := core.Resolve[log.Logger](reg)
	if err != nil {
		return
	}
	r.logger = delivered.Named(ModuleName)
}

func (r *roster) load(ctx context.Context) error {
	if err := r.read(ctx); err != nil {
		return err
	}
	active, disabled, deleted := r.snap.Load().counts()
	r.logger.Info(diagnosticPrefix+ModuleName+": roster loaded",
		"tenants", active+disabled+deleted, "active", active, "disabled", disabled, "deleted", deleted,
		"refresh", r.refresh.String())
	return nil
}

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

func (r *roster) stop() {
	r.once.Do(func() {
		if r.stopCh != nil {
			close(r.stopCh)
		}
	})
}

func (r *roster) refreshOnce(ctx context.Context) {
	if err := r.read(ctx); err != nil {
		r.reportFailedReread(err)
	}
}

func (r *roster) reportFailedReread(err error) {
	r.logger.Warn(diagnosticPrefix+ModuleName+
		": the roster could not be reread; the previous one stays in force",
		"error", err)
}

// read runs one at a time: two reads in flight could publish out of order and put back a roster without a write that committed in between.
func (r *roster) read(ctx context.Context) error {
	if err := r.acquire(ctx); err != nil {
		return fmt.Errorf("%s%s: reading table %s was given up while another read of it was in flight: %w",
			diagnosticPrefix, ModuleName, tableName, err)
	}
	defer r.release()
	return r.readHeld(ctx)
}

func (r *roster) acquire(ctx context.Context) error {
	select {
	case r.reading <- struct{}{}:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (r *roster) release() { <-r.reading }

func (r *roster) readHeld(ctx context.Context) error {
	var rows []row
	ran, err := r.onConnection(ctx, func(session *gorm.DB) error {
		if err := session.Model(&row{}).Find(&rows).Error; err != nil {
			return failedStatement(ctx, session, err)
		}
		return nil
	})
	if err != nil {
		if !ran {
			return fmt.Errorf("%s%s: table %s was not read: %w", diagnosticPrefix, ModuleName, tableName, err)
		}
		return err
	}
	if err := checkRows(rows); err != nil {
		return err
	}
	r.snap.Store(buildSnapshot(rows))
	return nil
}

func (r *roster) onConnection(ctx context.Context, fn func(session *gorm.DB) error) (bool, error) {
	ran := false
	err := db.Connection(ctx, r.handle, r.connectWithin, func(session *gorm.DB) error {
		ran = true
		return fn(session)
	})
	if err != nil && !ran && errors.Is(err, context.DeadlineExceeded) && ctx.Err() == nil {
		return false, fmt.Errorf("%w: %w", tenant.ErrNoConnection, err)
	}
	return ran, err
}

func failedStatement(ctx context.Context, session *gorm.DB, err error) error {
	if ended := ctx.Err(); ended != nil {
		return fmt.Errorf("a statement on table %s ended with its context (%w): %w", tableName, ended, err)
	}
	if tableAbsent(session) {
		return fmt.Errorf(
			"%w: a statement on table %s failed, and the database has no table by that name: %w. "+
				"This module's migration set creates it, and a command whose work is applying "+
				"migrations collects that set only when the host reaches this module for its declarations",
			tenant.ErrRosterMissing, tableName, err)
	}
	return fmt.Errorf("a statement on table %s failed: %w", tableName, err)
}

func tableAbsent(session *gorm.DB) bool {
	if session.Name() != postgresDialector {
		tables, err := session.Migrator().GetTables()
		return err == nil && !slices.Contains(tables, tableName)
	}
	named, err := namedSchemas(session)
	if err != nil {
		return false
	}
	var absent bool
	err = session.Raw(`SELECT NOT EXISTS (SELECT 1 FROM pg_catalog.pg_class c
		JOIN pg_catalog.pg_namespace n ON n.oid = c.relnamespace
		WHERE c.relname = ? AND (n.nspname IN ? OR n.nspname = ANY (pg_catalog.current_schemas(true))))`,
		tableName, named).Scan(&absent).Error
	return err == nil && absent
}

func namedSchemas(session *gorm.DB) ([]string, error) {
	var setting struct {
		Path string
		User string
	}
	if err := session.Raw(`SELECT pg_catalog.current_setting('search_path') AS path, current_user AS "user"`).
		Scan(&setting).Error; err != nil {
		return nil, err
	}
	var schemas []string
	for _, name := range searchPath(setting.Path) {
		if name == "$user" {
			name = setting.User
		}
		if !slices.Contains(schemas, name) {
			schemas = append(schemas, name)
		}
	}
	return schemas, nil
}

// blanks are the characters PostgreSQL skips around an item of an identifier list.
const blanks = " \t\n\r\f\v"

func searchPath(setting string) []string {
	var names []string
	rest := setting
	for {
		rest = strings.TrimLeft(rest, blanks)
		var name string
		if strings.HasPrefix(rest, `"`) {
			name, rest = quotedName(rest)
		} else {
			end := strings.IndexByte(rest, ',')
			if end < 0 {
				end = len(rest)
			}
			name, rest = asciiLower(strings.TrimRight(rest[:end], blanks)), rest[end:]
		}
		if name != "" {
			names = append(names, name)
		}
		rest = strings.TrimLeft(rest, blanks)
		if !strings.HasPrefix(rest, ",") {
			return names
		}
		rest = rest[1:]
	}
}

func quotedName(s string) (name, rest string) {
	var b strings.Builder
	for i := 1; i < len(s); i++ {
		if s[i] != '"' {
			b.WriteByte(s[i])
			continue
		}
		if i+1 < len(s) && s[i+1] == '"' {
			b.WriteByte('"')
			i++
			continue
		}
		return b.String(), s[i+1:]
	}
	return b.String(), ""
}

// asciiLower folds ASCII letters only, as PostgreSQL folds an unquoted identifier in a multibyte encoding.
func asciiLower(s string) string {
	return strings.Map(func(r rune) rune {
		if 'A' <= r && r <= 'Z' {
			return r + ('a' - 'A')
		}
		return r
	}, s)
}

const postgresDialector = "postgres"

// connectionWait bounds the wait for a connection only; a lock another connection holds is waited for as the engine waits for it.
const connectionWait = 5 * time.Second

// checkRows fails the read rather than dropping a row, and names the table and field, never the value.
func checkRows(rows []row) error {
	for _, r := range rows {
		if err := checkTenant(tenantOf(r)); err != nil {
			return fmt.Errorf("%w: table %s holds a row this module will not act on: %w",
				tenant.ErrInvalidTenant, tableName, err)
		}
	}
	return nil
}

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

// ByName implements tenant.Catalog; it reads the snapshot and touches no table.
func (r *roster) ByName(_ context.Context, name string) (tenant.Tenant, bool, error) {
	s, err := r.current()
	if err != nil {
		return tenant.Tenant{}, false, err
	}
	t, ok := s.byName[name]
	return t, ok, nil
}

// NameOf implements tenant.NameResolver; an identifier no row carries answers empty.
func (r *roster) NameOf(_ context.Context, id tenant.ID) (string, error) {
	s, err := r.current()
	if err != nil {
		return "", err
	}
	if t, ok := s.byID[id]; ok {
		return t.Name, nil
	}
	return "", nil
}

// TenantNameOf implements authn.TenantNameResolver.
func (r *roster) TenantNameOf(ctx context.Context, id string) (string, error) {
	return r.NameOf(ctx, tenant.ID(id))
}

// TenantIDOf implements authn.TenantDirectory; a name no tenant carries answers false with no error.
func (r *roster) TenantIDOf(ctx context.Context, name string) (string, bool, error) {
	t, ok, err := r.ByName(ctx, name)
	if err != nil || !ok {
		return "", false, err
	}
	return string(t.ID), true, nil
}

// TenantStateOf implements authn.TenantStateResolver; an identifier no row carries is reported deleted.
func (r *roster) TenantStateOf(_ context.Context, id string) (authn.TenantState, error) {
	s, err := r.current()
	if err != nil {
		return authn.TenantDeleted, err
	}
	t, ok := s.byID[tenant.ID(id)]
	if !ok {
		return authn.TenantDeleted, nil
	}
	switch t.Status {
	case tenant.StatusActive:
		return authn.TenantServing, nil
	case tenant.StatusDisabled:
		return authn.TenantDisabled, nil
	default:
		return authn.TenantDeleted, nil
	}
}

// TenantDescriptionOf implements authn.TenantDescriptionResolver; an identifier no row carries answers the zero description.
func (r *roster) TenantDescriptionOf(_ context.Context, id string) (authn.TenantDescription, error) {
	s, err := r.current()
	if err != nil {
		return authn.TenantDescription{}, err
	}
	t, ok := s.byID[tenant.ID(id)]
	if !ok {
		return authn.TenantDescription{}, nil
	}
	return authn.TenantDescription{
		Name:        t.Name,
		DisplayName: t.Profile.DisplayName,
		System:      t.ID == systemTenant,
	}, nil
}

// List implements tenant.Catalog; the slice is a copy.
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
func (r *roster) Enter(ctx context.Context, id tenant.ID) (context.Context, error) {
	return r.enter(ctx, id, false)
}

// EnterRegistered implements tenant.Catalog.
func (r *roster) EnterRegistered(ctx context.Context, id tenant.ID) (context.Context, error) {
	return r.enter(ctx, id, true)
}

// enter returns a context bound to the empty tenant on refusal, which lifts a cross-tenant mark, so a caller that drops the error is refused rather than let across.
func (r *roster) enter(ctx context.Context, id tenant.ID, anyStatus bool) (context.Context, error) {
	refused := tenantctx.Bind(ctx, "", contextLogger)
	if err := checkField("identifier", string(id)); err != nil {
		return refused, err
	}
	s, err := r.current()
	if err != nil {
		return refused, err
	}
	admits := func(t tenant.Tenant) bool { return anyStatus || t.Status == tenant.StatusActive }
	t, ok := s.byID[id]
	if !ok || !admits(t) {
		if t, ok, err = r.fromTable(ctx, id); err != nil {
			return refused, err
		}
		r.catchUp(ctx, id, t, ok)
	}
	switch {
	case !ok:
		return refused, fmt.Errorf("%w: no tenant has identifier %q", tenant.ErrInvalidTenant, id)
	case !anyStatus && t.Status == tenant.StatusDeleted:
		return refused, fmt.Errorf("%w: tenant %q is %s", tenant.ErrTenantDeleted, id, t.Status)
	case !admits(t):
		return refused, fmt.Errorf("%w: tenant %q is %s", tenant.ErrTenantDisabled, id, t.Status)
	}
	return tenantctx.Bind(ctx, string(t.ID), contextLogger), nil
}

func (r *roster) fromTable(ctx context.Context, id tenant.ID) (tenant.Tenant, bool, error) {
	var held []row
	_, err := r.onConnection(ctx, func(session *gorm.DB) error {
		var err error
		if held, err = lookUp(session, "id", string(id)); err != nil {
			return failedStatement(ctx, session, err)
		}
		return nil
	})
	if err != nil {
		return tenant.Tenant{}, false, fmt.Errorf(
			"%s%s: this replica's snapshot does not admit tenant %q, and table %s could not be read "+
				"for it, so there is no answer: %w", diagnosticPrefix, ModuleName, id, tableName, err)
	}
	if len(held) == 0 {
		return tenant.Tenant{}, false, nil
	}
	if err := checkRows(held); err != nil {
		return tenant.Tenant{}, false, err
	}
	return tenantOf(held[0]), true, nil
}

func (r *roster) catchUp(ctx context.Context, id tenant.ID, t tenant.Tenant, held bool) {
	if agrees(r.snap.Load(), id, t, held) {
		return
	}
	if err := r.acquire(ctx); err != nil {
		return
	}
	defer r.release()
	if agrees(r.snap.Load(), id, t, held) {
		return
	}
	if err := r.readHeld(ctx); err != nil {
		r.reportFailedReread(err)
	}
}

func agrees(s *snapshot, id tenant.ID, t tenant.Tenant, held bool) bool {
	current, ok := s.byID[id]
	return ok == held && sameTenant(current, t)
}

// sameTenant compares instants, not the pointers that hold them: two reads of one row build two values.
func sameTenant(a, b tenant.Tenant) bool {
	if a.ID != b.ID || a.Name != b.Name || a.Status != b.Status ||
		a.DisabledBy != b.DisabledBy || a.OwnerAccount != b.OwnerAccount ||
		a.Profile != b.Profile || !a.CreatedAt.Equal(b.CreatedAt) {
		return false
	}
	if a.DeletedAt == nil || b.DeletedAt == nil {
		return a.DeletedAt == nil && b.DeletedAt == nil
	}
	return a.DeletedAt.Equal(*b.DeletedAt)
}

var contextLogger = tenantctx.Logger{With: log.WithAttrs, Without: log.WithoutAttrs}

// Put implements tenant.Catalog. It writes by identifier: an existing row keeps its name, owner, profile and creation time and takes the status and disable source.
// A changed name is refused with ErrInvalidTenant, a name another tenant holds with ErrNameConflict, and a tombstone with ErrTenantDeleted.
func (r *roster) Put(ctx context.Context, t tenant.Tenant) error {
	if err := checkWrite(t); err != nil {
		return err
	}
	return r.write(ctx, func(tx *gorm.DB) error {
		held, err := lookUp(tx, "id", string(t.ID))
		if err != nil {
			return err
		}
		switch {
		case len(held) > 0 && tenant.Status(held[0].Status) == tenant.StatusDeleted:
			return tombstone(t.ID)
		case len(held) > 0 && held[0].Name != t.Name:
			return fmt.Errorf("%w: tenant %q already carries the name %q, and a name "+
				"is chosen at creation and never changed", tenant.ErrInvalidTenant, t.ID, held[0].Name)
		case len(held) == 0:
			if err := checkNamespace(tx, t); err != nil {
				return err
			}
		}
		written := tx.Clauses(clause.OnConflict{
			Columns:   []clause.Column{{Name: "id"}},
			DoUpdates: clause.AssignmentColumns([]string{"status", "disabled_by"}),
			// The statement itself refuses a tombstone, against a deletion committed after the check above.
			Where: clause.Where{Exprs: []clause.Expression{
				clause.Neq{Column: clause.Column{Table: tableName, Name: "status"}, Value: int(tenant.StatusDeleted)},
			}},
		}).Create(&row{
			ID:         string(t.ID),
			Name:       t.Name,
			Status:     int(t.Status),
			DisabledBy: int(t.DisabledBy),

			OwnerAccount: t.OwnerAccount,

			// creationTime, not gorm's auto-create, which would record local time rather than UTC.
			CreatedAt: creationTime(),
		})
		if written.Error != nil {
			return written.Error
		}
		if written.RowsAffected == 0 {
			return missingOrTombstone(tx, t.ID)
		}
		return nil
	}, func(session *gorm.DB, err error) error { return failedPut(session, t, err) })
}

func (r *roster) write(ctx context.Context, apply func(tx *gorm.DB) error,
	failed func(session *gorm.DB, err error) error,
) error {
	_, err := r.writeResult(ctx, func(tx *gorm.DB) (bool, error) {
		return true, apply(tx)
	}, failed)
	return err
}

// writeResult rereads the roster only after the connection is given back: on a database held to one connection it is the only one.
func (r *roster) writeResult(ctx context.Context, apply func(tx *gorm.DB) (bool, error),
	failed func(session *gorm.DB, err error) error,
) (bool, error) {
	wrote := false
	ran, err := r.onConnection(ctx, func(session *gorm.DB) error {
		err := session.Transaction(func(tx *gorm.DB) error {
			w, aerr := apply(tx)
			if aerr != nil {
				return aerr
			}
			wrote = w
			return nil
		})
		if err != nil && failed != nil {
			err = failed(session, err)
		}
		if err != nil && !answered(err) {
			err = failedStatement(ctx, session, err)
		}
		return err
	})
	if err != nil {
		if !ran {
			return false, fmt.Errorf("%s%s: nothing was written: %w", diagnosticPrefix, ModuleName, err)
		}
		return false, err
	}
	if !wrote {
		return false, nil
	}
	if err := r.afterWrite(ctx); err != nil {
		return true, err
	}
	return true, nil
}

func answered(err error) bool {
	return errors.Is(err, tenant.ErrNameConflict) || errors.Is(err, tenant.ErrTenantExists) ||
		errors.Is(err, tenant.ErrInvalidTenant) || errors.Is(err, tenant.ErrTenantDeleted)
}

func tombstone(id tenant.ID) error {
	return fmt.Errorf("%w: identifier %q", tenant.ErrTenantDeleted, id)
}

// A zero affected-row count does not mean the row is missing, since a driver reports it for an unchanged value, so this read decides.
func missingOrTombstone(tx *gorm.DB, id tenant.ID) error {
	held, err := lookUp(tx, "id", string(id))
	if err != nil {
		return err
	}
	switch {
	case len(held) == 0:
		return fmt.Errorf("%w: no tenant has identifier %q", tenant.ErrInvalidTenant, id)
	case tenant.Status(held[0].Status) == tenant.StatusDeleted:
		return tombstone(id)
	}
	return nil
}

func failedPut(session *gorm.DB, t tenant.Tenant, err error) error {
	return failedName(session, t.ID, t.Name, err)
}

// failedName reads outside the failed transaction, which PostgreSQL no longer accepts statements in.
func failedName(session *gorm.DB, id tenant.ID, name string, err error) error {
	if errors.Is(err, tenant.ErrNameConflict) {
		return err
	}
	held, lookErr := lookUp(session, "name", name)
	if lookErr != nil || len(held) == 0 || held[0].ID == string(id) {
		return err
	}
	return fmt.Errorf("%w: name %q is held by tenant %q, which took it while this write was "+
		"in flight: %w", tenant.ErrNameConflict, name, held[0].ID, err)
}

// Create implements tenant.Catalog. It only inserts: a taken identifier, a tombstone's included, is ErrTenantExists and a taken name is ErrNameConflict.
// The profile is written with the row, and the creation time is the roster's own, never the caller's.
func (r *roster) Create(ctx context.Context, t tenant.Tenant) error {
	if err := checkWrite(t); err != nil {
		return err
	}
	return r.write(ctx, func(tx *gorm.DB) error {
		_, err := r.createIn(ctx, tx, t)
		return err
	}, func(session *gorm.DB, err error) error { return failedCreate(session, t, err) })
}

func (r *roster) createIn(_ context.Context, tx *gorm.DB, t tenant.Tenant) (bool, error) {
	held, err := lookUp(tx, "id", string(t.ID))
	if err != nil {
		return false, err
	}
	if len(held) > 0 {
		return false, tenantExists(t.ID)
	}
	if err := checkNamespace(tx, t); err != nil {
		return false, err
	}
	written := tx.Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "id"}},
		DoNothing: true,
	}).Create(&row{
		ID:         string(t.ID),
		Name:       t.Name,
		Status:     int(t.Status),
		DisabledBy: int(t.DisabledBy),

		OwnerAccount: t.OwnerAccount,

		DisplayName:  t.Profile.DisplayName,
		Description:  t.Profile.Description,
		ContactEmail: t.Profile.ContactEmail,
		Website:      t.Profile.Website,
		CreatedAt:    creationTime(),
	})
	if written.Error != nil {
		return false, written.Error
	}
	if written.RowsAffected == 0 {
		return false, tenantExists(t.ID)
	}
	return true, nil
}

func creationTime() time.Time {
	return time.Now().UTC()
}

func tenantExists(id tenant.ID) error {
	return fmt.Errorf("%w: identifier %q", tenant.ErrTenantExists, id)
}

func failedCreate(session *gorm.DB, t tenant.Tenant, err error) error {
	if errors.Is(err, tenant.ErrTenantExists) || errors.Is(err, tenant.ErrNameConflict) {
		return err
	}
	held, lookErr := lookUp(session, "id", string(t.ID))
	if lookErr == nil && len(held) > 0 {
		return fmt.Errorf("%w, by a write that got there while this one was in flight: %w",
			tenantExists(t.ID), err)
	}
	return failedPut(session, t, err)
}

// SetStatus implements tenant.Catalog as the operator's transition. Only active and disabled are accepted, and the disable source is written as the operator's.
// A tombstone is refused with ErrTenantDeleted, and an unknown identifier or any other status with ErrInvalidTenant.
func (r *roster) SetStatus(ctx context.Context, id tenant.ID, s tenant.Status) error {
	if err := checkField("identifier", string(id)); err != nil {
		return err
	}
	if s != tenant.StatusActive && s != tenant.StatusDisabled {
		return fmt.Errorf("%w: %d is not a status an operator sets; deletion goes through Delete",
			tenant.ErrInvalidTenant, int(s))
	}
	return r.write(ctx, func(tx *gorm.DB) error {
		written := tx.Model(&row{}).
			Where("id = ? AND status <> ?", string(id), int(tenant.StatusDeleted)).
			Updates(map[string]any{
				"status":      int(s),
				"disabled_by": int(tenant.DisabledByOperator),
			})
		if written.Error != nil {
			return written.Error
		}
		if written.RowsAffected > 0 {
			return nil
		}
		return missingOrTombstone(tx, id)
	}, nil)
}

// SetStatusByAccount implements tenant.Catalog as the account-driven transition. Disabling is taken only from an active tenant and writes the account as the source; enabling only from a tenant the account disabled.
// Any other request writes nothing and returns nil; a tombstone is ErrTenantDeleted, and an unknown identifier or any other status ErrInvalidTenant.
func (r *roster) SetStatusByAccount(ctx context.Context, id tenant.ID, s tenant.Status) error {
	if err := checkField("identifier", string(id)); err != nil {
		return err
	}
	if s != tenant.StatusActive && s != tenant.StatusDisabled {
		return fmt.Errorf("%w: %d is not a status account state sets; deletion goes through Delete",
			tenant.ErrInvalidTenant, int(s))
	}
	_, err := r.writeResult(ctx, func(tx *gorm.DB) (bool, error) {
		var written *gorm.DB
		switch s {
		case tenant.StatusDisabled:
			written = tx.Model(&row{}).
				Where("id = ? AND status = ?", string(id), int(tenant.StatusActive)).
				Updates(map[string]any{
					"status":      int(tenant.StatusDisabled),
					"disabled_by": int(tenant.DisabledByAccount),
				})
		case tenant.StatusActive:
			written = tx.Model(&row{}).
				Where("id = ? AND status = ? AND disabled_by = ?",
					string(id), int(tenant.StatusDisabled), int(tenant.DisabledByAccount)).
				Updates(map[string]any{
					"status":      int(tenant.StatusActive),
					"disabled_by": int(tenant.DisabledByOperator),
				})
		}
		if written.Error != nil {
			return false, written.Error
		}
		if written.RowsAffected > 0 {
			return true, nil
		}
		return false, missingOrTombstone(tx, id)
	}, nil)
	return err
}

// Delete implements tenant.Catalog. It writes the deleted status and the deletion instant in UTC and keeps the row; deleting a tombstone again returns nil.
// An unknown identifier is refused with ErrInvalidTenant.
func (r *roster) Delete(ctx context.Context, id tenant.ID) error {
	if err := checkField("identifier", string(id)); err != nil {
		return err
	}
	_, err := r.writeResult(ctx, func(tx *gorm.DB) (bool, error) {
		return r.deleteIn(ctx, tx, id)
	}, nil)
	return err
}

func (r *roster) deleteIn(_ context.Context, tx *gorm.DB, id tenant.ID) (bool, error) {
	held, err := lookUp(tx, "id", string(id))
	if err != nil {
		return false, err
	}
	if len(held) == 0 {
		return false, fmt.Errorf("%w: no tenant has identifier %q", tenant.ErrInvalidTenant, id)
	}
	if tenant.Status(held[0].Status) == tenant.StatusDeleted {
		return false, nil
	}
	return true, tx.Model(&row{}).Where("id = ?", string(id)).
		Updates(map[string]any{
			"status":     int(tenant.StatusDeleted),
			"deleted_at": deletionTime(),
		}).Error
}

func deletionTime() time.Time {
	return time.Now().UTC()
}

// SetOwner implements tenant.Catalog. It writes the owner alone, which must be an identifier; writing the owner a row already has is a no-op returning nil.
// A tombstone is refused with ErrTenantDeleted, and an unknown identifier with ErrInvalidTenant.
func (r *roster) SetOwner(ctx context.Context, id tenant.ID, owner string) error {
	if err := checkField("identifier", string(id)); err != nil {
		return err
	}
	if err := checkField("owner", owner); err != nil {
		return err
	}
	_, err := r.writeResult(ctx, func(tx *gorm.DB) (bool, error) {
		held, err := lookUp(tx, "id", string(id))
		if err != nil {
			return false, err
		}
		if len(held) == 0 {
			return false, fmt.Errorf("%w: no tenant has identifier %q", tenant.ErrInvalidTenant, id)
		}
		if tenant.Status(held[0].Status) == tenant.StatusDeleted {
			return false, tombstone(id)
		}
		if held[0].OwnerAccount == owner {
			return false, nil
		}
		written := tx.Model(&row{}).
			Where("id = ? AND status <> ?", string(id), int(tenant.StatusDeleted)).
			Update("owner_account", owner)
		if written.Error != nil {
			return false, written.Error
		}
		if written.RowsAffected > 0 {
			return true, nil
		}
		return false, missingOrTombstone(tx, id)
	}, nil)
	return err
}

// SetOwnerIfEmpty implements tenant.Catalog. It writes the owner only while the column is empty and the row is not deleted, and reports whether it wrote; a standing owner answers false with nil.
// Errors are those of SetOwner.
func (r *roster) SetOwnerIfEmpty(ctx context.Context, id tenant.ID, owner string) (bool, error) {
	if err := checkField("identifier", string(id)); err != nil {
		return false, err
	}
	if err := checkField("owner", owner); err != nil {
		return false, err
	}
	return r.writeResult(ctx, func(tx *gorm.DB) (bool, error) {
		written := tx.Model(&row{}).
			Where("id = ? AND owner_account = ? AND status <> ?", string(id), "", int(tenant.StatusDeleted)).
			Update("owner_account", owner)
		if written.Error != nil {
			return false, written.Error
		}
		if written.RowsAffected > 0 {
			return true, nil
		}
		held, err := lookUp(tx, "id", string(id))
		if err != nil {
			return false, err
		}
		if len(held) == 0 {
			return false, fmt.Errorf("%w: no tenant has identifier %q", tenant.ErrInvalidTenant, id)
		}
		if tenant.Status(held[0].Status) == tenant.StatusDeleted {
			return false, tombstone(id)
		}
		return false, nil
	}, nil)
}

// SetProfile implements tenant.Catalog. It writes the four profile columns alone, after validating the profile.
// A tombstone is refused with ErrTenantDeleted, and an unknown identifier with ErrInvalidTenant.
func (r *roster) SetProfile(ctx context.Context, id tenant.ID, profile tenant.Profile) error {
	if err := checkField("identifier", string(id)); err != nil {
		return err
	}
	if err := profile.Validate(); err != nil {
		return err
	}
	return r.write(ctx, func(tx *gorm.DB) error {
		held, err := lookUp(tx, "id", string(id))
		if err != nil {
			return err
		}
		if len(held) == 0 {
			return fmt.Errorf("%w: no tenant has identifier %q", tenant.ErrInvalidTenant, id)
		}
		if tenant.Status(held[0].Status) == tenant.StatusDeleted {
			return tombstone(id)
		}
		written := tx.Model(&row{}).
			Where("id = ? AND status <> ?", string(id), int(tenant.StatusDeleted)).
			Updates(map[string]any{
				"display_name":  profile.DisplayName,
				"description":   profile.Description,
				"contact_email": profile.ContactEmail,
				"website":       profile.Website,
			})
		if written.Error != nil {
			return written.Error
		}
		if written.RowsAffected > 0 {
			return nil
		}
		return missingOrTombstone(tx, id)
	}, nil)
}

// lookUp returns an empty result, not First's not-found failure, which GORM's logger would write as an error record.
func lookUp(tx *gorm.DB, column, value string) ([]row, error) {
	var held []row
	if err := tx.Where(clause.Eq{Column: clause.Column{Name: column}, Value: value}).
		Limit(1).Find(&held).Error; err != nil {
		return nil, err
	}
	return held, nil
}

// afterWrite rereads with the caller's cancellation removed: the write has committed, and ending the reread with the caller would leave this replica serving a roster without it.
func (r *roster) afterWrite(ctx context.Context) error {
	if err := r.read(context.WithoutCancel(ctx)); err != nil {
		return fmt.Errorf(
			"%s%s: the write was applied, but the roster could not be reread, so this "+
				"process is serving the roster as it was before it: %w",
			diagnosticPrefix, ModuleName, err)
	}
	return nil
}

func checkTenant(t tenant.Tenant) error {
	if err := checkField("identifier", string(t.ID)); err != nil {
		return err
	}
	if err := checkName(t.Name); err != nil {
		return err
	}
	if t.OwnerAccount != "" && !tenant.IsIdentifier(t.OwnerAccount) {
		return fmt.Errorf("%w: the owner account carries a blank or a control character",
			tenant.ErrInvalidTenant)
	}
	return t.Profile.Validate()
}

func checkWrite(t tenant.Tenant) error {
	if err := checkTenant(t); err != nil {
		return err
	}
	if t.Status == tenant.StatusDeleted {
		return fmt.Errorf("%w: a registration cannot write the deleted state; Delete is its only route",
			tenant.ErrInvalidTenant)
	}
	if t.DisabledBy != tenant.DisabledByOperator {
		return fmt.Errorf("%w: a registration cannot record the disable source %d",
			tenant.ErrInvalidTenant, int(t.DisabledBy))
	}
	return nil
}

func checkName(name string) error {
	if name == "" {
		return fmt.Errorf("%w: the name is empty", tenant.ErrInvalidTenant)
	}
	if !tenant.IsName(name) {
		return fmt.Errorf("%w: the name is not a lower-case slug of 3 to 63 letters, digits and dashes",
			tenant.ErrInvalidTenant)
	}
	return nil
}

func checkNamespace(tx *gorm.DB, t tenant.Tenant) error {
	held, err := lookUp(tx, "name", t.Name)
	if err != nil {
		return err
	}
	if len(held) > 0 {
		return fmt.Errorf("%w: name %q is held by tenant %q",
			tenant.ErrNameConflict, t.Name, held[0].ID)
	}
	held, err = lookUp(tx, "id", t.Name)
	if err != nil {
		return err
	}
	if len(held) > 0 {
		return fmt.Errorf("%w: name %q is the identifier of tenant %q",
			tenant.ErrNameConflict, t.Name, held[0].ID)
	}
	held, err = lookUp(tx, "name", string(t.ID))
	if err != nil {
		return err
	}
	if len(held) > 0 {
		return fmt.Errorf("%w: identifier %q is the name of tenant %q",
			tenant.ErrTenantExists, t.ID, held[0].ID)
	}
	return nil
}

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
