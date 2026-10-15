package db

import (
	"fmt"
	"strings"

	"github.com/vislake/speed/pkg/db"
)

// tableName is the table the migrations build and the loop's statements
// address. It is one constant in three places: a change here is a change to
// both migration files too.
const tableName = "jobs"

// statements is the SQL one dialect runs, written once for each engine the
// database capability delivers.
//
// Every statement here is raw SQL rather than a model statement, and that is
// load-bearing. The loop reads and writes rows of every tenant at once, so the
// tenant filter has nothing to add to it and would only refuse it; and the
// clock, the ordering and the lock are what the dialect differs in, which a
// model statement cannot express. Raw SQL reaches neither the filter nor the
// audit capture, which is what a cross-tenant table wants.
//
// Every instant is milliseconds since the Unix epoch, read from the database's
// own clock, so that the executors of several replicas judge every claim
// against one clock. A claim judged against each replica's own clock would be
// taken over early by a replica whose clock runs ahead.
type statements struct {
	// now is the engine's clock, in milliseconds since the Unix epoch.
	now string
	// dueNext answers how many milliseconds remain until the earliest
	// non-terminal row may be claimed: negative or zero when one is due now,
	// and NULL when the table holds none.
	dueNext string
	// claim takes the due rows, up to a limit, for one executor until its
	// claim runs out, and returns them in the order the sort keys give.
	// Arguments: executor token, claim length in milliseconds (twice, for the
	// instant the row falls due again and the claim's expiry), the tenant the
	// rotation starts after, limit.
	claim string
	// cancelCheck reports whether a row the executor still holds has had a
	// cancellation requested. Arguments: row identifier, executor token.
	cancelCheck string
	// progress records the last progress the handler reported on a row the
	// executor still holds. Arguments: percentage, message, row identifier,
	// executor token.
	progress string
	// succeed settles a row the executor holds into succeeded. Arguments:
	// result bytes, attempts, row identifier, executor token.
	succeed string
	// fail settles a row the executor holds back into retrying, due after a
	// delay. Arguments: error text, attempts, delay in milliseconds, row
	// identifier, executor token.
	fail string
	// deadLetter settles a row the executor holds into dead_letter.
	// Arguments: error text, attempts, row identifier, executor token.
	deadLetter string
	// cancelled settles a row the executor holds into cancelled, after the
	// executor observed a cancellation request. Arguments: row identifier,
	// executor token.
	cancelled string
	// release gives back every row one executor still holds, unexecuted and
	// due at once, returning each to the state it is to be claimed from
	// again. Argument: executor token.
	release string
	// pendingPublish lists the terminal rows whose terminal event has not been
	// published: the rows that owe one. Argument: limit.
	pendingPublish string
	// markPublished records that a terminal row's event has been published.
	// Argument: row identifier.
	markPublished string
}

// statementsFor returns the statements of a dialect, or an error for a dialect
// no statements are written for.
func statementsFor(dialect db.Dialect) (statements, error) {
	switch dialect {
	case db.Postgres:
		// A claim locks the rows it takes and skips the rows another claim is
		// locking. Without the lock two claims read the same row as due and
		// both take it, and its job runs twice; waiting on the lock instead of
		// skipping would serialise every executor behind the slowest.
		return build(`CAST(EXTRACT(EPOCH FROM clock_timestamp()) * 1000 AS BIGINT)`,
			" FOR UPDATE SKIP LOCKED"), nil
	case db.SQLite:
		// SQLite runs one writer at a time, so a claim is atomic without a lock
		// of its own, and 'now' reads the same instant throughout a statement.
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
		dueNext: r.Replace(`SELECT MIN(available_at) - {now} FROM {table} ` +
			`WHERE status IN ('pending', 'retrying', 'running')`),
		// The rotation key after available_at is what keeps one tenant's due
		// rows from leading every batch: the tenant the claim starts after is
		// carried from one claim to the next, so the next batch begins at the
		// tenant after the last one served rather than at the first tenant
		// every time. A row whose claim has run out is due again through
		// available_at, so a leftover running row is taken over by the same
		// predicate.
		// The claim moves available_at to the claim's expiry beside
		// claim_expires_at, and the two are the same instant: a row is due
		// again exactly when its claim runs out, which is what hands a row an
		// executor died holding to another executor. Leaving available_at
		// where it was would leave the row due, and the next claim would take
		// it again while the first executor is still running it.
		claim: r.Replace(`UPDATE {table} SET claimed_by = ?, available_at = {now} + ?, claim_expires_at = {now} + ?, ` +
			`status = 'running', started_at = {now}, updated_at = {now} ` +
			`WHERE id IN (SELECT id FROM {table} ` +
			`WHERE available_at <= {now} AND status IN ('pending', 'retrying', 'running') ` +
			`ORDER BY priority DESC, available_at ASC, ` +
			`CASE WHEN tenant_id > ? THEN 0 ELSE 1 END, tenant_id LIMIT ?{lock}) ` +
			`RETURNING id, tenant_id, type, priority, payload, attempts, max_retries, timeout_ms, ` +
			`scheduled_at, started_at, created_at, updated_at, progress_pct, progress_msg`),
		cancelCheck: r.Replace(`SELECT cancel_requested FROM {table} WHERE id = ? AND claimed_by = ?`),
		progress: r.Replace(`UPDATE {table} SET progress_pct = ?, progress_msg = ?, updated_at = {now} ` +
			`WHERE id = ? AND claimed_by = ?`),
		succeed: r.Replace(`UPDATE {table} SET status = 'succeeded', result = ?, error = NULL, attempts = ?, ` +
			`completed_at = {now}, claimed_by = NULL, claim_expires_at = NULL, updated_at = {now} ` +
			`WHERE id = ? AND claimed_by = ?`),
		fail: r.Replace(`UPDATE {table} SET status = 'retrying', error = ?, attempts = ?, ` +
			`available_at = {now} + ?, claimed_by = NULL, claim_expires_at = NULL, updated_at = {now} ` +
			`WHERE id = ? AND claimed_by = ?`),
		deadLetter: r.Replace(`UPDATE {table} SET status = 'dead_letter', error = ?, attempts = ?, ` +
			`completed_at = {now}, available_at = {now}, claimed_by = NULL, claim_expires_at = NULL, ` +
			`updated_at = {now} WHERE id = ? AND claimed_by = ?`),
		cancelled: r.Replace(`UPDATE {table} SET status = 'cancelled', completed_at = {now}, ` +
			`available_at = {now}, claimed_by = NULL, claim_expires_at = NULL, updated_at = {now} ` +
			`WHERE id = ? AND claimed_by = ?`),
		release: r.Replace(`UPDATE {table} SET claimed_by = NULL, claim_expires_at = NULL, ` +
			`available_at = {now}, updated_at = {now}, ` +
			`status = CASE WHEN attempts > 0 THEN 'retrying' ELSE 'pending' END ` +
			`WHERE claimed_by = ? AND status = 'running'`),
		pendingPublish: r.Replace(`SELECT id, tenant_id, type, status FROM {table} ` +
			`WHERE status IN ('succeeded', 'dead_letter', 'cancelled') AND terminal_published_at IS NULL ` +
			`ORDER BY completed_at ASC LIMIT ?`),
		markPublished: r.Replace(`UPDATE {table} SET terminal_published_at = {now} ` +
			`WHERE id = ? AND terminal_published_at IS NULL`),
	}
}
