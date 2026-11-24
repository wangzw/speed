package impersonation

import (
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/vislake/speed/pkg/settings"
)

const (
	maxTTLKey     = "max-ttl"
	maxAuthAgeKey = "max-auth-age"

	maxTTLPath     = ModuleName + "." + maxTTLKey
	maxAuthAgePath = ModuleName + "." + maxAuthAgeKey
)

const accessTTLPath = "authn.token.access-ttl"

const (
	minMaxTTL     = time.Minute
	maxMaxTTL     = time.Hour
	minMaxAuthAge = time.Minute
	maxMaxAuthAge = 30 * time.Minute
)

const (
	tokenAccessTTLMin     = time.Minute
	tokenAccessTTLMax     = time.Hour
	defaultTokenAccessTTL = 10 * time.Minute
)

type options struct {
	MaxTTL     time.Duration `config:"max-ttl"`
	MaxAuthAge time.Duration `config:"max-auth-age"`
}

var defaults = options{
	MaxTTL:     30 * time.Minute,
	MaxAuthAge: 5 * time.Minute,
}

// maxTTLInDomain and maxAuthAgeInDomain are the one statement of each setting's
// range, judged again on read because a stored value may predate it.
func maxTTLInDomain(d time.Duration) bool { return d >= minMaxTTL && d <= maxMaxTTL }

func maxAuthAgeInDomain(d time.Duration) bool { return d >= minMaxAuthAge && d <= maxMaxAuthAge }

func settingsSchema() settings.Schema {
	return settings.Schema{
		Namespace: ModuleName,
		Mounts:    []settings.Mount{{Value: &defaults}},
		Items: map[string]settings.Item{
			maxTTLKey: {
				Description: "the longest an impersonation may last, from " + minMaxTTL.String() +
					" to " + maxMaxTTL.String(),
				Domain: settings.DomainOf("durations from "+minMaxTTL.String()+" to "+
					maxMaxTTL.String(), maxTTLInDomain),
			},
			maxAuthAgeKey: {
				Description: "how recent the administrator's local multi-factor authentication must be to begin " +
					"an impersonation, from " + minMaxAuthAge.String() + " to " + maxMaxAuthAge.String(),
				Domain: settings.DomainOf("durations from "+minMaxAuthAge.String()+" to "+
					maxMaxAuthAge.String(), maxAuthAgeInDomain),
			},
		},
	}
}

type reader struct {
	store  settings.Store
	logger *slog.Logger

	mu       sync.Mutex
	reported map[string]string
}

func newReader(store settings.Store, logger *slog.Logger) *reader {
	return &reader{store: store, logger: logger, reported: make(map[string]string)}
}

func (r *reader) maxTTL() time.Duration {
	return readTuned(r, maxTTLPath, defaults.MaxTTL, maxTTLInDomain)
}

func (r *reader) maxAuthAge() time.Duration {
	return readTuned(r, maxAuthAgePath, defaults.MaxAuthAge, maxAuthAgeInDomain)
}

func (r *reader) tokenAccessTTL() time.Duration {
	return readTuned(r, accessTTLPath, defaultTokenAccessTTL, func(d time.Duration) bool {
		return d >= tokenAccessTTLMin && d <= tokenAccessTTLMax
	})
}

func readTuned[T any](r *reader, path string, fallback T, inDomain func(T) bool) T {
	if r.store == nil {
		return fallback
	}
	v, err := settings.Get[T](r.store, settings.Global(), path)
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
		problem = ""
	}
	r.mu.Unlock()
	if problem != "" {
		r.logger.Error(ModuleName+": "+problem, slog.String("setting", path))
	}
}
