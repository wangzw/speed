package token

import (
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/vislake/speed/pkg/settings"
)

// The keys of the settings this module reads, relative to its namespace. They
// are read from the global layer of the settings store while the process runs.
// The absolute lifetime of a session's refresh tokens is not here: it is the
// session's, and the module reads it through Sessions.
const (
	accessTTLKey      = "access-ttl"
	leewayKey         = "leeway"
	refreshIdleTTLKey = "refresh-idle-ttl"
	refreshGraceKey   = "refresh-grace"
	checkSessionKey   = "check-session"

	accessTTLPath      = ConfigNamespace + "." + accessTTLKey
	leewayPath         = ConfigNamespace + "." + leewayKey
	refreshIdleTTLPath = ConfigNamespace + "." + refreshIdleTTLKey
	refreshGracePath   = ConfigNamespace + "." + refreshGraceKey
	checkSessionPath   = ConfigNamespace + "." + checkSessionKey
)

// checkTTLPath is the session module's setting the verifying backend's own
// caches are bounded by. The token backend reads it because the identity it
// serves from those caches carries the same lifetime as the session check they
// stand beside, and the two are one bound an operator sets.
const (
	checkTTLKey     = "check-ttl"
	checkTTLPath    = "authn.session." + checkTTLKey
	checkTTLMax     = 60 * time.Second
	defaultCheckTTL = 5 * time.Second
)

// The domains of the settings: the one statement of each range. The settings
// store holds a write to it, and a read is judged against it again, since a
// value stored before the domain was declared, or written around the store, is
// served as it is.
const (
	accessTTLMin      = time.Minute
	accessTTLMax      = time.Hour
	leewayMin         = 0
	leewayMax         = 120 * time.Second
	refreshIdleTTLMin = time.Hour
	refreshIdleTTLMax = 2160 * time.Hour
	refreshGraceMin   = 0
	refreshGraceMax   = 60 * time.Second
)

const accessTTLUpperBound = accessTTLMax

// options carries the settings items and their declared defaults. It is the
// prototype the settings module reads the declared defaults off, and never
// receives a value.
type options struct {
	AccessTTL      time.Duration `config:"access-ttl"`
	Leeway         time.Duration `config:"leeway"`
	RefreshIdleTTL time.Duration `config:"refresh-idle-ttl"`
	RefreshGrace   time.Duration `config:"refresh-grace"`
	CheckSession   bool          `config:"check-session"`
}

var defaults = options{
	AccessTTL:      10 * time.Minute,
	Leeway:         30 * time.Second,
	RefreshIdleTTL: 168 * time.Hour,
	RefreshGrace:   0,
	CheckSession:   true,
}

func inRange(d, lo, hi time.Duration) bool { return d >= lo && d <= hi }

func accessTTLInDomain(d time.Duration) bool { return inRange(d, accessTTLMin, accessTTLMax) }
func leewayInDomain(d time.Duration) bool    { return inRange(d, leewayMin, leewayMax) }
func refreshIdleInDomain(d time.Duration) bool {
	return inRange(d, refreshIdleTTLMin, refreshIdleTTLMax)
}

func refreshGraceInDomain(d time.Duration) bool {
	return inRange(d, refreshGraceMin, refreshGraceMax)
}

func checkTTLInDomain(d time.Duration) bool { return inRange(d, 0, checkTTLMax) }

func checkSessionInDomain(bool) bool { return true }

func settingsSchema() settings.Schema {
	return settings.Schema{
		Namespace: ConfigNamespace,
		Mounts:    []settings.Mount{{Value: &defaults}},
		Items: map[string]settings.Item{
			accessTTLKey: {
				Description: "how long an access token is valid, which is also the span an external verifier " +
					"may accept a token the process itself has not seen",
				Domain: settings.DomainOf("durations from 1m to 1h", accessTTLInDomain),
			},
			leewayKey: {
				Description: "how much clock skew is allowed when a token's times are compared, so replicas " +
					"whose clocks differ do not refuse each other's tokens",
				Domain: settings.DomainOf("durations from 0 to 120s", leewayInDomain),
			},
			refreshIdleTTLKey: {
				Description: "how long a refresh token may go unused before it expires, reset on every rotation; " +
					"the absolute lifetime is the session's",
				Domain: settings.DomainOf("durations from 1h to 2160h", refreshIdleInDomain),
			},
			refreshGraceKey: {
				Description: "how long after a refresh token was used a second presentation is read as a retry " +
					"rather than a reuse; 0 is strict",
				Domain: settings.DomainOf("durations from 0 to 60s", refreshGraceInDomain),
			},
			checkSessionKey: {
				Description: "whether the verifying backend checks the session a token names; false is the " +
					"stateless mode where a revocation takes effect only at the token's expiry",
				Domain: settings.DomainOf("true or false", checkSessionInDomain),
			},
		},
	}
}

// settingReader reads the settings this module acts on. The store is nil when
// the assembly has none, and then every setting reads as its declared default.
// A read that fails and a value outside the setting's domain both read as the
// declared default and are reported at Error, once for as long as the same
// fault stands.
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

func (r *settingReader) accessTTL() time.Duration {
	return readSetting(r, accessTTLPath, defaults.AccessTTL, accessTTLInDomain)
}

func (r *settingReader) leeway() time.Duration {
	return readSetting(r, leewayPath, defaults.Leeway, leewayInDomain)
}

func (r *settingReader) refreshIdleTTL() time.Duration {
	return readSetting(r, refreshIdleTTLPath, defaults.RefreshIdleTTL, refreshIdleInDomain)
}

func (r *settingReader) refreshGrace() time.Duration {
	return readSetting(r, refreshGracePath, defaults.RefreshGrace, refreshGraceInDomain)
}

func (r *settingReader) checkSession() bool {
	return readSetting(r, checkSessionPath, defaults.CheckSession, checkSessionInDomain)
}

func (r *settingReader) checkTTL() time.Duration {
	return readSetting(r, checkTTLPath, defaultCheckTTL, checkTTLInDomain)
}
