package relay

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"testing/fstest"
	"time"

	"github.com/vislake/speed/pkg/config"
	jsonformat "github.com/vislake/speed/pkg/config/format/json"
	filesource "github.com/vislake/speed/pkg/config/source/file"
	"github.com/vislake/speed/pkg/core"
	"github.com/vislake/speed/pkg/db"
	dbpostgres "github.com/vislake/speed/pkg/db/postgres"
	"github.com/vislake/speed/pkg/db/sqlite"
	"github.com/vislake/speed/pkg/event"
	"github.com/vislake/speed/pkg/event/memory"
	"github.com/vislake/speed/pkg/event/outbox"
	eventpostgres "github.com/vislake/speed/pkg/event/postgres"
	"github.com/vislake/speed/pkg/internal/pgpool/pgtest"
	"github.com/vislake/speed/pkg/log"
)

// stack is the database and the bus an assembled case runs on.
type stack struct {
	database core.Module
	bus      core.Module
	// section is the configuration: the db and event namespaces.
	section map[string]any
}

// sqliteStack is the SQLite engine and the in-process bus.
func sqliteStack(t *testing.T) stack {
	return stack{
		database: sqlite.Module(),
		bus:      memory.Module(),
		section: map[string]any{
			"db": map[string]any{"sqlite": map[string]any{"dsn": filepath.Join(t.TempDir(), "delivered.db")}},
		},
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

// ordersModule stands for a host's module: it keeps the orders table, stages
// its announcements through the outbox, and subscribes to them on the bus.
type ordersModule struct {
	mu       sync.Mutex
	received []event.Event
}

// module is the descriptor.
func (m *ordersModule) module() core.Module {
	return core.Module{
		Name: "orders",
		Requires: []core.Requirement{
			{Token: (*db.Database)(nil)},
			{Token: (*outbox.Outbox)(nil)},
		},
		Resources: []any{db.Migrations{FS: fstest.MapFS{
			"sqlite/0001_orders.sql":   {Data: []byte(createOrders)},
			"postgres/0001_orders.sql": {Data: []byte(createOrders)},
		}}},
		New: func(context.Context, *core.Registry) (any, error) { return m, nil },
		Init: func(_ context.Context, reg *core.Registry, _ any) error {
			bus, err := core.Resolve[event.Bus](reg)
			if err != nil {
				return err
			}
			bus.Subscribe("order.placed", func(_ context.Context, evt event.Event) error {
				m.mu.Lock()
				m.received = append(m.received, evt)
				m.mu.Unlock()
				return nil
			})
			return nil
		},
	}
}

// events is what the subscriber received.
func (m *ordersModule) events() []event.Event {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]event.Event(nil), m.received...)
}

// assembled drives a real registry to Start and runs action there: the stack's
// engine and bus, the log module, this module and the orders module, each
// through its own descriptor, with the configuration loaded the way a host
// loads it and supplied as the instance of the config module. The engine
// applies this module's migrations; the drive stops every module once the
// action returns.
func assembled(t *testing.T, s stack, orders *ordersModule, action func(ctx context.Context, reg *core.Registry) error) {
	t.Helper()
	dir := t.TempDir()
	body := map[string]any{"log": map[string]any{
		"outputs": []any{map[string]any{"to": "file", "format": "json", "path": filepath.Join(dir, "log.json")}},
	}}
	for key, value := range s.section {
		body[key] = value
	}
	encoded, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("encoding the configuration: %v", err)
	}
	locator := filepath.Join(dir, "config.json")
	if err = os.WriteFile(locator, encoded, 0o600); err != nil {
		t.Fatalf("writing the primary config source: %v", err)
	}
	modules := []core.Module{s.database, s.bus, log.Module(), Module(), orders.module()}

	loading := core.New()
	loading.Register(jsonformat.Module())
	loading.Register(filesource.Module())
	loading.Register(core.Module{
		Name:      "host",
		Resources: []any{config.HostIdentity{Prefix: "SPEEDOUTBOXTEST", DefaultLocator: "file://" + locator}},
	})
	for _, m := range modules {
		loading.Register(m)
	}
	reader, err := config.Load(t.Context(), loading, nil)
	if err != nil {
		t.Fatalf("loading the configuration: %v", err)
	}

	reg := core.New()
	reg.Register(config.Module())
	names := make([]string, 0, len(modules))
	for _, m := range modules {
		reg.Register(m)
		names = append(names, m.Name)
	}
	reg.Supply(reader, config.ModuleName)
	if err := reg.Drive(t.Context(), core.Drive{
		Depth:        core.StageStart,
		Participants: names,
		Action:       action,
	}); err != nil {
		t.Fatalf("driving the assembly: %v", err)
	}
}

// errCaseFailed ends a drive whose case has already reported what went wrong.
var errCaseFailed = errors.New("the case failed")

// In an assembled host an order's announcement reaches its subscriber once the
// order commits, under the identifier it was staged under and with its amount
// exact, and an abandoned order's never does. The engine built this module's
// table from the migrations the module declares.
func TestAnAssembledHostAnnouncesACommittedWriteThroughTheOutbox(t *testing.T) {
	orders := &ordersModule{}
	assembled(t, sqliteStack(t), orders, func(ctx context.Context, reg *core.Registry) error {
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
		abandoned, kept := event.NewID(), event.NewID()
		if err := placeOrder(ctx, database, o, "o1", abandoned, true); err != nil {
			t.Errorf("abandoning the order: %v", err)
			return errCaseFailed
		}
		if err := placeOrder(ctx, database, o, "o2", kept, false); err != nil {
			t.Errorf("placing the order: %v", err)
			return errCaseFailed
		}
		if !eventually(func() bool { return len(orders.events()) > 0 }) {
			t.Errorf("the committed order's announcement never reached its subscriber")
			return errCaseFailed
		}
		time.Sleep(time.Second)
		got := orders.events()
		if len(got) != 1 || got[0].ID != kept || got[0].TenantID != "acme" {
			t.Errorf("the subscriber received %+v, want the committed order's %s alone", got, kept)
			return errCaseFailed
		}
		var payload placed
		if err := event.DecodePayload(got[0].Payload, &payload); err != nil || payload.Amount != pastFloat {
			t.Errorf("the subscriber decoded %+v (%v), want the amount %d exact", payload, err, pastFloat)
		}
		return nil
	})
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
		if err := placeOrder(ctx, database, o, "o1", id, false); err != nil {
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
		time.Sleep(time.Second)
		if got := idsOf(orders.events()); len(got) != 1 || got[0] != id {
			t.Errorf("the subscriber received %v, want the refused announcement once, %s", got, id)
		}
		return nil
	})
}

// A command that takes the relay in without a bus fails its startup rather
// than staging events nothing would publish.
func TestAnAssemblyWithoutABusFailsToStart(t *testing.T) {
	reg := core.New()
	reg.Register(core.Module{
		Name:     "database",
		Provides: []core.Provision{{Token: (*db.Database)(nil)}},
		New:      func(context.Context, *core.Registry) (any, error) { return nil, errors.New("not reached") },
	})
	reg.Register(Module())
	err := reg.Drive(t.Context(), core.Drive{Depth: core.StageNew, Participants: []string{"database", ModuleName}})
	if !errors.Is(err, core.ErrMissingProvider) {
		t.Errorf("an assembly with no bus gave %v, want the missing provider named", err)
	}
}
