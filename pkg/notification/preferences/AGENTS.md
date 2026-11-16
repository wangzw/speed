# pkg/notification/preferences

The channel preference matrix and the type-taxonomy consumption point of the
notification capability. It holds, for each recipient and each notification type,
which channels that recipient's dispatches of the type go out under, and it
collects the notification types the business modules declare to validate a
preference write against.

The descriptor declares the static feature `notification.preferences`, present
whenever the module is in force.

## What it delivers

The module delivers `(*notification.Preferences)(nil)` **exclusively**: one
process has exactly one preference matrix, so a second provider fails the
startup. It requires, non-optionally, `(*db.Database)(nil)` and
`(*tenant.Catalog)(nil)`. The roster is required for the statement filter it
installs — the filter that scopes, fills and checks the tenant column of every
statement this module issues — and the database for the table. The HTTP entry
point (`(*http.Router)(nil)`) and the logging capability (`(*log.Logger)(nil)`)
are optional: with no entry point the routes are simply not mounted and the
endpoint list is not read, and a run without logging still gets a working matrix
whose failures reach the process default logger. The message localizer is taken
up in `Init` when one is assembled; with none, a refusal carries this module's
English text.

The seam is `notification.Preferences`:

- `Types` and `Type` read the taxonomy: `Types` returns a fresh, type-key-ordered
  copy every call, and `Type` reports whether a key is declared and, if so, its
  spec.
- `Effective(ctx, recipient, typeKey)` is the channels a recipient's dispatches
  of a type go out under — the channel set the recipient stored for the type, or
  the type's default channels when the recipient stored none.
- `Set(ctx, recipient, typeKey, channels)` replaces a recipient's channel set for
  a type and returns the effective set written.

`Effective` is the only seam the delivery pipeline uses to resolve a recipient's
channels; the pipeline never reads this module's table. The tenant comes from
`ctx`; a context with no tenant is refused before storage is touched.

## The type taxonomy

A notification type is not declared here. It belongs to the business module that
raises it: a business module places a `notification.TypeSpec` in its own module
descriptor's `Resources`, and this module collects every declaration in `New`
through `core.Resources[notification.TypeSpec](reg)`.

The source domain is the **available set, not a participation set**. The resource
query walks every registered module, so a type a module declares is in the
taxonomy whether or not that module is driven by the run. A type is a piece of
vocabulary a preference write is checked against, and a word does not leave the
language because one command did not load it.

Every declaration is held to `notification.ValidateTypeSpec` — the one criterion
both consumption points of the taxonomy apply — and a declaration or a set that
fails it ends the startup with `notification.ErrInvalidConfig`, naming the key
and the declaring module:

- a type key that is empty;
- a default channel outside the closed set `in_app` / `email` / `sms`;
- an empty default channel set;
- a default channel listed twice;
- a repeated recipient-visible-parameter whitelist entry;
- two modules claiming one type key — the criterion's set-level rule, applied
  here because this is where the declarations are collected.

`Unsubscribable` is **not** one of these rules: it constrains `Set`, not which
channels a type may go out under by default. A type that cannot be unsubscribed
may therefore default to `sms` — a verification code does.

The set is then ordered by type key and held read-only for the life of the
process. The taxonomy is not read from any table: `New` runs before the
migrations, so on a first start the table does not exist yet.

## The effective set

The effective set of one (recipient, type) is computed in exactly one place:

1. a type whose `Unsubscribable` is false takes all of its default channels, and
   never has a stored row;
2. a type that can be unsubscribed takes the recipient's stored row when there is
   one, and the type's default channels when there is none;
3. the result is deduplicated and ordered by the closed channel set
   (`in_app`, `email`, `sms`).

`GET /preferences`, `Effective` and a `PUT` all consume this one computation.

## How the surface is declared

The routes are declared as an `http.Operations` set in `operations.go`, and
each handler is bound to its declaration (`Operation[In, Out]`), so the entry
point binds the declared input and writes the declared answer. The fragment
`openapi/preferences.json` is **generated** from the same declarations by
`pkg/internal/openapigen`, never written by hand. The `Spec` carries the API
root (`api` / `v1`) and the mounting resolves each declared path against it, so
the route and the document are one source. Regenerate the fragment after
changing a declaration:

```
UPDATE_FRAGMENTS=1 go -C pkg test ./notification/preferences -run TestTheFragmentIsGeneratedFromTheDeclarations
```

The `PUT` body is read as `application/json`: a request whose content type is
not that media type is refused with `415` before the handler runs, and an
unknown JSON member is a `400`.

## Routes

The base path is `/api/v1/notifications`. The route set is closed and every route
carries **no permission of its own** — this module declares no permission point,
so who may read or change a recipient's preferences is the host's authorization
decision.

| Method | Path | Success |
|---|---|---|
| `GET` | `/api/v1/notifications/types` | 200, `{"types": [{key, group, default_channels, unsubscribable, recipient_visible_params}]}` |
| `GET` | `/api/v1/notifications/preferences` | 200, `{"preferences": [{type_key, group, channels, explicit}]}` |
| `PUT` | `/api/v1/notifications/preferences/:type_key/:channel` | 200, `{"type_key", "channels"}` |

The two `GET` routes carry `Vary: Accept-Language`: their bodies do not vary by
language, but this module's error responses do, so the endpoint keys its
representation by the negotiated language. `explicit` is true when the type has a
stored row for the recipient and false when the channels are the type's defaults.

