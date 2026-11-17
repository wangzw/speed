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
// notification path. The listening session can be terminated or lose its
// network path while the process lives on; waiting on the dead connection
// again fails at once every round, and this replica would deliver only by
// polling the table — every event up to a poll interval late, and an error
// line every round — for the rest of the process's life.
//
// Each event is published while the replica is waiting for a notification,
// so it arrives promptly only if a live session is listening: a replica left
// polling picks it up a poll interval later. The case sets that interval to a
// minute, far past the bound it allows: a wrong implementation needs up to a
// minute for each event, while a notification takes milliseconds, and the
// bound of two seconds carries GATE_TIME_SCALE alone. The two stay apart until
// the caller's scale reaches about thirty; under the race detector the margin
// is smaller, about twelve, because the notification the bound is compared
// against is slowed there while the bound itself is not.
func TestALostListeningConnectionIsTakenAgain(t *testing.T) {
	dsn := pgtest.Acquire(t)
	ctx := context.Background()
	b := openBus(t, dsn)
	// The wrong implementation this case detects is one that delivers by
	// polling the table. Its poll interval is what its delivery costs, so it
	// is widened here rather than compared against its production value.
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
			if took := at.Sub(sent); took > coretest.Declared(2*time.Second) {
				t.Fatalf("event %d took %v to arrive after the listening session was terminated: the "+
					"replica is delivering by polling the table, not by notification", i, took)
			}
		case <-time.After(coretest.Scale(2 * time.Minute)):
			t.Fatalf("event %d never arrived", i)
		}
		time.Sleep(300 * time.Millisecond)
	}
}
