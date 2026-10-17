# pkg/authn/invite

The authentication capability's invitation module. It owns the
`authn_invitations` table, the state machine of an invitation, and the routes of
the invitation's issuance face and its acceptance face. It delivers the
`Invites` capability exclusively.

The descriptor declares the static feature `authn.invitations`, present whenever
the module is in force; the frontend gates the invitation faces on it.

An invitation is a pending grant of a membership: it names an address and a
tenant and, until it is accepted, no account and no membership row exists. One
module owns the whole of it — the table, the token and both route faces. It does
not keep accounts, memberships or passwords: acceptance creates an account
through `AccountAdmin` and writes the membership through `Memberships`, with
`invite` as the membership's source, and those rules stay with the account
module.

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
  prefix, and keeps only its SHA-256. In one transaction it revokes a pending
  invitation for the same tenant and address and inserts the new row. The token
  is handed out once, in the result. The issuer is read from the request's
  identity when there is one.
- **Accept** finds the row by the token's digest, reports a terminal state, and
  wins the row with a conditional write (`status` still pending and not
  expired). Two concurrent acceptances leave at most one winner. The winner
  creates an account when the address has none, then writes the membership. A
  failure after the win is settled by a second conditional write: a transient
  failure returns the row to pending, a permanent one revokes it.
- **Revoke** flips a pending invitation to revoked. Revoking one that was
  already accepted is `ErrInvitationAlreadyAccepted`; revoking one that was
  already revoked is an idempotent success.
- **List** returns a tenant's invitations one page at a time: pending first,
  then by creation time descending and identifier descending. The cursor is
  opaque and carries the sort key of the page's last row.

## Routes and the decision

Every management route is decided in the handler by the `Decider` seam with the
action `authn.membership.manage`; the module grants itself no permission, and
the routes are registered guarded and never impersonable. The acceptance routes
are registered public: the token is the whole credential and there is no
identity to read.

| Face | Method and path | Registration | Authorization |
|---|---|---|---|
| Management | `POST B/admin/tenants/:tenant/invitations` | Guarded | decision + mandatory audit |
| Management | `GET B/admin/tenants/:tenant/invitations` | Guarded | decision for the read; no audit |
| Management | `DELETE B/admin/tenants/:tenant/invitations/:id` | Guarded | decision + mandatory audit |
| Acceptance | `GET B/invitations/:token` | Public | the token |
| Acceptance | `POST B/invitations/:token` | Public | the token |

`B` is the capability's base path, read through the route mounter. A
management route asks the `Decider`; a request with no identity is the
gateway's 401 and a refusal is a 403, while a decider that is absent refuses
every management request. A write lands its mandatory audit row, outside any
transaction and before the action; a row that does not land is a 503 and the
action is not carried out.

## Boundaries

- **The token is the only credential of acceptance.** It is handed out once, at
  issue, and only its digest is stored. Revoking is a change of state, not the
  revocation of a signature. The token plaintext must not reach a log, an event
  or a trace attribute; the `spd_inv_` prefix is what a log redactor recognises
  it by.
- **Acceptance reconciles across modules, not one transaction.** The invitation
  row is this module's; the account and the membership are the account module's,
  in its own transaction. A failure after the win is settled by a second
  conditional write, never an unconditional one; the settlement's own failure is
  an `Error` line and changes nothing about the error handed back. The
  correctness floor is the conditional write and the state checks, not the
  settlement completing.
- **One pending invitation per tenant and address**, guaranteed by a partial
  unique index and the superseding write of issue. A concurrent issuance of the
  same pair is `ErrInvitationPending`.
- **The module does not deliver, queue or retry.** It hands the acceptance link
  to the issuer once and knows nothing of delivery. It does not import the
  tenant module; a tenant identifier is a string here.
- **The membership source is `invite`**, written into every membership row the
  module creates, so a subscriber of `authn.membership.changed` can tell an
  invitation join from an administrator's direct add.

## Configuration

- **`authn.invite.ttl`** (a duration, primary source only): how long an
  invitation lives from the moment it is issued, when the issue call states no
  deadline. The declared default is `168h`, and the domain is one hour to
  `2160h`; a value outside it is `authn.ErrInvalidConfig` at startup.

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
