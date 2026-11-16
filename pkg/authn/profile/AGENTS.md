# pkg/authn/profile

The authentication capability's user-profile self-service module. It mounts
`GET B/me` and `PATCH B/me` under the base path `B` — the path the `authn.mount`
module joins its routes to, `/api/v1/auth` unless configured otherwise — and delivers no
capability of its own.

It holds no data, no rule and no store. Every route's action is a call on the
account module, reached through the `authn.Accounts` read surface and the
`authn.AccountAdmin` write surface, so this module only reads the caller from the
request, checks what it may do, and shapes the answer.

Taken in only by a command that wants this surface, it is absent from every other
command: without it the account is still read and written through its Go
interfaces by the host's own commands. It states `StateEnabled` and never
`StateAuto`: whether the profile surface exists is a decision on the command tree,
not a conclusion a configuration can infer.

The API is the package's godoc, plus the `Accounts`, `AccountAdmin` and
`SignupVerification` interfaces and the `Account`, `Identifier`,
`ExternalBinding` and `SignupStatus` types in `pkg/authn`.

## Routes

Both routes are mounted guarded: the gateway authenticates the endpoint, and a
request with no credential never reaches a handler. This set is the module's whole
route surface.

| Method and path | Access | Action |
|---|---|---|
| `GET B/me` | Required, impersonable | read the caller's own profile |
| `PATCH B/me` | Required | write the caller's own profile, within the closed writable set |

`B` is the base path the module joins its routes to; with the default
configuration that is `/api/v1/auth`, so the two routes are `/api/v1/auth/me`.

## Boundaries

- **The account is the one the identity names, and no path carries one.** A route
  reads its caller through `authn.FromContext` and takes the account from the
  identity. No path parameter names an account, and no value the client sent is
  dereferenced to find one. The authority to read and write the account is the one
  fact this module can check for itself — the caller is that account — so no
  `Decider` is asked and no permission point is required. This is what separates
  the profile surface from the administration surface: a management action needs a
  permission point and is decided through the `Decider`; a self-service action is
  authorised by the identity alone.
- **The read face publishes the self-registration state and the proof behind each
  identifier.** `GET B/me` reads `authn.SignupVerification.SignupStateOf` on the
  request and answers `verification: {state, email, since}`: `state` is `none`,
  `pending` or `verified`; `email` is the address the account module selects for
  that state, and is empty when there is none; `since` is the first instant the
  account reached verified and is kept when a removal returns it to pending.
  Each identifier carries `proven`, derived from `Verification.Proves`: the
  four proofs are true and `admin`, `import-unverified` and `self` are false.
  The pending contact is not an identifier and appears only in
  `verification.email`. A state read that fails is a 503 and is never answered
  as `none`.
- **The writable set is closed, and the read side is broader than the write side.**
  The display name, the whole attribute set, the language and the time zone are
  writable. The account's status, its identifiers and its external bindings are
  not: each has its own route and a stronger authentication requirement — the
  status and the identifiers in the administration surface, the identifiers'
  removal and the external unbinding in the login engine's own self-service
  routes. A new writable field needs a new decision.
- **The write reuses the account module's write surface and adds no rule of its
  own.** The display name goes through `AccountAdmin.SetDisplayName` and the
  attribute set through `AccountAdmin.SetAttributes`, once each. The display
  name's length, the attribute set's bounds and the key shapes are the account
  module's to judge; this module maps what it returns to responses rather than
  re-checking them. The language and the time zone are named uses of the attribute
  set, under the keys `i18n.LocaleAttributeKey` (`i18n.locale`) and
  `i18n.TimeZoneAttributeKey` (`i18n.tz`) that `pkg/i18n` declares; nothing new is
  stored and no preference table is added.
- **One merge composes the attribute set, and one write lands it.** A given
  `attributes` replaces the whole current set; a given `locale` or `time_zone`
  then overwrites its key, and an empty one clears it. The set is read, merged and
  written back as a whole in a single `SetAttributes`, so two concurrent writes
  that change different keys can lose one of them and the loss is bounded to the
  concurrent pair. This is the shape the language and time-zone preference write
  already had.
- **The two writes are not one atomic change.** When the display name lands and
  the attribute set does not, the display name's change is in force and the answer
  is the failure's. The display name is written first; a failure of the first
  write stops the second.
- **Both routes are impersonable.** An impersonating identity reads and writes
  the impersonated account the identity's `Account` names, so the read face and
  the write are registered through `mount.Impersonable`. The write handler keeps
  no impersonation refusal of its own: the operation is one the effective account
  may perform.
