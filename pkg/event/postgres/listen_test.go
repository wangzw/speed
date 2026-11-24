//go:build integration

package postgres

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/vislake/speed/pkg/core/coretest"
	"github.com/vislake/speed/pkg/event"
	"github.com/vislake/speed/pkg/internal/pgpool/pgtest"
)

// TestALostListeningConnectionIsTakenAgain pins the recovery of the
// notification path.
func TestALostListeningConnectionIsTakenAgain(t *testing.T) {
	dsn := pgtest.Acquire(t)
	ctx := context.Background()
	b := openBus(t, dsn)
	// The wrong implementation this case detects delivers by polling, whose cost
	// is this interval; widening it makes that implementation take the declared
	// time.
	b.pollEvery = time.Minute
	publisher := openBus(t, dsn)
	arrived := make(chan time.Time, 16)
	b.Subscribe("listen", "test", func(context.Context, event.Event) error {
		arrived <- time.Now()
		return nil
	})
	pid := b.listen.PgConn().PID()
	if err := b.begin(ctx); err != nil {
		t.Fatalf("begin: %v", err)
	}
	if err := publisher.begin(ctx); err != nil {
		t.Fatalf("begin on the publisher: %v", err)
	}

	admin, err := pgx.Connect(ctx, dsn)
	if err != nil {
		t.Fatalf("connecting: %v", err)
	}
	defer func() { _ = admin.Close(ctx) }()
	if _, err := admin.Exec(ctx, `SELECT pg_terminate_backend($1)`, pid); err != nil {
		t.Fatalf("terminating the listening session: %v", err)
	}
	time.Sleep(coretest.Scale(time.Second))

	for i := range 3 {
		sent := time.Now()
		if err := publisher.Publish(ctx, event.Event{Type: "listen"}); err != nil {
			t.Fatalf("Publish: %v", err)
		}
		select {
		case at := <-arrived:
			if took := at.Sub(sent); took > coretest.DeclaredAgainst(t, 0, 2*time.Second, b.pollEvery) {
				t.Fatalf("event %d took %v to arrive after the listening session was terminated: the "+
					"replica is delivering by polling the table, not by notification", i, took)
			}
		case <-time.After(coretest.Scale(2 * time.Minute)):
			t.Fatalf("event %d never arrived", i)
		}
		time.Sleep(300 * time.Millisecond)
	}
}
