//go:build integration

package relay

import (
	"context"
	"testing"

	"gorm.io/gorm"

	"github.com/vislake/speed/pkg/core"
	"github.com/vislake/speed/pkg/db"
	dbpostgres "github.com/vislake/speed/pkg/db/postgres"
	"github.com/vislake/speed/pkg/event"
	"github.com/vislake/speed/pkg/event/outbox"
	eventpostgres "github.com/vislake/speed/pkg/event/postgres"
	"github.com/vislake/speed/pkg/internal/pgpool/pgtest"
)

// The bus's tables, as this package's cases read them. The bus keeps its own
// outbox in the same database as the relay's staged table, so a case that
// resolves the database can judge what the bus still owes its subscribers.
const (
	busOutbox    = "event_outbox"
	busDelivered = "event_delivered"
)

// busSettled waits, within the settle bound, for the PostgreSQL bus sharing the
// case's database to have delivered and marked every entry staged under
// identifier. Publish writes the entry and returns; the bus's reader hands it
// to the subscribers later, and records the delivery mark after doing so. A
// count of what the subscriber received is therefore final only once the mark
// is there: an entry the reader redelivers — because the mark of an earlier
// attempt failed, say — is handed over and marked again, and this waits for
// that last mark. Waiting on the relay's staged table alone would leave exactly
// that delivery, and any redelivery, outside what the count is judged on.
func busSettled(t *testing.T, handle *gorm.DB, identifier string) {
	t.Helper()
	settled := func() bool {
		var staged int64
		if err := handle.Raw(`SELECT count(*) FROM `+busOutbox+` WHERE identifier = ?`, identifier).
			Row().Scan(&staged); err != nil {
			t.Fatalf("counting the bus's entries for %s: %v", identifier, err)
		}
		if staged == 0 {
			return false
		}
		var undelivered int64
		if err := handle.Raw(`SELECT count(*) FROM `+busOutbox+` o WHERE o.identifier = ? AND NOT EXISTS (`+
			`SELECT 1 FROM `+busDelivered+` d WHERE d.event_id = o.id)`, identifier).
			Row().Scan(&undelivered); err != nil {
			t.Fatalf("counting the bus's undelivered entries for %s: %v", identifier, err)
		}
		return undelivered == 0
	}
	if !eventually(settled) {
		t.Fatalf("the bus still held an entry staged under %s undelivered after it should have settled", identifier)
	}
}

// postgresStack is the PostgreSQL engine and the bus whose outbox is a table in
// the same database.
func postgresStack(t *testing.T) (stack, string) {
	dsn := pgtest.Acquire(t)
	return stack{
		database: dbpostgres.Module(),
		bus:      eventpostgres.Module(),
		section: map[string]any{
			"db":    map[string]any{"postgres": map[string]any{"dsn": dsn}},
			"event": map[string]any{"postgres": map[string]any{"dsn": dsn}},
		},
	}, dsn
}

// A won deal whose announcement the bus refuses while the deal is moved is
// counted once the bus takes events again: the event stays staged through the
// refusals and is published once. The bus here keeps its own outbox in the same
// PostgreSQL database, and a constraint on that outbox is what refuses the type.
func TestAWinWhoseAnnouncementTheBusRefusedIsPublishedOnceItAcceptsAgain(t *testing.T) {
	s, _ := postgresStack(t)
	orders := &ordersModule{}
	assembled(t, s, orders, func(ctx context.Context, reg *core.Registry) error {
		database, err := core.Resolve[db.Database](reg)
		if err != nil {
			t.Errorf("resolving the database: %v", err)
			return errCaseFailed
		}
		o, err := core.Resolve[outbox.Outbox](reg)
		if err != nil {
			t.Errorf("resolving the outbox: %v", err)
			return errCaseFailed
		}
		handle := database.DB().WithContext(ctx)
		refusal := `ALTER TABLE event_outbox ADD CONSTRAINT case_refuses_orders CHECK (type <> 'order.placed')`
		if err := handle.Exec(refusal).Error; err != nil {
			t.Errorf("making the bus refuse the type: %v", err)
			return errCaseFailed
		}
		id := event.NewID()
		if err := placeOrder(t.Context(), database, o, "o1", id, false); err != nil {
			t.Errorf("placing the order while the bus refuses its announcement: %v", err)
			return errCaseFailed
		}
		var attempts int
		refused := eventually(func() bool {
			return handle.Raw(`SELECT attempts FROM `+tableName+` WHERE event_id = ?`, id).Row().Scan(&attempts) == nil &&
				attempts > 0
		})
		if !refused {
			t.Errorf("the staged row never met the bus's refusal")
			return errCaseFailed
		}
		if len(orders.events()) != 0 {
			t.Errorf("the subscriber received %+v while the bus refused the type", orders.events())
		}
		if err := handle.Exec(`ALTER TABLE event_outbox DROP CONSTRAINT case_refuses_orders`).Error; err != nil {
			t.Errorf("lifting the refusal: %v", err)
			return errCaseFailed
		}
		if !eventually(func() bool { return len(orders.events()) > 0 }) {
			t.Errorf("the announcement never reached its subscriber once the bus accepted again")
			return errCaseFailed
		}
		// Both sources have to be quiet before the subscriber's count is final.
		// The staged table says the relay published the announcement and will
		// publish nothing more; the bus's delivery mark says its reader has
		// handed the entry over and will not hand it over again. This bus's
		// Publish returns before the delivery, so the mark is the only fact
		// that makes the count below final.
		waitSettled(t, database)
		busSettled(t, handle, id)
		if got := idsOf(orders.events()); len(got) != 1 || got[0] != id {
			t.Errorf("the subscriber received %v, want the refused announcement once, %s", got, id)
		}
		return nil
	})
}
