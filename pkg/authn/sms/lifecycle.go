package sms

import (
	"container/list"
	"context"
	"sync"

	"github.com/vislake/speed/pkg/authn"
)

const seenCapacity = 1024

// AccountCreated changes nothing: a new account has no challenge.
func (p *product) AccountCreated(context.Context, authn.AccountID) error { return nil }

// AccountDisabled leaves the account's challenges where they are.
func (p *product) AccountDisabled(context.Context, authn.AccountID) error { return nil }

// AccountEnabled changes nothing: the challenges of a disabled account stayed
// where they were, and an enabled account sends and submits codes again.
func (p *product) AccountEnabled(context.Context, authn.AccountID) error { return nil }

// ServiceSecretRevoked has nothing to do: a service secret is not a challenge.
func (p *product) ServiceSecretRevoked(context.Context, authn.AccountID, string) error { return nil }

// AccountDeleted removes the account's mfa and enroll challenges; login challenges carry no account and go with the sweep. It is idempotent.
func (p *product) AccountDeleted(ctx context.Context, id authn.AccountID) error {
	if id == "" {
		return authn.ErrInvalidAccountID
	}
	if _, err := p.register.deleteAccount(ctx, string(id)); err != nil {
		return p.unavailable(err)
	}
	return nil
}

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
