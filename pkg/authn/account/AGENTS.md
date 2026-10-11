# pkg/authn/account

The account store of the authentication capability, registered as
`authn.account`. It owns the tables for the accounts, the identifiers they are
found by, their external identities and tenant memberships, their password
credentials, the secrets of service accounts, and the failure back-off; it
delivers five capabilities exclusively — `Accounts`, `AccountAdmin`,
`Passwords`, `Memberships` and `Throttle` — and it moves an account between
disabled, enabled and deleted, calling every `AccountLifecycle` hook after the
change is committed.

No other module reads these tables. Every module that needs an account, a
credential, a membership or a back-off takes one of the five interfaces from the
registry; the `*gorm.DB` stays inside this package, and so do the migrations.

## Where the authority is

- What this module is and why it is shaped this way:
  [`docs/design/modules/design-authn-account.md`](../../../docs/design/modules/design-authn-account.md),
  with [`design-authn-config.md`](../../../docs/design/modules/design-authn-config.md)
  for the settings and [`design-authn-failure.md`](../../../docs/design/modules/design-authn-failure.md)
  for the sentinels.
- The decisions behind it, one file per decision, in
  [`docs/adr/`](../../../docs/adr/):
  `adr-authn-accounts-2026-10-10`, `adr-authn-account-lifecycle-2026-10-10`,
  `adr-authn-external-binding-2026-10-10`, `adr-authn-password-hash-2026-10-10`,
  `adr-authn-throttling-2026-10-10`, `adr-authn-state-placement-2026-10-10`,
  `adr-authn-guarantees-2026-10-10`, `adr-authn-wiring-2026-10-10`,
  `adr-authn-packaging-2026-10-10`.
- The API itself: the godoc in this package and the interface comments in
  `pkg/authn`.

This file states only what neither of those states: the boundaries a change here
must not cross, and how to run it.

## Boundaries

- **The five interfaces are one instance.** The method names of `Accounts`,
  `AccountAdmin`, `Passwords`, `Memberships` and `Throttle` are pairwise
  disjoint, which is what lets the one `*product` satisfy all of them.
  `Memberships` reads one membership as `Membership` and writes as `Put`, never
  `Get` or `Set`, so that a name never collides with `Accounts.Get` or
  `Passwords.Set`. A new method must not reuse a name another of the five
  already has.
- **A table's name is one constant in three places.** A migration file, the
  model's `TableName` and the `table…` constant in `rows.go` all name the same
  table, and the `tables` list is what `Start` probes. A change to a name
  changes it in the constant, in every migration file of both dialects and
  wherever a raw statement writes it.
- **The hot and competing writes are raw SQL; the rest go through the model
  path.** `Touch`, the `last_used_at` write, the atomic increment of
  `authn_throttles` and the rehash's compare-and-swap are raw, so they leave no
  audit row and no tenant plugin touches them; a password or a service secret is
  a plaintext column and is written by raw SQL so that its value never enters a
  statement log. The audited writes — creating, changing status, deleting,
  binding and unbinding, and the memberships — go through the model path. A
  secret, a credential or a token never reaches a statement text, a log line, an
  error or an event; only a PHC entry or a digest is ever stored.
- **The module imports neither `pkg/tenant` nor the session, factor or SMS
  modules.** A tenant is a string here; the cross-module cleanup goes the other
  way, through `AccountLifecycle`, which the cleaning modules implement and this
  one takes up with `core.ResolveAll`. A hook runs after the account transaction
  has committed, never inside it, and an implementation must not read the
  account row beyond the tombstone.
- **Every time written to the database or bound to a query goes through
  `internal/clock` and is converted to UTC.** The SQLite driver compares times
  as text, so a non-UTC value makes `valid_after`, a back-off's lock and an
  expiry compare wrongly; the model path's `CreatedAt` and `UpdatedAt` are
  assigned explicitly, since GORM's `NowFunc` reads the local zone.
- **`Prepare` states enabled and nothing else; `New` reads no table.** Whether to
  run is the command's decision. `Start` probes every table and refuses with
  `ErrNotMigrated` when one is missing, which is what tells a host to take the
  module in statically in the command that migrates.
- **The two switches that relax a check take the primary source alone.** `signup`
  and `auto-link` are configuration, not settings: the settings write surface has
  no access control, so a value that can open a check is not writable there. The
  cost settings and the nine runtime parameters are settings, read on every use
  and judged against their declared domain when read as well as when written.

## How to run it

From the module directory:

```
env -C pkg go build ./authn/account/...
env -C pkg go test ./authn/account/... ./authn/internal/password/...
env -C pkg go test -race ./authn/account/...
env -C pkg golangci-lint run ./authn/account/... ./authn/internal/password/...
```

Run `golangci-lint` from `pkg`, never from the workspace root with an absolute
path: run from outside the module it reports a typechecking error and still
exits 0.

The cases run on SQLite and on PostgreSQL. `dbtest.SQLite` needs nothing;
`dbtest.Postgres` takes a database from `pkg/internal/pgpool/pgtest`, skips on a
machine without Docker and fails where CI is set. A case that ends a
`testClock`'s instant far from the real one will disagree with a statement that
read the real clock, which is how a module that bypasses `internal/clock`
fails here.
