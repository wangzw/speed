//go:build integration

package postgres

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"

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

// TestAPaddedConnectionStringStillReachesTheServer is the half the stance cannot hold on its own.
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

// TestReplicasOpeningAFreshDatabaseTogetherAllStart covers the first rollout of several replicas
// against an empty database.
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

// TestARoleThatMayOnlyWriteRowsStartsOnAPreparedDatabase checks that a role with only row privileges
// starts on an already-prepared table and can run every operation and the sweep.
func TestARoleThatMayOnlyWriteRowsStartsOnAPreparedDatabase(t *testing.T) {
	dsn := pgtest.Acquire(t)
	ctx := context.Background()
	prepared, err := open(ctx, &pgpool.Config{DSN: dsn, MaxConns: 1})
	if err != nil {
		t.Fatalf("preparing the table as its owner: %v", err)
	}
	_ = prepared.(*store).Close(ctx)

	roleDSN := pgtest.DMLRole(t, dsn)
	opened, err := open(ctx, &pgpool.Config{DSN: roleDSN, MaxConns: 2})
	if err != nil {
		t.Fatalf("a role that may read and write rows could not start on a database whose table is "+
			"already there: %v", err)
	}
	s := opened.(*store)
	t.Cleanup(func() { _ = s.Close(ctx) })

	if err := s.Set(ctx, "k", []byte("v"), time.Hour); err != nil {
		t.Errorf("Set as the role: %v", err)
	}
	if _, _, err := s.Get(ctx, "k"); err != nil {
		t.Errorf("Get as the role: %v", err)
	}
	if _, err := s.CompareAndSwap(ctx, "k", []byte("v"), []byte("w")); err != nil {
		t.Errorf("CompareAndSwap as the role: %v", err)
	}
	if _, err := s.SetIfAbsent(ctx, "claim", []byte("v"), time.Hour); err != nil {
		t.Errorf("SetIfAbsent as the role: %v", err)
	}
	if _, err := s.IncrByFloatWithTTL(ctx, "n", 1, time.Hour); err != nil {
		t.Errorf("IncrByFloatWithTTL as the role: %v", err)
	}
	if err := s.Delete(ctx, "k"); err != nil {
		t.Errorf("Delete as the role: %v", err)
	}
	if _, err := s.pool.Exec(ctx, sweepExpired, sweepBatch); err != nil {
		t.Errorf("the sweep as the role: %v", err)
	}
}

// TestARoleThatMayOnlyWriteRowsIsToldWhatIsMissing is the other side of the same deployment: a
// database whose table was never created.
func TestARoleThatMayOnlyWriteRowsIsToldWhatIsMissing(t *testing.T) {
	dsn := pgtest.Acquire(t)
	roleDSN := pgtest.DMLRole(t, dsn)
	opened, err := open(context.Background(), &pgpool.Config{DSN: roleDSN, MaxConns: 1})
	if err == nil {
		_ = opened.(*store).Close(context.Background())
		t.Fatal("a role that may create nothing started on a database without the table")
	}
	if !strings.Contains(err.Error(), table) || !strings.Contains(err.Error(), "creating the table") {
		t.Errorf("the failure does not say it was creating %s: %v", table, err)
	}
	var refusal *pgconn.PgError
	if !errors.As(err, &refusal) || refusal.Code != insufficientPrivilege {
		t.Errorf("the failure does not carry the server's refusal for want of a privilege: %v", err)
	}
}

const insufficientPrivilege = "42501"

// TestATableWithTextKeysIsConvertedInPlace covers a database that already holds the table in the
// shape it had when its key column was text.
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

// TestAnExpiryIsJudgedByTheClockThatSetIt covers replicas whose clocks do not agree with the
// database's.
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

// TestExpiredRowsAreReclaimedWithoutBeingReadAgain covers the storage a long-running deployment
// accumulates.
func TestExpiredRowsAreReclaimedWithoutBeingReadAgain(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	for i := range 50 {
		if err := s.Set(ctx, fmt.Sprintf("cached-%d", i), []byte("v"), time.Millisecond); err != nil {
			t.Fatalf("Set: %v", err)
		}
		if _, err := s.IncrByFloatWithTTL(ctx, fmt.Sprintf("window-%d", i), 1, time.Millisecond); err != nil {
			t.Fatalf("IncrByFloatWithTTL: %v", err)
		}
	}
	time.Sleep(20 * time.Millisecond)
	deadline := time.Now().Add(5 * time.Second)
	for i := 0; ; i++ {
		if _, err := s.IncrByFloatWithTTL(ctx, fmt.Sprintf("later-%d", i), 1, time.Hour); err != nil {
			t.Fatalf("IncrByFloatWithTTL: %v", err)
		}
		var expired int
		if err := s.pool.QueryRow(ctx,
			`SELECT count(*) FROM `+table+` WHERE expires_at <= now()`).Scan(&expired); err != nil {
			t.Fatalf("counting the expired rows: %v", err)
		}
		if expired == 0 {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("%d expired rows are still in %s, and nothing will read them again", expired, table)
		}
		time.Sleep(100 * time.Millisecond)
	}
}

func withSearchPath(dsn, path string) string {
	separator := "?"
	if strings.Contains(dsn, "?") {
		separator = "&"
	}
	return dsn + separator + "search_path=" + url.QueryEscape(path)
}

// TestTwoStoresOnOneDatabaseShareTheSameEntries checks that two stores on one database read each
// other's value and count one shared counter to 12.
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
