# pkg/authn/invite

The authentication capability's invitation module. It owns the
`authn_invitations` table, the state machine of an invitation, and the routes of
the invitation's issuance face and its acceptance face. It delivers the
`Invites` capability exclusively.

The descriptor declares the static feature `authn.invitations`, present whenever
the module is in force; the frontend gates the invitation faces on it.

An invitation is a pending grant of a membership: it names an address and a
tenant and, until it is accepted, no account and no membership row exists. It may
also name one role, which the acceptance binds beside the membership. One module
owns the whole of it — the table, the token and both route faces. The token
grants joining; it does not prove the invited address. Acceptance needs a
signed-in account whose own identifiers prove that address, and it writes the
membership and the role binding in one transaction, with `invite` as the
membership's source when the membership is new. It never creates an account; the
account rules stay with the account module.

The API is the package's godoc, plus `Invites`, `Invitation`,
`InvitationStatus`, `Issued`, `Accepted`, `IssueInvite`, `AcceptInvite` and the
sentinels declared in this package. The design places this interface and the value types
beside the other authentication seams in the root package; they live here
instead because the root package already declares `Issued` (the session type,
an alias of the model's), which an invitation's `Issued` would collide with.
Nothing in the method set or the value types depends on the choice of package.

`issue.go` holds issuance and `eligibility.go` its target checks; `resend.go`
holds the in-place token replacement; `query.go` holds listing and revocation;
`accept.go` holds acceptance and the proved-address check; `accept_http.go` and
`eligibility_http.go` hold the public read, the guarded eligibility read and the
acceptance; `admin.go` holds the management handlers; `operations.go` declares
the routes, and `views.go` builds the answers, including the masked address.

## The state machine

The stored states are `InvitationPending` (the zero value), `InvitationAccepted`
and `InvitationRevoked`. Expiry is not a stored state: it is read from the clock
and `ExpiresAt`, or from a non-null `superseded_at`, so a superseded or expired
pending invitation is still `InvitationPending`, and listing and the public read
judge it expired. A missing expiry
flip only makes an already unredeemable invitation look redeemable, and
acceptance itself refuses an expired row, so no sweeper is needed.

- **Issue** normalizes the address, draws a 256-bit token under the `spd_inv_`
  prefix, and keeps only its SHA-256. It charges the issuing account's send
  budget first, then judges the target, and only after every check has passed
  charges the invited address's and the deployment's budgets: an attempt the
  checks refuse costs the operator but not the address, and a charge whose row is
  not written is given back. An issuance past any budget is refused before a row
  is written. The target is judged: a service
  account's address and the system tenant are
  refused (`invitation-target-invalid`), an address that is already a member of
  the tenant, active or suspended, is refused (`invitation-member`), an address
  with an unexpired pending invitation is refused (`invitation-pending`), and an
  explicit deadline past 90 days from issuance is refused
  (`invitation-expiry-too-long`). A non-empty role is judged by
  `authn.InviteRolePolicy.Grantable` before any row is written: an absent role
  is `authn.ErrRoleUnknown`, a caller without the tenant's role-management
  permission is `authn.ErrForbidden`, and the platform administrator, a
  permission the caller does not hold, or the host write guard's refusal are
  `authn.ErrRoleProtected`; an assembly with no such policy refuses the
  issuance rather than writing an unchecked role. In one transaction it supersedes an expired
  pending invitation for the same tenant and address — the old row keeps its
  stored pending state so it still reads expired, and `superseded_at` records
  the replacement — and inserts the new row. The first issuance counts as one
  send and writes `last_sent_at` and `send_count = 1`. The token is handed out
  once, in the result, and the issue answer carries it only when the notice was
  not sent: a sent notice means the recipient holds the link already, and a
  failed or unconfigured one leaves the inviter to hand it over. The issuer
  budget is keyed by the acting account of the request; a host command with no
  identity skips it. The invitation's issuer field is the effective account
  instead, so an invitation issued during an impersonation names the
  impersonated account, while the audit keeps the operator. The host exemption
  is decided by the absence of an identity, not by an empty account: a request
  identity that carries no account is refused, because recording it as an empty
  issuer would make the invitation read as a host command and skip the inviter
  checks at acceptance.
- **Resend** sends the same row again: the invitation keeps its identifier and
  its deadline, and only its token digest, `last_sent_at`, `send_count + 1` and
  `updated_at` change. The old token stops resolving at
  once. It is a management action with the issue route's admission order — a
  signed-in caller, the signup gate, the `authn.membership.manage` decision with
  the invitation identifier as its target, the mandatory `authn.invitation.resend`
  audit row on that identifier, then the issuer budget before the terminal check
  and the address and deployment budgets after it. Only a
  pending invitation that has not expired and that no later invitation
  superseded can be resent; a terminal one is
  `invitation-terminal` and an identifier naming no invitation of the tenant is a
  bare 404. A resend re-judges no target: the address was judged at issuance, so
  a membership or a system tenant appearing later does not refuse the resend. The
  notice and the one-time link follow the issue answer.
- **Accept** requires an authenticated user account. A caller that is not a user
  account is refused with 403 `invitation-target-invalid`: an invitation grants
  a membership to a person, and a service account signs in with a service secret
  and has no acceptable membership, while a subject with no account row (a
  static password-table subject) has no account to grant it to. A read of the
  caller's account that fails answers 503 and leaves the invitation pending. It
  finds the row by the token's digest, refuses a
  terminal state, requires the caller's account to hold the invited address as
  a proven email identifier — the address need not be chosen as a sign-in way —
  and re-judges the role the invitation carries, when it carries one, before
  opening its transaction: the role must still exist and, for an invitation an
  account issued, the inviter must still be an enabled account, still be an
  active member of the tenant, still hold the tenant's role-management
  permission, and still hold every permission the role carries. An inviter whose
  account is disabled or no longer exists withdraws the invitation the same way;
  a read of the inviter's account that fails is not a decision about the role,
  so the invitation stays pending and the acceptance answers 503. A static
  subject (`static.` prefix, an account of the deployment's credential table)
  skips the account-state read but still goes through the role review: its
  permissions come from the deployment configuration, so removing it means
  changing the configuration and restarting, and judging it by the account store
  would withdraw every invitation it issued. A role that can no longer be
  granted revokes the invitation, with the `invitation-role-withdrawn` audit
  reason, and answers the terminal refusal; a policy that cannot answer leaves
  the invitation pending. A settlement's audit row is written even when the
  tenant name cannot be read, with the tenant identifier in its place. The tradeoff of the static exemption is that a static
  subject removed from the configuration can still have its pending invitations
  grant their role until they expire or an administrator revokes them; the demo's
  `(*tenancyProduct).IsMember` answers true for any `static.` subject for the same
  reason. The acceptance then wins the row and writes the
  membership and the role binding in one transaction: a conditional write
  (`status` still pending, not expired, not superseded by a later invitation,
  and `token_digest` still the presented token's), the account module's
  transaction-aware join, and the policy module's transaction-aware binding. The digest in the condition is what keeps a resend
  issued mid-flight intact: the replaced token no longer resolves, and the
  acceptance answers no invitation instead of redeeming the row the resend
  refreshed. Two concurrent acceptances leave at most one winner. Any of the
  three writes failing rolls all three back: the invitation stays pending and
  neither the membership nor the binding is written. An existing active
  membership keeps its source and SSO exemption and still receives the role; a
  suspended one is refused and the invitation is revoked. Before the write, the
  acceptance lands one mandatory `authn.invitation.accept` audit row naming the
  tenant, the role when there is one and the inviter; a row that cannot be
  written means the acceptance does not happen. The token is never treated as
  proof of the address, so the inviter holding it cannot accept on the invitee's
  behalf.
- **Revoke** flips a pending invitation that has not expired and that no later
  invitation superseded to revoked and
  writes `revoked_at`. Revoking one that is expired or already accepted is
  `invitation-terminal`; revoking one that was already revoked is an idempotent
  success and leaves its revocation time alone. Every revocation path writes
  `revoked_at`: the management route, a member's suspension or removal, an
  organization's deletion, an acceptance that meets a suspended membership, and
  an acceptance whose role can no longer be granted.
- **List** returns a tenant's invitations one page at a time in four groups:
  unexpired pending, expired pending, accepted, revoked; each group is ordered
  by creation time descending with the identifier as the tie-break. The cursor
  is opaque and carries the group and sort key of the page's last row. A group
  is judged from the clock, the deadline and the superseded mark, so a row that
  crosses its deadline or is superseded between two pages can change group and
  be repeated or skipped at the boundary. The management
  read narrows the page with its `status` query: `pending` is a pending row
  that no later invitation superseded and whose deadline has not passed,
  `expired` is a pending row that a later invitation superseded or whose
  deadline has passed, `accepted` and `revoked` are the terminal stored states,
  and an absent value lists every state. The two pending views share the stored
  state — expiry is read from the clock, `ExpiresAt` and `superseded_at`, never
  stored — and a value outside the set is refused with 400.
- **The management view carries `state` and the inviter's names.** `state` is
  the four-state judgement (`pending`, `expired`, `accepted`, `revoked`) on the
  server's clock, the same judgement the listing uses; `status` remains the
  stored three-state column. `invited_by_name` and `invited_by_username` are
  resolved from `invited_by` and are absent, not empty, when a host command
  issued the invitation, the account is a tombstone, or the account has no
  username. A read that cannot answer also omits them and is logged instead of
  failing the answer: on an issue or resend the invitation and its notice are
  already settled, so the answer still carries the one-time token, and a
  listing still returns its whole page. During an impersonation `invited_by` is
  the effective (impersonated) account, so the names are that account's; the
  audit keeps the operator.
  `last_sent_at` is always a moment: issuance writes it.

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
what a token points to, with the invited address masked on the server so the
person it was sent to recognizes it without the link disclosing the whole
address, and with the tenant's and the inviter's display names instead of their
identifiers; an accepted link shows the organization's name and display name
and no address, and an expired or revoked one shows its state alone. The
acceptance write is registered optional: it needs a signed-in caller, and the
handler refuses one without an identity, because the token alone grants a
membership no more. The eligibility read is registered guarded: it answers, for
the signed-in caller alone, the invitation's state and whether that account has
proved the invited address, so a client can show the proof step on arrival; it
carries no address and charges the same probe budget as the public read.

| Face | Method and path | Registration | Authorization |
|---|---|---|---|
| Management | `POST /api/v1/tenants/{tenant}/invitations` | Guarded, Impersonable | decision + mandatory audit |
| Management | `GET /api/v1/tenants/{tenant}/invitations` | Guarded, Impersonable | decision for the read; no audit |
| Management | `DELETE /api/v1/tenants/{tenant}/invitations/{id}` | Guarded, Impersonable | decision + mandatory audit |
| Management | `POST /api/v1/tenants/{tenant}/invitations/{id}/resend` | Guarded, Impersonable | decision + mandatory audit |
| Acceptance | `GET /api/v1/auth/invitations/{token}` | Public | the token |
| Acceptance | `POST /api/v1/auth/invitations/{token}` | Optional | a signed-in account that proves the invited address; the token |
| Acceptance | `GET /api/v1/auth/invitations/{token}/eligibility` | Guarded | a signed-in account; the answer is about the caller alone |

A management route asks the `Decider`; a request with no identity is the
gateway's 401 and a refusal is a 403, while a decider that is absent refuses
every management request. A write lands its mandatory audit row, outside any
transaction and before the action; a row that does not land is a 503 and the
action is not carried out.

**The routes are declared as HTTP operations, and the fragment is generated.**
`operations.go` declares the seven operations — method, path relative to the API
root, summary, description, parameters, request bodies and responses — and binds
each handler's input and answer types. Issuing and listing use
`Operation[In, Out]`, so the entry point binds the request and writes the
declared body. Revoking, resending and every acceptance face use `ReplyOperation[In]`,
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
- **Acceptance is one transaction across three modules.** This module owns the
  invitation row and opens the transaction; the membership is written through
  the account module's transaction-aware join and the role binding through the
  policy module's transaction-aware write, both on the handle the acceptance
  hands them. The three commit together or not at all: a failed write rolls the
  claim back with it, so the invitation stays pending and neither the membership
  nor the binding exists. The judgement that needs another connection — the role
  review — runs before the transaction opens, because the single-writer database
  would otherwise wait on a transaction it is not part of. There is no
  settlement path after the transaction.
- **One live invitation per tenant and address**, guaranteed by the partial
  unique index over pending rows whose `superseded_at` is null. A second
  issuance to an unexpired pending address is refused before writing; an expired
  one is superseded, and its row keeps reading expired. A concurrent issuance of
  the same pair is `ErrInvitationPending`, answered 409 `invitation-pending`.
- **The module renders nothing and delivers nothing itself.** After a
  successful management issue or resend it asks the optional
  `authn.InviteNotifier` seam, bounded to five seconds, and reports the outcome
  beside the one-time link as `sent`, `failed` or `not_configured`. An absent
  notifier is `not_configured`, not a failure. A failed notice is logged and
  closed with a best-effort `authn.invitation.deliver` audit row, and the write is
  still answered with its token (201 for an issue, 200 for a resend). The module does not queue or retry, and it does not import the
  tenant module; a tenant identifier is a string here. Every view, audit row
  and outbound notice names the tenant by its name: the notice the notifier
  renders carries the name, never the identifier the path guard left in the
  request. The optional `authn.TenantNameResolver` answers the name an address
  surface uses. The required `authn.TenantDescriptionResolver` answers the
  name, the display name and whether the tenant is the system tenant in one
  read; the roster delivers it, and the module declares it in `Requires`, so an
  assembly without it fails the startup rather than serving a nameless view.
  Issuing refuses the system tenant through the description, and a public link
  shows the organization's display name. The outbound notice also names the
  inviter by the display name read from the issuing account, and carries none
  when a host command issued the invitation without an account.
- **The membership source is `invite`**, written into every membership row the
  module creates, so a subscriber of `authn.membership.changed` can tell an
  invitation join from an administrator's direct add. An existing membership is
  not rewritten: an acceptance that finds one keeps its source and SSO
  exemption.
- **A withdrawn member loses the pending invitations addressed to them.** The
  module subscribes to `authn.membership.changed`; on `suspended` or `removed` it
  revokes the pending invitations for the account's email identifiers in that
  tenant, conditional on `status = pending` and `expires_at > now`, so an
  accepted, already revoked or expired row is untouched and a redelivery changes
  nothing. An acceptance that meets a
  suspended membership revokes its pending row too, so a token the event missed
  cannot be reused.
- **A deleted tenant loses every pending invitation; a disabled one refuses
  acceptance and keeps them.** The module delivers `authn.InvitationRevoker`,
  and the tenant ownership module calls it after a deletion has committed; the
  write is conditional on `status = pending` and `expires_at > now`, so an
  expired row keeps reading expired. Acceptance reads the optional
  `authn.TenantStateResolver`: a deleted tenant answers 404 and its row is
  revoked, a disabled tenant answers 409 `invitation-tenant-inactive` with `ErrInvitationTenantInactive`
  and the row stays pending, so enabling the tenant makes the same token
  redeemable again. An assembly with no resolver accepts, having no tenant state
  to judge.
- **The membership write is conditional.** Acceptance writes through
  `provisioning.MembershipWriter.PutJoiningTx`, which leaves the account an
  active member only when it had no membership or its membership is still
  active. A membership suspended or removed between the acceptance's read and
  its write is not resumed: the transaction rolls back, the acceptance answers
  409 `membership-inactive`, and the invitation is revoked.
- **A role is judged, never owned.** The invitation stores the role name only;
  whether an account may grant it, whether it still exists and whether the
  inviter still holds what it carries are the `authn.InviteRolePolicy` seam's
  answers, implemented and delivered by `authz.policy`. The binding is written
  through `provisioning.RoleBinder`, also delivered by `authz.policy`. This
  module imports no package of the authorization capability; an assembly with no
  such policy issues and accepts role-less invitations as before, and fails
  closed on one that carries a role.
- **Every acceptance writes one mandatory audit row.** `authn.invitation.accept`
  is written before the transaction, with the tenant, the role when there is one
  and the inviter; a row that does not land means the acceptance is not carried
  out. The issue audit names the role too.
- **The key-value store is required.** The send and probe budgets count through
  `kv.KV`; the descriptor declares it in `Requires` and `newProduct` resolves
  it, so an assembly that names this module also names a counter backend. A
  budget that silently disappeared with its dependency would leave the
  invitation faces unbounded.
- **`SignupVerification` is required.** The descriptor declares it in
  `Requires` and `newProduct` resolves it. The management write gate reads
  `SignupStateOf`; acceptance itself reads no pending-contact row.
- **The role and transaction seams are optional and fail closed when needed.**
  `wire` takes up `authn.InviteRolePolicy`, `provisioning.MembershipWriter` and
  `provisioning.RoleBinder`; none is declared in `Requires`, because an
  assembly that only issues, lists, revokes and resends invitations is legal.
  `InviteRolePolicy` and `RoleBinder` are needed only when an invitation carries
  a role; every acceptance writes the membership through `MembershipWriter`, so
  an assembly without it answers 503 on acceptance while the rest of the surface
  stays available. A needed capability that is absent refuses the request with
  503 — never carried out with a check skipped.

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
  deployment may send in one window (defaults `5`, `50`, `500`). A resend spends
  the same budgets but is not a new issuance: it re-judges no target. The issuing
  account's budget is charged before the target checks; the address's and the
  deployment's are charged only after every check has passed, and a charge whose
  row is not written is given back, so an attempt that writes nothing does not
  spend an address's allowance. The address is counted in its
  canonical inbox form, so a plus suffix or a Gmail dot cannot multiply the
  allowance.
- **The acceptance probe budgets are settings.**
  `authn.invite.probe-window` is the fixed window (default `1m`), and
  `authn.invite.probe-token-limit` and `authn.invite.probe-client-limit` are
  the probes one token and one client address may make in it (defaults `10`,
  `30`). The token key is the token's digest, never the plaintext; an IPv6
  client counts by its /64. The probe is charged before the token is looked up,
  so a spent budget answers the same 429 whether or not the token exists. The
  public acceptance read and the guarded eligibility read charge the same two
  budgets, so the authenticated read cannot walk tokens faster than the public
  one allows.
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
