// Package webui serves one built frontend bundle over HTTP.
//
// A host hands the bundle to New as an fs.FS — an embed.FS for a deployment
// baked into the binary, os.DirFS(dir) for one served from disk — and this
// package answers GET and HEAD requests for it on one listening endpoint. It
// reads paths out of that filesystem, answers the file a path names together
// with a cache header, and for a path the bundle does not carry falls back to
// the entry document index.html when the path names a client-side route, which
// is how a client-side router's deep links reach the single page that renders
// them, and answers 404 when it names a static resource.
//
// # What it does and does not do
//
// It serves the bundle as it is. It does not read, rewrite or re-encode a
// file's bytes: compression, content hashing and any other build-time transform
// are the build tool's, and the bundle arrives already carrying their result.
// The media type of a response is the one net/http's ServeContent derives from
// the file's name, or sniffs from its content when the name gives it nothing to
// go on. Conditional requests (If-None-Match, If-Modified-Since) and ranges are
// ServeContent's as well; this package hands it the content and the file's
// information and nothing more.
//
// It knows no application and no frontend framework. The bundle it serves is
// whatever bytes it was handed; the module can tell neither which application
// nor which tool produced them. It does not parse a client-side route table —
// the fallback keys off the path's shape alone — and it does not render
// templates or a page on the server.
//
// It authenticates nothing, resolves no tenant and limits no traffic. Those are
// the jobs of the layers a host registers around it; which layers sit on the
// endpoint is the host's decision, not this module's.
//
// # The endpoint
//
// The module is a leaf: it delivers no capability, nothing depends on it, and a
// dependency closure never pulls it in — a host brings it in by naming it on a
// command tree. On the endpoint configuration names it registers one catch-all
// route (gin's /*path grammar) for GET and one for HEAD, covering the whole
// path space of that endpoint. Because a catch-all and any concrete route on
// the same method tree are mutually exclusive in gin, the endpoint this module
// is given must not carry another route: mounting both is a route conflict, and
// the startup ends with it. A host that needs an API and a frontend together
// puts them on different endpoints, or puts a reverse proxy in front that
// routes each to its own endpoint.
//
// # Cache and fallback
//
// The content-addressed subtree of the bundle — the directory named by the
// asset-prefix input item, "assets" by default — is served with a long,
// immutable cache lifetime, since a file under it never changes under its name.
// The entry document and every other file are served with no-cache, so a
// browser revalidates them. A path the bundle does not carry is answered by the
// entry document when it names a client-side route, a path with no file
// extension like /console/tenants, and as a 404 when it names a static
// resource: a file under the content-addressed subtree, a path whose final
// segment carries a file extension like /runtime-config.json, or a path under a
// configured resource prefix — /.well-known by default — where an
// extensionless resource such as an ACME challenge token lives. A missing
// hashed file means a stale index.html points at a package this bundle does not
// ship, and a missing runtime resource is a deployment gap; answering either
// with an HTML document would hide it behind a 200 — a browser would run the
// document as a script, or a JSON parser would fail with no signal that the
// file is what is absent.
//
// An extensionless resource at an arbitrary path — a root CNAME, a bare
// /favicon — carries nothing in its shape to tell it from a client-side route,
// which is a path with no extension too. It falls on the fallback side, and a
// deployment that serves one places it under a resource prefix or gives it a
// file extension; the resource-prefixes input item adds namespaces for exactly
// that.
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

// ModuleName is this module's name in the registry and on a command tree. It
// is also the namespace its input items are mounted under.
const ModuleName = "webui"

// ConfigNamespace is where this module's input items are mounted in the config
// data.
const ConfigNamespace = ModuleName

// The input items this module accepts, and their full paths in the config data.
// The full path is the namespace, a dot, then the item key; it is what a
// missing-required failure names.
const (
	endpointKey         = "endpoint"
	assetPrefixKey      = "asset-prefix"
	resourcePrefixesKey = "resource-prefixes"
	endpointPath        = ConfigNamespace + "." + endpointKey
)

