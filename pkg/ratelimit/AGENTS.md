# pkg/ratelimit

The rate limiting module: one middleware layer per listening endpoint, counting
requests per identity string inside a tenant, and answering 429 with a
`Retry-After` header once the estimate for the current window exceeds the
limit.

The root package is the implementation — there is no separate interface
package, because the module has one implementation and one path. It registers
the module `ratelimit`, delivers `(*Limiter)(nil)` exclusively, and declares
one configuration item and two settings items, all under the `ratelimit`
namespace.

## Where the authority is

- What this module is and why it is shaped this way:
  [`docs/design/modules/design-ratelimit.md`](../../docs/design/modules/design-ratelimit.md).
- The decisions behind it, one file per decision: [`docs/adr/`](../../docs/adr/) —
  what it limits and what it does not
  ([`adr-ratelimit-scope-2026-09-22.md`](../../docs/adr/adr-ratelimit-scope-2026-09-22.md)),
  why the count is two weighted windows
  ([`adr-ratelimit-algorithm-2026-09-22.md`](../../docs/adr/adr-ratelimit-algorithm-2026-09-22.md)),
  why an unreachable backend lets requests through
  ([`adr-ratelimit-failure-2026-09-22.md`](../../docs/adr/adr-ratelimit-failure-2026-09-22.md)).
- The API itself: the godoc in this package.

This file states only what neither of those states: the boundaries a change
here must not cross, and how to run it.

## Boundaries

- **The key's segments are escaped, and that is not decoration.** A tenant
  identifier's domain excludes only whitespace and control characters, so a
  colon is legal in one; an identity string is two halves joined by a separator
  with nothing keeping a colon out of either. Without `escape`, tenant `a:b`
  with identity `c` and tenant `a` with identity `b:c` build one key and share
  one count across two tenants, with nothing reporting it. The percent sign is
  escaped first, so the escape of a colon is not escaped twice.
- **The expiry covers two windows.** The key written in the previous window has
  to stay readable throughout the current one, and its expiry was fixed when it
  was created — a live key never has its expiry extended. At one window's
  expiry, an identity string that made its first request at the start of a
  window has its previous key lapse the moment the next window opens, the
  weighted term collapses to nothing, and the decision is made on the current
  window alone: the single fixed window the design rejected. There is a test
  pinning this (`TestCountAsksForTwoWindowsOfExpiry`).
- **The increment happens before the decision.** A refused request spends. A
  layer that counted only what it let through would let a caller that is already
  over its limit keep retrying without consuming anything, and would open the
  second half of the window up as the weight falls.
- **The count is kept per window index, not per request time.** A sliding window
  log would need an ordered structure, and the primitives are designed against
  the weakest backend and offer none. Two weighted fixed windows are the closest
  shape they can express; that is a decision, not a shortcoming to fix here.
- **The failure classes are reported apart, each with its own flag.** They do
  not imply one another: while the counter backend is unreachable, the
  thresholds class still has to be able to report itself, and one shared flag
  would keep it from ever writing a line. A class clears when a decision
  evaluates it and it does not trip — not when a decision succeeds outright. A
  decision returns at the first class that trips, so the classes behind it are
  never evaluated and their flags are left alone; a class ahead of the trip
  still clears, which is what keeps a fixed condition from waiting on an
  unfixed one.
- **The lines go to the process's standard error, not through the logging
  module.** The reporting chain may be absent or switched off, which is the same
  reason the logging module reports its own write failures that way.
- **Every failing path lets the request through.** A refusal would turn one bad
  backend or one bad row into a refusal of the whole endpoint. The limits here
  are an abuse surface, not a data boundary
  ([`adr-ratelimit-boundary-2026-09-22.md`](../../docs/adr/adr-ratelimit-boundary-2026-09-22.md)).
- **A settings read names the item's declared path, not its item key.** The
  settings module registers every item under its full path — the namespace, a
  dot, then the item key — and looks a key up by that exact path. A read of the
  bare item key covers no declaration, so it fails, and every decision then
  falls on the read class and lets the request through: the layer refuses
  nothing in any assembly, and no chain listing shows it, because the layer's
  position is the same either way. `windowPath` and `limitPath` derive the
  paths from the namespace and the item keys, and have to stay in step with
  what `settingsSchema` registers. There is a test holding the read paths
  against the declaration (`TestThresholdsReadsTheDeclaredPathOfEachItem`).
- **Both settings domains exclude zero, and the window has an upper bound.** A
  limit of zero would refuse every request carrying an identity string, since
  the increment comes first and the current count is at least one; a window of
  zero leaves the window index undefined; a window too long to double leaves
  the counting key written with a non-positive expiry, which the key-value
  contract reads as "no expiry", so the key never disappears. None of the three
  is a value this layer acts on, and the settings module does not judge values
  on this module's behalf.
- **The layer's `Provides` carries `(*Limiter)(nil)`**, the same token the
  descriptor delivers under. That is the only anchor another layer has for
  placing itself inside or outside rate limiting, so the two must not drift.
- **The layer's `After` carries three anchors, and each one is load-bearing.**
  Without `(*authn.Gate)(nil)` the layer may sit outside authentication and
  every request is let through for want of an identity; without
  `(*tenant.Resolution)(nil)` it reads the global row for every request;
  without `(*audit.Audit)(nil)` its place relative to the audit request layer is
  left to `Order`, which carries no constraint. Audit's own token is the way
  that module settled for modules ordering against it
  ([`adr-audit-anchor-2026-09-21.md`](../../docs/adr/adr-audit-anchor-2026-09-21.md)).
- **`Order` stays at zero.** Every position this layer needs is fixed by the
  `After` entries, so a numeric preference would only decide where it lands
  when those entries do not land — a case worth not tuning for.
- **Reading the identity is the one step a test here cannot drive.** Go keeps
  the package that writes an identity to the authentication capability
  directory, so `decide` takes the identity as an argument and `wrap` is the
  only place that calls `authn.FromContext`. Everything below that call is
  exercisable in this package; keep it that way when changing the signature.
- **Time is read through `l.now`, never `time.Now` directly.** The weight a
  decision applies depends on where in the window the request lands, so a test
  letting the clock run would pass or fail with the time of day.

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
go -C pkg build ./ratelimit/...
go -C pkg vet ./ratelimit/...
go -C pkg test ./ratelimit/...
go -C pkg test -race ./ratelimit/...
```

The tests ask for no service and no environment: they bring their own counter
store and settings store.
