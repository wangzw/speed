package db_test

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"

	"github.com/vislake/speed/pkg/config"
	jsonformat "github.com/vislake/speed/pkg/config/format/json"
	filesource "github.com/vislake/speed/pkg/config/source/file"
	"github.com/vislake/speed/pkg/core"
	"github.com/vislake/speed/pkg/db"
)

const hostNamespace = "db.testhost"

func sqliteSpec() db.Spec {
	return db.Spec{
		ModuleName:          hostNamespace,
		ConfigNamespace:     hostNamespace,
		Dialect:             db.SQLite,
		Dialector:           sqlite.Open,
		ConstraintViolation: func(error) error { return nil },
	}
}

func hostConfig(t *testing.T, extra ...string) string {
	t.Helper()
	body := fmt.Sprintf(`{"db":{"testhost":{"dsn":%q`, filepath.Join(t.TempDir(), "host.db"))
	for _, item := range extra {
		body += "," + item
	}
	return body + "}}}"
}

func unopenableLocator(t *testing.T) string {
	t.Helper()
	return filepath.Join(t.TempDir(), "no-such-directory", "db.sqlite")
}

func poolOf(t *testing.T, handle *gorm.DB) *sql.DB {
	t.Helper()
	pool, err := handle.DB()
	if err != nil {
		t.Fatalf("reaching the connection pool: %v", err)
	}
	return pool
}

type probe struct {
	delivered chan db.Database
	migrated  chan struct{}
	gate      chan struct{}
}

func newProbe() *probe {
	return &probe{
		delivered: make(chan db.Database, 1),
		migrated:  make(chan struct{}),
		gate:      make(chan struct{}),
	}
}

func (p *probe) module() core.Module {
	return core.Module{
		Name:     "probe",
		Requires: []core.Requirement{{Token: (*db.Database)(nil)}},
		New: func(_ context.Context, reg *core.Registry) (any, error) {
			instance, err := core.Resolve[db.Database](reg)
			if err != nil {
				return nil, err
			}
			p.delivered <- instance
			return nil, nil
		},
		Init: func(context.Context, *core.Registry, any) error {
			close(p.migrated)
			<-p.gate
			return nil
		},
	}
}

type host struct {
	Instance db.Database

	migrated chan struct{}
	gate     chan struct{}
	cancel   context.CancelFunc
	done     chan error
	once     sync.Once
}

func (h *host) finish(t *testing.T) {
	t.Helper()
	h.once.Do(func() {
		close(h.gate)
		h.cancel()
		if err := <-h.done; err != nil {
			t.Errorf("the host shut down with an error: %v", err)
		}
	})
}

func startHost(t *testing.T, spec db.Spec, body string, extra ...core.Module) (*host, error) {
	t.Helper()
	return startHostWith(t, spec, body, nil, extra...)
}

func startHostWith(t *testing.T, spec db.Spec, body string, before func(*core.Registry),
	extra ...core.Module,
) (*host, error) {
	t.Helper()
	locator := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(locator, []byte(body), 0o600); err != nil {
		t.Fatalf("writing the primary configuration source: %v", err)
	}

	reg := core.New()
	reg.Register(config.Module())
	reg.Register(jsonformat.Module())
	reg.Register(filesource.Module())
	reg.Register(core.Module{
		Name:      "host",
		Resources: []any{config.HostIdentity{Prefix: "SPEEDDBTEST", DefaultLocator: "file://" + locator}},
	})
	reg.Register(db.NewModule(spec))
	for _, module := range extra {
		reg.Register(module)
	}
	p := newProbe()
	reg.Register(p.module())
	if before != nil {
		before(reg)
	}

	// The command line is the process's and a test binary carries arguments of its own, so they are taken away while the tree reads them; the cases here therefore do not run in parallel.
	previous := os.Args
	os.Args = []string{"db.test"}

	app := config.NewApp(reg)
	app.Root().IncludeAll()
	app.Root().Do(core.StageServe, nil)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- app.Execute(ctx) }()

	h := &host{migrated: p.migrated, gate: p.gate, cancel: cancel, done: done}
	select {
	case err := <-done:
		os.Args = previous
		cancel()
		return nil, err
	case h.Instance = <-p.delivered:
		os.Args = previous
	}
	select {
	case err := <-done:
		cancel()
		return nil, err
	case <-p.migrated:
	}
	t.Cleanup(func() { h.finish(t) })
	return h, nil
}

