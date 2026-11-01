# pkg/ratelimit

The rate limiting module. It registers two middleware layers that do different
jobs and are configured apart.

The **quota** is the per-identity layer: one on each endpoint its list names,
counting requests per identity string inside a tenant and answering 429 with a
`Retry-After` header once the estimate for the current window exceeds the limit.

The **address shield** is the second layer, in the guard stage, before any
identity is settled. It counts requests per client address in this process's
memory and spends by outcome, charging a refusal far more than a success. It is
a coarse defence against a source that has not identified itself, not a quota;
the two layers share the module and the chain listing and nothing else, and each
names its own endpoints and reads its own settings.

The root package is the implementation — there is no separate interface
package, because the module has one implementation and one path. It registers
the module `ratelimit`, delivers `(*Limiter)(nil)` exclusively, and declares two
configuration items and five settings items, all under the `ratelimit`
namespace.

The API is the package's godoc.

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
  window alone: the single fixed window this module rejected. There is a test
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
  evaluates it and it does not trip, once it has gone `recoveryQuiet` without
  failing — not when a decision succeeds outright, and not on the first
  passing decision. A decision returns at the first class that trips, so the
  classes behind it are never evaluated and their flags are left alone; a
  class ahead of the trip still clears, which is what keeps a fixed condition
  from waiting on an unfixed one. The quiet period is what keeps a failure
  confined to some decisions — one tenant's row, one key, a backend answering
  some calls — from writing a pair of lines per alternation
  (`TestAFailureConfinedToOneTenantWritesOneLine`).
- **The lines go to the process's standard error, not through the logging
  module.** The reporting chain may be absent or switched off, which is the same
  reason the logging module reports its own write failures that way.
- **Every failing path lets the request through.** A refusal would turn one bad
  backend or one bad row into a refusal of the whole endpoint. The limits here
  are an abuse surface, not a data boundary.
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
  is a value this layer acts on. `windowInDomain` and `limitInDomain` are the
  one statement of each domain, and both judgements go through them: the
  settings declaration carries them as the items' domains, so the settings
  store refuses a write outside either before the row is written, and
  `thresholds` applies them to what a decision reads, since a read hands out
  whatever a row holds — one stored before the domain was declared, or written
  around the store. A second statement of either domain, in the declaration or
  in the decision, is a second place the two can drift apart
  (`TestAThresholdOutsideItsDomainIsRefusedWhereItIsWritten` drives one table
  of values through both).
- **The layer's `Provides` carries `(*Limiter)(nil)`**, the same token the
  descriptor delivers under. That is the only anchor another layer has for
  placing itself inside or outside rate limiting, so the two must not drift.
- **The layer declares `StageThrottle`, and carries no `After`.** Where it sits
  relative to the layers that settle an identity and the layer that records the
  request is the stage order's business, not an anchor's: `StageIdentify` is
  outside this stage and `StageRecord` inside it, so an identity exists by the
  time the layer reads one and a refusal here leaves no audit row.
  `After` is for a constraint between two layers of one stage, and this layer has
  none.
- **The address shield is a second layer, declaring `StageGuard`.** It runs
  before any identity is settled, because before that point the only key
  available is the client's address. It carries no `Provides`: nothing places
  itself relative to the shield, and the quota layer's `(*Limiter)(nil)` must not
  be read as standing for it.
- **Neither layer states an `Order`.** The quota layer has no preference within
  its stage; the shield keeps the zero value so that a layer in its own stage
  asking for the outside — the cross-origin layer, which carries
  `OrderOutermost` — stays outside it, which is what lets a 429 carry that
  layer's headers.
- **A refusal sets `Retry-After` first and then goes through `speedhttp.Fail`,
  with the status and this module's `throttled` type in the `Problem`.** The
  shape of the body is the entry point's — `application/problem+json` unless a
  module delivers an `ErrorResponder` — and the layer writes no status of its
  own. Both layers write `urn:speed:ratelimit:throttled`, so a client branches
  on the problem type rather than on the status; the rest of the body stays the
  entry point's choice. The order is load-bearing: `Fail` writes the response,
  and a header set after it never reaches the client.
- **The layer does not read the route.** It runs for every request on the
  endpoint, including one that matches no route and one whose method the path
  does not allow, and counts them like any other; a branch on `c.FullPath()`
  would let the requests the entry point answers itself escape the limit.
- **Reading the identity is the one step a test here cannot drive.** Go keeps
  the package that writes an identity to the authentication capability
  directory, so `decide` takes the identity as an argument and `handle` is the
  only place that calls `authn.FromContext`. Everything below that call is
  exercisable in this package; keep it that way when changing the signature.
  `host_test.go` reaches the layer in the two ways that leaves: through the
  module's descriptor with the product supplied, where the real `handle` runs on
  requests that carry no identity, and through the layer registered with the
  metadata `middleware` declares in front of a handler that fixes the identity.
