package journal

import (
	"fmt"
	"strings"

	"github.com/vislake/speed/pkg/config"
)

const (
	endpointsItem = "endpoints"
	requestsItem  = "requests"
	eventsItem    = "events"
)

type moduleConfig struct {
	// Endpoints names the listening endpoints the request-level capture layer
	// is registered on. Left empty, that layer is registered on none of them.
	Endpoints []string `config:"endpoints"`

	// Requests names the requests to record, each as a method and a route
	// path separated by one space, the path beginning with a slash and
	// written the way the route was registered: "GET /contacts/:id".
	Requests []string `config:"requests"`

	// Events names the event types to subscribe to, by their exact type name.
	Events []string `config:"events"`
}

type requestItem struct {
	method string
	path   string
}

type settings struct {
	endpoints []string
	requests  []requestItem
	events    []string
}

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

func parseRequestItem(entry string) (requestItem, bool) {
	method, path, found := strings.Cut(entry, " ")
	if !found || method == "" || !strings.HasPrefix(path, "/") {
		return requestItem{}, false
	}
	return requestItem{method: method, path: path}, true
}
