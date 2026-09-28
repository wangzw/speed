package journal

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"os"
	"strconv"
	"time"

	"gorm.io/gorm"

	"github.com/vislake/speed/pkg/audit"
	"github.com/vislake/speed/pkg/db"
)

// captureCallbackName identifies this plugin's callbacks in GORM's own
// registry, which is per processor.
const captureCallbackName = "audit:capture"

// commitStep is the step GORM ends a statement's transaction at, and the anchor
// this plugin hangs its callbacks on.
//
// Anchoring on the statement's own step is not enough, and the difference is
// the whole of what this path promises. GORM registers that step without an
// anchor of its own, and its sort appends anything anchored *after* a step that
// is already ordered to the end of the chain — so a callback anchored after
// `gorm:create` lands after the commit, once the connection has been handed
// back to the pool. A row written there is written on a connection of its own
// again, after the fact: the record and the write stop being one unit of work,
// and a failure is reported against a statement that has already committed.
// Anchored before this step, the callback runs with the transaction still open,
// which is the only place the promise holds.
//
// Only the model-path processors are registered on. `Exec` and `Raw` are not,
// and leaving them out is what makes the self-reference judgement a comparison
// rather than a guard: a statement this plugin never sees cannot recurse
// through it. The price is stated as a non-guarantee — a host's own cleanup and
// a migration file issue raw SQL and reach no audit row.
const commitStep = "gorm:commit_or_rollback_transaction"

// capture is the plugin the descriptor declares.
//
// It holds nothing at all. The instance is frozen with the descriptor at init
// time, and the database module hands that one instance to every handle it
// builds, so a process driving two commands builds two registries that share
// it; anything kept on it would be the other run's value on the second one. The
// callbacks need nothing from it either — they take the session they are handed
// — which is why there is no per-handle state here to get wrong.
type capture struct{}

// newCapture builds the plugin the descriptor declares.
func newCapture() *capture { return &capture{} }

// Name identifies this plugin in GORM's registry of plugins, which is what the
// database module reports when two declarations collide. The convention there
// is the package path.
func (c *capture) Name() string { return "github.com/vislake/speed/pkg/audit/journal" }

// Initialize registers the capture callbacks on one handle.
//
// The handle is used to register and then dropped. GORM calls this once per
// handle, and what each callback works on is the session it is handed when it
// runs, so no handle has to be remembered. Where a row for a statement on a
// connection built with Open belongs is asked of that session when the
// statement runs; see captureOne.
//
// The anchor is the step the transaction ends at, so the callback runs after
// the statement's own work and before the commit. The tenant filter registers
// on the same processors ahead of their own steps, so the two orders are
// decided by the anchors and not by which plugin was installed first.
func (c *capture) Initialize(handle *gorm.DB) error {
	registered := []func(*gorm.DB) error{
		func(h *gorm.DB) error {
			return h.Callback().Create().Before(commitStep).
				Register(captureCallbackName, captureOne("create"))
		},
		func(h *gorm.DB) error {
			return h.Callback().Update().Before(commitStep).
				Register(captureCallbackName, captureOne("update"))
		},
		func(h *gorm.DB) error {
			return h.Callback().Delete().Before(commitStep).
				Register(captureCallbackName, captureOne("delete"))
		},
	}
	for _, register := range registered {
		if err := register(handle); err != nil {
			return err
		}
	}
	return nil
}

