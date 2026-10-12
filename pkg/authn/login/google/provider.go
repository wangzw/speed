package google

import (
	"context"
	"net/http"
	"sync"

	"github.com/coreos/go-oidc/v3/oidc"
)

// providerCache holds the discovered provider. Discovery is lazy and cached:
// the provider is discovered on the first call that needs it, a failure is
// returned as an outage and is not cached, so the next call tries again. The
// provider's key set lives with it, and a rotation of the provider's keys is
// picked up by the library when a key identifier is not found.
type providerCache struct {
	issuer string
	client *http.Client

	mu       sync.Mutex
	provider *oidc.Provider
}

// get returns the discovered provider, discovering it under ctx when it is not
// yet known. The restricted client is injected through the context, so the
// discovery request, the key set and every later call go through it; without the
// injection the library would use a client with no timeout. A discovery that
// fails is not cached: the failure is returned and the next call retries.
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
