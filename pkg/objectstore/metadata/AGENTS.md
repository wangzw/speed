# pkg/objectstore/metadata

The object store's metadata and lifecycle module. It holds the `objects` table
and carries an object through declaration, upload, completion and deletion. The
bytes themselves do not live here — they move through the `objectstore.Store`
seam — but the metadata row, the upload window, the keyset listing, the expiry
sweep and the HTTP surface are this module's.

The descriptor declares the static feature `objectstore.objects`, present
whenever the module is in force; the assembled byte backend does not change it.

## What it delivers

The module delivers `(*objectstore.Objects)(nil)` **exclusively**: one process
has exactly one metadata surface, so a second provider fails the startup. It
requires, non-optionally, `(*db.Database)(nil)`, `(*tenant.Catalog)(nil)` and
`(*objectstore.Store)(nil)`. The tenant roster is required because it delivers
the statement filter that scopes, fills and checks the tenant column of every
statement this module issues; the byte seam is required because it is where
bytes land, so an assembly that installs no backend fails resolution rather
than at the first upload.

The HTTP entry point (`(*http.Router)(nil)`), the logging capability
(`(*log.Logger)(nil)`) and the settings store (`settings.Store`) are optional.
With no entry point the routes are not mounted, the `endpoints` list is not read
and the module still delivers `Objects` for a command that reaches it through
`core.Resolve`. With no settings store the upload window and the object TTL read
as their declared defaults. The message localizer is taken up in `Init` when one is
assembled.

## The lifecycle

An object is written in three steps, each a call on `Objects`:

1. **Declaration** — `Create` validates the declaration (a positive `Size`, a
   non-empty `ContentType`, a `ChecksumSHA256` that is empty or 64 hex digits),
   normalizes the optional `Name`, mints the object identifier and the internal
   key, and lands a `pending` row open for upload. A declaration that fails
   validation is `ErrInvalidRequest` and lands no row; the name never fails a
   declaration. A row is written only under a tenant; a context with no
   tenant is refused.
2. **Upload** — `Upload` requires the row to be `pending` or `uploaded`, its
   upload window to be open, and the byte count it is given to equal the
   declared size. Every precondition is checked before any byte is written. The
   bytes are streamed to the byte seam through a reader that counts and hashes
   them, and the row is advanced to `uploaded` with the observed size, SHA-256
   and content type.
3. **Completion** — `Complete` moves an `uploaded` row to `completed` and stamps
   its expiry from the object lifetime; a `completed` row is returned unchanged;
   a `pending` row is `ErrStateConflict` and a `deleted` one is
   `ErrObjectNotFound`.

Reads are `Get` and `OpenContent`, which serve only a `completed` object;
`List` returns a tenant's undeleted objects paged by a keyset cursor over
`(created_at, id)`; `Delete` removes the bytes and tombstones the row, and is
idempotent on an object already deleted.

A `deleted` row is a tombstone: the metadata row is kept and the bytes are gone.
An object the tenant cannot see — an unknown identifier, another tenant's, or a
deleted one — is `ErrObjectNotFound`, one shape for all three, so the answer
leaks nothing about existence. Every operation but `Delete` answers that for a
deleted row: the object is gone, which is not a state conflict. `Delete` on a
deleted row is an idempotent success and removes no byte.

## The name

