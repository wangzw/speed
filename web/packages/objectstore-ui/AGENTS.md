# AGENTS.md for `@speed/objectstore-ui`

The tenant object store's face in the browser: the objectstore backend
capability's one page. `ObjectsPage` composes the whole surface -- a
cursor-paginated list of the tenant's stored objects, an upload surface
over `@speed/ui-kit`'s `FileUploader` that runs the module's three-step
transfer, a per-object details dialog, a download and a confirmed delete
-- over the generated `objectstore` operations of `@speed/api-sdk`,
reading the current tenant from `@speed/auth-core`'s tenant context.

## What this package is

- **A capability UI package, one per backend capability directory.** It is
  the browser face of `pkg/objectstore`: the module's routes (`/api/v1/objects`
  and its content, complete, list, get and delete routes) rendered as one
  page. Same-tier packages never import one another, and a capability UI
  package depends only on the layers below it: `ui-kit`, `i18n`,
  `api-sdk` and `auth-core` (the tenant-scoped query key is auth-core's
  own fact).
- **The generated-hooks tier, consumed.** Every read is a generated
  react-query hook and every write a generated mutation, selected through
  this package's response-model narrowing (`src/internal/models.ts`) and
  run under a tenant-namespaced key (`src/internal/queries.ts`,
  auth-core's `useTenantQueryKey`). The page builds no `QueryClient` and
  writes no query key by hand; the host owns the one client.
- **The tenant is the current tenant of the host's context.** The page
  reads it from `TenantProvider` and namespaces every read's key under it,
  so a switch never serves the previous tenant's rows; the request itself
  announces the tenant through the client's own `tenantProvider`
  (transport, not a claim of the token). With no tenant in play the reads
  stay disabled, failing closed rather than reading a tenant-less store.
- **The upload's byte leg moves real bytes.** The three-step transfer is
  declare (`POST /objects`, a JSON body of `{name, size, content_type}`)
  -> upload the bytes (`PUT /objects/{id}/content`, the picked `File` as a
  Blob body) -> complete (`POST /objects/{id}/complete`). The Blob body
  travels through the api-client transport verbatim (never
  JSON-serialized), which is what the transport's binary-body path exists
  for; the download reads `GET /objects/{id}/content` as a Blob through
  the transport's `responseType: 'blob'` path.
- **Every built-in string is bilingual and bundled.** All text renders
  from the `objectstore-ui` namespace (`OBJECTSTORE_UI_NAMESPACE`, one
  `zh-CN.json` and one `en-US.json` under `src/locales/`, identical leaf
  keys per language), registered by the host exactly once through
  `registerNamespace`. Dates and numbers go through `Intl` (UTC for
  instants, the byte size's number through `Intl.NumberFormat` with the
  unit from the bundle); code-level failures resolve through the
  namespace's error text.
- **It composes ui-kit, it does not re-implement it.** The list is
  `DataTable`, the async states are `AsyncSection` (with a permission-refusal
  branch and, in the details dialog, an object-gone branch for a typed
  `objectstore.not_found` 404, with no retry), the queue is
  `FileUploader`, the delete is `ConfirmDialog`, the failures are
  `InlineError`. The page never lays out its own spinner, empty block or
  alert.

## What it is not

- **No routing, no navigation, no path.** The host mounts `ObjectsPage` at
  a route of its choosing (the demo host mounts it at `/objects` with a nav
  label); the package imports no router.
- **No session, no storage, no token.** It never reads the access token,
  writes storage or drives a session; the client the host bound carries
  the credential.
- **No HTTP of its own.** Every call is a generated operation; the package
  contains no `fetch`/`axios` (the workspace's `speed/no-direct-http`
  rule enforces it) and never constructs a client.
- **The declaration name is shown as the module stored it.** The object view
  carries the `name` the module normalized once at declaration. The list has a
  one-line name column with truncation (the "unnamed" copy when it is empty);
  the details dialog carries the full value; and a download is named after it,
  falling back to the object `id` with a MIME-derived extension when it is
  empty. The name is rendered as a text node, never as HTML and never into a
  path.

## Its neighbors

| Dependency | Why |
|---|---|
| `@speed/api-sdk` | the generated objectstore operations and response models |
| `@speed/auth-core` | `TenantProvider`/`useCurrentTenant`, `useTenantQueryKey` and `tenantQueryKey` |
| `@speed/ui-kit` | the composed components (DataTable, FileUploader, AsyncSection, ConfirmDialog, InlineError, PageHeader, ListSkeleton) |
| `@speed/i18n` | the namespace, the translation hook and the error-text resolver |

`react`, `react-dom`, `@mui/material`, `@emotion/*` and `@tanstack/react-query`
are peer dependencies (the host renders the tree and owns the QueryClient).

## Checks

Run from this directory, or from `web/` with `pnpm --filter @speed/objectstore-ui <script>`:

```
pnpm lint
pnpm typecheck
pnpm test
pnpm build
```

## Conventions

- **English only** for comments and TSDoc; the design conclusion lives in
  `docs/design/modules/web/design-web-objectstore-ui.md` (Chinese).
- Comments describe the current state only.
- One test file per source file, co-located; shared test scaffolding lives
  in `test-utils/`.
