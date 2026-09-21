package journal

import "github.com/vislake/speed/pkg/config"

// The three input items, relative to the namespace.
const (
	endpointsItem = "endpoints"
	requestsItem  = "requests"
	eventsItem    = "events"
)

// moduleConfig is the set of input items this module accepts.
//
// All three default to empty, and empty means "capture nothing on this path".
// That is the whole of this module's configuration: what to watch is named
// here and never inferred, because the event bus matches type names exactly and
// the capture layer sits outside the routing engine, so there is no wildcard
// and no dispatch query to lean on.
type moduleConfig struct {
	// Endpoints names the listening endpoints the request-level capture layer
	// is registered on. Left empty, that layer is registered on none of them.
	Endpoints []string `config:"endpoints"`

	// Requests names the requests to record, as a method and a path pattern
	// written the way the routing engine writes them. Every pattern applies on
	// every endpoint listed above; the two lists carry no pairing.
	Requests []string `config:"requests"`

	// Events names the event types to subscribe to, by their exact type name.
	Events []string `config:"events"`
}

// settings is the configuration this module runs on.
type settings struct {
	endpoints []string
	requests  []string
	events    []string
}

// schema declares this module's input items. The list is collected over every
// registered module whether or not it runs, so the namespace has to be this
// module's own name: two modules declaring the same path is a startup failure
// even when only one of them ever runs.
func schema() config.Schema {
	return config.Schema{
		Namespace: ConfigNamespace,
		Mounts:    []config.Mount{{Value: &moduleConfig{}}},
		Items: map[string]config.Item{
			endpointsItem: {
				Description: "the listening endpoints whose requests are recorded, " +
					"or empty to record no request at all",
			},
			requestsItem: {
				Description: "the requests to record, as method and path pattern such as " +
					"\"GET /things\", or empty to record no request at all",
			},
			eventsItem: {
				Description: "the event type names to record, or empty to subscribe to none",
			},
		},
	}
}

// readSettings decodes this module's section. Prepare and New both go through
// it, so the two see one configuration.
//
// Nothing here is validated: a pattern the matcher cannot read is refused in
// Init, where the matcher is built, and that is the only way any of these three
// values can be wrong.
func readSettings(reader config.Reader) (settings, error) {
	var cfg moduleConfig
	if err := reader.Decode(ConfigNamespace, &cfg); err != nil {
		return settings{}, err
	}
	return settings{
		endpoints: cfg.Endpoints,
		requests:  cfg.Requests,
		events:    cfg.Events,
	}, nil
}
