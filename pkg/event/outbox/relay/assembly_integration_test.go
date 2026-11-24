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

const (
	busOutbox    = "event_outbox"
	busDelivered = "event_delivered"
)

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

// A won deal whose announcement the bus refuses while the deal is moved
// is counted once the bus takes events again.
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
		waitSettled(t, database)
		busSettled(t, handle, id)
		if got := idsOf(orders.events()); len(got) != 1 || got[0] != id {
			t.Errorf("the subscriber received %v, want the refused announcement once, %s", got, id)
		}
		return nil
	})
}
