package journal

import (
	"fmt"
	"strings"

	"github.com/vislake/speed/pkg/config"
)

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
// the entry point offers no way to list the routes it has registered, so there
// is nothing to expand a wildcard against.
type moduleConfig struct {
	// Endpoints names the listening endpoints the request-level capture layer
	// is registered on. Left empty, that layer is registered on none of them.
	Endpoints []string `config:"endpoints"`

	// Requests names the requests to record, each as a method and a route path
	// separated by one space, the path beginning with a slash and written the
	// way the route was registered: "GET /contacts/:id". Every request applies
	// on every endpoint listed above; the two lists carry no pairing.
	Requests []string `config:"requests"`

	// Events names the event types to subscribe to, by their exact type name.
	Events []string `config:"events"`
}

// requestItem is one entry of the requests input item: the method of a request
// and the route path it was dispatched under. The capture layer compares the
// pair with the matched request as it is, character for character.
type requestItem struct {
	method string
	path   string
}

// settings is the configuration this module runs on.
type settings struct {
	endpoints []string
	requests  []requestItem
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
				Description: "the requests to record, each as a method and a route path " +
					"separated by one space, the path written as the route was registered, " +
					"such as \"GET /contacts/:id\", or empty to record no request at all",
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
// The one thing it checks is the shape of a request: a string that is not a
// method, a space and a path that begins with a slash can never equal a request,
// because a route is registered under such a path, and an entry that cannot is a
// typing mistake the host would otherwise find out about only by the rows that
// never appear. Whether a well-formed request names a route that is registered
// is not checked, because nothing lists the routes an endpoint has.
func readSettings(reader config.Reader) (settings, error) {
	var cfg moduleConfig
	if err := reader.Decode(ConfigNamespace, &cfg); err != nil {
		return settings{}, err
	}
	requests := make([]requestItem, 0, len(cfg.Requests))
	for i, entry := range cfg.Requests {
		item, ok := parseRequestItem(entry)
		if !ok {
			return settings{}, fmt.Errorf("%s%s: entry %d of %s.%s is %q, which is not a request: "+
				"write each one as a method, one space and a route path that begins with a slash, "+
				"the path as the route was registered, for example \"GET /contacts/:id\"",
				diagnosticPrefix, ModuleName, i, ConfigNamespace, requestsItem, entry)
		}
		requests = append(requests, item)
	}
	return settings{
		endpoints: cfg.Endpoints,
		requests:  requests,
		events:    cfg.Events,
	}, nil
}

// parseRequestItem splits an entry of the requests input item at its first
// space into a method and a path. It reports false when there is no space, when
// the method is empty, or when the path does not begin with a slash: a second
// space ahead of the path is part of the path and fails that way.
func parseRequestItem(entry string) (requestItem, bool) {
	method, path, found := strings.Cut(entry, " ")
	if !found || method == "" || !strings.HasPrefix(path, "/") {
		return requestItem{}, false
	}
	return requestItem{method: method, path: path}, true
}