// captureOne builds the callback for one processor.
//
// The row is written on the session the statement ran on, which is what makes
// this path different from the three the module drives. The two consequences
// are the reason for it. The record stands or falls with the write it records —
// a transaction the caller opened and later rolled back takes the row with it,
// and no row is left saying a write happened that did not. And there is only
// ever one connection in play, so an engine that admits a single writer — the
// one this repository ships migrations for — has nothing to contend over.
//
// The price is that a failure here is raised rather than reported: the
// statement fails, because on this connection the record and the write are one
// unit of work. That is the one path in the module where that is true.
//
// A connection the database module built with Open is the exception. It carries
// this plugin but no migration, so the database it reaches usually has no audit
// table, and one process keeps one audit table: the row goes to the delivered
// database's, through the delivered handle the database module says the
// connection was opened from. It is still written before the statement commits
// and still fails the statement when it cannot be written, so a write that left
// no record does not happen there either; what it loses is the shared unit of
// work, because two databases have no transaction in common, and a statement
// rolled back after its row was written leaves the row behind. The plugin
// instance is shared by every registry in the process and holds nothing, so the
// answer has to come from the session: a guess made here — the only journal
// running, or whichever handle has the table — would hand one run's rows to
// another's table. How long that row may wait is bounded; see recordOpened. A
// connection built with Open onto the delivered database itself is no
// exception: its statement's session reaches the delivered table, and the row
// is written there; see reachesDeliveredTable.
//
// A statement that has already failed is left alone. GORM runs every callback
// of a processor whatever the ones before it did, so this one is reached by a
// statement another plugin refused before it ran, by one GORM itself refused,
// and by one the engine turned down — and none of those changed anything, so
// there is no write to record. The row could not stand anyway: the transaction
// underneath is on its way to a rollback that takes it along, and an engine that
// refuses every further statement in a transaction one has failed in would not
// take it at all. Attempting it is worse than pointless, because the session
// the row goes out on carries the statement's error: the insert fails with that
// same error, and raising it would hand the caller its own failure dressed up as
// an audit store that could not be written to.
func captureOne(action string) func(*gorm.DB) {
	return func(tx *gorm.DB) {
		if tx.Error != nil {
			return
		}
		st := tx.Statement
		if st == nil {
			return
		}
		table := targetTable(st)
		if table == "" || table == tableName {
			return
		}
		entry := audit.Entry{
			Action:  action,
			Object:  table,
			Outcome: statementOutcome,
			Detail: map[string]string{
				"rows_affected": strconv.FormatInt(tx.RowsAffected, 10),
			},
		}
		if err := record(tx, assemble(st.Context, audit.SourceStatement, entry, "")); err != nil {
			// The write that produced this row is not going to be reported any
			// other way: there is no caller to return to and no signal this
			// path reports through, because the row and the statement are one
			// unit. Raising it on the session is what fails the statement.
			_ = tx.AddError(err)
		}
	}
}

// record writes a statement's row.
//
// On the delivered connection it goes out on the statement's own session; see
// onOwnSession. On a connection built with Open it goes out through the
// delivered handle, unless that connection reaches the delivered table itself;
// see recordOpened.
func record(tx *gorm.DB, r row) error {
	if delivered, opened := db.OpenedFrom(tx); opened {
		return recordOpened(tx, delivered, r)
	}
	return onOwnSession(tx, r)
}

// onOwnSession writes a statement's row on a session of its own on the
// statement's connection: NewDB clears the conditions the statement being
// recorded left on it, and the transaction underneath — if there is one — is not
// one of them. Nothing is waited for there, because the row takes nothing the
// statement does not already hold.
func onOwnSession(tx *gorm.DB, r row) error {
	return insert(tx.Session(&gorm.Session{NewDB: true, Context: tx.Statement.Context}), r)
}

// recordOpened writes the row of a statement on a connection built with Open,
// under the statement's context, which is where the actor and the tenant are
// read from.
//
// A connection opened onto the delivered database itself reaches the delivered
// table on its own session, and the row is written there, as it is for a
// statement on the delivered connection: in the statement's own transaction,
// waiting for nothing. Sent through the delivered handle instead, it would wait
// for the write lock the statement itself holds on an engine with one writer,
// and on the other engine it would leave its row behind a statement rolled back
// on a database the two share. Which connection that is, is read from the two
// databases rather than from their locators; see reachesDeliveredTable.
//
// Every other row goes out through the delivered handle. It needs a connection
// of the delivered pool and, on an engine with one writer, the delivered
// database's write lock, while the statement it records holds its own
// transaction open on the other database. A caller inside a transaction on the
// delivered handle holds both until after the statement returns. Nothing a
// statement can see tells that from another writer's brief hold, so the wait is
// bounded rather than recognised: the connection is taken within openedRowWait,
// and the lock is waited for only as long as what remains of it. Past the bound
// the statement fails with ErrWriteFailed naming the wait — a record the
// statement could not have without waiting on the caller is a record it does not
// get, and the write it would record does not happen. The connection that tells
// which of the two ways the row goes is taken within the same bound.
//
// The insert itself runs under the statement's context, not the bound's: the
// bound is on waiting for what another holds, and cutting a write short once it
// is under way would leave it to the engine whether the row had been kept.
//
// The session carries the statement's own logger. The row is part of the
// statement's work, so a caller that gave its statement a logger of its own
// gets the record's failure reported the way the statement's is.
func recordOpened(tx, delivered *gorm.DB, r row) error {
	ctx := tx.Statement.Context
	bounded, cancel := context.WithTimeout(ctx, openedRowWait)
	defer cancel()
	failed := func(err error) error {
		if errors.Is(bounded.Err(), context.DeadlineExceeded) && ctx.Err() == nil {
			return fmt.Errorf("%w: the row of a statement on a connection built with Open was not "+
				"written to the delivered database within %v, and the statement waits no longer: a "+
				"transaction the caller holds on the delivered handle keeps what the row needs until "+
				"after the statement returns: %w", audit.ErrWriteFailed, openedRowWait, err)
		}
		return fmt.Errorf("%w: %w", audit.ErrWriteFailed, err)
	}

	pool, err := delivered.DB()
	if err != nil {
		return fmt.Errorf("%w: the delivered handle has no connection pool: %w", audit.ErrWriteFailed, err)
	}
	conn, err := pool.Conn(bounded)
	if err != nil {
		return failed(err)
	}
	same, err := reachesDeliveredTable(ctx, bounded, tx, conn)
	if err != nil {
		_ = conn.Close()
		return fmt.Errorf("%w: telling whether a connection built with Open reaches the delivered "+
			"database's audit table: %w", audit.ErrWriteFailed, err)
	}
	if same {
		_ = conn.Close()
		return onOwnSession(tx, r)
	}
	release, err := boundLockWait(delivered, conn, bounded)
	defer release()
	if err != nil {
		return fmt.Errorf("%w: %w", audit.ErrWriteFailed, err)
	}
	session := delivered.Session(&gorm.Session{NewDB: true, Context: ctx, Logger: tx.Logger})
	session.Statement.ConnPool = conn
	if err := create(session, r); err != nil {
		return failed(err)
	}
	return nil
}

