# pkg/http

The HTTP entry point module: the registration surface modules bind routes and
middleware through, the `Spec` resource type they declare OpenAPI fragments as,
and the request-handling helpers their handlers call. `pkg/http/stdmux` is the
implementation subpackage that binds the standard library's routing engine.

## Where the authority is

- What this module is and why it is shaped this way:
  [`docs/design/modules/design-http.md`](../../docs/design/modules/design-http.md).
- The decisions behind it, one file per decision:
  [`docs/adr/`](../../docs/adr/) — `adr-http-surface-2026-09-14`,
  `adr-http-middleware-2026-09-14`, `adr-http-routing-2026-09-14`,
  `adr-http-endpoints-2026-09-14`, `adr-http-shutdown-2026-09-14`,
  `adr-http-recovery-2026-09-22`.
- The API itself: the godoc in this package, and the runnable examples in
  `example_test.go`. Read `Middleware`'s field comments before adding a seat to
  a layer.

This file states only what neither of those states: the boundaries a change
here must not cross, and how to run it.

## Boundaries

- **The root package adds no third-party dependency of its own, and its
  surface carries no third-party type.** Every module that registers a route
  imports this package, so a dependency added here lands in the dependency list
  of all of them. What already reaches them from here is what reading
  configuration costs: the input items are declared to `pkg/config` and the
  endpoints are read through its reader, so config's parsing libraries are in
  the closure (ADR-config-parsing-2026-09-20). A routing library is bound by an
  implementation subpackage and its types stop there; `deps_test.go` holds the
  budget — the standard library, this repository's modules and config's own
  closure — and fails on the first package outside it.
- **The engine seam is `Engine`, and it is not for registrants.** A change that
  puts the engine's type, or any other plumbing type, on the surface is the
  shape to reject: a registrant goes through `Endpoint`, and the request
  helpers use `net/http`'s types. The seam asks an implementation subpackage
  for one thing besides the interface: a refused mount has to be attributed to
  the mounted pattern it clashes with. The subpackage's engine answers
  `AttributeRefusal`, and an engine that does not leaves the startup failure
  saying the refusal was not attributed — see `Engine`'s godoc.
- **No knowledge of any capability, and no interpretation of a layer.** The
  ordering reads capability tokens and nothing else: a `switch` on a concrete
  token, or anything else that reads what a layer stands for, is the shape to
  reject.
- **The root package registers nothing at init time.** Registration lives in
  the implementation subpackage — `pkg/http/stdmux` registers the module named
  `http.stdmux` — so importing this package alone opens no port.
- **The exported surface is pinned.** `surface_test.go` holds a golden list of
  the root package's exported declarations; adding one is a deliberate edit to
  that list rather than something a change slips through.

## Shape of a module

A module takes the `Router` capability up in its `Init` callback, looks a
listening endpoint up by the name configuration gave it, and binds routes and
middleware layers there. A layer states what it stands for with `Provides`, and
the `After` and `Before` of the other layers name those statements: the fields
belong together, and a layer that declares a position without saying what it
stands for gives the next layer nothing to point at.

```go
package catalog

import (
	"context"
	"net/http"

	"github.com/vislake/speed/pkg/audit"
	"github.com/vislake/speed/pkg/core"
	speedhttp "github.com/vislake/speed/pkg/http"
)

// Catalog is what this module delivers, and what another module's layer can
// place itself relative to.
type Catalog interface{ catalog() }

// Module wires this package's route and the layer guarding it.
func Module() core.Module {
	return core.Module{
		Name:     "catalog",
		Provides: []core.Provision{{Token: (*Catalog)(nil)}},
		Init: func(_ context.Context, reg *core.Registry, _ any) error {
			router, err := core.Resolve[speedhttp.Router](reg)
			if err != nil {
				return err
			}
			endpoint, err := router.Endpoint("public")
			if err != nil {
				return err
			}

			endpoint.Route("GET /things", http.HandlerFunc(listThings))

			// The layer stands for this module's capability and belongs
			// inside the middleware of the auditing module. The anchor is
			// that module's own interface type, taken from its interface
			// package: the ordering matches tokens by type identity, so an
			// interface declared here with the same methods would be
			// another type, stand for no layer, and place nothing.
			endpoint.Use(speedhttp.Middleware{
				Name:     "catalog-access",
				Provides: []core.Token{(*Catalog)(nil)},
				After:    []core.Token{(*audit.Audit)(nil)},
				Wrap:     access,
			})
			return nil
		},
	}
}

func listThings(w http.ResponseWriter, _ *http.Request) {
	_ = speedhttp.WriteJSON(w, speedhttp.StatusFor(nil), []string{"a", "b"})
}

func access(next http.Handler) http.Handler { return next }
```

`net/http` keeps its own name outside this package and the parent package takes
an alias, which is the direction the package documentation gives an external
file. A module written inside this package uses the other direction.

## Running it

From the repository root, over every module in the workspace:

```
make build
make test
make test-race
make lint
make check
```

`make check` is what CI runs. This capability directory alone, inside the `pkg`
module:

```
go -C pkg build ./http/...
go -C pkg vet ./http/...
go -C pkg test ./http/...
go -C pkg test -race ./http/...
```

The tests bring up their own assemblies and ask for no service, no database and
no environment: the Go toolchain is the whole of what they need installed.
`pkg/http/stdmux` sits under this directory, so these commands cover both
packages.
