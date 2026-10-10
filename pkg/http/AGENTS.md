# pkg/http

The HTTP entry point module, a wrapper of gin: the registration surface modules
bind routes and middleware through, the `Spec` resource type they declare
OpenAPI fragments as, and the error response every layer and handler writes
through (`Problem`, `Fail`, the optional `ErrorResponder`). The root package
registers the module, named `http`, when it is initialised.

## Where the authority is

- What this module is and why it is shaped this way:
  [`docs/design/modules/design-http.md`](../../docs/design/modules/design-http.md).
- The decisions behind it, one file per decision:
  [`docs/adr/`](../../docs/adr/) — `adr-http-surface-2026-09-14`,
  `adr-http-middleware-2026-09-14`, `adr-http-routing-2026-09-14`,
  `adr-http-endpoints-2026-09-14`, `adr-http-shutdown-2026-09-14`,
  `adr-http-recovery-2026-09-22`, `adr-http-errors-2026-10-10`.
- The API itself: the godoc in this package, and the runnable examples in
  `example_test.go`. Read `Middleware`'s field comments before adding a seat to
  a layer.

This file states only what neither of those states: the boundaries a change
here must not cross, and how to run it.

## Boundaries

- **The root package adds no dependency of its own to gin's and config's.**
  Every module that registers a route imports this package, so a dependency
  added here lands in the dependency list of all of them. What already reaches
  them from here is gin's closure (ADR-http-surface-2026-09-14) and what reading
  configuration costs: the input items are declared to `pkg/config` and the
  endpoints are read through its reader, so config's parsing libraries are in
  the closure too (ADR-config-parsing-2026-09-20). `deps_test.go` holds the
  budget — the standard library, this repository's modules, config's closure and
  gin's closure, each read as it stands — and fails on the first package outside
  it. An OpenAPI parsing library is such a package: `Spec` is checked for its
  shape and nothing else.
- **gin is the surface, and nothing wraps it.** A route's handlers and a
  layer's `Handler` are `gin.HandlerFunc`s, and routing, the middleware chain,
  body binding and validation, and rendering are gin's. A helper that decodes a
  body, validates one or renders a response is the shape to reject, and so is an
  exported setting that changes how gin binds. The engine is internal: a
  registrant goes through `Endpoint` and never holds it.
- **gin's process-wide state is the host's, apart from the mode.** This module
  puts gin in release mode, because gin's debug mode prints for every route
  registered and those lines would bypass the logging module. It does not touch
  the decoding options, the validator or gin's writers. These can only be
  observed in a process that has built no engine yet, which is why
  `TestGinsProcessWideSettings` re-executes the test binary (see `TestMain`).
- **gin's recovery middleware is not installed.** It swallows
  `http.ErrAbortHandler` and ends the response as an empty 200. The panic
  fallback is a `net/http` layer outside the engine, once per endpoint, and
  lets `ErrAbortHandler` through. The assertion that matters is on a real
  connection (`TestAnAbortedConnectionIsNotAnsweredAsASuccess`): a recorder has
  no connection to abandon.
- **Every error response goes through one place.** `Fail`, the answer to a
  request that matches no route, the answer to a disallowed method and the
  fallback's answer to a panic all end in `respondProblem`, which hands the
  `Problem` to the delivered `ErrorResponder` or writes the default shape. Code
  in this package that calls `WriteHeader` for an error itself is the shape to
  reject. A response that has begun to go out is left as it is: `Fail` checks
  the gin writer, and the fallback's `startedWriter` stands in for that check
  outside the engine.
- **A writer this package wraps keeps its optional interfaces, and claims no
  more than the one underneath has.** `Flusher`, `Hijacker`, `CloseNotifier` and
  `Unwrap` are forwarded by every layer that puts its own writer around the
  connection's, and `Pusher` is handed down only when the writer underneath is
  one: gin gives a handler a nil `Pusher` for a writer that is not, and handlers
  test for it. A handler that asserts one of them on a writer that lost it fails
  quietly: a stream arrives all at once, an upgrade cannot take the connection.
  `engine_test.go` observes each on a real connection.
- **Paths are in the form gin keeps them.** `Route` refuses, by panicking at
  the call, a path that gin would rewrite (`x`, `/a//b`, `/a/../b`), because a
  layer that compares `Context.FullPath` with the registered path — an
  authentication exemption, an audit match — would otherwise never match.
- **No knowledge of any capability, and no interpretation of a layer.** The
  ordering reads capability tokens and nothing else: a `switch` on a concrete
  token, or anything else that reads what a layer stands for, is the shape to
  reject.
- **The root package registers the module at init time, and registering opens
  nothing.** `ModuleName` is the name a host takes the module into a command
  by; a module only takes part in a run when a command names it.
- **The exported surface is pinned.** `surface_test.go` holds a golden list of
  the root package's exported declarations, and compile-time assertions that the
  surface is expressed in gin's types; adding a declaration is a deliberate
  edit to that list rather than something a change slips through.

## Shape of a module

A module takes the `Router` capability up in its `Init` callback, looks a
listening endpoint up by the name configuration gave it, and binds routes and
middleware layers there. A layer states what it stands for with `Provides`, and
the `After` and `Before` of the other layers name those statements: the fields
belong together, and a layer that declares a position without saying what it
stands for gives the next layer nothing to point at. A layer that refuses a
request sets the response headers the refusal needs and then calls `Fail`.

```go
package catalog

import (
	"context"
	"net/http"

	"github.com/gin-gonic/gin"

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

			endpoint.Route(http.MethodGet, "/things", listThings)

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
				Handler:  access,
			})
			return nil
		},
	}
}

func listThings(c *gin.Context) { c.JSON(http.StatusOK, []string{"a", "b"}) }

// access refuses a request that carries no credentials and hands the rest on.
func access(c *gin.Context) {
	if c.GetHeader("Authorization") == "" {
		c.Header("WWW-Authenticate", "Bearer")
		speedhttp.Fail(c, speedhttp.Problem{Status: http.StatusUnauthorized})
		return
	}
	c.Next()
}
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
no environment: the Go toolchain is the whole of what they need installed. Two
of them start a process of their own — `go run` of `testdata/rootimport`, the
program whose only import from this module is the root package, and the test
binary itself for gin's process-wide settings — so they need the `go` command
on the `PATH`.
