package gate

import (
	"fmt"
	"time"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/config"
)

// The keys this module accepts, relative to its namespace. The failure this
// module reports when it has nothing to guard names the first of them, so a
// reader is told which key to write.
const (
	endpointsItem = "endpoints"
	budgetItem    = "call-budget"
	inFlightItem  = "in-flight"
)

// configDefaults is what this module is worth without a configuration file.
//
// There is no default for the endpoints: guarding one by guessing its name
// would be a guess about which traffic is public, and a gateway that guards
// nothing refuses to start, with the failure naming the key.
var configDefaults = moduleConfig{
	CallBudget: 5 * time.Second,
	InFlight:   64,
}

// moduleConfig is the set of input items this module accepts.
type moduleConfig struct {
	// Endpoints names the listening endpoints this gateway guards. The
	// names are the entry point's, and there is no way to ask it for the
	// set: its surface takes an endpoint by name and enumerates none. So
	// the list is this module's own input rather than something followed
	// from the entry point's configuration, which would tie this module's
	// configuration shape to another module's.
	Endpoints []string `config:"endpoints"`
	// CallBudget is the deadline one call into a backend is given, the
	// ownership question included, and the point past which its answer is
	// no longer honoured: a backend that answers late ends the request as
	// an outage rather than as a pass.
	//
	// It does not cut the wait short. The call is synchronous, so a backend
	// that ignores the deadline holds its goroutine and its in-flight slot
	// until it returns; abandoning it would either keep the slot, which
	// changes nothing, or release it and let hung goroutines grow without
	// bound. What bounds this module's own side is the gate below.
	CallBudget time.Duration `config:"call-budget"`
	// InFlight bounds how many calls into backends run at once. Past it a
	// request ends as an outage without waiting, so that a backend that
	// stopped answering cannot take every goroutine the server has.
	//
	// It counts calls, not requests. A bare subject is put to every open
	// claim at once and takes a slot for each, so the width has to be
	// chosen against the number of open claims a request can reach, not
	// against the number of requests to admit.
	InFlight int `config:"in-flight"`
}

// settings is the configuration this module actually runs on.
type settings struct {
	endpoints []string
	budget    time.Duration
	inFlight  int
}

// resolve turns decoded configuration into what the module runs on, refusing
// what cannot be run on.
func (c moduleConfig) resolve() (settings, error) {
	s := settings{endpoints: c.Endpoints, budget: c.CallBudget, inFlight: c.InFlight}
	seen := make(map[string]struct{}, len(c.Endpoints))
	for _, name := range c.Endpoints {
		if name == "" {
			return settings{}, fmt.Errorf("%w: %s.%s holds an empty endpoint name, which names "+
				"no endpoint the entry point has", authn.ErrInvalidConfig, ConfigNamespace, endpointsItem)
		}
		// A name written twice would register the layer twice on that
		// endpoint: every request would be authenticated twice, every
		// backend asked twice, and every conclusion counted twice. The
		// second registration buys nothing, so this is a mistake to
		// report rather than a duplicate to drop quietly.
		if _, twice := seen[name]; twice {
			return settings{}, fmt.Errorf("%w: %s.%s names the endpoint %q twice, which would "+
				"register the authentication layer on it twice and ask every backend "+
				"twice per request", authn.ErrInvalidConfig, ConfigNamespace, endpointsItem, name)
		}
		seen[name] = struct{}{}
	}
	if s.budget <= 0 {
		return settings{}, fmt.Errorf("%w: %s.%s is %s, and a call with no bound is one a hung "+
			"backend never gives back", authn.ErrInvalidConfig, ConfigNamespace, budgetItem, s.budget)
	}
	if s.inFlight <= 0 {
		return settings{}, fmt.Errorf("%w: %s.%s is %d, and a gateway that admits no call at all "+
			"refuses every request it is given", authn.ErrInvalidConfig, ConfigNamespace, inFlightItem, s.inFlight)
	}
	return s, nil
}

// schema declares this module's input items.
func schema() config.Schema {
	return config.Schema{
		Namespace: ConfigNamespace,
		Mounts:    []config.Mount{{Value: &configDefaults}},
		Items: map[string]config.Item{
			endpointsItem: {
				Origins:  config.OriginPrimary,
				Required: true,
				Description: "the listening endpoints this gateway guards, by the names the " +
					"entry point's own configuration gives them",
			},
			budgetItem: {
				Description: "how long one call into a credential-verifying backend may take " +
					"before the request ends as an outage",
			},
			inFlightItem: {
				Description: "how many calls into credential-verifying backends may run at " +
					"once before further requests end as an outage",
			},
		},
	}
}

// readSettings decodes this module's section and validates it. Prepare and New
// both go through it, so the two see one configuration.
func readSettings(reader config.Reader) (settings, error) {
	var cfg moduleConfig
	if err := reader.Decode(ConfigNamespace, &cfg); err != nil {
		return settings{}, err
	}
	return cfg.resolve()
}
