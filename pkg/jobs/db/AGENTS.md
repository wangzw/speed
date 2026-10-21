# pkg/jobs/db

The persistent, cross-replica job queue. It delivers the two seams `pkg/jobs`
declares — `jobs.Queue` for submitting, reading back and cancelling a job, and
`jobs.Handlers` for registering the code that runs one — over a single table,
and it drives the executions. The submitting side calls `Enqueue` in a tenant's
context and a row lands in the table; the executing side is a set of coroutines
that read the due rows, claim a batch of them, enter each row's tenant, hand the
job to the handler registered for its type, and settle the row by the result.

The module registers the name `jobs.db`, and its configuration namespace is the
same string. Both capabilities are delivered **exclusively**: a process holds
exactly one queue, and two providers of either seam fail the startup rather than
one silently winning.

The root package `pkg/jobs` is the interface package; it registers no module and
imports nothing of this repository. `pkg/jobs/scheduler` (a separate module)
enqueues periodic jobs through `Queue`; it does not import this package, and
this package does not import it.

## The two seams

```go
type Queue interface {
    Enqueue(ctx context.Context, t Task, opts ...EnqueueOption) (JobID, error)
    Get(ctx context.Context, id JobID) (*Job, error)
    Cancel(ctx context.Context, id JobID) error
}

type Handlers interface {
    Register(h Handler) error
    Registered() []string
}
```

Take them up with `core.Resolve[jobs.Queue](reg)` and
`core.Resolve[jobs.Handlers](reg)`; declare them as
`core.Requirement{Token: (*jobs.Queue)(nil)}` and the same for
`(*jobs.Handlers)(nil)`.

### Queue

- **The tenant comes from the context, not from a `Task` field.** `Task` carries
  no tenant. A job belongs to whichever tenant the request or background path
  that submitted it was in, read through `tenant.FromContext`; the row's tenant
  column is filled from the context when it is written. A context with no tenant
  is refused with `tenant.ErrNoTenantInContext`, and nothing is written.
- **`Enqueue` validates the task before it writes.** A blank `Type` is
  `jobs.ErrInvalidTask` and a `Type` with no registered handler is
  `jobs.ErrNoHandler`; neither writes a row. The job's identifier is a fresh
  UUID and its status starts `pending`, due at the scheduled instant — which is
  now when the caller named none.
- **`IdempotencyKey`, when non-empty, makes `Enqueue` idempotent.**
  `(tenant_id, idempotency_key)` is unique: a second submission with the same
  key from the same tenant returns the existing job's `JobID`, writing no second
  row and reporting no error. The same key under another tenant is a different
  job.
- **`Get` reads one job visible to the tenant of its context.** An unknown
  identifier, another tenant's identifier and an identifier that is no longer
  visible are all answered `jobs.ErrJobNotFound` alike, so existence does not
  leak.
- **`Cancel` works by the row's current status.** An unclaimed job (`pending` or
  `retrying`, with no claimant) moves straight to `cancelled` in one conditional
  statement. A claimed job only has a cancellation requested, which the claimant
  observes before it executes the job and then settles it as `cancelled`. A job
  already in a terminal status is an idempotent no-op that returns nil.
- **Every method returns `jobs.ErrNotRunning` before the queue's start stage and
  after its stop stage.**

### Handlers

- **Registration happens in `Init`**, before the queue starts, so the handler
  set is fixed before any job executes. Registering a type twice, registering a
  handler whose `Type` is empty, registering nil, and registering after the
  queue has started are all refused, and each leaves the set unchanged —
  `ErrDuplicateHandler`, `ErrInvalidHandler` (twice) and `ErrHandlerSetFrozen`
  respectively.
- **`Registered` lists the registered types**, sorted, as a copy.

## The lifecycle

| Stage | What this module does |
|---|---|
| `Prepare` | Reads its configuration; checks the values are ones the loop can run on. A bad value fails the startup with `ErrInvalidConfig`. |
| `New` | Takes up the database, the tenant roster, and the optional bus, logger and settings store; builds the statements for the running dialect. A dialect with no statements written fails the startup here. Reads no table: the migrations have not run yet. |
| `Migrate` | The database module applies this module's migration set, which creates the `jobs` table. |
| `Init` | Nothing here; this is where a consumer module registers its handler through the `Handlers` face. |
| `Start` | The executors begin, reading the table at once, which is how a start takes over the due jobs a previous run left behind. |
| `Stop` | Claims nothing more; an execution under way is given the shutdown grace to finish, then cancelled; every row an executor still holds is given back. |
| `Close` | Runs the stop stage if it has not run; the database handle belongs to the database module and is not closed here. |

