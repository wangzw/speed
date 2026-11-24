//go:build integration

package postgres_test

import (
	"context"
	"errors"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/vislake/speed/pkg/db"
	"github.com/vislake/speed/pkg/db/postgres"
	"github.com/vislake/speed/pkg/internal/pgpool/pgtest"
)

// TestOpenIsBoundByItsContext pins the same bound on a connection a caller builds at run time: the
// context handed to Open is where a request puts its deadline.
func TestOpenIsBoundByItsContext(t *testing.T) {
	capability, err := runAssembly(t, pgtest.Acquire(t))
	if err != nil {
		t.Fatalf("starting a host on the fixture: %v", err)
	}
	dsn, stop := silentServer(t)
	t.Cleanup(stop)

	ctx, cancel := context.WithTimeout(context.Background(), connectDeadline)
	defer cancel()
	type outcome struct{ err error }
	result := make(chan outcome, 1)
	go func() {
		handle, err := capability.Open(ctx, dsn)
		if err == nil {
			_ = db.Close(handle)
		}
		result <- outcome{err}
	}()
	select {
	case got := <-result:
		if !errors.Is(got.err, db.ErrConnectFailed) {
			t.Fatalf("Open against a database that never answers gave %v, want ErrConnectFailed", got.err)
		}
	case <-time.After(connectBound):
		stop()
		<-result
		t.Fatalf("Open was still waiting on the database %s after its context's %s deadline had passed",
			connectBound, connectDeadline)
	}
}

// TestAConnectFailureDoesNotEchoThePassword pins the part of a locator the failure text must never
// carry, on this engine's own driver: a refused login reports the dialect.
func TestAConnectFailureDoesNotEchoThePassword(t *testing.T) {
	parsed, err := url.Parse(pgtest.Acquire(t))
	if err != nil {
		t.Fatalf("reading the fixture's locator: %v", err)
	}
	const wrongPassword = "not-the-password-4c1f"
	parsed.User = url.UserPassword(parsed.User.Username(), wrongPassword)
	ctx, cancel := context.WithTimeout(context.Background(), connectBound)
	defer cancel()
	returned, err := startupAgainst(t, ctx, map[string]any{"dsn": parsed.String()}, func() {})
	if !returned {
		t.Fatal("the startup with a wrong password did not end")
	}
	if !errors.Is(err, db.ErrConnectFailed) {
		t.Fatalf("a refused login ended the startup with %v, want ErrConnectFailed", err)
	}
	if strings.Contains(err.Error(), wrongPassword) {
		t.Errorf("the failure echoes the password: %v", err)
	}
	if strings.Contains(err.Error(), parsed.String()) {
		t.Errorf("the failure echoes the locator: %v", err)
	}
	if !strings.Contains(err.Error(), string(postgres.Dialect)) {
		t.Errorf("the failure does not name the dialect: %v", err)
	}
}
