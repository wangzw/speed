package oidc

import (
	"log/slog"
	"sync"
	"time"

	"github.com/vislake/speed/pkg/settings"
)

const (
	providerCacheTTLKey = "provider-cache-ttl"
	egressTimeoutKey    = "egress-timeout"
)

var (
	providerCacheTTLPath = ssoNamespace + "." + providerCacheTTLKey
	egressTimeoutPath    = ssoNamespace + "." + egressTimeoutKey
)

const (
	defaultProviderCacheTTL = 60 * time.Second
	defaultEgressTimeout    = 10 * time.Second
)

type reader struct {
	store  settings.Store
	logger *slog.Logger

	mu       sync.Mutex
	reported map[string]string
}

func newReader(store settings.Store, logger *slog.Logger) *reader {
	return &reader{store: store, logger: logger, reported: map[string]string{}}
}

func (r *reader) providerCacheTTL() time.Duration {
	return r.readDuration(providerCacheTTLPath, defaultProviderCacheTTL)
}

func (r *reader) egressTimeout() time.Duration {
	return r.readDuration(egressTimeoutPath, defaultEgressTimeout)
}

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
