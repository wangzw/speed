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
		if !eventually(func() bool {
			return l.count() >= staged && stagedRows(t, first) == 0 && claimedRows(t, first) == 0 &&
				busA.inflight() == 0 && busB.inflight() == 0
		}) {
			t.Fatalf("the replicas published %d of %d staged events, the table holds %d (%d claimed)",
				l.count(), staged, stagedRows(t, first), claimedRows(t, first))
		}
		// The relays are the source of these publications: ending their loops
		// waits for any pass still under way to finish, so the count that
		// follows cannot be changed by a publish still to come.
		stopRelays(t, relayA, relayB)

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

// A relay whose publishes are slow gives back the rows its claim no
// longer covers rather than publishing them.
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
		fastBus := newRecordingBus("fast", l)
		fast := newQuick(t, e.open(t, dsn), fastBus, quick)
		fast.start(t.Context())

		if !eventually(func() bool {
			return l.count() >= len(ids) && stagedRows(t, database) == 0 && claimedRows(t, database) == 0 &&
				slowBus.inflight() == 0 && fastBus.inflight() == 0
		}) {
			t.Fatalf("the relays published %d of %d events, the table holds %d (%d claimed)",
				l.count(), len(ids), stagedRows(t, database), claimedRows(t, database))
		}
		// Both relays are the source of these publications. Ending their loops
		// lets any pass still under way finish before the count, so an event a
		// relay was about to publish cannot arrive after it.
		stopRelays(t, slow, fast)
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

func claimedBy(t *testing.T, database db.Database, token string) int64 {
	t.Helper()
	var n int64
	if err := database.DB().WithContext(context.Background()).
		Raw(`SELECT count(*) FROM `+tableName+` WHERE claimed_by = ?`, token).Row().Scan(&n); err != nil {
		t.Fatalf("counting the claimed rows: %v", err)
	}
	return n
}
