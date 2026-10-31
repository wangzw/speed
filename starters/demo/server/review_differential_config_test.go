//go:build review

package main

import (
	"fmt"
	"testing"
)

// differentialConfigBody is the configuration the differential case runs its
// processes over when the test binary carries the review tag. That binary's
// program assembly adds the review modules, and each of them reads its own
// configuration items at startup — the social login methods their client
// identifiers and secrets, the SMS family its template registry — so the case
// reads the review assembly's own file, server/review/sqlite.yaml, rather than
// the baseline template that supplies none of them. The database is in-process
// because serve is the only command the case runs.
func differentialConfigBody(t *testing.T, ports []int, dsn string) string {
	t.Helper()
	return reviewConfigBody(t, t.TempDir(),
		fmt.Sprintf("127.0.0.1:%d", ports[backendListener]),
		dsn)
}
