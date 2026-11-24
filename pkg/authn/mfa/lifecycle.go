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

const seenCapacity = 1024

type seenEvents struct {
	mu    sync.Mutex
	order *list.List
	ids   map[string]*list.Element
}

func newSeenEvents() *seenEvents {
	return &seenEvents{order: list.New(), ids: make(map[string]*list.Element, seenCapacity)}
}

func (s *seenEvents) has(id string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, ok := s.ids[id]
	return ok
}

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

func (m *product) subscribe(bus event.Bus) {
	bus.Subscribe(authn.EventAccountDeleted, ModuleName, m.onAccountDeleted)
	if m.settings.store != nil {
		bus.Subscribe(settings.ChangedEventType, ModuleName, m.onSettingsChanged)
	}
}

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
