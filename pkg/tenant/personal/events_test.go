package personal

import (
	"context"
	"sync"
	"testing"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/event"
	"github.com/vislake/speed/pkg/tenant"
)

type recordingBus struct {
	mu       sync.Mutex
	handlers map[string]event.Handler
}

func newRecordingBus() *recordingBus {
	return &recordingBus{handlers: make(map[string]event.Handler)}
}

func (*recordingBus) Publish(context.Context, event.Event) error { return nil }

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

// The module subscribes both account events.
func TestTheModuleSubscribesTheAccountEvents(t *testing.T) {
	p, _, _, _, _ := unitProduct()
	bus := newRecordingBus()
	p.bus = bus
	p.subscribe()
	for _, eventType := range []string{authn.EventAccountStatusChanged, authn.EventAccountDeleted} {
		if !bus.subscribed(eventType) {
			t.Errorf("the module does not subscribe %s", eventType)
		}
	}
}

// A deletion event finishes the cleanup the synchronous hook did not.
func TestADeletedEventFinishesTheCleanup(t *testing.T) {
	p, roster, memberships, manager, auditor := seededPersonal(t)
	p.bus = newRecordingBus()
	p.subscribe()
	id := TenantID(unitAccount)

	if err := p.bus.(*recordingBus).deliver(t, event.Event{
		ID:      "evt-deleted",
		Type:    authn.EventAccountDeleted,
		Payload: authn.AccountDeleted{AccountID: unitAccount.String()},
	}); err != nil {
		t.Fatalf("the account-deleted event: %v", err)
	}
	if got := roster.rows[id].Status; got != tenant.StatusDeleted {
		t.Errorf("the tenant is %v after the event, want deleted", got)
	}
	if held := memberships.rows[unitAccount]; len(held) != 0 {
		t.Errorf("the account's membership survived the event: %v", held)
	}
	if held := manager.bindings[string(id)]; len(held) != 0 {
		t.Errorf("the tenant's bindings survived the event: %v", held)
	}
	if rows := entriesOfAction(auditor.rows(), personalDeleteAction); len(rows) != 1 {
		t.Errorf("the event wrote %d deletion audit row(s), want one", len(rows))
	}
}

// A status event acts on the account's current state, not the payload's target.
func TestAStatusEventUsesTheAccountsCurrentState(t *testing.T) {
	statusEvent := func(to string) event.Event {
		return event.Event{
			ID:      "evt-status-" + to,
			Type:    authn.EventAccountStatusChanged,
			Payload: authn.AccountStatusChanged{AccountID: unitAccount.String(), From: "active", To: to},
		}
	}

	t.Run("a stale disable leaves an active account's tenant active", func(t *testing.T) {
		p, roster, _, _, _ := seededPersonal(t)
		p.accounts = &fakeAccounts{status: authn.StatusActive}
		p.bus = newRecordingBus()
		p.subscribe()
		if err := p.bus.(*recordingBus).deliver(t, statusEvent(authn.StatusDisabled.String())); err != nil {
			t.Fatalf("the stale status event: %v", err)
		}
		if got := roster.rows[TenantID(unitAccount)].Status; got != tenant.StatusActive {
			t.Errorf("the tenant is %v, want active", got)
		}
	})

	t.Run("a current disable disables the tenant", func(t *testing.T) {
		p, roster, _, _, _ := seededPersonal(t)
		p.accounts = &fakeAccounts{status: authn.StatusDisabled}
		p.bus = newRecordingBus()
		p.subscribe()
		if err := p.bus.(*recordingBus).deliver(t, statusEvent(authn.StatusActive.String())); err != nil {
			t.Fatalf("the status event: %v", err)
		}
		if got := roster.rows[TenantID(unitAccount)].Status; got != tenant.StatusDisabled {
			t.Errorf("the tenant is %v, want disabled", got)
		}
	})
}

// One event delivered twice is acted on once.
func TestARepeatedEventIsHandledOnce(t *testing.T) {
	p, _, _, _, _ := seededPersonal(t)
	reader := &fakeAccounts{status: authn.StatusActive}
	p.accounts = reader
	p.bus = newRecordingBus()
	p.subscribe()

	evt := event.Event{
		ID:      "evt-repeated",
		Type:    authn.EventAccountStatusChanged,
		Payload: authn.AccountStatusChanged{AccountID: unitAccount.String(), To: authn.StatusActive.String()},
	}
	for range 2 {
		if err := p.bus.(*recordingBus).deliver(t, evt); err != nil {
			t.Fatalf("the repeated status event: %v", err)
		}
	}
	if reader.reads != 1 {
		t.Errorf("one event delivered twice read the account %d time(s), want once", reader.reads)
	}
}
