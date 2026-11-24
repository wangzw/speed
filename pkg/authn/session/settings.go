package session

import (
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/vislake/speed/pkg/settings"
)

// The keys of the settings this module reads, relative to its namespace. They
// are read from the global layer of the settings store while the process runs.
const (
	idleTimeoutKey     = "idle-timeout"
	absoluteTimeoutKey = "absolute-timeout"
	tokenAbsoluteKey   = "token-absolute-timeout"
	ssoAbsoluteKey     = "sso-absolute-timeout"
	touchIntervalKey   = "touch-interval"
	checkTTLKey        = "check-ttl"
	maxPerAccountKey   = "max-per-account"
	retentionKey       = "retention"

	idleTimeoutPath     = ConfigNamespace + "." + idleTimeoutKey
	absoluteTimeoutPath = ConfigNamespace + "." + absoluteTimeoutKey
	tokenAbsolutePath   = ConfigNamespace + "." + tokenAbsoluteKey
	ssoAbsolutePath     = ConfigNamespace + "." + ssoAbsoluteKey
	touchIntervalPath   = ConfigNamespace + "." + touchIntervalKey
	checkTTLPath        = ConfigNamespace + "." + checkTTLKey
	maxPerAccountPath   = ConfigNamespace + "." + maxPerAccountKey
	retentionPath       = ConfigNamespace + "." + retentionKey
)

// The domains of the settings: the one statement of each range. The settings
// store holds a write to it, and a read is judged against it again, since a
// value stored before the domain was declared, or written around the store, is
// served as it is.
const (
	idleTimeoutMin     = time.Minute
	idleTimeoutMax     = 24 * time.Hour
	absoluteTimeoutMin = 10 * time.Minute
	absoluteTimeoutMax = 30 * 24 * time.Hour
	tokenAbsoluteMin   = time.Hour
	tokenAbsoluteMax   = 8760 * time.Hour
	ssoAbsoluteMin     = 10 * time.Minute
	ssoAbsoluteMax     = 30 * 24 * time.Hour
	touchIntervalMin   = 5 * time.Second
	touchIntervalMax   = 10 * time.Minute
	checkTTLMax        = 60 * time.Second
	maxPerAccountMin   = 1
	maxPerAccountMax   = 1000
	retentionMin       = time.Hour
	retentionMax       = 8760 * time.Hour
)

// lifetimeUpperBound is the longest absolute period any kind of session may be
// given by its setting: the largest upper bound among the absolute-timeout
// domains. It is the Lifetime the session library is built with, so that the
// library's own deadline never ends a session the settings still allow. The
// period a session actually gets is computed here from the settings and written
// to its row.
const lifetimeUpperBound = max(absoluteTimeoutMax, tokenAbsoluteMax, ssoAbsoluteMax)

// options carries the settings items and their declared defaults. It is the
// prototype the settings module reads the declared defaults off, and never
// receives a value.
type options struct {
	IdleTimeout          time.Duration `config:"idle-timeout"`
	AbsoluteTimeout      time.Duration `config:"absolute-timeout"`
	TokenAbsoluteTimeout time.Duration `config:"token-absolute-timeout"`
	SSOAbsoluteTimeout   time.Duration `config:"sso-absolute-timeout"`
	TouchInterval        time.Duration `config:"touch-interval"`
	CheckTTL             time.Duration `config:"check-ttl"`
	MaxPerAccount        int           `config:"max-per-account"`
	Retention            time.Duration `config:"retention"`
}

var defaults = options{
	IdleTimeout:          30 * time.Minute,
	AbsoluteTimeout:      12 * time.Hour,
	TokenAbsoluteTimeout: 720 * time.Hour,
	SSOAbsoluteTimeout:   8 * time.Hour,
	TouchInterval:        60 * time.Second,
	CheckTTL:             5 * time.Second,
	MaxPerAccount:        20,
	Retention:            168 * time.Hour,
}

func inRange(d, lo, hi time.Duration) bool { return d >= lo && d <= hi }

func idleTimeoutInDomain(d time.Duration) bool { return inRange(d, idleTimeoutMin, idleTimeoutMax) }
func absoluteTimeoutInDomain(d time.Duration) bool {
	return inRange(d, absoluteTimeoutMin, absoluteTimeoutMax)
}

func tokenAbsoluteInDomain(d time.Duration) bool {
	return inRange(d, tokenAbsoluteMin, tokenAbsoluteMax)
}
func ssoAbsoluteInDomain(d time.Duration) bool { return inRange(d, ssoAbsoluteMin, ssoAbsoluteMax) }
func touchIntervalInDomain(d time.Duration) bool {
	return inRange(d, touchIntervalMin, touchIntervalMax)
}
func checkTTLInDomain(d time.Duration) bool  { return inRange(d, 0, checkTTLMax) }
func maxPerAccountInDomain(n int) bool       { return n >= maxPerAccountMin && n <= maxPerAccountMax }
func retentionInDomain(d time.Duration) bool { return inRange(d, retentionMin, retentionMax) }

