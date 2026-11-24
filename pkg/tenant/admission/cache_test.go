package admission

import (
	"context"
	"strconv"
	"testing"
	"time"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/authn/authntest"
	"github.com/vislake/speed/pkg/core"
	"github.com/vislake/speed/pkg/event"
	"github.com/vislake/speed/pkg/settings"
	"github.com/vislake/speed/pkg/tenant"
)

func TestAMembershipIsRememberedForTheLifetimeAndNotLonger(t *testing.T) {
	r := newRig(t)
	for i := 0; i < 3; i++ {
		if rp := r.serve(r.as(acme)); !rp.reached() {
			t.Fatalf("request %d answered %d, want it admitted", i, rp.status)
		}
	}
	if n, _ := r.members.reads(); n != 1 {
		t.Fatalf("three requests read the membership %d times, want 1: the answer is remembered", n)
	}
	r.clock.advance(defaults.CheckTTL - time.Nanosecond)
	r.serve(r.as(acme))
	if n, _ := r.members.reads(); n != 1 {
		t.Fatalf("a request just inside the lifetime read the membership again (%d reads)", n)
	}
	r.clock.advance(time.Nanosecond)
	r.serve(r.as(acme))
	if n, _ := r.members.reads(); n != 2 {
		t.Fatalf("a request at the lifetime read the membership %d times in all, want 2: the lifetime has ended", n)
	}
}

func TestANonMembershipIsRememberedToo(t *testing.T) {
	r := newRig(t)
	r.serve(r.as(acme, authntest.WithAccount(bob)))
	r.members.add(bob, acme)
	if rp := r.serve(r.as(acme, authntest.WithAccount(bob))); rp.reached() {
		t.Fatal("a non-member whose answer is remembered was admitted before the lifetime ended")
	}
	if n, _ := r.members.reads(); n != 1 {
		t.Fatalf("the membership was read %d times, want 1", n)
	}
	r.clock.advance(defaults.CheckTTL)
	if rp := r.serve(r.as(acme, authntest.WithAccount(bob))); !rp.reached() {
		t.Fatal("a new member was still refused after the lifetime ended")
	}
}

func TestAnAnswerIsRememberedPerAccountAndTenant(t *testing.T) {
	r := newRig(t)
	r.members.add(alice, globex)
	r.members.add(bob, acme)
	r.serve(r.as(acme))
	r.serve(r.as(globex))
	r.serve(r.as(acme, authntest.WithAccount(bob)))
	if n, _ := r.members.reads(); n != 3 {
		t.Fatalf("three different (account, tenant) pairs read the membership %d times, want 3", n)
	}
}

func TestAnImpersonationIsKeyedByTheImpersonatedAccount(t *testing.T) {
	r := newRig(t)
	r.members.add(bob, acme)
	r.serve(r.as(acme))
	impersonating := r.as(acme, authntest.WithAccount(alice), authntest.WithActor(bob),
		authntest.WithSession("s"), authntest.WithScope(acme))
	r.serve(impersonating)
	if n, _ := r.members.reads(); n != 1 {
		t.Fatalf("the impersonation of alice read the membership again (%d reads): it is alice's answer", n)
	}
	r.serve(r.as(acme, authntest.WithAccount(bob)))
	if n, _ := r.members.reads(); n != 2 {
		t.Fatalf("bob's own request did not read his membership (%d reads)", n)
	}
}

func TestAnErrorIsNeverRemembered(t *testing.T) {
	r := newRig(t)
	r.members.fail(errBoom)
	if rp := r.serve(r.as(acme)); rp.status != 503 {
		t.Fatalf("answered %d, want 503", rp.status)
	}
	r.members.fail(nil)
	if rp := r.serve(r.as(acme)); !rp.reached() {
		t.Fatalf("the request after the outage answered %d: the failure was remembered", rp.status)
	}
	if n, _ := r.members.reads(); n != 2 {
		t.Fatalf("the membership was read %d times, want 2: the first failed, the second was not served from memory", n)
	}
}