The `name` column holds the label the caller declared, normalized once in
`Create` and never re-read for meaning: it is not unique and no lookup uses it.
The stored value is valid UTF-8, carries no character in the Unicode categories
Cc, Cf, Zl or Zp (those are dropped), carries no `/` or `\` (they become `_`),
has no leading or trailing whitespace, is at most 255 bytes cut at a character
boundary with whitespace trimmed again, and is empty when it consists only of
dots. Quotes and semicolons are kept, no extension is treated specially, and a
name never fails a declaration: an omitted, empty or reduced-to-nothing name
stores as the empty string. Rows that predate the name migration carry the empty
string, with no backfill.

## The internal key

An object's bytes are addressed by an internal key whose syntax this module
fixes: two segments — the tenant identifier and the object identifier — joined
by `/`, with a per-segment cap of 128 bytes and a whole-key cap of 256 that the
key column's length check also declares. The key is generated at declaration,
never handed back through any API, never parsed back, and never written into any
error text or log line. It is passed only to the byte seam. Changing its syntax
is an internal change no caller can observe.

## The expiry sweep

A background pass runs on `objectstore.metadata.sweep-interval`. Each pass reads
rows whose time is up — a `pending` object whose upload window has closed, and a
`completed` object past its expiry — removes each one's bytes through the seam,
and tombstones its row. A pending row cannot be assumed to have no bytes: a
`Store.Put` that succeeded with a state advance that did not leaves bytes behind,
so the byte removal runs for a pending row too. A byte removal that fails is a
warning and leaves the row as it is, for the next pass to retry. The pass crosses
tenants — it is the database's own housekeeping, not a tenant's request — and
judges every instant against the database's own clock, so several replicas'
sweeps agree.

The table is first read in `Start`, by the sweep's first pass. The module reads
nothing in `New` or `Init`: on a first start the table does not exist until the
migrations have run, and the routes are registered rather than served. With the
sweep turned off, the first read is the first request.

## Configuration

Under the namespace `objectstore.metadata`. `endpoints` and `sweep-interval` are
configuration items, from the primary config source:

| Item | Meaning |
|---|---|
| `endpoints` | The listening endpoints the routes are mounted on, by the entry point's own names. Required of a run that assembles an entry point — `Init` refuses a run that mounts the routes with the list empty rather than mounting nothing silently — and unread by a run that assembles none, where an empty list is legal. Concrete names only, never `*`. |
| `sweep-interval` | How often the sweep runs. Default `1m`; `0` or negative turns the sweep off. It sizes the ticker `Start` raises, so a change needs a restart. |

`upload-window` and `object-ttl` are settings, declared here and read per call:

| Setting | Meaning |
|---|---|
| `upload-window` | How long a declared object stays open for upload, read at each `Create`. Default `15m`; must be at least one millisecond, since it is stored and read in whole milliseconds. |
| `object-ttl` | How long a completed object lives before the sweep tombstones it, read at each `Complete`. Default `0` (no expiry); zero or at least one millisecond, since a positive value is stored and read in whole milliseconds. |

With no settings store the declared defaults apply; a read that fails or lands
outside the domain applies the default and writes one `Error` naming the key.
Neither is watched for relaxation. The config schema no longer carries the two
items: a config file naming either fails startup as an unknown key.

## Routes and permissions

The base path is `/api/v1/objects`. The route set is closed:

| Method | Path | Permission |
|---|---|---|
| `POST` | `/api/v1/objects` | `objectstore.object.write` |
| `PUT` | `/api/v1/objects/:id/content` | `objectstore.object.write` |
| `POST` | `/api/v1/objects/:id/complete` | `objectstore.object.write` |
| `GET` | `/api/v1/objects` | `objectstore.object.read` |
| `GET` | `/api/v1/objects/:id` | `objectstore.object.read` |
| `GET` | `/api/v1/objects/:id/content` | `objectstore.object.read` |
| `DELETE` | `/api/v1/objects/:id` | `objectstore.object.write` |

**The routes are declared as HTTP operations, and the fragment is generated.**
`operations.go` declares each route with its input and answer types; the
content GET is a `ReplyOperation` answering `*/*` with the object's recorded
MIME, the content PUT binds the raw body and checks `Meta.ContentLength`
against the declared size, and the fragment `openapi/metadata.json` is a build
product of those declarations, regenerated with:

```
UPDATE_FRAGMENTS=1 go -C pkg test ./objectstore/metadata -run TestTheFragmentIsGeneratedFromTheDeclarations
```

Without the variable the same test is the drift gate.

Each route declares its permission on the route-declaration surface; the
enforcement middleware decides and the handler carries no decision of its own.
The two permissions are declared as `authz.PermissionSpec` and their translation
as `authz.TranslationSpec`. A refusal maps closed and fail-closed: a request
whose shape cannot be read is a `400` with `urn:speed:objectstore:bad-request`,
an object the tenant cannot see is a `404` with `urn:speed:objectstore:not-found`,
a state that does not allow the action is a `409` with
`urn:speed:objectstore:conflict`, and every other failure — an unavailable byte
seam, missing content — is a `503` whose cause stays in the log. The titles and
details are rendered from this module's own catalog under `locales/`, one file
per locale with the same key set.

The object view the routes render carries `id`, `name`, `state`, `size`, `mime`,
`checksum_sha256`, `expires_at`, `created_at` and `updated_at`, in UTC. The
`name` member is always present; it is the empty string when the object has no
name. The declaration request's `name` is optional. It never carries the
internal key.

## Audit actions

The module **declares no audit row and writes none**, and points no dependency at
the audit module. The action type names — `objectstore.object.created`,
`objectstore.object.completed` and `objectstore.object.deleted`, exported by the
root package as `objectstore.EventObjectCreated` and its siblings — are declared
there for the host to publish at the irreversible points it chooses; the audit
module names the types it watches through its own configuration.

## Running it

From the repository root:

```
make build
make test
make lint
```

On this module alone, from inside `pkg`:

```
go build ./objectstore/metadata/...
go test ./objectstore/metadata/...
go test -race ./objectstore/metadata/...
env -C pkg golangci-lint run ./objectstore/metadata/...
```

`golangci-lint` must be run from inside `pkg`: handed an absolute path from
outside the module it prints zero issues without loading the packages.

## Boundaries

- **The metadata row lives here; the bytes do not.** This module never touches a
  filesystem or an object-storage SDK directly. Bytes move only through
  `objectstore.Store`, and this module imports no backend SDK, so a host compiles
  no backend but the one it deploys on.
- **The tenant comes from the context.** Every `Objects` call is scoped by the
  roster module's filter, which fills the tenant column on insert and checks it
  on write. The one pass that crosses tenants is the sweep.
- **The internal key never leaves the module.** It is in no response, no error
  text and no log line.
