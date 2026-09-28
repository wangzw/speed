package postgres

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/vislake/speed/pkg/event"
	"github.com/vislake/speed/pkg/event/internal/bussuite"
	"github.com/vislake/speed/pkg/internal/pgpool/pgtest"
)

// TestOpeningAReplicaWritesNothing pins what a replica costs to start: no rows.
// A replica used to mark every entry already in the outbox as delivered to
// itself, so each process start — every command that takes the bus in, a
// short-lived one included — wrote one row per outbox entry, and those rows
// stayed until their entries retired.
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

// TestEntriesPublishedBeforeAReplicaOpenedAreNotDelivered is the other half of
// the same starting point: what was published before a replica opened is not
// owed to its subscribers, and what is published after is.
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
	late.Subscribe("t", func(context.Context, event.Event) error {
		got.Add(1)
		return nil
	})
	if err := late.begin(ctx); err != nil {
		t.Fatalf("begin: %v", err)
	}
	if err := publisher.Publish(ctx, event.Event{Type: "t"}); err != nil {
		t.Fatalf("Publish: %v", err)
	}
	bussuite.WaitFor(t, "the event published after the replica opened", func() bool { return got.Load() >= 1 })
	time.Sleep(pollInterval + 500*time.Millisecond)
	if n := got.Load(); n != 1 {
		t.Fatalf("the late replica delivered %d events, want only the 1 published after it opened", n)
	}
}

// TestAnEntryThatCommitsAfterAReplicaOpenedIsDelivered pins the case the
// delivery marks exist for. A transaction that was already running when the
// replica opened — its entry's id handed out, its commit not yet made — commits
// afterwards, and its entry is one published after the replica opened.
func TestAnEntryThatCommitsAfterAReplicaOpenedIsDelivered(t *testing.T) {
	dsn := pgtest.Acquire(t)
	ctx := context.Background()
	// A first replica creates the tables.
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
	late.Subscribe("late", func(context.Context, event.Event) error {
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

// TestAnOutboxFromBeforeTheCurrentColumnsIsBroughtUpToDate covers a database
// whose outbox was created before a column this implementation now reads
// existed. CREATE TABLE IF NOT EXISTS leaves such a table as it is, so without
// the column being added every read of it would fail.
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
	b.Subscribe("t", func(context.Context, event.Event) error {
		got.Add(1)
		return nil
	})
	if err := b.begin(ctx); err != nil {
		t.Fatalf("begin: %v", err)
	}
	if err := b.Publish(ctx, event.Event{Type: "t"}); err != nil {
		t.Fatalf("Publish on the older outbox: %v", err)
	}
	bussuite.WaitFor(t, "the event published on the older outbox", func() bool { return got.Load() >= 1 })
	time.Sleep(pollInterval + 500*time.Millisecond)
	if n := got.Load(); n != 1 {
		t.Fatalf("the replica delivered %d events, want only the 1 published after it opened", n)
	}
}