func TestACachedMemberIsAdmittedThroughAnOutageUntilTheLifetimeEnds(t *testing.T) {
	r := newRig(t)
	r.serve(r.as(acme))
	r.members.fail(errBoom)
	if rp := r.serve(r.as(acme)); !rp.reached() {
		t.Fatalf("a member whose answer is remembered answered %d during an outage, want it admitted", rp.status)
	}
	r.clock.advance(defaults.CheckTTL)
	if rp := r.serve(r.as(acme)); rp.status != 503 {
		t.Fatalf("after the lifetime the outage answered %d, want 503: a miss is never answered from nothing", rp.status)
	}
}

func TestAZeroLifetimeSwitchesTheMemoryOff(t *testing.T) {
	r := newRig(t)
	r.withSettings().set(settings.Global(), checkTTLKey, time.Duration(0))
	r.serve(r.as(acme))
	r.serve(r.as(acme))
	if n, _ := r.members.reads(); n != 2 {
		t.Fatalf("with a zero lifetime two requests read the membership %d times, want 2", n)
	}
	if r.l.cache.size() != 0 {
		t.Fatalf("the memory holds %d entries with a zero lifetime", r.l.cache.size())
	}
	r.members.fail(errBoom)
	if rp := r.serve(r.as(acme)); rp.status != 503 {
		t.Fatalf("with no memory an outage answered %d, want 503", rp.status)
	}
}

func TestTheLifetimeIsReadOnEveryRequest(t *testing.T) {
	r := newRig(t)
	store := r.withSettings()
	store.set(settings.Global(), checkTTLKey, 10*time.Second)
	r.serve(r.as(acme))
	r.clock.advance(6 * time.Second)
	r.serve(r.as(acme))
	if n, _ := r.members.reads(); n != 1 {
		t.Fatalf("under a 10s lifetime a 6s-old answer was read again (%d reads)", n)
	}
	store.set(settings.Global(), checkTTLKey, 3*time.Second)
	r.serve(r.as(acme))
	if n, _ := r.members.reads(); n != 2 {
		t.Fatalf("under a 3s lifetime a 6s-old answer was not read again (%d reads)", n)
	}
}

func TestALifetimeTheCodeCannotActOnIsTheDefaultAndIsReported(t *testing.T) {
	for name, apply := range map[string]func(s *settingsStub){
		"unreadable":      func(s *settingsStub) { s.failKey(checkTTLKey, errBoom) },
		"above its range": func(s *settingsStub) { s.set(settings.Global(), checkTTLKey, maxCheckTTL+time.Second) },
		"below zero":      func(s *settingsStub) { s.set(settings.Global(), checkTTLKey, -time.Second) },
	} {
		t.Run(name, func(t *testing.T) {
			r := newRig(t)
			apply(r.withSettings())
			r.serve(r.as(acme))
			r.clock.advance(defaults.CheckTTL - time.Nanosecond)
			r.serve(r.as(acme))
			if n, _ := r.members.reads(); n != 1 {
				t.Fatalf("the default lifetime was not the one acted on: %d reads inside it", n)
			}
			r.clock.advance(time.Nanosecond)
			r.serve(r.as(acme))
			if n, _ := r.members.reads(); n != 2 {
				t.Fatalf("the default lifetime was not the one acted on: %d reads at its end", n)
			}
			errs := r.logs.atLevel(levelError)
			if len(errs) != 1 || errs[0].attrs["setting"] != checkTTLKey {
				t.Fatalf("the Error records are %+v, want one naming %s: the fault is reported once, not once a request", errs, checkTTLKey)
			}
		})
	}
}

func TestTheLifetimeFaultIsReportedAgainOnceItHasBeenCleared(t *testing.T) {
	r := newRig(t)
	store := r.withSettings()
	store.failKey(checkTTLKey, errBoom)
	r.serve(r.as(acme))
	store.failKey(checkTTLKey, nil)
	r.serve(r.as(acme))
	store.failKey(checkTTLKey, errBoom)
	r.serve(r.as(acme))
	if errs := r.logs.atLevel(levelError); len(errs) != 2 {
		t.Fatalf("the Error records are %+v, want two: the fault, then again after it was gone and came back", errs)
	}
}

