package postgres

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/vislake/speed/pkg/event"
	"github.com/vislake/speed/pkg/internal/pgpool/pgtest"
)

// TestALostListeningConnectionIsTakenAgain pins the recovery of the
// notification path. The listening session can be terminated or lose its
// network path while the process lives on; waiting on the dead connection
// again fails at once every round, and this replica would deliver only by
// polling the table — every event up to a poll interval late, and an error
// line every round — for the rest of the process's life.
//
// Each event is published while the replica is waiting for a notification,
// so it arrives promptly only if a live session is listening: a replica left
// polling picks it up a poll interval later.
func TestALostListeningConnectionIsTakenAgain(t *testing.T) {
	dsn := pgtest.Acquire(t)
	ctx := context.Background()
	b := openBus(t, dsn)
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
	time.Sleep(time.Second)

	for i := range 3 {
		sent := time.Now()
		if err := publisher.Publish(ctx, event.Event{Type: "listen"}); err != nil {
			t.Fatalf("Publish: %v", err)
		}
		select {
		case at := <-arrived:
			if took := at.Sub(sent); took > pollInterval/3 {
				t.Fatalf("event %d took %v to arrive after the listening session was terminated: the "+
					"replica is delivering by polling the table, not by notification", i, took)
			}
		case <-time.After(10 * time.Second):
			t.Fatalf("event %d never arrived", i)
		}
		time.Sleep(300 * time.Millisecond)
	}
}
