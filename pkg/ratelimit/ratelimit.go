// Package ratelimit registers two middleware layers: a quota on a settled
// identity, and an address shield that runs before any identity is settled.
//
// The quota is the per-identity layer. One such layer is registered on each
// endpoint this module's configuration names, a request that carries an
// identity string is counted once, and a request whose estimate exceeds the
// limit for the current window is answered 429 with a Retry-After header. Its
// counting goes through the key-value primitives, so the module holds no state
// of its own across replicas.
//
// The shield is a second, separate layer, registered on the endpoints its own
// list names. It runs in the guard stage, before identity is known, so its key
// is the client address; it counts that address in this process's memory and
// makes no network call of any kind, and it spends by outcome, charging a
// refusal far more than a success. Its cost is stated where it is implemented:
// each replica counts alone, so the effective limit is the per-replica limit
// times the number of replicas, and a flood spread over many addresses is not
// stopped by it at all.
package ratelimit

import (
	"time"

	"github.com/vislake/speed/pkg/config"
	"github.com/vislake/speed/pkg/settings"
)

// ModuleName is this module's name in the registry and the token that opens
// every diagnostic line the module writes. A host writes it into the commands
// whose traffic should be limited.
//
// The capability directory and the module name are the same string here, so
// the diagnostics carry this one token: a format that wrote a prefix as well
// would render the name twice.
const ModuleName = "ratelimit"

// ConfigNamespace is where this module's input items are mounted, and where
// its settings are declared. It is this module's own name, the way a
// registering module's namespace always is.
const ConfigNamespace = ModuleName

// typePrefix starts the Problem.Type of every refusal this module writes. The
// machine-readable code follows it, as it does in every capability's own
// answers; the only code this module writes today is throttled.
const typePrefix = "urn:speed:ratelimit:"

// The quota's two setting items, and the full paths they are declared and read
// under. The shield's three are declared the same way in address.go.
//
// The settings module registers every declared item under its full path — the
// namespace, a dot, then the item key — and looks a key up by that exact path,
// so a read names the full path and not the bare item key. The paths derive
// from ConfigNamespace and the item keys, and they have to stay in step with
// what settingsSchema registers: a read of a path nothing declares finds no
// declaration and fails open on every request.
const (
	windowKey = "window"
	limitKey  = "limit"

	windowPath = ConfigNamespace + "." + windowKey
	limitPath  = ConfigNamespace + "." + limitKey
)

// Limiter is the capability this module delivers. A layer writes it into its
// own After or Before to place itself inside or outside rate limiting.
//
// The method is unexported, so a caller that takes the instance up out of the
// registry can do nothing with it, and the identifier appears as
// (*Limiter)(nil), the token. The decision is made in the middleware and
// nowhere else. A type outside this package satisfies the interface by
// embedding it, which is how an instance the host supplies for this module
// delivers the capability.
type Limiter interface {
	limit()
}

// endpointsKey is the input item naming the endpoints to guard, and
// endpointsPath its full path, which the startup failure for an empty list
// names so the host knows which key to write.
const (
	endpointsKey  = "endpoints"
	endpointsPath = ConfigNamespace + "." + endpointsKey
)

// Config is the set of input items this module accepts.
//
// There is no default: an empty list means this module has no endpoint to
// guard, which is a startup failure rather than a module that quietly does
// nothing.
type Config struct {
	// Endpoints names the listening endpoints the quota layer is registered on.
	Endpoints []string `config:"endpoints"`

	// AddressEndpoints names the listening endpoints the address shield is
	// registered on. The two lists are separate so the two layers are switched
	// apart: an endpoint is guarded by the quota, the shield, both or neither
	// according to which list names it. An empty list leaves the shield off,
	// which is not a startup failure — a host that wants only the quota says
	// nothing here.
	AddressEndpoints []string `config:"address-endpoints"`
}

// options carries the two settings items and their declared defaults.
//
// A host that writes no row for either key gets these values. Both domains
// exclude zero: a limit of zero rejects every request that carries an identity
// string, because the increment happens before the decision, and a window of
// zero leaves the window index undefined. The window is bounded above as well,
// by the longest one whose double still fits a duration.
type options struct {
	Window time.Duration `config:"window"`
	Limit  int           `config:"limit"`

	// The shield's three settings. They are read from the global layer and
	// never a tenant's: the shield runs before any tenant is settled, so there
	// is no tenant row to read them from.
	AddressRate    int `config:"address-rate"`
	AddressBurst   int `config:"address-burst"`
	AddressPenalty int `config:"address-penalty"`
}

