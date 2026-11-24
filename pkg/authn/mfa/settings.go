package mfa

import (
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/vislake/speed/pkg/settings"
)

const (
	globalRequiredKey  = "global-required"
	recentWindowKey    = "recent-window"
	maxPerKindKey      = "max-per-kind"
	pendingTTLKey      = "pending-ttl"
	sensitiveMinAgeKey = "sensitive-min-age"

	globalRequiredPath  = ModuleName + "." + globalRequiredKey
	recentWindowPath    = ModuleName + "." + recentWindowKey
	maxPerKindPath      = ModuleName + "." + maxPerKindKey
	pendingTTLPath      = ModuleName + "." + pendingTTLKey
	sensitiveMinAgePath = ModuleName + "." + sensitiveMinAgeKey
)

const (
	minRecentWindow = time.Minute
	maxRecentWindow = time.Hour

	minMaxPerKind = 1
	maxMaxPerKind = 20

	minPendingTTL = 5 * time.Minute
	maxPendingTTL = 24 * time.Hour

	minSensitiveMinAge = time.Hour
	maxSensitiveMinAge = 720 * time.Hour
)

type options struct {
	GlobalRequired  bool          `config:"global-required"`
	RecentWindow    time.Duration `config:"recent-window"`
	MaxPerKind      int           `config:"max-per-kind"`
	PendingTTL      time.Duration `config:"pending-ttl"`
	SensitiveMinAge time.Duration `config:"sensitive-min-age"`
}

var defaults = options{
	GlobalRequired:  false,
	RecentWindow:    10 * time.Minute,
	MaxPerKind:      5,
	PendingTTL:      time.Hour,
	SensitiveMinAge: 24 * time.Hour,
}

func recentWindowInDomain(d time.Duration) bool {
	return d >= minRecentWindow && d <= maxRecentWindow
}

func maxPerKindInDomain(n int) bool { return n >= minMaxPerKind && n <= maxMaxPerKind }

func pendingTTLInDomain(d time.Duration) bool { return d >= minPendingTTL && d <= maxPendingTTL }

func sensitiveMinAgeInDomain(d time.Duration) bool {
	return d >= minSensitiveMinAge && d <= maxSensitiveMinAge
}

func settingsSchema() settings.Schema {
	return settings.Schema{
		Namespace: ModuleName,
		Mounts:    []settings.Mount{{Value: &defaults}},
		Items: map[string]settings.Item{
			globalRequiredKey: {
				Description: "whether every account has to register a second factor, which an " +
					"account with none is asked to do at its next login",
			},
			recentWindowKey: {
				Description: "how recent a local multi-factor authentication must be for an " +
					"action on the account's own factors, from " + minRecentWindow.String() +
					" to " + maxRecentWindow.String(),
				Domain: settings.DomainOf("durations from "+minRecentWindow.String()+" to "+
					maxRecentWindow.String(), recentWindowInDomain),
			},
			maxPerKindKey: {
				Description: "the most factors of one kind an account may hold, pending ones included",
				Domain:      settings.DomainOf("whole numbers from 1 to 20", maxPerKindInDomain),
			},
			pendingTTLKey: {
				Description: "how long a factor may stay pending before the sweep removes it",
				Domain: settings.DomainOf("durations from "+minPendingTTL.String()+" to "+
					maxPendingTTL.String(), pendingTTLInDomain),
			},
			sensitiveMinAgeKey: {
				Description: "how long a factor has to have been active to count for the " +
					"highest-privilege actions, impersonation and resetting another account's factors",
				Domain: settings.DomainOf("durations from "+minSensitiveMinAge.String()+" to "+
					maxSensitiveMinAge.String(), sensitiveMinAgeInDomain),
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

var errSettingRead = errors.New("the setting cannot be read")

func (r *reader) globalRequired() (bool, error) {
	if r.store == nil {
		return defaults.GlobalRequired, nil
	}
	v, err := settings.Get[bool](r.store, settings.Global(), globalRequiredPath)
	if err != nil {
		r.report(globalRequiredPath, "the mandatory switch cannot be read, so the request is refused", err.Error())
		return false, fmt.Errorf("%w: %s: %w", errSettingRead, globalRequiredPath, err)
	}
	r.report(globalRequiredPath, "", "")
	return v, nil
}

func (r *reader) recentWindow() time.Duration {
	return readTuned(r, recentWindowPath, defaults.RecentWindow, recentWindowInDomain)
}

func (r *reader) maxPerKind() int {
	return readTuned(r, maxPerKindPath, defaults.MaxPerKind, maxPerKindInDomain)
}

func (r *reader) pendingTTL() time.Duration {
	return readTuned(r, pendingTTLPath, defaults.PendingTTL, pendingTTLInDomain)
}

func (r *reader) sensitiveMinAge() time.Duration {
	return readTuned(r, sensitiveMinAgePath, defaults.SensitiveMinAge, sensitiveMinAgeInDomain)
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
