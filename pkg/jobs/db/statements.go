package db

import (
	"fmt"
	"strings"

	"github.com/vislake/speed/pkg/db"
)

// tableName is the table the migrations build and the loop's statements address;
// a change here is a change to both migration files too.
const tableName = "jobs"

// statements is the SQL one dialect runs. Every statement is raw SQL so it
// bypasses the tenant filter and the audit capture, which a cross-tenant table
// wants; every instant is epoch milliseconds read from the database's own clock.
type statements struct {
	now            string
	dueNext        string
	claim          string
	cancelCheck    string
	progress       string
	succeed        string
	fail           string
	deadLetter     string
	cancelled      string
	release        string
	pendingPublish string
	markPublished  string
}

func statementsFor(dialect db.Dialect) (statements, error) {
	switch dialect {
	case db.Postgres:
		// A claim locks the rows it takes and skips rows another claim is
		// locking; without it two claims both take a row and its job runs twice.
		return build(`CAST(EXTRACT(EPOCH FROM clock_timestamp()) * 1000 AS BIGINT)`,
			" FOR UPDATE SKIP LOCKED"), nil
	case db.SQLite:
		// SQLite runs one writer at a time, so a claim is atomic without a lock.
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
		dueNext: r.Replace(`SELECT MIN(available_at) - {now} FROM {table} ` +
			`WHERE status IN ('pending', 'retrying', 'running')`),
		// The rotation key after available_at keeps one tenant's due rows from
		// leading every batch, and the claim moves available_at to the claim's
		// expiry beside claim_expires_at so a row is due again exactly when its
		// claim runs out.
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
