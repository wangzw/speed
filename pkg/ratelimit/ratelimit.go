// Package ratelimit registers two middleware layers: a quota on a settled
// identity, and an address shield that runs before any identity is settled.
package ratelimit

import (
	"time"

	"github.com/vislake/speed/pkg/config"
	"github.com/vislake/speed/pkg/settings"
)

// ModuleName is this module's name in the registry and the token that opens
// every diagnostic line the module writes.
const ModuleName = "ratelimit"

// ConfigNamespace is where this module's input items and settings are mounted.
const ConfigNamespace = ModuleName

// typePrefix starts the Problem.Type of every refusal this module writes.
const typePrefix = "urn:speed:ratelimit:"

const (
	windowKey = "window"
	limitKey  = "limit"

	windowPath = ConfigNamespace + "." + windowKey
	limitPath  = ConfigNamespace + "." + limitKey
)

// Limiter is the capability this module delivers; a layer writes it into its own
// After or Before to place itself inside or outside rate limiting. The method is
// unexported, so the identifier appears as (*Limiter)(nil).
type Limiter interface {
	limit()
}

const (
	endpointsKey  = "endpoints"
	endpointsPath = ConfigNamespace + "." + endpointsKey
)

// Config is the set of input items this module accepts; there is no default, and
// an empty endpoints list is a startup failure.
type Config struct {
	// Endpoints names the listening endpoints the quota layer is registered on.
	Endpoints []string `config:"endpoints"`

	// AddressEndpoints names the listening endpoints the address shield is
	// registered on; empty leaves the shield off.
	AddressEndpoints []string `config:"address-endpoints"`
}

type options struct {
	Window time.Duration `config:"window"`
	Limit  int           `config:"limit"`

	AddressRate    int `config:"address-rate"`
	AddressBurst   int `config:"address-burst"`
	AddressPenalty int `config:"address-penalty"`
}

var defaults = options{
	Window: time.Minute,
	Limit:  100,

	AddressRate:    4,
	AddressBurst:   80,
	AddressPenalty: 20,
}

func windowInDomain(window time.Duration) bool { return window > 0 && window <= maxWindow }

func limitInDomain(limit int) bool { return limit > 0 }

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

func readConfig(reader config.Reader) (Config, error) {
	var cfg Config
	if err := reader.Decode(ConfigNamespace, &cfg); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

// duplicateEndpoint reports the first repeated endpoint name; a repeat registers
// the same layer twice and halves the effective limit.
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
