package relay

import (
	"context"
	"testing"
	"time"

	"gorm.io/gorm"

	"github.com/vislake/speed/pkg/db"
	"github.com/vislake/speed/pkg/event"
)

// Two replicas relaying one table publish every staged event once between
// them, under the identifier it was staged under, and both take part.
//
// The replicas are two assemblies' pools on one database, each with a relay
// and a bus of its own, and every publish takes a while, so a backlog keeps
// both relays claiming at once. A relay that read the due rows without claiming
// them would publish each row its peer was publishing too.
func TestTwoRelaysOnOneTablePublishEachStagedEventOnce(t *testing.T) {
	onEachEngine(t, func(t *testing.T, e engine, dsn string) {
		first := migrated(t, e, dsn)
		second := e.open(t, dsn)
		l := &ledger{}
		busA, busB := newRecordingBus("a", l), newRecordingBus("b", l)
		busA.delay, busB.delay = 3*time.Millisecond, 3*time.Millisecond
		relayA := newQuick(t, first, busA, quick)
		relayB := newQuick(t, second, busB, quick)
		relayA.start(t.Context())
		relayB.start(t.Context())

		const staged = 120
		want := map[string]bool{}
		err := first.DB().WithContext(t.Context()).Transaction(func(tx *gorm.DB) error {
			for i := range staged {
				id := event.NewID()
				want[id] = true
				if err := relayA.Stage(tx, event.Event{ID: id, Type: "order.placed", Payload: placed{OrderID: orderID(i)}}); err != nil {
					return err
				}
			}
			return nil
		})
		if err != nil {
			t.Fatalf("staging the backlog: %v", err)
		}
		if !eventually(func() bool { return l.count() >= staged && stagedRows(t, first) == 0 }) {
			t.Fatalf("the replicas published %d of %d staged events, the table holds %d",
				l.count(), staged, stagedRows(t, first))
		}
		time.Sleep(10 * quick.poll)

		seen := map[string]int{}
		byBus := map[string]int{}
		for _, d := range l.deliveries() {
			seen[d.evt.ID]++
			byBus[d.bus]++
		}
		for id, n := range seen {
			if !want[id] {
				t.Errorf("the replicas published %s, which was never staged", id)
			}
			if n != 1 {
				t.Errorf("the replicas published %s %d times, want once", id, n)
			}
		}
		if len(seen) != staged {
			t.Errorf("the replicas published %d distinct events, want the %d staged", len(seen), staged)
		}
		if byBus["a"] == 0 || byBus["b"] == 0 {
			t.Errorf("the replicas published %v between them, want both to have taken part: the case "+
				"did not exercise two relays at once", byBus)
		}
	})
}

// A row a relay claimed and never settled — a replica that died holding it —
// is published by another relay once the claim runs out, and not before.
func TestARowHeldByARelayThatDiedIsPublishedOnceItsClaimRunsOut(t *testing.T) {
	onEachEngine(t, func(t *testing.T, e engine, dsn string) {
		database := migrated(t, e, dsn)
		held := quick
		held.claim = 600 * time.Millisecond
		dead := newQuick(t, database, newRecordingBus("dead", &ledger{}), held)

		id := event.NewID()
		if err := placeOrder(t.Context(), database, dead, "o1", id, false); err != nil {
			t.Fatalf("placing the order: %v", err)
		}
		claimed, err := dead.claim()
		if err != nil || len(claimed) != 1 {
			t.Fatalf("the relay that dies claimed %+v (%v), want the one row", claimed, err)
		}
		claimedAt := time.Now()

		l := &ledger{}
		live := newQuick(t, e.open(t, dsn), newRecordingBus("live", l), quick)
		live.start(t.Context())
		if !eventually(func() bool { return l.count() > 0 }) {
			t.Fatalf("the row the dead relay held was never published")
		}
		if waited := time.Since(claimedAt); waited < held.claim/2 {
			t.Errorf("the row was published %v after another relay claimed it for %v", waited, held.claim)
		}
		if got := idsOf(l.events()); len(got) != 1 || got[0] != id {
			t.Errorf("the live relay published %v, want the held event once, %s", got, id)
		}
	})
}

