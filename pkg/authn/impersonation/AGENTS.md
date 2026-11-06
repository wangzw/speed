# pkg/authn/impersonation

The impersonation module: one row per impersonation an administrator began,
in the table `authn_impersonations`. A record is a lifetime, a reason and one
tenant, and not a new credential and not a switch on an identity. It hangs on
the administrator's own session; the browser carries it as a marker on that
session, the API carries it as a short-lived impersonation access token, and
the administrator stays the accountable party through every request.

The descriptor declares the static feature `authn.impersonation` for the
self-service impersonation routes, present whenever the module is in force.

The module delivers `authn.Impersonation` exclusively, owns the table and its
migration, mounts the `B/impersonation` routes through `authn.mount`, and
writes the seam-level audit rows and the two impersonation events.

The API is the package's godoc.

## Boundaries

- **The structural restrictions are this module's, the decision seam is the
  host's.** `Start` enforces the four groups — the actor's facts, the
  target's, the parameters, the session's concurrency — before it asks the
  `Decider`, and the `Decider` may only tighten them. None of them has a
  switch that turns it off. A target or a parameter violation and a
  step-up shortfall are answered apart so that nothing the caller learns
  tells it a target's state.
- **The mandatory audit is written outside the record transaction.** The
  audit seam takes its own connection and does not join the caller's
  transaction, so `Start` writes the granted row before it opens any
  transaction; a start whose audit cannot be written is refused with
  `ErrAuditRequired` and writes no record. The record transaction writes only
  `authn_impersonations` and calls neither `audit.Record` nor `Bus.Publish`.
- **The record is written before the carrier is attached.** `Start` cannot
  reach a response writer, so the handler attaches the session marker or
  issues the token after `Start` returns, and compensates on a failure by
  ending the record with the reason `start-failed`. `End` never touches a
  carrier: the handler clears the marker, and the path that revokes an
  impersonation token has no cookie at all.
- **A lapse is a read, written back once.** `Active`, `Start`, `List` and
  `Revoke` share one judgement (`active.go` `judge`, `settle`). `Active` is
  what the session and token backends ask; `Start` judges the session's
  unended record inside its record transaction and writes a lapse back there
  before inserting the new record; `List` settles every unended record on the
  page before it returns and returns the page as persisted; `Revoke` settles
  the record before it writes `revoked`, so a record that has already lapsed
  ends with its own reason and the revoke is a no-op. `Get` does not judge. A
  record that has lapsed is written back with a conditional update, and the
  caller that changes the row publishes the ended event and counts the end;
  `Start` does both only after its transaction commits, and a rolled-back
  transaction neither publishes nor counts. An expiry publishes nothing.
- **The management paths read the session from the store.** `List` and `Revoke`
  judge the bound session with `Sessions.StatusFresh`, so an administrator sees
  and revokes against the store rather than against what a verifying backend
  may still believe. `Active` reads `Sessions.Status`, because it is the
  verification hot path the check-ttl cache serves.
- **The module caches nothing.** `Active` reads the record, the session and
  the account each time; the caching is the verifying backends', bounded by
  `authn.session.check-ttl` and cleared by `authn.impersonation.ended`. A
  `List` page holds the account and membership answers it reads for the length
  of the call, so rows that share them are judged from one read.
- **The table is this module's.** `migrations/{postgres,sqlite}` hold the one
  migration that creates `authn_impersonations`, its indexes and the partial
  unique index `authn_imp_one_active`. `Start` probes the table and fails
  with `ErrNotMigrated` when it is absent, because `New` does not touch it.
  Rows carry no foreign key; every time in them is UTC, and a read
  normalises the zone. The record is never deleted.
- **A tenant is named by its alias, never its identifier.** The record stores
  the internal identifier; the grant view and the audit detail answer the alias
  through the optional `authn.TenantAliasResolver`. An assembly with no resolver
  names no tenant rather than exposing the identifier.
- **The reason never leaves the record and the audit row.** It is not in an
  event payload, a log line or a response. The audit detail's key set is
  closed, and the session is written there as `sid`: `session`,
  `on_behalf_of` and `impersonation_scope` belong to the audit journal on an
  impersonating row.
- **The routes are mounted through `mount.Mounter` and the access level is
  registered beside them.** `POST /impersonation` is guarded and not
  impersonable; `GET` and `DELETE /impersonation` are guarded and
  impersonable, so an administrator can always see and end an impersonation.
  The ordinary scope's mount endpoint (`authn.gate.mount.endpoints.default`)
  may not be one the admission layer guards, which the module's `Start` refuses
  with `ErrInvalidConfig`.
- **An unverified self-registration cannot start an impersonation.** The first
  condition of the actor's group reads `SignupVerification.SignupStateOf` and
  answers `ErrContactRequired` (`403 contact-required`) when the state is
  pending, before the strength, freshness, method-set and `Seasoned` checks and
  before the `Decider`. That answer writes no audit row and counts no denied
  attempt: it is guidance to prove an address, not an attempt. A state that
  cannot be read is `ErrUnavailable` (503) with the same silence, never a pass.
  A service account is not a candidate in the first place, and an account that
  never self-registered is exempt. `SignupVerification` is required, so an
  assembly that takes this module in also delivers the account module. The
  subject is the initiator, `Actor()`, not `Account()`: an impersonating
  identity's `Account()` is the target, whose state never takes part, and
  `Start` is a host-visible seam that judges this itself rather than relying on
  the handler having refused the impersonating identity first.
- **The required dependency `ScopeGuard` is held, not called, by the module's
  own logic beyond the endpoint check.** It is what makes the absence of the
  admission layer fail the assembly before any instance is built. The scope
  enforcement itself is the gateway's and the admission layer's.
- **`Get` reads one record beyond the published `Impersonation` method set.** The
  administration routes need a record's tenant and target before asking the
  Decider, which `List` alone cannot give; the method set of `Impersonation` is
  fixed, so `authn.admin` reaches `Get` through its own narrow interface and
  mounts the record routes only when the capability answers it. A record that is
  not there is `authn.ErrNotImpersonating`, and an ended record is still
  returned (`Get`, `TestGetReadsOneRecordAndReportsAMissingOne`). `List` confines
  to `GrantQuery.Tenant` in the store, so a tenant's page is not a full page
  filtered afterwards (`TestListConfinesToTheQueriedTenant`).
- **A new end reason, a new carrier, or another writer of the table** belongs
  in this module only as a deliberate change, because the closed set of end
  reasons is a promise to the session and token modules and to the host.
- **A setting moved toward more lenient is visible after the fact, never
  blocked.** The subscription to `settings.changed` writes a `Warn` and a
  best-effort `authn.settings.relaxed` audit row naming the key and the
  direction and no value; the settings it watches are `max-ttl` and
  `max-auth-age`, each relaxing in the `increase` direction (`relax.go`,
  `TestARelaxingChangeWritesAWarnAndAnAuditRow`).

## Running it

From the repository root, over every module in the workspace:

```
make build
make test
make lint
make check
```

`make check` is what CI runs. To run this package alone, from `pkg/`:

```
go test -race ./authn/impersonation/...
go test -tags=integration -race ./authn/impersonation/...
```

The tests run over both dialects the store supports (SQLite and PostgreSQL)
and need the same fixtures the rest of the repository uses. Only the SQLite leg
belongs to a default run; the PostgreSQL leg needs the `integration` tag and the
repository's compose stack, and acquires a database through
`pkg/internal/pgpool/pgtest`, which fails rather than skips when nothing answers
there.
