# pkg/observability

The runtime telemetry module: traces and metrics produced while the process
serves. Logs are not here — they are their own module and their own signal.

The root package carries the interface and the two helpers. `otel` registers the
assembler: it builds the two providers, collects the exporters the registry
holds, and instruments the request path and the event bus. `otlp` and `stdout`
are export backends, each registering one module that delivers both exporter
capabilities.

## Where the authority is

- What this module is and why it is shaped this way:
  [`docs/design/modules/design-observability.md`](../../docs/design/modules/design-observability.md).
- The decisions behind it, one file per decision: [`docs/adr/`](../../docs/adr/) —
  what it owns and what it does not
  ([`adr-observability-scope-2026-09-22.md`](../../docs/adr/adr-observability-scope-2026-09-22.md)),
  the library it wraps and the types it re-exports
  ([`adr-observability-library-2026-09-22.md`](../../docs/adr/adr-observability-library-2026-09-22.md)),
  the attribute surface of the request histogram
  ([`adr-observability-instruments-2026-09-22.md`](../../docs/adr/adr-observability-instruments-2026-09-22.md)),
  and what happens when the endpoint is unreachable
  ([`adr-observability-failure-2026-09-22.md`](../../docs/adr/adr-observability-failure-2026-09-22.md)).
- The vocabulary this module shares with the rest of the tree: `遥测` in
  [`docs/glossary.md`](../../docs/glossary.md), which is also where the
  difference from the assembly-time diagnostics architecture calls `可观测性` is
  stated.

This file states only what neither of those states: the boundaries a change here
must not cross, and how to run it.

## Boundaries

- **The root package registers no module, and imports no backend transport.**
  Naming the upstream API and SDK exporter types costs their closure and nothing
  besides; a transport belongs to the export backend that binds it. Nothing
  enforces that at compile time — the whole capability is one Go module, so its
  dependency list carries every backend whichever one a host deploys on — and
  the gate that stands in for it is `TestTheInterfacePackageReachesNoBackendLibrary`
  and `TestTheAssemblerReachesNoBackendTransport` in `deps_test.go`.
- **The two provider interfaces cannot be implemented outside the upstream
  library.** Both embed an internal interface whose method is unexported, so a
  product satisfies them only by embedding them as fields. Writing the two
  methods out compiles and then fails the registry's delivery check at startup,
  which is a slower way to learn it.
- **One module delivers both exporter capabilities, so one product satisfies
  both.** The consequence is that `Shutdown` — declared by both interfaces and
  therefore present once — releases both exporters and must do so once; two
  providers call it, and an exporter released twice is not something the
  upstream promises anything about.
- **The providers are never installed into the library's global state.** The
  global is a process-level singleton that the second assembly in one process
  overwrites, and architecture invariant 9 rules out handing instances around
  through globals besides. The cost is that a third-party library reaching the
  global for its own instrumentation gets an empty provider and exports nothing,
  with no layer reporting it.
- **A provider that fails to construct is a startup failure, not a fallback to
  the process root logger.** A line written through the root logger goes to
  standard output instead of the destinations configuration named, and nothing
  would report the difference.
- **The assembler reaches an entry module and a bus only when configuration
  names one.** An empty `endpoints` or `events` list must leave the process free
  of both: a background worker carries this module and has neither.
- **The probe lives in `New`, never in `Prepare`.** `Prepare` runs for every
  module a command took in, including one that collects stances and constructs
  nothing; a round trip there would make such a command fail over an endpoint it
  was never going to send to.
- **The attribute surface of the request histogram is a contract.** Two
  attributes, both closed sets: the standard methods plus the folding value, and
  the status code. No route — this layer sits outside the routing engine and the
  request has not been dispatched — and no tenant: that dimension lives on
  event-derived spans. The instrument's name and unit are equally fixed; a
  dashboard reads them.
- **An event-derived span's attributes are the payload's fields as published.**
  Every top-level field of the payload becomes one attribute, so the set is
  whatever publishers put in payloads and the module limits no attribute's
  cardinality. Keeping a credential out of a payload is therefore the
  publisher's obligation: this module neither inspects nor strips one.
- **An event-derived span's parent and kind come from the bus, not from this
  module.** The span starts on the context the handler was handed: the
  process-local bus hands the publisher's own context, so the span joins the
  trace that produced the fact, while a transport-backed bus hands one of its
  own, leaving the span a root. The kind is `consumer`, the value upstream
  defines for receiving a message from a broker; a locally-subscribed fact
  crosses no such boundary, so the kind upstream defines for an in-application
  operation — `internal` — is the closer fit. It stays `consumer` because one
  handler serves both backend shapes, and a transport-backed bus does receive
  from a broker.
- **The method dimension is folded in both signals.** The histogram's method
  attribute and the span name both fold anything outside the nine standard
  methods into one value. Narrowing the cardinality on one side only moves it to
  the other: a backend indexes a span name just as it indexes a dimension.
- **The recorder is the only place the response is observed, so it owes two
  things.** It keeps the *first* status the handler wrote, because the server
  ignores a second `WriteHeader` and answers with the first — reporting the last
  would report a code no caller saw. And it forwards flushing and hijacking,
  because a handler type-asserts the writer it was given, which is this type,
  and never reaches the writer underneath.
- **A handler that abandons its connection is recorded as a defect.** A handler
  panicking with `net/http`'s `ErrAbortHandler` asks for its connection to be
  dropped; the entry module's fallback lets that value past without answering or
  logging, and this layer sits inside it. The layer cannot tell the two apart, so
  it does what it does for any panic: marks the span errored and records the
  fallback's default status, for a response the caller never received. Reporting
  the recorder's untouched default instead would file every panicking request
  under success, so the misreport stays — the design document carries it as a
  non-guarantee.
- **A name written twice in `endpoints` or `events` is registered once.** Both
  loops register unconditionally and neither the entry module nor the bus
  dedupes, so a repeat would double every request in the histogram and open two
  spans for it. The repeat is dropped and a line says so.
- **`TraceIDKey` and `SpanIDKey` are a contract with whoever reads a log line**,
  not an implementation detail. They are bound here and the log module carries
  them without knowing what they mean.
- **No input item takes its value from the command line.** The endpoint locator
  and the headers would then be readable from the process list. Items that carry
  a credential are additionally marked sensitive, and an error about one never
  repeats its content: a header element is reported by position, because an
  element with no separator has no key to name and a key is not reliably the
  harmless half either.
- **A new export backend is a subpackage that delivers both exporter
  capabilities, non-exclusively.** The assembler collects exporters through the
  registry and knows no destination, so adding one is adding a module and naming
  it on the command tree. It also releases what it built through the providers:
  a backend has no `Close` callback of its own, and giving it one would shut the
  same exporter down twice.
- **A backend returns `observability.Exporters` from its `New`.** That type is
  where the two exporters become one product, and it lives in the interface
  package rather than in each backend because two copies is how the two would
  drift. It is not under `pkg/internal/` because it is not what those units
  hold: they carry third-party clients and fixtures, and this carries neither.

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
go -C pkg/observability build ./...
go -C pkg/observability vet ./...
go -C pkg/observability test ./...
go -C pkg/observability test -race ./...
GOWORK=off go -C pkg/observability build ./...   # the standalone leg, without the workspace
```

The standard-output backend needs nothing beyond the Go toolchain. The OTLP
backend reaches a collector over the network; its cases construct exporters and
probe, and do not require a collector to be running.
