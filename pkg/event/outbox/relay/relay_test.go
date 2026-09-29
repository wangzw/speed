package relay

import (
	"errors"
	"log/slog"
	"regexp"
	"testing"
	"time"

	"gorm.io/gorm"

	"github.com/vislake/speed/pkg/db"
	"github.com/vislake/speed/pkg/event"
	"github.com/vislake/speed/pkg/event/outbox"
)

// uuidV7 is the form Stage assigns an empty identifier in.
var uuidV7 = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)

// An event staged in a transaction is not published while the transaction is
// open, and is published once it commits: with the identifier it was staged
// under, its type and tenant, and a payload whose integer past a float64 is
// exact. Its row is gone once the bus has accepted it.
func TestAnEventIsPublishedOnceTheWriteThatStagedItCommits(t *testing.T) {
	onEachEngine(t, func(t *testing.T, e engine, dsn string) {
		database := migrated(t, e, dsn)
		l := &ledger{}
		r := newQuick(t, database, newRecordingBus("only", l), quick)
		r.start(t.Context())

		staging := make(chan struct{})
		commit := make(chan struct{})
		committed := make(chan error, 1)
		go func() {
			committed <- database.DB().WithContext(t.Context()).Transaction(func(tx *gorm.DB) error {
				if err := tx.Create(&order{ID: "o1", Note: "placed"}).Error; err != nil {
					return err
				}
				if err := r.Stage(tx, event.Event{
					Type: "order.placed", TenantID: "acme", Payload: placed{OrderID: "o1", Amount: pastFloat},
				}); err != nil {
					return err
				}
				close(staging)
				<-commit
				return nil
			})
		}()
		<-staging
		// The relay was told of the staging, and reads the table many times
		// over while the transaction stays open.
		time.Sleep(10 * quick.poll)
		if got := l.events(); len(got) != 0 {
			t.Errorf("the bus accepted %+v while the transaction that staged it was still open", got)
		}
		close(commit)
		if err := <-committed; err != nil {
			t.Fatalf("committing the order: %v", err)
		}

		if !eventually(func() bool { return l.count() > 0 }) {
			t.Fatalf("the committed order's event was never published")
		}
		got := l.events()
		if len(got) != 1 {
			t.Fatalf("the bus accepted %+v, want the one staged event", got)
		}
		evt := got[0]
		if !uuidV7.MatchString(evt.ID) || evt.Type != "order.placed" || evt.TenantID != "acme" {
			t.Errorf("the published event is %+v, want order.placed of acme under an assigned identifier", evt)
		}
		var payload placed
		if err := event.DecodePayload(evt.Payload, &payload); err != nil || payload != (placed{OrderID: "o1", Amount: pastFloat}) {
			t.Errorf("the published payload decodes to %+v (%v), want the order with the amount %d", payload, err, pastFloat)
		}
		if !eventually(func() bool { return stagedRows(t, database) == 0 }) {
			t.Errorf("the table still holds %d rows after the bus accepted the event", stagedRows(t, database))
		}
	})
}

// An identifier the caller set is the one every subscriber receives, so the
// caller can record it beside its write.
func TestAnEventCarriesTheIdentifierItWasStagedUnder(t *testing.T) {
	onEachEngine(t, func(t *testing.T, e engine, dsn string) {
		database := migrated(t, e, dsn)
		l := &ledger{}
		r := newQuick(t, database, newRecordingBus("only", l), quick)
		r.start(t.Context())

		want := event.NewID()
		if err := placeOrder(t.Context(), database, r, "o1", want, false); err != nil {
			t.Fatalf("placing the order: %v", err)
		}
		if !eventually(func() bool { return l.count() > 0 }) {
			t.Fatalf("the committed order's event was never published")
		}
		if got := idsOf(l.events()); len(got) != 1 || got[0] != want {
			t.Errorf("the bus accepted the identifiers %v, want the one the event was staged under, %s", got, want)
		}
	})
}

