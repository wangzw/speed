# pkg/kv

The key-value module: it delivers opaque bytes stored under an opaque key, with
an expiry and atomic operations, and it serves both the callers who want a cache
and the ones who want a counter, a lock, an idempotency key or a
session-revocation list.

The root package carries the interface and everything the implementations share.
Concrete backends are subpackages — `memory`, `redis`, `postgres`, `memcached`
and `nats` — and each registers one module, binds its own client, and declares
the same capability exclusively.

The API is the package's godoc, the root package's and each subpackage's. Read
`Spec`'s field comments before adding an implementation.

## Boundaries

- **The interface is designed against the weakest backend it must support.** No
  server-side scripting, no pipelines, no ordered structures, no data type beyond
  opaque bytes. Atomicity is the increment pair, the compare-and-swap and the
  set-if-absent. A caller that needs a read-modify-write cycle builds it from
  those. Adding a
  method that only some backend can honour breaks the property that a caller
  writes once and deploys anywhere, and it is the one change to `KV` that a
  review must refuse.
- **The root package registers no module.** Each implementation's subpackage does
  it in its own `init`.
- **The root package imports no backend client.** A client belongs to the
  subpackage that binds it, and the root package reaches a backend only through
  the `Spec.Open` an implementation hands it. Nothing enforces that at compile
  time — an implementation subpackage and the root package are one module, so
  every client is in one dependency list — and the gate that stands in for it is
  `TestRootPackageImportsNoBackendClient` in `deps_test.go`.
- **A new backend is a subpackage with a namespace of its own.** The namespace
  has to be its own: the configuration manifest is collected from every
  registered module, so two implementations declaring items on one path conflict
  as soon as a host imports both — a conflict the collection raises on its own,
  which resolution never gets a chance to clear.
- **Every implementation declares the capability exclusively**, and says in its
  `Stance` how that claim yields and what it cannot start without. A
  zero-configuration backend states `StateAuto` so it yields to one that was
  explicitly enabled; a backend that must be pointed at a setting states
  `StateEnabled` so that two of them being configured at once fails the startup
  rather than one silently winning.
- **A module named in the command tree has to start.** An implementation that
  cannot — a setting it was not given, a deployment precondition its backend
  cannot serve — returns an error naming that input, and the startup fails with
  it. `StateDisabled` is not a stance an implementation may take: it is
  resolution's verdict on a provider that stood down over an exclusive claim, and
  the answer the enablement query gives for a module outside this command's
  participating set.
- **A deployment precondition is declared, never inferred.** An implementation
  whose backend cannot serve the assembly it finds itself in says so in its
  `Stance`, as an error naming the input item and what it read. It does not
  guess, and it does not start and misbehave quietly.
- **The sentinel table is closed at one entry.** `ErrNotNumeric` is the only
  failure this capability defines; a backend's own failures travel as its own
  errors and are wrapped with `%w` so the chain stays walkable.
- **Values are opaque.** No implementation may interpret one, not even where its
  backend could — a store that reads JSON to answer a path query has stopped
  being an implementation of this interface.

## How an implementation contributes

A subpackage binds its client, fixes its namespace, and registers the module this
package builds from its `Spec`. The exclusive delivery, the input-item
declaration, taking up the reading face and releasing what was opened all stay
here, shared with every other implementation.

```go
func init() { core.ProcessRegistry.Register(Module()) }

func Module() core.Module {
	prototype := Config{}
	return kv.NewModule(kv.Spec[Config]{
		ModuleName:      ModuleName,   // "kv.redis"
		ConfigNamespace: ModuleName,   // where this backend's items are mounted
		Config:          &prototype,   // the prototype, carrying the defaults
		Items:           items,        // the declared input items
		Stance:          stance,       // how the exclusive claim yields, or what is missing
		Open:            open,         // the client, and the only place it appears
	})
}
```

It then runs `internal/kvsuite.AssertConforms` in its own tests. That suite is
the capability's semantics written once; a backend that does not pass it is not
an implementation of this interface. Durability, cross-replica sharing and
survival across a restart are deliberately absent from it: those belong to a
backend, and a backend that promises one tests it where its own machinery lives.

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
go -C pkg/kv build ./...
go -C pkg/kv vet ./...
go -C pkg/kv test ./...
go -C pkg/kv test -race ./...
GOWORK=off go -C pkg/kv build ./...   # the standalone leg, without the workspace
```

The in-memory implementation needs nothing beyond the Go toolchain. A backend
whose client is a container adds its own integration cases. A fixture for a
server another module tests against too is shared — `pkg/internal/pgpool/pgtest`
for PostgreSQL, `pkg/internal/natstest` for NATS, `pkg/internal/redis/redistest`
for Redis — so two modules cannot drift onto differently behaved servers. The
Memcached fixture serves this module alone and lives in
`internal/memcachetest`. Every test binary on the machine, from every checkout,
shares its one server, and Memcached has no logical databases: a case works in a
key space of its own (`memcachetest.Space`), and no case empties the server —
a flush takes the entries of whatever case another binary is running at that
moment.
