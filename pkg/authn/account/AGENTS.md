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

The API is the package's godoc and the interface comments in `pkg/authn`.

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
- **`signup` and `auto-link` are settings, declared here and read on every
  decision.** Both default to the strict value (`closed`, `never`); a move toward
  `open` or `verified-email` is a relaxation, so the `settings.changed`
  subscriber writes a `Warn` and a best-effort `authn.settings.relaxed` audit row
  naming the key and the direction (`relax.go`). A process that starts with the
  switch already relaxed writes the same `Warn`. The login engine reads
  `authn.account.*` by path through its optional settings store; this module is
  the only declarer. The cost settings and the remaining runtime parameters are
  settings too, read on every use and judged against their declared domain when
  read as well as when written. The config schema no longer carries the two
  switches: a config file naming them fails startup as an unknown key.
- **`password-algorithm` is a setting, declared here and read at every hash
  write.** It is `argon2id` (the default) or `pbkdf2-sha256`; enrollment and the
  post-login upgrade both read the current value. A read that fails or lands
  outside the domain does not fall back to another algorithm: enrollment and a
  password change answer `authn.ErrUnavailable`, and the post-login upgrade is
  skipped with one `Error` naming the key while the login itself still succeeds.
  The one exception is a key no declaration covers: the store reserves that
  answer for a defect in this module's own schema, so the declared default is
  written and the defect is reported at `Error` rather than blocking every
  password write.
  A move to `pbkdf2-sha256` is a downgrade, so the `settings.changed` subscriber
  writes a `Warn` and a best-effort `authn.settings.relaxed` audit row with
  direction `downgrade`; a move back to `argon2id` writes nothing. The config
  schema no longer carries the item: a config file naming it fails startup as an
  unknown key.

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
`dbtest.Postgres` takes a database from `pkg/internal/pgpool/pgtest`, on the
server the repository's compose stack brings up. The PostgreSQL leg carries the
`integration` tag, so a default run compiles none of it, and an integration run
fails when nothing answers there. A case that ends a
`testClock`'s instant far from the real one will disagree with a statement that
read the real clock, which is how a module that bypasses `internal/clock`
fails here.
