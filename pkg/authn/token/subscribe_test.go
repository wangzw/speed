package token

import (
	"context"
	"testing"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/core"
	"github.com/vislake/speed/pkg/event"
	"github.com/vislake/speed/pkg/settings"
)

// TestAnEventIsActedOnOnce holds the deduplication the events design requires: a
// delivery carrying the same identifier is a no-op, while a new identifier and
// an event carrying none are acted on.
func TestAnEventIsActedOnOnce(t *testing.T) {
	p := &product{seen: newSeenEvents()}
	calls := 0
	h := p.once(func(context.Context, event.Event) error { calls++; return nil })
	ctx := context.Background()

	_ = h(ctx, event.Event{ID: "e1", Type: authn.EventAccountDeleted})
	_ = h(ctx, event.Event{ID: "e1", Type: authn.EventAccountDeleted})
	if calls != 1 {
		t.Errorf("one event was acted on %d times, want 1", calls)
	}

	_ = h(ctx, event.Event{ID: "e2", Type: authn.EventAccountDeleted})
	if calls != 2 {
		t.Errorf("a second event was dropped: acted on %d times, want 2", calls)
	}

	_ = h(ctx, event.Event{Type: authn.EventAccountDeleted})
	_ = h(ctx, event.Event{Type: authn.EventAccountDeleted})
	if calls != 4 {
		t.Errorf("an event with no identifier was dropped: acted on %d times, want 4", calls)
	}
}

// TestWireRegistersTheCacheClearingSubscriptions holds the module wiring: Init
// (wire) subscribes the handlers that drop the verifying backend's caches, so a
// revocation a replica hears of clears the entry at once instead of at the end of
// check-ttl, and the handler that makes a relaxing settings change visible. It
// subscribes the settings watch because the rig carries both a bus and a store.
// Without the call from wire the bus carries no handler and the caches only ever
// lapse.
func TestWireRegistersTheCacheClearingSubscriptions(t *testing.T) {
	r := newRig(t)
	if err := r.p.wire(core.New()); err != nil {
		t.Fatalf("wire: %v", err)
	}
	got := make(map[string]bool)
	for _, typ := range r.bus.subscribedTypes() {
		got[typ] = true
	}
	want := []string{
		authn.EventSessionRevoked,
		authn.EventAccountStatusChanged,
		authn.EventAccountDeleted,
		authn.EventImpersonationEnded,
		settings.ChangedEventType,
	}
	if len(got) != len(want) {
		t.Errorf("wire registered %d subscriptions, want %d", len(got), len(want))
	}
	for _, typ := range want {
		if !got[typ] {
			t.Errorf("wire did not subscribe %s", typ)
		}
	}
}

// TestAnAccountEventDropsTheCachedSnapshot holds the account cache's clearing: a
// session revocation, an account status change or a deletion drops the snapshot
// cached for the account, so the next verification reads the store again rather
// than serving the old snapshot for check-ttl.
func TestAnAccountEventDropsTheCachedSnapshot(t *testing.T) {
	r := newRig(t)
	if err := r.p.wire(core.New()); err != nil {
		t.Fatalf("wire: %v", err)
	}
	key := string(account)
	seed := func() {
		r.p.caches().accounts.Put(key, authn.Account{ID: account, Status: authn.StatusActive})
		if _, ok := r.p.caches().accounts.Get(key); !ok {
			t.Fatal("the snapshot was not cached to begin with")
		}
	}

	seed()
	r.bus.deliver(authn.EventSessionRevoked,
		authn.SessionRevoked{AccountID: key, Scope: "one", SessionID: "sess-1"})
	if _, ok := r.p.caches().accounts.Get(key); ok {
		t.Error("a session revocation left the cached snapshot in place")
	}

	seed()
	r.bus.deliver(authn.EventAccountStatusChanged, authn.AccountStatusChanged{AccountID: key})
	if _, ok := r.p.caches().accounts.Get(key); ok {
		t.Error("a status change left the cached snapshot in place")
	}

	seed()
	r.bus.deliver(authn.EventAccountDeleted, authn.AccountDeleted{AccountID: key})
	if _, ok := r.p.caches().accounts.Get(key); ok {
		t.Error("a deletion left the cached snapshot in place")
	}
}

// TestAnImpersonationEndedEventDropsTheCachedRecord holds the impersonation
// cache's clearing: ending a record drops what the backend cached for it, so the
// next verification asks the capability again rather than serving the old answer
// for check-ttl.
func TestAnImpersonationEndedEventDropsTheCachedRecord(t *testing.T) {
	r := newRig(t)
	if err := r.p.wire(core.New()); err != nil {
		t.Fatalf("wire: %v", err)
	}
	const grantID = "grant-1"
	r.p.caches().grants.Put(grantID, grantEntry{active: true})
	if _, ok := r.p.caches().grants.Get(grantID); !ok {
		t.Fatal("the record was not cached to begin with")
	}
	r.bus.deliver(authn.EventImpersonationEnded, authn.ImpersonationEnded{GrantID: grantID})
	if _, ok := r.p.caches().grants.Get(grantID); ok {
		t.Error("ending the record left the cached record in place")
	}
}
