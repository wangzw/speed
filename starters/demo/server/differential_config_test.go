package main

import "testing"

// differentialConfigBody is the configuration the differential case runs its
// processes over. The program assembly carries the baseline modules, and the
// case's template is the one they read.
func differentialConfigBody(t *testing.T, ports []int, dsn string) string {
	t.Helper()
	return configBody(t, ports, dsn)
}
