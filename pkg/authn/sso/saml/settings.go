package saml

import (
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/vislake/speed/pkg/settings"
)

const (
	assertionMaxAgePath = "authn.sso.assertion-max-age"
	providerCachePath   = "authn.sso.provider-cache-ttl"
	egressTimeoutPath   = "authn.sso.egress-timeout"
)

const (
	defaultAssertionMaxAge = 5 * time.Minute
	defaultProviderCache   = 60 * time.Second
	defaultEgressTimeout   = 10 * time.Second
)

func assertionMaxAgeInDomain(d time.Duration) bool { return d >= time.Minute && d <= 30*time.Minute }
func providerCacheInDomain(d time.Duration) bool   { return d >= 5*time.Second && d <= time.Hour }
func egressTimeoutInDomain(d time.Duration) bool   { return d >= time.Second && d <= time.Minute }

type reader struct {
	store  settings.Store
	logger *slog.Logger

	mu       sync.Mutex
	reported map[string]string
}

func newReader(store settings.Store, logger *slog.Logger) *reader {
	return &reader{store: store, logger: logger, reported: make(map[string]string)}
}

func (r *reader) assertionMaxAge() time.Duration {
	return r.tuned(assertionMaxAgePath, defaultAssertionMaxAge, assertionMaxAgeInDomain)
}

func (r *reader) providerCacheTTL() time.Duration {
	return r.tuned(providerCachePath, defaultProviderCache, providerCacheInDomain)
}

func (r *reader) egressTimeout() time.Duration {
	return r.tuned(egressTimeoutPath, defaultEgressTimeout, egressTimeoutInDomain)
}

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
