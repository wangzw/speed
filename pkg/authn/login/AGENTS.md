# pkg/authn/login

The login engine of the authentication capability, registered as `authn.login`.
It holds a login: one record in the key-value store advanced one
compare-and-swap per step, the account resolution that turns a `Proof` into an
account, the second-factor orchestration, and the terminal actions that make a
session or issue a token. It delivers no capability — it is a process and a set
of routes — and takes every first-factor method with `core.ResolveAll`.

The descriptor declares the conditional features `authn.phone` and
`authn.identifiers.email`. `Mount` posts each in the judgement that mounts the
phone routes or switches on the email branch, during `Init`, and skips the post
when the process has no feature board.

The kinds of first factor are not here. Each is a leaf package that delivers
`authn.LoginMethod` beside the others; this package imports none of them, and a
command that names this module and delivers no method fails at startup with
`authn.ErrNoLoginMethod`, as two methods of one name do with
`authn.ErrMethodConflict`.

Three inner packages carry the parts that would otherwise crowd this one.
`login/internal/resolve` resolves a `Proof` into an account — opening an account
at signup, linking or merging an external identity — and answers one `Result` the
engine lands on the flow. `login/internal/selfservice` mounts the account's own
routes: the password and phone writes, adding or deleting a login identifier
(a username in one step, an email only once its code comes back over the optional
`authn.EmailCodeSender` channel), and unbinding an external identity.
`login/internal/stepup` owns the one reading of the `WWW-Authenticate` value a
step-up refusal carries, because a typed handler returns a fault and has no
response writer. This package builds the first two and calls them, and uses
`stepup` for its own refusals; none is reachable from another module.

The anonymous register route `POST /auth/register` is the one place this package
takes part in opening an account rather than reading one, and it opens none
itself: `authn.Signups.Register` owns the one transaction, this route owns the
request's shape and the two budgets in front of it.

The API is the package's godoc. Read `Module`'s comment before changing the
descriptor and `migrateFirst`/`resolveAndDecide` before changing what a state
does.

## Boundaries

- **The store's atomicity is the concurrency defence, and the engine adds
  none.** Every migration is a read of the old bytes and a `CompareAndSwap`; the
  loser is answered `flow-invalid` and never retried. A callback is moved to
  `processing` *before* the method is asked, so an authorization code or an
  assertion is handled at most once; a record left in `processing` by a crash is
  dead and the user starts again. A terminal action swaps the record to the
  `done` tombstone first and acts only as the winner, so a login completes at
  most once. A tombstone lives as long as the record; the swap never moves the
  expiry.
- **Abandoning a flow releases the state its owner kept, and removes the record
  whatever happens.** `DELETE flows/current` hands the flow's private state to
  the collaborator that owns it, when that collaborator implements the optional
  `authn.MethodAbandoner`: the multi-factor module for an enrollment, the named
  method for a flow at its first factor. A second-factor flow's private state
  belongs to a factor and a settled flow has spent its own, so neither has an
  owner here. The call runs under the call budget; a failure or a panic is
  logged and does not keep the record, because abandoning is idempotent and a
  record left behind would hold a binding the client has given up
  (`releaseFlow`, `abandonOwner`,
  `TestAbandonHandsTheFlowsPrivateStateToItsModule`,
  `TestAbandonHandsALoginMethodsFlowToTheMethod`,
  `TestAFailedAbandonCallbackStillRemovesTheFlowAndIsLogged`).
- **The engine is method-agnostic and drives factors through `MFA`.** It never
  names a method's own step, reads only the `Step` kind and its payload, and
  reaches a factor through `authn.MFA` and never through `Factor`. `MFA` is
  required: an assembly with a register holding factors and no module here would
  let a login skip the second factor without a word.
- **A terminal action re-checks the session it was started on.** `link`,
  `step-up` and `enroll` complete on the `Open` answer route, which the gateway
  did not guard, so `review` re-reads `StatusFresh` and checks that the session
  still belongs to the flow: the flow's account, or, for a flow an impersonating
  identity started, the operator's account and the same record. Such a flow also
  asks `Impersonation.Active` that the record still holds for the flow's actor
  and account, because a record that lapses without `End` leaves its marker on
  the session; an assembly without that capability refuses the completion.
  `link` and `enroll` also re-check the account's freshness requirement before
  acting. A flow an impersonating identity started stays bound to the effective
  account while its steps prove the operator's own factors. A `login` that
  resolved through an automatic merge runs the account's cleanup — revoke every
  session, then delete the password — *before* the new session is created. A
  `login` whose proof replaced the account's password
  (`Proof.PasswordChanged`) revokes the account's other sessions once the new
  one exists.
