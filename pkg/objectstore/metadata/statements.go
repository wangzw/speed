package metadata

import (
	"fmt"
	"strings"

	"github.com/vislake/speed/pkg/db"
)

// statements is the SQL the sweep and the content route run, written once for
// each engine the database capability delivers.
//
// Two reads cross tenants: the sweep and the lookup of a content grant, which a
// request presents without a tenant. Both are native SQL rather than model
// statements: the roster's filter has nothing to add to a pass that is not a
// tenant's request or to a lookup that names its row by a unique hash, and the
// clock, which the design pins to the database, is what a dialect differs in. A
// native statement reaches neither the filter nor the audit capture, which is
// what these two want.
//
// Every instant is milliseconds since the Unix epoch, read from the database's
// own clock, so the sweeps of several replicas judge "expired" against one
// clock. A pass judged against each replica's own clock could tombstone a row
// whose window another replica's clock still holds open.
type statements struct {
	// expired selects the rows whose time is up: a pending object whose upload
	// window has closed, and a completed object past its expiry. Argument: limit.
	expired string
	// tombstone marks one row deleted, conditionally on it not being a tombstone
	// already. Arguments: tenant identifier, row identifier.
	tombstone string
	// grant reads one grant by token hash and judges it against the database's
	// clock in the same statement, so a token's liveness is decided by one
	// engine. Argument: token hash.
	grant string
	// purgeGrants removes the grants that have expired, so the table does not
	// grow without bound. Argument: none.
	purgeGrants string
}

// statementsFor returns the statements of a dialect, or an error for a dialect
// no statements are written for.
func statementsFor(dialect db.Dialect) (statements, error) {
	switch dialect {
	case db.Postgres:
		return build(`CAST(EXTRACT(EPOCH FROM clock_timestamp()) * 1000 AS BIGINT)`), nil
	case db.SQLite:
		// 'now' reads the same instant throughout a statement.
		return build(`CAST((julianday('now') - 2440587.5) * 86400000 AS INTEGER)`), nil
	default:
		return statements{}, fmt.Errorf("no statements are written for the %q dialect; this module "+
			"supports the dialects the database capability delivers, %s and %s", dialect, db.Postgres, db.SQLite)
	}
}

// build writes the statements around an engine's clock expression.
func build(now string) statements {
	r := strings.NewReplacer("{table}", tableName, "{grants}", grantTableName, "{now}", now)
	return statements{
		expired: r.Replace(`SELECT tenant_id, id, key FROM {table} WHERE ` +
			`(state = 'pending' AND upload_expires_at < {now}) OR ` +
			`(state = 'completed' AND expires_at IS NOT NULL AND expires_at < {now}) LIMIT ?`),
		tombstone: r.Replace(`UPDATE {table} SET state = 'deleted', updated_at = {now} ` +
			`WHERE tenant_id = ? AND id = ? AND state <> 'deleted'`),
		grant: r.Replace(`SELECT tenant_id, object_id, use, expires_at, ` +
			`CASE WHEN expires_at > {now} THEN 1 ELSE 0 END AS live ` +
			`FROM {grants} WHERE token_hash = ? LIMIT 1`),
		purgeGrants: r.Replace(`DELETE FROM {grants} WHERE expires_at < {now}`),
	}
}
