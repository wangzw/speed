# pkg/jobs

The job-execution capability for a distributed deployment: a piece of work to do
at a later time, or to do away from the request path, is handed in as a durable
job record and run by whichever replica claims it. One job table carries three
needs at once — run once at a future instant, repeat every interval, and run a
slow piece of work asynchronously.

The root package is the interface package. It carries the capability's seams and
its common types and sentinels, registers no module, and imports nothing of this
repository: a module that only submits a job depends on this package alone,
without carrying storage or the tenant roster.

The seams it defines:

- **`Queue`** is the submission face: `Enqueue` writes one job and returns its
  `JobID`, `Get` reads one back, and `Cancel` asks for one to be cancelled. A
  module that has work to hand to the background depends on this and nothing
  more.
- **`Handlers`** is the registration face: `Register` binds a `Handler` under
  its `Type`, and `Registered` lists the types that are bound. A module that has
  jobs to run registers one in its own `Init`.
- **`Handler`** is what a module registers: `Type` names the kind of job, and
  `Handle` runs one and hands back a `Result` or an error.

`Task` is what a caller submits — a type, a payload and an optional idempotency
key. `Job` is the read-only view a caller reads back. `Schedule` is a periodic
job a business module declares as a resource. The package's godoc is the API;
read `Queue`'s, `Handlers`' and `Task`'s comments before adding a backend or a
consumer.

## The modules

The root package registers no module. The modules are subpackages:

- **`db`** (module `jobs.db`) delivers `Queue` and `Handlers`, both exclusively.
  It holds the job table, the claim loop, the lease, backoff and the terminal
  statuses, and it drives the consumers. It is the only database-backed queue;
  another of this shape would conflict with it in resolution.
- **`scheduler`** (module `jobs.scheduler`) delivers no capability. It collects
  the `Schedule` declarations business modules place in their resources, and on
  a tick it enqueues one job per window. A run that only executes jobs takes
  `db` and not `scheduler`; a run that only produces periodic jobs takes
  `scheduler` and not `db`.

A run that both executes jobs and has periodic work takes both. The two meet
only at the root package's seams: `scheduler` goes through `Queue` and does not
import `db`, and `db` does not import `scheduler`.

## Boundaries

- **The capability carries only the triggers whose effect must converge across
  replicas.** The judge is whether the work has to happen once for the whole
  platform, not whether it is asynchronous. A loop each replica runs for
  itself — refreshing an in-process snapshot, sweeping expired rows, rotating a
  log file, polling an external state — is **not** this capability's: it belongs
  to the module that needs it, as that module's own ticker, with that module's
  own stop mechanism. Shipping such a loop through this capability would make it
  pay a database round trip, a job-row write and a claim for a problem it does
  not have. A trigger that must converge — run once at an instant, repeat on an
  interval, run off the request path — is this capability's.
- **The root package registers no module and imports nothing of this
  repository.** The types it carries reach the standard library alone. Nothing
  holds that at compile time, because a subpackage and the root package are one
  module, and the gate that stands in for it is
  `TestRootPackageDependsOnTheStandardLibraryAlone` in `deps_test.go`.
- **`Queue` and `Handlers` are delivered exclusively, by one module.** A
  process has exactly one queue. A command that named two queue backends fails
  in resolution rather than silently picking one.
- **The tenant comes from the context, not from a `Task` field.** `Task` carries
  no tenant. A job belongs to whichever tenant the request or background path
  that submitted it was in, read through the tenant capability. A `Task` field
  would be a second source that can disagree with the context.
- **The type and the payload are opaque.** `Task.Type` names a kind of job
  chosen by the caller and has to match a registered handler's `Type`;
  `Task.Payload` is stored and never interpreted. This capability does not
  dispatch on a payload or look anything up by it.
- **Execution is at least once.** A job runs at least once; a duplicate arises
  from a claim that expired and was taken over, or from a replica that died
  holding a claim. A duplicate execution is not this capability's to prevent:
  the consumer de-duplicates by `Task.IdempotencyKey`. The capability does not
  replay a result, and it does not deliver a result anywhere — a result is
  stored on the job row and read back through `Get`.
- **Not a workflow engine, and never synchronous.** One job's result does not
  drive the next; there is no fan-in, no fan-out and no dependency graph. A
  caller that must do several things in order does them inside `Handle`, or
  enqueues the next job after it succeeds. `Enqueue` writes one row and returns;
  work that must finish before the call returns is not this capability's shape.
- **`Schedule` is drawn from the participating set.** A declaration describes
  work to do, and its effect depends on the declaring module being driven and
  its handler being registered. A module that was only named statically is not
  built, has no handler, and its declaration does not fire; a module that loses
  resolution drops out. The window idempotency key is derived from the
  declaration's `Key` and the window start and does **not** include the tenant,
  so each tenant in the roster gets its own row per window while repeated ticks
  within a window converge to one job per tenant.
- **`cancelled`, `succeeded` and `dead_letter` are terminal.** A job enters one
  of them and never leaves. Cancelling a job that is already terminal changes
  nothing and returns nil.

## How a module contributes

A module that has jobs to run registers its handler in its own `Init`, which
runs before the queue starts, so the handler set is fixed before any job
executes. It registers through the `Handlers` face, which it takes up like any
other capability:

```go
core.Module{
	Name: "mymodule",
	// ... Provides, Requires, New ...
	Init: func(_ context.Context, reg *core.Registry, _ any) error {
		handlers, err := core.Resolve[jobs.Handlers](reg)
		if err != nil {
			return err
		}
		return handlers.Register(myHandler{})
	},
}
```

A module that has periodic work places a `Schedule` in its own `Resources`,
where the scheduler module collects it from the participating modules:

```go
var reconcile = jobs.Schedule{
	Key:      "mymodule.reconcile",
	Type:     "mymodule.reconcile",
	Interval: time.Hour,
}
```

To submit work, a module takes up the `Queue` face and enqueues. A caller that
needs a job to cause an effect once — a webhook that must not be sent twice, a
row written once — sets an idempotency key.

## Running it

From the repository root, over every module in the workspace:

```
make build
make test
make test-race
make lint
make check
```

`make check` is what CI runs. On this capability alone:

```
go -C pkg build ./jobs/...
go -C pkg vet ./jobs/...
go -C pkg test ./jobs/...
go -C pkg test -race ./jobs/...
```

Run `golangci-lint` from inside `pkg`: handed an absolute path from outside the
module it prints zero issues without loading the packages. The root package's
cases need no database; the queue and scheduler modules' cases run against the
module's own minimal test host and need no example application.
