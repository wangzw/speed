package token

import (
	"container/list"
	"context"
	"fmt"
	"sync"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/event"
)

// seenCapacity is how many event identifiers the subscriptions remember. The bus
// delivers an event at least once on some transports and a handler only drops a
// cache entry either way, so the set saves a repeated clear and cannot make the
// outcome wrong; it is bounded, and an identifier that has fallen out is simply
// cleared again.
const seenCapacity = 1024

// seenEvents is a bounded set of the event identifiers a handler has finished
// with.
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
// serves from. The account snapshot and the impersonation record it read are
// kept for check-ttl; an event that says one changed clears the entry so a
// replica that hears it stops serving the old value at once, and check-ttl stays
// the longest a replica that missed the event can go on serving it. The
// subscriptions are made in Init and their types are static. They are the rows
// the events design lists for a verifying backend.
//
// The session a token names is not cached here: the session module caches it and
// clears it from its own subscription, so this backend reads it through that
// seam. A session revocation therefore drops the account snapshot alone, which
// costs a re-read and is the conservative side.
func (p *product) subscribe() {
	if p.bus == nil {
		return
	}
	p.bus.Subscribe(authn.EventSessionRevoked, p.once(p.onSessionRevoked))
	p.bus.Subscribe(authn.EventAccountStatusChanged, p.once(p.onAccountStatusChanged))
	p.bus.Subscribe(authn.EventAccountDeleted, p.once(p.onAccountDeleted))
	p.bus.Subscribe(authn.EventImpersonationEnded, p.once(p.onImpersonationEnded))
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

// onSessionRevoked drops the snapshot cached for the account the revoked session
// belongs to, so a change that came with the revocation is read again.
func (p *product) onSessionRevoked(_ context.Context, evt event.Event) error {
	var payload authn.SessionRevoked
	if err := event.DecodePayload(evt.Payload, &payload); err != nil {
		return fmt.Errorf("%s: reading %s: %w", ModuleName, evt.Type, err)
	}
	p.forgetAccount(payload.AccountID)
	return nil
}

// onAccountStatusChanged drops the snapshot cached for the account the event
// names, so the next verification reads its status and valid-after again.
func (p *product) onAccountStatusChanged(_ context.Context, evt event.Event) error {
	var payload authn.AccountStatusChanged
	if err := event.DecodePayload(evt.Payload, &payload); err != nil {
		return fmt.Errorf("%s: reading %s: %w", ModuleName, evt.Type, err)
	}
	p.forgetAccount(payload.AccountID)
	return nil
}

// onAccountDeleted drops the snapshot cached for the account the event names.
func (p *product) onAccountDeleted(_ context.Context, evt event.Event) error {
	var payload authn.AccountDeleted
	if err := event.DecodePayload(evt.Payload, &payload); err != nil {
		return fmt.Errorf("%s: reading %s: %w", ModuleName, evt.Type, err)
	}
	p.forgetAccount(payload.AccountID)
	return nil
}

// onImpersonationEnded drops the record cached for the impersonation the event
// names, so the next verification asks the capability again.
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

// forgetAccount drops the snapshot cached for the account. An event that names
// no account clears the whole cache: a change that cannot be placed is treated as
// one that may touch anything, which costs a re-read and never serves a stale
// snapshot.
func (p *product) forgetAccount(id string) {
	if id == "" {
		p.caches().accounts.Purge()
		return
	}
	p.caches().accounts.Invalidate(id)
}
