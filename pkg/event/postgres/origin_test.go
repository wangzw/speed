//go:build integration

package postgres

import (
	"context"
	"sync/atomic"
	"testing"

	"github.com/jackc/pgx/v5"

	"github.com/vislake/speed/pkg/event"
	"github.com/vislake/speed/pkg/event/internal/bussuite"
	"github.com/vislake/speed/pkg/internal/pgpool/pgtest"
)

func deliveredMarked(t *testing.T, ctx context.Context, b *bus, identifier string) bool {
	t.Helper()
	var marked bool
	if err := b.pool.QueryRow(ctx,
		`SELECT EXISTS (SELECT 1 FROM `+table+` o JOIN `+deliveredTable+` d ON d.event_id = o.id
		 WHERE o.identifier = $1 AND d.replica = $2)`, identifier, b.replica).Scan(&marked); err != nil {
		t.Fatalf("reading the delivery marks: %v", err)
	}
	return marked
}

// TestOpeningAReplicaWritesNothing pins what a replica costs to start: no
// rows.
func TestOpeningAReplicaWritesNothing(t *testing.T) {
	dsn := pgtest.Acquire(t)
	ctx := context.Background()
	publisher := openBus(t, dsn)
	if err := publisher.begin(ctx); err != nil {
		t.Fatalf("begin: %v", err)
	}
	for range 50 {
		if err := publisher.Publish(ctx, event.Event{Type: "history"}); err != nil {
			t.Fatalf("Publish: %v", err)
		}
	}
	late := openBus(t, dsn)
	if marks := marksOf(t, dsn, late.replica); marks != 0 {
		t.Fatalf("opening a replica wrote %d delivery marks, one per entry already in the outbox", marks)
	}
}

// TestEntriesPublishedBeforeAReplicaOpenedAreNotDelivered is the other
// half of the same starting point.
func TestEntriesPublishedBeforeAReplicaOpenedAreNotDelivered(t *testing.T) {
	dsn := pgtest.Acquire(t)
	ctx := context.Background()
	publisher := openBus(t, dsn)
	if err := publisher.begin(ctx); err != nil {
		t.Fatalf("begin: %v", err)
	}
	for range 3 {
		if err := publisher.Publish(ctx, event.Event{Type: "t"}); err != nil {
			t.Fatalf("Publish: %v", err)
		}
	}
	late := openBus(t, dsn)
	var got atomic.Int32
	late.Subscribe("t", "test", func(context.Context, event.Event) error {
		got.Add(1)
		return nil
	})
	if err := late.begin(ctx); err != nil {
		t.Fatalf("begin: %v", err)
	}
	published := event.NewID()
	if err := publisher.Publish(ctx, event.Event{ID: published, Type: "t"}); err != nil {
		t.Fatalf("Publish: %v", err)
	}
	// The entry published after the replica opened is the last one it owes,
	// so its delivered mark says the replica has finished that pass.
	bussuite.WaitFor(t, "the late replica to settle the entry published after it opened",
		func() bool { return deliveredMarked(t, ctx, late, published) })
	if n := got.Load(); n != 1 {
		t.Fatalf("the late replica delivered %d events, want only the 1 published after it opened", n)
	}
}

// TestAnEntryThatCommitsAfterAReplicaOpenedIsDelivered pins the case the
// delivery marks exist for.
func TestAnEntryThatCommitsAfterAReplicaOpenedIsDelivered(t *testing.T) {
	dsn := pgtest.Acquire(t)
	ctx := context.Background()
	openBus(t, dsn)

	writer, err := pgx.Connect(ctx, dsn)
	if err != nil {
		t.Fatalf("connecting: %v", err)
	}
	defer func() { _ = writer.Close(ctx) }()
	inFlight, err := writer.Begin(ctx)
	if err != nil {
		t.Fatalf("beginning the transaction: %v", err)
	}
	if _, err := inFlight.Exec(ctx,
		`INSERT INTO `+table+` (type, payload) VALUES ('late', 'null')`); err != nil {
		t.Fatalf("writing the entry: %v", err)
	}

	late := openBus(t, dsn)
	var got atomic.Int32
	late.Subscribe("late", "test", func(context.Context, event.Event) error {
		got.Add(1)
		return nil
	})
	if err := late.begin(ctx); err != nil {
		t.Fatalf("begin: %v", err)
	}
	if err := inFlight.Commit(ctx); err != nil {
		t.Fatalf("committing: %v", err)
	}
	bussuite.WaitFor(t, "the entry that committed after the replica opened", func() bool { return got.Load() == 1 })
}

// TestAnOutboxFromBeforeTheCurrentColumnsIsBroughtUpToDate covers an outbox
// created before a column this implementation reads existed.
func TestAnOutboxFromBeforeTheCurrentColumnsIsBroughtUpToDate(t *testing.T) {
	dsn := pgtest.Acquire(t)
	ctx := context.Background()
	admin, err := pgx.Connect(ctx, dsn)
	if err != nil {
		t.Fatalf("connecting: %v", err)
	}
	defer func() { _ = admin.Close(ctx) }()
	if _, err := admin.Exec(ctx, `CREATE TABLE `+table+` (
		id         BIGSERIAL PRIMARY KEY,
		type       TEXT NOT NULL,
		tenant     TEXT NOT NULL DEFAULT '',
		payload    TEXT NOT NULL,
		created_at TIMESTAMPTZ NOT NULL DEFAULT now()
	);
	INSERT INTO `+table+` (type, payload) VALUES ('t', 'null')`); err != nil {
		t.Fatalf("creating the older outbox: %v", err)
	}

	b := openBus(t, dsn)
	var got atomic.Int32
	var id atomic.Value
	b.Subscribe("t", "test", func(_ context.Context, evt event.Event) error {
		got.Add(1)
		id.Store(evt.ID)
		return nil
	})
	if err := b.begin(ctx); err != nil {
		t.Fatalf("begin: %v", err)
	}
	const published = "published-on-the-older-outbox"
	if err := b.Publish(ctx, event.Event{ID: published, Type: "t"}); err != nil {
		t.Fatalf("Publish on the older outbox: %v", err)
	}
	bussuite.WaitFor(t, "the replica to settle the event published on the older outbox",
		func() bool { return deliveredMarked(t, ctx, b, published) })
	if n := got.Load(); n != 1 {
		t.Fatalf("the replica delivered %d events, want only the 1 published after it opened", n)
	}
	if carried := id.Load(); carried != published {
		t.Fatalf("the event arrived with the identifier %v, want %q", carried, published)
	}
}
