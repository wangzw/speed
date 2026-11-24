// Package webui serves one built frontend bundle over HTTP. It answers GET and
// HEAD for the bundle, falls back to index.html for a path naming a client-side
// route, and answers 404 for a static resource.
package webui

import (
	"context"
	"fmt"
	"io/fs"
	nethttp "net/http"

	"github.com/vislake/speed/pkg/config"
	"github.com/vislake/speed/pkg/core"
	speedhttp "github.com/vislake/speed/pkg/http"
)

// ModuleName is this module's name in the registry and the namespace its input
// items are mounted under.
const ModuleName = "webui"

// ConfigNamespace is where this module's input items are mounted.
const ConfigNamespace = ModuleName

const (
	endpointKey         = "endpoint"
	assetPrefixKey      = "asset-prefix"
	resourcePrefixesKey = "resource-prefixes"
	endpointPath        = ConfigNamespace + "." + endpointKey
)

// defaultAssetPrefix is the directory under which a build tool collects its
// content-addressed files, used when configuration names none.
const defaultAssetPrefix = "assets"

// defaultResourcePrefix is the reserved path prefix answered as a static-resource
// namespace when configuration names none: RFC 8615's well-known URI space.
const defaultResourcePrefix = ".well-known"

// Config is the set of input items this module accepts.
type Config struct {
	// Endpoint is the name of the listening endpoint whose path space this
	// module's route covers, by the name the entry point's configuration gives
	// it. It is required.
	Endpoint string `config:"endpoint"`
	// AssetPrefix is the directory name, in the bundle, of the content-addressed
	// subtree, answered with a long immutable cache lifetime; everything else is
	// answered no-cache. It defaults to "assets".
	AssetPrefix string `config:"asset-prefix"`
	// ResourcePrefixes are path prefixes of static-resource namespaces beyond the
	// content-addressed subtree: a path the bundle does not carry under one is
	// answered 404 and never the entry document. It defaults to [".well-known"].
	ResourcePrefixes []string `config:"resource-prefixes"`
}

var defaults = Config{
	AssetPrefix:      defaultAssetPrefix,
	ResourcePrefixes: []string{defaultResourcePrefix},
}

func schema() config.Schema {
	return config.Schema{
		Namespace: ConfigNamespace,
		Mounts:    []config.Mount{{Value: &defaults}},
		Items: map[string]config.Item{
			endpointKey: {
				Description: "the name of the listening endpoint the frontend is served " +
					"on, by the name the entry point's configuration gives it; required",
				Required: true,
			},
			assetPrefixKey: {
				Description: "the directory name, in the bundle, of the content-addressed " +
					"files answered with a long, immutable cache lifetime; every other file " +
					"is answered with no-cache, and the default is " + defaultAssetPrefix,
			},
			resourcePrefixesKey: {
				Description: "path prefixes, in the bundle, of static-resource namespaces: " +
					"a path the bundle does not carry that falls under one is answered 404 " +
					"and never the entry document; the default is " + defaultResourcePrefix,
			},
		},
	}
}

// New binds this module to the built frontend bundle it delivers; a nil fsys is
// refused in Prepare with ErrNilAssets.
func New(fsys fs.FS) core.Module {
	return core.Module{
		Name:      ModuleName,
		Requires:  []core.Requirement{{Token: (*speedhttp.Router)(nil)}},
		Resources: []any{schema()},

		Prepare: func(_ context.Context, reg *core.Registry) (core.Enablement, error) {
			if fsys == nil {
				return core.Enablement{}, fmt.Errorf("%s: New was handed no asset filesystem, so "+
					"there is no frontend to serve: %w", ModuleName, ErrNilAssets)
			}
			if _, err := readConfig(reg); err != nil {
				return core.Enablement{}, err
			}
			return core.Enablement{State: core.StateEnabled}, nil
		},

		New: func(_ context.Context, reg *core.Registry) (any, error) {
			cfg, err := readConfig(reg)
			if err != nil {
				return nil, err
			}
			return newHandler(fsys, cfg), nil
		},

		Init: func(_ context.Context, reg *core.Registry, instance any) error {
			h, ok := instance.(*handler)
			if !ok {
				return nil
			}
			router, err := core.Resolve[speedhttp.Router](reg)
			if err != nil {
				return fmt.Errorf("%s: binding the frontend's route needs the entry point's "+
					"registration surface: %w", ModuleName, err)
			}
			endpoint, err := router.Endpoint(h.cfg.Endpoint)
			if err != nil {
				return fmt.Errorf("%s: binding the frontend's route on the endpoint named by %s: %w",
					ModuleName, endpointPath, err)
			}
			endpoint.Route(nethttp.MethodGet, wildcardRoute, h.handle)
			endpoint.Route(nethttp.MethodHead, wildcardRoute, h.handle)
			return nil
		},
	}
}

func readConfig(reg *core.Registry) (Config, error) {
	reader, err := core.Resolve[config.Reader](reg)
	if err != nil {
		return Config{}, fmt.Errorf("%s: this module's input items are read from configuration, "+
			"and no reader was handed to the registry before the assembly began: %w", ModuleName, err)
	}
	cfg := Config{}
	if err := reader.Decode(ConfigNamespace, &cfg); err != nil {
		return Config{}, err
	}
	return cfg, nil
}
