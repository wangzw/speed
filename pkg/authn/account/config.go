package account

import (
	"fmt"
	"time"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/authn/internal/password"
	"github.com/vislake/speed/pkg/config"
	"github.com/vislake/speed/pkg/settings"
)

// The keys this module accepts, relative to its namespace. The two remaining
// configuration items fix what is allocated at construction, so a change to one
// requires a restart; the rest are settings, read from the settings store while
// the process runs.
const (
	concurrencyItem       = "password-concurrency"
	signupConcurrencyItem = "signup-concurrency"
	memoryBudgetItem      = "password-memory-budget-mib"

	algorithmKey       = "password-algorithm"
	signupKey          = "signup"
	passwordSignupKey  = "password-signup"
	signupIPLimitKey   = "signup-ip-limit"
	signupHourlyKey    = "signup-global-hourly"
	unverifiedTTLKey   = "unverified-ttl"
	unverifiedSweepKey = "unverified-sweep-interval"
	autoLinkKey        = "auto-link"
	minLengthKey       = "password-min-length"
	argonMemoryKey     = "password-argon2-memory-kib"
	argonIterationKey  = "password-argon2-iterations"
	argonLanesKey      = "password-argon2-parallelism"
	pbkdf2IterKey      = "password-pbkdf2-iterations"
	freeAttemptsKey    = "throttle-free-attempts"
	maxDelayKey        = "throttle-max-delay"
	decayKey           = "throttle-decay"
	retentionKey       = "secret-retention"

	algorithmPath       = ConfigNamespace + "." + algorithmKey
	signupPath          = ConfigNamespace + "." + signupKey
	passwordSignupPath  = ConfigNamespace + "." + passwordSignupKey
	signupIPLimitPath   = ConfigNamespace + "." + signupIPLimitKey
	signupHourlyPath    = ConfigNamespace + "." + signupHourlyKey
	unverifiedTTLPath   = ConfigNamespace + "." + unverifiedTTLKey
	unverifiedSweepPath = ConfigNamespace + "." + unverifiedSweepKey
	autoLinkPath        = ConfigNamespace + "." + autoLinkKey
	minLengthPath       = ConfigNamespace + "." + minLengthKey
	argonMemoryPath     = ConfigNamespace + "." + argonMemoryKey
	argonIterationPath  = ConfigNamespace + "." + argonIterationKey
	argonLanesPath      = ConfigNamespace + "." + argonLanesKey
	pbkdf2IterPath      = ConfigNamespace + "." + pbkdf2IterKey
	freeAttemptsPath    = ConfigNamespace + "." + freeAttemptsKey
	maxDelayPath        = ConfigNamespace + "." + maxDelayKey
	decayPath           = ConfigNamespace + "." + decayKey
	retentionPath       = ConfigNamespace + "." + retentionKey
)

// The values the switches that relax a check take. The strict one is the default
// of each.
const (
	signupClosed     = "closed"
	signupOpen       = "open"
	autoLinkNever    = "never"
	autoLinkVerified = "verified-email"
)

// The sanity bounds on the sizes configuration states. A derivation width of
// 256 is beyond any machine this runs on, and a budget past a tebibyte is a
// typing mistake that is better refused than allocated.
const (
	maxConcurrency     = 256
	maxMemoryBudgetMiB = 1 << 20
	// memoryBudgetFloorMiB is the least a memory budget may be: twice the
	// largest memory one derivation may take, so that one derivation is never
	// most of the budget and two of the largest still run together.
	memoryBudgetFloorMiB = 2 * password.MaxMemoryKiB / 1024
)

// The bounds of the settings other than those the back-off shares with the
// static table, which password declares.
const (
	minPasswordLengthFloor, minPasswordLengthCeil = 8, 64
	minThrottleFree, maxThrottleFree              = 1, 10
	minThrottleDelay, maxThrottleDelay            = 10 * time.Second, 24 * time.Hour
	minThrottleDecay, maxThrottleDecay            = time.Minute, 24 * time.Hour
	minRetention, maxRetention                    = time.Hour, 8760 * time.Hour

	// signupIPLimitCeil is how many registrations one source address may make in
	// a minute, at most; the minute is the setting's own unit, not its value.
	signupIPLimitCeil = 10000
	// signupHourlyFloor and signupHourlyCeil bound the registrations of the whole
	// deployment in an hour.
	signupHourlyFloor, signupHourlyCeil = 1, 1000000
	// unverifiedTTLCeil is how long a self-registered account may stay without a
	// proved address; zero turns the sweep off.
	unverifiedTTLCeil = 8760 * time.Hour
	// minUnverifiedSweep and maxUnverifiedSweep bound how often that sweep runs.
	minUnverifiedSweep, maxUnverifiedSweep = time.Minute, 24 * time.Hour
)