// A transaction that rolls back takes its staged event with it: nothing is
// published for it, while an event a later transaction committed is.
func TestAnEventStagedByAWriteThatRollsBackIsNeverPublished(t *testing.T) {
	onEachEngine(t, func(t *testing.T, e engine, dsn string) {
		database := migrated(t, e, dsn)
		l := &ledger{}
		r := newQuick(t, database, newRecordingBus("only", l), quick)
		r.start(t.Context())

		abandoned, kept := event.NewID(), event.NewID()
		if err := placeOrder(t.Context(), database, r, "o1", abandoned, true); err != nil {
			t.Fatalf("abandoning the order: %v", err)
		}
		if err := placeOrder(t.Context(), database, r, "o2", kept, false); err != nil {
			t.Fatalf("placing the order: %v", err)
		}
		// The kept event is published after the abandoned one would have
		// been: the relay reads the rows in the order they were staged.
		if !eventually(func() bool { return l.count() > 0 }) {
			t.Fatalf("the committed order's event was never published")
		}
		time.Sleep(10 * quick.poll)
		if got := idsOf(l.events()); len(got) != 1 || got[0] != kept {
			t.Errorf("the bus accepted %v, want only the committed order's %s and never the rolled-back %s",
				got, kept, abandoned)
		}
	})
}

// A bus that refuses an event for a while delays it and loses nothing: the
// row stays staged, carrying the refusals it met, and the event is published
// once the bus takes it, once, under the identifier it was staged under.
func TestABusThatRefusesForAWhileDelaysTheEventButNeverLosesIt(t *testing.T) {
	onEachEngine(t, func(t *testing.T, e engine, dsn string) {
		database := migrated(t, e, dsn)
		l := &ledger{}
		bus := newRecordingBus("only", l)
		bus.setRefuse(func(event.Event) error { return errRefused })
		r := newQuick(t, database, bus, quick)
		r.start(t.Context())

		id := event.NewID()
		if err := placeOrder(t.Context(), database, r, "o1", id, false); err != nil {
			t.Fatalf("placing the order: %v", err)
		}
		if !eventually(func() bool { return bus.refused() >= 3 }) {
			t.Fatalf("the relay tried the refused event %d times, want it tried again after each refusal",
				bus.refused())
		}
		var attempts int
		var lastError string
		if err := database.DB().Raw(`SELECT attempts, last_error FROM `+tableName+` WHERE event_id = ?`, id).
			Row().Scan(&attempts, &lastError); err != nil {
			t.Fatalf("reading the refused row: %v", err)
		}
		if attempts < 3 || lastError != errRefused.Error() {
			t.Errorf("the refused row carries %d attempts and %q, want the refusals it met", attempts, lastError)
		}

		bus.setRefuse(nil)
		if !eventually(func() bool { return l.count() > 0 && stagedRows(t, database) == 0 }) {
			t.Fatalf("the event was not published once the bus took events again: the bus holds %d, the "+
				"table %d", l.count(), stagedRows(t, database))
		}
		time.Sleep(10 * quick.poll)
		if got := idsOf(l.events()); len(got) != 1 || got[0] != id {
			t.Errorf("the bus accepted %v, want the refused event once, %s", got, id)
		}
	})
}

// A refusal of one event holds back no other: an event the bus refuses waits
// out its delay while the events staged after it are published.
func TestARefusedEventHoldsBackNoOther(t *testing.T) {
	onEachEngine(t, func(t *testing.T, e engine, dsn string) {
		database := migrated(t, e, dsn)
		l := &ledger{}
		bus := newRecordingBus("only", l)
		refusedID := event.NewID()
		bus.setRefuse(func(evt event.Event) error {
			if evt.ID == refusedID {
				return errRefused
			}
			return nil
		})
		r := newQuick(t, database, bus, quick)
		r.start(t.Context())

		if err := placeOrder(t.Context(), database, r, "o1", refusedID, false); err != nil {
			t.Fatalf("placing the refused order: %v", err)
		}
		var later []string
		for i := 2; i <= 20; i++ {
			id := event.NewID()
			later = append(later, id)
			if err := placeOrder(t.Context(), database, r, orderID(i), id, false); err != nil {
				t.Fatalf("placing order %d: %v", i, err)
			}
		}
		if !eventually(func() bool { return l.count() == len(later) }) {
			t.Fatalf("the bus accepted %d of the %d events staged after a refused one", l.count(), len(later))
		}
		if stagedRows(t, database) != 1 {
			t.Errorf("the table holds %d rows, want the refused one alone", stagedRows(t, database))
		}
	})
}