- **The direction of a failed read is a refusal or an outage, never a pass.** A
  store that cannot be read is `Unavailable`; a `flow-invalid` covers a missing
  binding, a wrong header, a step out of order and a lost compare-and-swap. The
  back-off is read before a credential is derived, and a back-off that cannot be
  read lets no attempt through. A private key in the flow record is treated as a
  credential: the `state` and PKCE verifier in `method_state` are secrets the
  record holds.
- **No token, cookie value, password, secret or identifier value reaches a log
  line, an error text, an event or a metric attribute.** The flow token is stored
  as its SHA-256 and compared in constant time; `method_state` is opaque to the
  engine; this implementation writes no identifier value, including
  `identifier_mac`.
- **An identity is derived only for the audit rows a flow writes.** The flow's
  routes are public, so the request carries no identity; the engine derives one
  from the record for the login's audit row, a completed link's best-effort row,
  an impersonating enrollment's terminal row, and the row of a registration that
  opened an account. The derived context is handed to that one record and to no
  handler and no later step, so it does not flow back into the request chain. An
  impersonating flow's derived identity is centred on the effective account and
  carries the operator as its actor and the grant's scope and session, so the
  journal writes the row's on_behalf_of, impersonation_scope and session from
  them.
- **This package imports no first-factor leaf package and no package of the tenant
  tree.** It depends on the root package, its own inner packages and the
  capability interfaces only. A tenant is never read here: the login request
  carries no tenant name, and the account is global.
- **The account module's relaxation switches are read by path, never declared
  here.** `authn.account.signup` and `authn.account.password-signup` belong to
  `pkg/authn/account`; this package reads them through its optional settings
  store at each use — both signup switches on every methods request, the
  password one again on every registration, and `signup` at each account
  resolution — and a read error reads as the strict value (`closed`). The two
  registration budgets
  (`authn.account.signup-ip-limit`, `authn.account.signup-global-hourly`) are read
  the same way, into their declared domains. Declaring the same key here is the
  shape to reject: two modules must not declare one path.
- **The register route opens an account through `Signups.Register` and counts
  before it.** The switch is checked here first only so a closed deployment
  refuses before any count; `Signups.Register` reads it again as the authority and
  answers the same `403 signup-closed`. The per-source minute bucket and the
  deployment-wide hour bucket are key-value increments; both answer `429
  throttled` with a `Retry-After` that ends with the bucket. A source bucket that
  cannot be written is skipped with a `Warn` — it is a coarse pre-throttle, the
  same direction as the login route's `ip-limit` — while the deployment-wide
  bucket that cannot be written answers `503`, because it is the only bound on an
  open registration. The route reads no cookie, keeps no flow record, makes no
  session and sends no email.
- **The email branch declares its budgets and refuses an address another account
  holds.** `authn.login.email-per-address-hourly`,
  `email-per-address-daily`, `email-per-account-hourly`,
  `email-per-account-daily`, `email-per-source-hourly`,
  `email-per-source-daily` and `email-global-hourly` are declared here
  (`config.go`) and read by the self-service package through the settings store;
  the code lifetime and resend interval are declared the same way. Each send is
  one atomic increment in a fixed window of its own — an hour, or a fixed UTC day
  for the daily dimensions — and a count past its limit is a 429 `throttled` with
  the `Retry-After` that ends that window. A counter that cannot be advanced is
  a 503 and no send: the budgets are the only bound on messages to an address,
  so none of them is skipped. The source dimensions are applied only when the
  request carries an address; an empty source is fail-open. The increments are
  not one atomic step, so the branch reserves before it sends: a refusal
  gives back every count this request took, and a send failure gives them back
  only when the channel certifies that no message left (`CertifiesNotSent`); an
  uncertain failure keeps them. Refunds read and compare-and-swap, never
  increment negatively, so an expired window cannot be recreated with a free
  slot. Address keys and the resend key are built from the budget form of the
  address (`internal/budgetalias`): the `+` suffix is dropped for every domain,
  and `gmail.com`/`googlemail.com` fold dots and become one domain. This form
  changes no identifier semantics — `normalizeEmail`, `Accounts.Lookup`,
  `PendingAccountByContact`, `AddIdentifier`, the pending record and the address
  actually mailed all keep the accepted address. Before any window is claimed the
  branch reads `Accounts.Lookup` and
  `SignupVerification.PendingAccountByContact`; an address another account holds
  as a proved identifier or as an unproved reservation is answered
  `409 email-taken`, with no message, no resend window and no budget spent. A
  read that fails is a 503, never "nobody holds it". The verify route answers a
  missing record under the same login-failed code as a wrong code; a conflict at
  the identifier write, where another account took the address after the code
  went out, is `409 email-taken` as well. Proving an address the account already
  holds as an unproved identifier brings that row up to proved in place, so
  re-adding the account's own address after its code comes back succeeds rather
  than conflicting.
