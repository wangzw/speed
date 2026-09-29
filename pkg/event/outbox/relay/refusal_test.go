package relay

import (
	"context"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/vislake/speed/pkg/event"
)

// funcBus is a bus whose every publish one function answers.
type funcBus struct {
	publish func(ctx context.Context, evt event.Event) error
}

func (b funcBus) Subscribe(string, event.Handler) {}

func (b funcBus) Publish(ctx context.Context, evt event.Event) error { return b.publish(ctx, evt) }

// A bus that panics has refused the event: the relay records the refusal on
// the row, and goes on to publish the events staged after it.
func TestABusThatPanicsHasRefusedTheEvent(t *testing.T) {
	onEachEngine(t, func(t *testing.T, e engine, dsn string) {
		database := migrated(t, e, dsn)
		l := &ledger{}
		accepting := newRecordingBus("only", l)
		broken := event.NewID()
		bus := funcBus{publish: func(ctx context.Context, evt event.Event) error {
			if evt.ID == broken {
				panic("the bus broke")
			}
			return accepting.Publish(ctx, evt)
		}}
		r := newQuick(t, database, bus, quick)
		r.start(t.Context())

		later := event.NewID()
		for i, id := range []string{broken, later} {
			if err := placeOrder(t.Context(), database, r, orderID(i), id, false); err != nil {
				t.Fatalf("placing order %d: %v", i, err)
			}
		}
		var met refusal
		if !eventually(func() bool { met = refusalOf(t, database, broken); return met.attempts >= 1 }) {
			t.Fatalf("the row whose publish panicked carries %d attempts, want the panic counted as a refusal",
				met.attempts)
		}
		if !strings.Contains(met.lastError, "the bus panicked: the bus broke") {
			t.Errorf("the row whose publish panicked records %q, want the panic", met.lastError)
		}
		if !eventually(func() bool { return slices.Contains(idsOf(l.events()), later) }) {
			t.Fatalf("the relay published %v after the bus panicked, want the event staged after it, %s",
				idsOf(l.events()), later)
		}
	})
}

// A publish is bounded: the bus is handed a context that ends at the publish
// bound, and a publish that ends with it has refused the event, which the
// relay records before going on to the events staged after it.
func TestAPublishThatOutlastsItsBoundHasRefusedTheEvent(t *testing.T) {
	onEachEngine(t, func(t *testing.T, e engine, dsn string) {
		database := migrated(t, e, dsn)
		l := &ledger{}
		accepting := newRecordingBus("only", l)
		stuck := event.NewID()
		bus := funcBus{publish: func(ctx context.Context, evt event.Event) error {
			if evt.ID == stuck {
				<-ctx.Done()
				return ctx.Err()
			}
			return accepting.Publish(ctx, evt)
		}}
		bounded := quick
		bounded.publish = 100 * time.Millisecond
		r := newQuick(t, database, bus, bounded)
		r.start(t.Context())

		later := event.NewID()
		for i, id := range []string{stuck, later} {
			if err := placeOrder(t.Context(), database, r, orderID(i), id, false); err != nil {
				t.Fatalf("placing order %d: %v", i, err)
			}
		}
		var met refusal
		if !eventually(func() bool { met = refusalOf(t, database, stuck); return met.attempts >= 1 }) {
			t.Fatalf("the row whose publish never returned carries %d attempts, want the publish ended at "+
				"its bound and counted as a refusal", met.attempts)
		}
		if met.lastError != context.DeadlineExceeded.Error() {
			t.Errorf("the row whose publish never returned records %q, want the bound's deadline", met.lastError)
		}
		if !eventually(func() bool { return slices.Contains(idsOf(l.events()), later) }) {
			t.Fatalf("the relay published %v past a publish that never returned, want the event staged "+
				"after it, %s", idsOf(l.events()), later)
		}
	})
}

// A refused row falls due again after a delay that starts at the retry and
// doubles with every further refusal, up to its cap.
//
// The delays are hours, so the relay never reaches one within the case; the
// case stands in for the clock, making the row due each time it has read the
// delay.
func TestARefusedEventFallsDueAfterADelayThatDoublesUpToItsCap(t *testing.T) {
	onEachEngine(t, func(t *testing.T, e engine, dsn string) {
		database := migrated(t, e, dsn)
		bus := newRecordingBus("only", &ledger{})
		bus.setRefuse(func(event.Event) error { return errRefused })
		delayed := quick
		delayed.retry, delayed.retryCap = time.Hour, 3*time.Hour
		r := newQuick(t, database, bus, delayed)
		r.start(t.Context())

		id := event.NewID()
		if err := placeOrder(t.Context(), database, r, "o1", id, false); err != nil {
			t.Fatalf("placing the order: %v", err)
		}
		for i, want := range []time.Duration{time.Hour, 2 * time.Hour, 3 * time.Hour, 3 * time.Hour} {
			refusals := i + 1
			var met refusal
			if !eventually(func() bool { met = refusalOf(t, database, id); return met.attempts >= refusals }) {
				t.Fatalf("the row carries %d attempts, want refusal %d met", met.attempts, refusals)
			}
			if met.attempts != refusals || met.dueIn > want || met.dueIn <= want-settle {
				t.Errorf("after refusal %d the row carries %d attempts and falls due in %v, want it due in %v",
					refusals, met.attempts, met.dueIn, want)
			}
			if err := database.DB().Exec(`UPDATE `+tableName+` SET available_at = `+r.sql.now+
				` WHERE event_id = ?`, id).Error; err != nil {
				t.Fatalf("making the refused row due: %v", err)
			}
		}
	})
}

// A relay whose claim on a row ran out, and another relay took the row over,
// leaves the other relay's claim alone when the bus refuses its publish: the
// row stays claimed by the relay now holding it, with no refusal counted, so
// it is not made due again while that relay is publishing it.
func TestARefusalAfterTheClaimWasTakenOverLeavesTheNewClaimAlone(t *testing.T) {
	onEachEngine(t, func(t *testing.T, e engine, dsn string) {
		database := migrated(t, e, dsn)
		refusing := newRecordingBus("lapsed", &ledger{})
		refusing.setRefuse(func(event.Event) error { return errRefused })
		lapsing := quick
		lapsing.claim = 50 * time.Millisecond
		lapsed := newQuick(t, database, refusing, lapsing)

		id := event.NewID()
		if err := placeOrder(t.Context(), database, lapsed, "o1", id, false); err != nil {
			t.Fatalf("placing the order: %v", err)
		}
		held, err := lapsed.claim()
		if err != nil || len(held) != 1 {
			t.Fatalf("the first relay claimed %+v (%v), want the one row", held, err)
		}
		holding := quick
		holding.claim = time.Minute
		next := newQuick(t, e.open(t, dsn), newRecordingBus("next", &ledger{}), holding)
		if !eventually(func() bool { taken, err := next.claim(); return err == nil && len(taken) == 1 }) {
			t.Fatalf("the next relay never took over the row once the first relay's claim ran out")
		}

		lapsed.relayOne(held[0])
		if met := refusalOf(t, database, id); met.claimedBy != next.token || met.attempts != 0 || met.dueIn <= 0 {
			t.Errorf("after the first relay's publish was refused the row is claimed by %q with %d attempts "+
				"and falls due in %v, want it still held by the relay that took it over, %q, with no refusal",
				met.claimedBy, met.attempts, met.dueIn, next.token)
		}
	})
}
