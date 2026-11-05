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

	// reading is held by the one read of the table in flight. It is a channel
	// of one slot rather than a mutex so that a read waiting for it gives up
	// when its context ends.
	reading chan struct{}

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
		reading: make(chan struct{}, 1),
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
// why, which is the shape this capability directory refuses wherever it meets
// it.
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
		r.reportFailedReread(err)
	}
}

// reportFailedReread says that a reread failed and the snapshot it would have
// replaced is still the one served.
func (r *roster) reportFailedReread(err error) {
	r.logger.Warn(diagnosticPrefix+ModuleName+
		": the roster could not be reread; the previous one stays in force",
		"error", err)
}

// read replaces the snapshot with the table's current contents.
//
// Reads run one at a time, and each publishes what it read. Three paths read —
// the refresher, a write rereading once it has committed, and an entry bringing
// the snapshot up to the table — and two reads in flight together could publish
// out of order: a refresh that read the table before a write committed,
// finishing after the write's own reread, would put the roster without the
// write back in force, and a tenant just disabled would be served again until
// the next refresh. In sequence, the read that read last publishes last.
func (r *roster) read(ctx context.Context) error {
	if err := r.acquire(ctx); err != nil {
		return fmt.Errorf("%s%s: reading table %s was given up while another read of it was in flight: %w",
			diagnosticPrefix, ModuleName, tableName, err)
	}
	defer r.release()
	return r.readHeld(ctx)
}

