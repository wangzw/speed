# pkg/sms/admin

The SMS capability's management module. It mounts the template-management routes
under `/api/v1/sms/admin/*` and delivers no capability of its own: every route's action
is a call on the template registry, reached through the `sms.Registry`
capability, and the module holds no template, no rule and no data of its own.

The descriptor declares the static feature `sms.admin`, present whenever the
module is in force.

The registry owns what a template is — which of them are in force, which the
process may change and what the vendor answered — so this module only decides
who may ask and how the answer is shaped. It carries no policy rule: it asks the
decision of the handler-internal `authz.guard` API and maps the registry's
refusals to responses.

Taken in only by a command that wants the management routes, it is absent from
every other command: without it the template set is still read and written
through the registry's own Go interface, and the host's own commands carry their
access control.

The API is the package's godoc, plus the `Registry`, `Record`, `TemplateSpec`,
`Submission`, `ApprovalState` and sentinel types in `pkg/sms`.

## Routes and the permissions they are held to

Every route is mounted under the base path `/api/v1/sms/admin`, on the endpoints the
module's configuration lists. A route is addressed by a natural key — a template
name — so no opaque row identifier ever appears in a path. The whole surface is
one group, `templates`, and it is always mounted, because the registry it reads
and writes is required. This set is the module's whole route surface.

| Group | Method and path | Permission |
|---|---|---|
| Templates | `GET /api/v1/sms/admin/templates` | `sms.template.read` |
| Templates | `POST /api/v1/sms/admin/templates` | `sms.template.manage` |
| Templates | `POST /api/v1/sms/admin/templates/submissions` | `sms.template.manage` |
| Templates | `POST /api/v1/sms/admin/templates/:name/enabled` | `sms.template.manage` |
| Templates | `DELETE /api/v1/sms/admin/templates/:name` | `sms.template.manage` |

A read route is held to `sms.template.read` and a write route to
`sms.template.manage`. A read answers 200; a write that creates a record
(register, submit) answers 201 with the record's view, and a write that lands
without one (enable, remove) answers 204 and no body.

The two permissions are this module's own: the sending capability names no
permission, so this module declares the vocabulary it decides its routes on. It
carries `authz.PermissionSpec` and `authz.TranslationSpec` resources — the two
permissions, and the `template`/`read` and `template`/`manage` pair each
translates to — so the process's catalog holds them and the process's translation
expresses a decision on either. Without the translation the guard could not
express a permission and every route would refuse its caller.

## Boundaries

- **One decision order, for every route, read and write alike.** The handler
  reads the caller first: a request with no identity is a 401 — the problem the
  admission and authorization layers write for a route that requires an identity
  and carries none: the status alone, no type, and the realm challenge naming the
  endpoint the route was mounted on — and the handler returns without asking the
  guard. Then it asks the guard for the route's permission: a shortfall is the
  guard's own refusal, a 403 for a denial and a 503 for an outage. Only past the
  decision does a write parse its body, run its checks, land its audit and call
  the registry, or a read call the registry and answer.
- **The decision is made in the platform domain, whatever tenant the request
  carries.** The template set is the process's own and not a tenant's, so the
  handler asks through the guard's explicit-domain form with `authz.PlatformDomain`
  and the request's tenant never steers it: deciding in the request's own tenant
  would let a caller authorized in one tenant reach the platform set by sending a
  tenant header.
- **The mandatory audit of a write lands before the action, outside any
  transaction.** A write's audit row is written before the registry is called; a
  row that does not land is answered 503 and the action is not carried out, so a
  change that could not be recorded is not made. When the registry refuses the
  write, a failed row linked to the permitted one by the action identifier is
  written best-effort: its own failure is an Error line and changes nothing about
  the answer. The action names are a closed set — `sms.template.register`,
  `sms.template.offer`, `sms.template.enable`, `sms.template.remove` — one per
  write route, and a read writes no audit row of its own.
