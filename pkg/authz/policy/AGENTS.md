# pkg/authz/policy

The authorization policy module `authz.policy`. It owns the policy tables and
their migrations, the thin store the engine reads and writes through, the
Casbin engine snapshot the decisions read, the permission catalog, the
management surface over the policy data, and the account lifecycle cleanup. It
delivers `authz.Enforcer`, `authz.Catalog` and `authz.Manager`, each
exclusively, and implements `authn.AccountLifecycle`.

The Casbin engine and the database it reads live here and nowhere else, so a
host that only guards routes carries no policy store. The thin adapter and the
snapshot holder are the package's own units under `pkg/authz/internal/`, which
only this directory can import.

## Where the authority is

- What this module is and why it is shaped this way:
  [`docs/design/modules/design-authz.md`](../../../docs/design/modules/design-authz.md) —
  the tables and migrations under "表与迁移", the adapter under "薄 adapter",
  the snapshot under "快照", the write and reload order under "快照与失效", the
  platform-domain check under "平台域与跨租户回退", the management surface under
  "核心数据结构" and "并发与事务边界", the input items under "配置与设置", and
  the module row under "登记单元与名字" and "依赖".
- The decisions behind it, one file per decision, in
  [`docs/adr/`](../../../docs/adr/):
  [`adr-authz-store-2026-10-12`](../../../docs/adr/adr-authz-store-2026-10-12.md)
  (the thin adapter and the tenant-keyed tables),
  [`adr-authz-cache-2026-10-12`](../../../docs/adr/adr-authz-cache-2026-10-12.md)
  (the immutable snapshot, the write order, the refresh fallback),
  [`adr-authz-engine-2026-10-12`](../../../docs/adr/adr-authz-engine-2026-10-12.md)
  (the engine and the fixed model text),
  [`adr-authz-model-2026-10-12`](../../../docs/adr/adr-authz-model-2026-10-12.md)
  (RBAC, the tenant as the policy domain).
- The API itself: the godoc in this package and in `pkg/authz`. Read the
  `Manager` and `Enforcer` comments before writing a consumer.

This file states only what neither of those states: the boundaries a change
here must not cross, and how to run it.

## Boundaries

- **The migration files are the schema.** They are declared as one
  `db.Migrations` resource and applied by the database module; the module
  declares no `Migrate` callback of its own. Every table leads its key with
  `tenant_id`, a real non-empty column, so the tenant filter and the scoped
  models recognise the tenant rather than reading it out of a policy string.
  The deciding process' correctness turns on this: with a key that named a
  non-tenant column first, one tenant's write would collide with another's row
  and rewrite its tenant, and the statement names no other tenant to refuse.
  `migrate_test.go` runs the shipped files on SQLite and PostgreSQL and asserts
  the keying and the non-empty column; a table shape written out in a test
  rather than read from the file proves nothing.
- **The model text is fixed in the root package, and its self-check runs before
  the process serves.** `ModelText` is the one model the engine is loaded with;
  a host writes policy data and never the model. A wrong matcher does not fail
  to load — it silently allows or denies where the design says the opposite —
  so `Start` runs the root-package fixtures on the exact text the process will
  enforce with, and a failure stops the startup. The reload in that path is
  bounded by the configured `authz.policy.load-timeout`; a load that does not
  finish fails the startup rather than installing a half-read snapshot.
- **The snapshot is immutable and replaced as a whole.** `Enforcer.Allowed`
  reads the atomic pointer and runs the matcher without a lock; the reload
  swaps the pointer and never mutates a snapshot in place, so a reader takes
  the old snapshot or the new one and never a mixture. The write order is fixed:
  the row is committed, then this replica swaps its pointer, then it publishes
  `authz.policy.changed`. The local swap does not wait on the event, and a
  failed publish is logged rather than returned — the row is already in the
  table and the other replicas reload at their next tick or a later change.
- **The table is written one row at a time, never rewritten wholesale.** A
  `Manager` write commits in its own transaction, and a write that checks a role
  first — a grant, a binding, a role rewrite — does that check and the row it
  guards in the same transaction, so a concurrent writer cannot land between
  them and leave a grant or a binding on a role that was just removed or turned
  builtin. The thin adapter's `SavePolicy` is left unimplemented because a
  clear-and-rewrite would drop a write that landed between its read and its
  write. A host that wants a different store shape is the shape to reject.