// acquire takes the one slot reads of the table run in, or gives up when the
// context ends first.
func (r *roster) acquire(ctx context.Context) error {
	select {
	case r.reading <- struct{}{}:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// release gives the slot back.
func (r *roster) release() { <-r.reading }

// readHeld is read, run by a caller already holding the slot.
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

// onConnection runs fn — the roster's own statements — on a connection of the
// delivered pool taken through db.Connection, within connectionWait, and reports
// whether fn ran.
//
// The roster's statements never join a caller's transaction: its methods take a
// context, which carries none. db.Connection bounds the wait for the
// connection, so a caller holding the one connection of a database held to one
// gets a failure once the bound has passed rather than a statement that waits
// for it. The statements themselves run under the caller's context and wait for
// a lock another connection holds as the engine makes them wait
// (connectionWait).
//
// A connection that does not come free within the bound is reported with
// ErrNoConnection, and a context that ends first with the context's own error;
// no statement ran, so neither says anything about the table. Any other
// failure to take a connection is reported as it stands.
func (r *roster) onConnection(ctx context.Context, fn func(session *gorm.DB) error) (bool, error) {
	ran := false
	err := db.Connection(ctx, r.handle, connectionWait, func(session *gorm.DB) error {
		ran = true
		return fn(session)
	})
	if err != nil && !ran && errors.Is(err, context.DeadlineExceeded) && ctx.Err() == nil {
		return false, fmt.Errorf("%w: %w", tenant.ErrNoConnection, err)
	}
	return ran, err
}

// failedStatement is the failure of a statement of the roster's on its table,
// under ctx, on the session it ran on.
//
// A table that is not there — never created, or dropped since — fails a
// statement as a statement fails for any other reason, and the drivers word the
// failure each their own way, so the failure alone does not say which it was.
// The database is asked instead, on the same connection (tableAbsent): when it
// has no table by this name, the failure carries ErrRosterMissing, whose repair
// is the migration set. Every other failure — a lock the engine gave up
// waiting for, a connection lost, a permission refused — is reported as it
// stands, with no sentinel, since there is no table missing to repair; so is
// one whose question fails too, since nothing then says the table is not
// there. A statement whose context ended is reported with the context's error.
// The statement's own failure is kept in the chain and in the text either way.
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

// tableAbsent reports whether the database a session is on has no table named
// as the roster's where the roster's statements look for it, and reports
// nothing absent when the question itself fails.
//
// On SQLite, which has no privileges, the tables GORM's migrator lists — the
// main database's — are asked.
//
// On PostgreSQL the schemas asked are the ones the session's search path
// names (namedSchemas), which is where the statement looked and where this
// module's migration creates the table, and the ones the effective path adds
// without their being named — the system catalog and the session's temporary
// schema. The system catalog is read for a relation of that name in any of
// them, which takes no privilege, where the alternatives do: the information
// schema lists only the tables the role holds a privilege on, and resolving the
// name as a statement does skips a schema the role holds no USAGE on, since
// PostgreSQL drops such a schema from the path it searches. Either way a table
// that is there would read as one that is not, and a refused permission would
// be answered with the migration.
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

// namedSchemas lists the schemas a PostgreSQL session's search path names,
// read as PostgreSQL reads the setting (searchPath), with "$user" standing for
// the session's current role. A schema the role holds no USAGE on, or that
// does not exist, is kept: the question is where a statement would look for
// the table, not whether the role may reach it. The schemas PostgreSQL searches
// without their being named — the system catalog, and the session's temporary
// schema when it has one — are the effective path's, which tableAbsent asks
// beside these.
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

// blanks are the characters PostgreSQL skips around an item of an identifier
// list.
const blanks = " \t\n\r\f\v"

// searchPath reads the names a PostgreSQL search_path setting lists, as
// PostgreSQL splits an identifier list: separated by commas, the blanks around
// each item dropped, a name in double quotes taken as written with a doubled
// quote standing for one, and any other name folded to lower case in its ASCII
// letters.
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

// quotedName reads a double-quoted name off the front of s, a doubled quote
// inside it standing for one, and returns it with what follows the closing
// quote.
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

// asciiLower folds the ASCII letters of a name to lower case, as PostgreSQL
// folds an unquoted identifier in a multibyte encoding.
func asciiLower(s string) string {
	return strings.Map(func(r rune) rune {
		if 'A' <= r && r <= 'Z' {
			return r + ('a' - 'A')
		}
		return r
	}, s)
}

// postgresDialector is the name the PostgreSQL engine's GORM dialector answers.
const postgresDialector = "postgres"

// connectionWait is how long each of the roster's own reads and writes — the
// reads of the whole table, an entry's read of one row, and each write with the
// reads it makes — waits for a connection of the delivered pool, through
// db.Connection.
//
// The roster's statements run on connections of their own, and a caller may be
// holding what they need. On a database held to one connection — pkg/db holds
// an in-memory SQLite database to one — a caller inside its own transaction on
// the delivered handle holds that connection until its own call returns, and a
// statement of the roster's waiting for it is waiting for the caller. An
// in-process bus is the ordinary shape of it, running a subscriber on its
// publisher's goroutine inside whatever transaction the publisher holds, and
// the subscriber enters the event's tenant. Nothing a statement can see tells
// that hold from another caller's brief one, so the wait is bounded rather than
// recognised. A connection taken under load comes free in the time a statement
// takes, far inside it.
//
// A lock another connection holds is waited for as the engine waits for it,
// which the bound does not reach. On SQLite that is the connection's busy
// timeout, the driver's five seconds unless the locator sets another; a
// roster write inside a caller's transaction that has written to the same
// database file meets that caller's write lock at its transaction's BEGIN,
// which the database module issues IMMEDIATE, and waits there for the busy
// timeout before SQLite refuses it. On PostgreSQL it is until
// the caller's context ends, or the session's lock_timeout or
// statement_timeout does; a roster write to a row the caller's own transaction
// has written waits on that caller, and with neither timeout and no deadline it
// waits for good.
const connectionWait = 5 * time.Second

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
		if err := checkTenant(tenantOf(r)); err != nil {
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
func (r *roster) Enter(ctx context.Context, id tenant.ID) (context.Context, error) {
	return r.enter(ctx, id, false)
}

// EnterRegistered implements tenant.Catalog.
func (r *roster) EnterRegistered(ctx context.Context, id tenant.ID) (context.Context, error) {
	return r.enter(ctx, id, true)
}

// enter is both entries: Enter admits a tenant the roster serves, and
// EnterRegistered one it holds whatever its status. Enter refuses a disabled
// tenant with ErrTenantDisabled and a tombstone with ErrTenantDeleted — the two
// are disposed of differently, one waits out and the other is dropped — while
// EnterRegistered enters both.
//
// It admits from the snapshot the resolving layer reads, and refuses only on
// the table's word. A refusal is what a path with no request behind it drops
// its work on — a bus acknowledges an event whatever its handler answered — so
// a snapshot behind the table would lose that work for good: a tenant another
// replica registered, or served again, since this replica last read the roster.
// Where the snapshot would refuse, the tenant's row is read from the table, the
// answer is the row's, and a snapshot found behind it is brought up to the table
// (catchUp). An admission is taken from the snapshot as it stands, so a tenant
// disabled on another replica is entered here until this replica's snapshot is
// next reread — the window the request face has too.
//
// The identifier's shape is judged first, because a value outside the domain
// is no tenant's and the message says which of the two fixes it needs. A table
// that cannot be read gives no answer, and the failure is reported as it
// stands rather than as either refusal: with ErrRosterMissing when the table is
// not there, with ErrNoConnection when no connection came free within
// connectionWait, with the context's error when the caller's context ended, and
// otherwise as the read failed (failedStatement). ErrNoConnection is what an
// entry inside a transaction the caller holds on a database held to one
// connection meets, once the snapshot would refuse; an admission reads nothing,
// and is not held up by it.
//
// What it writes is what the resolving layer writes on admitting a request —
// the identifier into the context and onto the logger, replacing a tenant the
// context carried in both — through the same internal package, which is what
// keeps the writing side closed to everything outside this capability
// directory. A refusal writes the empty identifier instead, which lifts a
// cross-tenant mark the context carried and takes a tenant bound on its logger
// off: a caller that drops the error and goes on with the context has its
// scoped statements refused, not let across, and its records name no tenant.
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

// fromTable reads one tenant's row from the table, under the caller's context,
// on a connection taken through onConnection. A row this module will not act on
// is refused as the read of the whole roster refuses it.
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

// catchUp rereads the roster when the row an entry read from the table is not
// what this replica's snapshot holds for it, so that the snapshot — which the
// resolving layer reads too — learns what the entry learned.
//
// Entries arriving together for one such tenant wait for one another on the
// slot reads run in; the first to find the snapshot still behind rereads it,
// and the rest find it caught up. An entry whose context ends while it waits
// leaves the snapshot to the next reread. The wait for the slot is as long as
// the reads that hold it take, each of which waits at most connectionWait for
// its connection and for a lock as long as the engine waits for one
// (connectionWait). A failed reread is reported as a failed refresh is, and the
// entry keeps the answer it read.
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

// agrees reports whether a snapshot holds what the table was found to hold for
// one identifier: this tenant, or, when held is false, none.
func agrees(s *snapshot, id tenant.ID, t tenant.Tenant, held bool) bool {
	current, ok := s.byID[id]
	return ok == held && sameTenant(current, t)
}

// sameTenant reports whether two readings name the same tenant. It compares the
// creation instants and the deletion instants rather than the pointers that
// hold them: two reads of one row build two values, and a struct comparison
// would take their addresses as the tenant's identity.
func sameTenant(a, b tenant.Tenant) bool {
	if a.ID != b.ID || a.Alias != b.Alias || a.Status != b.Status ||
		a.DisabledBy != b.DisabledBy || a.OwnerAccount != b.OwnerAccount ||
		a.Profile != b.Profile || !a.CreatedAt.Equal(b.CreatedAt) {
		return false
	}
	if a.DeletedAt == nil || b.DeletedAt == nil {
		return a.DeletedAt == nil && b.DeletedAt == nil
	}
	return a.DeletedAt.Equal(*b.DeletedAt)
}

// contextLogger is how an entered tenant reaches the logger a context carries.
var contextLogger = tenantctx.Logger{With: log.WithAttrs, Without: log.WithoutAttrs}

// Put implements tenant.Catalog.
//
// The rewrite is by identifier, so registering a tenant twice is one row. An
// alias is chosen when the row is written and is immutable afterwards: a
// rewrite whose value carries another alias is refused with ErrInvalidTenant
// and writes nothing, while one carrying the alias the row already has changes
// only the status and the disable source.
//
// An insert takes the alias, the owner and the creation time, and the alias is
// checked inside the same transaction as the write: the column's unique index
// would catch a collision too, but it would report it as a constraint violation
// rather than as which tenant holds the name. The alias and the identifier are
// one namespace — a slug may be a tenant's alias or its identifier, never both —
// so an insert also refuses an alias another tenant carries as its identifier
// and an identifier another tenant carries as its alias (checkNamespace).
//
// The check does not close the race on its own. Two writes putting one alias
// on two tenants at once can both find it free, and the later insert then
// meets the unique index. That is the same conflict, and it is answered the
// same way: a write that failed is read again against the table, and an alias
// another tenant now holds is reported as ErrAliasConflict naming it, with the
// driver's error kept in the chain.
//
// A row that is a tombstone is refused with ErrTenantDeleted, and nothing is
// written: a deleted tenant is terminal, and the rewrite would otherwise serve it
// again. The upsert carries the same condition in its own statement, so a
// deletion committed between the check and the write still leaves the tombstone
// as it is; a statement that updated no row is read again to tell the tombstone
// apart from a row that is no longer there. The value's own status and disable
// source are judged before the write as well (checkWrite), so a record that could
// not have come from a status transition is refused rather than stored.
//
// The write and the read that names a conflict run on one connection (write).
//
// The profile, owner and alias columns are not written by a rewrite. A rewrite
// takes the status and the disable source, so it cannot put back a profile
// another path changed, an owner another path transferred, or an alias the row
// was not created with; an insert it performs takes the migration's profile
// defaults and the value's owner. The
// creation time is written on an insert and only on an insert: a registration
// made through Put records the real moment in UTC, exactly as Create does, while
// the upsert's update clause leaves an existing row's time alone. Updating a
// profile is SetProfile's work and transferring an owner is SetOwner's.
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
		case len(held) > 0 && held[0].Alias != t.Alias:
			return fmt.Errorf("%w: tenant %q already carries the alias %q, and an alias "+
				"is chosen at creation and never changed", tenant.ErrInvalidTenant, t.ID, held[0].Alias)
		case len(held) == 0:
			if err := checkNamespace(tx, t); err != nil {
				return err
			}
		}
		written := tx.Clauses(clause.OnConflict{
			Columns: []clause.Column{{Name: "id"}},
			// The alias is immutable: an existing row keeps the one it was
			// written with, and the value's alias is read only by the insert
			// below.
			DoUpdates: clause.AssignmentColumns([]string{"status", "disabled_by"}),
			// The rewrite itself refuses a tombstone: the check above runs
			// before the statement, and a deletion committed between the two
			// would otherwise be overwritten by the values this caller read.
			Where: clause.Where{Exprs: []clause.Expression{
				clause.Neq{Column: clause.Column{Table: tableName, Name: "status"}, Value: int(tenant.StatusDeleted)},
			}},
		}).Create(&row{
			ID:         string(t.ID),
			Alias:      t.Alias,
			Status:     int(t.Status),
			DisabledBy: int(t.DisabledBy),

			// The owner is written on an insert and never by the update clause,
			// so a rewrite cannot transfer an organization's ownership.
			OwnerAccount: t.OwnerAccount,

			// The creation time is the roster's to record, as on Create. The
			// update clause does not name the column, so an existing row keeps
			// the time it has; a value left to gorm's auto-create would be
			// local rather than UTC.
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

// write runs one write of the roster in a transaction of its own, on a
// connection taken through onConnection; failed, when there is one, names what
// a failed transaction ran into, reading on that same connection outside the
// transaction. A failure that is none of this module's answers about the tenant
// is then reported as failedStatement reports it, on that connection too. The
// connection is given back before the reread that follows a write takes its
// own, which on a database held to one connection is the only one there is.
func (r *roster) write(ctx context.Context, apply func(tx *gorm.DB) error,
	failed func(session *gorm.DB, err error) error,
) error {
	_, err := r.writeResult(ctx, func(tx *gorm.DB) (bool, error) {
		return true, apply(tx)
	}, failed)
	return err
}

// writeResult is write for a caller that may decide, inside the transaction,
// that there is nothing to write. It reports whether a write happened, and
// rereads the roster only when one did: a call that changed nothing has no
// result for this process to serve, and paying a full read for it would turn a
// repeated event into load.
//
// A transaction that fails reports false whatever apply said about writing: it
// rolled back, so nothing reached the table.
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

// answered reports whether a write's failure is one of this module's answers
// about the tenant — a conflict, a registration already there, an identifier no
// row carries, a tombstone — which the table was read to give, rather than a
// statement that failed.
func answered(err error) bool {
	return errors.Is(err, tenant.ErrAliasConflict) || errors.Is(err, tenant.ErrTenantExists) ||
		errors.Is(err, tenant.ErrInvalidTenant) || errors.Is(err, tenant.ErrTenantDeleted)
}

// tombstone is the refusal of a write that reached a tenant written as deleted.
func tombstone(id tenant.ID) error {
	return fmt.Errorf("%w: identifier %q", tenant.ErrTenantDeleted, id)
}

// missingOrTombstone explains a statement that changed no row, read inside the
// statement's own transaction. An identifier no row carries is ErrInvalidTenant,
// and a row written as deleted is ErrTenantDeleted; a row that is there and not
// a tombstone is a write that had nothing to change, and is no error. The read
// is what tells a tombstone committed after the caller's own check from a
// traversal the caller is allowed to skip.
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

// failedPut names the conflict a failed Put ran into when the alias turns out
// to be held by another tenant, and returns the failure as it stands otherwise.
// The read runs on the write's session outside the failed transaction, which a
// PostgreSQL transaction that met a constraint no longer accepts statements in.
func failedPut(session *gorm.DB, t tenant.Tenant, err error) error {
	return failedAlias(session, t.ID, t.Alias, err)
}

// failedAlias names the conflict a failed write on one tenant's alias ran into
// — Put, and Create through failedCreate — when the alias turns out to be held
// by another tenant, and returns the failure as it stands otherwise. A write
// that meets the alias column's unique index in a race reaches the same conflict
// the check inside the write reports, and the caller is owed the same answer for
// it.
func failedAlias(session *gorm.DB, id tenant.ID, alias string, err error) error {
	if errors.Is(err, tenant.ErrAliasConflict) {
		return err
	}
	held, lookErr := lookUp(session, "alias", alias)
	if lookErr != nil || len(held) == 0 || held[0].ID == string(id) {
		return err
	}
	return fmt.Errorf("%w: alias %q is held by tenant %q, which took it while this write was "+
		"in flight: %w", tenant.ErrAliasConflict, alias, held[0].ID, err)
}

// Create implements tenant.Catalog.
//
// The value's profile is written with the row, and so is a creation time: the
// moment of the registration in UTC, never a value taken from the caller. A
// registration date is a fact about the roster, and a caller that could choose
// it could date a tenant before its account existed. The profile is judged by
// checkWrite before any statement, as the identifier and the alias are.
//
// A tombstone's identifier is taken as any existing identifier is: the creation
// is refused with ErrTenantExists, so a deleted tenant is never registered
// again, and its alias stays with the row. The value's own status and disable
// source are judged before the write (checkWrite), as Put judges them.
//
// Whether the identifier is taken is asked of the table inside the write, not
// of the snapshot: a replica whose snapshot predates another replica's
// registration would otherwise take the identifier for free, and the write
// would land on that tenant's row. The insert itself does nothing when the
// identifier is already there, and a write that inserted nothing is the
// refusal — so two creations of one identifier at once end with one row and
// one ErrTenantExists, the later one having waited on the earlier's row rather
// than overwriting it. The alias and the identifier are one namespace, so an
// insert also refuses an alias another tenant carries as its identifier and an
// identifier another tenant carries as its alias (checkNamespace); the alias's
// race is answered as Put answers it, on the write's one connection (write).
func (r *roster) Create(ctx context.Context, t tenant.Tenant) error {
	if err := checkWrite(t); err != nil {
		return err
	}
	return r.write(ctx, func(tx *gorm.DB) error {
		held, err := lookUp(tx, "id", string(t.ID))
		if err != nil {
			return err
		}
		if len(held) > 0 {
			return tenantExists(t.ID)
		}
		if err := checkNamespace(tx, t); err != nil {
			return err
		}
		written := tx.Clauses(clause.OnConflict{
			Columns:   []clause.Column{{Name: "id"}},
			DoNothing: true,
		}).Create(&row{
			ID:         string(t.ID),
			Alias:      t.Alias,
			Status:     int(t.Status),
			DisabledBy: int(t.DisabledBy),

			OwnerAccount: t.OwnerAccount,

			DisplayName:  t.Profile.DisplayName,
			Description:  t.Profile.Description,
			ContactEmail: t.Profile.ContactEmail,
			Website:      t.Profile.Website,
			// The creation time is the roster's to record, not the caller's to
			// choose: a value read off the request would let a registration
			// date itself. A row that already exists keeps the time it has,
			// because this insert does nothing on a taken identifier.
			CreatedAt: creationTime(),
		})
		if written.Error != nil {
			return written.Error
		}
		if written.RowsAffected == 0 {
			return tenantExists(t.ID)
		}
		return nil
	}, func(session *gorm.DB, err error) error { return failedCreate(session, t, err) })
}

// creationTime is the moment a registration is recorded at, in UTC. A new
// value each call: two registrations, however close, do not share one.
func creationTime() time.Time {
	return time.Now().UTC()
}

// tenantExists is the refusal of a creation whose identifier is taken.
func tenantExists(id tenant.ID) error {
	return fmt.Errorf("%w: identifier %q", tenant.ErrTenantExists, id)
}

// failedCreate names what a failed Create ran into: its own refusals as they
// stand, and a driver failure as the conflict it turns out to be once the table
// is read again — the identifier registered, or the alias taken, by a write
// that got there first. The reads run on the write's session, as failedPut's
// does.
func failedCreate(session *gorm.DB, t tenant.Tenant, err error) error {
	if errors.Is(err, tenant.ErrTenantExists) || errors.Is(err, tenant.ErrAliasConflict) {
		return err
	}
	held, lookErr := lookUp(session, "id", string(t.ID))
	if lookErr == nil && len(held) > 0 {
		return fmt.Errorf("%w, by a write that got there while this one was in flight: %w",
			tenantExists(t.ID), err)
	}
	return failedPut(session, t, err)
}

// SetStatus implements tenant.Catalog, and is the operator's status transition.
//
// Only active and disabled are writable here: the deleted state is reached
// through Delete and nowhere else, so s naming it, or naming anything outside
// the set, is refused with ErrInvalidTenant. A tombstone is refused with
// ErrTenantDeleted. Disabling writes the source as the operator's even on a row
// already disabled — the operator has restated the decision, and a row an
// account disabled must not stay revocable by that account — and enabling
// clears it; the two writes put the same operator value in the column.
//
// Existence is checked with its own read rather than inferred from the update's
// affected-row count: a driver is free to report zero rows for an update that
// set a value to what it already was, and this call would then report a tenant
// that exists as one that does not. The check and the update run on one
// connection (write).
//
// The transition is written by a statement that refuses a tombstone itself, so a
// deletion committed between the check and the write cannot be overwritten: the
// statement updates no row, and the read that distinguishes a tombstone from a
// missing tenant runs inside the same transaction. The same read covers the
// driver that reports no row for a value written as it already was.
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

// SetStatusByAccount implements tenant.Catalog, and is the transition account
// state drives.
//
// The two directions are asymmetric on purpose. Disabling is taken only from an
// active tenant, and writes the source as the account's; a tenant already
// disabled is not account state's to disable again, whoever disabled it.
// Enabling is taken only from a tenant disabled by the account, and clears the
// source; an operator's disable, and a source this build does not know, are not
// account state's to revoke. Every other request writes nothing and returns nil,
// so a repeated event is harmless and a no-op costs no reread (writeResult).
//
// Neither direction reaches a tombstone, which is refused with ErrTenantDeleted,
// and s naming the deleted state, or anything outside the set, is refused with
// ErrInvalidTenant. An identifier no row carries is ErrInvalidTenant, as the
// operator path reports it.
//
// Each direction is written by a statement that carries the state it is allowed
// to leave behind, so a deletion or an operator's disable committed between the
// read and the write is not overwritten: the statement updates no row, and a
// read inside the same transaction distinguishes the tombstone and the missing
// tenant from a transition that is simply not this call's to make. That read is
// also what covers a driver reporting no row for a value written as it already
// was.
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

// Delete implements tenant.Catalog, and is the one way to reach the terminal
// state.
//
// It writes the status column and the deletion instant together: the row, the
// identifier, the alias, the owner and the disable source stay, so a scoped
// table's data still has a tenant to be found under and the alias cannot be
// registered again while the row carries it. The instant is recorded in UTC and
// is the moment the terminal state was reached; the status stays the state a
// request is resolved by. Deleting a tombstone again is a no-op that returns
// nil, so a sweep retrying a deletion converges. An identifier no row carries,
// or one outside the domain, is refused with ErrInvalidTenant, as the status
// paths refuse it.
func (r *roster) Delete(ctx context.Context, id tenant.ID) error {
	if err := checkField("identifier", string(id)); err != nil {
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
			return false, nil
		}
		return true, tx.Model(&row{}).Where("id = ?", string(id)).
			Updates(map[string]any{
				"status":     int(tenant.StatusDeleted),
				"deleted_at": deletionTime(),
			}).Error
	}, nil)
	return err
}

// deletionTime is the moment a tenant was written as deleted, in UTC. A new
// value each call, as creationTime is.
func deletionTime() time.Time {
	return time.Now().UTC()
}

// SetOwner implements tenant.Catalog, and is the transfer write.
//
// It writes the owner_account column and nothing else. The alias, the status,
// the profile, the creation time and the deletion time are not named, so a
// transfer cannot undo a disable or a profile update committed in the same
// window. The owner must be an identifier: the empty string is refused because
// a business organization always has one, and so is a value carrying a blank or
// a control character.
//
// A tombstone is refused with ErrTenantDeleted: its owner stays with the row.
// Writing the owner a row already carries is a no-op that returns nil and reads
// nothing from the table, so a repeated transfer costs no reread. An identifier
// no row carries is refused with ErrInvalidTenant. The update carries the
// tombstone condition itself, so a deletion committed between the check and the
// write leaves the row as it is; a statement that updated no row is read again
// to tell a tombstone from a tenant that is no longer there.
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

// SetProfile implements tenant.Catalog, and writes the organization profile
// alone.
//
// The four profile columns are the whole statement. The alias, the owner, the
// status, the disable source and the creation time are not named, so a profile
// update cannot put back a value this process read before another path changed
// it: a disable or a deletion committed in the window stays. The tombstone
// condition is in the statement itself, as it is in SetStatus and SetOwner, so
// a deletion committed between the checks and the write leaves the row as it
// is; a statement that updated no row is read again to tell a tombstone from a
// tenant that is no longer there.
//
// The profile is judged before any write, so a refused update leaves the table
// as it found it. A tombstone is refused with ErrTenantDeleted: its profile is
// as terminal as the rest of its row.
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

// lookUp reads the row, if any, whose unique column holds this value.
//
// Finding none is an answer, and it is read as one: an empty result rather than
// the not-found failure GORM's First reports. That failure reaches GORM's
// logger as a failed statement, which the database module's logger writes as an
// error record — on every tenant a new alias is put for, and on every status
// change naming a tenant that is not there.
//
// A read that fails is returned as the driver reported it. Its caller reports
// it as every failed statement of the roster's is (failedStatement), once it is
// outside any transaction the read ran in: a PostgreSQL transaction a statement
// failed in accepts no further statement, and telling a table that is not there
// from any other failure takes one.
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
//
// The reread runs on the caller's context with its cancellation taken off. The
// write has committed by then, so a caller that goes away at that moment has
// nothing left to stop: ending the reread with it would only leave this replica
// serving a roster without the write until the next refresh — or, with none
// configured, until a restart — and report as failed a write that took place.
// The values the context carries, and so the logger and the trace, stay.
func (r *roster) afterWrite(ctx context.Context) error {
	if err := r.read(context.WithoutCancel(ctx)); err != nil {
		return fmt.Errorf(
			"%s%s: the write was applied, but the roster could not be reread, so this "+
				"process is serving the roster as it was before it: %w",
			diagnosticPrefix, ModuleName, err)
	}
	return nil
}

// checkTenant refuses a tenant this module will not act on.
//
// The identifier is judged by the shape this module can judge: not empty, and
// free of blanks and control characters. The alias is judged by the slug form
// every request surface carries, which is also the username's form because a
// personal tenant's alias is its owner's username. The owner is optional here:
// an empty value is "no owner yet", which the creation and transfer seams are
// responsible for filling on a business organization, and a value that is
// present is held to the identifier's own domain. How long an identifier may be
// is the declaring party's to say, in the column its migration declares — this
// module defines no length and so cannot check one. The profile is judged
// against tenant.Profile.Validate, whose bounds and shapes this module states
// nowhere else: a row the table holds has to pass the same check a write does,
// because the table can be written without this module's write paths.
func checkTenant(t tenant.Tenant) error {
	if err := checkField("identifier", string(t.ID)); err != nil {
		return err
	}
	if err := checkAlias(t.Alias); err != nil {
		return err
	}
	if t.OwnerAccount != "" && !tenant.IsIdentifier(t.OwnerAccount) {
		return fmt.Errorf("%w: the owner account carries a blank or a control character",
			tenant.ErrInvalidTenant)
	}
	return t.Profile.Validate()
}

// checkWrite is checkTenant plus the two values a registration may not choose.
//
// A registration takes the whole row's values, but two of them are written by
// another route only: the deleted state is reached through Delete, and an
// account-caused disable only through SetStatusByAccount. The source is judged
// fail-closed — only the operator's value, which is the zero one, is accepted —
// so a value this build does not know cannot be stored through a registration
// and later read as though this build had chosen it. Both refusals run before
// any write, so a refused registration leaves the table as it found it.
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

// checkAlias refuses an alias outside the slug form the roster serves. The form
// is stated once by tenant.IsAlias; the empty case is judged first because its
// fix is the caller's to choose a name rather than to correct one.
func checkAlias(alias string) error {
	if alias == "" {
		return fmt.Errorf("%w: the alias is empty", tenant.ErrInvalidTenant)
	}
	if !tenant.IsAlias(alias) {
		return fmt.Errorf("%w: the alias is not a lower-case slug of 3 to 63 letters, digits and dashes",
			tenant.ErrInvalidTenant)
	}
	return nil
}

// checkNamespace holds an alias and an identifier to one namespace, for the
// creation paths, inside the write's transaction.
//
// A slug is either a tenant's alias or a tenant's identifier, never both: a
// request surface that carried one value would otherwise name two tenants, and
// the path guard would refuse that segment on every route that takes a tenant.
// The two lookups are separate because the columns are — alias is unique on its
// own and id is the primary key — and neither index spans the other column, so
// the pair's disjointness is this check's to enforce.
//
// The row this call is about is not in the table yet: Create has already found
// its identifier free, and Put reaches here only on its insert path. An alias
// another tenant carries as its identifier is reported as ErrAliasConflict, the
// alias being the value whose fix is the caller's; an identifier another tenant
// carries as its alias is reported as ErrTenantExists, for the same reason. The
// message names the holder and never a value the caller has not already
// supplied.
func checkNamespace(tx *gorm.DB, t tenant.Tenant) error {
	held, err := lookUp(tx, "alias", t.Alias)
	if err != nil {
		return err
	}
	if len(held) > 0 {
		return fmt.Errorf("%w: alias %q is held by tenant %q",
			tenant.ErrAliasConflict, t.Alias, held[0].ID)
	}
	held, err = lookUp(tx, "id", t.Alias)
	if err != nil {
		return err
	}
	if len(held) > 0 {
		return fmt.Errorf("%w: alias %q is the identifier of tenant %q",
			tenant.ErrAliasConflict, t.Alias, held[0].ID)
	}
	held, err = lookUp(tx, "alias", string(t.ID))
	if err != nil {
		return err
	}
	if len(held) > 0 {
		return fmt.Errorf("%w: identifier %q is the alias of tenant %q",
			tenant.ErrTenantExists, t.ID, held[0].ID)
	}
	return nil
}

// checkField refuses an identifier that is empty or carries a character it
// could not survive.
//
// Blanks and control characters are refused anywhere in it. An identifier ends
// up in log lines, diagnostics and error text, where a newline splits one
// record into two.
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
