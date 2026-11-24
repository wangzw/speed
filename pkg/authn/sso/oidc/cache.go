package oidc

import (
	"context"
	"net/http"
	"sync"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	"golang.org/x/oauth2"
)

type providerEntry struct {
	version  int
	provider *oidc.Provider
	oauth    *oauth2.Config
	expires  time.Time
}

type providerCache struct {
	client   *http.Client
	settings *reader
	now      func() time.Time

	mu      sync.Mutex
	entries map[string]*providerEntry
}

func newProviderCache(client *http.Client, settings *reader) *providerCache {
	return &providerCache{
		client:   client,
		settings: settings,
		now:      time.Now,
		entries:  map[string]*providerEntry{},
	}
}

func (c *providerCache) get(ctx context.Context, id string, version int, cfg providerConfig, secret, redirectURL string) (*oidc.Provider, *oauth2.Config, error) {
	now := c.now()
	c.mu.Lock()
	if e, ok := c.entries[id]; ok && e.version == version && now.Before(e.expires) {
		c.mu.Unlock()
		return e.provider, e.oauth, nil
	}
	c.mu.Unlock()

	// The lock is not held across the request, so two callers that both find the cache empty each discover and the last to finish wins for this version.
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

func (c *providerCache) drop(id string) {
	c.mu.Lock()
	delete(c.entries, id)
	c.mu.Unlock()
}
