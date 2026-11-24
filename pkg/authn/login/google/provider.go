package google

import (
	"context"
	"net/http"
	"sync"

	"github.com/coreos/go-oidc/v3/oidc"
)

// providerCache holds the discovered provider: discovery is lazy, a failure is returned and
// not cached, and a key rotation is picked up when a key identifier is not found.
type providerCache struct {
	issuer string
	client *http.Client

	mu       sync.Mutex
	provider *oidc.Provider
}

// get returns the discovered provider, discovering it under ctx when unknown. The restricted
// client is injected through the context so discovery and the key set use it; a failed
// discovery is not cached.
func (c *providerCache) get(ctx context.Context) (*oidc.Provider, error) {
	c.mu.Lock()
	cached := c.provider
	c.mu.Unlock()
	if cached != nil {
		return cached, nil
	}
	// The lock is not held across the request, so a caller is not made to wait
	// on another caller's discovery: two callers that both find the cache empty
	// each discover, and the first to finish wins.
	discovered, err := oidc.NewProvider(oidc.ClientContext(ctx, c.client), c.issuer)
	if err != nil {
		return nil, err
	}
	c.mu.Lock()
	if c.provider == nil {
		c.provider = discovered
	}
	cached = c.provider
	c.mu.Unlock()
	return cached, nil
}