// A relay whose publishes are slow gives back the rows its claim no longer
// covers rather than publishing them: once a publish could outlast the claim,
// another relay may be taking the row over, and both would publish it.
func TestASlowRelayGivesBackTheRowsItsClaimNoLongerCovers(t *testing.T) {
	onEachEngine(t, func(t *testing.T, e engine, dsn string) {
		database := migrated(t, e, dsn)
		l := &ledger{}
		slowBus := newRecordingBus("slow", l)
		slowBus.delay = 120 * time.Millisecond
		slowTiming := quick
		slowTiming.claim, slowTiming.publish = 400*time.Millisecond, 250*time.Millisecond
		slow := newQuick(t, database, slowBus, slowTiming)
		slow.start(t.Context())

		ids := stageBacklog(t, database, slow, 6)
		if !eventually(func() bool { return claimedBy(t, database, slow.token) > 0 }) {
			t.Fatalf("the slow relay never claimed the backlog")
		}
		fast := newQuick(t, e.open(t, dsn), newRecordingBus("fast", l), quick)
		fast.start(t.Context())

		if !eventually(func() bool { return l.count() >= len(ids) && stagedRows(t, database) == 0 }) {
			t.Fatalf("the relays published %d of %d events", l.count(), len(ids))
		}
		time.Sleep(2 * slowTiming.claim)
		seen := map[string]int{}
		for _, evt := range l.events() {
			seen[evt.ID]++
		}
		for _, id := range ids {
			if seen[id] != 1 {
				t.Errorf("event %s was published %d times, want once", id, seen[id])
			}
		}
	})
}

// On PostgreSQL a claim neither takes nor waits for the rows another claim is
// taking: while one claim's transaction still holds its rows, a second claim
// answers at once, without them.
//
// Two claims running at once is the case the claimed time cannot cover — both
// read a row as due before either has written its claim — and it is the row
// lock's to settle. A claim that took no lock would wait for the first claim
// and then take its rows as well; one that waited on the lock rather than
// skipping it would stall behind the slowest relay.
func TestAClaimSkipsTheRowsAnotherClaimIsTaking(t *testing.T) {
	e := engines[1]
	dsn := e.locate(t)
	database := migrated(t, e, dsn)
	stager := newQuick(t, database, newRecordingBus("stager", &ledger{}), quick)
	for i := range 3 {
		if err := placeOrder(t.Context(), database, stager, orderID(i), event.NewID(), false); err != nil {
			t.Fatalf("placing order %d: %v", i, err)
		}
	}
	other := newQuick(t, e.open(t, dsn), newRecordingBus("other", &ledger{}), quick)

	tx := database.DB().WithContext(t.Context()).Begin()
	if tx.Error != nil {
		t.Fatalf("beginning the first claim's transaction: %v", tx.Error)
	}
	defer tx.Rollback()
	taken := 0
	err := func() error {
		rows, err := tx.Raw(stager.sql.claim, "first", time.Minute.Milliseconds(), 8).Rows()
		if err != nil {
			return err
		}
		defer func() { _ = rows.Close() }()
		for rows.Next() {
			taken++
		}
		return rows.Err()
	}()
	if err != nil {
		t.Fatalf("the first claim: %v", err)
	}
	if taken != 3 {
		t.Fatalf("the first claim took %d rows, want the 3 staged", taken)
	}

	answered := make(chan []staged, 1)
	failed := make(chan error, 1)
	go func() {
		claimed, err := other.claim()
		if err != nil {
			failed <- err
			return
		}
		answered <- claimed
	}()
	waiting := false
	select {
	case claimed := <-answered:
		if len(claimed) != 0 {
			t.Errorf("the second claim took %+v, which the first claim is taking", claimed)
		}
	case err := <-failed:
		t.Errorf("the second claim failed: %v", err)
	case <-time.After(2 * time.Second):
		waiting = true
		t.Errorf("the second claim is still waiting for the rows the first claim is taking")
	}
	if err := tx.Commit().Error; err != nil {
		t.Fatalf("committing the first claim: %v", err)
	}
	if !waiting {
		return
	}
	select {
	case claimed := <-answered:
		if len(claimed) != 0 {
			t.Errorf("once the first claim committed, the second took its rows as well: %+v", claimed)
		}
	case err := <-failed:
		t.Errorf("the second claim failed: %v", err)
	case <-time.After(settle):
	}
}

// claimedBy counts the rows a relay token holds.
func claimedBy(t *testing.T, database db.Database, token string) int64 {
	t.Helper()
	var n int64
	if err := database.DB().WithContext(context.Background()).
		Raw(`SELECT count(*) FROM `+tableName+` WHERE claimed_by = ?`, token).Row().Scan(&n); err != nil {
		t.Fatalf("counting the claimed rows: %v", err)
	}
	return n
}
