package token

import (
	"container/list"
	"context"
	"fmt"
	"sync"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/event"
	"github.com/vislake/speed/pkg/settings"
)

// seenCapacity is how many event identifiers the subscriptions remember. The bus
// delivers at least once, so the set only saves a repeated clear; an identifier
// that has fallen out is simply cleared again.
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

// subscribe registers the handlers that drop the caches the verifying backend
// serves from; an event that says a value changed clears its entry at once, and
// check-ttl stays the longest a replica that missed the event serves it.
func (p *product) subscribe() {
	if p.bus == nil {
		return
	}
	p.bus.Subscribe(authn.EventSessionRevoked, ModuleName, p.once(p.onSessionRevoked))
	p.bus.Subscribe(authn.EventAccountStatusChanged, ModuleName, p.once(p.onAccountStatusChanged))
	p.bus.Subscribe(authn.EventAccountDeleted, ModuleName, p.once(p.onAccountDeleted))
	p.bus.Subscribe(authn.EventImpersonationEnded, ModuleName, p.once(p.onImpersonationEnded))
	// Watching a relaxing change needs both the bus to hear it and the store the
	// settings live in: with no store every setting reads as its declared
	// default and there is no change to watch.
	if p.settings.store != nil {
		p.bus.Subscribe(settings.ChangedEventType, ModuleName, p.onSettingsChanged)
	}
}

// once wraps a handler so an event is acted on once: the identifier is
// remembered after the handler succeeds, so a handler that failed is called
// again when the event is delivered again.
func (p *product) once(h func(context.Context, event.Event) error) event.Handler {
	return func(ctx context.Context, evt event.Event) error {
		if evt.ID != "" && p.seen.has(evt.ID) {
			return nil
		}
		if err := h(ctx, evt); err != nil {
			return err
		}
		if evt.ID != "" {
			p.seen.mark(evt.ID)
		}
		return nil
	}
}

func (p *product) onSessionRevoked(_ context.Context, evt event.Event) error {
	var payload authn.SessionRevoked
	if err := event.DecodePayload(evt.Payload, &payload); err != nil {
		return fmt.Errorf("%s: reading %s: %w", ModuleName, evt.Type, err)
	}
	p.forgetAccount(payload.AccountID)
	return nil
}

func (p *product) onAccountStatusChanged(_ context.Context, evt event.Event) error {
	var payload authn.AccountStatusChanged
	if err := event.DecodePayload(evt.Payload, &payload); err != nil {
		return fmt.Errorf("%s: reading %s: %w", ModuleName, evt.Type, err)
	}
	p.forgetAccount(payload.AccountID)
	return nil
}

func (p *product) onAccountDeleted(_ context.Context, evt event.Event) error {
	var payload authn.AccountDeleted
	if err := event.DecodePayload(evt.Payload, &payload); err != nil {
		return fmt.Errorf("%s: reading %s: %w", ModuleName, evt.Type, err)
	}
	p.forgetAccount(payload.AccountID)
	return nil
}

func (p *product) onImpersonationEnded(_ context.Context, evt event.Event) error {
	var payload authn.ImpersonationEnded
	if err := event.DecodePayload(evt.Payload, &payload); err != nil {
		return fmt.Errorf("%s: reading %s: %w", ModuleName, evt.Type, err)
	}
	if payload.GrantID == "" {
		p.caches().grants.Purge()
		return nil
	}
	p.caches().grants.Invalidate(payload.GrantID)
	return nil
}

// forgetAccount drops the snapshot cached for the account. An event that names no
// account clears the whole cache, since a change that cannot be placed may touch
// anything.
func (p *product) forgetAccount(id string) {
	if id == "" {
		p.caches().accounts.Purge()
		return
	}
	p.caches().accounts.Invalidate(id)
}
