package sms

import (
	"container/list"
	"context"
	"sync"

	"github.com/vislake/speed/pkg/authn"
)

// seenCapacity is how many event identifiers the compensation remembers. The bus
// delivers an event at least once on some transports, and the cleanup is
// idempotent either way, so the memory only saves a repeated delete and cannot
// make the cleanup wrong: it is bounded, and an identifier that has fallen out
// is simply cleaned again.
const seenCapacity = 1024

// AccountCreated has nothing to build: a new account has no challenge, and a
// challenge is stored only when one is asked for. The hook is idempotent and
// returns nil.
func (p *product) AccountCreated(context.Context, authn.AccountID) error { return nil }

// AccountDisabled leaves the account's challenges where they are: the login rows
// carry no account, the mfa and enroll rows belong to an account that may be
// enabled again, and a disabled account cannot receive or submit a code anyway.
func (p *product) AccountDisabled(context.Context, authn.AccountID) error { return nil }

// AccountEnabled changes nothing: the challenges of a disabled account stayed
// where they were, and an enabled account sends and submits codes again.
func (p *product) AccountEnabled(context.Context, authn.AccountID) error { return nil }

// ServiceSecretRevoked has nothing to do: a service secret is not a challenge.
func (p *product) ServiceSecretRevoked(context.Context, authn.AccountID, string) error { return nil }

// AccountDeleted removes the account's mfa and enroll challenges. The login
// rows carry no account and go with the sweep. A call that finds nothing to
// remove returns nil, which is what makes the cleanup idempotent and the
// repeated call of a failed one finish the work.
func (p *product) AccountDeleted(ctx context.Context, id authn.AccountID) error {
	if id == "" {
		return authn.ErrInvalidAccountID
	}
	if _, err := p.register.deleteAccount(ctx, string(id)); err != nil {
		return p.unavailable(err)
	}
	return nil
}

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
