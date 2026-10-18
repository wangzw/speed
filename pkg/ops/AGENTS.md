# pkg/ops

The operations module: it mounts the process's probe routes and its merged API
document on the listening endpoint the configuration names, and delivers no
capability. It registers the module named `ops`.

It exists as a module through its descriptor and its lifecycle callbacks alone —
it declares no `Provides` — and through the `http.Spec` resource it declares for
itself, which carries the OpenAPI fragment describing its own three routes.

The routes, all `GET`, mounted on the endpoint `ops.endpoint` names:

- `/health` — the process is alive. It reads nothing and always answers `200`
  with `{"status":"ok"}`.
- `/ready` — whether the process can serve. It checks every declared endpoint
  (`http.Router.Endpoints`, each `Accepting`) and, when a database is assembled,
  pings it once. Any failure is named in the answer's `failures` list and the
  answer is `503`; all pass and it is `200` with `{"status":"ready"}`. The
  verdict is read fresh on every request.
- `/openapi.json` — the API document, merged from every fragment the binary
  declares. It is merged once in `Init` and never recomputed.

The routes sit on an endpoint the host assembles **unguarded**: a probe carries
no credential and names no tenant, so nothing here consults an identity, a
tenant or a permission and no handler decides one.

## Configuration

Input items under the `ops` namespace, all read from the primary config source:

| Item | Meaning | Default |
|---|---|---|
| `ops.endpoint` | the endpoint the routes are mounted on, and the endpoint the module's own operations are tagged with in the document | `ops` |
| `ops.title` | the merged document's `info.title` | empty |
| `ops.version` | the merged document's `info.version` | empty |

An endpoint name the assembly does not declare fails the startup.

## Dependency

The module requires the HTTP entry point (`http.Router`): it mounts its routes
through it and reads the endpoint set from it, so a command that takes this
module in also takes the entry point in. The logger and the database are
optional requirements: with a database the readiness probe checks it beside the
endpoints, and with neither capability absent the module still serves — its
release records simply go unwritten and readiness checks the endpoints alone.

## The merged document

Merging is this module's work, and it reads the **available set** of fragments —
every fragment declared in the binary, whether or not its module runs in this
command — because the document answers which API this binary provides, not which
it is currently serving. The merge itself lives in the exported package
`pkg/ops/openapi`, so a build tool can produce the same bytes without
constructing any module.

A fragment's paths are relative to its Spec's `Prefix` and `Version`, and the
merge resolves them into absolute paths before they reach the document, so a
conflict is judged on what a client would address.

The document's top level carries `openapi`, `info` (title and version from the
two input items), `paths`, `components`, and the extension key `x-fragments`
listing each fragment's source (module and endpoint). Each operation carries the
extension key `x-endpoint`, naming the endpoint it is served on. These keys, the
module name, the configuration keys and the route methods and paths are the
host-visible surface: changing any of them is a breaking change to reconcile at
release.

A conflict between two fragments fails the startup rather than being resolved:
the same path and method declared by two fragments names both declaring modules
and the endpoints they were declared for, and one component declared under one
section and name with different content names the module that declared the later
one and the component. The same component content declared twice merges into one
entry.

## Boundaries

- **It delivers nothing.** No other module can take anything up from it; it
  registers a lifecycle and a resource declaration and nothing else.
- **It is not a business health check.** `/ready` reports process-level
  readiness — endpoints accepting, database answering — and says nothing about
  any tenant or any business state.
- **It guards nothing and decides no permission.** Its routes are on the probe
  endpoint, where there is no identity and no tenant to judge.
- **It does not check fragments against registered routes.** The two are
  separately maintained facts and are allowed to drift; a mismatch produces no
  failure signal, the same way `pkg/http`'s own shape check takes no view of it.
- **It does not recompute the merge.** The fragments are static declarations
  fixed for the life of the binary; the merge runs once in `Init`.

## Running it

From the repository root, over every module in the workspace:

```
make build
make test
make test-race
make lint
make check
```

On this capability alone:

```
go -C pkg build ./ops/...
go -C pkg test ./ops/...
go -C pkg test -race ./ops/...
env -C pkg golangci-lint run ./ops/...
```

Run `golangci-lint` from inside `pkg`: handed an absolute path from outside the
module it prints zero issues without loading the packages.