- **Time is read through `l.now`, never `time.Now` directly.** The weight a
  decision applies depends on where in the window the request lands, so a test
  letting the clock run would pass or fail with the time of day. The shield
  reads it through the same field, for the refill its buckets take.

## The address shield

- **The shield is the shield; the identity layer is the quota.** They are not the
  two halves of one thing. The quota is fairness on a known identity — a
  business limit, applied after identity is settled, and it can be generous. The
  shield is a coarse defence against a source that has not identified itself —
  applied before any identity exists, and aimed only at the traffic an attacker
  produces. A chain listing that shows `ratelimit.address` before `ratelimit` is
  showing exactly that: the shield outermost, the quota inside it. Any change
  that blurs the two — one settings set, one endpoint list, one shared counter —
  takes the shield's job away from it.
- **The key is the client address, and an IPv6 address counts by its /64.** The
  address is whatever the entry point reports through `c.ClientIP()`; the shield
  reads no forwarded header itself. With no trusted proxies configured that is
  the peer address, and a forwarded header is ignored, so a client cannot choose
  its own bucket by writing one. A /64 is counted as one key because a host is
  routinely handed a whole /64, and counting whole addresses would let one host
  walk through its own prefix and never fill a bucket. The tests pin both
  (`TestAddressKeyCountsAnIPv6AddressByItsSlashSixtyFour`).
- **The counting is in this process's memory and makes no network call of any
  kind.** This is the one thing that must not change: a shield that read a shared
  counter over the network on every request would itself be the amplification
  point it exists to close. The state is the `addressGuard` on the product, not
  the key-value primitives the quota counts through.
- **The cost is stated and not hidden.** Each replica counts alone, so the
  effective limit is the per-replica limit times the number of replicas, and a
  flood spread over many addresses is not stopped by this layer at all — a
  per-address counter cannot see a distributed one. The first is the price of
  keeping the count in-process; the second is why this layer sits behind an edge
  (a CDN or a WAF) and not instead of one: the application layer does not solve
  a distributed flood.
- **A request is charged by outcome, and the charge is what empties the
  bucket.** The gate at the front spends one token and refuses with a 429 when
  there is none; after `c.Next` a 404, or a 401 that refused a credential, spends
  the penalty on top, and a success spends nothing more. A 401 that asks an
  authenticated caller to step up is not a credential refusal: `http.Fail` marks
  that answer by its problem type wherever it is written, the shield reads the
  mark with `http.IsStepUp`, and
  that answer pays the one token only, because it is an exchange the client must
  complete rather than the signature of an attack. Only those statuses are
  charged: they are what credential stuffing and tenant enumeration produce, and
  charging the others would spend a client's budget on the server's own faults.
  The penalty is applied with `spend`, which reads `TokensAt` first and charges
  only what the bucket holds, because `rate.Limiter` cannot overdraw a bucket — a
  penalty charged as one `AllowN` on a nearly empty bucket would consume nothing
  and let the source straight back in. Both halves of that are held by a test
  (`TestSpendTakesWhatTheBucketHoldsAndNoMore`,
  `TestTheShieldStopsARefusedSourceAndKeepsAddressesApart`).
- **The shield's endpoints are their own item, and the layer is off unless one
  names it.** `address-endpoints` is separate from `endpoints` so the two layers
  are switched apart; an endpoint named in both carries both. The shield's
  numbers are `address-rate`, `address-burst` and `address-penalty`, read from
  the global settings layer and never a tenant's, because the shield runs before
  any tenant is settled. Each is judged against the same domain on the way in
  and on the way out as the quota's are, and the shield stands aside on a value
  outside it rather than act on one.
- **Every failing path lets the request through, the shield's included.** A
  failed settings read, or a row outside its domain, stands the shield aside and
  reports on standard error like the quota's failure classes; it never refuses
  the endpoint on one bad row. The two shield classes are kept apart from the
  quota's for the same reason the quota's are kept apart from one another.
- **The bucket map is bounded, and that is not an optimisation.** A flood of
  distinct addresses — which this layer does not stop — would otherwise grow the
  map without limit. Entries idle past `addressIdle` are dropped on a sweep, and
  a hard cap (`addressCap`) drops entries whatever their age past it. Dropping a
  bucket only resets one address's budget, and an address silent long enough to
  be swept has refilled to `burst` many times over, so nothing the shield relied
  on is lost.

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

The tests ask for no service and no environment. They bring their own counter
store, and a settings store of their own: a stand-in for the decision's cases,
and for the domain's the settings module itself, driven over a SQLite database
of the case's own that `pkg/internal/dbtest` delivers. The cases that read what
a request is answered with run the entry point module itself, listening on a
loopback port the system picks, and read the response off the socket
(`host_test.go`); none of them stands a router of its own in for it.
