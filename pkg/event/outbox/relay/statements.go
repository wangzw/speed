package relay

import (
	"fmt"
	"strings"

	"github.com/vislake/speed/pkg/db"
)

const tableName = "event_staged"

// statements is the SQL one dialect runs, written once for each engine
// the database capability delivers.
type statements struct {
	now string
	// stage writes one row, due at once. Arguments: event identifier, type,
	// tenant, payload, tracing context.
	stage   string
	nextDue string
	claim   string
	remove  string
	// refuse gives a row the bus refused back, due after a delay, with the
	// refusal recorded. Arguments: error text, delay in milliseconds, row
	// identifier, relay token.
	refuse string
	// release gives every row one relay still holds back, due at once.
	// Argument: relay token.
	release string
}

func statementsFor(dialect db.Dialect) (statements, error) {
	switch dialect {
	case db.Postgres:
		// A claim locks the rows it takes and skips the rows another claim is
		// locking.
		return build(`CAST(EXTRACT(EPOCH FROM clock_timestamp()) * 1000 AS BIGINT)`,
			" FOR UPDATE SKIP LOCKED"), nil
	case db.SQLite:
		// SQLite runs one writer at a time, so a claim needs no row lock of its
		// own; 'now' reads one instant throughout the statement.
		return build(`CAST((julianday('now') - 2440587.5) * 86400000 AS INTEGER)`, ""), nil
	default:
		return statements{}, fmt.Errorf("no statements are written for the %q dialect; this module "+
			"supports the dialects the database capability delivers, %s and %s", dialect, db.Postgres, db.SQLite)
	}
}

func build(now, lock string) statements {
	r := strings.NewReplacer("{table}", tableName, "{now}", now, "{lock}", lock)
	return statements{
		now: now,
		stage: r.Replace(`INSERT INTO {table} (event_id, type, tenant_id, payload, trace_context, ` +
			`staged_at, available_at) VALUES (?, ?, ?, ?, ?, {now}, {now})`),
		nextDue: r.Replace(`SELECT MIN(available_at) - {now} FROM {table}`),
		claim: r.Replace(`UPDATE {table} SET claimed_by = ?, available_at = {now} + ? ` +
			`WHERE id IN (SELECT id FROM {table} WHERE available_at <= {now} ORDER BY id LIMIT ?{lock}) ` +
			`RETURNING id, event_id, type, tenant_id, payload, trace_context, attempts`),
		remove: r.Replace(`DELETE FROM {table} WHERE id = ?`),
		refuse: r.Replace(`UPDATE {table} SET claimed_by = NULL, attempts = attempts + 1, last_error = ?, ` +
			`available_at = {now} + ? WHERE id = ? AND claimed_by = ?`),
		release: r.Replace(`UPDATE {table} SET claimed_by = NULL, available_at = {now} WHERE claimed_by = ?`),
	}
}
