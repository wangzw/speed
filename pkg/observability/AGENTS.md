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
  the attribute surface of the request histogram and the server span's name
  and attributes
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
  the gates that stand in for it are `TestTheInterfacePackageReachesNoBackendLibrary`,
  `TestTheAssemblerStaysInsideItsDependencyBudget` and
  `TestTheAssemblerReachesNoBackendTransport` in `deps_test.go`. The assembler's
  budget is the root package's, the upstream packages it binds itself, and the
  current closure of gin, which the request layer is written against; the closure
  is read from the module graph, never written down. The gates read the
  non-test imports only: the assembler's external tests import `otlp` to hold
  the stop against a real exporter.
- **The two provider interfaces cannot be implemented outside the upstream
  library.** Both embed an internal interface whose method is unexported, so a
  product satisfies them only by embedding them as fields. Writing the two
  methods out compiles and then fails the registry's delivery check at startup,
  which is a slower way to learn it.
- **One module delivers both exporter capabilities, so one product satisfies
  both.** The consequence is that `Shutdown` — declared by both interfaces and
  therefore present once — releases both exporters and must do so once; the
  assembler collects the product under both capabilities and asks it twice, and
  an exporter released twice is not something the upstream promises anything
  about. Because one call releases both halves, no provider may make it: the
  first provider to shut down would release the other's exporter ahead of that
  provider's final export. The forwarding wrappers hold the providers' calls
  back, and the assembler releases every collected exporter after both
  providers have flushed (`releaseExporters`).
- **The release of the exporters runs under a bound of its own, not the
  flush's.** `releaseExporters` derives it from the context the callback
  received, keeping the values and dropping the cancellation, and gives every
  exporter the one fixed `releaseTimeout`. When an export ran to the cutoff the
  flush leaves at most the cutoff's margin of `shutdown-timeout`, and none once
  a provider's shutdown used up the bound, and the upstream exporters answer an
  expired context with its error, which would fail the stop. The bound holds a
  release that waits on its context; one that ignores it is called in line and
  is not bounded.
- **The exports are held to `shutdown-timeout` too, by the forwarding
  wrappers.** The batch processor runs every export, its final one included, on
  a context of its own bounded by `batch.export-timeout`, which the shutdown
  bound does not cancel, and the gRPC exporter retries a refused connection
  until its context ends, its own timeout passes, or its retry budget runs out,
  none of which is shorter than `shutdown-timeout` by default; the HTTP exporter
  waits the same way on a collector that does not answer. `shutdown` therefore
  arms the `cutoff` a tenth of the bound before the bound ends (`exportCutoff`),
  and each wrapper joins every export with it (`cutoff.join`): an export still
  running when the cutoff ends, the one in flight when `Close` began and a
  final one alike, and every export begun after it end there, fail, and are
  reported and absorbed like any other, so each provider returns inside the
  bound. An export that ends before the cutoff is untouched, a final one
  included (`TestAFinalExportThatFitsBeforeTheCutoffIsDelivered`,
  `TestAFinalMetricExportThatFitsBeforeTheCutoffIsDelivered`). An exporter
  that ignores its context is not reached by the cutoff: with a span exporter
  the tracer provider's shutdown returns the deadline at the bound and fails
  `Close` (`TestAShutdownPastItsBoundFailsTheStop`); a metric exporter holds
  the meter provider's shutdown until its final export returns, since the
  reader makes that export in line, unbounded, and a reader after it then
  finds the context expired, makes no final export, and fails `Close` with the
  deadline. With several metric readers the cutoff is what lets `Close` succeed: an export
  waiting for its context would otherwise use up the bound for the readers
  after it (`TestAStopOverTwoMetricBackendsThatWaitIsCutInsideTheBound`).
