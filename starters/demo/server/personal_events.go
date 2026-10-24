package main

// The event compensation for the account lifecycle hooks. The account module
// calls the hooks after its own transaction commits, and stages the account
// events in the same transaction through its outbox. A process that stops between
// the commit and the hooks would otherwise leave the personal tenant as it was,
// so the module also subscribes the account events and runs the same idempotent
// hooks again when a delivery arrives. The event is a hint and never the
// authority: the handler of a status change reads the account's current state
// first, so a late or reordered event cannot apply a state the account has
// already left.

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/event"
)

// personalSeenEventsCapacity bounds the set of event identifiers remembered to
// tell a second delivery from a new event. When it is full the oldest identifier
// is forgotten, and an event that old delivered again is handled again, which is
// harmless: every hook is idempotent.
const personalSeenEventsCapacity = 4096

// personalAccountReader is the read of the account store the compensation makes
// to decide the account's current state. The account module delivers
// authn.Accounts; the narrow interface keeps the test stand-in small.
type personalAccountReader interface {
	Get(ctx context.Context, id authn.AccountID) (authn.Account, error)
}

// subscribe registers the compensation for the two account events. It runs in
// Init, where the bus accepts subscriptions, and it runs whether or not the
// route's switch is on: the switch withdraws the route, and the tenant's
// lifecycle belongs to the hooks the module provides either way.
func (p *personalProduct) subscribe() {
	if p.bus == nil {
		return
	}
	p.bus.Subscribe(authn.EventAccountStatusChanged, personalModuleName, p.once(p.onAccountStatusChanged))
	p.bus.Subscribe(authn.EventAccountDeleted, personalModuleName, p.once(p.onAccountDeleted))
}

// once wraps a handler so that one event is acted on once: the identifier is
// remembered after the handler succeeds, so a handler that failed is called
// again when the event is delivered again.
func (p *personalProduct) once(h event.Handler) event.Handler {
	return func(ctx context.Context, evt event.Event) error {
		if evt.ID != "" && p.seen != nil && p.seen.has(evt.ID) {
			return nil
		}
		if err := h(ctx, evt); err != nil {
			return err
		}
		if evt.ID != "" && p.seen != nil {
			p.seen.add(evt.ID)
		}
		return nil
	}
}

// onAccountStatusChanged reruns the status hook that matches the account's
// current state. The payload's target is not trusted on its own: delivery
// guarantees neither order nor count, and a disable event delivered after the
// account was enabled again would take a tenant out of service for a state the
// account has left. The account is read first; one the store no longer holds is
// a deleted account, and its tenant is wound down through the deletion hook.
func (p *personalProduct) onAccountStatusChanged(ctx context.Context, evt event.Event) error {
	var payload authn.AccountStatusChanged
	if err := event.DecodePayload(evt.Payload, &payload); err != nil {
		return fmt.Errorf("%s: reading %s: %w", personalModuleName, evt.Type, err)
	}
	id, err := authn.ParseAccountID(payload.AccountID)
	if err != nil {
		return fmt.Errorf("%s: %s names no account: %w", personalModuleName, evt.Type, err)
	}
	status, err := p.accountStatus(ctx, id, payload)
	if err != nil {
		return err
	}
	switch status {
	case authn.StatusActive:
		return p.AccountEnabled(ctx, id)
	case authn.StatusDeleted:
		return p.AccountDeleted(ctx, id)
	default:
		return p.AccountDisabled(ctx, id)
	}
}

// onAccountDeleted reruns the deletion hook. The event carries nothing but the
// account identifier, and deletion is terminal, so the current state need not be
// read: the hook is a no-op over a tenant that is already a tombstone.
func (p *personalProduct) onAccountDeleted(ctx context.Context, evt event.Event) error {
	var payload authn.AccountDeleted
	if err := event.DecodePayload(evt.Payload, &payload); err != nil {
		return fmt.Errorf("%s: reading %s: %w", personalModuleName, evt.Type, err)
	}
	id, err := authn.ParseAccountID(payload.AccountID)
	if err != nil {
		return fmt.Errorf("%s: %s names no account: %w", personalModuleName, evt.Type, err)
	}
	return p.AccountDeleted(ctx, id)
}

// accountStatus answers the account's current state for a status-changed event.
// With the account read surface in the assembly it is the authority, and an
// account the store no longer holds reads as deleted. Without that surface the
// event's target is the best evidence available, and a target this build does
// not know is treated as the refusing side.
func (p *personalProduct) accountStatus(ctx context.Context, id authn.AccountID,
	payload authn.AccountStatusChanged,
) (authn.AccountStatus, error) {
	if p.accounts != nil {
		account, err := p.accounts.Get(ctx, id)
		switch {
		case errors.Is(err, authn.ErrAccountNotFound):
			return authn.StatusDeleted, nil
		case err != nil:
			return authn.StatusDisabled, fmt.Errorf("%s: reading the account %s names: %w",
				personalModuleName, id, err)
		}
		return account.Status, nil
	}
	switch payload.To {
	case authn.StatusActive.String():
		return authn.StatusActive, nil
	case authn.StatusDeleted.String():
		return authn.StatusDeleted, nil
	default:
		return authn.StatusDisabled, nil
	}
}

// personalSeenEvents remembers the identifiers of the events handled lately,
// bounded: when it is full the oldest is forgotten.
type personalSeenEvents struct {
	mu       sync.Mutex
	capacity int
	order    []string
	byID     map[string]struct{}
}

func newPersonalSeenEvents(capacity int) *personalSeenEvents {
	return &personalSeenEvents{capacity: capacity, byID: make(map[string]struct{})}
}

func (s *personalSeenEvents) has(id string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, ok := s.byID[id]
	return ok
}

func (s *personalSeenEvents) add(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.byID[id]; ok {
		return
	}
	for len(s.order) >= s.capacity {
		delete(s.byID, s.order[0])
		s.order = s.order[1:]
	}
	s.order = append(s.order, id)
	s.byID[id] = struct{}{}
}
