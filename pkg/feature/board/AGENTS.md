# pkg/feature/board

The module `feature.board`: it publishes the feature document — the feature names
this process serves — on `GET /api/v1/features`, to authenticated callers alone.

It declares no feature of its own. Modules that serve a user-facing surface put a
`feature.Spec` in their own resources; this module checks every declaration in
the available set, collects the conditional ones posted during Init, computes the
set from the enabled set in Start, and freezes the answer bytes. A request reads
those bytes and nothing else: no registry, no configuration, no backend, and no
failure path.

## Descriptor

- `Requires`: `speedhttp.Router` (required — the module mounts a route) and
  `log.Logger` (optional — Start writes a summary when it is there).
- `Provides`: `feature.Board`, exclusively. A dependency closure never pulls the
  module in; a host names `feature.board` on the command of every process that
  serves a frontend.
- `Resources`: the configuration schema, a `speedhttp.Spec` and an
  `http.Operations` declaration. `operations.go` declares the one operation —
  method, relative path, summary, description and responses — and binds the
  handler's types (`Operation[struct{}, FeatureBoardDocument]`), so the entry
  point supplies the (empty) input and writes the declared 200;
  `openapi/board.json` is **generated** from the same declaration by
  `pkg/internal/openapigen`, never written by hand. The `Spec` gives the fragment's API root (`api` /
  `v1`) and the conventional endpoint name `account`; the mounting resolves
  the same declaration's path against that root, so route and document are one
  source. The fragment's endpoint name and the configured one are separately
  maintained facts.

  The 401 is the module's common failure: it is declared once in
  `commonFailures` and merged into the operation with `Failures`, the same way
  every route on an endpoint that the authentication gateway guards shares it.

  Regenerate the fragment after changing the declaration:

  ```
  UPDATE_FRAGMENTS=1 go -C pkg test ./feature/board -run TestTheFragmentIsGeneratedFromTheDeclarations
  ```

  Without the variable the same test is the drift gate; `make check` runs it.
- `authn.Gate` and `authz.Gate` are not in `Requires`; both claim what they
  deliver exclusively. Init resolves them.

## Configuration

| Item | Meaning |
|---|---|
| `feature.board.endpoint` | The endpoint the document is published on, by the name the entry point's configuration gives it. Required, primary source only, no default. |

A missing item fails Prepare with `config.ErrMissingRequired`, naming the full
path.

## Lifecycle

- **Prepare** reads the configuration and checks every declaration in the
  **available** set — a module this command never took in is checked too. Shape
  failures are `feature.ErrInvalidName`, a first segment that differs from the
  declaring module's is `feature.ErrForeignName`, and a name declared twice is
  `feature.ErrDuplicate`.
- **Migrate** opens the posting window. The window closes in Start and contains
  the whole Init stage. `Post` outside it panics.
- **Init** mounts `GET /api/v1/features`, registers it as impersonable on the
  authentication gateway (`feature.ErrNoGate` if there is none, and the startup
  fails), and declares it open on the authorization middleware when one is
  present.
- **Start** seals the window, refuses a post that is not a conditional feature of
  the poster (`feature.ErrUndeclared`), computes the set, freezes the answer
  bytes, and writes `feature set frozen count=N digest=sha256:… names=[…]` when
  logging is present.

## How the set is computed

```
inForce(m) = reg.AllResourcesInForce(m) || (e, ok := reg.Enablement(m); ok && e.State != core.StateDisabled)
static feature:      inForce(declaring module)
conditional feature: inForce(declaring module) && posted by it in this run
```

The enabled set is used rather than the participating set: a module that lost an
exclusive resolution is a participant with no instance and no routes. The
reason's text is not compared. `AllResourcesInForce` lets a host's
`AllResources` static inclusion count a static feature of a module that does not
run; a conditional feature of such a module is absent, because the module does
not post.

## Response

`200 application/json`, `Cache-Control: no-store`, body
`{"features":["…"]}` with the names ascending and without repetition. Anonymous
requests get the gateway's 401 problem document; this module defines no shape of
its own. The outer object is the compatible place to add fields; changing what
`features` holds is not.

## Tests

`board_test.go` drives a private registry with stand-ins for the entry point and
the gateway and holds the set computation, the post window, the preparation
checks, the response shape and the Start summary. `assembly_test.go` drives real
assemblies — the real entry point, authentication gateway and authorization
middleware — and holds the three answers to a real request (anonymous, session,
impersonation), the unguarded-endpoint failure, the missing-gateway failure and
the open declaration on a guarded endpoint.

From the module directory:

```
go -C pkg test ./feature/...
go -C pkg vet ./feature/...
golangci-lint run ./feature/...
```
