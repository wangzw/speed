# pkg/audit/admin

The audit capability's management module. It mounts the record-query and export
routes under `/api/v1/system/audit/*` and delivers no capability of its own: every
route's action is one or more reads of the audit table, reached through the audit
implementation subpackage's read surface — `journal.Reader` — and the module holds
no table, no rule and no data of its own.

The descriptor declares the static feature `audit.admin`, present whenever the
module is in force.

The audit module owns what a record is — the table, its shape and its writing —
so this module only decides who may ask and how the answer is shaped. It carries
no policy rule: it asks the decision of the handler-internal `authz.guard` API
and maps the read surface's refusals to responses.

It is the query surface the audit capability deliberately does not provide:
reading an audit record needs an authorization decision, which belongs to the
permission model, so the capability leaves it to a module that carries one. This
is that module, mounted only by a command that wants it; without it the audit
table is still written by the capture paths, and a host that must read a tenant's
trail can do so itself.

The API is the package's godoc, plus the `Reader`, `Query` and `Row` types in
`pkg/audit/journal`.

## Routes and the permission they are held to

Every route is mounted under the base path `/api/v1/system/audit`, on the endpoints the
module's configuration lists. A route is addressed by a natural key — a row
identifier — so no opaque cursor ever appears in a path. The whole surface is one
group, `records`, and it is always mounted, because the read surface it reads
through is required. This set is the module's whole route surface. All three
routes share one permission, `audit.read`: a caller who may page through the
table may also export the set the filters match, and the module asks no separate
export permission.

| Group | Method and path | Permission |
|---|---|---|
| Records | `GET /api/v1/system/audit/records` | `audit.read` |
| Records | `GET /api/v1/system/audit/records/:id` | `audit.read` |
| Records | `GET /api/v1/system/audit/export` | `audit.read` |

The list route answers 200 with one page of records and the cursor of the next.
The get route answers 200 with one record, or a 404 when the table holds no such
row. The export route answers 200 and streams every record the filters match as
NDJSON, one record view per line — no envelope, no header line, no total — reading
page after page until the read surface's cursor is empty. From the first byte on,
the status and headers are committed: a failure after that abandons the connection
and the caller has an incomplete download. The export is not a snapshot: a row
appended while it runs is not in the file, and a row deleted while it runs is
missing from it.

`audit.read` is not this module's permission: the authorization capability
carries it, and this module only asks for it. The module therefore declares no
permission vocabulary of its own — it carries no `authz.PermissionSpec` and no
`authz.TranslationSpec`.

## Query parameters

The list route accepts this closed set of query parameters. A parameter not in
the set is refused with a 400, so that a misspelled bound is reported rather
than swallowed by a read that ignores it.

| Parameter | Meaning |
|---|---|
| `since` | The lower time bound, RFC 3339, inclusive. Absent for no lower bound. |
| `until` | The upper time bound, RFC 3339, exclusive. Absent for no upper bound. |
| `action` | An action, repeatable. Given, only those actions are read. |
| `outcome` | An outcome, repeatable. Given, only those outcomes are read. |
| `actor` | An actor, repeatable. Given, only those actors are read. |
| `cursor` | The previous page's cursor. Absent to start at the newest row. |
| `limit` | The page size. Absent for the read surface's default, at most its maximum. |

A time bound is folded to UTC, the way every row's time is written. An empty
value for `action`, `outcome` or `actor` names nothing and is refused like a
parameter outside the set.

The export route accepts the same filters — `since`, `until`, `action`,
`outcome` and `actor` — and neither `cursor` nor `limit`: both describe a page,
and an export reads the whole set the filters match, so sending either is a 400.
The export reads at the read surface's maximum page size, a size the caller does
not choose.

## Boundaries

- **One decision order, for every route.** The handler reads the caller first: a
  request with no identity is a 401 — the problem the admission and authorization
  layers write for a route that requires an identity and carries none: the status
  alone, no type, and the realm challenge naming the endpoint the route was
  mounted on — and the handler returns without asking the guard. Then it asks the
  guard for `audit.read`: a shortfall is the guard's own refusal, a 403 for a
  denial and a 503 for an outage. Only past the decision does it parse the
  request, read the table and answer.
- **The decision is made in the platform domain, whatever tenant the request
  carries.** The audit table is a platform asset — it holds every tenant's rows
  and the rows that belong to no tenant — so the handler asks through the guard's
  explicit-domain form with `authz.PlatformDomain` and the request's tenant never
  steers it: deciding in the request's own tenant would let a caller authorized
  in one tenant read another's rows by sending a tenant header.
- **The read is issued under a cross-tenant mark.** The table holds every
  tenant's rows, so the handler derives a context with `tenant.CrossTenant` and
  hands it to the read surface, which therefore adds no tenant condition. The
  row identifier is unique over the whole table, so the get route needs no tenant
  either: it reads the one row, or nothing.
- **A read writes no audit row.** Reading the table is not an action, and a row
  per read would grow the table with its being read. Every route of this module
  is a read, so the module requires no audit capability and writes nothing: the
  trace of a read is a diagnostic line in the log — the route, the caller and the
  number of rows read; an export's line also carries the filters and the rows
  written. A host that wants these reads in the audit table itself configures the
  audit module's own request-level capture over this surface's endpoint and
  routes; this module adds no path of its own.