// moduleConfig is the set of input items this module accepts.
type moduleConfig struct {
	// PasswordConcurrency is how many password derivations run at once, fixed
	// when the module is built.
	PasswordConcurrency int `config:"password-concurrency"`
	// SignupConcurrency is how many self-registration derivations run at once,
	// a capacity of its own that never takes a place from a password login and
	// carries no memory budget, fixed when the module is built.
	SignupConcurrency int `config:"signup-concurrency"`
	// PasswordMemoryBudgetMiB is the memory the derivations of this module may
	// hold at once, fixed when the module is built.
	PasswordMemoryBudgetMiB int `config:"password-memory-budget-mib"`
}

// configDefaults is what this module is worth without a configuration file.
var configDefaults = moduleConfig{
	PasswordConcurrency:     password.DefaultConcurrency(),
	SignupConcurrency:       password.DefaultConcurrency(),
	PasswordMemoryBudgetMiB: password.DefaultMemoryBudgetKiB / 1024,
}

// options carries the settings items and their declared defaults.
type options struct {
	PasswordAlgorithm        string        `config:"password-algorithm"`
	Signup                   string        `config:"signup"`
	PasswordSignup           string        `config:"password-signup"`
	SignupIPLimit            int           `config:"signup-ip-limit"`
	SignupGlobalHourly       int           `config:"signup-global-hourly"`
	UnverifiedTTL            time.Duration `config:"unverified-ttl"`
	UnverifiedSweepInterval  time.Duration `config:"unverified-sweep-interval"`
	AutoLink                 string        `config:"auto-link"`
	PasswordMinLength        int           `config:"password-min-length"`
	PasswordArgon2Memory     int           `config:"password-argon2-memory-kib"`
	PasswordArgon2Iterations int           `config:"password-argon2-iterations"`
	PasswordArgon2Lanes      int           `config:"password-argon2-parallelism"`
	PasswordPBKDF2Iterations int           `config:"password-pbkdf2-iterations"`
	ThrottleFreeAttempts     int           `config:"throttle-free-attempts"`
	ThrottleMaxDelay         time.Duration `config:"throttle-max-delay"`
	ThrottleDecay            time.Duration `config:"throttle-decay"`
	SecretRetention          time.Duration `config:"secret-retention"`
}

// defaults is the prototype the settings module reads the declared defaults off.
// It never receives a value. The algorithm is Argon2id, the stronger of the two;
// the Argon2id numbers are the least OWASP combination that costs the least
// memory; the password minimum is the domain's floor, which a deployment raises
// when its own credential policy asks for more; the rest are the strictest value
// of each range's usual end.
var defaults = options{
	PasswordAlgorithm:        string(password.Argon2id),
	Signup:                   signupClosed,
	PasswordSignup:           signupClosed,
	SignupIPLimit:            10,
	SignupGlobalHourly:       100,
	UnverifiedTTL:            720 * time.Hour,
	UnverifiedSweepInterval:  time.Hour,
	AutoLink:                 autoLinkNever,
	PasswordMinLength:        8,
	PasswordArgon2Memory:     int(password.DefaultParams().MemoryKiB),
	PasswordArgon2Iterations: int(password.DefaultParams().Iterations),
	PasswordArgon2Lanes:      int(password.DefaultParams().Parallelism),
	PasswordPBKDF2Iterations: password.DefaultPBKDF2Iterations,
	ThrottleFreeAttempts:     5,
	ThrottleMaxDelay:         15 * time.Minute,
	ThrottleDecay:            time.Hour,
	SecretRetention:          720 * time.Hour,
}

// The domain of each setting is the one statement of its range: the settings
// store holds a write to it, and a read is judged against it again, since a
// value stored before the domain was declared, or written around the store, is
// served as it is.
func algorithmInDomain(v string) bool {
	return v == string(password.Argon2id) || v == string(password.PBKDF2SHA256)
}

func signupInDomain(v string) bool { return v == signupClosed || v == signupOpen }

func passwordSignupInDomain(v string) bool { return v == signupClosed || v == signupOpen }

