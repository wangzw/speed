# pkg/event

The event-bus module: it lets one module publish a domain fact under a type name
and another subscribe to that name, without either knowing the other. It is how
cross-cutting capabilities — audit, observability — reach domain facts they
would otherwise have to couple themselves to every producer for.

The root package carries the interface, the event type and the payload helpers.
Concrete backends are subpackages — `memory`, `redis`, `postgres` and `nats` — and
each registers one module, binds its own client, and declares the same capability
exclusively.

## Where the authority is

- What this module is and why it is shaped this way:
  [`docs/design/modules/design-event.md`](../../docs/design/modules/design-event.md).
- The decisions behind it, one file per decision: [`docs/adr/`](../../docs/adr/) —
  why the payload is read structurally and the tenant identifier is carried on
  the event
  ([`adr-event-scope-2026-09-21.md`](../../docs/adr/adr-event-scope-2026-09-21.md)),
  and what a publish does and does not promise
  ([`adr-event-delivery-2026-09-21.md`](../../docs/adr/adr-event-delivery-2026-09-21.md)).
- The API itself: the godoc in this package and in each subpackage. Read `Spec`'s
  field comments before adding an implementation.

This file states only what neither of those states: the boundaries a change here
must not cross, and how to run it.

## Boundaries

- **The delivery promise is a floor, and it does not move down or sideways.**
  `Publish` returning nil means the bus accepted the event; returning non-nil
  means it was not accepted, and the failure was in handing it over. A
  subscriber's failure never reaches the publisher, **not even in an
  implementation that knows the result** — the process-local one does. Folding a
  handler's failure into that return value would make the error mean two things
  a publisher cannot tell apart, and the one action the floor exists to support —
  treating the error as "this did not get out" — would silently become wrong.
  An implementation may promise more than the floor and says so in its own
  documentation; a caller may rely on the floor alone.
- **A subscriber cannot name a publisher's type.** The payload is read through
  the helpers in this package, by field spelling. An implementation that
  type-asserts a payload has stopped being an implementation of this interface,
  and so has one that hands a subscriber something other than what was
  published.
- **The root package registers no module.** Each implementation's subpackage does
  it in its own `init`.
- **The root package imports no backend client.** A client belongs to the
  subpackage that binds it, and the root package reaches a backend only through
  the `Spec.Open` an implementation hands it. Nothing enforces that at compile
  time — an implementation subpackage and the root package are one module — and
  the gate that stands in for it is `TestRootPackageImportsNoBackendClient` in
  `deps_test.go`.
- **The root package does not import the tenant module.** The tenant identifier
  on an event is a string. Importing that module's interface package would put
  its whole dependency closure — the database, the HTTP entry point, their
  drivers — into every host that only wants to publish an event, because Go
  resolves dependencies per module rather than per package. The reason and its
  cost are recorded in `adr-event-scope-2026-09-21`.
- **A new backend is a subpackage with a namespace of its own.** The namespace
  has to be its own: the configuration manifest is collected from every
  registered module, so two implementations declaring items on one path conflict
  as soon as a host imports both.
- **Every implementation declares the capability exclusively**, and says in its
  `Stance` whether it was pointed at anything and whether the deployment suits
  it. A zero-configuration backend states `StateAuto` so it yields to one that
  was explicitly enabled; a backend that must be pointed at something states
  `StateEnabled` so that two of them being configured at once fails the startup.
- **A subscription is made in `Init` and nowhere else.** The drive calls each
  module's `Init` one at a time, so a backend freezes its subscription table at
  the start stage and refuses later registrations at the call site. A
  subscription arriving after the first publish would miss everything published
  before it, and nothing else would say so.
- **`Event.Type` is an agreement with no checker.** A publisher that renames one
  silently stops reaching its subscribers. Nothing here can catch that, and an
  implementation must not try by, say, refusing types nobody publishes — a
  publisher may appear at any time.

## How an implementation contributes

A subpackage binds its client, fixes its namespace, and registers the module this
package builds from its `Spec`.

```go
func init() { core.ProcessRegistry.Register(Module()) }

func Module() core.Module {
	prototype := Config{}
	return event.NewModule(event.Spec[Config]{
		ModuleName:      ModuleName,   // "event.redis"
		ConfigNamespace: ModuleName,   // where this backend's items are mounted
		Config:          &prototype,   // the prototype, carrying the defaults
		Items:           items,        // the declared input items
		Stance:          stance,       // pointed at anything? does the deployment suit it?
		Open:            open,         // the client, and the only place it appears
		Start:           start,        // freeze the subscription table
	})
}
```

It then runs `internal/bussuite.AssertConforms` in its own tests. That suite is
the capability's promises written once, and it deliberately covers only the
floor: delivery across replicas, durability, ordering and exactly-once are
properties of a backend, and a backend that promises one tests it where its own
machinery lives.

## Running it

From the repository root, over every module in the workspace:

```
make build
make test
make test-race
make lint
make check
```

`make check` is what CI runs. On this module alone:

```
go -C pkg/event build ./...
go -C pkg/event vet ./...
go -C pkg/event test ./...
go -C pkg/event test -race ./...
GOWORK=off go -C pkg/event build ./...   # the standalone leg, without the workspace
```

The in-memory implementation needs nothing beyond the Go toolchain. A backend
whose transport is a broker holds its own integration cases and its own fixture.
