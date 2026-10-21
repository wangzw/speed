# pkg/http

The HTTP entry point module, a wrapper of gin: the registration surface modules
bind routes and middleware through, the `Spec` and `Operations` resource types
they declare their API as, and the error response every layer and handler writes
through (`Problem`, `ProblemDocument`, `Fail`, the optional
`ErrorResponder`). A middleware layer
declares the stage it belongs to, which is where the chain's cross-category
order comes from, and the capability tokens it stands for, which is what another
layer's constraints point at. The root package registers the module, named
`http`, when it is initialised.

The configuration declares two sets: **listeners**, one socket each, and
**endpoints**, one middleware chain and route table each, hung on a listener by
name. A registrant takes an endpoint up by name and binds its routes and layers
there; several endpoints may share one listener's socket. Two module-level items,
`http.trusted-proxies` and `http.remote-ip-headers`, name the networks in front
of the deployment and the headers a trusted proxy writes the client address
into; an endpoint that omits either inherits the module-level list, and one that
writes it — an explicit `[]` included — overrides it. Both are empty by default,
which trusts no proxy and leaves gin's own header order. Each endpoint claims
the path prefixes it answers under `http.endpoints.<name>.prefixes`; omitting
the key claims `/`, which makes the endpoint its listener's fallback — the one
that answers the paths no other endpoint claims. A listener's dispatch selects
an endpoint by those claims alone, the longest matching one winning, and hands
the request to that endpoint's engine: a request under a claim is answered by
that endpoint even when no route registered the path, so a mistyped API path
gets that endpoint's own 404 rather than the fallback's 200. Registered routes
take no part in selection; at startup each route's literal leading path must
select the endpoint that registered it, or the startup fails with
`ErrUnclaimedRoute`. Two endpoints claiming one prefix on a listener, `/`
included, fail the startup as `ErrClaimConflict`.

**A `Spec` carries the path its fragment is relative to.** `Endpoint` names the
logical endpoint; `Prefix`, `Version` and `Scope` are bare segments the reader
joins into the API root (`api` and `v1` compose `/api/v1`; adding `system`
composes `/api/v1/system`), and the fragment's paths are written relative to it
(`/auth/me`). `Scope` is optional and, when set, is `tenants` or `system`: it is
the management level the fragment is published under. An empty scope is the
ordinary surface — the caller's own resources and the unauthenticated sign-in
routes — and its paths hang directly off the root. In the `tenants` scope every
path names the tenant first (`/{tenant}/members`). A fragment outside the API
root, such as the process probes, carries no `Prefix` either. A module serving a
second API version or a second management level declares a second `Spec`.
`Spec.APIRoot` and `Spec.ResolvePath` are the composition; the shape check
refuses a `Prefix` with a leading or trailing slash, a `Version` that is not one
literal segment, a `Scope` that is neither of the two, a `tenants` path that
does not start with `/{tenant}/`, and a path that already carries the composed
root.

**A module may declare its operations instead of writing a fragment.** An
`Operations` resource carries one `Operation[In, Out]` per route: method, path,
summary, description and the response set, with `In` the handler's input and
`Out` its success body. `Operation.Handle` accepts only
`func(context.Context, In) (Out, error)`, and the returned entry-point handler
binds `In` from the request's `path`/`query`/`header`/`cookie` tags and its
`json`-tagged body fields, then writes the declared response. A handler
therefore cannot read a parameter the declaration does not carry, and a body
member the declaration does not describe is refused rather than dropped. A
`Failure` returned by the handler names a status the operation must declare;
an undeclared one is answered 500. `pkg/internal/openapigen` generates the
fragment from the same descriptor.