func signupIPLimitInDomain(n int) bool { return n >= 0 && n <= signupIPLimitCeil }

func signupHourlyInDomain(n int) bool { return n >= signupHourlyFloor && n <= signupHourlyCeil }

func unverifiedTTLInDomain(d time.Duration) bool { return d >= 0 && d <= unverifiedTTLCeil }

func unverifiedSweepInDomain(d time.Duration) bool {
	return d >= minUnverifiedSweep && d <= maxUnverifiedSweep
}

func autoLinkInDomain(v string) bool { return v == autoLinkNever || v == autoLinkVerified }

func minLengthInDomain(n int) bool {
	return n >= minPasswordLengthFloor && n <= minPasswordLengthCeil
}

func argonMemoryInDomain(n int) bool { return n >= 1 && n <= password.MaxMemoryKiB }

func argonIterationsInDomain(n int) bool { return n >= 1 && n <= password.MaxIterations }

func argonLanesInDomain(n int) bool { return n >= 1 && n <= password.MaxParallelism }

func pbkdf2IterationsInDomain(n int) bool {
	return n >= password.DefaultPBKDF2Iterations && n <= password.MaxPBKDF2Iterations
}

func freeAttemptsInDomain(n int) bool { return n >= minThrottleFree && n <= maxThrottleFree }

func maxDelayInDomain(d time.Duration) bool { return d >= minThrottleDelay && d <= maxThrottleDelay }

func decayInDomain(d time.Duration) bool { return d >= minThrottleDecay && d <= maxThrottleDecay }

func retentionInDomain(d time.Duration) bool { return d >= minRetention && d <= maxRetention }

// resolved is the configuration this module actually runs on.
type resolved struct {
	concurrency       int
	signupConcurrency int
	memoryBudget      int64 // KiB
}

// resolve turns decoded configuration into what the module runs on, or says
// which item to correct.
func (c moduleConfig) resolve() (resolved, error) {
	var r resolved
	if c.PasswordConcurrency < 1 || c.PasswordConcurrency > maxConcurrency {
		return resolved{}, fmt.Errorf("%w: %s.%s is %d, and derivations run from 1 to %d at once",
			authn.ErrInvalidConfig, ConfigNamespace, concurrencyItem, c.PasswordConcurrency, maxConcurrency)
	}
	r.concurrency = c.PasswordConcurrency
	if c.SignupConcurrency < 1 || c.SignupConcurrency > maxConcurrency {
		return resolved{}, fmt.Errorf("%w: %s.%s is %d, and self-registration derivations run from 1 to %d at once",
			authn.ErrInvalidConfig, ConfigNamespace, signupConcurrencyItem, c.SignupConcurrency, maxConcurrency)
	}
	r.signupConcurrency = c.SignupConcurrency
	if c.PasswordMemoryBudgetMiB < memoryBudgetFloorMiB || c.PasswordMemoryBudgetMiB > maxMemoryBudgetMiB {
		return resolved{}, fmt.Errorf("%w: %s.%s is %d MiB, and the budget is from %d MiB, twice the most "+
			"memory one derivation may take, to %d MiB",
			authn.ErrInvalidConfig, ConfigNamespace, memoryBudgetItem, c.PasswordMemoryBudgetMiB,
			memoryBudgetFloorMiB, maxMemoryBudgetMiB)
	}
	r.memoryBudget = int64(c.PasswordMemoryBudgetMiB) * 1024
	return r, nil
}

// schema declares this module's input items.
func schema() config.Schema {
	return config.Schema{
		Namespace: ConfigNamespace,
		Mounts:    []config.Mount{{Value: &configDefaults}},
		Items: map[string]config.Item{
			concurrencyItem: {
				Description: "how many password derivations of the account store run at once before " +
					"one waits for a free place",
			},
			signupConcurrencyItem: {
				Description: "how many password derivations of anonymous self-registration run at once, " +
					"a capacity of its own that takes no place from a password login and carries no " +
					"memory budget",
			},
			memoryBudgetItem: {
				Description: "the memory in MiB the password derivations of the account store may " +
					"hold at once, at least twice the largest memory one derivation may take",
			},
		},
	}
}

