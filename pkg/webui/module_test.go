package webui

import (
	"context"
	"encoding/json"
	"errors"
	nethttp "net/http"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/vislake/speed/pkg/config"
	"github.com/vislake/speed/pkg/core"
	speedhttp "github.com/vislake/speed/pkg/http"
)

// TestTheModuleServesTheFrontendOnTheConfiguredEndpoint drives the whole
// module — New, the configuration, the route registration on the entry point —
// through a running assembly and reads the answers off a socket.
func TestTheModuleServesTheFrontendOnTheConfiguredEndpoint(t *testing.T) {
	h := startHost(t, bundle())

	t.Run("a file under the content-addressed subtree is answered and cached long", func(t *testing.T) {
		got := h.get(t, "/"+assetName)
		if got.status != nethttp.StatusOK {
			t.Fatalf("GET /%s answered %d, want 200", assetName, got.status)
		}
		if got.body != "console.log(1)" {
			t.Fatalf("GET /%s answered %q, want the file's bytes", assetName, got.body)
		}
		if cc := got.header.Get("Cache-Control"); cc != longCache {
			t.Fatalf("GET /%s carries Cache-Control %q, want %q", assetName, cc, longCache)
		}
	})

	t.Run("a file outside the subtree is answered and not cached", func(t *testing.T) {
		got := h.get(t, "/favicon.ico")
		if got.status != nethttp.StatusOK {
			t.Fatalf("GET /favicon.ico answered %d, want 200", got.status)
		}
		if cc := got.header.Get("Cache-Control"); cc != noCache {
			t.Fatalf("GET /favicon.ico carries Cache-Control %q, want %q", cc, noCache)
		}
	})

	t.Run("an unknown path falls back to the entry document", func(t *testing.T) {
		got := h.get(t, "/a/client/route")
		if got.status != nethttp.StatusOK {
			t.Fatalf("GET a client route answered %d, want 200 (the entry document)", got.status)
		}
		if got.body != entryDoc {
			t.Fatalf("GET a client route answered %q, want the entry document", got.body)
		}
		if cc := got.header.Get("Cache-Control"); cc != noCache {
			t.Fatalf("the fallback carries Cache-Control %q, want %q", cc, noCache)
		}
	})

	t.Run("a missing asset is refused and does not fall back", func(t *testing.T) {
		got := h.get(t, "/assets/missing.deadbeef.js")
		if got.status != nethttp.StatusNotFound {
			t.Fatalf("GET a missing asset answered %d, want 404 (no fallback)", got.status)
		}
		if ct := got.header.Get("Content-Type"); ct != "application/problem+json" {
			t.Fatalf("GET a missing asset answered media type %q, want a problem document", ct)
		}
		if got.body == entryDoc {
			t.Fatalf("GET a missing asset answered the entry document; an asset miss must not fall back")
		}
	})

	t.Run("a missing static resource is refused and does not fall back", func(t *testing.T) {
		got := h.get(t, "/runtime-config.json")
		if got.status != nethttp.StatusNotFound {
			t.Fatalf("GET a missing static resource answered %d, want 404 (no fallback)", got.status)
		}
		if ct := got.header.Get("Content-Type"); ct != "application/problem+json" {
			t.Fatalf("GET a missing static resource answered media type %q, want a problem document", ct)
		}
		if got.body == entryDoc {
			t.Fatalf("GET a missing static resource answered the entry document; " +
				"a static-resource miss must not fall back")
		}
	})

	t.Run("a missing resource under a reserved prefix is refused and does not fall back", func(t *testing.T) {
		got := h.get(t, "/.well-known/acme-challenge/sometoken")
		if got.status != nethttp.StatusNotFound {
			t.Fatalf("GET a reserved-prefix resource answered %d, want 404 (no fallback)", got.status)
		}
		if ct := got.header.Get("Content-Type"); ct != "application/problem+json" {
			t.Fatalf("GET a reserved-prefix resource answered media type %q, want a problem document", ct)
		}
		if got.body == entryDoc {
			t.Fatalf("GET a reserved-prefix resource answered the entry document; it must not fall back")
		}
	})

	t.Run("HEAD answers the headers and no body", func(t *testing.T) {
		got := h.send(t, nethttp.MethodHead, "/"+assetName)
		if got.status != nethttp.StatusOK {
			t.Fatalf("HEAD /%s answered %d, want 200", assetName, got.status)
		}
		if got.body != "" {
			t.Fatalf("HEAD /%s answered a body of %d bytes, want none", assetName, len(got.body))
		}
		if cc := got.header.Get("Cache-Control"); cc != longCache {
			t.Fatalf("HEAD /%s carries Cache-Control %q, want %q", assetName, cc, longCache)
		}
	})

	t.Run("a method other than GET or HEAD is answered 405 with Allow", func(t *testing.T) {
		got := h.send(t, nethttp.MethodPost, "/anything")
		if got.status != nethttp.StatusMethodNotAllowed {
			t.Fatalf("POST /anything answered %d, want 405", got.status)
		}
		allow := got.header.Get("Allow")
		if !strings.Contains(allow, nethttp.MethodGet) || !strings.Contains(allow, nethttp.MethodHead) {
			t.Fatalf("the 405 carries Allow %q, want it to name GET and HEAD", allow)
		}
		if ct := got.header.Get("Content-Type"); ct != "application/problem+json" {
			t.Fatalf("the 405 answered media type %q, want a problem document", ct)
		}
	})
}