// reachesDeliveredTable reports whether the statement's own session reaches the
// audit table a row written through the delivered connection would land in.
//
// The two databases are asked, not their locators. A locator is the database
// module's to read, and one database answers to many of them — a relative path
// and an absolute one, a link, a host name and its address — while each engine
// can say which database a connection is on: SQLite by the file it keeps its
// main database in, PostgreSQL by the server run, the database and the relation
// the table's name resolves to on that connection. The statement's side is asked
// on the statement's own connection, which it already holds; the delivered side
// on a connection of the delivered pool, which is only ever read.
//
// A database with no file behind it — SQLite's in-memory and temporary ones —
// is never taken for the delivered one. A private one belongs to its own
// connection and cannot be; one in a shared cache has no name the engine
// reports, so it is not recognised. On an engine this plugin knows no question
// for, the answer is no, which sends the row through the delivered handle.
func reachesDeliveredTable(ctx, bounded context.Context, tx *gorm.DB, delivered *sql.Conn) (bool, error) {
	own := tx.Statement.ConnPool
	switch tx.Name() {
	case sqliteDialector:
		return sameSQLiteFile(ctx, bounded, own, delivered)
	case postgresDialector:
		return samePostgresTable(ctx, bounded, own, delivered)
	default:
		return false, nil
	}
}

// sqliteMainFile is the file SQLite keeps a connection's main database in, as
// the engine resolved it, links followed; it is empty for a database with no
// file behind it.
const sqliteMainFile = "SELECT file FROM pragma_database_list WHERE name = 'main'"

// sameSQLiteFile reports whether two SQLite connections keep their main
// database in one file. The engine's own path decides, links resolved, and a
// second name of one file the engine does not resolve to the same path — a hard
// link — is recognised by the file system.
func sameSQLiteFile(ctx, bounded context.Context, own gorm.ConnPool, delivered *sql.Conn) (bool, error) {
	var ownFile, deliveredFile string
	if err := own.QueryRowContext(ctx, sqliteMainFile).Scan(&ownFile); err != nil {
		return false, fmt.Errorf("reading the statement's database file: %w", err)
	}
	if ownFile == "" {
		return false, nil
	}
	if err := delivered.QueryRowContext(bounded, sqliteMainFile).Scan(&deliveredFile); err != nil {
		return false, fmt.Errorf("reading the delivered database file: %w", err)
	}
	if deliveredFile == "" {
		return false, nil
	}
	if ownFile == deliveredFile {
		return true, nil
	}
	ownInfo, ownErr := os.Stat(ownFile)
	deliveredInfo, deliveredErr := os.Stat(deliveredFile)
	return ownErr == nil && deliveredErr == nil && os.SameFile(ownInfo, deliveredInfo), nil
}

// postgresDialector is the name the PostgreSQL engine's GORM dialector answers.
const postgresDialector = "postgres"

// postgresTable names, on one connection, the server run, the database and the
// relation the audit table's name resolves to there, quoted the way GORM writes
// it. The relation is null where the name resolves to nothing.
var postgresTable = fmt.Sprintf(
	`SELECT pg_postmaster_start_time(), current_database(), to_regclass('"%s"')::oid::int8`, tableName)

