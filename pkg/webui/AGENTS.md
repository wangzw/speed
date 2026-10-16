# pkg/webui

The static frontend module: it serves one built frontend bundle over HTTP on a
listening endpoint the entry point declares.

A host hands the bundle to `New` as an `fs.FS` — an `embed.FS` for a deployment
baked into the binary, `os.DirFS(dir)` for one served from disk — and registers
the returned module:

```go
reg.Register(webui.New(fsys))
```

The module reads paths out of that filesystem and answers them:
`GET` and `HEAD` requests are answered with the file the path names, together
with a cache header; a path the bundle does not carry that names a client-side
route falls back to the entry document `index.html`, and one that names a static
resource is answered 404. The root package is the whole implementation: it
registers the module `webui`, delivers no capability (it is a leaf), and declares
three configuration items under the `webui` namespace. The root package's godoc
is the API.

## What it does

- **Answers `GET` and `HEAD`.** Both walk the same path; `HEAD` writes the
  headers and no body. On the endpoint it is given the module registers one
  catch-all route (`/*path`) for each of the two methods, covering the whole
  path space, the root included. Every other method matches no route on that
  endpoint and the entry point's engine answers 405 with an `Allow` header.
- **Falls back to the entry document for a client-side route.** A path the bundle
  does not carry whose final segment has no file extension -- a bare segment like
  `console/tenants` -- and that falls under no resource prefix is answered with
  `index.html`, so a client-side router's deep link reaches the single page that
  renders it. The module keys this off the path's shape alone: it parses no route
  table and does not know which routes the application declares.
- **Refuses a path that names a static resource.** A path the bundle does not
  carry that names a static resource -- a file under the content-addressed
  subtree, a path whose final segment has a file extension like
  `runtime-config.json`, or a path under a resource prefix (the
  `resource-prefixes` item, `/.well-known` by default) -- is answered 404 and
  never with `index.html`. A missing hashed file is the signature of a bundle that
  does not match the entry document that references it, and a missing runtime
  resource is a deployment gap; answering either with HTML would hide it behind a
  200. The resource prefixes are how an extensionless resource is refused -- an
  ACME HTTP-01 challenge token under `/.well-known/acme-challenge`, a root
  `CNAME`: with no extension in its final segment such a path is shape-identical
  to a client-side route, so its namespace, not its own shape, decides.
- **Caches by subtree.** A file under the content-addressed subtree — the
  directory named by the `asset-prefix` item, `assets` by default — is answered
  with `public, max-age=31536000, immutable`, because a file's name changes with
  its content. The entry document and every other file are answered with
  `no-cache`.
- **Limits the path to the bundle.** The request path is cleaned with
  `path.Clean` after its leading slash is dropped, so a `..` segment folds onto
  the bundle root rather than climbing above it, and a cleaned key that is not a
  valid bundle path is answered 404. No request reaches a file outside the
  `fs.FS`.
- **Hands the mechanics to `net/http`.** The response body, the media type
  (derived from the file's name, or sniffed from its content when the name gives
  nothing), a conditional request (`If-None-Match`, `If-Modified-Since`) and a
  `Range` request are all `net/http`'s `ServeContent`'s. The module hands it the
  content and the file's information and nothing more; it does not write an
  `ETag` of its own.

## What it does not do

- **It does not read, rewrite or re-encode a file's bytes.** Compression
  (`gzip`, `br`), content hashing, `Accept-Encoding` negotiation and asset
  merging are the build tool's, and the bundle arrives carrying their result. The
  module serves what it was handed.
- **It knows no application and no frontend framework.** It has no idea which
  application or which tool produced the bundle. It does not parse a client-side
  route table (the fallback is unconditional) and it renders nothing on the
  server.
- **It does not authenticate, resolve a tenant or limit traffic.** Those are the
  jobs of the layers a host registers around it; which layers sit on the endpoint
  is the host's decision.
- **It does not open a directory or read an environment variable of its own.**
  The bundle comes in as an `fs.FS`; whether it is embedded or on disk is the
  host's decision at construction, not the module's.
- **It does not negotiate the media type against the request's `Accept`, and it
  parses no route table.** Whether a path the bundle does not carry falls back or
  is refused is decided by the path's shape alone -- its final segment's file
  extension, and whether it falls under the content-addressed subtree or a
  resource prefix; a path on the resource side is refused regardless of what the
  request accepts.

