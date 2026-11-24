package policy

import (
	"errors"
	"sync"
	"testing"
	"time"

	"gorm.io/gorm"

	"github.com/vislake/speed/pkg/authz"
	"github.com/vislake/speed/pkg/event"
)

// TestAWritePublishesTheChangeEvent checks that a write publishes the change event.
func TestAWritePublishesTheChangeEvent(t *testing.T) {
	p := newProductOver(t)
	bus := newRecordingBus()
	wireProduct(t, p, bus)
	if err := p.begin(t.Context()); err != nil {
		t.Fatalf("begin: %v", err)
	}

	if err := p.PutRole(t.Context(), authz.RoleRecord{Tenant: "acme", Role: "roleA", DescriptionKey: "k"}); err != nil {
		t.Fatalf("PutRole: %v", err)
	}
	events := bus.events()
	if len(events) != 1 || events[0].Type != policyChangedEventType {
		t.Fatalf("the write published %v, want one %q event", events, policyChangedEventType)
	}
}

// TestADeliveredChangeReloadsTheSnapshot checks that a delivered change reloads the snapshot.
func TestADeliveredChangeReloadsTheSnapshot(t *testing.T) {
	p := newProductOver(t)
	bus := newRecordingBus()
	wireProduct(t, p, bus)
	ctx := t.Context()
	if err := p.begin(ctx); err != nil {
		t.Fatalf("begin: %v", err)
	}
	if allowed(t, ctx, p, "acc1", "acme", "account", "read") {
		t.Fatalf("an empty policy allowed a query")
	}

	seedPolicy(t, p, "acme", "roleA", "acc1", "account", "read")
	if allowed(t, ctx, p, "acc1", "acme", "account", "read") {
		t.Fatalf("the snapshot picked up a row no reload saw")
	}

	bus.deliver(t, policyChangedEventType, event.Event{Type: policyChangedEventType})
	if !allowed(t, ctx, p, "acc1", "acme", "account", "read") {
		t.Errorf("the delivered change did not reload the snapshot")
	}
}

// TestTheRefreshIntervalControlsThePeriodicReload checks that the refresh interval controls the periodic reload.
func TestTheRefreshIntervalControlsThePeriodicReload(t *testing.T) {
	ctx := t.Context()

	off := newProductOver(t)
	off.refreshInterval = 0
	wireProduct(t, off, nil)
	if err := off.begin(ctx); err != nil {
		t.Fatalf("begin: %v", err)
	}
	if off.cancelRefresh != nil {
		t.Errorf("a zero refresh interval started a periodic reload")
	}

	on := newProductOver(t)
	on.refreshInterval = time.Millisecond
	wireProduct(t, on, nil)
	if err := on.begin(ctx); err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer on.stopRefresh()

	seedPolicy(t, on, "acme", "roleA", "acc1", "account", "read")
	if !pollUntil(t, 3*time.Second, func() bool { return allowed(t, ctx, on, "acc1", "acme", "account", "read") }) {
		t.Errorf("a positive refresh interval did not reload the snapshot")
	}
}

// TestTheLoadTimeoutFailsTheStartupWithNoSnapshot checks that the load timeout fails the startup with no snapshot.
func TestTheLoadTimeoutFailsTheStartupWithNoSnapshot(t *testing.T) {
	p := newProductOver(t)
	holdReadsUntilCancelled(t, p.handle)
	p.loadTimeout = time.Millisecond

	err := p.begin(t.Context())
	if !errors.Is(err, authz.ErrPolicyUnavailable) {
		t.Fatalf("begin with the load held past its timeout gave %v, want ErrPolicyUnavailable", err)
	}
	if p.holder.Get() != nil {
		t.Errorf("a half-read snapshot was installed")
	}
}

func holdReadsUntilCancelled(t *testing.T, handle *gorm.DB) {
	t.Helper()
	err := handle.Callback().Query().Before("gorm:query").Register("test:hold", func(tx *gorm.DB) {
		<-tx.Statement.Context.Done()
	})
	if err != nil {
		t.Fatalf("registering the holding callback: %v", err)
	}
}

// TestConcurrentReloadsDoNotOverlap checks that concurrent reloads do not overlap.
func TestConcurrentReloadsDoNotOverlap(t *testing.T) {
	p := newProductOver(t)
	ctx := t.Context()

	var (
		mu        sync.Mutex
		inFlight  int
		overlap   bool
		firstHold = true
		release   = make(chan struct{})
	)
	if err := p.handle.Callback().Query().After("gorm:query").Register("test:overlap", func(*gorm.DB) {
		mu.Lock()
		inFlight++
		hold := firstHold
		firstHold = false
		if inFlight > 1 && !overlap {
			overlap = true
			close(release)
		}
		mu.Unlock()
		if hold {
			select {
			case <-release:
			case <-time.After(time.Second):
			}
		}
		mu.Lock()
		inFlight--
		mu.Unlock()
	}); err != nil {
		t.Fatalf("registering the overlap callback: %v", err)
	}

	var wg sync.WaitGroup
	for range 4 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = p.swap(ctx)
		}()
	}
	wg.Wait()

	if overlap {
		t.Errorf("two policy loads read the table at once: the reload path is not serialised")
	}
}
