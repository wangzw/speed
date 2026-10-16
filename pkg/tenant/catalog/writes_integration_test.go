//go:build integration

package catalog

import "testing"

func TestAnUpsertByKeyRewritesTheContextTenantsOwnRowOnPostgres(t *testing.T) {
	requireUpsertByKey(t, openFilteredPostgres(t))
}

func TestAnUpsertAssigningTheColumnFromAnythingElseOfTheInsertedRowIsRefusedOnPostgres(t *testing.T) {
	requireInsertedRowCopiesRefused(t, openFilteredPostgres)
}