- **A verification-message send presents the deployment's human-verification
  proof first.** `authn.HumanVerifier` is optional in `Requires` and resolved in
  `Init`; an assembly without one, or with the off mechanism, sends without
  asking for a proof. When the gate requires one, the engine checks a non-empty
  `human_proof` before every send and before any send budget is spent: a start
  whose method descriptor declares a phone field and whose submission supplies
  it, the authenticated start for the same case, a first-factor answer that
  carries it, the SMS second-factor challenge (the kind read from the offered
  factor), the phone self-service route, and the email branch after the
  address-taken check and before its resend window. The decision comes from the
  method descriptor or the factor kind, never from a method name. The field is
  capped at 8 KiB, and a longer value is a 400 before a provider is called. The
  seam's failures map to 403 `human-verification-required` for a missing,
  invalid, expired or used proof, 400 for an over-long proof the provider admits,
  and 503 for a provider or store that cannot answer; none of the three sends.
  Registration is not gated, because it sends nothing. The discovery document
  reports the gate as `human` with `required`, `provider` and, for a mechanism
  that needs one, `site_key`; no seam and the off mechanism both read
  `provider: "off"`.
- **Removing a login identifier is refused when it would strand a password
  account.** `DELETE /auth/identifiers/:id` maps the account module's
  `ErrUsernameImmutable` to 409 `username-immutable`: a username is never
  removable, and that refusal comes before every other rule. It maps
  `ErrIdentifierRequired` to 409 `login-identifier-required`: an account holding a
  password credential keeps its last chosen proved email address, a rule that
  under the normal write surface only reaches a row a write around the surface
  left without a username. Removing a phone number never triggers that rule, and
  a removal that would leave the account with no password, no proved phone number
  chosen as a sign-in way and no external identity is `ErrLastLoginMethod`,
  answered 409 `last-login-method`. `DELETE /links/:id` answers the same code for
  the same last-way rule. The identifier is checked against the caller's own
  before the write, and the route revokes no session and publishes no event of
  its own.
- **The account module's signup verification read is required beside `Signups`.**
  `authn.SignupVerification` is declared in `Requires` and resolved in `New`, so an
  assembly that names this module supplies the account module that delivers it,
  which is also where `Signups`, `Accounts` and `AccountAdmin` come from. The
  `purpose=link` gate that refuses an account whose email address has not been
  proved reads it: a link answers `403 contact-required` before any flow record is
  written, `step-up` and `enroll` are not gated, and a read that fails is a 503.
  An account with no email address at all reads none and is not gated, and a
  pending account still signs in: only its non-verification actions are
  restricted.
- **A registration's 409s are `username-taken` and `email-taken`, not
  `conflict`.** Only `Signups.Register`'s `ErrAccountExists` produces the first.
  The email address is reserved by the transaction that opens the account, and a
  reservation another account holds is `ErrContactTaken`, answered
  `409 email-taken`.
- **The discovery document publishes the two registration switches separately.**
  `signup.enabled` is true when either `authn.account.signup` or
  `authn.account.password-signup` is open, and `signup.password.enabled` reports
  the register route's own switch. A method's `opens_account` is cleared when
  `authn.account.signup` is closed; the password method never carries
  `opens_account`, because it never opens an account.
- **The routes are mounted through `mount.Mounter` with their access level in one
  call, and the callback bypasses the cross-origin check.** The base path is
  `authn.gate.mount.bases.default` on the endpoint
  `authn.gate.mount.endpoints.default`; the flow cookie is `SameSite=None` so
  that a SAML POST navigation still carries it, and the `__Host-` prefix is
  dropped only under `allow-insecure`.
- **A setting moved toward more lenient is visible after the fact, never
  blocked.** The subscription to `settings.changed` writes a `Warn` and a
  best-effort `authn.settings.relaxed` audit row naming the key and the
  direction and no value; the settings it watches are `flow-ttl` in the
  `increase` direction, `disabled-methods` in `remove-member` (with the password
  method filtered out of both sides, since no deployment turns it off),
  `delivery-modes` in `add-member` and `ip-limit` in `to-zero` (`relax.go`,
  `TestARelaxingChangeWritesAWarnAndAnAuditRow`).

## How to run it

From the module directory:

```
env -C pkg go build ./authn/login/...
env -C pkg go vet ./authn/login/...
env -C pkg go test ./authn/login/... ./authn/login/password/...
env -C pkg go test -race ./authn/login/...
env -C pkg golangci-lint run ./authn/login/...
```

Run `golangci-lint` from `pkg`, never from the workspace root with an absolute
path: run from outside the module it loads no package and still exits 0.

`rig_test.go` builds the product over fakes — the key-value store, the methods,
`MFA`, the sessions, the accounts and the admin — each scripted per case, and
`mounttest.Mounter` mounts the routes. `e2e_test.go` drives the real modules
through a real registry to a serving entry point on a loopback port: the account,
session, mfa, login, password, gate and mount modules with SQLite, an in-memory
key-value store and an in-memory event bus. The loopback cases need no external
service.