func TestThePermissionIsRememberedAndAnErrorIsNot(t *testing.T) {
	r := newRig(t)
	d := r.withDecider(true)
	stranger := func() context.Context { return r.as(acme, authntest.WithAccount(bob)) }
	d.err = errBoom
	if rp := r.serve(stranger()); rp.status != 503 {
		t.Fatalf("answered %d, want 503", rp.status)
	}
	d.err = nil
	r.serve(stranger())
	r.serve(stranger())
	if got := len(d.questions()); got != 2 {
		t.Fatalf("the decider was asked %d times, want 2: the failure and one answer, then the memory", got)
	}
	d.mu.Lock()
	d.allow = false
	d.mu.Unlock()
	if rp := r.serve(stranger()); !rp.reached() {
		t.Fatal("a remembered permission was withdrawn before the lifetime ended")
	}
	r.clock.advance(defaults.CheckTTL)
	if rp := r.serve(stranger()); rp.reached() {
		t.Fatal("a permission the decider withdrew was still honoured after the lifetime")
	}
}

func TestTheSSOExemptionIsRemembered(t *testing.T) {
	r := newRig(t)
	r.withProviders()
	r.withSettings().set(settings.ForTenant(acme), requireSSOKey, true)
	r.members.setRow(authn.Membership{Account: alice, Tenant: acme, Active: true, SSOExempt: true})
	multi := authntest.WithAssurance(authn.AssuranceMulti)
	r.serve(r.as(acme, multi))
	r.serve(r.as(acme, multi))
	if _, n := r.members.reads(); n != 1 {
		t.Fatalf("the membership row was read %d times, want 1", n)
	}
}

func membershipChanged(account, tenantID string) event.Event {
	return event.Event{
		ID:       "e-" + account + "-" + tenantID,
		Type:     authn.EventMembershipChanged,
		TenantID: tenantID,
		Payload:  authn.MembershipChanged{AccountID: account, Change: "removed", Source: "admin"},
	}
}

func TestAMembershipEventDropsThatAccountInThatTenantAndNothingElse(t *testing.T) {
	r := newRig(t)
	r.members.add(alice, globex)
	r.members.add(bob, acme)
	for _, ctx := range []func(){
		func() { r.serve(r.as(acme)) },
		func() { r.serve(r.as(globex)) },
		func() { r.serve(r.as(acme, authntest.WithAccount(bob))) },
	} {
		ctx()
	}
	before, _ := r.members.reads()

	if err := r.l.onMembershipChanged(t.Context(), membershipChanged(string(alice), acme)); err != nil {
		t.Fatalf("the handler failed: %v", err)
	}
	r.serve(r.as(globex))
	r.serve(r.as(acme, authntest.WithAccount(bob)))
	if after, _ := r.members.reads(); after != before {
		t.Fatalf("the event about (alice, acme) dropped other entries: %d reads became %d", before, after)
	}
	r.serve(r.as(acme))
	if after, _ := r.members.reads(); after != before+1 {
		t.Fatalf("the event did not drop (alice, acme): %d reads became %d", before, after)
	}
}

func TestAMembershipEventEndsAnAdmissionAtOnce(t *testing.T) {
	r := newRig(t)
	if rp := r.serve(r.as(acme)); !rp.reached() {
		t.Fatalf("answered %d, want it admitted", rp.status)
	}
	r.members.remove(alice, acme)
	if rp := r.serve(r.as(acme)); !rp.reached() {
		t.Fatal("the removal was seen with no event and no time passing: the answer is not remembered")
	}
	if err := r.l.onMembershipChanged(t.Context(), membershipChanged(string(alice), acme)); err != nil {
		t.Fatal(err)
	}
	notFound(t, "the removed member after the event", r.serve(r.as(acme)))
}

func TestAMembershipEventDropsEveryKindOfEntry(t *testing.T) {
	r := newRig(t)
	r.withProviders()
	d := r.withDecider(true)
	r.withSettings().set(settings.ForTenant(acme), requireSSOKey, true)
	r.members.setRow(authn.Membership{Account: alice, Tenant: acme, Active: true, SSOExempt: true})
	multi := authntest.WithAssurance(authn.AssuranceMulti)
	r.serve(r.as(acme, multi))                      // member, exempt
	r.serve(r.as(acme, authntest.WithAccount(bob))) // non-member, permission
	if err := r.l.onMembershipChanged(t.Context(), membershipChanged(string(alice), acme)); err != nil {
		t.Fatal(err)
	}
	if err := r.l.onMembershipChanged(t.Context(), membershipChanged(string(bob), acme)); err != nil {
		t.Fatal(err)
	}
	if size := r.l.cache.size(); size != 0 {
		t.Fatalf("the memory holds %d entries after both accounts' events", size)
	}
	_ = d
}