- **The forwarding wrapper is the one report of a failed export, and it hands
  the upstream no error back.** The upstream's processor and reader pass every
  failed export to the process-level error handler, and the default one prints a
  line per failed batch to standard error, outside the log chain and its
  redaction. Returning the error from the wrapper brings those lines back, and
  makes a failed final export of the reader at shutdown, a cut one included, a
  failure of `Close`.
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
- **The resource is layered upstream default, environment, this module's own
  attributes, and a later layer overrides an earlier one.** The upstream default
  is computed once per process and keeps the environment it first read, so the
  environment layer in `newResource` matters only where the environment changes
  after that, as in a test: a variable set later is seen only through a fresh
  read, and a variable removed later stays in the default. In production the
  environment is constant and the layer coincides with the default.
  `service.name` is this module's attribute only when `service-name` is
  configured; leaving it out when it is not is what lets the upstream settle the
  name from `OTEL_SERVICE_NAME`, the `service.name` in `OTEL_RESOURCE_ATTRIBUTES`
  and its `unknown_service:` fallback, in the order the resource section of
  `docs/design/modules/design-observability.md` gives, and setting it to the
  empty string instead would report an empty one.
  `TestAnUnnamedServiceTakesItsNameFromTheUpstream` and
  `TestALaterLayerOverridesAnEarlierOne` hold the order.
- **The assembler reaches an entry module only when `endpoints` is not empty,
  and a bus only when `events` is not.** An empty list must leave the process
  free of the one it would reach: a background worker carries this module and
  has neither. The `endpoints` default is `*`, every endpoint the entry module
  declares, and for that value alone a missing entry module is skipped, told
  apart from every other failure of the lookup by `errors.Is` against
  `core.ErrMissingProvider`; a list of names fails without one, and so does any
  other lookup failure, such as two router providers that neither declare
  exclusivity (the entry module declares it, so a second provider beside it never
  reaches `wire`: the registry refuses the pair first). Each failure names the
  input item that asked and wraps the sentinel it came with.
- **The probe lives in `New`, never in `Prepare`.** `Prepare` runs for every
  module a command took in (except one the host supplied an instance for),
  including a command that collects stances and constructs nothing; a round trip
  there would make such a command fail over an endpoint it was never going to
  send to.
- **The attribute surface of the request histogram is a contract.** Two
  attributes, both closed sets: the standard methods plus the folding value, and
  the status code. No route — the matched route is readable on the chain, but it
  is whatever hosts register, and a dimension a dashboard has read cannot be taken
  back — and no tenant: that dimension lives on event-derived spans. The
  instrument's name, unit and bucket boundaries are equally fixed; a dashboard
  reads them. The server span carries the same two attributes, and a final
  server-error status marks it errored whether or not the handler panicked.
- **The server span is named for the method and the route template, and carries
  both the route and the endpoint.** When the span ends, a non-empty
  `c.FullPath()` names it for the method and the template — `GET /orders/:id`
  for one — and goes on as `http.route`; an empty
  one — no route matched, a 404 or a 405 alike — leaves the name the method
  alone and adds no `http.route`, because the path such a request asked for is
  whatever the caller wrote. The template's values are bounded by the routes
  registered, not by traffic, and the span is outside the histogram's fixed
  surface, so the route goes on the span and stays off the histogram. The
  endpoint name goes on as `speed.endpoint.name` from the start, a value the
  configuration lists: it is the only place the span records the endpoint. The
  rename does not reach sampling, which reads the parent and the trace
  identifier, not the name.
- **An event-derived span's attributes are the payload's fields as published,
  plus the two the bus carries.** Every top-level field of the payload becomes
  one attribute, in the type and with the digits it was published with — a
  number is not read through a float64 and printed back — so the set is
  whatever publishers put in payloads and the module limits no attribute's
  cardinality. Keeping a credential out of a payload is therefore the
  publisher's obligation: this module neither inspects nor strips one. The
  event's identifier goes on as `messaging.message.id` and its tenant as
  `tenant_id`, both after the payload's fields, so a payload field of the same
  name cannot displace what the bus said.
- **The identifier is what groups the spans of one event.** The bus promises no
  delivery count: a transport-backed bus hands every event to every replica's
  subscribers, a backend may redeliver and a publisher may publish the same
  event again, so one fact can become several spans, each the root of its own
  trace over a transport. Every one carries the same identifier, and a span
  count is not a fact count until spans are grouped by it.
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
  attribute and the span name, with or without a route, both fold anything
  outside the nine standard methods into one value. Narrowing the cardinality
  on one side only moves it to the other: a backend indexes a span name just as
  it indexes a dimension. The folded value is `_OTHER` in the name as well, where
  the upstream convention writes `HTTP` for a method it does not know and
  `QUERY` for the method it lists as in development, so only a standard method's
  span name takes the upstream form.
