package metadata

import (
	"fmt"
	"strings"

	"github.com/vislake/speed/pkg/db"
)

type statements struct {
	expired     string
	tombstone   string
	grant       string
	purgeGrants string
}

func statementsFor(dialect db.Dialect) (statements, error) {
	switch dialect {
	case db.Postgres:
		return build(`CAST(EXTRACT(EPOCH FROM clock_timestamp()) * 1000 AS BIGINT)`), nil
	case db.SQLite:
		return build(`CAST((julianday('now') - 2440587.5) * 86400000 AS INTEGER)`), nil
	default:
		return statements{}, fmt.Errorf("no statements are written for the %q dialect; this module "+
			"supports the dialects the database capability delivers, %s and %s", dialect, db.Postgres, db.SQLite)
	}
}

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
