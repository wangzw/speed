package oidc

import (
	"log/slog"
	"sync"
	"time"

	"github.com/vislake/speed/pkg/settings"
)

// The setting keys this module reads, relative to the SSO module's namespace.
// The SSO module declares them; this module only reads them, at request time.
const (
	providerCacheTTLKey = "provider-cache-ttl"
	egressTimeoutKey    = "egress-timeout"
)

// The full paths the reads go by.
var (
	providerCacheTTLPath = ssoNamespace + "." + providerCacheTTLKey
	egressTimeoutPath    = ssoNamespace + "." + egressTimeoutKey
)

// The declared defaults of the settings, used when the assembly has no store.
const (
	defaultProviderCacheTTL = 60 * time.Second
	defaultEgressTimeout    = 10 * time.Second
)

// reader reads the settings this module acts on. The store is nil when the
// assembly has none, and every setting then reads as its declared default, which
// is a legal assembly.
type reader struct {
	store  settings.Store
	logger *slog.Logger

	// mu guards reported, which holds for each setting the fault last written,
	// so that a setting read on every request is reported once and not on each.
	mu       sync.Mutex
	reported map[string]string
}

// newReader builds a reader over a settings store, which may be nil.
func newReader(store settings.Store, logger *slog.Logger) *reader {
	return &reader{store: store, logger: logger, reported: map[string]string{}}
}

// providerCacheTTL is how long a discovered provider stays in the cache. It is
// read on a request, so a store that cannot answer reads as the declared
// default, which is the shorter direction and never keeps a stale provider.
func (r *reader) providerCacheTTL() time.Duration {
	return r.readDuration(providerCacheTTLPath, defaultProviderCacheTTL)
}

// egressTimeout is the deadline of one outbound call to an identity provider.
func (r *reader) egressTimeout() time.Duration {
	return r.readDuration(egressTimeoutPath, defaultEgressTimeout)
}

// readDuration reads one duration setting: one that fails to read, and one that
// is not positive, read as the declared default, and the fault is reported at
// Error once per distinct fault.
func (r *reader) readDuration(path string, fallback time.Duration) time.Duration {
	if r.store == nil {
		return fallback
	}
	v, err := settings.Get[time.Duration](r.store, settings.Global(), path)
	switch {
	case err != nil:
		r.report(path, "the setting cannot be read, so its declared default is used", err.Error())
		return fallback
	case v <= 0:
		r.report(path, "the setting is not a positive duration, so its declared default is used", v.String())
		return fallback
	}
	r.report(path, "", "")
	return v
}

// report writes the Error line of a setting that is read in a way other than as
// it says. A setting is read on requests, so the same fault is written once and
// again only when it changes or has been cleared in between; an empty problem
// clears it.
func (r *reader) report(path, problem, detail string) {
	if r.logger == nil {
		return
	}
	r.mu.Lock()
	last, reported := r.reported[path]
	switch {
	case problem == "":
		delete(r.reported, path)
	case !reported || last != detail:
		r.reported[path] = detail
	default:
		problem = ""
	}
	r.mu.Unlock()
	if problem != "" {
		r.logger.Error(ModuleName+": "+problem, slog.String("setting", path))
	}
}
