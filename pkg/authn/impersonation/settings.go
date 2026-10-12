package impersonation

import (
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/vislake/speed/pkg/settings"
)

// The setting keys, relative to the module's namespace, and the full paths a
// read goes by.
const (
	maxTTLKey     = "max-ttl"
	maxAuthAgeKey = "max-auth-age"

	maxTTLPath     = ModuleName + "." + maxTTLKey
	maxAuthAgePath = ModuleName + "." + maxAuthAgeKey
)

// accessTTLPath is the access-token lifetime the token module declares.
// This module only reads it, to cap an impersonation token at the shorter of
// that and the record's remaining lifetime; it declares no key under the token
// module's namespace.
const accessTTLPath = "authn.token.access-ttl"

// The bounds of the setting domains.
const (
	minMaxTTL     = time.Minute
	maxMaxTTL     = time.Hour
	minMaxAuthAge = time.Minute
	maxMaxAuthAge = 30 * time.Minute
)

// accessTTLMin and accessTTLMax are the token module's own bounds on its
// access-ttl setting, repeated here because this module only reads the value it
// declares.
const (
	tokenAccessTTLMin     = time.Minute
	tokenAccessTTLMax     = time.Hour
	defaultTokenAccessTTL = 10 * time.Minute
)

// options carries the two settings and their declared defaults. The settings
// module reads the defaults off it and it never receives a value.
type options struct {
	MaxTTL     time.Duration `config:"max-ttl"`
	MaxAuthAge time.Duration `config:"max-auth-age"`
}

// defaults is the declared default of each setting, and the value a read that
// cannot be made falls back to.
var defaults = options{
	MaxTTL:     30 * time.Minute,
	MaxAuthAge: 5 * time.Minute,
}

// maxTTLInDomain and maxAuthAgeInDomain are the one statement of each setting's
// range: a write to the store is held to it, and a read is judged against it
// again, since a value stored before the domain was declared, or written around
// the store, is served as it is.
func maxTTLInDomain(d time.Duration) bool { return d >= minMaxTTL && d <= maxMaxTTL }

func maxAuthAgeInDomain(d time.Duration) bool { return d >= minMaxAuthAge && d <= maxMaxAuthAge }

// settingsSchema declares the module's runtime parameters, each with the domain
// the code acts on.
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

// reader reads the settings with the failure direction each one has. The store
// is nil when the assembly has none, and every setting then reads as its
// declared default, which is a legal assembly and says nothing.
type reader struct {
	store  settings.Store
	logger *slog.Logger

	// mu guards reported, which holds for each setting the fault last written, so
	// that a setting read on every start is reported once and not on each.
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

// tokenAccessTTL reads the access-token lifetime the token module declares. A
// failed read and a value outside its domain read as the declared default. A
// missing store reads the default too, which is the value the token module
// itself would use.
func (r *reader) tokenAccessTTL() time.Duration {
	return readTuned(r, accessTTLPath, defaultTokenAccessTTL, func(d time.Duration) bool {
		return d >= tokenAccessTTLMin && d <= tokenAccessTTLMax
	})
}

// readTuned reads a tuning setting: one that fails to read, and one outside its
// domain, read as the declared default, and the fault is reported at Error once
// per distinct fault.
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

// report writes the Error line of a setting read in a way other than as it
// says. A setting is read on requests, so the same fault is written once and
// again only when it changes or has been cleared in between; an empty problem
// clears it.
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
