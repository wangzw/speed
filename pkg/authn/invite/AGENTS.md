# pkg/authn/invite

The authentication capability's invitation module. It owns the
`authn_invitations` table, the state machine of an invitation, and the routes of
the invitation's issuance face and its acceptance face. It delivers the
`Invites` capability exclusively.

The descriptor declares the static feature `authn.invitations`, present whenever
the module is in force; the frontend gates the invitation faces on it.

An invitation is a pending grant of a membership: it names an address and a
tenant and, until it is accepted, no account and no membership row exists. One
module owns the whole of it — the table, the token and both route faces. The
token grants joining; it does not prove the invited address. Acceptance needs a
signed-in account whose own identifiers prove that address, and it writes the
membership through `Memberships`, with `invite` as the membership's source when
the membership is new. It never creates an account; the account rules stay with
the account module.

The API is the package's godoc, plus `Invites`, `Invitation`,
`InvitationStatus`, `Issued`, `IssueInvite`, `AcceptInvite` and the sentinels
declared in this package. The design places this interface and the value types
beside the other authentication seams in the root package; they live here
instead because the root package already declares `Issued` (the session type,
an alias of the model's), which an invitation's `Issued` would collide with.
Nothing in the method set or the value types depends on the choice of package.

## The state machine

The stored states are `InvitationPending` (the zero value), `InvitationAccepted`
and `InvitationRevoked`. Expiry is not a stored state: it is read from the clock
and `ExpiresAt`, so an expired pending invitation is still `InvitationPending`,
and acceptance and listing judge it by the current time. A missing expiry
flip only makes an already unredeemable invitation look redeemable, and
acceptance itself refuses it, so no sweeper is needed.

- **Issue** normalizes the address, draws a 256-bit token under the `spd_inv_`
  prefix, and keeps only its SHA-256. It charges the three send budgets — the
  invited address, the issuing account and the deployment — and refuses an
  issuance past any of them before writing a row. In one transaction it revokes
  a pending invitation for the same tenant and address and inserts the new row.
  The token is handed out once, in the result, and the issue answer carries it
  only when the notice was not sent: a sent notice means the recipient holds the
  link already, and a failed or unconfigured one leaves the inviter to hand it
  over. The issuer is read from the request's identity when there is one; a host
  command with none skips only the issuer budget.
- **Accept** requires an authenticated caller. It finds the row by the token's
  digest, refuses a terminal state, requires the caller's account to hold the
  invited address as a proven email identifier — the address need not be chosen
  as a sign-in way — and then wins the row with a conditional write (`status`
  still pending and not expired). Two concurrent acceptances leave at most one
  winner. The winner writes the membership on the caller's account; an existing
  membership keeps its source and SSO exemption, and a suspended one is a
  conflict whose invitation is revoked. A caller who has not proved the address
  is refused and the row stays pending. A failure after the win is settled by a
  second conditional write: a transient failure returns the row to pending, and
  only a failure no retry can complete revokes it. The token is never treated as
  proof of the address, so the inviter holding it cannot accept on the invitee's
  behalf.
- **Revoke** flips a pending invitation to revoked. Revoking one that was
  already accepted is `ErrInvitationAlreadyAccepted`; revoking one that was
  already revoked is an idempotent success.
- **List** returns a tenant's invitations one page at a time: pending first,
  then by creation time descending and identifier descending. The cursor is
  opaque and carries the sort key of the page's last row. The management read
  narrows the page with its `status` query: `pending` is a pending row whose
  deadline has not passed, `expired` is a pending row whose deadline has passed,
  `accepted` and `revoked` are the terminal stored states, and an absent value
  lists every state. The two pending views share the stored state — expiry is
  read from the clock and `ExpiresAt`, never stored — and a value outside the
  set is refused with 400.

## Routes and the decision

Every management route is decided in the handler by the `Decider` seam with the
action `authn.membership.manage`; the module grants itself no permission. The
routes are registered guarded and impersonable: the action is decided in the
path's tenant, so an impersonating identity is decided by the effective
account's permissions there. Before the decision
and before the mandatory audit, a management write asks the signup gate: the
acting operator's user account whose self-registration is still pending
(`signup_state=1`) gets `403 contact-required`, asks no `Decider` and writes no
row or audit row. The
list is a read and is not gated; a service account and an account that never
self-registered are exempt. The acceptance read is registered public: it shows
what a token points to. The acceptance write is registered optional: it needs a
signed-in caller, and the handler refuses one without an identity, because the
token alone grants a membership no more.

| Face | Method and path | Registration | Authorization |
|---|---|---|---|
| Management | `POST B/admin/tenants/:tenant/invitations` | Guarded, Impersonable | decision + mandatory audit |
| Management | `GET B/admin/tenants/:tenant/invitations` | Guarded, Impersonable | decision for the read; no audit |
| Management | `DELETE B/admin/tenants/:tenant/invitations/:id` | Guarded, Impersonable | decision + mandatory audit |
| Acceptance | `GET B/invitations/:token` | Public | the token |
| Acceptance | `POST B/invitations/:token` | Optional | a signed-in account that proves the invited address; the token |

`B` is the capability's base path, read through the route mounter. A
management route asks the `Decider`; a request with no identity is the
gateway's 401 and a refusal is a 403, while a decider that is absent refuses
every management request. A write lands its mandatory audit row, outside any
transaction and before the action; a row that does not land is a 503 and the
action is not carried out.

**The routes are declared as HTTP operations, and the fragment is generated.**
`operations.go` declares the five operations — method, path relative to the API
root, summary, description, parameters, request bodies and responses — and binds
each handler's input and answer types. Issuing and listing use
`Operation[In, Out]`, so the entry point binds the request and writes the
declared body. Revoking and both acceptance faces use `ReplyOperation[In]`,
because an unknown invitation is a bare 404 that a simple operation's error path
cannot write; their handlers build the declared answers through
`JSONReply`/`EmptyReply`. `openapi/invite.json` is a build product of the same
declarations, generated by `pkg/internal/openapigen`, never written by hand. The
mounter takes each route's path from its declaration, so the fragment and the
registration cannot drift.

Regenerate the fragment after a change to the declarations:

```
UPDATE_FRAGMENTS=1 go -C pkg test ./authn/invite -run TestTheFragmentIsGeneratedFromTheDeclarations
```

Without the variable the same test is the drift gate; `make check` runs it.

## Boundaries

- **The token grants joining; it is not proof of the invited address.** It is
  handed out once, at issue, and only its digest is stored. Revoking is a change
  of state, not the revocation of a signature. The token plaintext must not reach
  a log, an event or a trace attribute; the `spd_inv_` prefix is what a log
  redactor recognises it by. Acceptance requires a signed-in account whose own
  identifiers prove the address, so the inviter, who also holds the token,
  cannot accept on the invitee's behalf.
- **Acceptance reconciles across modules, not one transaction.** The invitation
  row is this module's; the membership is the account module's, in its own
  transaction. A failure after the win is settled by a second
  conditional write, never an unconditional one; the settlement's own failure is
  an `Error` line and changes nothing about the error handed back. The
  correctness floor is the conditional write and the state checks, not the
  settlement completing.
- **One pending invitation per tenant and address**, guaranteed by a partial
  unique index and the superseding write of issue. A concurrent issuance of the
  same pair is `ErrInvitationPending`.
- **The module renders nothing and delivers nothing itself.** After a
  successful management issue it asks the optional `authn.InviteNotifier` seam,
  bounded to five seconds, and reports the outcome beside the one-time link as
  `sent`, `failed` or `not_configured`. An absent notifier is `not_configured`,
  not a failure. A failed notice is logged and closed with a best-effort
  `authn.invitation.deliver` audit row, and the issue is still answered 201 with
  its token. The module does not queue or retry, and it does not import the
  tenant module; a tenant identifier is a string here. Every view, audit row
  and outbound notice names the tenant by its name: the notice the notifier
  renders carries the name, never the identifier the path guard left in the
  request. The optional `authn.TenantNameResolver` answers it, the roster
  delivers it, and an assembly with none names no tenant rather than exposing
  the identifier.
- **The membership source is `invite`**, written into every membership row the
  module creates, so a subscriber of `authn.membership.changed` can tell an
  invitation join from an administrator's direct add. An existing membership is
  not rewritten: an acceptance that finds one keeps its source and SSO
  exemption.
- **A withdrawn member loses the pending invitations addressed to them.** The
  module subscribes to `authn.membership.changed`; on `suspended` or `removed` it
  revokes the pending invitations for the account's email identifiers in that
  tenant, conditional on `status = pending`, so an accepted or already revoked
  row is untouched and a redelivery changes nothing. An acceptance that meets a
  suspended membership revokes its pending row too, so a token the event missed
  cannot be reused.
- **A deleted tenant loses every pending invitation; a disabled one refuses
  acceptance and keeps them.** The module delivers `authn.InvitationRevoker`,
  and the tenant ownership module calls it after a deletion has committed; the
  write is conditional on `status = pending`. Acceptance reads the optional
  `authn.TenantStateResolver`: a deleted tenant answers 404 and its row is
  revoked, a disabled tenant answers 409 `conflict` with `ErrInvitationTenantInactive`
  and the row stays pending, so enabling the tenant makes the same token
  redeemable again. An assembly with no resolver accepts, having no tenant state
  to judge.
- **The membership write is conditional.** Acceptance writes through
  `Memberships.PutJoining`, which leaves the account an active member only when
  it had no membership or its membership is still active. A membership
  suspended or removed between the acceptance's read and its write is not
  resumed: the acceptance answers 409 `membership-inactive` and the invitation
  is revoked.
- **Settling a failed acceptance back to pending never collides with a resend.**
  A resend issued while the acceptance held the old row has already written a
  new pending invitation; the settlement detects it (or a unique-constraint
  collision) and revokes the old row instead of returning it to pending.
- **The key-value store is required.** The send and probe budgets count through
  `kv.KV`; the descriptor declares it in `Requires` and `newProduct` resolves
  it, so an assembly that names this module also names a counter backend. A
  budget that silently disappeared with its dependency would leave the
  invitation faces unbounded.
- **`SignupVerification` is required.** The descriptor declares it in
  `Requires` and `newProduct` resolves it. The management write gate reads
  `SignupStateOf`; acceptance itself reads no pending-contact row.

## Configuration

- **`authn.invite.ttl`** is a setting, declared here: how long an invitation
  lives from the moment it is issued, when the issue call states no deadline.
  The declared default is `168h`, and the domain is one hour to `2160h`. It is
  read at each such issue; with no settings store the declared default applies,
  and a read that fails or lands outside the domain applies the default and
  writes one `Error` naming the key. A longer lifetime is a relaxation, so the
  `settings.changed` subscriber writes a `Warn` and a best-effort
  `authn.settings.relaxed` audit row with direction `increase`, naming the key
  and never the value. The config schema no longer carries the item: a config
  file naming it fails startup as an unknown key.
- **The send budgets are settings.** `authn.invite.issue-window` is the fixed
  window (default `1h`), and `authn.invite.issue-address-limit`,
  `authn.invite.issue-issuer-limit` and `authn.invite.issue-global-limit` are
  the invitations one invited address, one issuing account and the whole
  deployment may send in one window (defaults `5`, `50`, `500`). A resend is
  another issuance and spends the same budgets. The address is counted in its
  canonical inbox form, so a plus suffix or a Gmail dot cannot multiply the
  allowance.
- **The acceptance probe budgets are settings.**
  `authn.invite.probe-window` is the fixed window (default `1m`), and
  `authn.invite.probe-token-limit` and `authn.invite.probe-client-limit` are
  the probes one token and one client address may make in it (defaults `10`,
  `30`). The token key is the token's digest, never the plaintext; an IPv6
  client counts by its /64. The probe is charged before the token is looked up,
  so a spent budget answers the same 429 whether or not the token exists.
- **A spent budget is the platform-standard 429.** The handler answers the
  entry point's throttled problem (`urn:speed:authn:throttled`) with
  `Retry-After` in whole seconds; the response is declared on every route that
  can throttle. A counter backend that cannot be read is a 503
  (`authn.ErrUnavailable`), never a silent pass.
- **Every budget read falls back to its declared default.** With no settings
  store, a failed read or a value outside the domain (a window from `1s` to the
  longest whose double fits a `time.Duration`; a limit of at least `1`), the
  declared default applies and one `Error` names the key.

## Running it

From the repository root, over every module in the workspace:

```
make build
make test
make lint
make check
```

On this module alone:

```
go -C pkg build ./authn/invite/...
go -C pkg vet ./authn/invite/...
go -C pkg test ./authn/invite/...
go -C pkg test -race ./authn/invite/...
env -C pkg golangci-lint run ./authn/invite/...
```

The tests ask for no service and no environment. The state-machine cases run the
product over a real delivered SQLite handle with this module's migrations
applied, with stand-ins for the account reads and writes, the membership writes,
the audit and the decision seam, and a clock a case moves by hand. The route
cases mount the module's whole route set on a `mounttest` Mounter and drive it
over an `httptest` recorder with a real identity, so a case can assert that a
route the decider refused, a request with no identity, and a write whose
mandatory audit did not land reached no store and wrote no row.