- **The layer is a `gin.HandlerFunc` and leaves `c.Writer` in place.** It hands
  the span's context down with `c.Request = c.Request.WithContext(...)` and reads
  the outcome from `c.Writer.Status()` and `Written()` after `c.Next()`; it wraps
  nothing. The writer already carries `Flusher`, `Hijacker` and `Unwrap`, so a
  wrapper here would only have to forward them, and one that forgot a method
  would degrade every instrumented endpoint with nothing said. The status the
  writer holds is the one the caller is answered with — gin keeps the code it
  will commit, a second `WriteHeader` before the commit replaces it and one
  after is ignored — with one exception: an informational code other than 101 is
  not a final one. The server sends it ahead of the response and answers with
  the 200 it supplies, while the writer goes on reporting the informational
  code, so `answeredWith` reads it as that 200.
- **A handler that abandons its connection is recorded as a defect.** A handler
  panicking with `net/http`'s `ErrAbortHandler` asks for its connection to be
  dropped; the entry module's fallback lets that value past without answering or
  logging, and this layer sits inside it. The layer cannot tell the two apart, so
  it does what it does for any panic: marks the span errored and records the
  fallback's default status, for a response the caller never received. Reporting
  the status the writer holds instead would file every panicking request under
  success, so the misreport stays — the design document carries it as a
  non-guarantee. The status the writer holds is read only once the response has
  started: before that it is no answer, and the fallback's internal error is.
- **A name written twice in `endpoints` or `events` is registered once.** Both
  loops register unconditionally and neither the entry module nor the bus
  dedupes, so a repeat would double every request in the histogram and open two
  spans for it. The repeat is dropped and a line says so.
- **`TraceIDKey` and `SpanIDKey` are a contract with whoever reads a log line**,
  not an implementation detail. They are bound here and the log module carries
  them without knowing what they mean.
- **Every setting the configuration declares, and every part of the wire
  shape the design documents, is given to the upstream exporter explicitly.**
  The upstream reads the standard `OTEL_EXPORTER_OTLP_*` variables for whatever
  it is not given, and applies them before the options, so an option that
  looks redundant because it passes the upstream default is what keeps a
  variable out: the endpoint's path, the TLS switch and its certificates, the
  headers, the encoding, and the metric temporality and aggregation. The
  certificates are the ones `ca-file`, `client-cert-file` and
  `client-key-file` load to, and with none of them set the host's root
  certificates and no client certificate: the TLS configuration is given in
  full either way (`clientTLS`), so a certificate variable neither replaces a
  configured certificate nor fills in one the configuration left out.
  Removing one of these options reopens a second environment surface outside
  this module's prefix and its sensitive marking.
  `TestNoUpstreamVariableOverridesTheConfiguration`,
  `TestNoUpstreamVariableTurnsTLSOff` and
  `TestNoUpstreamCertificateVariableStandsInForTheConfiguration` hold it.
- **A standard variable set for a setting this capability gives explicitly
  gets one warning line per module, in `New`.** `otel` and `otlp` each keep
  the variables they override in `overriddenVariables` (`variables.go`), with
  the item to set instead; `warnIgnoredVariables` names the ones set, by name
  only — a header variable carries a credential — and writes nothing when none
  is. A value that is blank once trimmed is not set, as the upstream exporters
  read it; what the SDK makes of a blank value differs by variable. The
  variables that still apply are not on the lists: `OTEL_SERVICE_NAME` where
  `service-name` is not configured, `OTEL_RESOURCE_ATTRIBUTES` beneath the
  attributes this module gives, the span limits, the exemplar filter, the
  cardinality limit, and the exporters' timeout and compression. An attribute
  `OTEL_SERVICE_NAME` or `OTEL_RESOURCE_ATTRIBUTES` gives that this module
  also gives — `service.instance.id`, and the service name under a configured
  `service-name` — is replaced, and that is not warned about.
  `TestNoUpstreamVariableOverridesTheProviderConfiguration` and
  `TestNoUpstreamVariableOverridesTheMetricTimeout` hold that the `otel` list
  has no effect; for `otlp`, `TestNoUpstreamVariableOverridesTheConfiguration`,
  `TestNoUpstreamVariableTurnsTLSOff` and the certificate cases in `tls_test.go`
  hold it for a sample of the list, and build giving each setting explicitly
  holds it for the rest. `TestNoUpstreamVariableIsLeftOffTheOverriddenList` in each
  package reads the upstream files its `upstreamSources` names — the ones the
  list rests on, and for `otel` also those of the variables left to the
  upstream — from the module directory `go list -m` reports, and fails when a
  variable named there is neither on the list nor left to the upstream, or a
  listed one is no longer read: an upgrade is where the list goes stale. A
  variable an upgrade starts reading in a file outside `upstreamSources` is
  outside its reach.