- **The refusal mapping is closed and fail-closed.** A request the module cannot
  read on shape — a path that is not a row identifier, a time bound that is not
  RFC 3339, an action, outcome or actor that names nothing, a limit that is not a
  non-negative integer, a query parameter the route does not accept, a cursor or
  limit on the export — is a 400. A cursor the read surface will not accept is a
  400 too: the cursor is opaque, so only the read surface can say it is not one it
  handed out. A read failure is a 503. A get route the table holds no row for is a
  404. The 400 carries the capability's problem type
  `urn:speed:audit:bad-request`; the 404 and the 503 carry no type — they say a
  row is not there or a dependency could not answer, not which refusal it was —
  and the cause of a 503 stays in the log and never reaches the caller. A failure
  the mapping does not name gets the same 503 an unknown failure gets; it never
  falls through as a success.
- **The export's first page is read before the reply is returned.** A failure
  there happens before any byte is written, so it is still a 503. A request the
  caller already cancelled is not a dependency failure: the mapped answer stays
  the 503, but no failure record is written for it, the same way a disconnect
  ends the stream quietly. Once the status
  and headers are committed, a later page's failure cannot become a status: the
  connection is abandoned, the caller has an incomplete download, and the log
  carries the filters, the rows written and the cause. A caller that disconnects
  ends the stream quietly, without a failure record.
- **The module decides its own routes in the handler and declares them open.**
  The middleware lets a query request through to the handler, which makes the
  decision; the module grants itself no permission. Where the assembly delivers
  the route-declaration surface, `Init` registers every query route open on it.
  With no surface the routes carry no declaration, and a guarded endpoint
  answers them as undeclared — which is why a command that mounts these routes
  should name the enforcement module too.
- **The module delivers no capability and registers `audit.admin`.** It carries
  no product another module resolves; it is taken in only to mount the routes. It
  states `StateEnabled` and never `StateAuto`: whether the query routes exist is
  a decision of the command tree, not a conclusion a configuration infers.
- **It requires the read surface and the handler-internal guard.** A command that
  brings this one in without the read surface to read through or the guard to
  decide with fails the startup before any route is mounted, rather than
  answering every route with a refusal or reading nothing. The read surface is
  delivered exclusively by the audit implementation module, so a command that
  mounts these routes names it too; the HTTP entry point is taken up in `Init`
  and is required — a command that mounts the routes and has no entry point has
  nothing to mount on, which is `authz.ErrNoRouter`. The logger is the one
  optional requirement, and the route-declaration surface is taken up optionally
  in `Init`.
- **The answer is a view, built field by field.** Every route answers with a view
  assembled from a `journal.Row` and never the row as it stands, so a field added
  to a row later does not reach a client by being there. The record view's fields
  are `id`, `tenant`, `occurred_at`, `actor`, `source`, `action`, `object`,
  and `tenant` is the alias the optional `tenant.AliasResolver` answers for the
  row's internal identifier; an assembly with no resolver names no tenant
  rather than exposing the identifier;
  `outcome`, `purpose` and `detail`; the list answer is `entries` and `next`.
  These names are the module's contract with a host and a front end. An empty
  `tenant` means the row belongs to no tenant — a platform operation or a
  system-level one — and times are written in UTC.
- **The export body and its headers are a response shape.** The 200's media type
  is `application/x-ndjson`: one record view per line, each line a JSON object
  ending in a newline. It carries `Content-Disposition: attachment` with the
  filename `audit-records-<the export's UTC instant>.ndjson`, built with
  `mime.FormatMediaType`, and `Cache-Control: no-store`. An empty match answers
  200 with an empty body. A caller reading the route directly relies on these;
  changing them is a breaking change. The module does not publish the record
  view's schema for the body — the contract describes it as binary — so a
  generated client receives the file as bytes.

## Configuration

- **`audit.admin.endpoints`** (configuration, a list of strings): the endpoints
  the query routes are mounted on, by the names the entry point's own
  configuration gives them. It is required and has no default — a query surface
  mounted nowhere would silently drop every route — and it takes concrete names
  only, never `*`, because mounting the routes on every endpoint would put them
  on the public ones, where no gateway guards them. The item takes the primary
  source alone. A shape the module cannot run on — an empty list, an empty name,
  `*`, a name written twice — is `authn.ErrInvalidConfig`; a name the entry point
  does not declare is `authz.ErrUnknownEndpoint` at startup.

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
go -C pkg build ./audit/...
go -C pkg vet ./audit/admin/...
go -C pkg test ./audit/admin/...
go -C pkg test -race ./audit/admin/...
env -C pkg golangci-lint run ./audit/admin/...
```

The tests ask for no service and no environment. The route cases mount the
module's whole route set on a real `gin` engine and drive it over an `httptest`
recorder, with stand-ins for the read surface and the guard and a real identity
from `authntest` — so a case can assert that a route the guard refused, a request
with no identity, and a shape the module cannot read reached no read at all. The
export's post-first-byte failure is driven over a real server instead, because a
recorder swallows the abort the reply raises to abandon the connection. The
assembly cases drive the module's descriptor through a real registry, which is
where what it requires and takes up is observed.