- **The store is read and written through the adapter's construction, not per
  call.** The `persist.Adapter` methods carry neither a context nor a tenant, so
  the adapter binds both once in `New`: the context a load runs under, marked
  cross-tenant with `tenant.CrossTenant` so that a load reads every tenant's
  rows, and the caller a write is checked against. A write whose rule names
  another tenant is refused with `authz.ErrTenantMismatch`; the cross-tenant
  mark says the call intends to reach every tenant, not that any row may be
  written into any tenant. `LoadPolicy` is this module's one cross-tenant read
  of a security-sensitive table and each replica holds every tenant's policy;
  isolation rests on judging only within the request's domain, not on the
  process holding one tenant's data.
- **`Manager` refuses the writes the design names.** A grant or a binding on a
  role that was not created in the tenant is `authz.ErrUnknownRole`; deleting a
  role, rewriting it, or changing a builtin role's grants is
  `authz.ErrBuiltinRole`; a write that names no tenant is
  `authz.ErrTenantMismatch`, and a binding that names no account is refused too.
  A builtin role may still be bound — that is how the platform operations'
  roles are handed to an account.
- **Deleting a role revokes it.** `DeleteRole` removes the role's row together
  with its rows in `authz_grants` and `authz_bindings`, in one transaction. The
  engine decides from those two tables and never reads `authz_roles`, so the
  three rows are one unit: a leftover grant or binding would keep deciding for a
  role that no longer exists. Another tenant's same-named role, and its rows,
  are untouched.
- **The platform-domain check runs only in an assembly that has a roster.**
  `Start` reads the roster for a tenant whose identifier is the reserved
  `PlatformDomain` and fails the startup with `authz.ErrPlatformDomainTaken` if
  one exists; a policy module with no tenant module has no roster and runs no
  such check, and that absence is legal.
- **The account lifecycle cleanup is idempotent and crosses tenants.** A
  deleted account's binding rows are removed across every tenant; running the
  cleanup again is no work and no error. A binding row that outlives the
  deletion names an account the authentication side can no longer produce, so
  the account is unusable there whether or not the row arrived.
- **The input items are split by when they are read.** `authz.policy.load-timeout`
  is configuration: it bounds the startup load and is read before the snapshot
  exists. `authz.policy.refresh-interval` is a setting: it is the periodic
  reload this module falls back on with no event bus, read at runtime, and zero
  turns it off. The module declares no runtime switch that loosens a judgement.
- **The optional capabilities are taken up in `Init` and their absence is
  legal.** The roster, the bus, the settings store and the logger are each
  optional: a policy module with no roster checks no platform-domain conflict,
  one with no bus relies on the refresh interval, one with no settings carries
  no refresh, and one with no logger writes through the process default. The
  database is the one hard dependency; the engine, the catalog and the manager
  are delivered exclusively, so a second provider of any of them fails the
  startup.
- **A secret, a credential and a policy value reach no log line and no error
  text.** An account key and a tenant may be logged; a grant's rules and a
  binding's role reach no log. A refusal names the row that failed and never
  the value that failed it.

## Running it

From the repository root, over every module in the workspace:

```
make build
make test
make test-race
make lint
make check
```

`make check` is what CI runs. On this package alone:

```
go -C pkg build ./authz/...
go -C pkg vet ./authz/policy/...
go -C pkg test ./authz/policy/...
go -C pkg test -race ./authz/policy/...
env -C pkg golangci-lint run ./authz/policy/...
```

The cases bring up a real database: SQLite through `pkg/internal/dbtest`, and
PostgreSQL through `pkg/internal/pgpool/pgtest`, which starts a container and
skips on a machine without docker. The migration cases run the module's own
files; `policy_test.go` drives the management surface over a handle with the
tenant filter installed; `assembly_test.go` drives the policy and guard
descriptors through a real registry, which is where what each callback takes up
and what the two modules resolve from each other is observed; `module_test.go`
exercises the change event, the refresh interval and the load timeout. The
snapshot and the model self-check are tested under `pkg/authz/internal/policy`.
