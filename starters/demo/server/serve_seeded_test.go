package main

import (
	"fmt"
	"path/filepath"
	"testing"
)

func seededServe(t *testing.T, extra string) (string, string) {
	t.Helper()
	base, token, _ := seededServeWithDatabase(t, extra)
	return base, token
}

func seededServeWithDatabase(t *testing.T, extra string) (string, string, string) {
	t.Helper()
	dsn := filepath.Join(t.TempDir(), "demo.db")
	write := func(ports []int) string {
		return writeConfigFile(t, configBody(t, ports, dsn)+extra)
	}
	config := write(freePorts(t, listenerCount))
	if res := runCommand(t, config, "sqlite-memory", seedCommand); res.err != nil {
		t.Fatalf("seed failed: %v\n%s", res.err, res.combined())
	}
	_, ports := startServing(t, func(ports []int) *process {
		return start(t, write(ports), "sqlite-memory", serveCommand)
	})
	base := fmt.Sprintf("http://127.0.0.1:%d", ports[backendListener])
	return base, signIn(t, base, hashIdentifier, seedPassword), dsn
}
