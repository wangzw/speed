# pkg/objectstore/metadata

The object store's metadata and lifecycle module. It holds the `objects` table
and the `object_grants` table, carries an object through declaration, upload,
completion and deletion, mints the addresses a browser uses to read or write an
object's bytes, serves the content route when no backend mints addresses of its
own, sweeps expired rows and grants, and reads its runtime tunables.

The bytes themselves do not live here. They move through the
`objectstore.Store` seam; this module holds the metadata row, generates the
opaque internal key the bytes are addressed by, and reaches the backend only
through that seam. It imports no object-storage SDK.

## What it delivers

The module delivers `(*objectstore.Objects)(nil)` **exclusively**: one process
has exactly one metadata surface, so a second provider fails the startup.

It requires, non-optionally, `(*db.Database)(nil)`, `(*tenant.Catalog)(nil)`
and `(*objectstore.Store)(nil)`. The tenant roster is required because it
delivers the statement filter that scopes, fills and checks the tenant column of
every scoped statement, and because the content route enters a tenant when it
resolves a grant. The byte seam is required because it is where bytes land, so
an assembly that installs no backend fails resolution rather than at the first
upload.

It takes up optionally:

| Capability | What its absence means |
|---|---|
| `(*objectstore.DirectURL)(nil)` | Present when the backend can be reached directly (the S3 backend delivers it, the local-directory backend does not). Present: `URL` mints pre-signed addresses and no content route is mounted. Absent: this module serves the content route itself. |
| `(*settings.Store)(nil)` | The upload window, the object lifetime and the URL lifetime read as their declared defaults. |
| `(*speedhttp.Router)(nil)` | With a direct backend, nothing changes. Without one, nothing can mint an address: no content route is mounted, and `URL` answers `ErrURLUnavailable` rather than an address nobody serves. |
| `(*log.Logger)(nil)` | The process default logger is used. |

## The lifecycle

An object is written in three steps, each a call on `Objects`:

1. **Declaration** — `Create` validates the declaration (a positive `Size`, a
   non-empty `ContentType`, a `ChecksumSHA256` that is empty or 64 hex digits),
   normalizes the optional `Name`, mints the object identifier and the internal
   key, and lands a `pending` row open for upload. A declaration that fails
   validation is `ErrInvalidRequest` and lands no row; the name never fails a
   declaration. A context with no tenant is refused by the insert.
2. **Upload** — `Upload` requires the row to be `pending` or `uploaded`, its
   upload window to be open, and the byte count it is given to equal the
   declared size. Every precondition is checked before any byte is written. The
   bytes are streamed to the byte seam through a reader that counts and hashes
   them; a declared checksum that the bytes do not match has them removed and
   the call is `ErrInvalidRequest`; otherwise the row advances to `uploaded`
   with the observed size, SHA-256 and content type.
3. **Completion** — `Complete` accepts a `pending` or `uploaded` row and observes
   the backend with `Stat`:
   - no bytes behind the key is `ErrContentMissing`;
   - an unreachable backend is `ErrStoreUnavailable`;
   - a byte count that differs from the declaration has the bytes removed — best
     effort, the sweep would catch them otherwise — and is `ErrInvalidRequest`,
     with the row left where it was;
   - a match advances the row to `completed` with the observed size, the
     declared media type and an expiry from the object lifetime; the SHA-256 is
     not touched here, because a direct-to-S3 upload never passed through this
     process.

   A `completed` row is returned unchanged and no byte is looked at: completion
   is idempotent. A `deleted` row, an unknown identifier and another tenant's
   object are all `ErrObjectNotFound`.

Reads are `Get` and `OpenContent`; `OpenContent` serves only a `completed`
object. `List` returns a tenant's undeleted objects paged by a keyset cursor over
`(created_at, id)`. `Delete` removes the bytes and tombstones the row, and is
idempotent on an object already deleted.

A `deleted` row is a tombstone: the metadata row is kept and the bytes are gone.
An object the tenant cannot see — an unknown identifier, another tenant's, or a
deleted one — is `ErrObjectNotFound`, one shape for all three.