An instance the host supplies for this module is left alone: the start, stop and
close callbacks act on this module's own product and on nothing else.

## The job table

One row per job, in the table `jobs`. The row embeds `tenant.Model`, so the
tenant column is `tenant_id` and leads the key, as it does on every scoped
table. The columns are `id`, `tenant_id`, `type`, `status`, `priority`,
`payload`, `result`, `error`, `attempts`, `max_retries`, `timeout_ms`,
`progress_pct`, `progress_msg`, `idempotency_key`, `scheduled_at`,
`available_at`, `started_at`, `completed_at`, `claimed_by`, `claim_expires_at`,
`cancel_requested`, `terminal_published_at`, `created_at` and `updated_at`.
There is an index on `available_at` and a unique index on
`(tenant_id, idempotency_key)`.

Every instant is stored as milliseconds since the Unix epoch rather than as a
timestamp type. The claim reads, writes and compares them in native SQL, and an
integer is what lets one statement judge "due now" and "claimed until" against
the database's own clock on either engine.

The table belongs to this module. An operator reads it to see a backlog, retries
and dead letters; it is not a contract face, and a host does not write it.

## How a job runs

1. **Read.** The executor asks how long until the earliest non-terminal row is
   due, and sleeps that long — never longer than `poll-interval`, so a job
   enqueued with an earlier time is noticed.
2. **Claim.** One statement takes up to `claim-batch` due rows, ordered by
   `priority` descending, `available_at` ascending, and then across tenants by a
   rotation key: the claim starts after the tenant the executor last served, so
   one tenant's due rows do not lead every batch. It writes the executor's token
   into `claimed_by`, the claim's expiry into `claim_expires_at`, `running` into
   `status` and the database's clock into `started_at`.
3. **Execute.** For each claimed row: read whether a cancellation was requested
   and whether the executor still holds it; enter the row's tenant through
   `tenant.Catalog.Enter`; hand the job to its handler within the job's own
   `timeout_ms`.
4. **Settle.** Write the result back conditionally on the executor still holding
   the row: `succeeded` with the handler's bytes, `retrying` after a backoff, or
   `dead_letter` once the attempts pass the job's retry limit.

A claim is one statement and the execution runs outside any transaction, holding
no connection. Two claims running at once are decided by row locks: PostgreSQL
locks the rows it takes and skips the rows another claim is locking, and SQLite
runs one writer at a time so a claim is atomic on its own. Every instant is read
from the database's clock, so the executors of several replicas judge due-ness
and expiry against one clock.

Backoff starts at `retry-base` and doubles with each failure, capped at
`retry-max`. A job's `attempts` counts executions; when the count passes
`max_retries` the next failure sends it to `dead_letter`, so a job with the
default of 3 runs at most four times.

### Cancellation

Cancelling an unclaimed job moves it to `cancelled` directly. Cancelling a
claimed job only records the request; the executor reads it between claiming and
executing and settles the job `cancelled` without calling the handler. A
cancellation that arrives while a handler is running is not observed — the
running handler judges its own context — and a retry after that execution is
cancelled at the next claim.

### Stopping

The stop stage claims nothing more. An execution under way is given
`shutdown-grace` to finish and is then cancelled. Every row an executor still
holds but has not settled is given back: `claimed_by` and `claim_expires_at` are
cleared, `available_at` is set to now, and the status returns to the one it is
claimed from again — `pending` for a job that never ran, `retrying` for one that
has failed. Another executor, or the next start, may claim them at once without
waiting out the lease.

## Terminal events

A job reaching `succeeded`, `dead_letter` or `cancelled` publishes one event:
`jobs.job.succeeded`, `jobs.job.dead_letter` or `jobs.job.cancelled`. The
event's identifier is the job's, so a subscriber that de-duplicates by event
identifier treats a repeated publish of one job's terminal event as one event.

