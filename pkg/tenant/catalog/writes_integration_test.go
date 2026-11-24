//go:build integration

package catalog

import "testing"

// TestAnUpsertByKeyRewritesTheContextTenantsOwnRowOnPostgres checks on
// PostgreSQL that an upsert by key conflicts on the context tenant's own row.
func TestAnUpsertByKeyRewritesTheContextTenantsOwnRowOnPostgres(t *testing.T) {
	requireUpsertByKey(t, openFilteredPostgres(t))
}

// TestAnUpsertAssigningTheColumnFromAnythingElseOfTheInsertedRowIsRefusedOnPostgres
// checks on PostgreSQL that an upsert assigning the tenant column from anything
// but the inserted tenant column is refused.
func TestAnUpsertAssigningTheColumnFromAnythingElseOfTheInsertedRowIsRefusedOnPostgres(t *testing.T) {
	requireInsertedRowCopiesRefused(t, openFilteredPostgres)
}