## URLs

`URL(ctx, id, request)` mints one direct-access address. The caller's decision
about which end user may hold it is made before this call: the method checks the
tenant and the object's state, and nothing about the end user. The address is a
bearer capability until it expires. This module keeps no per-URL record of who
it was minted for and offers no revocation; a URL dies only by expiring.

The state matrix:

| Request | Pending | Uploaded | Completed | Deleted |
|---|---|---|---|---|
| `URLAccessRead` | `ErrStateConflict` | `ErrStateConflict` | a `GET` address | `ErrObjectNotFound` |
| `URLAccessWrite` | a `PUT` address | a `PUT` address | `ErrStateConflict` | `ErrObjectNotFound` |

The lifetime is the configured `url-ttl` when the request names zero. A positive
request may only shorten it. It is then clamped to the object's remaining life
(a read), the remaining upload window (a write) and the seven-day maximum both
backends share. A remaining life or window under one second answers as though it
had already passed: `ErrObjectNotFound` for a read, `ErrStateConflict` for a
write. An unknown access or a negative lifetime is `ErrInvalidRequest`.

### A direct backend

When `DirectURL` is assembled, `URL` asks the backend to sign the address:
`PresignRead` with the object's MIME and name for a read, `PresignWrite` for a
write. The backend's `IssuedURL` is returned as it stands. A signing failure is
`ErrURLUnavailable`, never the backend's own error escaping. No content route is
mounted, and `content-endpoints` is not read.

### The content route

When no backend mints addresses, this module serves them itself. Three methods
on one path:

```
GET  /content?t=<token>
HEAD /content?t=<token>
PUT  /content?t=<token>
```

The token is a credential, so it lives in the query string: the entry point's
access log records `r.URL.Path` unconditionally, and a credential in a path
segment would be recorded with it.