// TestPoolParametersReachTheHandle pins that the connection a module delivers is sized by the
// configuration the host wrote.
func TestPoolParametersReachTheHandle(t *testing.T) {
	h, err := startHost(t, sqliteSpec(), hostConfig(t, `"max-open-conns":7`))
	if err != nil {
		t.Fatalf("starting a host on a configured database: %v", err)
	}
	if got := poolOf(t, h.Instance.DB()).Stats().MaxOpenConnections; got != 7 {
		t.Errorf("the delivered handle opens at most %d connections, want the configured 7", got)
	}
}

// TestTheDeliveredInstanceReportsItsEngine pins the one thing a dependant branching on native SQL
// needs: which engine is behind the handle it took up.
func TestTheDeliveredInstanceReportsItsEngine(t *testing.T) {
	h, err := startHost(t, sqliteSpec(), hostConfig(t))
	if err != nil {
		t.Fatalf("starting a host on a configured database: %v", err)
	}
	if got := h.Instance.Dialect(); got != db.SQLite {
		t.Errorf("the delivered instance reports %q, want %q", got, db.SQLite)
	}
}

// TestStartupOnAnUnreachableDatabaseIsErrConnectFailed pins that a locator the driver refuses fails
// the startup with ErrConnectFailed.
func TestStartupOnAnUnreachableDatabaseIsErrConnectFailed(t *testing.T) {
	_, err := startHost(t, sqliteSpec(),
		fmt.Sprintf(`{"db":{"testhost":{"dsn":%q}}}`, unopenableLocator(t)))
	if !errors.Is(err, db.ErrConnectFailed) {
		t.Fatalf("starting a host on a locator the driver refuses gave %v, want ErrConnectFailed", err)
	}
}

// TestOpenFailureIsErrConnectFailed pins the same sentinel on the other side of the delivery.
func TestOpenFailureIsErrConnectFailed(t *testing.T) {
	h, err := startHost(t, sqliteSpec(), hostConfig(t))
	if err != nil {
		t.Fatalf("starting a host on a configured database: %v", err)
	}
	if _, err := h.Instance.Open(t.Context(), unopenableLocator(t)); !errors.Is(err, db.ErrConnectFailed) {
		t.Fatalf("building a connection to a locator the driver refuses gave %v, want "+
			"ErrConnectFailed", err)
	}
}

// TestOpenSharesPoolParameters pins what a second connection keeps from the delivered one, and what
// it must not keep.
func TestOpenSharesPoolParameters(t *testing.T) {
	h, err := startHost(t, sqliteSpec(), hostConfig(t, `"max-open-conns":7`))
	if err != nil {
		t.Fatalf("starting a host on a configured database: %v", err)
	}
	opened, err := h.Instance.Open(t.Context(), filepath.Join(t.TempDir(), "second.db"))
	if err != nil {
		t.Fatalf("building a second connection: %v", err)
	}
	deliveredPool, openedPool := poolOf(t, h.Instance.DB()), poolOf(t, opened)
	t.Cleanup(func() {
		if err := openedPool.Close(); err != nil {
			t.Errorf("releasing the second connection's pool: %v", err)
		}
	})

	if deliveredPool == openedPool {
		t.Error("the second connection came back on the delivered handle's own pool, so closing " +
			"it would close the module's connections as well")
	}
	if got, want := openedPool.Stats().MaxOpenConnections, deliveredPool.Stats().MaxOpenConnections; got != want {
		t.Errorf("the second connection opens at most %d connections and the delivered one %d, "+
			"want the pool shape of the configuration on both", got, want)
	}
	if err := openedPool.PingContext(t.Context()); err != nil {
		t.Errorf("the second connection does not answer: %v", err)
	}
}

// TestCloseIsReentrant pins both halves of the package-level Close at once: the first call really
// releases the pool, and a second one on the same handle is not an error.
func TestCloseIsReentrant(t *testing.T) {
	h, err := startHost(t, sqliteSpec(), hostConfig(t))
	if err != nil {
		t.Fatalf("starting a host on a configured database: %v", err)
	}
	handle := h.Instance.DB()
	pool := poolOf(t, handle)

	if err := db.Close(handle); err != nil {
		t.Fatalf("closing a live handle: %v", err)
	}
	if err := pool.PingContext(t.Context()); err == nil {
		t.Error("the pool still answers after it was closed, so the first close released nothing")
	}
	if err := db.Close(handle); err != nil {
		t.Errorf("closing a handle whose pool is already closed: %v", err)
	}
}
