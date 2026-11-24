//go:build integration

package postgres

import (
	"context"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"

	"github.com/vislake/speed/pkg/event"
	"github.com/vislake/speed/pkg/event/internal/bussuite"
	"github.com/vislake/speed/pkg/event/internal/tracing"
	"github.com/vislake/speed/pkg/internal/pgpool"
	"github.com/vislake/speed/pkg/internal/pgpool/pgtest"
	"github.com/vislake/speed/pkg/log"
)

// TestTheListeningConnectionGoesWhereThePoolGoes pins that the listening
// session reaches the same database as every publish, whatever the whitespace.
func TestTheListeningConnectionGoesWhereThePoolGoes(t *testing.T) {
	dsn := pgtest.Acquire(t)
	for _, given := range []string{dsn + "\n", "  " + dsn, "\t" + dsn + " \r\n"} {
		opened, err := open(context.Background(), &pgpool.Config{DSN: given, MaxConns: 2}, log.Default(), tracing.Noop())
		if err != nil {
			t.Errorf("open with the connection string %q: %v", given, err)
			continue
		}
		b := opened.(*bus)
		pooled, listening := b.pool.Config().ConnConfig, b.listen.Config()
		if listening.Host != pooled.Host || listening.Port != pooled.Port || listening.Database != pooled.Database {
			t.Errorf("with the connection string %q the listening session went to %s:%d/%s, and the "+
				"pool to %s:%d/%s", given, listening.Host, listening.Port, listening.Database,
				pooled.Host, pooled.Port, pooled.Database)
		}
		_ = b.Close(context.Background())
	}
}

// TestARoleThatMayOnlyWriteRowsStartsOnAPreparedDatabase pins the
// deployment that splits the credentials.
func TestARoleThatMayOnlyWriteRowsStartsOnAPreparedDatabase(t *testing.T) {
	dsn := pgtest.Acquire(t)
	ctx := context.Background()
	prepared, err := open(ctx, &pgpool.Config{DSN: dsn, MaxConns: 1}, log.Default(), tracing.Noop())
	if err != nil {
		t.Fatalf("preparing the tables as their owner: %v", err)
	}
	_ = prepared.(*bus).Close(ctx)

	roleDSN := pgtest.DMLRole(t, dsn)
	opened, err := open(ctx, &pgpool.Config{DSN: roleDSN, MaxConns: 2}, log.Default(), tracing.Noop())
	if err != nil {
		t.Fatalf("a role that may read and write rows could not start on a database whose tables are "+
			"already there: %v", err)
	}
	b := opened.(*bus)
	t.Cleanup(func() { _ = b.Close(ctx) })

	var delivered atomic.Int32
	b.Subscribe("t", "test", func(context.Context, event.Event) error {
		delivered.Add(1)
		return nil
	})
	if err := b.begin(ctx); err != nil {
		t.Fatalf("begin as the role: %v", err)
	}
	if err := b.Publish(ctx, event.Event{Type: "t"}); err != nil {
		t.Fatalf("Publish as the role: %v", err)
	}
	bussuite.WaitFor(t, "the event published as the role to be delivered", func() bool { return delivered.Load() == 1 })
	if err := b.runRetention(ctx); err != nil {
		t.Errorf("the retention pass as the role: %v", err)
	}
}

// TestARoleThatMayOnlyWriteRowsIsToldWhatIsMissing is the other side of
// the same deployment: a database whose tables were never created.
func TestARoleThatMayOnlyWriteRowsIsToldWhatIsMissing(t *testing.T) {
	dsn := pgtest.Acquire(t)
	roleDSN := pgtest.DMLRole(t, dsn)
	opened, err := open(context.Background(), &pgpool.Config{DSN: roleDSN, MaxConns: 1}, log.Default(), tracing.Noop())
	if err == nil {
		_ = opened.(*bus).Close(context.Background())
		t.Fatal("a role that may create nothing started on a database without the tables")
	}
	if !strings.Contains(err.Error(), "creating "+table) {
		t.Errorf("the failure does not say it was creating %s: %v", table, err)
	}
	var refusal *pgconn.PgError
	if !errors.As(err, &refusal) || refusal.Code != insufficientPrivilege {
		t.Errorf("the failure does not carry the server's refusal for want of a privilege: %v", err)
	}
}

const insufficientPrivilege = "42501"

// TestReplicasStartingTogetherOnANewDatabaseAllStart pins what a rollout
// of several replicas onto an empty database needs.
func TestReplicasStartingTogetherOnANewDatabaseAllStart(t *testing.T) {
	const rounds, replicas = 5, 4
	for range rounds {
		dsn := pgtest.Acquire(t)
		var wg sync.WaitGroup
		errs := make([]error, replicas)
		for i := range errs {
			wg.Go(func() {
				opened, err := open(context.Background(), &pgpool.Config{DSN: dsn, MaxConns: 2}, log.Default(), tracing.Noop())
				if err != nil {
					errs[i] = err
					return
				}
				_ = opened.(*bus).Close(context.Background())
			})
		}
		wg.Wait()
		for i, err := range errs {
			if err != nil {
				t.Fatalf("replica %d of %d starting together on a new database failed: %v", i, replicas, err)
			}
		}
	}
}