func settingsSchema() settings.Schema {
	return settings.Schema{
		Namespace: ConfigNamespace,
		Mounts:    []settings.Mount{{Value: &defaults}},
		Items: map[string]settings.Item{
			idleTimeoutKey: {
				Description: "how long a browser session may go unused before it ends",
				Domain:      settings.DomainOf("durations from 1m to 24h", idleTimeoutInDomain),
			},
			absoluteTimeoutKey: {
				Description: "how long a browser session lasts at most, whatever its use",
				Domain:      settings.DomainOf("durations from 10m to 720h", absoluteTimeoutInDomain),
			},
			tokenAbsoluteKey: {
				Description: "how long a token session lasts at most, which is also how long its refresh token does",
				Domain:      settings.DomainOf("durations from 1h to 8760h", tokenAbsoluteInDomain),
			},
			ssoAbsoluteKey: {
				Description: "how long a session made by an enterprise SSO login lasts at most, whatever its kind; " +
					"the shorter of this and the kind's own period applies",
				Domain: settings.DomainOf("durations from 10m to 720h", ssoAbsoluteInDomain),
			},
			touchIntervalKey: {
				Description: "how seldom the use of a browser session extends its idle period",
				Domain:      settings.DomainOf("durations from 5s to 10m", touchIntervalInDomain),
			},
			checkTTLKey: {
				Description: "how long a replica serves what it read about a session, an account and an impersonation " +
					"before reading again, which is the longest a revocation goes unseen by a replica that was not told; " +
					"0 reads the database on every request",
				Domain: settings.DomainOf("durations from 0 to 60s", checkTTLInDomain),
			},
			maxPerAccountKey: {
				Description: "how many browser and token sessions one account may hold before the least recently used is revoked",
				Domain:      settings.DomainOf("whole numbers from 1 to 1000", maxPerAccountInDomain),
			},
			retentionKey: {
				Description: "how long a session row is kept after it ended, for audit and for listing revoked sessions",
				Domain:      settings.DomainOf("durations from 1h to 8760h", retentionInDomain),
			},
		},
	}
}

// settingReader reads the settings this module acts on. The store is nil when
// the assembly has none, and then every setting reads as its declared default.
// A read that fails and a value outside the setting's domain, which the store
// judges when it is written and not when it is read, both read as the declared
// default and are reported at Error, once for as long as the same fault stands.
type settingReader struct {
	store  settings.Store
	logger *slog.Logger

	mu       sync.Mutex
	reported map[string]string
}

func newSettingReader(logger *slog.Logger) *settingReader {
	return &settingReader{logger: logger, reported: make(map[string]string)}
}

// read returns the value of one setting, or the declared default.
func readSetting[T any](r *settingReader, path string, fallback T, inDomain func(T) bool) T {
	if r.store == nil {
		return fallback
	}
	value, err := settings.Get[T](r.store, settings.Global(), path)
	switch {
	case err != nil:
		r.report(path, "the setting cannot be read, so its declared default is used", err.Error())
		return fallback
	case !inDomain(value):
		r.report(path, "the setting holds a value outside its domain, so its declared default is used",
			fmt.Sprint(value))
		return fallback
	}
	r.report(path, "", "")
	return value
}

// report writes the Error line for a setting read as its default. A setting is
// read on every request, so a fault is written once and again only when it
// changes or has been cleared in between; an empty problem clears it.
func (r *settingReader) report(path, problem, detail string) {
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
		r.logger.Error(diagnosticPrefix+ModuleName+": "+problem, slog.String("setting", path))
	}
}

type timing struct {
	idle, absolute, tokenAbsolute, ssoAbsolute time.Duration
}

func (r *settingReader) timing() timing {
	return timing{
		idle:          readSetting(r, idleTimeoutPath, defaults.IdleTimeout, idleTimeoutInDomain),
		absolute:      readSetting(r, absoluteTimeoutPath, defaults.AbsoluteTimeout, absoluteTimeoutInDomain),
		tokenAbsolute: readSetting(r, tokenAbsolutePath, defaults.TokenAbsoluteTimeout, tokenAbsoluteInDomain),
		ssoAbsolute:   readSetting(r, ssoAbsolutePath, defaults.SSOAbsoluteTimeout, ssoAbsoluteInDomain),
	}
}

func (r *settingReader) touchInterval() time.Duration {
	return readSetting(r, touchIntervalPath, defaults.TouchInterval, touchIntervalInDomain)
}

func (r *settingReader) checkTTL() time.Duration {
	return readSetting(r, checkTTLPath, defaults.CheckTTL, checkTTLInDomain)
}

func (r *settingReader) maxPerAccount() int {
	return readSetting(r, maxPerAccountPath, defaults.MaxPerAccount, maxPerAccountInDomain)
}

func (r *settingReader) retention() time.Duration {
	return readSetting(r, retentionPath, defaults.Retention, retentionInDomain)
}
