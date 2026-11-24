package db_test

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"

	"github.com/vislake/speed/pkg/core"
	"github.com/vislake/speed/pkg/db"
)

// TestEveryFailurePathKeepsTheChain is the executable form of the rule every wrap in this module
// follows: an error goes on with %w.
func TestEveryFailurePathKeepsTheChain(t *testing.T) {
	t.Run("connect", func(t *testing.T) {
		locator := unopenableLocator(t)
		_, err := startHost(t, sqliteSpec(), fmt.Sprintf(`{"db":{"testhost":{"dsn":%q}}}`, locator))
		assertReportsOnly(t, err, "ErrConnectFailed")
		assertHoldsNodeReading(t, err, driverRefusal(t, locator))
	})

	t.Run("plugin", func(t *testing.T) {
		refusal := &pluginRefusal{reason: "the audit sink is not reachable"}
		_, err := startHost(t, sqliteSpec(), hostConfig(t),
			pluginModule("auditing", refusingPlugin{err: refusal}))
		assertReportsOnly(t, err, "ErrPluginFailed")

		var recovered *pluginRefusal
		if !errors.As(err, &recovered) {
			t.Fatalf("the tree under %v does not hold the error the plugin reported, so what the "+
				"plugin said did not travel: a %%v keeps its text and drops the value", err)
		}
		if recovered != refusal {
			t.Errorf("the tree holds a %T other than the one the plugin reported", recovered)
		}
	})

	t.Run("migration", func(t *testing.T) {
		const broken = "CREATE TABLE widgets (id INTEGER"
		_, err := startHost(t, sqliteSpec(), hostConfig(t), core.Module{
			Name:      "broken",
			Resources: []any{migrationSet(db.SQLite, map[string]string{"0001_widgets.sql": broken})},
		})
		assertReportsOnly(t, err, "ErrMigrationFailed")
		assertHoldsNodeReading(t, err, statementRefusal(t, broken))
	})

	t.Run("migration mutex", func(t *testing.T) {
		holder := errors.New("the other replica had not finished applying its migrations")
		spec := sqliteSpec()
		spec.NewMigrationLock = func(allowance time.Duration) db.MigrationLock {
			return &refusingLock{allowance: allowance, holder: holder}
		}
		spec.DefaultMigrationLockTimeout = 50 * time.Millisecond

		_, err := startHost(t, spec, hostConfig(t), core.Module{
			Name: "declaring",
			Resources: []any{migrationSet(db.SQLite, map[string]string{
				"0001_widgets.sql": "CREATE TABLE widgets (id INTEGER)",
			})},
		})
		// A timeout is its own class: reporting it as a migration that would not apply sends the reader through migration files that are sound.
		assertReportsOnly(t, err, "ErrMigrationLockTimeout")
		if !errors.Is(err, holder) {
			t.Errorf("the tree under %v does not hold the error the mutex reported, so what the "+
				"mutex said did not travel", err)
		}
	})

	t.Run("foreign instance", func(t *testing.T) {
		_, _, err := startWithForeignInstance(t)
		assertReportsOnly(t, err, "ErrForeignInstance")
	})
}

func assertReportsOnly(t *testing.T, err error, name string) {
	t.Helper()
	want, known := sentinels[name]
	if !known {
		t.Fatalf("%q is not one of the classes the sentinel table holds", name)
	}
	if err == nil {
		t.Fatal("the startup reported no error, and this case needs it to have failed")
	}
	if !errors.Is(err, want) {
		t.Fatalf("the startup reported %v, which does not carry %s (%v)", err, name, want)
	}
	for otherName, other := range sentinels {
		if otherName == name {
			continue
		}
		if errors.Is(err, other) {
			t.Errorf("the error reports %s and carries %s as well, and the two call for different "+
				"responses: %v", name, otherName, err)
		}
	}
}

func assertHoldsNodeReading(t *testing.T, err, cause error) {
	t.Helper()
	want := cause.Error()
	var walked []string
	for _, node := range errorTree(err) {
		if node.Error() == want {
			return
		}
		walked = append(walked, node.Error())
	}
	t.Errorf("no error in the tree reads as the error from the layer below it does (%q), so that "+
		"error did not travel: a %%v in place of a %%w keeps the text and drops the node. The tree "+
		"under %v reads: %q", want, err, walked)
}

func errorTree(err error) []error {
	var tree []error
	seen := map[error]bool{}
	var walk func(error)
	walk = func(node error) {
		if node == nil {
			return
		}
		if reflect.ValueOf(node).Type().Comparable() {
			if seen[node] {
				return
			}
			seen[node] = true
		}
		tree = append(tree, node)
		for _, child := range childrenOf(node) {
			walk(child)
		}
	}
	walk(err)
	return tree
}

func childrenOf(node error) []error {
	if joined, ok := any(node).(interface{ Unwrap() []error }); ok {
		return joined.Unwrap()
	}
	if wrapped, ok := any(node).(interface{ Unwrap() error }); ok {
		if child := wrapped.Unwrap(); child != nil {
			return []error{child}
		}
	}
	return nil
}

func driverRefusal(t *testing.T, locator string) error {
	t.Helper()
	session, err := gorm.Open(sqlite.Open(locator), &gorm.Config{})
	if err != nil {
		return err
	}
	pool, err := session.DB()
	if err != nil {
		return err
	}
	defer func() { _ = pool.Close() }()
	refusal := pool.PingContext(t.Context())
	if refusal == nil {
		t.Fatal("the driver served a locator this case needs it to refuse, so the failure under " +
			"test is not the driver's refusal")
	}
	return refusal
}

func statementRefusal(t *testing.T, statement string) error {
	t.Helper()
	session, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "refusal.db")), &gorm.Config{})
	if err != nil {
		t.Fatalf("opening a connection to produce the driver's own error: %v", err)
	}
	pool, err := session.DB()
	if err != nil {
		t.Fatalf("reaching the connection pool behind it: %v", err)
	}
	defer func() { _ = pool.Close() }()
	refusal := session.Exec(statement).Error
	if refusal == nil {
		t.Fatal("the driver accepted a statement this case needs it to refuse")
	}
	return refusal
}

// pluginRefusal is what a plugin reports when it will not install; its own type lets a case observe it with errors.As rather than by reading text a %v would leave in place.
type pluginRefusal struct{ reason string }

func (e *pluginRefusal) Error() string { return "the audit plugin will not install: " + e.reason }

type refusingPlugin struct{ err error }

func (p refusingPlugin) Name() string { return "auditing" }

func (p refusingPlugin) Initialize(*gorm.DB) error { return p.err }

type refusingLock struct {
	allowance time.Duration
	holder    error
}

func (l *refusingLock) Acquire(ctx context.Context, _ *sql.Conn) error {
	select {
	case <-ctx.Done():
		return fmt.Errorf("db_test: waiting for the migration mutex was cut short: %w", ctx.Err())
	case <-time.After(l.allowance):
	}
	return fmt.Errorf("%w: another replica held the migration mutex for the whole %s, and %s.%s "+
		"says to give up: %w", db.ErrMigrationLockTimeout, l.allowance, hostNamespace,
		db.MigrationLockTimeoutKey, l.holder)
}

func (l *refusingLock) Release(context.Context, *sql.Conn) error { return nil }
