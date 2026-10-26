//go:build !review

package main

import "testing"

// differentialConfigBody is the baseline configuration the differential case
// runs its processes over. Without the review tag the program assembly carries
// the baseline modules only, and the case's template is the one they read.
func differentialConfigBody(t *testing.T, ports []int) string {
	t.Helper()
	return configBody(t, ports, ":memory:")
}
