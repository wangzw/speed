package postgres

import (
	"context"
	"sync"
	"testing"

	"github.com/vislake/speed/pkg/internal/pgpool"
	"github.com/vislake/speed/pkg/internal/pgpool/pgtest"
	"github.com/vislake/speed/pkg/log"
)

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
