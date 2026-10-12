package oidc

import (
	"testing"
	"time"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/event"
)

// TestCacheHitsUntilTheVersionChanges holds that a second Begin reuses the
// discovered provider, and that a raised provider version discovers it again.
func TestCacheHitsUntilTheVersionChanges(t *testing.T) {
	m := newMockIDP(t)
	r := newRig(t, m)
	row := r.putProvider("idp-1", "tenant-a", m, nil)

	r.begin("idp-1")
	if n := m.discoveries(); n != 1 {
		t.Fatalf("discoveries after the first Begin = %d, want 1", n)
	}
	r.begin("idp-1")
	if n := m.discoveries(); n != 1 {
		t.Fatalf("discoveries after a cached Begin = %d, want 1", n)
	}

	row.Version = 2
	r.providers.put(row, testClientSecret)
	r.begin("idp-1")
	if n := m.discoveries(); n != 2 {
		t.Fatalf("discoveries after a version change = %d, want 2", n)
	}
}

// TestIDPChangedDropsTheCachedProvider holds that an authn.idp.changed event for
// a provider makes the next Begin discover it again.
func TestIDPChangedDropsTheCachedProvider(t *testing.T) {
	m := newMockIDP(t)
	r := newRig(t, m)
	r.putProvider("idp-1", "tenant-a", m, nil)

	r.begin("idp-1")
	if n := m.discoveries(); n != 1 {
		t.Fatalf("discoveries = %d, want 1", n)
	}
	if err := r.p.onIDPChanged(t.Context(), event.Event{Payload: authn.IDPChanged{IDPID: "idp-1", Change: "updated"}}); err != nil {
		t.Fatalf("the handler: %v", err)
	}
	r.begin("idp-1")
	if n := m.discoveries(); n != 2 {
		t.Fatalf("discoveries after the change event = %d, want 2", n)
	}
}

// TestCacheExpires holds that an entry past its provider-cache-ttl is discovered
// again.
func TestCacheExpires(t *testing.T) {
	m := newMockIDP(t)
	r := newRig(t, m)
	r.putProvider("idp-1", "tenant-a", m, nil)
	now := time.Now()
	r.p.cache.now = func() time.Time { return now }

	r.begin("idp-1")
	if n := m.discoveries(); n != 1 {
		t.Fatalf("discoveries = %d, want 1", n)
	}
	now = now.Add(defaultProviderCacheTTL + time.Second)
	r.begin("idp-1")
	if n := m.discoveries(); n != 2 {
		t.Fatalf("discoveries after the cache expired = %d, want 2", n)
	}
}
