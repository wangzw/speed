package main

// The cases in this file hold the event compensation: the module subscribes the
// two account events, and each one reruns the same idempotent hook the account
// module calls synchronously. The status-changed handler reads the account's
// current state, so a late or reordered event cannot apply a state the account
// has already left.

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/event"
	"github.com/vislake/speed/pkg/tenant"
)

// recordingBus is the event.Bus of these cases: it keeps the handlers subscribed
// to it and delivers an event to the one registered for its type.
type recordingBus struct {
	mu       sync.Mutex
	handlers map[string]event.Handler
}

func newRecordingBus() *recordingBus {
	return &recordingBus{handlers: make(map[string]event.Handler)}
}

func (b *recordingBus) Publish(context.Context, event.Event) error { return nil }

func (b *recordingBus) Subscribe(eventType string, name string, h event.Handler) {
	if name == "" {
		panic("a subscription without a name")
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	b.handlers[eventType] = h
}

func (b *recordingBus) deliver(t *testing.T, evt event.Event) error {
	t.Helper()
	b.mu.Lock()
	h, ok := b.handlers[evt.Type]
	b.mu.Unlock()
	if !ok {
		t.Fatalf("nothing is subscribed to %s", evt.Type)
	}
	return h(context.Background(), evt)
}

func (b *recordingBus) subscribed(eventType string) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	_, ok := b.handlers[eventType]
	return ok
}

// stubAccountReader answers the account's current state, and counts the reads so
// a case can tell a second delivery of one event from a second event.
type stubAccountReader struct {
	kind   authn.AccountKind
	status authn.AccountStatus
	err    error
	reads  atomic.Int64
}

func (s *stubAccountReader) Get(_ context.Context, id authn.AccountID) (authn.Account, error) {
	s.reads.Add(1)
	if s.err != nil {
		return authn.Account{}, s.err
	}
	return authn.Account{ID: id, Kind: s.kind, Status: s.status}, nil
}

// TestTheModuleSubscribesTheAccountEvents holds the two subscriptions the
// compensation is made of. They are registered whenever the assembly carries a
// bus, whether or not the route's switch is on.
func TestTheModuleSubscribesTheAccountEvents(t *testing.T) {
	product, _, _, _, _ := unitProductWith(personalConfig{Enabled: false})
	bus := newRecordingBus()
	product.bus = bus
	product.subscribe()

	for _, eventType := range []string{authn.EventAccountStatusChanged, authn.EventAccountDeleted} {
		if !bus.subscribed(eventType) {
			t.Errorf("the module does not subscribe %s", eventType)
		}
	}
}

// TestADeletedEventFinishesTheCleanup holds the compensation the event exists
// for: the synchronous hook did not run, and the event finishes what it would
// have done.
func TestADeletedEventFinishesTheCleanup(t *testing.T) {
	product, roster, memberships, manager, auditor := seededPersonal(t)
	product.bus = newRecordingBus()
	product.subscribe()
	id := personalTenantID(unitAccount)

	if err := product.bus.(*recordingBus).deliver(t, event.Event{
		ID:      "evt-deleted",
		Type:    authn.EventAccountDeleted,
		Payload: authn.AccountDeleted{AccountID: unitAccount.String()},
	}); err != nil {
		t.Fatalf("the account-deleted event: %v", err)
	}
	if got := roster.rows[id].Status; got != tenant.StatusDeleted {
		t.Errorf("the tenant's status after the event is %v, want deleted", got)
	}
	if held := memberships.rows[unitAccount]; len(held) != 0 {
		t.Errorf("the account's membership survived the event: %v", held)
	}
	if held := manager.bindings[string(id)]; len(held) != 0 {
		t.Errorf("the tenant's bindings survived the event: %v", held)
	}
	if held := manager.roles[string(id)]; len(held) != 0 {
		t.Errorf("the owner role survived the event: %v", held)
	}
	if rows := entriesOfAction(auditor.rows(), deleteAuditAction); len(rows) != 1 {
		t.Errorf("the event wrote %d deletion audit row(s), want one", len(rows))
	}
}

// TestAStatusChangedEventUsesTheAccountsCurrentState holds the guard against a
// stale event: the handler acts on what the account is now, not on the target
// the event carries.
func TestAStatusChangedEventUsesTheAccountsCurrentState(t *testing.T) {
	statusEvent := func(to string) event.Event {
		return event.Event{
			ID:      "evt-status",
			Type:    authn.EventAccountStatusChanged,
			Payload: authn.AccountStatusChanged{AccountID: unitAccount.String(), From: "active", To: to},
		}
	}

	t.Run("a stale disable leaves an enabled account's tenant active", func(t *testing.T) {
		product, roster, _, _, _ := seededPersonal(t)
		product.accounts = &stubAccountReader{status: authn.StatusActive}
		product.bus = newRecordingBus()
		product.subscribe()
		id := personalTenantID(unitAccount)

		if err := product.bus.(*recordingBus).deliver(t, statusEvent(authn.StatusDisabled.String())); err != nil {
			t.Fatalf("the stale status event: %v", err)
		}
		if got := roster.rows[id].Status; got != tenant.StatusActive {
			t.Errorf("a stale disable event left the tenant %v, want it active", got)
		}
	})

	t.Run("a current disable disables the tenant", func(t *testing.T) {
		product, roster, _, _, _ := seededPersonal(t)
		product.accounts = &stubAccountReader{status: authn.StatusDisabled}
		product.bus = newRecordingBus()
		product.subscribe()
		id := personalTenantID(unitAccount)

		// The payload says active, which is stale; the account is disabled.
		if err := product.bus.(*recordingBus).deliver(t, statusEvent(authn.StatusActive.String())); err != nil {
			t.Fatalf("the status event: %v", err)
		}
		row := roster.rows[id]
		if row.Status != tenant.StatusDisabled || row.DisabledBy != tenant.DisabledByAccount {
			t.Errorf("the tenant is %v/%v after the event, want disabled by the account",
				row.Status, row.DisabledBy)
		}
	})

	t.Run("an account the store no longer holds is deleted", func(t *testing.T) {
		product, roster, _, _, _ := seededPersonal(t)
		product.accounts = &stubAccountReader{err: authn.ErrAccountNotFound}
		product.bus = newRecordingBus()
		product.subscribe()
		id := personalTenantID(unitAccount)

		if err := product.bus.(*recordingBus).deliver(t, statusEvent(authn.StatusDisabled.String())); err != nil {
			t.Fatalf("the status event of a gone account: %v", err)
		}
		if got := roster.rows[id].Status; got != tenant.StatusDeleted {
			t.Errorf("the tenant's status is %v, want deleted: the account is gone", got)
		}
	})
}

// TestARepeatedEventIsHandledOnce holds the de-duplication: one event delivered
// twice is one event, and the handler does not read the account a second time.
func TestARepeatedEventIsHandledOnce(t *testing.T) {
	product, _, _, _, _ := seededPersonal(t)
	reader := &stubAccountReader{status: authn.StatusActive}
	product.accounts = reader
	product.bus = newRecordingBus()
	product.subscribe()

	evt := event.Event{
		ID:      "evt-repeated",
		Type:    authn.EventAccountStatusChanged,
		Payload: authn.AccountStatusChanged{AccountID: unitAccount.String(), To: authn.StatusActive.String()},
	}
	for range 2 {
		if err := product.bus.(*recordingBus).deliver(t, evt); err != nil {
			t.Fatalf("the repeated status event: %v", err)
		}
	}
	if got := reader.reads.Load(); got != 1 {
		t.Errorf("one event delivered twice read the account %d time(s), want once", got)
	}
}
