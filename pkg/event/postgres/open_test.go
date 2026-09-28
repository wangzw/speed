package postgres

import (
	"context"
	"sync"
	"testing"

	"github.com/vislake/speed/pkg/internal/pgpool"
	"github.com/vislake/speed/pkg/internal/pgpool/pgtest"
	"github.com/vislake/speed/pkg/log"
)

// TestTheListeningConnectionGoesWhereThePoolGoes pins that the listening
// session reaches the same database as every publish, whatever whitespace the
// connection string carries. Secrets and env files routinely end in a newline;
// the pool normalises the string, and a listening connection parsed from the
// raw text again failed the startup on a trailing newline, and on leading
// whitespace went to the libpq defaults instead — a different server or
// database, where the notifications a publish sends never arrive.
func TestTheListeningConnectionGoesWhereThePoolGoes(t *testing.T) {
	dsn := pgtest.Acquire(t)
	for _, given := range []string{dsn + "\n", "  " + dsn, "\t" + dsn + " \r\n"} {
		opened, err := open(context.Background(), &pgpool.Config{DSN: given, MaxConns: 2}, log.Default())
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

// TestReplicasStartingTogetherOnANewDatabaseAllStart pins what a rollout of
// several replicas onto an empty database needs: every one of them starts.
// CREATE TABLE IF NOT EXISTS run by several sessions at once is not safe — the
// sessions that lose fail on the catalog's unique indexes — so without the
// creation serialised, all replicas but the first failed their startup.
//
// Each round is a fresh database, because only the first creation races.
func TestReplicasStartingTogetherOnANewDatabaseAllStart(t *testing.T) {
	const rounds, replicas = 5, 4
	for range rounds {
		dsn := pgtest.Acquire(t)
		var wg sync.WaitGroup
		errs := make([]error, replicas)
		for i := range errs {
			wg.Go(func() {
				opened, err := open(context.Background(), &pgpool.Config{DSN: dsn, MaxConns: 2}, log.Default())
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
