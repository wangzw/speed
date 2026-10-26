# pkg/authn/account

The account store of the authentication capability, registered as
`authn.account`. It owns the tables for the accounts, the identifiers they are
found by, the contact reservations they own, their external
identities and tenant memberships, their password credentials, the secrets of
service accounts, and the failure back-off; it delivers eight capabilities
exclusively — `Accounts`, `AccountAdmin`, `Signups`, `SignupVerification`,
`Passwords`, `Memberships`, `MembershipCleanup` and `Throttle` — and it creates
an account and moves it between disabled, enabled and deleted, calling every
`AccountLifecycle` hook after the transaction commits. An explicit membership
removal asks every optional `MembershipGuard` first and calls every
`MembershipLifecycle` hook after the removal commits; the account-deletion
cleanup removes through `MembershipCleanup`, which asks neither.

No other module reads these tables. Every module that needs an account, a
credential, a membership or a back-off takes one of the eight interfaces from the
registry; the `*gorm.DB` stays inside this package, and so do the migrations.

The API is the package's godoc and the interface comments in `pkg/authn`.

## Boundaries

- **The eight interfaces are one instance.** The method names of `Accounts`,
  `AccountAdmin`, `Signups`, `SignupVerification`, `Passwords`, `Memberships`,
  `MembershipCleanup` and `Throttle` are pairwise disjoint, which is what lets
  the one `*product` satisfy all of them. `Memberships` reads one membership as
  `Membership` and writes as `Put`, never `Get` or `Set`, so that a name never
  collides with `Accounts.Get` or `Passwords.Set`. A new method must not reuse a
  name another of the eight already has.
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
  way, through `AccountLifecycle` and `MembershipLifecycle`, which the cleaning
  modules implement and this one takes up with `core.ResolveAll`. A hook runs
  after the transaction it cleans up after has committed, never inside it. A
  state-change or deletion hook must not read the account row beyond the
  tombstone, and a membership hook must not read the membership row, which is
  gone; `AccountCreated` runs while the account exists, may read it, and does
  nothing when a retry finds the account deleted.
- **An explicit membership removal is guarded before it deletes and cleaned
  after it commits.** `Memberships.Remove` asks every optional
  `authn.MembershipGuard` (they stand side by side, taken up with
  `core.ResolveAll`; any refusal refuses) only when a membership row is there to
  remove: a guard's `authn.ErrLastTenantAdmin` leaves the row in place, and any
  other guard error is `ErrUnavailable`, never a pass. It then calls every
  `authn.MembershipLifecycle` hook, including on a retry whose row is already
  gone, and a failing hook makes the call `ErrCleanupIncomplete`. Account
  deletion does not come through `Remove`, so it consults neither the guard nor
  the membership hooks; its own `AccountDeleted` hook and the membership events
  are its paths, and its cleanup collects rows through
  `MembershipCleanup.RemoveMembershipForDeletion`, which asks neither.
- **Every time written to the database or bound to a query goes through
  `internal/clock` and is converted to UTC.** The SQLite driver compares times
  as text, so a non-UTC value makes `valid_after`, a back-off's lock and an
  expiry compare wrongly; the model path's `CreatedAt` and `UpdatedAt` are
  assigned explicitly, since GORM's `NowFunc` reads the local zone.
- **`Prepare` states enabled and nothing else; `New` reads no table.** Whether to
  run is the command's decision. `Start` probes every table and refuses with
  `ErrNotMigrated` when one is missing, which is what tells a host to take the
  module in statically in the command that migrates.
- **`signup`, `password-signup` and `auto-link` are settings, declared here and
  read on every decision.** All three default to the strict value (`closed`,
  `closed`, `never`); a move toward `open` or `verified-email` is a relaxation,
  so the `settings.changed` subscriber writes a `Warn` and a best-effort
  `authn.settings.relaxed` audit row naming the key and the direction
  (`relax.go`). A process that starts with a switch already relaxed writes the
  same `Warn`. `Signups.Register` reads `password-signup` itself and is its
  authority; a caller that reads the same key before validating its input is
  only pre-checking, never deciding. The login engine reads `authn.account.*` by
  path through its optional settings store; this module is the only declarer.
  The cost settings, the registration budgets (`signup-ip-limit`,
  `signup-global-hourly`) and the remaining runtime parameters are settings too,
  read on every use and judged against their declared domain when read as well as
  when written. Their moves are watched by the same subscriber: a smaller cost or
  wait, a larger free-attempt count, and a budget raised are relaxations, and
  `signup-ip-limit` is not monotone — zero turns it off, so a positive value moved
  to zero is `to-zero` and a positive value raised is `increase`, while a positive
  value lowered is a tightening. The config schema does not carry the three
  switches: a config file naming one fails startup as an unknown key.
