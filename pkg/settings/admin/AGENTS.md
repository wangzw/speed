# pkg/settings/admin

The settings capability's management module. It mounts the setting read and write
routes under `/api/v1/system/settings` and delivers no capability of its own: every
route's action is a call on the settings surface, reached through the
`settings.Store` capability, and the module holds no setting, no rule and no data
of its own.

The descriptor declares the static feature `settings.admin`, present whenever the
module is in force.

The settings module owns what a setting item is — its declared type, its value
domain, its default and the three-level fallback — so this module only decides
who may ask and how the answer is shaped. It carries no policy rule: it asks the
decision of the handler-internal `authz.guard` API, refuses a write from an
account whose self-registration has not proved an email address, and maps the
settings module's refusals to responses. It also masks a sensitive item's value
before it reaches a client, which the settings module does not do on the read
path.

Taken in only by a command that wants the management routes, it is absent from
every other command: without it the settings are still read and written through
`settings.Store` from the host's own code or its own commands, and the host
carries their access control.

The API is the package's godoc, plus the `Store`, `Layer`, `Global`, `ForTenant`,
`Write`, `Entry` and `DeclaredItem` types and the `ErrUnknownKey`,
`ErrTypeMismatch`, `ErrInvalidValue` and `ErrNotLoaded` sentinels in
`pkg/settings`.

## Routes and the permissions they are held to

Every route is mounted under the base path `/api/v1/system`, on the endpoints
the module's configuration lists. A route is addressed by a natural key — a
setting item's namespace and the rest of the key — so no opaque row identifier
ever appears in a path. The whole surface is one group, `settings`, and it is
always mounted, because the settings surface it reads and writes is required.
This set is the module's whole route surface.

| Group | Method and path | Permission |
|---|---|---|
| Settings | `GET /api/v1/system/settings` | `settings.read` |
| Settings | `GET /api/v1/system/settings/:namespace` | `settings.read` |
| Settings | `PUT /api/v1/system/settings/:namespace/:key` | `settings.write` |
| Settings | `DELETE /api/v1/system/settings/:namespace/:key` | `settings.write` |

A read answers 200 with a view; a write that lands answers 204 and no body.

`:namespace` is the namespace that declared the item, which is the module
`DeclaredItem.Module` reports and the key's prefix segment (a module name may
contain a dot, as `authn.session` does); `:key` is the rest of the key under it,
and the full key is `<namespace>.<key>`. A full key no declaration covers is a
404; a namespace group no declared item belongs to is a 404.

The permissions are not this module's own: the settings capability names them,
and the authorization directory carries them for it — `authz.PermissionSettingsRead`
and `authz.PermissionSettingsWrite` for the global layer,
`authz.PermissionTenantSettingsRead` and `authz.PermissionTenantSettingsWrite`
for the tenant layer. This module declares no catalog entry and no translation
row.

### The query string names the layer

- **`layer`** is `global` or `tenant`, with no default. It is absent or outside
  that set is a 400.
- **`tenant`** is required when `layer=tenant` and names that tenant by its
  alias, the only external name a tenant has; it must be absent when
  `layer=global`. Present or absent against `layer` is a 400.

The alias is resolved through the optional `authn.TenantDirectory` the roster
delivers, before a layer is built: an alias no tenant carries is a 400. An
assembly that supplies no roster has no aliases to resolve, so a tenant layer
cannot be selected there and the request is a 400; the surface never reads the
query as an identifier, which is internal. The alias is held to the tenant
identifier's value domain — non-empty, and free of blanks and control characters
— before it is resolved, so a value outside it is a 400 rather than the panic
`settings.ForTenant` raises on such an identifier. The domain is restated here
and the tenant capability is not imported: a module that stores values per
tenant by their identifier is deliberately not dependent on it.

## The view

A read answers a JSON array of item views, one per declared item. Each view is
built field by field and never handed through as it stands, and every field is
fixed:

| Field | Meaning |
|---|---|
| `key` | the full key, `<namespace>.<key>` |
| `namespace` | the module that declared the item, which is the key's prefix segment |
| `type` | the declared type's text form |
| `description` | the declaration's description |
| `domain` | the rule the declaration states, empty when it declares none |
| `sensitive` | whether the item is marked sensitive |
| `default` | the declared default in the declared type's own text form |
| `written` | the text the selected layer holds, the mask string for a sensitive item |
| `has_row` | whether the selected layer holds a row for the item |
| `value` | the text the item resolves to after the three-level fallback |

