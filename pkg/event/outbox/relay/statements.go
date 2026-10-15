package relay

import (
	"fmt"
	"strings"

	"github.com/vislake/speed/pkg/db"
)

// tableName is the table the migrations build and every statement here
// addresses. It is one constant in two places: a change here is a change to
// every migration file of both dialects too.
const tableName = "event_staged"

// statements is the SQL one dialect runs, written once for each engine the
// database capability delivers.
//
// Every statement is raw SQL rather than a model statement, and that is
// load-bearing. The plugins the database module installs act on the model path:
// the audit capture would write a row for every staging, claim and removal, and
// a claim that failed there would stop the relay; the tenant filter has no
// business with a table that holds every tenant's events. Raw SQL reaches
// neither, and the table is this module's alone.
//
// Times are the database's own clock, in milliseconds since the Unix epoch, so
// the relays of several replicas judge every claim against one clock; a claim
// judged against each replica's own clock would be taken over early by a
// replica whose clock runs ahead.
type statements struct {
	// now is the engine's clock, in milliseconds since the Unix epoch.
	now string
	// stage writes one row, due at once. Arguments: event identifier, type,
	// tenant, payload, tracing context.
	stage string
	// nextDue answers how many milliseconds remain until the earliest row falls
	// due, negative or zero when one is due now, and NULL when none is staged.
	nextDue string
	// claim takes the due rows, up to a limit, for one relay until its claim
	// runs out, and returns them. Arguments: relay token, claim length in
	// milliseconds, limit.
	claim string
	// remove deletes a row the bus accepted. Argument: row identifier.
	remove string
	// refuse gives a row the bus refused back, due after a delay, with the
	// refusal recorded. Arguments: error text, delay in milliseconds, row
	// identifier, relay token.
	refuse string
	// release gives every row one relay still holds back, due at once.
	// Argument: relay token.
	release string
}

// statementsFor returns the statements of a dialect, or an error for a dialect
// no statements are written for.
func statementsFor(dialect db.Dialect) (statements, error) {
	switch dialect {
	case db.Postgres:
		// A claim locks the rows it takes and skips the rows another claim is
		// locking. Without the lock, two claims running at once both read a
		// row as due and both take it — the outer update waits for the other
		// claim's lock and then re-checks only its own condition, which the
		// row still meets — so two relays publish it; waiting on the lock
		// instead of skipping would serialise every relay behind the slowest.
		return build(`CAST(EXTRACT(EPOCH FROM clock_timestamp()) * 1000 AS BIGINT)`,
			" FOR UPDATE SKIP LOCKED"), nil
	case db.SQLite:
		// SQLite runs one writer at a time, so a claim is atomic without a
		// lock of its own. 'now' reads the same instant throughout a
		// statement.
		return build(`CAST((julianday('now') - 2440587.5) * 86400000 AS INTEGER)`, ""), nil
	default:
		return statements{}, fmt.Errorf("no statements are written for the %q dialect; this module "+
			"supports the dialects the database capability delivers, %s and %s", dialect, db.Postgres, db.SQLite)
	}
}

// build writes the statements around an engine's clock expression and the
// clause, if any, that locks the rows a claim takes.
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
