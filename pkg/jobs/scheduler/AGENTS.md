# pkg/jobs/scheduler

The periodic-trigger module, registered as `jobs.scheduler`: it reads the
periodic declarations business modules place in their own resources and, on
every window start, enqueues one job per window per tenant, so that one run of
the platform enqueues exactly one job per window however many replicas are
running. It delivers no capability — it appears in no `core.Resolve[T]` call
site — and is taken up only by being named in a command.

The API is the package's godoc.

## What it does and does not do

- **It only ticks and enqueues.** Claiming, executing, the lease, backoff and
  dead-lettering belong to `jobs.db`. After an enqueue this module never touches
  that job again.
- **It selects no leader and takes no distributed lock.** Every replica runs its
  own ticker, and the window idempotency key converges their enqueues.
- **It knows no job type.** A `Schedule.Type` is a string another module
  defines; this module hands it to `Queue.Enqueue` unchanged. It does carry the
  handler set for one purpose: at `Start` it checks every declaration's type has
  a registered handler.
- **It does not backfill.** It keeps no record of which window it has scheduled,
  so a window a process crossed while stopped is not enqueued when it comes back.
  The window the process starts in is the one its startup covers beyond the
  backscan, and nothing earlier is.
- **It does not carry per-replica loops.** A loop each replica runs for itself —
  refreshing an in-process snapshot, sweeping expired rows, rotating a file,
  polling an external state — belongs to the module that needs it, as its own
  ticker. This module carries only the triggers whose effect must converge across
  replicas.

## The window

A declaration of interval `I` has windows of length `I`. A window start is the
largest multiple of `I`, measured from the Unix epoch, that is not after the
instant in question; windows are aligned to absolute multiples so every replica
derives the same start from the same instant.

One tick enqueues for every window whose start falls in `(now - tick, now]`. The
backscan reaches back exactly one `tick`, so the set is derived from the current
instant, the tick and the interval alone, with no record of what was already
scheduled. A tick denser than the interval derives the same window start again,
and the window key converges the repeats; a tick sparser than the interval
crosses several windows and enqueues each.

The tick `Start` runs covers the window the process starts in as well, and it is
the only tick that does: that window's start can lie up to one interval before
now, while the backscan reaches back one tick. Every later tick covers
`(now - tick, now]` alone, and no window earlier than the one startup covers is
enqueued. The extra coverage carries the same window key, so a window another
replica has already enqueued converges to that job rather than being run twice.

The window key is `hex(sha256(schedule_key + window_start))`. It does **not**
carry the tenant, so every tenant's job in one window shares one key while each
becomes its own row under the queue's `(tenant_id, idempotency_key)`
uniqueness.

## The declaration set and its domain

The declarations are `jobs.Schedule` resources business modules place in their
own `Resources`. They are collected once, in `New`, filtered and frozen; nothing
re-reads them for the life of the process.

The domain is the **participating set**. A declaration describes work to do, and
its effect depends on the declaring module being driven and its handler being
registered, so a module that was only named statically is not built, has no
handler, and its declaration must not fire. `core.Resources` walks every
registered module and filters nothing — `core` interprets no resource — so the
filter is this module's own: a participant is kept, a participant resolution
stood down over an exclusive claim is dropped, and a module that was never taken
in is dropped. A module the host put every declaration in force for
(`AllResources`) is collected as though it were running.

## Expanding per tenant

Every declaration is expanded per the tenants the roster holds, not per a list in
the configuration. One tick reads the roster once with `tenant.Catalog.List`;
for each tenant it enters with `tenant.Catalog.Enter` and enqueues one job. A
tenant the roster refuses as disabled is skipped for that window; the other
tenants carry on. A roster read that fails leaves the round unexpanded and is
retried on the next tick.

## Boundaries

- **It has no table and no migration.** The window key lands on the job row's
  `idempotency_key`, and "one enqueue per window" is the queue's uniqueness.
- **It reaches the queue only through `jobs.Queue` and never imports `jobs.db`.**
  The queue and the registration face are required and hard; the tenant roster
  is required too. The logging capability is optional.
- **A bad declaration fails the startup, not a window.** An empty `Key`, an empty
  `Type` or a non-positive `Interval` fails `New`; two declarations sharing a
  `Key` fail `New` as well, because within one window and one tenant they would
  derive the same key and one job would swallow the other. A declaration whose
  `Type` has no registered handler fails `Start`.
- **A failed enqueue does not stop the round.** It is logged and skipped, and the
  next tick derives the same window start and the same key, so that window is
  retried for as long as the ticker runs. A tenant that cannot be entered is
  skipped the same way.
- **At most one tick runs at a time.** The ticker is a single goroutine, so a
  tick never overlaps the next; the schedule set and the dependencies are
  read-only once `New` has built the product.
- **The ticker carries the values of its stage's context but not its
  cancellation.** It runs until this module's own `Stop`, which cancels it and
  waits for the goroutine to return. A tick at or below zero starts no ticker at
  all — the periodic declarations then have no effect, and the process reports no
  failure.

## Configuration

This module's input items are mounted under its own namespace, `jobs.scheduler`
(the module name). It accepts one:

- **`tick`** (a duration, default one minute): how often the trigger runs. A
  value at or below zero turns the trigger off; no ticker runs and no window is
  ever enqueued.

```yaml
jobs:
  scheduler:
    tick: 30s
```

## Running it

From the repository root, over every module in the workspace:

```
make build
make test
make test-race
make lint
make check
```

This module on its own:

```
go -C pkg build ./jobs/scheduler
go -C pkg test ./jobs/scheduler
go -C pkg test -race ./jobs/scheduler
env -C pkg golangci-lint run ./jobs/scheduler/...
```

Run `golangci-lint` from inside `pkg`: handed an absolute path from outside the
module it prints zero issues without loading the packages.

The cases build the scheduler over fakes for the queue, the registration face
and the roster, with a pinned clock, so a window is derived without waiting for a
ticker. The descriptor cases drive the module through `core.Drive` with a
minimal set of stub providers. No case needs a database or an example
application.