- **Self-registration is one transaction and one state machine.**
  `Signups.Register` derives the password under `signup-concurrency`, a width of
  its own with no memory budget that never takes a place from a password login,
  and then writes in one transaction the enabled account with `signup_state` 1, a
  username identifier written through `self`, the credential and one email
  reservation in `authn_pending_contacts`. It sends no email and creates no
  session. The reservation's global unique index on `(kind, value)` is the only
  arbiter of the address: a registration that loses it answers
  `authn.ErrContactTaken`, and an unproved reservation is accepted until the
  address is proved or the account is deleted. Proving an address disposes only
  the same-value reservation: an unproved one for an address the account
  replaced stays in place until the account is deleted.
  `authn_pending_contacts` is the reservation table for contacts. A row with an
  empty `verified_at` is an unproved target a registration wrote; `AddIdentifier`
  of an email address brings this account's row to proved now or inserts one, and
  a reservation another account holds answers `authn.ErrContactTaken`. A proving
  phone deletes the same-value pending target and changes no state; `admin`,
  `import-unverified` and `self` no longer leave an email reservation unproved.
  An email address the account already holds as an identifier is brought up to
  proved by a proving way in place: the row's `verified_at` and `verified_via`
  are rewritten, no second row is inserted and no identifier-changed event is
  published. A way that does not prove ownership never downgrades or replaces
  that row and answers `authn.ErrAccountExists`.
  `RemoveIdentifier` of an email deletes this account's reservation, releasing the
  address; removing the last proved email of a self-registration moves
  `signup_state` back to 1, keeps the instant and rebuilds no reservation. The
  verification read then reports pending only while another unproved address
  remains, and none once the account holds no email at all. An account holding a
  password credential cannot lose its last username or email address: that
  removal answers `authn.ErrIdentifierRequired` and the transaction rolls back,
  since the password would then authenticate nothing. An account with no password
  credential may lose its last login name, and removing a phone number never
  triggers the rule.
  `Create` always writes `signup_state` 0 and writes only identifiers; an email
  address also writes a proved reservation, and a reservation another account
  holds answers `authn.ErrContactTaken`. The way a username came about is
  `UsernameVerifiedVia`, whose zero value is `admin`.
- **The verification read is judged from the identifiers, not the column.**
  `SignupStateOf` reports Verified when a user account holds an email identifier
  whose way of verification proves ownership (the earliest `verified_at`, ties
  broken by identifier; `Since` is `signup_verified_at`, or that identifier's
  instant when the column is empty). It reports Pending when no email is proved
  but the account has an unproved email identifier (`admin`, `import-unverified`
  or `self`) or a reservation with `verified_at` empty (the earliest by
  `created_at`, ties broken by identifier, across both). It reports None when the
  account has no email address at all, and for every kind but user. None is
  therefore "nothing to verify", not "not a self-registration": an account an
  administrator created with an email address reads Pending until that address is
  proved, and one with no email address is not gated. The column is still
  maintained for the sweep, which deletes only `signup_state` 1 accounts and
  never an administrator-created one. Proving an email writes `signup_verified_at`
  the first time, on any account, so `Since` is available without a
  self-registration.
- **The unverified-account sweep has its own switch and interval.** It deletes
  only an account with `signup_state` 1, no `signup_verified_at`, no email
  identifier written through a proving channel, and `COALESCE(last_login_at,
  created_at)` older than `unverified-ttl`. The conditions are read once to pick
  a batch and re-judged in each deletion's own transaction under a lock of the
  account row, so an account that proved an address or logged in in between is
  skipped; a deletion erases the identifiers, credential and contact reservations
  with the row, through the same body `Delete` uses. `unverified-ttl` of
  zero turns it off, and `unverified-sweep-interval` is read before every pass,
  so a change needs no restart. `authn_pending_contacts` has no sweep of its own:
  its rows go with the account.
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
