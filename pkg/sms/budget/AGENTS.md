# pkg/sms/budget

The shared ledger of SMS attempts. It owns one table, `sms_budget_events`, and
delivers one capability, `Store`: both the authentication capability
(`pkg/authn/sms`) and the notification capability (`pkg/notification/delivery`)
write their attempts here and read the counts their budgets are compared
against. It sits under the SMS capability directory because it is about SMS
sending; it registers a module of its own, `sms.budget`, because it owns a table
and the database module has to apply its migrations.

A row is an attempt, not a delivery. It is written when the caller spends its
budget, before the carrier answers. `outcome` says what was known then;
`refunded_at` is the state that changes later. `suppressed` marks an attempt the
caller deliberately did not send: it is refunded at write time, so it spends no
send budget, and it is the row that keeps a refusal from revealing whether a
destination was registered. One row per attempt, whichever channel made it.

## Boundaries

- **The ledger stores and counts; it does not decide.** No limit, window,
  retry-after policy or response a throttled caller receives lives here. The
  consumers own those, read their own settings and compare them to the counts
  this package answers. A budget added here would be a second place a
  deployment's policy is written down. The one exception is the cross-channel
  hourly ceiling, `sms.ceiling-hourly`: it spans both sending channels, so
  neither consumer can own it alone, and this module declares it once under the
  shared `sms` namespace. Its value is still read by the consumers; this module
  only states the key, its default and its domain.
- **A refund removes a row from send counts and leaves it in attempt counts.**
  That is the whole reason the two counts are separate. A message the carrier
  certainly did not take — a rejection, an unroutable destination, a throttle —
  costs no real send budget, and a caller that keeps aiming at a destination
  that always fails still spends its attempt budget. An uncertain outcome is not
  refunded: it stays in both counts, which is the direction that cannot do harm
  if the message did go out.
- **`outcome` is written once.** The set is closed — `pending`, `sent`,
  `uncertain`, `throttled`, `rejected`, `unroutable` — and no method rewrites
  it. A caller that writes the row before the carrier answers writes `pending`
  and marks the row refunded on a certain failure; a caller that knows the
  answer already writes it at Record. The row's diagnostic value is what was
  known when the attempt was charged, and the refund is the only fact that
  changes afterwards.
- **A suppressed row is refunded at write time.** `Record` refuses a suppressed
  attempt whose `RefundedAt` is nil, so the invariant is the store's, not every
  caller's. A suppressed row spends no send budget and no share of the
  cross-channel ceiling, and it takes part in a count only when the query asks
  for it.
- **The query has two independent switches.** `IncludeSuppressed` (false by
  default) decides whether suppressed rows take part; the refund switch decides
  which refund state counts. The default is a send budget
  (`refunded_at IS NULL AND NOT suppressed`), `IncludeRefunded` is an attempt
  budget (every row, `NOT suppressed` unless `IncludeSuppressed`), and
  `OnlyRefunded` counts rows refunded after the fact (`refunded_at IS NOT NULL`,
  `NOT suppressed` unless `IncludeSuppressed`). `OnlyRefunded` with
  `IncludeRefunded` is refused: one asks for the refunded subset and the other
  for every row. A send budget with `IncludeSuppressed` counts a suppressed row
  as a charge even though it is refunded, which is how a per-destination count
  sees a request the deployment deliberately did not send. The exact predicates
  are in the `Query` godoc; a caller reads them there instead of guessing.
- **A nullable column has a meaning.** `source_mac` is null when the attempt has
  no trustworthy source: it takes no source budget and matches no source count.
  An empty source would put every such attempt under one key, so the store
  refuses a present-but-empty pointer instead. `account_id` is null when the
  purpose has no account, which is the login and reset purposes and a
  notification delivered to a recipient the caller did not name; the
  `DimensionAccount` count scopes by it, and a null matches no account. `tenant_id`
  is not null; an attempt that is not scoped to a tenant carries the empty string,
  which no tenant count matches, because a notify-only row always has a tenant
  and an authentication row does not.
- **Neither the source identity nor the destination is stored in the clear.**
  `source_mac` and `destination_mac` are the caller's keyed digests. The
  plaintext, a number or an address, never reaches this table or this package's
  logs.
- **The channel is a closed set and the purpose is not.** A channel is `authn` or
  `notification`, and the table's check constraint enforces it. A purpose is the
  authentication module's purpose (`login`, `mfa`, `enroll`, `reset`) or the
  notification type's key; it is an open string and this package does not rank
  it.
- **The store holds no counter and no in-process state.** `Record` is one
  INSERT; every count is a query over committed rows. Concurrent records are
  concurrent inserts, so the lost-update shape a shared counter has does not
  exist here, and the concurrency case holds that every row written in parallel
  is counted. Whether a budget is a hard cap is therefore the caller's
  transaction: a caller that must check and record atomically runs both on a
  store built over its transaction with `New(tx)` and serialises the dimension
  itself. The store locks nothing, and a caller that counts and records outside
  one transaction races between the two statements.
- **`CountCeiling` is the one count that spans the channels.** It counts every
  non-refunded, non-suppressed attempt since a time, over both channels, and is
  the deployment emergency cap both consumers share. Every other count is scoped
  by `Channel`, optionally by `Purpose`, and at most one indexed dimension.
- **`Oldest` and `Latest` read the same rows `Count` does.** `Oldest` is when a
  window frees a slot; `Latest` is what a resend interval is measured from. Both
  take a `Query`, so the refund and suppression switches narrow them exactly as
  they narrow a count.
- **`PruneBefore` is the retention sweep.** It deletes rows attempted strictly
  before a cutoff and answers how many it deleted. It is a method and not a
  scheduled job: this module runs no ticker, and wiring a daily 30-day prune is a
  follow-up. A caller that adds the job owns its schedule and its handler.
- **Windows are inclusive at `Since`.** A row written exactly at the boundary is
  inside the window; one a nanosecond earlier is not. Every time is normalized
  to UTC before it reaches the database, because SQLite compares a time as the
  text it carries.
- **The module declares the shared ceiling and no other setting.** The
  consumers own every other setting and read them; this package's module
  delivers `Store`, the migration set and the `sms.ceiling-hourly` declaration. A
  consumer that takes `Store` in its `Requires` pulls `sms.budget` into the run's
  dependency closure, because the provision is non-exclusive.

The five indexes are the five count shapes: `(channel, attempted_at)`,
`(source_mac, attempted_at)`, `(destination_mac, attempted_at)`,
`(tenant_id, attempted_at)` and `(account_id, attempted_at)`. A new count over a
column that has no index is a migration, not a query change. The first four ship
in migration `0001`; the account index and the `suppressed` column ship in
`0002`.

## Running it

From the repository root, over every module in the workspace:

```
make build
make test
make test-race
make lint
make check
```

`make check` is what CI runs. On this package alone, inside the `pkg` module:

```
go -C pkg build ./sms/budget/...
go -C pkg vet ./sms/budget/...
go -C pkg test ./sms/budget/...
go -C pkg test -race ./sms/budget/...
env -C pkg golangci-lint run ./sms/budget/...
```

The untagged cases run on SQLite and build the table from the module's own
migration file, so a case cannot drift from the schema a deployment gets. With
`make deps-up` running, the `integration` build adds PostgreSQL beside it and
the same cases run on a database of the case's own:

```
go -C pkg test -tags integration ./sms/budget/...
```
