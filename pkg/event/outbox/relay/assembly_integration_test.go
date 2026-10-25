//go:build integration

package relay

import (
	"context"
	"testing"

	"github.com/vislake/speed/pkg/core"
	"github.com/vislake/speed/pkg/db"
	dbpostgres "github.com/vislake/speed/pkg/db/postgres"
	"github.com/vislake/speed/pkg/event"
	"github.com/vislake/speed/pkg/event/outbox"
	eventpostgres "github.com/vislake/speed/pkg/event/postgres"
	"github.com/vislake/speed/pkg/internal/pgpool/pgtest"
)

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
		// The staged table is the source: with no row left and none claimed, the
		// refused announcement has been published and no second delivery is on
		// its way.
		waitSettled(t, database)
		if got := idsOf(orders.events()); len(got) != 1 || got[0] != id {
			t.Errorf("the subscriber received %v, want the refused announcement once, %s", got, id)
		}
		return nil
	})
}
