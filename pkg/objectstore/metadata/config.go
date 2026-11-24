package metadata

import (
	"fmt"
	"time"

	"github.com/vislake/speed/pkg/config"
	"github.com/vislake/speed/pkg/objectstore"
)

const (
	contentEndpointsItem = "content-endpoints"
	publicURLItem        = "public-url"
	sweepItem            = "sweep-interval"

	contentEndpointsKey = ConfigNamespace + "." + contentEndpointsItem
)

const allEndpoints = "*"

const defaultSweepInterval = time.Minute

var configDefaults = moduleConfig{
	SweepInterval: defaultSweepInterval,
}

type moduleConfig struct {
	ContentEndpoints []string      `config:"content-endpoints"`
	PublicURL        string        `config:"public-url"`
	SweepInterval    time.Duration `config:"sweep-interval"`
}

type resolved struct {
	contentEndpoints []string
	publicURL        string
	sweepInterval    time.Duration
}

func (c moduleConfig) resolve() (resolved, error) {
	return resolved{
		contentEndpoints: c.ContentEndpoints,
		publicURL:        c.PublicURL,
		sweepInterval:    c.SweepInterval,
	}, nil
}

func validateContentEndpoints(names []string) error {
	if len(names) == 0 {
		return fmt.Errorf("%w: %s is empty. The content route is mounted on the endpoints this module "+
			"is told to and on none by default; list the endpoints that should carry it",
			objectstore.ErrInvalidConfig, contentEndpointsKey)
	}
	seen := make(map[string]struct{}, len(names))
	for i, name := range names {
		switch name {
		case "":
			return fmt.Errorf("%w: %s holds an empty name at position %d",
				objectstore.ErrInvalidConfig, contentEndpointsKey, i)
		case allEndpoints:
			return fmt.Errorf("%w: %s holds %q at position %d. This module takes concrete endpoint "+
				"names only: mounting the content route on every endpoint would put it on the public ones, "+
				"where the wrong layers guard it", objectstore.ErrInvalidConfig, contentEndpointsKey, allEndpoints, i)
		}
		if _, twice := seen[name]; twice {
			return fmt.Errorf("%w: %s names the endpoint %q twice, which would register "+
				"every route on it twice", objectstore.ErrInvalidConfig, contentEndpointsKey, name)
		}
		seen[name] = struct{}{}
	}
	return nil
}

func schema() config.Schema {
	return config.Schema{
		Namespace: ConfigNamespace,
		Mounts:    []config.Mount{{Value: &configDefaults}},
		Items: map[string]config.Item{
			contentEndpointsItem: {
				Origins: config.OriginPrimary,
				Description: "the listening endpoints the content route is mounted on, by the names the " +
					"entry point's own configuration gives them; concrete names only, never *. Required of " +
					"a run that serves the content route — one with an entry point whose backend mints no " +
					"direct address — and unread by a run that does not",
			},
			publicURLItem: {
				Origins: config.OriginPrimary,
				Description: "the external origin a minted content address is prefixed with, such as " +
					"https://api.example.com, without a trailing slash; empty answers a root-relative address, " +
					"which is right only for a same-origin deployment",
			},
			sweepItem: {
				Origins: config.OriginPrimary,
				Description: "how often the background sweep runs; zero or negative turns the sweep off, " +
					"leaving expired rows until one is read",
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
