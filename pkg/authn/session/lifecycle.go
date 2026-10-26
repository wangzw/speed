package session

import (
	"container/list"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/event"
	"github.com/vislake/speed/pkg/settings"
)

// recentEventsCapacity is how many event identifiers are remembered to tell a
// second delivery from a new event.
const recentEventsCapacity = 4096

// AccountCreated changes nothing: a new account has no session, and a session is
// stored only when one is asked for. The hook is idempotent and returns nil.
func (p *product) AccountCreated(context.Context, authn.AccountID) error { return nil }

// AccountDisabled revokes the account's browser and token sessions and clears
// what this replica cached for the account. A service session is left: it lapses
// with the account's status and its valid-after instant, and comes back through
// ForCredential once the account is enabled. The hook is idempotent and reads
// nothing of the account: it is called after the account's transaction committed.
func (p *product) AccountDisabled(ctx context.Context, id authn.AccountID) error {
	_, err := p.RevokeAccount(ctx, id, "", authn.ReasonAccountDisabled)
	return err
}

// AccountEnabled changes nothing: the browser and token sessions of before the
// disable were void by the account's valid-after instant, which enabling does
// not move back, so none of them comes to life again. A service session is not
// stored until it is asked for: ForCredential issues it again once the account
// is enabled. The hook is idempotent and returns nil.
func (p *product) AccountEnabled(context.Context, authn.AccountID) error { return nil }

// AccountDeleted revokes every session of the account and deletes the rows,
// service sessions included, and clears what this replica cached for it. The
// rows are revoked before they are deleted, so a failure in between leaves them
// unusable.
func (p *product) AccountDeleted(ctx context.Context, id authn.AccountID) error {
	if _, err := p.exec(ctx, "revoking a deleted account's sessions",
		"UPDATE "+tableName+" SET revoked_at = ?, revoke_reason = ? WHERE account_id = ? AND revoked_at IS NULL",
		p.now(), authn.ReasonAccountDeleted, string(id)); err != nil {
		return err
	}
	n, err := p.exec(ctx, "deleting a deleted account's sessions",
		"DELETE FROM "+tableName+" WHERE account_id = ?", string(id))
	if err != nil {
		return err
	}
	p.caches().forgetAccount(id)
	if n > 0 {
		p.count(ctx, opRevokedAll)
		p.publishRevoked(ctx, authn.SessionRevoked{
			AccountID: string(id), Scope: "all", ReasonClass: authn.ReasonAccountDeleted,
		})
	}
	return nil
}

// ServiceSecretRevoked revokes the service session of the revoked secret. The
// closed set of reasons has no class for a revoked secret, and an administrator
// is the one who revokes it.
func (p *product) ServiceSecretRevoked(ctx context.Context, _ authn.AccountID, credentialID string) error {
	return p.RevokeCredential(ctx, credentialID, authn.ReasonAdmin)
}

// publishRevoked publishes the revocation, after the row changed. A failure is a
// Warn and never fails the revocation: the replicas that are not told still stop
// serving the session when their cache entry runs out.
func (p *product) publishRevoked(ctx context.Context, payload authn.SessionRevoked) {
	if p.bus == nil {
		return
	}
	// The row is already changed, so the publish is this module's bookkeeping
	// for it and not the caller's work: it runs whatever becomes of ctx.
	err := p.bus.Publish(context.WithoutCancel(ctx), event.Event{Type: authn.EventSessionRevoked, Payload: payload})
	if err != nil {
		p.logger.Warn(diagnosticPrefix+ModuleName+": publishing the revocation failed, so other replicas "+
			"learn of it when their cache entry runs out",
			slog.String("event", authn.EventSessionRevoked), slog.Any("error", err))
	}
}

// count reports one operation on the operations counter, when the assembly has a
// meter.
func (p *product) count(ctx context.Context, operation string) {
	if p.operations == nil {
		return
	}
	p.operations.Add(context.WithoutCancel(ctx), 1,
		metric.WithAttributes(attribute.String(operationAttribute, operation)))
}

