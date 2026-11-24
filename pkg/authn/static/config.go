package static

import (
	"fmt"
	"regexp"
	"time"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/authn/internal/password"
	"github.com/vislake/speed/pkg/config"
	"github.com/vislake/speed/pkg/settings"
)

// The keys this module accepts, relative to its namespace: the first three are configuration
// read at startup, the last four settings read from the store at run time.
const (
	subjectsItem    = "subjects"
	cacheSizeItem   = "cache-size"
	concurrencyItem = "concurrency"

	cacheTTLKey     = "cache-ttl"
	freeAttemptsKey = "free-attempts"
	failWindowKey   = "fail-window"
	maxBackoffKey   = "max-backoff"

	cacheTTLPath     = ConfigNamespace + "." + cacheTTLKey
	freeAttemptsPath = ConfigNamespace + "." + freeAttemptsKey
	failWindowPath   = ConfigNamespace + "." + failWindowKey
	maxBackoffPath   = ConfigNamespace + "." + maxBackoffKey
)

// Sanity bounds on the two sizes configuration states: a cache of a million entries is a few
// hundred megabytes and a derivation width of 256 is beyond any machine this runs on.
const (
	maxCacheSize   = 1_000_000
	maxConcurrency = 256
)

// subjectName is what a subject of the table is called: it is the part of an
// account identifier after the static prefix, and it has to leave the whole
// within 64 bytes.
var subjectName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]{0,56}$`)

// configDefaults is empty: there is no default subject table, and a table naming no subject
// refuses the startup.
var configDefaults = moduleConfig{
	CacheSize:   1024,
	Concurrency: password.DefaultConcurrency(),
}

type moduleConfig struct {
	// Subjects maps each subject to its password entry in the format ParseHash reads; the value is
	// a hash, because a file carrying readable passwords hands over every account.
	Subjects map[string]string `config:"subjects"`
	// CacheSize is how many checks that held the cache remembers, allocated
	// when the module is built.
	CacheSize int `config:"cache-size"`
	// Concurrency is how many derivations run at once, fixed when the module is
	// built. One that finds the width taken is answered as an outage and does
	// not queue.
	Concurrency int `config:"concurrency"`
}

type options struct {
	CacheTTL     time.Duration `config:"cache-ttl"`
	FreeAttempts int           `config:"free-attempts"`
	FailWindow   time.Duration `config:"fail-window"`
	MaxBackoff   time.Duration `config:"max-backoff"`
}

// defaults is the prototype the settings module reads the declared defaults
// off. It never receives a value.
var defaults = options{
	CacheTTL:     60 * time.Second,
	FreeAttempts: password.DefaultThrottleParams().FreeAttempts,
	FailWindow:   password.DefaultThrottleParams().Window,
	MaxBackoff:   password.DefaultThrottleParams().MaxBackoff,
}

// The domains of the four settings: each is the one statement of its range, and a read is
// judged against it again, since a stored value may predate it.
func cacheTTLInDomain(d time.Duration) bool { return d >= 0 && d <= password.MaxCacheTTL }

func freeAttemptsInDomain(n int) bool {
	return n >= password.MinFreeAttempts && n <= password.MaxFreeAttempts
}

func failWindowInDomain(d time.Duration) bool {
	return d >= password.MinWindow && d <= password.MaxWindow
}

func maxBackoffInDomain(d time.Duration) bool {
	return d >= password.MinBackoff && d <= password.MaxBackoff
}

// resolved is the configuration this module actually runs on: the subject table
// with every entry already read, so that a malformed one is a startup failure
// rather than a refusal that only the holder of that account ever sees.
type resolved struct {
	subjects    map[string]password.Entry
	cacheSize   int
	concurrency int
	// secrets are the raw encoded entries of the subject table, in no fixed order; New registers
	// them as literal redaction rules because the parsed entries hold no string a rule could match.
	secrets []string
}

func (c moduleConfig) resolve() (resolved, error) {
	if c.CacheSize < 1 || c.CacheSize > maxCacheSize {
		return resolved{}, fmt.Errorf("%w: %s.%s is %d, and the cache holds from 1 to %d checks",
			authn.ErrInvalidConfig, ConfigNamespace, cacheSizeItem, c.CacheSize, maxCacheSize)
	}
	if c.Concurrency < 1 || c.Concurrency > maxConcurrency {
		return resolved{}, fmt.Errorf("%w: %s.%s is %d, and derivations run from 1 to %d at once",
			authn.ErrInvalidConfig, ConfigNamespace, concurrencyItem, c.Concurrency, maxConcurrency)
	}

	r := resolved{
		subjects:    make(map[string]password.Entry, len(c.Subjects)),
		cacheSize:   c.CacheSize,
		concurrency: c.Concurrency,
	}
	for subject, encoded := range c.Subjects {
		// The name is not echoed: it is in a table that is a secret as a whole,
		// and a name outside the format may be anything at all.
		if !subjectName.MatchString(subject) {
			return resolved{}, fmt.Errorf("%w: %s.%s carries a subject whose name is not "+
				"[A-Za-z0-9][A-Za-z0-9_-]{0,56}. The name follows %q in the account "+
				"identifier a pass carries, which is at most 64 bytes of ASCII letters, digits, "+
				"'_' and '-'", authn.ErrInvalidClaim, ConfigNamespace, subjectsItem, authn.StaticPrefix)
		}
		entry, err := password.Parse(encoded)
		if err != nil {
			// The hash is not echoed: it is the secret this table exists to
			// hold.
			return resolved{}, fmt.Errorf("%w: %s.%s has an unusable password hash for the "+
				"subject %q: %w", authn.ErrInvalidClaim, ConfigNamespace, subjectsItem, subject, err)
		}
		r.subjects[subject] = entry
		r.secrets = append(r.secrets, encoded)
	}
	return r, nil
}

func schema() config.Schema {
	return config.Schema{
		Namespace: ConfigNamespace,
		Mounts:    []config.Mount{{Value: &configDefaults}},
		Items: map[string]config.Item{
			subjectsItem: {
				Origins:   config.OriginPrimary,
				Required:  true,
				Sensitive: true,
				Description: "the subjects this backend answers for, each mapped to its password " +
					"entry as Hash or HashPBKDF2 writes it",
			},
			cacheSizeItem: {
				Description: "how many password checks that held the cache remembers; the " +
					"lifetime is the setting " + cacheTTLPath,
			},
			concurrencyItem: {
				Description: "how many password derivations run at once before a request is " +
					"answered as an outage",
			},
		},
	}
}

func settingsSchema() settings.Schema {
	return settings.Schema{
		Namespace: ConfigNamespace,
		Mounts:    []settings.Mount{{Value: &defaults}},
		Items: map[string]settings.Item{
			cacheTTLKey: {
				Description: "how long a password check that held is remembered, counted from the " +
					"derivation that vouched for it and not from the requests it served since, from 0, " +
					"which switches the cache off, to " + password.MaxCacheTTL.String(),
				Domain: settings.DomainOf("durations from 0 to "+password.MaxCacheTTL.String(), cacheTTLInDomain),
			},
			freeAttemptsKey: {
				Description: "how many failed attempts one subject name may make before it is held back",
				Domain:      settings.DomainOf("whole numbers from 1 to 20", freeAttemptsInDomain),
			},
			failWindowKey: {
				Description: "how long a subject name may go without a failed attempt before its count returns to zero",
				Domain: settings.DomainOf("durations from "+password.MinWindow.String()+" to "+
					password.MaxWindow.String(), failWindowInDomain),
			},
			maxBackoffKey: {
				Description: "the longest a subject name is held back",
				Domain: settings.DomainOf("durations from "+password.MinBackoff.String()+" to "+
					password.MaxBackoff.String(), maxBackoffInDomain),
			},
		},
	}
}

func readConfig(reader config.Reader) (resolved, error) {
	var cfg moduleConfig
	if err := reader.Decode(ConfigNamespace, &cfg); err != nil {
		return resolved{}, err
	}
	return cfg.resolve()
}
