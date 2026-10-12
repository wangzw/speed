package saml

import (
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/vislake/speed/pkg/settings"
)

// The paths of the settings this module reads. The enterprise provider register
// declares them and owns their domains and defaults; this module only reads
// them, so it repeats the declared default rather than the domain.
const (
	assertionMaxAgePath = "authn.sso.assertion-max-age"
	providerCachePath   = "authn.sso.provider-cache-ttl"
	egressTimeoutPath   = "authn.sso.egress-timeout"
)

// The declared defaults of the settings, which a tuning read that cannot be
// made, or one outside its domain, falls back to, and the domains the owner
// declares. They are repeated here because this module only reads the values
// the enterprise provider register declares.
const (
	defaultAssertionMaxAge = 5 * time.Minute
	defaultProviderCache   = 60 * time.Second
	defaultEgressTimeout   = 10 * time.Second
)

func assertionMaxAgeInDomain(d time.Duration) bool { return d >= time.Minute && d <= 30*time.Minute }
func providerCacheInDomain(d time.Duration) bool   { return d >= 5*time.Second && d <= time.Hour }
func egressTimeoutInDomain(d time.Duration) bool   { return d >= time.Second && d <= time.Minute }

// reader reads this module's tuning settings. The store is nil when the
// assembly has none, and every setting then reads as its declared default,
// which is a legal assembly.
type reader struct {
	store  settings.Store
	logger *slog.Logger

	mu       sync.Mutex
	reported map[string]string
}

func newReader(store settings.Store, logger *slog.Logger) *reader {
	return &reader{store: store, logger: logger, reported: make(map[string]string)}
}

// assertionMaxAge is how long before now a SAML assertion's IssueInstant may be
// and still be accepted.
func (r *reader) assertionMaxAge() time.Duration {
	return r.tuned(assertionMaxAgePath, defaultAssertionMaxAge, assertionMaxAgeInDomain)
}

// providerCacheTTL is how long a built service provider stays cached.
func (r *reader) providerCacheTTL() time.Duration {
	return r.tuned(providerCachePath, defaultProviderCache, providerCacheInDomain)
}

// egressTimeout is the deadline of one outbound call to an IdP.
func (r *reader) egressTimeout() time.Duration {
	return r.tuned(egressTimeoutPath, defaultEgressTimeout, egressTimeoutInDomain)
}

// tuned reads one duration setting. A read that fails, a value outside its
// declared domain and a value that is not a duration all read as the declared
// default, and the fault is reported at Error once per distinct fault.
func (r *reader) tuned(path string, fallback time.Duration, inDomain func(time.Duration) bool) time.Duration {
	if r.store == nil {
		return fallback
	}
	v, err := settings.Get[time.Duration](r.store, settings.Global(), path)
	switch {
	case err != nil:
		r.report(path, "the setting cannot be read, so its declared default is used", err.Error())
		return fallback
	case !inDomain(v):
		r.report(path, "the setting holds a value outside its domain, so its declared default is used", fmt.Sprint(v))
		return fallback
	}
	r.report(path, "", "")
	return v
}

// report writes the Error line of a setting read in a way other than as it says,
// once per distinct fault.
func (r *reader) report(path, problem, detail string) {
	r.mu.Lock()
	last, reported := r.reported[path]
	switch {
	case problem == "":
		delete(r.reported, path)
	case !reported || last != detail:
		r.reported[path] = detail
	default:
		r.mu.Unlock()
		return
	}
	r.mu.Unlock()
	if problem != "" {
		r.logger.Error(diagnosticPrefix+ModuleName+": "+problem, slog.String("setting", path), slog.String("detail", detail))
	}
}
