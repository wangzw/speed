package postgres_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/vislake/speed/pkg/config"
	jsonformat "github.com/vislake/speed/pkg/config/format/json"
	filesource "github.com/vislake/speed/pkg/config/source/file"
	"github.com/vislake/speed/pkg/core"
	"github.com/vislake/speed/pkg/db"
	"github.com/vislake/speed/pkg/db/postgres"
)

// connectBound is how long the cases below give a startup or an Open whose
// context carries a deadline of connectDeadline. It is far above that deadline:
// a return inside it is the context doing its work, and a return only after it
// is a connection attempt nothing bounded.
const (
	connectDeadline = time.Second
	connectBound    = 15 * time.Second
)

// silentServer accepts TCP connections and never answers on them, which is
// what an unresponsive database host looks like to a driver that has already
// reached the port: the connection attempt waits on the first reply for as
// long as nothing bounds it. It returns the locator of a database behind it
// and the function that shuts it down, which also ends every attempt still
// waiting on it.
func silentServer(t *testing.T) (string, func()) {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listening for the silent server: %v", err)
	}
	var (
		mu       sync.Mutex
		accepted []net.Conn
	)
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			mu.Lock()
			accepted = append(accepted, conn)
			mu.Unlock()
		}
	}()
	stop := func() {
		_ = listener.Close()
		mu.Lock()
		defer mu.Unlock()
		for _, conn := range accepted {
			_ = conn.Close()
		}
	}
	return fmt.Sprintf("postgres://user:secret@%s/silent?sslmode=disable", listener.Addr()), stop
}

// startupAgainst drives a whole host whose section is the one given, under
// parent, and returns what the run ended with, or reports that it had not
// ended within connectBound.
//
// Only the run itself goes to another goroutine, so a run that never returns
// leaves nothing of the test's own behind it; stop is what ends it, and the
// cleanup calls it before waiting for the run.
func startupAgainst(t *testing.T, parent context.Context, section map[string]any, stop func()) (bool, error) {
	t.Helper()
	body, err := json.Marshal(map[string]any{"db": map[string]any{"postgres": section}})
	if err != nil {
		t.Fatalf("building the primary configuration source: %v", err)
	}
	locator := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(locator, body, 0o600); err != nil {
		t.Fatalf("writing the primary configuration source: %v", err)
	}
	reg := core.New()
	reg.Register(config.Module())
	reg.Register(jsonformat.Module())
	reg.Register(filesource.Module())
	reg.Register(core.Module{
		Name:      "host",
		Resources: []any{config.HostIdentity{Prefix: "SPEEDPOSTGRESTEST", DefaultLocator: "file://" + locator}},
	})
	reg.Register(postgres.Module())
	app := config.NewApp(reg)
	app.Root().IncludeAll()
	app.Root().Do(core.StageServe, nil)

	previous := os.Args
	os.Args = []string{"postgres.test"}
	done := make(chan error, 1)
	go func() { done <- app.Execute(parent) }()
	t.Cleanup(func() {
		stop()
		select {
		case <-done:
		case <-time.After(connectBound):
			t.Error("the run was still going after the silent server stopped")
		}
		os.Args = previous
	})
	select {
	case err := <-done:
		done <- err
		return true, err
	case <-time.After(connectBound):
		return false, nil
	}
}

// TestTheStartupContextBoundsTheFirstConnection pins the one bound the design
// gives a host on how long a startup waits for its database: the deadline on
// the context the startup is driven under. Against a host that accepts the
// connection and never answers, and a locator that sets no timeout of its own,
// the startup has to end at that deadline with ErrConnectFailed rather than
// wait for as long as the driver would.
func TestTheStartupContextBoundsTheFirstConnection(t *testing.T) {
	dsn, stop := silentServer(t)
	ctx, cancel := context.WithTimeout(context.Background(), connectDeadline)
	defer cancel()

	started := time.Now()
	returned, err := startupAgainst(t, ctx, map[string]any{"dsn": dsn}, stop)
	if !returned {
		t.Fatalf("the startup was still waiting on the database %s after its context's %s deadline "+
			"had passed", connectBound, connectDeadline)
	}
	if !errors.Is(err, db.ErrConnectFailed) {
		t.Fatalf("the startup against a database that never answers ended with %v, want ErrConnectFailed", err)
	}
	t.Logf("the startup ended after %s", time.Since(started))
}