## The endpoint is the module's alone

A catch-all route and any concrete route on one method tree are mutually
exclusive in gin: mounting both makes gin panic, and the entry point reports that
as a route conflict that fails the startup. The endpoint this module is given
therefore carries no other route — not an API route, not an operations probe.
A host that needs a frontend and an API together puts them on different
endpoints, or puts a reverse proxy in front that routes each to its own endpoint.
This is a consequence of the routing shape, not something the module can work
around.

## Configuration

All three items are mounted under the `webui` namespace.

| Item | Required | Default | Meaning |
|---|---|---|---|
| `endpoint` | yes | — | the name of the listening endpoint the frontend is served on, by the name the entry point's configuration gives it |
| `asset-prefix` | no | `assets` | the directory, in the bundle, of the content-addressed subtree answered with the long cache lifetime |
| `resource-prefixes` | no | `.well-known` | the path prefixes, in the bundle, of static-resource namespaces: a path the bundle does not carry that falls under one is answered 404, never the entry document |

A missing `endpoint` fails the startup with `config.ErrMissingRequired`, naming
the full key `webui.endpoint`. An `endpoint` that names no declared endpoint
fails the startup with `http.ErrUnknownEndpoint` during `Init`.

## Stages

| Stage | What it does |
|---|---|
| `Prepare` | Reads the configuration. A missing `endpoint` fails here; a `nil` filesystem handed to `New` fails here too, with `ErrNilAssets`. |
| `New` | Builds the handler holding the bundle root and the decoded configuration. |
| `Migrate` | Nothing. |
| `Init` | Takes up the entry point's `Router`, looks the endpoint up by name, and binds the two catch-all routes. An instance the host supplied that is not this module's product is left alone. |
| `Start`, `Serve` | Nothing: the routes are on the entry point's engine, which serves them. |
| `Stop`, `Close` | Nothing: the module holds no connection and starts no goroutine. |

## Empty and missing bundles are not failures

A bundle with no `index.html`, or an empty tree, is served normally: the module
constructs and registers its routes, answers 404 for a file it does not carry,
and answers 404 when the fallback cannot find the entry document either. This is
deliberate — a backend may start before the frontend is built and deployed, and
treating "the bundle is in place" as a construction premise would make that
deployment impossible. A `nil` filesystem is the one exception: it is a host
error, and `Prepare` refuses it.

## How a request the bundle does not carry is answered

- A key that names a **static resource** is answered 404, never with
  `index.html`: a file under the content-addressed subtree, a path whose final
  segment carries a file extension, or a path under a resource prefix (the
  `resource-prefixes` item, `/.well-known` by default). A missing hashed file is
  the signature of a bundle that does not match the entry document that references
  it, and a missing runtime resource is a deployment gap; answering a script or
  JSON request with HTML would let a browser run a document as a script, or hand a
  JSON parser an HTML body, behind a 200 with nothing reporting the absence. The
  resource prefixes are how an extensionless resource -- an ACME HTTP-01 challenge
  token, a root `CNAME` -- is refused: with no extension in its final segment it is
  shape-identical to a client-side route, so its namespace decides.
- A key that names a **client-side route** -- no file extension in its final
  segment and under no resource prefix -- is answered with the entry document
  `index.html`. When the bundle carries no entry document either, that is a 404 as
  well.
- A read of the bundle that fails for a reason other than a plain absence is
  answered 500, through the entry point's error writer. The bundle is read-only,
  so the module neither retries nor degrades.

## Testing it

The tests run against a real assembly — the entry point module itself, listening
on a loopback port the system picks — with the module registered through `New`
and driven by the lifecycle, and they read each answer off the socket. That is
what holds the routing, the 405 for a method other than `GET` or `HEAD`, the
error-response shape and the cache headers to the entry point's behaviour rather
than a stand-in's. The handler's own edge cases (an empty tree, a directory, a
read failure that is not an absence, the path cleaning) run against a gin engine
built in the test, and the descriptor's shape is checked directly.

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
go -C pkg build ./webui/...
go -C pkg vet ./webui/...
go -C pkg test ./webui/...
go -C pkg test -race ./webui/...
env -C pkg golangci-lint run ./webui/...
```

Run `golangci-lint` from inside `pkg`: handed an absolute path from outside the
module it prints zero issues without loading the packages.