// defaults is the prototype the settings module reads the declared defaults
// off. It never receives a value.
//
// The shield's numbers are chosen for the endpoints it is meant for — an
// authentication endpoint, where a human client makes a request every few
// seconds at most and an attacker makes hundreds. Four tokens a second is more
// than a page load's worth of parallel requests and far above a human's click
// rate. A burst of eighty covers what one ordinary client produces back to
// back: a token renewal and the replay it triggers, and a person clicking
// through several steps in a row. A smaller burst lets that routine traffic
// empty the bucket and lock the client out until it refills. A refusal costs
// one plus a penalty of twenty — a quarter of the bucket — so an ordinary
// success pays one token while a 404, or a 401 that refused a credential, pays
// twenty-one: four refusals empty a full bucket. The penalty raises the one-time
// burst a source can spend, not its steady-state rate. Once the bucket is empty
// a source that keeps being refused is admitted at the refill rate, one request
// every quarter second, because each request spends only the tokens that have
// accrued; the penalty drains them again with the refusal. Lowering that
// steady-state rate is a different design decision — it belongs in the rate,
// not in the penalty — and is not part of this one. A step-up handshake — a 401
// that asks an already authenticated caller to prove itself again — is the same
// status but not the same event: a client repeats it as its session ages, so it
// pays the one token every request costs and no penalty, and cannot lock itself
// out of the handshake it has to complete. A normal client never gets near the
// penalty, so its throughput is unchanged.
var defaults = options{
	Window: time.Minute,
	Limit:  100,

	AddressRate:    4,
	AddressBurst:   80,
	AddressPenalty: 20,
}

// windowInDomain and limitInDomain are the two settings' domains: the values
// this layer acts on.
//
// Each is the one statement of its domain, and it is judged in two places.
// The settings declaration carries it, so the settings store refuses a write
// outside it before the row is written; thresholds applies it to what a
// decision reads, since a row stored before the domain was declared, or
// written around the store, is served as it is, and the layer stands aside on
// such a value rather than act on it.
func windowInDomain(window time.Duration) bool { return window > 0 && window <= maxWindow }

func limitInDomain(limit int) bool { return limit > 0 }

// schema declares this module's input items.
func schema() config.Schema {
	return config.Schema{
		Namespace: ConfigNamespace,
		Mounts:    []config.Mount{{Value: &Config{}}},
		Items: map[string]config.Item{
			endpointsKey: {
				Description: "the listening endpoints whose requests are limited, by the names " +
					"the entry point's configuration gives them; at least one is required",
			},
			addressEndpointsKey: {
				Description: "the listening endpoints whose requests the address shield " +
					"counts, by the names the entry point's configuration gives them; the " +
					"shield is registered on the endpoints this list names and on no others, " +
					"so an empty list leaves it off",
			},
		},
	}
}

// settingsSchema declares this module's runtime parameters, each with the
// domain this layer acts on.
func settingsSchema() settings.Schema {
	return settings.Schema{
		Namespace: ConfigNamespace,
		Mounts:    []settings.Mount{{Value: &defaults}},
		Items: map[string]settings.Item{
			windowKey: {
				Description: "the length of one counting window, which must be positive " +
					"and short enough for two of them to fit in a duration",
				Domain: settings.DomainOf("the positive durations up to "+maxWindow.String()+
					", the longest whose double a duration holds", windowInDomain),
			},
			limitKey: {
				Description: "the requests one identity string may make in one window " +
					"within one tenant, which must be positive",
				Domain: settings.DomainOf("the positive integers", limitInDomain),
			},
			addressRateKey: {
				Description: "the requests per second the address shield admits from one " +
					"client address, which must be positive",
				Domain: settings.DomainOf("the positive integers", addressRateInDomain),
			},
			addressBurstKey: {
				Description: "how many requests the address shield admits from one client " +
					"address in a burst, which must be positive",
				Domain: settings.DomainOf("the positive integers", addressBurstInDomain),
			},
			addressPenaltyKey: {
				Description: "the extra tokens a refusal (a 404, or a 401 that refused a " +
					"credential rather than asking for a step-up) costs a client address " +
					"against the shield's bucket, which must not be negative; zero leaves " +
					"the shield a plain per-address rate cap",
				Domain: settings.DomainOf("the non-negative integers", addressPenaltyInDomain),
			},
		},
	}
}

// readConfig decodes this module's section. Prepare and New both go through
// it, so the two see one configuration.
func readConfig(reader config.Reader) (Config, error) {
	var cfg Config
	if err := reader.Decode(ConfigNamespace, &cfg); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

// duplicateEndpoint reports the first endpoint name that appears more than
// once, if any.
//
// A repeated name registers the same layer twice, and two registrations are
// two layers: the endpoint then decides twice per request and the effective
// limit is halved. The chain listing shows two layers, but not that they come
// from one mistake.
func duplicateEndpoint(names []string) (string, bool) {
	seen := make(map[string]struct{}, len(names))
	for _, name := range names {
		if _, ok := seen[name]; ok {
			return name, true
		}
		seen[name] = struct{}{}
	}
	return "", false
}