- **A write needs a login session and no freshness beyond it.** An identity with
  no session has nothing this route may act for and is refused 401 before the body
  is read. The session's age is not a condition: a profile write asks for no
  step-up, so an authentication of any age within a live session writes.
- **The body is closed and its fields are optional.** Every field left out is left
  as it is, and a field the body does not have is refused — the request decoder
  rejects unknown fields. The capability's decode rule runs in front of the
  bound handler: `application/json` with no parameter or with `charset=utf-8`
  only, a non-empty body of at most 64 KiB, and one JSON value of the closed
  shape. A refusal carries the capability's own problem code rather than the
  entry point's default; a literal `null` is the zero value and changes nothing.
  The language is checked with `i18n.ValidLocaleTag` and
  the time zone with `i18n.ValidTimeZone`; a value outside either domain is a 400
  and reaches no write.
- **The refusal mapping is closed and fail-closed.** A request with no identity,
  or an identity with no account, is a 401. The identity's own account gone — a
  tombstone, or a row that cannot be read — is the same 401 on either route. A
  value the account module refuses on shape or on its attribute bounds
  (`ErrInvalidArgument`) is a 400. Anything else is a 503 whose cause stays in the
  log and never reaches the caller. The module does not retry and does not
  degrade: a write that fails is that failure, with nothing queued and nothing
  part-rolled-back.
- **The audit row is best-effort.** When the assembly delivers the audit
  capability, a write that changed something leaves one `authn.profile.update` row
  on the account; a row that does not land is an Error line and changes nothing
  about the answer. An empty body writes no row, because it changed nothing. With
  no audit capability in the assembly the write still lands and writes no row.
  The row names the account and the kind of change and carries no field value the
  client sent.
- **The routes are declared as HTTP operations, and the fragment is generated
  from the declarations.** `operations.go` declares the two operations — method,
  path relative to the API root, summary, description, request body and
  responses — and binds each handler's input and answer types. Both use
  `Operation[In, Out]`, so the entry point binds the request and writes the
  declared body. The write route's checks that must run before the body is read
  — the identity and the session — stay in a wrapper in front of the bound
  handler, and the capability's decode rule is a second wrapper between them and
  the bound handler. `openapi/profile.json` is a build
  product of the same declarations, generated by `pkg/internal/openapigen`,
  never written by hand. The mounter takes each route's path from its
  declaration, so the fragment and the registration cannot drift.
- **The module delivers no capability and registers `authn.profile`.** It carries
  no product another module resolves; it is taken in only to mount the routes.
- **It requires the route mounter, the account read and write surfaces and the
  signup verification reads.** A command that brings this one in without the
  mounter to mount on or the account to act on fails the startup before any
  instance is built. The audit capability and the logging capability are declared
  optional: their absence is a legal assembly, and each is taken up in `Init`.
- **The answer is a view, built field by field.** Every route answers with a view
  assembled from the account module's values and never the value as it stands, so
  a field added to a record later does not reach a client by being there. The
  read view's external bindings carry no subject: the provider's own name for the
  person is no part of what the account's holder is shown. A time is written as
  an RFC 3339 instant in UTC ending in `Z`; an absent time is `null`, and the
  field is declared nullable (ADR-api-time-format). The write view gives the
  self-writable fields as they stand after the write.

## Running it

From the repository root, over every module in the workspace:

```
make build
make test
make lint
make check
```

`make check` is what CI runs. On this module alone:

```
go -C pkg build ./authn/profile/...
go -C pkg vet ./authn/profile/...
go -C pkg test ./authn/profile/...
go -C pkg test -race ./authn/profile/...
env -C pkg golangci-lint run ./authn/profile/...
```

The tests ask for no service, no database and no environment. They drive the
handlers over a `mounttest.Mounter` with stand-ins for the account reads, the
account writes and the audit, and a real identity from `authntest`,
so a case can assert that a write the guard refused and a write with no session
reached no capability call. The assembly cases drive the
module's descriptor through a real registry, which is where what it requires and
delivers is observed and where the two routes are seen mounted with their access
levels.

The fragment is regenerated after a change to the operation declarations:

```
UPDATE_FRAGMENTS=1 go -C pkg test ./authn/profile -run TestTheFragmentIsGeneratedFromTheDeclarations
```

Without the variable the same test is the drift gate; `make check` runs it.
