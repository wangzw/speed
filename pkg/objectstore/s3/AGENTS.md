# pkg/objectstore/s3

The S3-compatible backend of the object-storage capability, registered as
`objectstore.s3`. It delivers the byte seam — `objectstore.Store` — exclusively
over one bucket of an S3-compatible service: an object's bytes are stored as one
object, read back as a stream, stat'ed and deleted. It also delivers
`objectstore.DirectURL` (`Spec.Direct` is true), so a browser can reach the
bucket through a signed address that does not pass through this process. Both
tokens name the one value `Open` returns. A host writes this module's name into a
command; the byte seam is delivered exclusively, so a command names exactly one
backend and two at once fail in resolution.

The whole leaf package binds `github.com/minio/minio-go/v7`, and the SDK is
imported here and nowhere else — not by the root package, not by the
local-directory backend, not by the metadata module. An assembly that uses only
the local-directory backend therefore carries no object-storage SDK; a multi-
replica assembly that shares one set of bytes needs this one.

## Configuration

This backend's input items hang under the `objectstore.s3` namespace, which is
also its module name.

| Item | Required | Meaning |
|---|---|---|
| `endpoint` | yes | the service the client connects to |
| `region` | no | the region every call is signed in; left empty, the SDK resolves it |
| `bucket` | yes | the bucket every object's bytes are kept in |
| `access-key` | yes | the access-key identifier every call is signed with |
| `secret-key` | yes | the access-key secret every call is signed with |
| `use-path-style` | no | address the bucket in the request path rather than in the host name |

`access-key` and `secret-key` are sensitive items. The two credentials are handed
to the factory through its secrets hook, which registers them as literal
redaction rules through `pkg/internal/literal` **before** this package's `Open`
builds the client, so a credential that turns up in any log line of the process is
masked.

The endpoint may carry an explicit `http` or `https` scheme, which chooses the
transport and is stripped before the client is built; a scheme-less endpoint is
taken as https, the safe default for a cloud service or a TLS-terminated one, and
only an explicit scheme is read as one, so a host-and-port value such as
`minio.local:9000` keeps its colon. A scheme the backend does not know is an
invalid configuration and fails the startup. `use-path-style` is what a
self-hosted service usually needs; left off, the SDK picks its own address form.

## Outbound calls

Every call this backend makes to the service — `Put`, the `Open` call that forces
its `Stat`, a standalone `Stat`, and `Delete` — and every signing call
(`PresignRead`, `PresignWrite`) is logged once on **the module's own logger**. The
factory takes up the optional `log.Logger` in `New`, names it after this module
and hands it to `Open`, and the store holds it. The operation's context is not
consulted: the factory's background sweep calls `Delete` on a context it created,
which carries no injected logger, so `log.FromContext` would put that path's
records on the process default chain instead of the destinations the host
configured. Reading from the module's logger also gives every line the module
attribute, whichever context drove the call. A success is one `INFO` line and a
failure one `ERROR` line, each carrying:

| Field | Meaning |
|---|---|
| `operation` | `put`, `open`, `stat`, `delete`, `presign-read`, `presign-write`, or `read` for the body read after `Open` |
| `bucket` | the configured bucket |
| `key_hash` | the first twelve hex digits of the internal key's SHA-256 |
| `size` | the declared length on a put, the object's observed length on an open |
| `duration_ms` | the time from issuing the call to its answer |
| `outcome` | `ok` or `failed` |
| `cause` | on a failure: the service's error code, or a transport error's text with the key scrubbed out |
| `http_status` | on a failure, when the service answered: its HTTP status |

`Open`'s success line is written once the object's header is in hand, before any
body byte is read. The stream `Open` hands back reports a body read that ends
anywhere but at `EOF` as its own `read` failure line, with the same `key_hash`, so
a transfer that breaks after `Open` returned still leaves a failure record; one
broken stream writes one such line, however often the reader answers the error.
An end of stream is the transfer completing and writes nothing. A `read` line's
`duration_ms` is the body read's own call: it starts at the first `Read`, which is
the call that makes the SDK issue its request, so neither `Open`'s call (which
has its own line) nor the wait between `Open` returning and the first read is
part of it.

The internal key itself, the object's bytes, the credentials and the request
body never reach a line. `key_hash` names an object for a trace without
addressing it. A failure is reported by the service's own code — `AccessDenied`,
`NoSuchBucket` — rather than its message, and any other error text has the key
and its URL-escaped forms (path-escaped and query-escaped, including a key
carrying spaces, `+` and `%`) replaced before it is logged, because the SDK
repeats the key in a service message and in the URL a transport error carries. A
call the context had already ended writes no line: no request left the process.
A signed address never reaches a line either: it carries the object key and the
access-key identifier.