The **publish obligation lives on the row**: `terminal_published_at` empty means
the event still owes publishing. The settle into a terminal status writes the
status only; once the bus accepts the event, the publish time is recorded. A
round additionally takes up every terminal row whose publish time is still empty
and publishes it, so a lost publish — a bus that was down, a replica that died
between publishing and recording — is taken up again until it lands.

`(*event.Bus)(nil)` is an **optional** dependency. With no bus in the assembly
no event is published at all, and the rows keep their empty publish time rather
than being marked; the jobs run and settle exactly as they otherwise would.

## Configuration

Namespace `jobs.db`. `workers` and `shutdown-grace` are configuration items,
taken from the primary configuration source alone: an environment variable or a
command-line argument under those paths is not read, and the loader reports it
as unused. They size the worker pool at construction and the stop stage, so a
change needs a restart.

| Item | Kind | Default | Meaning |
|---|---|---|---|
| `workers` | integer | `4` | How many jobs this process executes at once. |
| `shutdown-grace` | duration | `10s` | How long the stop stage waits for an execution under way before cancelling it. |

The poll interval, the claim batch, the lease and the retry bounds are settings,
declared here and read while the process runs — per loop iteration, per claim
and per failure settle:

| Setting | Kind | Default | Meaning |
|---|---|---|---|
| `poll-interval` | duration | `1s` | How often an executor reads the table when it found no due job, and the longest it sleeps before reading again. |
| `claim-batch` | integer | `16` | How many due jobs one claim takes. |
| `lease` | duration | `2m` | How long one claim holds the jobs it took, and so how long a job held by a replica that died waits before another replica takes it over. |
| `retry-base` | duration | `1s` | The delay after a job's first failure, doubled with each further failure. |
| `retry-max` | duration | `1m` | The cap on that doubling. |

All five must be positive. `lease`, `retry-base` and `retry-max` are stored and
read in whole milliseconds, so each must also be at least one millisecond; a
shorter value would truncate to zero and expire a claim at once or retry a job
immediately. With no settings store the declared defaults apply;
a read that fails or lands outside the domain applies the default and writes one
`Error` naming the key. A `retry-max` below `retry-base` is not a startup
invariant: both are accepted and the retry is capped at `retry-max` when it is
settled. The config schema no longer carries the five: a config file naming any
of them fails startup as an unknown key.

Set `lease` to at least the longest `Timeout` you enqueue with: a job whose
execution outlasts its claim may be taken over and run a second time.

## What this module does not do

- **It does not guarantee exactly-once.** A job runs at least once; a duplicate
  comes from a claim that expired and was taken over, or from a replica that
  died holding a claim. De-duplicating a duplicate effect is the consumer's, by
  the job's `IdempotencyKey`. A result is stored on the row and read back
  through `Get`; it is not replayed and not delivered anywhere.
- **It does not schedule.** A periodic job is a `jobs.Schedule` a business
  module declares in its resources; `pkg/jobs/scheduler` reconciles those
  through the table's idempotency key. This module only runs what is submitted.
- **It does not order jobs.** Priority, availability, backoff and the tenant
  rotation all change the order, and across replicas there is no order at all.
- **It does not interpret a job.** `Task.Type` and `Task.Payload` are opaque; a
  handler defines what a type means.
- **It holds no leader and no distributed lock.** Several replicas share one
  table by taking time-limited claims of it.
- **It mounts no HTTP surface and declares no permission points.** Submitting
  and cancelling are in-process calls to the seams.

## Extending it

- **A new dialect** needs its clock expression and its claim lock clause in
  `statementsFor`, plus a subdirectory of the same name under `migrations/`.
  A dialect with no statements written is refused in `New` rather than starting
  an executor that cannot read the table.
- **Another queue backend** implements `jobs.Queue` and `jobs.Handlers` and
  delivers both exclusively; it conflicts with this module in resolution, and
  the host picks one.

## Running it

From the repository root:

```
make build
make test
make test-race
make lint
```

On this package alone, from inside `pkg`:

```
go -C pkg build ./jobs/...
go -C pkg vet ./jobs/...
go -C pkg test ./jobs/...
go -C pkg test -race ./jobs/...
```

Run `golangci-lint` from inside `pkg`: handed an absolute path from outside the
module it prints zero issues without loading the packages.

The cases run against this module's own minimal test host — a real registry
driven to its start stage over a SQLite database, and over PostgreSQL when one
is reachable — and need no example application.