// defaultAssetPrefix is the directory under which a build tool collects its
// content-addressed files, used when configuration names none.
const defaultAssetPrefix = "assets"

// defaultResourcePrefix is the one reserved path prefix answered as a static
// resource namespace when configuration names none: RFC 8615's well-known URI
// space, which no client-side router owns and under which extensionless
// resources such as an ACME HTTP-01 challenge token are served.
const defaultResourcePrefix = ".well-known"

// Config is the set of input items this module accepts.
type Config struct {
	// Endpoint is the name of the listening endpoint whose path space this
	// module's route covers, by the name the entry point's configuration gives
	// it. It is required.
	Endpoint string `config:"endpoint"`
	// AssetPrefix is the directory name, in the bundle, of the content-addressed
	// subtree: the files under it are answered with a long, immutable cache
	// lifetime, everything else with no-cache. It defaults to "assets".
	AssetPrefix string `config:"asset-prefix"`
	// ResourcePrefixes are path prefixes, in the bundle, of static-resource
	// namespaces beyond the content-addressed subtree: a path the bundle does
	// not carry that falls under one of them is answered 404 and never the entry
	// document, whatever its final segment looks like. It is how an
	// extensionless resource is refused rather than answered with HTML, since a
	// path like /CNAME or /favicon carries no extension to tell it apart from a
	// client-side route. It defaults to [".well-known"].
	ResourcePrefixes []string `config:"resource-prefixes"`
}

// defaults is the carrier prototype configuration is read against: its field
// values are the declared defaults. It is never given a run's values.
var defaults = Config{
	AssetPrefix:      defaultAssetPrefix,
	ResourcePrefixes: []string{defaultResourcePrefix},
}

// schema declares this module's input items.
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

// New binds this module to the built frontend bundle it delivers.
//
// fsys is the root of the built bundle: an embed.FS for a deployment embedded
// in the binary, os.DirFS(dir) for one served from disk. The module reads
// nothing outside it and knows neither the application nor the tool that
// produced it. A nil fsys is refused in Prepare, which ends the startup with
// ErrNilAssets; an empty but non-nil bundle is not a failure — it is served,
// answering 404 to what it does not carry, so a frontend may be built and
// deployed after the process has started.
func New(fsys fs.FS) core.Module {
	return core.Module{
		Name: ModuleName,
		// The registration surface is required: the module's whole purpose is to
		// bind one route on an endpoint, and there is no endpoint to bind without
		// it. There is no optional form to fall back to.
		Requires:  []core.Requirement{{Token: (*speedhttp.Router)(nil)}},
		Resources: []any{schema()},

		Prepare: func(_ context.Context, reg *core.Registry) (core.Enablement, error) {
			if fsys == nil {
				return core.Enablement{}, fmt.Errorf("%s: New was handed no asset filesystem, so "+
					"there is no frontend to serve: %w", ModuleName, ErrNilAssets)
			}
			// The read is what reports a missing required item: the endpoint is
			// declared Required, so Decode fails with config.ErrMissingRequired
			// naming the full path.
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

		// The route is bound here and not earlier: the entry point's registration
		// surface is taken up at this stage, where every instance exists already.
		// An instance that is not this module's own product is one the host
		// supplied, and it is left alone.
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
			// One catch-all per method tree; GET and HEAD are separate trees, so
			// the two do not conflict. Every other method has no route on this
			// endpoint and the entry point's engine answers 405 with Allow.
			endpoint.Route(nethttp.MethodGet, wildcardRoute, h.handle)
			endpoint.Route(nethttp.MethodHead, wildcardRoute, h.handle)
			return nil
		},
	}
}

// readConfig takes up the configuration reader and decodes this module's
// section. Prepare and New both go through it, so the two see one
// configuration.
//
// The reader is a capability the host supplies before the drive begins, the way
// the entry point takes it up; it is not declared in Requires because there is
// no order to arrange, only a value to read.
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