func TestAMembershipEventIsIdempotent(t *testing.T) {
	r := newRig(t)
	r.serve(r.as(acme))
	for i := 0; i < 3; i++ {
		if err := r.l.onMembershipChanged(t.Context(), membershipChanged(string(alice), acme)); err != nil {
			t.Fatalf("delivery %d failed: %v", i, err)
		}
	}
	if rp := r.serve(r.as(acme)); !rp.reached() {
		t.Fatalf("answered %d after repeated events, want it admitted: the read is of the table", rp.status)
	}
	if err := r.l.onMembershipChanged(t.Context(), membershipChanged("nobody", "nowhere")); err != nil {
		t.Fatal(err)
	}
}

func TestAnEventThatCannotBeReadDownToAKeyDropsEverything(t *testing.T) {
	for name, evt := range map[string]event.Event{
		"no payload":              {Type: authn.EventMembershipChanged, TenantID: acme},
		"a payload of no account": {Type: authn.EventMembershipChanged, TenantID: acme, Payload: authn.MembershipChanged{}},
		"an envelope with no tenant": {
			Type:    authn.EventMembershipChanged,
			Payload: authn.MembershipChanged{AccountID: string(alice)},
		},
		"a payload that is not an object": {Type: authn.EventMembershipChanged, TenantID: acme, Payload: "alice"},
	} {
		t.Run(name, func(t *testing.T) {
			r := newRig(t)
			r.members.add(bob, acme)
			r.serve(r.as(acme))
			r.serve(r.as(acme, authntest.WithAccount(bob)))
			if err := r.l.onMembershipChanged(t.Context(), evt); err != nil {
				t.Fatalf("the handler failed: %v", err)
			}
			if size := r.l.cache.size(); size != 0 {
				t.Fatalf("the memory holds %d entries: an event that cannot be placed must drop all", size)
			}
		})
	}
}

func TestAnEventInTheShapeATransportDeliversIsRead(t *testing.T) {
	r := newRig(t)
	r.serve(r.as(acme))
	evt := event.Event{
		Type: authn.EventMembershipChanged, TenantID: acme,
		Payload: map[string]any{"account_id": string(alice), "change": "removed", "source": "admin"},
	}
	if err := r.l.onMembershipChanged(t.Context(), evt); err != nil {
		t.Fatal(err)
	}
	if r.l.cache.size() != 0 {
		t.Fatal("the decoded payload of a transport did not drop the entry")
	}
}

func TestAnAnswerReadWhileAnEventArrivesIsNotLeftBehind(t *testing.T) {
	r := newRig(t)
	r.members.during = func() {
		_ = r.l.onMembershipChanged(t.Context(), membershipChanged(string(alice), acme))
	}
	if rp := r.serve(r.as(acme)); !rp.reached() {
		t.Fatalf("answered %d, want it admitted: the read saw a member", rp.status)
	}
	r.members.mu.Lock()
	r.members.during = nil
	r.members.mu.Unlock()
	r.serve(r.as(acme))
	if n, _ := r.members.reads(); n != 2 {
		t.Fatalf("the membership was read %d times, want 2: an answer that straddled the event must not be remembered", n)
	}
}

func TestAnAnswerReadWhileAClearArrivesIsNotLeftBehind(t *testing.T) {
	r := newRig(t)
	r.members.during = func() {
		_ = r.l.onMembershipChanged(t.Context(), event.Event{
			Type:    authn.EventMembershipChanged,
			Payload: authn.MembershipChanged{AccountID: string(alice)},
		})
	}
	if rp := r.serve(r.as(acme)); !rp.reached() {
		t.Fatalf("answered %d, want it admitted: the read saw a member", rp.status)
	}
	r.members.mu.Lock()
	r.members.during = nil
	r.members.mu.Unlock()
	r.serve(r.as(acme))
	if n, _ := r.members.reads(); n != 2 {
		t.Fatalf("the membership was read %d times, want 2: an answer that straddled the clear must not be remembered", n)
	}
}