- **The refusal mapping is closed and fail-closed.** A template that came from
  the configuration is a 409; a template the registry does not hold is a 404; a
  name the registry already holds is a 409; a configuration or a message the
  registry refuses is a 400; any other failure is a 503. A 404 and a 503 carry no
  problem type — they say something is not there or a dependency could not answer
  and not which refusal it was — while a 400 and a 409 carry the capability's type
  prefix `urn:speed:sms:`. The cause of a 409 and a 503 stays in the log and never
  reaches the caller. `sms.ErrNotSendable` is not mapped: it is the sending path's
  sentinel, and a registry that answers it here is answering the wrong question,
  so it falls to the same 503 an unknown failure gets.
- **The configuration-sourced refusal names its source.** A write the registry
  refuses with `sms.ErrImmutable` — a template that came from the configuration —
  is answered with the problem type `urn:speed:sms:config-template`, distinct from
  the plain `urn:speed:sms:conflict` a name collision wears, so an operator reads
  the fix — edit the configuration and restart — off the answer.
- **A shape refusal happens after the decision and before the action.** A
  registration or a submission with an empty name, and an enable whose body names
  no target state, are 400s and reach neither the registry nor the audit. The
  enable route takes the target as a pointer so a body that names no state is told
  apart from one naming `false`: the route changes a template's state, and a
  change by omission is not one this module makes.
- **The module decides its own routes in the handler and declares them open.**
  The middleware lets a management request through to the handler, which makes the
  decision; the module grants itself no permission. Where the assembly delivers
  the route-declaration surface, `Init` registers every management route open on
  it. With no surface the routes carry no declaration, and a guarded endpoint
  answers them as undeclared — which is why a command that mounts these routes
  should name the enforcement module too.
- **The module delivers no capability and registers `sms.admin`.** It carries no
  product another module resolves; it is taken in only to mount the routes. It
  states `StateEnabled` and never `StateAuto`: whether the management routes exist
  is a decision of the command tree, not a conclusion a configuration infers.
- **It requires the template registry, the handler-internal guard and the audit
  capability.** A command that brings this one in without the registry to act on,
  the guard to decide with, or the audit to record through fails the startup
  before any route is mounted, rather than answering every route with a refusal or
  a write that cannot land. The HTTP entry point is taken up in `Init` and is
  required — a command that mounts the management routes and has no entry point
  has nothing to mount on, which is `authz.ErrNoRouter`. The logger is the one
  optional requirement, and the route-declaration surface is taken up optionally
  in `Init`.
- **The answer is a view, built field by field.** Every route answers with a view
  assembled from a `sms.Record` and never the record as it stands, so a field
  added to a record later does not reach a client by being there. The view carries
  no vendor code: a code is the registry's own bookkeeping, and an operator
  reading which templates are in force needs to know that one is, not what number
  the vendor filed it under.
- **A credential and a vendor code reach no log line, no error text and no
  metric.** The audit detail names a template by its logical name and, for an
  enable, the state it set; it never names the carrier code behind a template.

## Configuration

- **`sms.admin.endpoints`** (configuration, a list of strings): the endpoints the
  management routes are mounted on, by the names the entry point's own
  configuration gives them. It is required and has no default — a management
  surface mounted nowhere would silently drop every route — and it takes concrete
  names only, never `*`, because mounting the routes on every endpoint would put
  them on the public ones, where no gateway guards them. The item takes the
  primary source alone. A shape the module cannot run on — an empty list, an empty
  name, `*`, a name written twice — is `sms.ErrInvalidConfig`; a name the entry
  point does not declare is `authz.ErrUnknownEndpoint` at startup.

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
go -C pkg build ./sms/...
go -C pkg vet ./sms/...
go -C pkg test ./sms/admin/...
go -C pkg test -race ./sms/admin/...
env -C pkg golangci-lint run ./sms/admin/...
```

The tests ask for no service and no environment. The route cases mount the
module's whole route set on a real `gin` engine and drive it over an `httptest`
recorder, with stand-ins for the registry, the guard, the audit and the
configuration reader, and a real identity from `authntest` — so a case can assert
that a route the guard refused, a request with no identity, and a write whose
mandatory audit did not land reached no registry call and wrote no audit row. The
assembly cases drive the module's descriptor through a real registry, which is
where what it requires and takes up is observed; one of them drives the real
enforcement module alongside this one, which is what proves this module's
translation rows reach the process's one set without a conflict.