- **The environment is a source only under the host's prefix.** Every
  variable name is derived from `config.HostIdentity.Prefix` and the item's
  path (the derivation section of `docs/design/modules/design-config.md` for
  the spelling, its locator section for what an absent prefix leaves), so a
  host that declares no prefix gives these items no environment name at all:
  they come from the primary source alone, an unprefixed
  `OBSERVABILITY__OTEL__SERVICE_NAME` is read by nothing and reported by
  nothing, and the upstream's variables for these settings are kept out
  besides.
- **No input item takes its value from the command line.** The endpoint locator
  and the headers would then be readable from the process list. Items that carry
  a credential are additionally marked sensitive, and an error about one never
  repeats its content: a header element is reported by position, because an
  element with no separator has no key to name and a key is not reliably the
  harmless half either. Whitespace around an element, its key and its value is
  trimmed — the environment's list is split at the commas and nothing more — and
  a key with whitespace left inside is refused by position like any other
  element. The endpoint error does not repeat the value: one written as a URL
  can carry userinfo.
- **`otlp` checks the endpoint's shape in `Prepare`, not its host.** A host and
  an optional port, an IPv6 address in brackets; empty, a scheme, a path or
  whitespace is `ErrInvalidEndpoint`, and nothing else is refused, a port that
  is not a number included. Over gRPC a URL builds an exporter that never
  delivers, which is why the check exists; gRPC's own target forms (`dns:///`,
  `passthrough:///`, `unix://`) would deliver and are refused with it. An empty
  host is the local system, and counts as loopback. The port is optional, and
  the probe dials an endpoint without one at the port the exporters fall back
  to (`dialAddress`). `http/protobuf` is read as `http` in `read`, so `build`
  knows two protocols only.
- **`otlp` loads the certificate files in `Prepare`, and fails where the
  upstream ignores.** `loadTLS` gives `ca-file`, `client-cert-file` and
  `client-key-file` the upstream's semantics for its certificate variables —
  the CA file replaces the host's root certificates rather than adding to
  them, and the client pair loads as `tls.X509KeyPair` takes it — but a file
  that does not load, half a pair, or any of them with `insecure` true is a
  startup failure. The upstream goes on without a file it cannot load after a
  line to its own internal logger, and without half a pair in silence. Reading
  the files in `Prepare` is an accepted cost: every command this backend is
  part of, a shallow one that only prints a version included, fails where a
  configured file is missing or does not parse. The certificate items are
  paths and are not marked sensitive; an error names the item and never
  repeats what a file holds.
- **`otlp` takes the logger as an optional dependency, for two warnings.**
  Headers sent over plaintext to a host that is not a loopback one get a line
  in `New` that names no header, no value and no endpoint
  (`warnPlaintextHeaders`), and the standard variables it overrides get one,
  when any is set, that names them and none of their values
  (`warnIgnoredVariables`). The
  requirement is declared so that a constructed logging module is ordered
  ahead, and optional because its absence is a legal configuration: a command
  that takes this backend in without the logging module still assembles rather
  than failing for a missing provider. Without a logger neither line is
  written, never through the process root logger. The assembler requires the
  logger, so wherever exporters are driven it is present.
- **A new export backend is a subpackage that delivers both exporter
  capabilities, non-exclusively.** The assembler collects exporters through the
  registry and knows no destination, so adding one is adding a module and naming
  it on the command tree, or supplying an instance for it. What the registry
  holds for a backend, built by its `New` or supplied by the host, is released by
  the assembler: a backend has no `Close` callback of its own, and giving it one
  would shut the same exporter down twice, possibly ahead of the final export.
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