`default`, `written` and `value` are all text in the declared type's own text
form, so a client compares and displays them without converting between a typed
value and a row: a duration reads `5m0s`, not a count of nanoseconds. `written`
is the selected layer's explicit text; `value` walks the fallback — that layer's
row, then the global row, then the declared default.

`written` cannot say whether the layer holds a row: an empty text is a missing
row and a row written as the empty string alike. `has_row` carries that fact on
its own.

**A sensitive item is masked here.** The settings module withholds a sensitive
item's default from its listing but still hands out its value from a read. This
module masks the value before it reaches a client: `default` is empty and both
`written` and `value` carry the fixed mask string `********`. The masking is
applied in the one place a view is built, on every read route, so a credential
does not travel out with a response. `has_row` is not masked: it reports the
row's existence without revealing the text.

A write that carries the mask string is refused with 422
`urn:speed:settings:masked-value` before the mandatory audit and the store. The
mask is a display placeholder, not the item's value, so echoing it back would
replace the credential with the placeholder; the refusal asks nothing and
changes nothing.

## Boundaries

- **One decision order, for every route, read and write alike.** The route's
  authorization step reads the caller first: a request with no identity is a 401 —
  the problem the admission and authorization layers write for a route that
  requires an identity and carries none: the status alone, no type, and the realm
  challenge naming the endpoint the route was mounted on — and the route returns
  without asking the guard. On a write it then reads the request's actor's
  self-registration state and refuses an unverified one (see the next bullet).
  Then it settles the layer and the domain from the query string: a layer
  selection or an alias no roster carries is a 400, written before the guard is
  asked. Then it asks the guard for the route's permission in
  the layer's domain: a shortfall is the guard's own refusal, a 403 for a denial
  and a 503 for an outage. Only past the decision does the entry point bind a
  write's body and the declared handler run its shape checks, land its audit and
  call the settings module, or a read call the settings module and answer. The
  write decision is derived from the route's method, not passed in: a route that
  is not a read is a write, so a write route added to the set is gated by
  construction. The step sits in front of the declared handler because a typed
  handler receives no request, and the realm is bound per endpoint at the mount.
