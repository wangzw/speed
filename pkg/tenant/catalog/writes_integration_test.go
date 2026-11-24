//go:build integration

package catalog

import "testing"

// TestAnUpsertByKeyRewritesTheContextTenantsOwnRowOnPostgres checks that an upsert by
// key rewrites the context tenant's own row on PostgreSQL.
func TestAnUpsertByKeyRewritesTheContextTenantsOwnRowOnPostgres(t *testing.T) {
	requireUpsertByKey(t, openFilteredPostgres(t))
}

// TestAnUpsertAssigningTheColumnFromAnythingElseOfTheInsertedRowIsRefusedOnPostgres
// checks that such an upsert is refused on PostgreSQL.
func TestAnUpsertAssigningTheColumnFromAnythingElseOfTheInsertedRowIsRefusedOnPostgres(t *testing.T) {
	requireInsertedRowCopiesRefused(t, openFilteredPostgres)
}
