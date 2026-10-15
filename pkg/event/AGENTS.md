# pkg/event

The event-bus module: it lets one module publish a domain fact under a type name
and another subscribe to that name, without either knowing the other. It is how
cross-cutting capabilities such as audit reach domain facts they would
otherwise have to couple themselves to every producer for.

The root package carries the interface, the event type and the payload helpers.
Concrete backends are subpackages — `memory`, `redis`, `postgres` and `nats` — and
each registers one module, binds its own client, and declares the same capability
exclusively.

`outbox` is a second capability inside this directory: the transactional outbox,
which stages an event in the transaction of the write it announces and relays it
to the bus once that transaction commits. Its interface package is `outbox`, its
implementation `outbox/relay`, and its boundaries are in
[`outbox/AGENTS.md`](outbox/AGENTS.md).

The API is the package's godoc, the root package's and each subpackage's. Read
`Spec`'s field comments before adding an implementation.

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
  documentation; a caller may rely on the floor alone. The floor has one window
  no implementation reached over a connection can close: a request the server
  took, whose reply was lost on the way back. The call returns an error with the
  event stored, and no client can tell that from a request that never arrived.
- **A subscriber cannot name a publisher's type.** The payload is read through
  the helpers in this package, by field spelling. An implementation that
  type-asserts a payload has stopped being an implementation of this interface,
  and so has one that hands a subscriber something other than what was
  published.
- **A publisher keeps credentials out of a payload, and the bus does not
  help.** A subscriber reads a payload by field name and may write what it read
  straight into its own records — audit writes the top-level scalar fields
  into an entry's detail, masking only a closed set of key names — while the
  bus neither inspects nor strips a payload. A credential in a top-level
  scalar field under any other key reaches those records.
- **A delivery's span belongs to the bus, and the event's tracing context is its
  parent.** The dispatch layer starts one span per subscriber per delivery and
  runs the handler inside it. The parent comes from the context the event
  carries, not from the context the implementation hands the handler, so the
  trace that produced the fact continues into every subscriber under a
  process-local and a transport-backed bus alike. An event carrying no tracing
  context — published outside any span, or from a process with no telemetry —
  leaves the delivery's span a root. The context a handler receives still
  belongs to the implementation, and no other parentage follows from it.
- **An implementation reports through the logger `Spec.Open` hands it, and
  through nothing else.** That logger is the assembly's, named after the module,
  when the assembly delivers logging; the log module never replaces the process
  default logger, so a record written through `slog.Default()` bypasses the
  configured destinations, format and redaction — and a subscriber's error text
  can quote payload values.
- **The root package registers no module.** Each implementation's subpackage does
  it in its own `init`.
- **The root package imports no backend client.** A client belongs to the
  subpackage that binds it, and the root package reaches a backend only through
  the `Spec.Open` an implementation hands it. Nothing enforces that at compile
  time — an implementation subpackage and the root package are one module — and
  the gate that stands in for it is `TestRootPackageImportsNoBackendClient` in
  `deps_test.go`.
- **The optional tracer dependency is declared by the implementation subpackage,
  not by the root package.** `NewModule` hands back a descriptor whose `Requires`
  holds only the logging capability: the root package cannot name
  `(*observability.TracerProvider)(nil)` without importing the telemetry
  interface package, which the gate above forbids. A subpackage appends that
  requirement to the descriptor it gets back. The edge is what orders the
  assembly ahead of the bus; without it the two are ordered by module name alone,
  `Trace` finds no provider and falls back to one that exports nothing, and the
  bus runs on exporting no span, with nothing to say so.
- **A publish stays apart from any transaction.** Tying an event to a write is
  the outbox's, whose interface package takes the GORM session. The root package
  gains no method or helper that takes one: every publisher imports it, and GORM
  would enter the closure of every publisher that never stages an event.