- **A write from an unverified self-registration is refused before the
  decision.** The step reads the request's actor's state through
  `authn.SignupVerification.SignupStateOf` after the caller and before the layer
  selection, the permission decision, the body, the mandatory audit and the
  settings module: a `SignupPending` account is answered 403 with the problem type
  `urn:speed:authn:contact-required` (the authentication capability's exported
  `authn.ContactRequiredProblemType`, not this surface's own code) — and the
  action does not run. The subject is the actor and not the account the request
  is taken to be from, so under an impersonation the operator's state is read and
  the target's does not participate. A service account, a static-table
  subject and an account created outside self-registration are exempt, and a read
  route never consults the state. A state that cannot be read is a 503 and is
  never read as verified: the gate only narrows what a caller may do, so a
  failure must not widen it. An account row that does not exist is the one
  exception: it is answered as "no self-registration record" and passed to the
  rest of the pipeline.
  `TestAWriteFromAnUnverifiedRegistrationIsRefusedBeforeAnythingRuns`,
  `TestAReadIsNotGated`,
  `TestTheNamespaceReadIsNotGated`,
  `TestAServiceAccountAndAStaticSubjectAreExempt`,
  `TestAnUnreadableSignupStateIs503`,
  `TestTheRegistrationGatePrecedesThePermissionDecision`,
  `TestAnAccountWithNoSignupRecordWrites`,
  `TestTheGateIsDerivedFromTheMethod`,
  `TestTheGateRefusesBeforeTheLayerShape`,
  `TestTheGateReadsTheActorNotTheImpersonationTarget`,
  `TestAnAccountCreatedOutsideSelfRegistrationIsExemptAfterTheRead`.
- **The decision is made per layer, in the layer's own domain.** The global layer
  is a platform operation, decided in the platform domain `authz.PlatformDomain`;
  a tenant layer is decided in that tenant's domain `authz.Domain(id)`. The two
  layers decide on different permissions, so a caller authorized in one tenant
  cannot reach the global layer by sending a tenant header. The route asks
  through the guard's explicit-domain form, so the request's own resolved tenant
  never steers it.
- **The mandatory audit of a write lands before the action, outside any
  transaction.** A write's audit row is written before the settings module is
  called; a row that does not land is answered 503 and the action is not carried
  out, so a change that could not be recorded is not made. When the settings
  module refuses the write, a failed row linked to the permitted one by the action
  identifier is written best-effort: its own failure is an Error line and changes
  nothing about the answer. The action names are a closed set — `settings.set`,
  `settings.delete` — one per write route, and the detail carries the action
  identifier, the key, the layer and the tenant. No value enters a row, and a read
  writes no audit row of its own.
- **The refusal mapping is closed and fail-closed.** A value the settings module
  cannot encode under the declared type or that lies outside its declared domain
  is a 422 carrying `urn:speed:settings:invalid-value`; a key with no declaration
  is a 404; any other failure is a 503. A 404 and a 503 carry no problem type —
  they say something is not there or a dependency could not answer and not which
  refusal it was. The cause of a 503 stays in the log and never reaches the
  caller. `settings.ErrNotLoaded` is not mapped: it is the read before the
  settings module's `Init` loaded its snapshot, and a management route reads on
  the request path, so it falls to the same 503 an unknown failure gets.
- **A shape refusal happens after the decision and before the action.** An
  unreadable body is a 400, and a full key no declaration covers is a 404; both
  reach neither the audit nor the settings module.
- **The module decides its own routes ahead of the declared handler and declares
  them open.** The middleware lets a management request through to the route,
  which makes the decision; the module grants itself no permission. Where the
  assembly delivers the route-declaration surface, `Init` registers every
  management route open on it. With no surface the routes carry no declaration,
  and a guarded endpoint answers them as undeclared — which is why a command that
  mounts these routes should name the enforcement module too.
- **The module delivers no capability and registers `settings.admin`.** It
  carries no product another module resolves; it is taken in only to mount the
  routes. It states `StateEnabled` and never `StateAuto`: whether the management
  routes exist is a decision of the command tree, not a conclusion a
  configuration infers.
- **It requires the settings surface, the handler-internal guard, the audit
  capability and the registration-verification read.** A command that brings this
  one in without the settings surface to act on, the guard to decide with, the
  audit to record through, or the `authn.SignupVerification` read to gate a write
  on fails the startup before any route is mounted. The HTTP entry point is taken
  up in `Init` and is required — a command that mounts the management routes and
  has no entry point has nothing to mount on, which is `authz.ErrNoRouter`. The
  tenant directory is optional and taken up in `Init`: without it the global
  layer still works, and every request for a tenant layer is a 400 because no
  alias can be resolved. The logger and the route-declaration surface are also
  optional, both taken up in `Init`.
- **The module validates no value.** A value's type and domain are judged by the
  settings module when it is written; this module checks only the request's shape
  — the layer selection, the tenant alias, the full key's declaration and
  the body's readability.

## Configuration

- **`settings.admin.endpoints`** (configuration, a list of strings): the endpoints
  the management routes are mounted on, by the names the entry point's own
  configuration gives them. It is required and has no default — a management
  surface mounted nowhere would silently drop every route — and it takes concrete
  names only, never `*`, because mounting the routes on every endpoint would put
  them on the public ones, where no gateway guards them. The item takes the
  primary source alone. A shape the module cannot run on — an empty list, an empty
  name, `*`, a name written twice — is `settings.admin.ErrInvalidConfig`; a name
  the entry point does not declare is `authz.ErrUnknownEndpoint` at startup.

## Running it

From the repository root, over every module in the workspace:

```
make build
make test
make test-race
make lint
make check
```

`make check` is what CI runs. On this module alone:

```
go -C pkg build ./settings/...
go -C pkg vet ./settings/...
go -C pkg test ./settings/admin/...
go -C pkg test -race ./settings/admin/...
env -C pkg golangci-lint run ./settings/admin/...
```

The tests ask for no service and no environment. The route cases mount the
module's whole route set on a real `gin` engine and drive it over an `httptest`
recorder, with stand-ins for the settings surface, the guard, the audit, the
signup-state read and the configuration reader, and a real identity from
`authntest` — so a case can assert that the layer chooses the domain and the
permission, that the guard's refusal is its own, that the registration gate
refuses an unverified write before the decision, that a sensitive item's value is
masked, and that a write whose mandatory audit did not land reached no settings
call. The assembly cases drive the module's
descriptor through a real registry, which is where what it requires and takes up
is observed.
