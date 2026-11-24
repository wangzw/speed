package journal

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"strconv"
	"time"

	"gorm.io/gorm"

	"github.com/vislake/speed/pkg/audit"
	"github.com/vislake/speed/pkg/db"
)

const captureCallbackName = "audit:capture"

// commitStep is where GORM ends a statement's transaction; anchor before it,
// since a callback anchored after a step is appended to the chain's end and
// would run after commit, when the connection is back in the pool.
const commitStep = "gorm:commit_or_rollback_transaction"

type capture struct{}

func newCapture() *capture { return &capture{} }

// Name identifies this plugin in GORM's registry of plugins, which is what the
// database module reports when two declarations collide. The convention there
// is the package path.
func (c *capture) Name() string { return "github.com/vislake/speed/pkg/audit/journal" }

// Initialize hooks only the model-path processors: Exec and Raw never enter
// the callbacks, and targetTable's table-name check keeps a model write from
// recording itself.
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
			// The write that produced this row is not going to be reported any other
			// way: there is no caller to return to and no signal this path reports
			// through, because the row and the statement are one unit.
			_ = tx.AddError(err)
		}
	}
}

func record(tx *gorm.DB, r row) error {
	if delivered, opened := db.OpenedFrom(tx); opened {
		return recordOpened(tx, delivered, r)
	}
	return onOwnSession(tx, r)
}

// onOwnSession writes a statement's row on a session of its own on the statement's
// connection; NewDB clears the statement's conditions but keeps its transaction.
func onOwnSession(tx *gorm.DB, r row) error {
	return insert(tx.Session(&gorm.Session{NewDB: true, Context: tx.Statement.Context}), r)
}

// recordOpened writes the row of a statement on a connection built with
// Open, under the statement's context, which is where the actor and the
// tenant are read from.
func recordOpened(tx, delivered *gorm.DB, r row) error {
	ctx := tx.Statement.Context
	var ran, same, told bool
	err := db.Connection(ctx, delivered, openedRowWait, func(session *gorm.DB) error {
		ran = true
		var err error
		if same, err = reachesDeliveredTable(ctx, tx, session.Statement.ConnPool); err != nil {
			return err
		}
		told = true
		if same {
			return nil
		}
		return create(session.Session(&gorm.Session{Logger: tx.Logger}), r)
	})
	switch {
	case err == nil && same:
		return onOwnSession(tx, r)
	case err == nil:
		return nil
	case !ran && ctx.Err() == nil && errors.Is(err, context.DeadlineExceeded):
		return fmt.Errorf("%w: no connection of the delivered database came free within %v for the row of "+
			"a statement on a connection built with Open, and the statement waits no longer: a "+
			"transaction the caller holds on the delivered handle keeps the one connection a database "+
			"held to one has until after the statement returns: %w", audit.ErrWriteFailed, openedRowWait, err)
	case !told:
		return fmt.Errorf("%w: telling whether a connection built with Open reaches the delivered "+
			"database's audit table: %w", audit.ErrWriteFailed, err)
	default:
		return fmt.Errorf("%w: %w", audit.ErrWriteFailed, err)
	}
}

// reachesDeliveredTable reports whether the statement's own session
// reaches the audit table a row written through the delivered connection
// would land in.
func reachesDeliveredTable(ctx context.Context, tx *gorm.DB, delivered gorm.ConnPool) (bool, error) {
	own := tx.Statement.ConnPool
	switch tx.Name() {
	case sqliteDialector:
		return sameSQLiteFile(ctx, own, delivered)
	case postgresDialector:
		return samePostgresTable(ctx, own, delivered)
	default:
		return false, nil
	}
}

const sqliteDialector = "sqlite"

func sqliteMainFile(ctx context.Context, conn gorm.ConnPool) (string, error) {
	rows, err := conn.QueryContext(ctx, "PRAGMA database_list")
	if err != nil {
		return "", err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var seq int64
		var name string
		var file sql.NullString
		if err := rows.Scan(&seq, &name, &file); err != nil {
			return "", err
		}
		if name == "main" {
			return file.String, rows.Err()
		}
	}
	if err := rows.Err(); err != nil {
		return "", err
	}
	return "", errors.New("the connection lists no main database")
}

func sameSQLiteFile(ctx context.Context, own, delivered gorm.ConnPool) (bool, error) {
	ownFile, err := sqliteMainFile(ctx, own)
	if err != nil {
		return false, fmt.Errorf("reading the statement's database file: %w", err)
	}
	if ownFile == "" {
		return false, nil
	}
	deliveredFile, err := sqliteMainFile(ctx, delivered)
	if err != nil {
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

const postgresDialector = "postgres"

var postgresTable = fmt.Sprintf(
	`SELECT pg_postmaster_start_time(), current_database(), to_regclass('"%s"')::oid::int8`, tableName)

func samePostgresTable(ctx context.Context, own, delivered gorm.ConnPool) (bool, error) {
	var ownStarted, deliveredStarted time.Time
	var ownDatabase, deliveredDatabase string
	var ownRelation, deliveredRelation sql.NullInt64
	if err := own.QueryRowContext(ctx, postgresTable).Scan(&ownStarted, &ownDatabase, &ownRelation); err != nil {
		return false, fmt.Errorf("reading which audit table the statement's connection reaches: %w", err)
	}
	if !ownRelation.Valid {
		return false, nil
	}
	if err := delivered.QueryRowContext(ctx, postgresTable).
		Scan(&deliveredStarted, &deliveredDatabase, &deliveredRelation); err != nil {
		return false, fmt.Errorf("reading which audit table the delivered connection reaches: %w", err)
	}
	return ownStarted.Equal(deliveredStarted) && ownDatabase == deliveredDatabase &&
		ownRelation == deliveredRelation, nil
}

const openedRowWait = time.Second

func targetTable(st *gorm.Statement) string {
	if st.Table != "" {
		return st.Table
	}
	if st.Schema != nil {
		return st.Schema.Table
	}
	return ""
}

const statementOutcome = "ok"
