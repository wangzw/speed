package mfa

import (
	"container/list"
	"context"
	"fmt"
	"log/slog"
	"sync"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/event"
	"github.com/vislake/speed/pkg/settings"
)

// seenCapacity is how many event identifiers the compensation remembers. The bus
// delivers an event at least once on some transports, and the cleanup is
// idempotent either way, so the memory only saves a repeated read and cannot
// make the cleanup wrong: it is bounded, and an identifier that has fallen out
// is simply cleaned again.
const seenCapacity = 1024

// seenEvents is a bounded set of event identifiers a handler has finished with.
type seenEvents struct {
	mu    sync.Mutex
	order *list.List
	ids   map[string]*list.Element
}

func newSeenEvents() *seenEvents {
	return &seenEvents{order: list.New(), ids: make(map[string]*list.Element, seenCapacity)}
}

// has reports whether id was marked.
func (s *seenEvents) has(id string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, ok := s.ids[id]
	return ok
}

// mark remembers id, forgetting the oldest when the set is full.
func (s *seenEvents) mark(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.ids[id]; ok {
		return
	}
	s.ids[id] = s.order.PushBack(id)
	if s.order.Len() > seenCapacity {
		oldest := s.order.Front()
		s.order.Remove(oldest)
		if v, ok := oldest.Value.(string); ok {
			delete(s.ids, v)
		}
	}
}

// subscribe registers the compensation for the account deletion and the
// relaxation monitor: the event that follows a deletion has this module clean
// the account's factors again, which finishes what a hook that failed left, and
// a settings change that moves a watched setting to a more permissive value is
// made visible after the fact. It runs in Init, where the bus accepts
// subscriptions. The settings subscription is made only when the assembly also
// has a settings store: without one no setting changes, so there is nothing to
// watch and the handler is not wired.
func (m *product) subscribe(bus event.Bus) {
	bus.Subscribe(authn.EventAccountDeleted, m.onAccountDeleted)
	if m.settings.store != nil {
		bus.Subscribe(settings.ChangedEventType, m.onSettingsChanged)
	}
}

// onAccountDeleted is the handler of the compensation. It cleans the account in
// the event and marks the event done only once the cleaning succeeded, so a
// delivery that failed is tried again by the next one. An event that cannot be
// read is dropped, which is what a subscriber does with what it cannot read, and
// a handler that returns an error leaves the redelivery to the bus.
func (m *product) onAccountDeleted(ctx context.Context, evt event.Event) error {
	if evt.ID != "" && m.seen.has(evt.ID) {
		return nil
	}
	var payload authn.AccountDeleted
	if err := event.DecodePayload(evt.Payload, &payload); err != nil {
		m.logger.Warn(ModuleName+": an account-deleted event could not be read", slog.String("cause", err.Error()))
		return nil
	}
	account, err := authn.ParseAccountID(payload.AccountID)
	if err != nil {
		m.logger.Warn(ModuleName + ": an account-deleted event names an identifier that is not one")
		return nil
	}
	if err := m.AccountDeleted(ctx, account); err != nil {
		return fmt.Errorf("cleaning the factors of a deleted account: %w", err)
	}
	if evt.ID != "" {
		m.seen.mark(evt.ID)
	}
	return nil
}