- **The root package does not import the tenant module.** The tenant identifier
  on an event is a string. Every publisher imports this package, so naming the
  tenant module's type here would make it import that module's interface
  package, and that edge would sit in every publisher's closure. The cost is
  that dependency — the bus reaching a concrete capability's interface — rather
  than a third-party stack, since the tenant interface package imports the
  standard library alone.
- **A new backend is a subpackage with a namespace of its own.** The namespace
  has to be its own: the configuration manifest is collected from every
  registered module, so two implementations declaring items on one path conflict
  as soon as a host imports both.
- **Every implementation declares the capability exclusively**, and judges in its
  `Stance` whether the host pointed it at anything and whether the deployment
  suits it. A run-level inclusion is a promise that the module starts, so a
  premise it cannot meet — no address to reach a backend, a replica count the
  backend cannot serve — is an error naming the input, never a stance that lets
  the run continue without an event bus. `StateDisabled` is not an implementation's
  to state; resolution is its only source. What is left is how the exclusive claim
  gives way: a zero-configuration backend states `StateAuto` so it stands down for
  one that was explicitly enabled; a backend that must be pointed at something
  states `StateEnabled` so that two of them being pointed at once fails the startup.
- **A subscription is registered before the subscription table freezes, and a
  backend freezes the table in its own `Start` callback.** The drive calls every
  module's `Init` before it reaches that stage, so a table frozen there holds
  each module's subscription made in its own `Init`. A registration arriving
  after the freeze does not cover anything published before it: an
  implementation may refuse it at the call site; one that accepts it leaves the
  subscriber missing everything published first.
- **A backend's `Start` callback takes up its own product and refuses any other
  bus, through `dispatch.ForeignBus`.** A bus the host supplied for the module
  has no table of the backend's to freeze and no readers to start, so a drive
  that carries it to that stage fails, and one that stops short of it does not.
  The refusal is each backend's own code, and it comes before the callback
  touches anything else, so `TestASuppliedBusIsRefusedAtTheStartStage` holds
  every backend to it without a server; a new backend is added to that test's
  list.
- **A publish is accepted between the start stage and the stop stage, and
  nowhere else.** Before `Start` the table is still being filled, so a publish
  would reach part of it; from `Stop` on no reader is left to deliver it. Both
  are refused (`ErrNotStarted`, `ErrClosed`) rather than accepted and dropped.
- **A transport's delivery ends at its own stop stage, not with the drive's
  context.** The contexts a reader reads and handles under are derived from the
  start stage's without its cancellation; the stop stage ends reading, lets the
  subscribers already running finish on a live context within `dispatch.Grace`,
  and delivers and acknowledges what a reader had already taken. The stage
  bookkeeping lives in `internal/dispatch` and an implementation takes it from
  there rather than writing its own.
- **`Event.ID` reaches every subscriber unchanged, and it is the only key a
  consumer de-duplicates by.** Every `Publish` stamps an empty one through
  `dispatch.Stamp` before the event leaves the call, and every transport
  carries the value beside the payload, so each subscriber on each replica
  receives the same one. An implementation never mints a second identifier
  for an event it redelivers, and never substitutes its backend's own entry
  identity: a consumer's stored keys would stop matching the day the backend
  changed.
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
		Stance:          stance,       // can it start? how does its claim give way?
		Trace:           trace,        // the optional tracer, adapted to TracerProvider
		Open:            open,         // the client, and the only place it appears;
		                               // takes the logger and the adapted tracer
		Start:           start,        // freeze the subscription table
	})
}
```

It then runs `internal/bussuite.AssertConforms` in its own tests, and a backend
that carries events between replicas runs `internal/bussuite.AssertReplicated`
as well. That suite is the capability's promises written once, and it
deliberately covers only the floor and what every replica is owed whatever the
delivery shape — the one identifier, and a reading position fixed before the
start stage returns: broadcast or load balancing, durability, ordering and
exactly-once are properties of a backend, and a backend that promises one
tests it where its own machinery lives.

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
whose transport is a broker holds its own integration cases, and reaches the
broker through the fixture it shares with the key-value module's backend of the
same kind (`pkg/internal/natstest`).