// A relay whose table cannot be read keeps reading: it records each failure,
// and once the table is there it publishes what is staged in it.
func TestARelayThatCannotReadItsTableKeepsTrying(t *testing.T) {
	onEachEngine(t, func(t *testing.T, e engine, dsn string) {
		database := e.open(t, dsn)
		l := &ledger{}
		written := &records{}
		r := newLogged(t, database, newRecordingBus("only", l), quick, slog.New(written))
		r.start(t.Context())
		if !eventually(func() bool { return len(written.atLeast(slog.LevelError)) >= 2 }) {
			t.Fatalf("the relay recorded %q reading a table that is not there, want each failure recorded",
				written.atLeast(slog.LevelError))
		}

		migrated(t, e, dsn)
		id := event.NewID()
		if err := placeOrder(t.Context(), database, r, "o1", id, false); err != nil {
			t.Fatalf("placing the order once the table is there: %v", err)
		}
		if !eventually(func() bool { return l.count() == 1 }) {
			t.Fatalf("the relay never published once its table was there")
		}
	})
}

// A session Stage cannot write into is refused before anything is written: the
// handle itself rather than the transaction on it, and a transaction on another
// database's pool — a connection built with Open, or another assembly's handle
// on the same database.
func TestStageRefusesASessionItCannotStageInto(t *testing.T) {
	onEachEngine(t, func(t *testing.T, e engine, dsn string) {
		database := migrated(t, e, dsn)
		r := newQuick(t, database, newRecordingBus("only", &ledger{}), quick)
		evt := event.Event{Type: "order.placed", Payload: placed{OrderID: "o1"}}

		if err := r.Stage(database.DB().WithContext(t.Context()), evt); !errors.Is(err, outbox.ErrNoTransaction) {
			t.Errorf("staging on the handle gave %v, want the refusal of a session in no transaction", err)
		}
		if err := r.Stage(nil, evt); !errors.Is(err, outbox.ErrNoTransaction) {
			t.Errorf("staging on no session gave %v, want the refusal of a session in no transaction", err)
		}

		other := e.open(t, dsn)
		if err := other.DB().Transaction(func(tx *gorm.DB) error { return r.Stage(tx, evt) }); !errors.Is(err, outbox.ErrOtherDatabase) {
			t.Errorf("staging in another assembly's transaction on the same database gave %v, want the "+
				"refusal of another database", err)
		}
		opened, err := database.Open(t.Context(), dsn)
		if err != nil {
			t.Fatalf("building a connection with Open: %v", err)
		}
		t.Cleanup(func() { _ = db.Close(opened) })
		if err := opened.Transaction(func(tx *gorm.DB) error { return r.Stage(tx, evt) }); !errors.Is(err, outbox.ErrOtherDatabase) {
			t.Errorf("staging in a transaction on a connection built with Open gave %v, want the refusal "+
				"of another database", err)
		}
		if n := stagedRows(t, database); n != 0 {
			t.Errorf("the refused stagings left %d rows", n)
		}
	})
}

// A payload that cannot be serialized is refused as Publish refuses it, and the
// caller's transaction is left to roll back with nothing staged.
func TestStageRefusesAPayloadThatCannotBeSerialized(t *testing.T) {
	onEachEngine(t, func(t *testing.T, e engine, dsn string) {
		database := migrated(t, e, dsn)
		r := newQuick(t, database, newRecordingBus("only", &ledger{}), quick)
		err := database.DB().Transaction(func(tx *gorm.DB) error {
			return r.Stage(tx, event.Event{Type: "order.placed", Payload: map[string]any{"f": func() {}}})
		})
		if err == nil {
			t.Fatalf("staging a payload holding a function succeeded")
		}
		if n := stagedRows(t, database); n != 0 {
			t.Errorf("the refused staging left %d rows", n)
		}
	})
}

// A transaction begun on a session db.Connection pinned is on the delivered
// pool, and staging in it works.
func TestStageAcceptsATransactionOnAPinnedConnection(t *testing.T) {
	onEachEngine(t, func(t *testing.T, e engine, dsn string) {
		database := migrated(t, e, dsn)
		l := &ledger{}
		r := newQuick(t, database, newRecordingBus("only", l), quick)
		r.start(t.Context())
		id := event.NewID()
		err := db.Connection(t.Context(), database.DB(), time.Second, func(session *gorm.DB) error {
			return session.Transaction(func(tx *gorm.DB) error {
				return r.Stage(tx, event.Event{ID: id, Type: "order.placed"})
			})
		})
		if err != nil {
			t.Fatalf("staging on a pinned connection: %v", err)
		}
		if !eventually(func() bool { return l.count() == 1 }) {
			t.Fatalf("the event staged on a pinned connection was never published")
		}
	})
}
