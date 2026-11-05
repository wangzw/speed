package main

// The program-level case helper several tests share: a seeded serve on a
// database file the two processes share, with the caller's extra configuration,
// answered by the backend base URL and a token for the seeded account that holds
// no business membership and no email address.

import (
	"fmt"
	"path/filepath"
	"testing"
)

// seededServe starts a seeded serve with the caller's extra configuration and
// answers the backend base URL and a token for the account with no membership
// and no email address (the configuration's second seed account).
func seededServe(t *testing.T, extra string) (string, string) {
	t.Helper()
	ports := freePorts(t, listenerCount)
	dsn := filepath.Join(t.TempDir(), "demo.db")
	body := configBody(t, ports, dsn) + extra
	config := writeConfigFile(t, body)
	if res := runCommand(t, config, "sqlite-memory", seedCommand); res.err != nil {
		t.Fatalf("seed failed: %v\n%s", res.err, res.combined())
	}
	serve := start(t, config, "sqlite-memory", serveCommand)
	serve.waitReady(t)
	base := fmt.Sprintf("http://127.0.0.1:%d", ports[backendListener])
	return base, signIn(t, base, hashIdentifier, seedPassword)
}
