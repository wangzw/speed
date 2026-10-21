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
- **The engine is method-agnostic and drives factors through `MFA`.** It never
  names a method's own step, reads only the `Step` kind and its payload, and
  reaches a factor through `authn.MFA` and never through `Factor`. `MFA` is
  required: an assembly with a register holding factors and no module here would
  let a login skip the second factor without a word.
- **A terminal action re-checks the session it was started on.** `link`,
  `step-up` and `enroll` complete on the `Open` answer route, which the gateway
  did not guard, so `review` re-reads `StatusFresh`, checks the account matches
  the record and refuses an impersonating identity; `link` and `enroll` also
  re-check the account's freshness requirement before acting. A `login` that
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
  engine; the audit row's `identifier_mac` is the one identifier shape that is
  written, and it is a MAC.
- **The one place an identity is written rather than read is the audit row of a
  completed login.** The engine derives a context carrying the account the login
  just proved and hands it to the audit record alone; the derived context reaches
  no handler and no later step, so it does not flow back into the request chain.
- **This package imports no first-factor leaf package and no package of the tenant
  tree.** It depends on the root package, its own inner packages and the
  capability interfaces only. A tenant is never read here: the login request
  carries no tenant name, and the account is global.
- **The account module's relaxation switches are read by path, never declared
  here.** `authn.account.signup` and `authn.account.auto-link` belong to
  `pkg/authn/account`; this package reads them through its optional settings
  store on every methods request and every account resolution, and a read error
  reads as the strict value (`closed`, `never`). Declaring the same key here is
  the shape to reject: two modules must not declare one path.
- **The routes are mounted through `mount.Mounter` with their access level in one
  call, and the callback bypasses the cross-origin check.** The base path is
  `authn.gate.mount.base-path` on the endpoint `authn.gate.mount.endpoint`; the
  flow cookie is `SameSite=None` so that a SAML POST navigation still carries it,
  and the `__Host-` prefix is dropped only under `allow-insecure`.
- **A setting moved toward more lenient is visible after the fact, never
  blocked.** The subscription to `settings.changed` writes a `Warn` and a
  best-effort `authn.settings.relaxed` audit row naming the key and the
  direction and no value; the settings it watches are `flow-ttl` in the
  `increase` direction, `disabled-methods` in `remove-member`, `delivery-modes`
  in `add-member` and `ip-limit` in `to-zero` (`relax.go`,
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
