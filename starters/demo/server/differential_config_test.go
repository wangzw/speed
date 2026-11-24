package main

import "testing"

func differentialConfigBody(t *testing.T, ports []int, dsn string) string {
	t.Helper()
	return configBody(t, ports, dsn)
}