A read checks, in this order: the token's shape (43 characters of unpadded
base64url, else 404); a grant the token hash names (else 404); a grant minted for
a read (else 404); a live grant (else 410); the tenant the grant names, entered
on the roster (unknown or disabled is 404, an unreadable roster is 503); the
object, which must be current and `completed` (else 404); and the backend, whose
missing bytes are 404 and whose other failure is 503. The response carries the
recorded media type (or `application/octet-stream`), an inline `Content-
Disposition` built with `mime.FormatMediaType` when the object has a name, a
private cache lifetime no longer than the grant's, `X-Content-Type-Options:
nosniff`, `Content-Security-Policy: sandbox` and `Referrer-Policy: no-referrer`.
A stream that implements `io.ReadSeeker` is served by `http.ServeContent`, which
answers ranges and HEAD; any other stream is copied whole with an explicit
`Content-Length` and `Accept-Ranges: none`.

A write shares the read's token checks, then: a deleted object is 404, a
`completed` one 409; a closed upload window is 410; a request with no
`Content-Length` is 411; a length that differs from the declaration is 400; the
bytes otherwise go through the same writer `Upload` uses, and a declared-checksum
mismatch is 400 with the bytes removed. Success is 204. Every response on this
method carries `Cache-Control: no-store`.

The route is mounted on the endpoints `content-endpoints` names and on none by
default. It must be mounted anonymously: a browser loading an image can carry
neither an `Authorization` header nor a tenant one, and the token is the
credential. Which layers sit on an endpoint is the host's configuration; list
the content endpoint in none of the authentication, tenant-resolution,
admission, authorization, rate-limiting or audit endpoint lists, and give it a
`max-body-bytes` of zero so a large direct upload is not refused with 413.

## Grants and the internal key

A grant row records: the tenant, the SHA-256 of the token as lowercase hex
(never the token itself), the object identifier, the direction, the expiry and
the creation instant. The primary key is the tenant and the token hash; the token
hash is unique on its own, so the content route can find a grant before it knows
the tenant. The lookup crosses tenants — the token names the one it was minted
for — and is a native statement that computes liveness against the database's
own clock in the same query.

The internal key is a random shard and a random name in lowercase hexadecimal,
`<8 hex>/<32 hex>`, joined by `/`. It carries no tenant and no object
identifier, so a backend address that embeds it (an S3 pre-signed URL does)
reveals nothing internal. It is generated at declaration, never handed back
through any API, never parsed back, never written into an error text or a log
line, and passed only to the byte seam. A row written before this grammar keeps
its old key; both are opaque to every reader here.

## The expiry sweep

A background pass runs on `objectstore.metadata.sweep-interval` and does two
things:

1. Reads rows whose time is up — a `pending` object whose upload window has
   closed, and a `completed` object past its expiry — removes each one's bytes
   through the seam, and tombstones its row. A pending row cannot be assumed to
   have no bytes: a `Store.Put` that succeeded with a state advance that did not
   leaves bytes behind. A byte removal that fails is a warning and leaves the row
   as it is for the next pass.
2. Removes the grants past their expiry. Correctness does not depend on this —
   every use compares the expiry — it only keeps the table bounded. This step
   runs whatever became of the object pass.

Two reads cross tenants: the object sweep and the content route's grant lookup.
Neither accepts a tenant from the requester. Both judge every instant against
the database's own clock, so several replicas agree. The table is first read in
`Start`, by the sweep's first pass; the module reads nothing in `New` or `Init`.

## Configuration

Under the namespace `objectstore.metadata`, from the primary config source:

| Item | Meaning |
|---|---|
| `content-endpoints` | The listening endpoints the content route is mounted on, by the entry point's own names. Required of a run that mounts the route — `Init` refuses an empty list, a `*`, or a repeated name rather than mounting nothing silently — and unread by a run that assembles no entry point or whose backend mints its own addresses. |
| `public-url` | The external origin a minted content address is prefixed with, without a trailing slash. Empty answers a root-relative address, which is right only for a same-origin deployment; a cross-origin deployment has to set it. |
| `sweep-interval` | How often the sweep runs. Default `1m`; zero or negative turns it off. It sizes the ticker `Start` raises, so a change needs a restart. |

## Settings

Declared here and read per call:

| Setting | Meaning |
|---|---|
| `upload-window` | How long a declared object stays open for upload, read at each `Create`. Default `15m`; at least one millisecond. |
| `object-ttl` | How long a completed object lives before the sweep tombstones it, read at each `Complete`. Default `0` (no expiry); zero or at least one millisecond. |
| `url-ttl` | How long a minted address lives, read at each `URL`. Default `5m`; from one second to seven days. |

With no settings store the declared defaults apply; a read that fails or lands
outside the domain applies the default and writes one `Error` naming the key.

## What it does not do

- The module declares no permission, translation, message catalog, feature or
  OpenAPI resource. It has no general API: the metadata and lifecycle are the
  `Objects` seam, reached in process by a business backend, and the only HTTP
  surface is the content route a browser loads bytes from.
- It declares no audit row and writes none. The action type names —
  `objectstore.object.created`, `objectstore.object.completed` and
  `objectstore.object.deleted`, exported by the root package — are there for the
  host to publish at the irreversible points it chooses.
- It makes no authorization decision about an end user, keeps no record of who a
  URL was minted for, and offers no revocation.

## Running it

From inside `pkg`:

```
go build ./objectstore/metadata/...
go test ./objectstore/metadata/...
go test -race ./objectstore/metadata/...
golangci-lint run ./objectstore/metadata/...
```

Run `golangci-lint` from inside `pkg`: handed an absolute path from outside the
module it prints zero issues without loading the packages. The integration-tagged
run needs the compose stack for PostgreSQL.

## Boundaries

- **The metadata row lives here; the bytes do not.** This module never touches a
  filesystem or an object-storage SDK directly.
- **The tenant comes from the context, except where a token names one.** Every
  `Objects` call is scoped by the roster module's filter. The content route's
  grant lookup crosses tenants by design, then enters the tenant it found.
- **The internal key and the token never leave the module.** Neither is in an
  API response, an error text or a log line. The table stores only a token's
  hash.
