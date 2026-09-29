package relay

import (
	"context"
	"log/slog"
	"testing"
	"time"

	"gorm.io/gorm"

	"github.com/vislake/speed/pkg/db"
	"github.com/vislake/speed/pkg/event"
)

// stageBacklog stages n events in one transaction and returns their
// identifiers in the order staged.
func stageBacklog(t *testing.T, database db.Database, r *relay, n int) []string {
	t.Helper()
	var ids []string
	err := database.DB().WithContext(t.Context()).Transaction(func(tx *gorm.DB) error {
		for i := range n {
			id := event.NewID()
			ids = append(ids, id)
			if err := r.Stage(tx, event.Event{ID: id, Type: "order.placed", Payload: placed{OrderID: orderID(i)}}); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("staging the backlog: %v", err)
	}
	return ids
}

// dueUnclaimed counts the rows no relay holds that are due now.
func dueUnclaimed(t *testing.T, database db.Database, r *relay) int64 {
	t.Helper()
	var n int64
	query := `SELECT count(*) FROM ` + tableName + ` WHERE claimed_by IS NULL AND available_at <= ` + r.sql.now
	if err := database.DB().Raw(query).Row().Scan(&n); err != nil {
		t.Fatalf("counting the due rows: %v", err)
	}
	return n
}

// The stop stage lets the publish under way finish, publishes nothing more, and
// gives every row the relay still held back, due at once: the next relay
// publishes them without waiting for a claim to run out, and every staged event
// is published once.
func TestTheRelayStopsWithoutLosingStagedRows(t *testing.T) {
	onEachEngine(t, func(t *testing.T, e engine, dsn string) {
		database := migrated(t, e, dsn)
		l := &ledger{}
		bus := newRecordingBus("stopping", l)
		hold, held := make(chan struct{}), make(chan event.Event, 1)
		bus.hold, bus.held = hold, held
		stopping := newQuick(t, database, bus, quick)
		stopping.start(t.Context())

		ids := stageBacklog(t, database, stopping, 3)
		select {
		case <-held:
		case <-time.After(settle):
			t.Fatalf("the relay never began publishing the backlog")
		}
		stopped := make(chan error, 1)
		go func() { stopped <- stopping.stop(context.Background()) }()
		// The stop stage waits for the publish under way, which the bus lets
		// go a moment after the stop began.
		time.Sleep(50 * time.Millisecond)
		close(hold)
		if err := <-stopped; err != nil {
			t.Fatalf("stopping the relay: %v", err)
		}
		if got := idsOf(l.events()); len(got) != 1 || got[0] != ids[0] {
			t.Fatalf("the stopping relay published %v, want the one under way, %s", got, ids[0])
		}
		if n := claimedBy(t, database, stopping.token); n != 0 {
			t.Errorf("the stopped relay still holds %d rows", n)
		}
		if n := dueUnclaimed(t, database, stopping); n != 2 {
			t.Errorf("%d rows are due and unclaimed after the stop, want the 2 the relay never published", n)
		}

		next := newQuick(t, e.open(t, dsn), newRecordingBus("next", l), quick)
		next.start(t.Context())
		// Well inside the claim the stopped relay would otherwise still hold.
		deadline := time.Now().Add(quick.claim / 2)
		for l.count() < len(ids) && time.Now().Before(deadline) {
			time.Sleep(5 * time.Millisecond)
		}
		seen := map[string]int{}
		for _, evt := range l.events() {
			seen[evt.ID]++
		}
		for _, id := range ids {
			if seen[id] != 1 {
				t.Errorf("event %s was published %d times across the stop, want once", id, seen[id])
			}
		}
	})
}

// A publish still under way when the stop stage's grace runs out is cancelled,
// and the stop reports it; the event stays staged, due at once and with no
// refusal counted against it or recorded as one — the bus refused nothing —
// and the next relay publishes it.
func TestAPublishCutShortAtTheStopStageLeavesItsEventStaged(t *testing.T) {
	onEachEngine(t, func(t *testing.T, e engine, dsn string) {
		database := migrated(t, e, dsn)
		l := &ledger{}
		bus := newRecordingBus("stuck", l)
		held := make(chan event.Event, 1)
		bus.hold, bus.held = make(chan struct{}), held
		short := quick
		short.grace = 200 * time.Millisecond
		written := &records{}
		stuck := newLogged(t, database, bus, short, slog.New(written))
		stuck.start(t.Context())

		ids := stageBacklog(t, database, stuck, 1)
		select {
		case <-held:
		case <-time.After(settle):
			t.Fatalf("the relay never began publishing")
		}
		began := time.Now()
		if err := stuck.stop(context.Background()); err == nil {
			t.Errorf("the stop stage cut a publish short and reported nothing")
		}
		if took := time.Since(began); took > short.grace+short.cutoff+time.Second {
			t.Errorf("the stop stage took %v, want it bounded by its grace of %v", took, short.grace)
		}
		var attempts int
		if err := database.DB().Raw(`SELECT attempts FROM `+tableName+` WHERE event_id = ?`, ids[0]).
			Row().Scan(&attempts); err != nil {
			t.Fatalf("reading the row cut short: %v", err)
		}
		if attempts != 0 || dueUnclaimed(t, database, stuck) != 1 {
			t.Errorf("the row cut short carries %d attempts and %d rows are due and unclaimed, want it given "+
				"back as it was", attempts, dueUnclaimed(t, database, stuck))
		}
		if got := written.atLeast(slog.LevelWarn); len(got) != 0 {
			t.Errorf("the relay recorded %q for a publish the stop stage cut short, which nothing refused", got)
		}

		next := newQuick(t, e.open(t, dsn), newRecordingBus("next", l), quick)
		next.start(t.Context())
		if !eventually(func() bool { return l.count() == 1 }) {
			t.Fatalf("the event cut short at the stop was never published")
		}
		if got := idsOf(l.events()); got[0] != ids[0] {
			t.Errorf("the next relay published %v, want %s", got, ids[0])
		}
	})
}

// A relay that never started stops and closes without complaint, and stopping
// twice is stopping once. What it staged before it stopped stays staged for the
// next relay.
func TestARelayThatNeverStartedStopsCleanly(t *testing.T) {
	onEachEngine(t, func(t *testing.T, e engine, dsn string) {
		database := migrated(t, e, dsn)
		idle, err := newRelay(database, newRecordingBus("idle", &ledger{}), discard, quick)
		if err != nil {
			t.Fatalf("building the relay: %v", err)
		}
		ids := stageBacklog(t, database, idle, 1)
		for range 2 {
			if err := idle.stop(context.Background()); err != nil {
				t.Errorf("stopping a relay that never started: %v", err)
			}
		}
		idle.start(t.Context())
		if n := stagedRows(t, database); n != 1 {
			t.Errorf("the table holds %d rows, want the one staged before the stop", n)
		}

		l := &ledger{}
		next := newQuick(t, database, newRecordingBus("next", l), quick)
		next.start(t.Context())
		if !eventually(func() bool { return l.count() == 1 }) || idsOf(l.events())[0] != ids[0] {
			t.Errorf("the next relay published %v, want %s", idsOf(l.events()), ids[0])
		}
	})
}
