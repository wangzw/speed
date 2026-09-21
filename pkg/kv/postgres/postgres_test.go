package postgres

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/vislake/speed/pkg/core"
	"github.com/vislake/speed/pkg/kv"
	"github.com/vislake/speed/pkg/kv/internal/kvsuite"
	"github.com/vislake/speed/pkg/pgpool"
	"github.com/vislake/speed/pkg/pgpool/pgtest"
)

func newStore(t *testing.T) *store {
	t.Helper()
	dsn := pgtest.Acquire(t)
	opened, err := open(context.Background(), &pgpool.Config{DSN: dsn, MaxConns: 4})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	s := opened.(*store)
	t.Cleanup(func() { _ = s.Close(context.Background()) })
	return s
}

// TestConforms runs the shared contract against a real server.
//
// The table is emptied between subtests rather than the database being replaced:
// the contract's factory promises a store with no relation to the ones before
// it, and that is the whole of what "no relation" means for a backend whose
// state is one table.
func TestConforms(t *testing.T) {
	s := newStore(t)
	kvsuite.AssertConforms(t, func(t *testing.T) kv.KV {
		t.Helper()
		if _, err := s.pool.Exec(context.Background(), `DELETE FROM `+table); err != nil {
			t.Fatalf("clearing %s: %v", table, err)
		}
		return s
	})
}

// TestAPaddedConnectionStringStillReachesTheServer is the half the stance
// cannot hold on its own. The stance reads a trimmed connection string, so one
// written with surrounding whitespace passes it; the pool is built from that
// same value and the server answers. Handing the parser the text as written
// would refuse a string the stance had just accepted, and the failure would
// land in New rather than in Prepare.
func TestAPaddedConnectionStringStillReachesTheServer(t *testing.T) {
	dsn := pgtest.Acquire(t)
	opened, err := open(context.Background(), &pgpool.Config{DSN: " " + dsn + " ", MaxConns: 4})
	if err != nil {
		t.Fatalf("opening against a padded connection string: %v", err)
	}
	s, ok := opened.(*store)
	if !ok {
		t.Fatalf("open returned %T, want *store", opened)
	}
	t.Cleanup(func() { _ = s.Close(context.Background()) })
}

// TestStanceFailsTheStartupWithoutAConnectionString pins the rule that a module
// named in the command tree has to start: this one reaches PostgreSQL at a
// locator, so being named without one stops the startup and names the input it
// stopped on. Under the stand-down it replaces, the process came up with the
// capability silently missing.
func TestStanceFailsTheStartupWithoutAConnectionString(t *testing.T) {
	_, err := stance(&pgpool.Config{})
	if err == nil {
		t.Fatal("naming this implementation without a connection string started anyway")
	}
	if want := ConfigNamespace + "." + pgpool.DSNKey; !strings.Contains(err.Error(), want) {
		t.Fatalf("the error does not say what the startup stopped on, want it to name %q: %v", want, err)
	}
}

func TestStanceEnablesOnceAConnectionStringIsGiven(t *testing.T) {
	got, err := stance(&pgpool.Config{DSN: "postgres://localhost/x"})
	if err != nil {
		t.Fatalf("stance with a locator: %v", err)
	}
	if got.State != core.StateEnabled {
		t.Fatalf("stance with a locator = %v, want StateEnabled", got.State)
	}
}

// TestOnlyTheServersNonNumericWordingBecomesTheSentinel covers the one mapping
// this backend has to make, and the narrowness that keeps a connection failure
// from being read as a bad value.
func TestOnlyTheServersNonNumericWordingBecomesTheSentinel(t *testing.T) {
	if got := asNotNumeric(nil); got != nil {
		t.Fatalf("asNotNumeric(nil) = %v, want nil", got)
	}
	conn := errors.New("failed to connect to `host=127.0.0.1`: connection refused")
	if got := asNotNumeric(conn); !errors.Is(got, conn) {
		t.Fatalf("a connection failure was rewritten as %v", got)
	}
	other := errors.New(`ERROR: relation "kv_entries" does not exist (SQLSTATE 42P01)`)
	if got := asNotNumeric(other); !errors.Is(got, other) {
		t.Fatalf("an unrelated server error was rewritten as %v", got)
	}
	nonNumeric := errors.New(`ERROR: invalid input syntax for type numeric: "not a number" (SQLSTATE 22P02)`)
	if got := asNotNumeric(nonNumeric); !errors.Is(got, kv.ErrNotNumeric) {
		t.Fatalf("the server's non-numeric complaint was not mapped onto the sentinel: %v", got)
	}
}

// TestTwoStoresOnOneDatabaseShareTheSameEntries is what this backend is for: an
// assembly of several replicas pointed at one PostgreSQL reads and writes one
// set of entries, and the increment is arbitrated by the server rather than by
// either process.
func TestTwoStoresOnOneDatabaseShareTheSameEntries(t *testing.T) {
	dsn := pgtest.Acquire(t)
	ctx := context.Background()
	first, err := open(ctx, &pgpool.Config{DSN: dsn, MaxConns: 4})
	if err != nil {
		t.Fatalf("open on the first: %v", err)
	}
	second, err := open(ctx, &pgpool.Config{DSN: dsn, MaxConns: 4})
	if err != nil {
		t.Fatalf("open on the second: %v", err)
	}
	t.Cleanup(func() {
		_ = first.(*store).Close(ctx)
		_ = second.(*store).Close(ctx)
	})

	if setErr := first.Set(ctx, "k", []byte("from-the-first"), 0); setErr != nil {
		t.Fatalf("Set: %v", setErr)
	}
	got, present, err := second.Get(ctx, "k")
	if err != nil || !present {
		t.Fatalf("Get on the other store: present=%v err=%v", present, err)
	}
	if string(got) != "from-the-first" {
		t.Fatalf("the other store read %q", got)
	}

	if _, incrErr := first.IncrByFloat(ctx, "n", 5); incrErr != nil {
		t.Fatalf("IncrByFloat: %v", incrErr)
	}
	if _, incrErr := second.IncrByFloat(ctx, "n", 7); incrErr != nil {
		t.Fatalf("IncrByFloat: %v", incrErr)
	}
	text, _, err := first.Get(ctx, "n")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	total, err := parseFloat(string(text))
	if err != nil {
		t.Fatalf("the counter holds %q, which is not a number", text)
	}
	if total != 12 {
		t.Fatalf("the two stores counted to %v between them, want 12", total)
	}
}
