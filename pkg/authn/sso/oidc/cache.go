package oidc

import (
	"context"
	"net/http"
	"sync"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	"golang.org/x/oauth2"
)

// providerEntry is one cached provider: the discovered provider, the OAuth
// client built from it and the provider's stored configuration, with the version
// they were built from and the instant they expire.
type providerEntry struct {
	version  int
	provider *oidc.Provider
	oauth    *oauth2.Config
	expires  time.Time
}

// providerCache holds the discovered provider and the OAuth client of each
// enterprise provider, keyed by the provider's identifier. A change of the
// provider's stored configuration raises its version, which is a different
// entry; an authn.idp.changed event drops the entry outright. Discovery is
// lazy: a provider is discovered on the first call that needs it, a failure is
// returned as an outage and is not cached, and the next call retries. The
// provider's key set lives with it, and a rotation of the provider's keys is
// picked up by the library when a key identifier is not found.
type providerCache struct {
	client   *http.Client
	settings *reader
	// now reads the clock; a test replaces it.
	now func() time.Time

	mu      sync.Mutex
	entries map[string]*providerEntry
}

// newProviderCache builds an empty cache.
func newProviderCache(client *http.Client, settings *reader) *providerCache {
	return &providerCache{
		client:   client,
		settings: settings,
		now:      time.Now,
		entries:  map[string]*providerEntry{},
	}
}

// get returns the discovered provider and the OAuth client of one provider,
// discovering the provider under ctx when the cache holds no entry for its
// version. The restricted client is injected through the context, so the
// discovery request, the key set and every later call go through it; without the
// injection the library would use a client with no timeout. A discovery that
// fails is not cached and leaves any entry of an earlier version in place.
func (c *providerCache) get(ctx context.Context, id string, version int, cfg providerConfig, secret, redirectURL string) (*oidc.Provider, *oauth2.Config, error) {
	now := c.now()
	c.mu.Lock()
	if e, ok := c.entries[id]; ok && e.version == version && now.Before(e.expires) {
		c.mu.Unlock()
		return e.provider, e.oauth, nil
	}
	c.mu.Unlock()

	// The lock is not held across the request, so a caller is not made to wait on
	// another caller's discovery: two callers that both find the cache empty each
	// discover, and the last to finish wins for this version.
	provider, err := oidc.NewProvider(oidc.ClientContext(ctx, c.client), cfg.Issuer)
	if err != nil {
		return nil, nil, err
	}
	oauth := &oauth2.Config{
		ClientID:     cfg.ClientID,
		ClientSecret: secret,
		Endpoint:     provider.Endpoint(),
		RedirectURL:  redirectURL,
		Scopes:       scopesOf(cfg),
	}
	c.mu.Lock()
	c.entries[id] = &providerEntry{
		version:  version,
		provider: provider,
		oauth:    oauth,
		expires:  c.now().Add(c.settings.providerCacheTTL()),
	}
	c.mu.Unlock()
	return provider, oauth, nil
}

// drop discards the cached entry of one provider, whatever version it holds. It
// is idempotent, which is what a change event's at-least-once delivery needs.
func (c *providerCache) drop(id string) {
	c.mu.Lock()
	delete(c.entries, id)
	c.mu.Unlock()
}
