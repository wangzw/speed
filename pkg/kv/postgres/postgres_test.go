package postgres

import (
	"context"
	"errors"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"

	"github.com/vislake/speed/pkg/core"
	"github.com/vislake/speed/pkg/internal/pgpool"
	"github.com/vislake/speed/pkg/internal/pgpool/pgtest"
	"github.com/vislake/speed/pkg/kv"
	"github.com/vislake/speed/pkg/kv/internal/kvsuite"
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
func TestOnlyTheServersNonNumericComplaintsBecomeTheSentinel(t *testing.T) {
	if got := asNotNumeric(nil, "k"); got != nil {
		t.Fatalf("asNotNumeric(nil) = %v, want nil", got)
	}
	conn := errors.New("failed to connect to `host=127.0.0.1`: connection refused")
	if got := asNotNumeric(conn, "k"); !errors.Is(got, conn) {
		t.Fatalf("a connection failure was rewritten as %v", got)
	}
	other := &pgconn.PgError{Code: "42P01", Message: `relation "kv_entries" does not exist`}
	if got := asNotNumeric(other, "k"); !errors.Is(got, other) {
		t.Fatalf("an unrelated server error was rewritten as %v", got)
	}
	for _, complaint := range []*pgconn.PgError{
		{Code: invalidTextRepresentation, Message: `invalid input syntax for type numeric: "not a number"`},
		{Code: characterNotInRepertoire, Message: `invalid byte sequence for encoding "UTF8": 0xff`},
	} {
		got := asNotNumeric(complaint, "k")
		if !errors.Is(got, kv.ErrNotNumeric) {
			t.Fatalf("the server's complaint %q was not mapped onto the sentinel: %v", complaint.Message, got)
		}
		if strings.Contains(got.Error(), "not a number") || strings.Contains(got.Error(), "0xff") {
			t.Fatalf("the sentinel carries the server's quotation of the stored value: %v", got)
		}
	}
}

// TestReplicasOpeningAFreshDatabaseTogetherAllStart covers the first rollout of
// several replicas against an empty database. Each one creates the table as it
// opens, and PostgreSQL's CREATE TABLE IF NOT EXISTS is not safe against a
// concurrent copy of itself: two sessions can both find the table missing, and
// the one that commits second fails on the catalog's unique index for the
// table's row type — which here is a replica that fails its startup.
//
// The race is probabilistic, so the case gives it several fresh databases and
// several simultaneous opens on each.
func TestReplicasOpeningAFreshDatabaseTogetherAllStart(t *testing.T) {
	const rounds, replicas = 4, 6
	ctx := context.Background()
	for round := range rounds {
		dsn := pgtest.Acquire(t)
		start := make(chan struct{})
		errs := make(chan error, replicas)
		var wg sync.WaitGroup
		for range replicas {
			wg.Add(1)
			go func() {
				defer wg.Done()
				<-start
				opened, err := open(ctx, &pgpool.Config{DSN: dsn, MaxConns: 1})
				if err != nil {
					errs <- err
					return
				}
				_ = opened.(*store).Close(ctx)
			}()
		}
		close(start)
		wg.Wait()
		close(errs)
		for err := range errs {
			t.Errorf("round %d: a replica failed to start against a fresh database: %v", round, err)
		}
	}
}

// TestATableWithTextKeysIsConvertedInPlace covers a database that already holds
// the table in the shape it had when its key column was text. Opening converts
// the column rather than leaving it — every statement here compares bytes, and
// against text they would all fail at the first request — and the entries
// already there stay readable under the keys they were written with, the long
// one included, whose spelling changes.
func TestATableWithTextKeysIsConvertedInPlace(t *testing.T) {
	dsn := pgtest.Acquire(t)
	ctx := context.Background()
	pool, err := pgpool.Connect(ctx, "probe", pgpool.Config{DSN: dsn, MaxConns: 1})
	if err != nil {
		t.Fatalf("connecting: %v", err)
	}
	long := strings.Repeat("l", 1500)
	for _, statement := range []string{
		`CREATE TABLE ` + table + ` (key TEXT PRIMARY KEY, value BYTEA NOT NULL, expires_at TIMESTAMPTZ)`,
		`INSERT INTO ` + table + ` (key, value) VALUES ('short', 'one'), ('` + long + `', 'two')`,
	} {
		if _, execErr := pool.Exec(ctx, statement); execErr != nil {
			pool.Close()
			t.Fatalf("preparing the text-keyed table: %v", execErr)
		}
	}
	pool.Close()

	opened, err := open(ctx, &pgpool.Config{DSN: dsn, MaxConns: 2})
	if err != nil {
		t.Fatalf("opening over a text-keyed table: %v", err)
	}
	s := opened.(*store)
	t.Cleanup(func() { _ = s.Close(ctx) })
	for key, want := range map[string]string{"short": "one", long: "two"} {
		got, present, err := s.Get(ctx, key)
		if err != nil || !present || string(got) != want {
			t.Fatalf("Get on the %d-byte key written before the conversion: %q, present=%v, err=%v",
				len(key), got, present, err)
		}
	}
	if err := s.Set(ctx, "nul\x00byte", []byte("three"), 0); err != nil {
		t.Fatalf("Set on a key a text column refuses, after the conversion: %v", err)
	}
}

// TestAnExpiryIsJudgedByTheClockThatSetIt covers replicas whose clocks do not
// agree with the database's. The statements judge a row's expiry against the
// database's now(), so the instant a row expires has to come from the same
// clock: stamped from the writing replica's clock instead, a replica running
// ahead of the database writes keys that live too long, and one running behind
// writes keys that are expired the moment they land.
//
// The case skews the database's clock rather than the replica's, which is the
// same disagreement seen from the other side: a now() of its own, earlier on
// the search path than the catalog's, shadows the server's for every statement
// the store runs.
func TestAnExpiryIsJudgedByTheClockThatSetIt(t *testing.T) {
	for name, skew := range map[string]string{
		"a database clock ahead of the replica": "+ interval '1 hour'",
		"a database clock behind the replica":   "- interval '1 hour'",
	} {
		t.Run(name, func(t *testing.T) {
			dsn := pgtest.Acquire(t)
			ctx := context.Background()
			pool, err := pgpool.Connect(ctx, "probe", pgpool.Config{DSN: dsn, MaxConns: 1})
			if err != nil {
				t.Fatalf("connecting: %v", err)
			}
			for _, statement := range []string{
				`CREATE SCHEMA skewed`,
				`CREATE FUNCTION skewed.now() RETURNS timestamptz LANGUAGE sql STABLE AS
					$$ SELECT pg_catalog.now() ` + skew + ` $$`,
			} {
				if _, execErr := pool.Exec(ctx, statement); execErr != nil {
					pool.Close()
					t.Fatalf("skewing the clock: %v", execErr)
				}
			}
			pool.Close()

			opened, err := open(ctx, &pgpool.Config{DSN: withSearchPath(dsn, "skewed,pg_catalog"), MaxConns: 2})
			if err != nil {
				t.Fatalf("open: %v", err)
			}
			s := opened.(*store)
			t.Cleanup(func() { _ = s.Close(ctx) })

			// A key with half an hour to live is there now, and a key with a
			// moment to live is gone a little later — whichever way the clocks
			// disagree.
			if setErr := s.Set(ctx, "long", []byte("v"), 30*time.Minute); setErr != nil {
				t.Fatalf("Set: %v", setErr)
			}
			if _, incrErr := s.IncrByFloatWithTTL(ctx, "counter", 1, 30*time.Minute); incrErr != nil {
				t.Fatalf("IncrByFloatWithTTL: %v", incrErr)
			}
			for _, key := range []string{"long", "counter"} {
				if _, present, getErr := s.Get(ctx, key); getErr != nil || !present {
					t.Fatalf("Get on %q, written with half an hour to live: present=%v err=%v", key, present, getErr)
				}
			}
			if setErr := s.Set(ctx, "short", []byte("v"), 20*time.Millisecond); setErr != nil {
				t.Fatalf("Set: %v", setErr)
			}
			time.Sleep(100 * time.Millisecond)
			if _, present, getErr := s.Get(ctx, "short"); getErr != nil || present {
				t.Fatalf("Get on a key past its 20ms: present=%v err=%v", present, getErr)
			}
		})
	}
}

// withSearchPath adds a search_path run-time parameter to a connection URL.
func withSearchPath(dsn, path string) string {
	separator := "?"
	if strings.Contains(dsn, "?") {
		separator = "&"
	}
	return dsn + separator + "search_path=" + url.QueryEscape(path)
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