func TestTheSubscriptionIsMadeInInitAndWithoutABusTheLifetimeAlone(t *testing.T) {
	bus := newBusStub()
	f := fixture{supplied: []supply{withBus(bus)}}
	if err := f.drive(t, core.StageInit, nil); err != nil {
		t.Fatalf("the drive failed: %v", err)
	}
	if n := bus.subscribed(authn.EventMembershipChanged); n != 1 {
		t.Fatalf("%d handlers are subscribed to %s, want 1", n, authn.EventMembershipChanged)
	}
	for _, other := range []string{authn.EventAccountDeleted, authn.EventSessionRevoked, authn.EventIDPChanged} {
		if bus.subscribed(other) != 0 {
			t.Fatalf("the layer subscribed to %s, which it has no use for", other)
		}
	}

	r := newRig(t)
	r.serve(r.as(acme))
	r.members.remove(alice, acme)
	r.clock.advance(defaults.CheckTTL - time.Nanosecond)
	if rp := r.serve(r.as(acme)); !rp.reached() {
		t.Fatal("the removal was seen before the lifetime ended with no event to say so")
	}
	r.clock.advance(time.Nanosecond)
	notFound(t, "the removed member once the lifetime ended", r.serve(r.as(acme)))
}

func TestAPublishedEventReachesTheHandlerTheLayerSubscribed(t *testing.T) {
	bus := newBusStub()
	var product *layer
	f := fixture{supplied: []supply{withBus(bus)}}
	err := f.drive(t, core.StageInit, func(_ context.Context, reg *core.Registry) error {
		anchor, err := core.Resolve[tenant.Admission](reg)
		if err != nil {
			return err
		}
		product = anchor.(*layer)
		return nil
	})
	if err != nil {
		t.Fatalf("the drive failed: %v", err)
	}
	product.cache.put(kindMember, cacheKey{account: alice, tenant: tenant.ID(acme)}, true, time.Minute, product.cache.begin())
	if err := bus.Publish(t.Context(), membershipChanged(string(alice), acme)); err != nil {
		t.Fatal(err)
	}
	if product.cache.size() != 0 {
		t.Fatal("an event published on the bus did not reach the layer's handler")
	}
}

func TestTheCacheHoldsAtMostItsBound(t *testing.T) {
	if maxEntries != 1<<16 {
		t.Fatalf("the per-kind bound is %d, want 1<<16: it is the documented ceiling on caller-chosen keys", maxEntries)
	}
	clk := &clock{now: time.Date(2026, time.October, 11, 9, 0, 0, 0, time.UTC)}
	c := newDecisionCache(clk.read)
	ttl := time.Minute
	for i := 0; i < maxEntries+10; i++ {
		c.put(kindMember, cacheKey{account: authn.AccountID("a" + strconv.Itoa(i)), tenant: acme}, true, ttl, c.begin())
	}
	if got := len(c.tables[kindMember]); got != maxEntries {
		t.Fatalf("the table holds %d entries, want it held at %d: the keys are chosen by callers", got, maxEntries)
	}
	if _, found := c.get(kindMember, cacheKey{account: "a0", tenant: acme}, ttl); !found {
		t.Fatal("an entry already held was evicted to make room for one that was not")
	}
	if _, found := c.get(kindMember, cacheKey{account: authn.AccountID("a" + strconv.Itoa(maxEntries+5)), tenant: acme}, ttl); found {
		t.Fatal("an entry was added to a full table of current entries")
	}
	clk.advance(ttl)
	c.put(kindMember, cacheKey{account: "fresh", tenant: acme}, true, ttl, c.begin())
	if _, found := c.get(kindMember, cacheKey{account: "fresh", tenant: acme}, ttl); !found {
		t.Fatal("a full table of expired entries refused a new one")
	}
	if got := len(c.tables[kindMember]); got != 1 {
		t.Fatalf("the sweep left %d entries, want the one fresh one", got)
	}
}
