# @speed/objectstore-ui

The tenant object store's face in the browser: one page over the
`objectstore` backend capability, plus the upload and download
orchestrations a host can compose into its own surface. It lists the
tenant's stored objects, uploads files (declare -> upload the bytes ->
complete), downloads them back, opens a per-object details dialog and
deletes them behind a confirmation -- every call through the generated
operations of `@speed/api-sdk`, on the host's QueryClient, under a
tenant-namespaced key.

## Install and register

```ts
import {
  OBJECTSTORE_UI_NAMESPACE,
  objectstoreUiResources,
  ObjectsPage,
} from '@speed/objectstore-ui'
```

Register the namespace once at bootstrap, beside `ui-kit`'s:

```ts
registerNamespace(i18n, OBJECTSTORE_UI_NAMESPACE, objectstoreUiResources)
```

The page renders under the host's tenant context and QueryClient (the same
ones `@speed/product-shell/bootstrap` assembles); mount it at a route of
your choosing:

```tsx
<Route path="/objects" view={() => <ObjectsPage />} label={...} />
```

The host's current tenant flows from `@speed/auth-core`'s `TenantProvider`
and the client's `tenantProvider` (the same value the shell announces as
`X-Tenant`); without a tenant the page's reads stay disabled.

## The page

| Part | What it does |
|---|---|
| List | `GET /api/v1/objects`, cursor-paginated (`next_cursor`); Previous/Next page through the cursor stack. The first column is the declared name, one line with truncation, or the "unnamed" copy when the module stored none |
| Upload | `@speed/ui-kit`'s `FileUploader`; each picked file runs declare (`POST /objects`) -> upload (`PUT /objects/{id}/content`) -> complete (`POST /objects/{id}/complete`) |
| Download | `GET /api/v1/objects/{id}/content` as a Blob, saved under the stored name, or named after the id with a MIME-derived extension when the name is empty |
| Details | `GET /api/v1/objects/{id}` in a dialog, carrying the full name |
| Delete | `DELETE /api/v1/objects/{id}` behind a danger `ConfirmDialog` |

`ObjectsPage` takes these optional props:

| Prop | Effect |
|---|---|
| `pageSize` | The list's requested page size; the module applies its own default and upper bound |
| `upload` | `false` hides the upload surface. An object passes its `accept` / `multiple` to `FileUploader`. Omitted, the surface keeps its default shape: several files per selection, any content type |
| `rowActions` | `(object) => ReactNode` appended to the row's existing actions cell, after the built-in download, details and delete controls. No column is added |

The refresh control is `@speed/ui-kit`'s `RefreshButton` over `usePageRefresh`, labelled with this package's `page.refresh` copy.

## Upload and download hooks

A host that renders its own surface over the same routes composes the two
orchestrations instead of the page:

```ts
import { useObjectDownload, useObjectUploads } from '@speed/objectstore-ui'

const uploads = useObjectUploads({
  onUploaded: (object, file) => { /* the completed object and its File */ },
})
// uploads.rows / selectFiles / retry / remove plug straight into <FileUploader>
const download = useObjectDownload()
await download(object) // reads the content route and saves the bytes
```

`onUploaded` receives the completing call's response object and the `File`
that produced it, after the three-step transfer lands. The latest function
passed is the one invoked -- a re-render mid-transfer swaps the callback
without restarting the queue or dropping the transfer in flight -- so an
inline arrow is safe. A callback that throws does not reopen the settled
row: the transfer has already landed and its promise stays resolved, so the
host handles its own failure inside the callback.

`useObjectUploads` is tenant-scoped through the host's `@speed/auth-core`
`TenantProvider`: render it under that provider, because it namespaces its
post-upload invalidation by the context's tenant. `useObjectDownload` reads
no React tenant context -- its generated operation travels through the
client the host bound, whose `tenantProvider` announces the current tenant,
so it needs no provider of its own.

## Notes

- The object view carries the declaration's `name` after the module
  normalized it once (no path separators, control characters or leading/
  trailing whitespace, at most 255 bytes). The list shows it as text; empty
  names render the "unnamed" copy, and a download falls back to the id plus a
  MIME-derived extension.
- The objectstore refusals travel as RFC 9457 problem objects (a `type`,
  no `code`), which the api-client maps to the module code derived from
  that `type`: `objectstore.bad_request` / `objectstore.not_found` /
  `objectstore.conflict`, and `authz.forbidden` for a refused write. The
  namespace's error text resolves each to its own bilingual leaf (plus
  the transport codes).
- A details read answered with the typed `objectstore.not_found` 404
  renders a "no longer available" placeholder without a retry: the server
  answered and the object is gone. The generic load-failure copy names the
  store failing to complete the request rather than silence, so a 429 or
  5xx reads as a retryable answer instead of no answer at all.
- Everything the page renders is bilingual (`zh-CN` + `en-US`) through the
  `objectstore-ui` namespace; override wording by registering your own
  bundle under the same namespace.

## License

Private, unpublished workspace package.
