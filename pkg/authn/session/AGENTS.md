# pkg/authn/session

The session module: the login sessions of the identity system. A browser
session is a cookie, a token session stands behind an access token and a
refresh token, and a service session is one row per service-account secret.
The three share one table, `authn_sessions`, and one revocation surface.

The module is the only place the session cookie is named, written, cleared
and read. It delivers the session store (`authn.Sessions`, exclusively), a
credential-verifying backend that recognises the session cookie
(`authn.Verifier`, non-exclusively) and the hook that cleans up after an
account is disabled, deleted or has a secret revoked
(`authn.AccountLifecycle`, non-exclusively). One instance satisfies all
three, and it also implements the optional `authn.CookieClearer` that the
gateway discovers by a type assertion.

The API is the package's godoc.

## Boundaries

- **The session library, `scs`, is used for the token, its digest in the
  store, the cookie's attributes and the creation path, and for nothing
  else.** The store adapter over the table, the public identifier, the
  revocation, the period of each kind, the node-local caches and the token
  rotation are this package's own. The library appears in this package and
  in no signature of the interface package.
- **The store adapter caches nothing and never decides revocation.**
  `FindCtx` finds only a live browser row; `CommitCtx` creates a row and
  ignores the expiry the library computed; `DeleteCtx` unlinks a token and
  sets nothing else. Revocation is `Sessions.Revoke` and is the row's
  `revoked_at`.
- **A cached answer is bounded by `check-ttl` and is a relaxation, not a
  decision.** The three caches (session, account snapshot, impersonation
  record) are `internal/acctcache` instances built for one value of the
  setting; a change of the setting replaces the set. The `sid` index is the
  session module's own, beside those caches: `acctcache` holds one key per
  entry, so clearing a session-cache entry by a session's second key (`sid`)
  needs an index the module maintains itself, bounded to the same capacity and
  cleared and evicted with the caches. A value a request reads is shared and
  read-only; a reader that merges something into it copies first.
- **The correctness of validity does not depend on the cleanup hooks
  running.** Every verification checks the account's status and `valid_after`
  as well as the row, so a hook that was missed still does not let a session
  through. The hooks and the events only shorten how long a stale row lives.
- **No token, cookie value, password or secret reaches a log line, an error
  text, an event or a metric attribute.** A token is held as its digest from
  the moment it is read; a public session identifier is no credential.
- **Failures are fail-closed.** A value that cannot be a token is refused
  before any read; a database that cannot be reached is `Unavailable` and
  the cookie is left for the browser to send again; a cookie that does not
  hold is `Denied` and the gateway clears it. A cache entry the idle period
  ran out of, and an entry past `check-ttl`, are both re-read.
- **The table is this module's.** `migrations/{postgres,sqlite}` hold the one
  migration that creates `authn_sessions`; `Start` probes the table and
  fails with `ErrNotMigrated` when it is absent, because `New` does not touch
  it (on a first start it is not there until the migration runs). Rows carry
  no foreign key; every time in them is UTC, and a read normalises the zone.
- **The cookie's configuration is read from the gateway's address.** The
  name and the `Secure`, `Domain` and prefix attributes follow
  `authn.gate.mount.public-url` and `allow-insecure`; `authn.session`'s own
  items are `cookie-domain` and `same-site`. A configuration that cannot run
  on (`same-site: none`, an address that is not https with no
  `allow-insecure`) is `ErrInsecureConfig` from `Prepare`, so a module a
  drive includes fails the startup rather than running insecurely.
- **The routes are mounted through `mount.Mounter` and the access level is
  registered beside them.** `GET /session` is optional and impersonable and
  answers `authenticated: false` with no credential; `POST /logout` is
  guarded and impersonable; listing and revoking sessions is guarded and not
  impersonable, and the handler refuses an impersonating identity again.
- **The routes are declared as HTTP operations, and the fragment is generated
  from the declarations.** `operations.go` declares the five operations —
  method, path relative to the API root, summary, description, path parameter
  and responses — and binds each handler's input and answer types. The session
  read, the listing and the bulk revocation use `Operation[In, Out]`, so the
  entry point binds the request and writes the declared body; logout and
  revoking one session use `ReplyOperation[In]`, because they answer 204 and
  clear the session cookie through `WithCookies`. `openapi/session.json` is a
  build product of the same declarations, generated by `pkg/internal/openapigen`,
  never written by hand. The mounter takes each route's path from its
  declaration, so the fragment and the registration cannot drift.
- **A new kind of session, a new credential a backend stands for, or another
  reader of the table** belongs in this module only as a deliberate change,
  because the periods, the validity judgement and the revocation surface are
  settled here.
- **A setting moved toward more lenient is visible after the fact, never
  blocked.** The subscription to `settings.changed` writes a `Warn` and a
  best-effort `authn.settings.relaxed` audit row naming the key and the
  direction and no value; the settings it watches are `idle-timeout`,
  `absolute-timeout`, `token-absolute-timeout`, `sso-absolute-timeout` and
  `check-ttl`, each relaxing in the `increase` direction (`relax.go`,
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
go test -race ./authn/session/...
go test -tags=integration -race ./authn/session/...
```

The tests run over both dialects the store supports (SQLite and PostgreSQL)
and need the same fixtures the rest of the repository uses. Only the SQLite leg
belongs to a default run; the PostgreSQL leg needs the `integration` tag and the
repository's compose stack, and acquires a database through
`pkg/internal/pgpool/pgtest`, which fails rather than skips when nothing answers
there.

The fragment is regenerated after a change to the operation declarations:

```
UPDATE_FRAGMENTS=1 go -C pkg test ./authn/session -run TestTheFragmentIsGeneratedFromTheDeclarations
```

Without the variable the same test is the drift gate; `make check` runs it.