// samePostgresTable reports whether two PostgreSQL connections resolve the
// audit table's name to one relation of one database on one server run.
//
// The relation is what a row goes into, and it is compared rather than the
// database alone because a connection's search path decides which schema's
// table the name reaches. The server run is when the server started, which two
// servers holding copies of one database — a restored backup, a promoted
// standby — do not share.
func samePostgresTable(ctx, bounded context.Context, own gorm.ConnPool, delivered *sql.Conn) (bool, error) {
	var ownStarted, deliveredStarted time.Time
	var ownDatabase, deliveredDatabase string
	var ownRelation, deliveredRelation sql.NullInt64
	if err := own.QueryRowContext(ctx, postgresTable).Scan(&ownStarted, &ownDatabase, &ownRelation); err != nil {
		return false, fmt.Errorf("reading which audit table the statement's connection reaches: %w", err)
	}
	if !ownRelation.Valid {
		return false, nil
	}
	if err := delivered.QueryRowContext(bounded, postgresTable).
		Scan(&deliveredStarted, &deliveredDatabase, &deliveredRelation); err != nil {
		return false, fmt.Errorf("reading which audit table the delivered connection reaches: %w", err)
	}
	return ownStarted.Equal(deliveredStarted) && ownDatabase == deliveredDatabase &&
		ownRelation == deliveredRelation, nil
}

// openedRowWait bounds the wait of a row for a statement on a connection built
// with Open: for a connection of the delivered pool, and for the delivered
// database's write lock.
//
// An insert into the audit table that the delivered database takes at all takes
// it in milliseconds. Waiting longer only helps when another writer holds the
// delivered database for longer than this, and every second of it keeps the
// statement's own transaction open on the other database; inside a transaction
// on the delivered handle no amount of waiting ends it, because what the row
// waits for is the caller's.
const openedRowWait = time.Second

// lockWaitMargin is how far past the bound the lock wait runs, so that a write
// refused for the lock is refused once the bound has passed and is reported as
// the wait it was.
const lockWaitMargin = 50 * time.Millisecond

// sqliteDialector is the name the SQLite engine's GORM dialector answers, which
// is how a plugin holding nothing but a handle tells that engine apart.
const sqliteDialector = "sqlite"

// boundLockWait holds the wait for the delivered database's write lock on conn
// to what remains of the bound, where the engine makes one connection's write
// wait for another's, and returns what hands the connection back to the pool as
// it was taken.
//
// SQLite waits for the lock inside the driver, for the busy timeout the
// connection carries, and a context that ends meanwhile does not cut that wait
// short; the connection's own busy timeout is therefore the only bound there
// is, and it is set for this write and put back after it. A connection whose
// busy timeout cannot be put back is discarded rather than handed on with this
// write's bound. An engine without a database-wide write lock needs none of
// this: its insert into the audit table waits for no other connection.
func boundLockWait(delivered *gorm.DB, conn *sql.Conn, bounded context.Context) (func(), error) {
	handBack := func() { _ = conn.Close() }
	// Name is the dialector's, reached through the handle's configuration.
	if delivered.Name() != sqliteDialector {
		return handBack, nil
	}
	settle := context.WithoutCancel(bounded)
	var taken int64
	if err := conn.QueryRowContext(settle, "PRAGMA busy_timeout").Scan(&taken); err != nil {
		return handBack, fmt.Errorf("reading the busy timeout of a connection of the delivered database: %w", err)
	}
	remaining := lockWaitMargin
	if deadline, ok := bounded.Deadline(); ok {
		remaining += max(time.Until(deadline), 0)
	}
	if _, err := conn.ExecContext(settle, fmt.Sprintf("PRAGMA busy_timeout = %d", remaining.Milliseconds())); err != nil {
		return handBack, fmt.Errorf("bounding the lock wait of a connection of the delivered database: %w", err)
	}
	return func() {
		if _, err := conn.ExecContext(settle, fmt.Sprintf("PRAGMA busy_timeout = %d", taken)); err != nil {
			_ = conn.Raw(func(any) error { return driver.ErrBadConn })
		}
		_ = conn.Close()
	}, nil
}

// targetTable is the table a statement writes to.
//
// Statement.Table is what the model path sets; the schema's own name is the
// fallback for a statement that reached this point without one. The comparison
// against tableName is what keeps this plugin from recording its own writes,
// and it holds because the write goes through the same model path — a write
// issued as raw SQL would carry no table and would not reach here at all.
func targetTable(st *gorm.Statement) string {
	if st.Table != "" {
		return st.Table
	}
	if st.Schema != nil {
		return st.Schema.Table
	}
	return ""
}

// statementOutcome is what the result column holds on every statement row. A
// statement that failed leaves no row, so the one that does has succeeded; how
// much it changed is the detail's rows_affected.
const statementOutcome = "ok"
