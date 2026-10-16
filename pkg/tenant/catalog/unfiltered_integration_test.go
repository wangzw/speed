//go:build integration

package catalog

import "testing"

func TestAnInsertWithNoFilterAndNoTenantFailsOnPostgres(t *testing.T) {
	requireUnfilteredInsertRefused(t, openUnfilteredPostgres(t))
}