The recipient a route acts for is not resolved here — this module holds no
identity — but is read from the request context, where the host's identity and
authorization side put it with `preferences.WithRecipient`. A `PUT` reached with
no recipient is refused, because a write names a recipient.

### Where the routes are mounted

The module declares the configuration namespace `notification.preferences` and
reads `notification.preferences.endpoints`: the listening endpoints its routes
are mounted on, by the entry point's own names. The item is read only when the
HTTP entry point is present, and then it is **required** — an empty list with the
entry point present ends the startup with `notification.ErrInvalidConfig`, naming
the key. The names are concrete: `*`, an empty name and a name written twice are
each refused, naming the key, and a name the entry point does not declare ends
the startup naming the key and the endpoint.

Naming the endpoints is what keeps the routes out of a frontend bundle's path
space. An endpoint that already carries a catch-all route — the shape the
frontend module leaves on the endpoint it is served from — cannot take a
concrete route beside it: gin refuses the pair on one method tree, and the entry
point reports the refusal as a route conflict. A host that serves a frontend and
an API from one process therefore puts them on different endpoints, and this
module's routes go on the API one.

### The PUT decision order

The order is fixed:

1. the entry point binds the body — a `Content-Type` that is not
   `application/json` is a `415`; an empty body is a `400`, because the
   declaration marks it required; and a body that is not a JSON object (a
   literal `null` excepted — it binds as the empty object), whose `enabled` is
   a non-boolean value (other than `null`), or that carries a member the
   declaration does not describe is a `400`; these answers are the entry
   point's, with the default problem document;
2. a body naming no `enabled`, naming it `null`, or consisting of the literal
   `null` (which binds as the empty object), is `400`, rendered from this
   module's catalog;
3. the channel is checked against the closed set — an unknown channel is `400`,
   **before any no-op branch**, so it is never taken for a no-op;
4. the type is checked against the taxonomy — an unknown type is `400`;
5. an unsubscribable type that is not — `409`;
6. the current effective set is computed;
7. the target set is derived from it (`enabled` true adds the channel, false
   removes it);
8. a target equal to the current effective set is a `200` that **writes
   nothing**, which also keeps a PUT of a row-less type to its own defaults from
   landing a row that would only make `explicit` lie;
9. otherwise `Set` replaces the type's channels and the answer is the effective
   set.

## The table

`notification_preferences`, tenant-scoped (the model embeds `tenant.Model`,
column `tenant_id`). One row per recipient and type:

| Column | Meaning |
|---|---|
| `id` | primary key, generated by this module |
| `tenant_id` | the tenant |
| `recipient_user_id` | the recipient |
| `type_key` | the notification type key |
| `channels` | the channel set, a stable-sorted JSON array kept as text |
| `created_at`, `updated_at` | row bookkeeping |

The primary key is `(tenant_id, id)`; the unique index `(tenant_id,
recipient_user_id, type_key)` is what the write's upsert converges on, so two
concurrent writes of one key leave one row. The channels are text because the
order of the set is part of the value and nothing queries a channel by its part.

A non-unsubscribable type never has a row: `Set` refuses to create one.

## Errors

The refusals a handler decides are rendered from this module's own catalog
under `locales/`, one file per locale with the same key set. `Problem.Type`
carries the prefix `urn:speed:notification:`:

- `ErrUnknownType`, `ErrUnknownChannel` — `400` with
  `urn:speed:notification:bad-request`;
- a body naming no `enabled`, or consisting of the literal `null` bound as the
  empty object — `400` with `urn:speed:notification:bad-request`;
- `ErrNotUnsubscribable` — `409` with `urn:speed:notification:not-unsubscribable`;
- every other failure, a missing tenant among them — `503` with no type, whose
  cause stays in the log.

A refusal the entry point makes while binding the body — a `Content-Type` that
is not `application/json` (`415`), an empty body, a body that is not a JSON
object (a literal `null` excepted, since it binds as the empty object), a
non-boolean `enabled` other than `null`, and a member the declaration does not
describe (`400`) — carries the default problem document: `about:blank`, the
standard English status text, and no detail. This module's routes do not produce
`ErrInvalidRequest`; the sentinel remains in `pkg/notification` for the other
capability modules.

## Running it

From the repository root:

```
make build
make test
make lint
```

On this module alone, from inside `pkg`:

```
go build ./notification/preferences/...
go test ./notification/preferences/...
go test -race ./notification/preferences/...
env -C pkg golangci-lint run ./notification/preferences/...
```

`golangci-lint` must be run from inside `pkg`: handed an absolute path from
outside the module it prints zero issues without loading the packages.

## Boundaries

- **This module does not send, render or write an inbox.** It only answers which
  channels a recipient's dispatches of a type go out under. Delivering the
  message is the delivery module's; writing the in-app row is the inbox module's.
- **This module declares no notification type.** A type is the declaring business
  module's. Adding a type is a declaration in the business module, not a change
  here; a declaration that is not self-consistent fails the startup and names the
  key.
- **This module declares no permission point.** Its routes carry no permission
  gate; who may read or change a recipient's preferences is the host's
  authorization decision.
- **This module imports no other implementation subpackage of the notification
  capability** and reaches the delivery and inbox modules only through the
  `notification` root package's types.
- **The channels are a closed platform vocabulary.** A business module picks from
  `in_app`, `email` and `sms` and cannot extend it; a new channel is a change to
  the notification root package.