// subscribe registers the handlers of the four events this module reads, and the
// settings change it watches when the assembly carries a settings store. The
// subscriptions are made in Init and their types are static. A handler clears
// caches and repeats a cleanup that is idempotent, so a second delivery, an
// earlier one that was lost and a different order all leave the same state.
func (p *product) subscribe() {
	if p.bus == nil {
		return
	}
	p.bus.Subscribe(authn.EventSessionRevoked, ModuleName, p.once(p.onSessionRevoked))
	p.bus.Subscribe(authn.EventImpersonationEnded, ModuleName, p.once(p.onImpersonationEnded))
	p.bus.Subscribe(authn.EventAccountStatusChanged, ModuleName, p.once(p.onAccountStatusChanged))
	p.bus.Subscribe(authn.EventAccountDeleted, ModuleName, p.once(p.onAccountDeleted))
	// A settings change is watched only when there is a settings store to have
	// published it: without one every setting reads as its declared default and
	// there is no change to see.
	if p.settings.store != nil {
		p.bus.Subscribe(settings.ChangedEventType, ModuleName, p.onSettingsChanged)
	}
}

// once wraps a handler so that an event is acted on once: the identifier is
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
			p.seen.add(evt.ID)
		}
		return nil
	}
}

func (p *product) onSessionRevoked(_ context.Context, evt event.Event) error {
	var payload authn.SessionRevoked
	if err := event.DecodePayload(evt.Payload, &payload); err != nil {
		return fmt.Errorf("%s: reading %s: %w", ModuleName, evt.Type, err)
	}
	cs := p.caches()
	switch {
	case payload.Scope == "all" || payload.SessionID == "":
		if payload.AccountID == "" {
			cs.purge()
			return nil
		}
		cs.forgetAccount(authn.AccountID(payload.AccountID))
	default:
		cs.forgetSession(authn.SessionID(payload.SessionID))
		if payload.AccountID != "" {
			cs.accounts.Invalidate(payload.AccountID)
		}
	}
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
	p.caches().forgetGrant(payload.GrantID)
	return nil
}

// onAccountStatusChanged clears the caches for the account and, when the account
// is no longer enabled, repeats the cleanup the hook does, which covers a hook
// that failed or a process that stopped between the commit and the hook.
//
// The event may arrive late and again. Before it revokes anything it reads the
// account: one that is enabled again has moved on, and sessions made since are
// not what the event was about; the ones made before were already void by the
// account's valid-after instant. So a stale disable event cannot revoke a session
// of an account that is enabled.
func (p *product) onAccountStatusChanged(ctx context.Context, evt event.Event) error {
	var payload authn.AccountStatusChanged
	if err := event.DecodePayload(evt.Payload, &payload); err != nil {
		return fmt.Errorf("%s: reading %s: %w", ModuleName, evt.Type, err)
	}
	id, err := authn.ParseAccountID(payload.AccountID)
	if err != nil {
		return fmt.Errorf("%s: %s names no account: %w", ModuleName, evt.Type, err)
	}
	p.caches().forgetAccount(id)
	if payload.To == authn.StatusActive.String() {
		return nil
	}
	acct, err := p.accounts.Get(ctx, id)
	switch {
	case errors.Is(err, authn.ErrAccountNotFound):
		return p.AccountDeleted(ctx, id)
	case err != nil:
		return accountError(err)
	case acct.Status == authn.StatusActive:
		return nil
	}
	return p.AccountDisabled(ctx, id)
}

func (p *product) onAccountDeleted(ctx context.Context, evt event.Event) error {
	var payload authn.AccountDeleted
	if err := event.DecodePayload(evt.Payload, &payload); err != nil {
		return fmt.Errorf("%s: reading %s: %w", ModuleName, evt.Type, err)
	}
	id, err := authn.ParseAccountID(payload.AccountID)
	if err != nil {
		return fmt.Errorf("%s: %s names no account: %w", ModuleName, evt.Type, err)
	}
	return p.AccountDeleted(ctx, id)
}

// recentEvents remembers the identifiers of the events handled lately, bounded:
// when it is full the oldest is forgotten, and an event that old delivered again
// is handled again, which is harmless because every handler is idempotent.
type recentEvents struct {
	mu       sync.Mutex
	capacity int
	byID     map[string]*list.Element
	order    *list.List
}

func newRecentEvents(capacity int) *recentEvents {
	return &recentEvents{capacity: capacity, byID: make(map[string]*list.Element), order: list.New()}
}

func (r *recentEvents) has(id string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	_, ok := r.byID[id]
	return ok
}

func (r *recentEvents) add(id string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.byID[id]; ok {
		return
	}
	for r.order.Len() >= r.capacity {
		oldest := r.order.Front()
		evicted, _ := oldest.Value.(string)
		delete(r.byID, evicted)
		r.order.Remove(oldest)
	}
	r.byID[id] = r.order.PushBack(id)
}