**An operation declares the level of caller identity its route admits.**
`Operation.Access` (and a reply operation's) is `AccessRequired` at its zero
value, `AccessOptional` or `AccessPublic`, set through the `Public()` and
`Optional()` constructor options. The fragment publishes it as the operation's
`security` requirement, and the authentication capability's mounter registers
the route at the level the declaration names, so the two cannot disagree. A
route that leaves the field out admits only an identified caller.

**A request body is bound by its declared media type.**
`Operation.Request` (and a reply operation's) is a `*RequestBody`, nil when the
operation takes no body; its media type selects the reader:
`application/json` reads the input's `json`-tagged members,
`application/x-www-form-urlencoded` its `form`-tagged fields,
`multipart/form-data` its `form`- and `file`-tagged fields (a `file` field is
`[]byte`), and `application/octet-stream` its single `raw:"true"` field. A body
whose Content-Type is not the declared one is a 415; a member, form key or part
the declaration does not carry is a 400. A declaration no request could satisfy
— an octet-stream type without exactly one `[]byte` raw field, a raw field under
another media type, a file field that is not `[]byte`, a body tag the declared
media type does not read (a `form` field under JSON, a `json` field under form,
multipart or octet-stream), an unsupported media type — panics when the handler
is built. After binding, an input with a
`Validate() error` method that refuses its value is answered 422, with the
validator's text as the problem detail. The `path`, `query`, `header` and
`cookie` parameter tags are read for every media type. An empty query value
leaves a non-string parameter unset (`?limit=` is zero, not a parse failure),
while a string keeps the empty value. `RequestBody.Description` and
`RequestBody.Required` are published by `pkg/internal/openapigen` with the
request body.

**A handler may build its own typed answer.** `ReplyOperation[In]` declares the
same metadata and response set as `Operation`, and `Handle` takes
`func(context.Context, In, *Meta) (Reply, error)`. `Meta` is the read-only
request snapshot: the client IP, the matched route template and the request
Content-Length (-1 when unknown), taken when the handler is called. A `Reply` is one of `JSONReply`, `EmptyReply`, `BytesReply`,
`StreamReply`, `SSEReply` or `RedirectReply`, optionally wrapped by
`WithHeaders` and `WithCookies`; the set is closed by an unexported marker
method. `NewSSEWriter` builds the event writer over an `io.Writer` and an
`http.Flusher`; `SSEWriter.Send` writes one server-sent event and flushes it,
and the reply path flushes the status and headers once before the event
callback runs. The data is split into one `data:` field per line, with CR, LF
and CRLF all ending a line. A reply operation implements `Declared` like the simple one; its
descriptor's `SuccessBody` is nil, because each declared response's own
`Structure` describes its body. A JSON answer must declare that `Structure`: the
fragment refuses one that does not, because the entry point would accept any
JSON value there. An octet-stream answer is published as a binary string
whatever its `Structure` names, since that is what the entry point writes.

**A reply is checked against its declaration before anything is written.** The
reply's status must be in the operation's `Responses`, and the kind must match
that response: a JSON body's `reflect.Type` must equal the declared `Structure`
(when the declaration names one) and its media type must be the declared one; an
empty reply needs a body-less response; bytes and streams must carry the
declared media type, and a stream or event stream must carry its callback; a
declared `*/*` on a bytes or stream body accepts the media type the handler
chooses; an event stream needs `text/event-stream` and gets `Cache-Control:
no-store` unless the response declares another value; a redirect needs a 3xx
status, a declared `Location` header and a body-less response. Every dynamic header name
must be declared on the matched response; cookies are always written. A mismatch
is answered 500 and the reply's own answer is never written. The declared static
headers are written first, then the dynamic ones, then the body. A streamed body
that fails after the status has gone out cannot change it: the failure is
recorded and the connection is abandoned, which the recovery layer lets through.

**Errors are declared too.** A module declares its shared answers once as a
common failure set and merges them into each operation with
`Failures(common, specific...)`: an operation's response replaces the common one
of the same status and adds its own. A handler refuses with `Fault`, returned or
panicked; the entry point writes it only when the operation declared that
status, and answers 500 otherwise. A fault's `Headers` are written only when the
matched response declares each name; an undeclared one is a 500. The recovery layer honors a fault panic on a
bound operation the same way; a fault panic on a route without a declaration is
a defect. The error body's shape is a declaration as well: `ErrorShape` names
the media type and type the entry point writes, defaulting to the problem
document. A host delivering its own `ErrorResponder` declares that responder's
shape in its error responses, so the contract keeps describing what is written.
A fault's `Type` is what a frontend can render by code: the web package
`@speed/api-client` parses only type values of the shape
`urn:speed:<module>:<code>` into a module code (`<module>.<code>`, dashes
normalized to underscores) and degrades every other type to
`client.http.<status>`.

The declaration types are plain structs, `reflect.Type` values and generic
handler signatures, so this package still imports only the standard library.
`ProblemDocument` is the declared JSON shape of the problem response, the
counterpart of `Problem` (the value a handler fills in).

The API is the package's godoc, plus the runnable examples in `example_test.go`.
Read `Middleware`'s field comments before adding a seat to a layer.

## Boundaries

- **The root package adds no dependency of its own to gin's and config's.**
  Every module that registers a route imports this package, so a dependency
  added here lands in the dependency list of all of them. What already reaches
  them from here is gin's closure and what reading
  configuration costs: the input items are declared to `pkg/config` and the
  endpoints are read through its reader, so config's parsing libraries are in
  the closure too. It also imports `pkg/i18n`'s root package — for the optional
  `Localizer` that renders this module's own refusals and the optional `Resolver`
  that names the language those responses are written in — which depends on the
  standard library only, so that too adds nothing of its own. `deps_test.go`
  holds the budget — the standard library, this repository's modules, config's
  closure and gin's closure, each read as it stands — and the extra closure a
  subpackage contributes when it bridges a library the root package does not
  reach. Every such subpackage is named in the test's `subpackagesInBudget`, so
  one that brings a library is a deliberate edit there rather than something a
  change slips through; the `./...` scan is what forces it to be named. The test
  fails on the first package outside the budget. An OpenAPI parsing library is
  such a package: `Spec` is checked for its shape and nothing else.
- **gin is the surface.** A route's handlers and a layer's `Handler` are
  `gin.HandlerFunc`s, and routing, the middleware chain and rendering are gin's.
  `BindJSON` is the one body-binding entry point this module adds: it binds
  through gin's own binder and turns the binder's failures — and a `dst`'s own
  `Validate` — into the entry point's error shape (413, 400 or 422), so the
  classification is one place rather than one per handler. Beyond it, a helper
  that decodes a body, validates one or renders a response is the shape to
  reject, and so is an exported setting that changes how gin binds. The engine is
  internal: a registrant goes through `Endpoint` and never holds it.
- **gin's process-wide state is the host's, apart from the mode.** This module
  puts gin in release mode, because gin's debug mode prints for every route
  registered and those lines would bypass the logging module. It does not touch
  the decoding options, the validator or gin's writers. These can only be
  observed in a process that has built no engine yet, which is why
  `TestGinsProcessWideSettings` re-executes the test binary (see `TestMain`).
- **gin's recovery middleware is not installed.** It swallows
  `http.ErrAbortHandler` and ends the response as an empty 200. The panic
  fallback is a gin handler on the engine, once per endpoint, inside the observe
  stage and outside every layer that can refuse a request, and it lets
  `ErrAbortHandler` through. A panic raised by an observe-stage layer itself
  runs outside the fallback: it reaches `net/http`, which closes the connection
  and writes a record through the endpoint's configured logger. The assertion
  that matters is on a real connection
  (`TestAnAbortedConnectionIsNotAnsweredAsASuccess`): a recorder has no
  connection to abandon.
- **Every error response goes through one place.** `Fail`, the answer to a
  request that matches no route, the answer to a disallowed method and the
  fallback's answer to a panic all end in `respondProblem`, which hands the
  `Problem` to the delivered `ErrorResponder` or writes the default shape. Code
  in this package that calls `WriteHeader` for an error itself is the shape to
  reject. A response that has begun to go out is left as it is: `Fail` and the
  fallback both read gin's writer (`Context.Writer.Written`), and the fallback
  writes its 500 only when nothing has been committed.
- **This module renders the refusals it produces itself.** The four refusals it
  writes on its own — the 404, 405, 413 and 500 — carry their title as finished
  text, rendered here from this module's own catalog under `http.<status>` keys
  through the optional `Localizer`. The text is finished before the `Problem`
  reaches whichever shape writes it, because the module that decides the shape
  of an error response carries no language. With no localizer assembled, or with
  a key no catalog carries, the title is `net/http`'s standard text for the
  status. With a `Resolver` assembled, the default shape also names the language
  it wrote in a `Content-Language` header; with none it writes no such header.
  The catalog is an `i18n.Catalog` resource declared in `Module`, in the
  same `locales/{tag}.json` shape another module declares, and the engine merges
  it with the rest. A problem this module did not produce — one a layer or a
  handler raises through `Fail` — is left as its author wrote it: this module
  renders its own messages and no others.
- **Paths are in the form gin keeps them.** `Route` refuses, by panicking at
  the call, a path that gin would rewrite (`x`, `/a//b`, `/a/../b`), because a
  layer that compares `Context.FullPath` with the registered path — an
  authentication exemption, an audit match — would otherwise never match.
- **No knowledge of any capability, and no interpretation of a layer.** The
  ordering reads the declared stage and the capability tokens, and nothing else:
  a `switch` on a concrete token, or anything else that reads what a layer
  stands for, is the shape to reject. The stage is a category the layer itself
  named, not a capability this module recognized.
- **The root package registers the module at init time, and registering opens
  nothing.** `ModuleName` is the name a host takes the module into a command
  by; a module only takes part in a run when a command names it.
- **The exported surface is pinned.** `surface_test.go` holds a golden list of
  the root package's exported declarations, and compile-time assertions that the
  surface is expressed in gin's types; adding a declaration is a deliberate
  edit to that list rather than something a change slips through.

## Stages

A layer declares the stage it belongs to, and the chain runs the stages in the
order they are declared — outermost first — so the boundary between two
categories is fixed by this module and does not move when a module is added or
removed. The stage is required: registration refuses the zero value and any
value that is not a declared stage, because a layer that left it out would
otherwise run in a category nobody chose.

The stages, and what each lets the layers inside it rely on:

- `StageObserve` sees every request and refuses none; it may time, trace and
  bind fields into the context, and everything inside it relies on having been
  reached by every request.
- `StageGuard` protects without knowing the caller. By the time it runs a panic
  is already answered and the body is not yet bounded — bounding starts at the
  identify stage. It refuses on what it can see without reading the body: an
  address, a header, a CORS preflight.
- `StageIdentify` settles who the caller is and which tenant the request is for;
  from here inwards the body is bounded. Its refusals are 401, 404 and 503.
- `StageThrottle` applies quotas to an identity or a tenant that is already
  settled; its refusal is 429.
- `StageRecord` records what passed admission and refuses nothing, so a refusal
  by an authorize layer inside it is recorded.
- `StageAuthorize` decides on a settled identity; its refusals are seen by the
  recording layers inside it.

Within a stage, `After` and `Before` are hard constraints and `Order` is an
integer that only picks among the places they leave open: a smaller value comes
out further out, the zero value is no preference, and a value is compared only
among the layers of one stage. Two layers sharing a value carry no order between
them and are separated by registration order, with nothing reported or failing.
`OrderOutermost` and `OrderInnermost` are the extreme values, for a layer that
must not be overtaken by an ordinary one. A cross-stage `After` or `Before`
holds only when it agrees with the stage order; one that points against it fails
the startup (`ErrStageConflict`), so the boundary cannot be moved by an edge.

## Shape of a module

A module takes the `Router` capability up in its `Init` callback, looks an
endpoint up by the name configuration gave it, and binds routes and
middleware layers there. A layer declares the stage it belongs to, and within
that stage states what it stands for with `Provides`; the `After` and `Before`
of the other layers name those statements. The fields belong together: a layer
that declares a position without saying what it stands for gives the next layer
nothing to point at, and a layer that leaves the stage out cannot be registered
at all. A layer that refuses a request sets the response headers the refusal
needs and then calls `Fail`.

```go
package catalog

import (
	"context"
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/vislake/speed/pkg/authn"
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

			// The layer belongs to the identifying stage and stands for this
			// module's capability, so it sits inside the gate's middleware.
			// The anchor is the gate module's own interface type, taken from
			// its interface package: the ordering matches tokens by type
			// identity, so an interface declared here with the same methods
			// would be another type, stand for no layer, and place nothing.
			endpoint.Use(speedhttp.Middleware{
				Name:     "catalog-access",
				Stage:    speedhttp.StageIdentify,
				Provides: []core.Token{(*Catalog)(nil)},
				After:    []core.Token{(*authn.Gate)(nil)},
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