// TestAnEmptyBundleIsServedAsAnEmptyEndpointRuns the module over a bundle with
// nothing in it through a real assembly: it must not fail the startup, and it
// answers 404 to everything.
func TestAnEmptyBundleIsServedAsAnEmptyEndpoint(t *testing.T) {
	h := startHost(t, fstest.MapFS{})

	for _, target := range []string{"/", "/a/client/route", "/assets/app.js"} {
		got := h.get(t, target)
		if got.status != nethttp.StatusNotFound {
			t.Fatalf("GET %s against an empty bundle answered %d, want 404", target, got.status)
		}
	}
}

// TestAMissingEndpointFailsTheStartup holds the configuration premise: the
// endpoint name is required, and its absence ends the startup naming the key.
func TestAMissingEndpointFailsTheStartup(t *testing.T) {
	document, err := json.Marshal(map[string]any{
		"http": map[string]any{
			"listeners": map[string]any{
				endpointName: map[string]any{"address": "127.0.0.1:0"},
			},
			"endpoints": map[string]any{
				endpointName: map[string]any{"listener": endpointName},
			},
		},
		// webui.endpoint is deliberately absent.
	})
	if err != nil {
		t.Fatalf("marshalling the config document: %v", err)
	}
	reader := loadDocument(t, document)

	reg := core.New()
	reg.Register(config.Module())
	reg.Supply(reader, config.ModuleName)
	reg.Register(speedhttp.Module())
	reg.Register(New(bundle()))

	ctx, cancel := context.WithTimeout(t.Context(), waitLimit)
	defer cancel()
	err = reg.Run(ctx)
	if !errors.Is(err, config.ErrMissingRequired) {
		t.Fatalf("the startup returned %v, want it to wrap config.ErrMissingRequired", err)
	}
	if !strings.Contains(err.Error(), endpointPath) {
		t.Fatalf("the startup error %q does not name the missing key %q", err, endpointPath)
	}
}

// TestNewRefusesANilFilesystem holds the constructor premise: a nil bundle is a
// startup failure, reported before anything is read.
func TestNewRefusesANilFilesystem(t *testing.T) {
	m := New(nil)
	_, err := m.Prepare(context.Background(), core.New())
	if !errors.Is(err, ErrNilAssets) {
		t.Fatalf("Prepare over a nil filesystem returned %v, want ErrNilAssets", err)
	}
}

// TestTheDescriptorIsALeafRequiringTheRouter pins the module's declared shape:
// its name, its one non-optional requirement of the entry point's registration
// surface, no delivered capability, and the schema resource carrying the three
// input items.
func TestTheDescriptorIsALeafRequiringTheRouter(t *testing.T) {
	m := New(fstest.MapFS{})
	if m.Name != ModuleName {
		t.Fatalf("the module is named %q, want %q", m.Name, ModuleName)
	}
	if len(m.Requires) != 1 {
		t.Fatalf("the module declares %d requirements, want exactly one", len(m.Requires))
	}
	if got := m.Requires[0].Token; got != any((*speedhttp.Router)(nil)) {
		t.Fatalf("the module requires %v, want a (*speedhttp.Router)(nil) token", got)
	}
	if m.Requires[0].Optional {
		t.Fatalf("the requirement on the entry point is optional, want it required")
	}
	if len(m.Provides) != 0 {
		t.Fatalf("the module delivers %d capabilities, want none: it is a leaf", len(m.Provides))
	}

	var decls []config.Schema
	for _, r := range m.Resources {
		if s, ok := r.(config.Schema); ok {
			decls = append(decls, s)
		}
	}
	if len(decls) != 1 {
		t.Fatalf("the module declares %d schema resources, want one", len(decls))
	}
	decl := decls[0]
	if decl.Namespace != ConfigNamespace {
		t.Fatalf("the schema mounts under %q, want %q", decl.Namespace, ConfigNamespace)
	}
	endpoint, declared := decl.Items[endpointKey]
	if !declared || !endpoint.Required {
		t.Fatalf("the schema does not declare %q required", endpointPath)
	}
	if _, declared := decl.Items[assetPrefixKey]; !declared {
		t.Fatalf("the schema does not declare %q", assetPrefixKey)
	}
	if _, declared := decl.Items[resourcePrefixesKey]; !declared {
		t.Fatalf("the schema does not declare %q", resourcePrefixesKey)
	}
}