## Direct addresses

`PresignRead` answers a GET address that serves the object's bytes with the media
type and file name the caller asked for, carried as `response-content-type` and
`response-content-disposition` overrides so the bucket serves them rather than
whatever the object was stored as. The disposition is rendered with
`mime.FormatMediaType`, so the file name is escaped, never spliced in as written;
an empty name adds no disposition parameter.

`PresignWrite` answers a PUT address. No request header is signed: a presigned
PUT binds the method, the object key and the expiry, and a `Content-Length`
cannot be expressed in one, so the declared size is what the completion step's
`Stat` checks.

Signing is local and reaches no service. A lifetime shorter than a second or
longer than seven days is refused with `objectstore.ErrURLUnavailable` rather
than handed to the SDK, whose own acceptance bounds are the same. A returned
`IssuedURL` carries the method and the instant the address stops working; the
address itself is a bearer capability, used whole and parsed by no caller.

## Boundaries

- **The backend maps the internal key to an object key and interprets nothing more
  of it.** The key is generated by the metadata module and its syntax is that
  module's convention; here it becomes the S3 object key, and the mapping never
  enters the seam.
- **The internal key itself reaches no error text, no log line and no answer.**
  This package's own error text names the operation and no key, and its call
  lines name the object by `key_hash` alone. The SDK's own error is kept
  reachable with `%w` so `errors.As` still finds its response type, and that type
  carries the key in a field; a caller that renders that error as text gets the
  service's message, never the structure. The call log does not render it: a
  service answer is logged by its code.
- **`Delete` is idempotent.** The SDK's `RemoveObject` maps to the service's
  `DeleteObject`, and an S3-compatible service answers `204` for a delete whether
  or not the object exists; the SDK treats any status other than `204` as the
  failure. Deleting bytes that are not there is therefore a success, which is the
  idempotence the seam promises.
- **`Open` reports an absent object from `Open`, not from the first read.** The
  SDK's `GetObject` issues no request until the stream is read, so the backend
  forces one `Stat` (a `HEAD`) before handing the stream back; an object the
  bucket does not hold is reported at once, the shape the local-directory backend
  has where opening a missing file errors immediately. The service's own
  not-found error is what is handed back, kept reachable with `%w`.
- **A byte operation passes the backend's own error through and invents no
  sentinel.** A call the service refuses, or one that never reaches it, reports
  the SDK's error; the metadata module is what wraps a failed byte operation into
  the capability's unavailable sentinel. The sentinels this package raises are
  the shapes the seam itself defines: `objectstore.ErrInvalidConfig` for a
  configuration the client cannot be built from, `objectstore.ErrContentMissing`
  when `Stat` finds the bucket holds no object under the key, and
  `objectstore.ErrURLUnavailable` when a lifetime is outside the signing bounds or
  the SDK refuses to sign.
- **Nothing is queued, retried or degraded.** Each method reaches the service and
  returns once it has answered, and the caller's context is passed to the call, so
  a context that ends cancels the in-flight request.
- **The SDK stays off the package surface.** The package exports its module name,
  its config namespace and its module descriptor; no vendor type appears in a
  signature, so a host that imports this package to register it never has to name
  the SDK.
- **The metadata module imports no backend.** This package reaches the seam only
  through the root package's `objectstore.Spec`, `objectstore.Store` and
  `objectstore.DirectURL`.

## How to run it

From the module directory:

```
env -C pkg go build ./objectstore/s3/...
env -C pkg go test ./objectstore/s3/...
env -C pkg go test -race ./objectstore/s3/...
env -C pkg golangci-lint run ./objectstore/s3/...
```

Run `golangci-lint` from inside `pkg`: handed an absolute path from outside the
module it loads no packages and still exits 0.

The cases need no real service: a case builds the store against a loopback
`httptest` endpoint and holds the request shape — method, path, host, signature —
the real SDK produced. A `Stat` case holds that a `HEAD` is sent and its length
mapped, and that a 404 wraps `ErrContentMissing`. The presign cases need no
listener at all, because signing is local: they hold that a read address carries
a signature, an expiry and the media-type and file-name overrides, that a write
address carries a signature and no read-side override, and that lifetimes outside
`[1s, 7d]` are refused. `s3_log_test.go` builds the store with a logger the case
owns and holds that each call — a signing call included — writes exactly one line
(it counts lines rather than asking whether a field is present), that a delete on
a background context still reaches the module logger, that a body cut short after
`Open` writes one `read` failure line, that `scrubKey` replaces a key's written,
path-escaped and query-escaped forms, and that neither the key nor a signed
address is in any line. `assembly_test.go` drives the real configuration loader
for the required items and a real registry through the descriptor to `Init`,
resolving both seams from the one product.