// settingsSchema declares this module's runtime parameters, each with the domain
// the code acts on.
func settingsSchema() settings.Schema {
	return settings.Schema{
		Namespace: ConfigNamespace,
		Mounts:    []settings.Mount{{Value: &defaults}},
		Items: map[string]settings.Item{
			algorithmKey: {
				Description: "the algorithm a new password hash is written with, argon2id or " +
					"pbkdf2-sha256; a deployment that has to run in FIPS 140 mode takes the second",
				Domain: settings.DomainOf("argon2id or pbkdf2-sha256", algorithmInDomain),
			},
			signupKey: {
				Description: "whether a first login through a social provider or an SMS code makes an " +
					"account, closed or open; open relaxes a check",
				Domain: settings.DomainOf("closed or open", signupInDomain),
			},
			passwordSignupKey: {
				Description: "whether an anonymous username-and-password self-registration opens an " +
					"account, closed or open; open relaxes a check",
				Domain: settings.DomainOf("closed or open", passwordSignupInDomain),
			},
			signupIPLimitKey: {
				Description: "how many self-registrations one source address may make in a minute, zero " +
					"turning the budget off",
				Domain: settings.DomainOf("whole numbers from 0 to 10000", signupIPLimitInDomain),
			},
			signupHourlyKey: {
				Description: "how many self-registrations the whole deployment may make in an hour",
				Domain:      settings.DomainOf("whole numbers from 1 to 1000000", signupHourlyInDomain),
			},
			unverifiedTTLKey: {
				Description: "how long a self-registered account that proved no email address may stay " +
					"before it is deleted, zero turning the sweep off",
				Domain: settings.DomainOf("durations from 0s to "+unverifiedTTLCeil.String(), unverifiedTTLInDomain),
			},
			unverifiedSweepKey: {
				Description: "how often the sweep of self-registered accounts without a proved address runs",
				Domain: settings.DomainOf("durations from "+minUnverifiedSweep.String()+" to "+
					maxUnverifiedSweep.String(), unverifiedSweepInDomain),
			},
			autoLinkKey: {
				Description: "whether a first social login whose provider proved an email address is " +
					"bound to the account that holds it, never or verified-email; verified-email " +
					"relaxes a check",
				Domain: settings.DomainOf("never or verified-email", autoLinkInDomain),
			},
			minLengthKey: {
				Description: "the fewest characters a password of the account store has",
				Domain:      settings.DomainOf("whole numbers from 8 to 64", minLengthInDomain),
			},
			argonMemoryKey: {
				Description: "the memory in KiB an Argon2id password hash is written with; the " +
					"combination with the two items beside it falls back to the default one when it " +
					"is below what OWASP recommends",
				Domain: settings.DomainOf("whole numbers from 1 to 262144", argonMemoryInDomain),
			},
			argonIterationKey: {
				Description: "the iterations an Argon2id password hash is written with",
				Domain:      settings.DomainOf("whole numbers from 1 to 10", argonIterationsInDomain),
			},
			argonLanesKey: {
				Description: "the lanes an Argon2id password hash is written with",
				Domain:      settings.DomainOf("whole numbers from 1 to 16", argonLanesInDomain),
			},
			pbkdf2IterKey: {
				Description: "the iterations a PBKDF2-SHA256 password hash is written with",
				Domain:      settings.DomainOf("whole numbers from 600000 to 10000000", pbkdf2IterationsInDomain),
			},
			freeAttemptsKey: {
				Description: "how many failed attempts one key may make before it is held back",
				Domain:      settings.DomainOf("whole numbers from 1 to 10", freeAttemptsInDomain),
			},
			maxDelayKey: {
				Description: "the longest a key is held back",
				Domain: settings.DomainOf("durations from "+minThrottleDelay.String()+" to "+
					maxThrottleDelay.String(), maxDelayInDomain),
			},
			decayKey: {
				Description: "how long a key may go without a failed attempt, once the wait its " +
					"failures earned is over, before its count returns to zero",
				Domain: settings.DomainOf("durations from "+minThrottleDecay.String()+" to "+
					maxThrottleDecay.String(), decayInDomain),
			},
			retentionKey: {
				Description: "how long the row of a service secret is kept after it expired or was revoked",
				Domain: settings.DomainOf("durations from "+minRetention.String()+" to "+
					maxRetention.String(), retentionInDomain),
			},
		},
	}
}

// readConfig decodes this module's section and validates it. Prepare and New both
// go through it, so the two see one table.
func readConfig(reader config.Reader) (resolved, error) {
	var cfg moduleConfig
	if err := reader.Decode(ConfigNamespace, &cfg); err != nil {
		return resolved{}, err
	}
	return cfg.resolve()
}
