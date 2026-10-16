# @speed/objectstore-ui

The tenant object store's face in the browser: one page over the
`objectstore` backend capability. It lists the tenant's stored objects,
uploads files (declare -> upload the bytes -> complete), downloads them
back, opens a per-object details dialog and deletes them behind a
confirmation -- every call through the generated operations of
`@speed/api-sdk`, on the host's QueryClient, under a tenant-namespaced key.

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
| List | `GET /api/v1/objects`, cursor-paginated (`next_cursor`); Previous/Next page through the cursor stack |
| Upload | `@speed/ui-kit`'s `FileUploader`; each picked file runs declare (`POST /objects`) -> upload (`PUT /objects/{id}/content`) -> complete (`POST /objects/{id}/complete`) |
| Download | `GET /api/v1/objects/{id}/content` as a Blob, saved named after the id with a MIME-derived extension |
| Details | `GET /api/v1/objects/{id}` in a dialog |
| Delete | `DELETE /api/v1/objects/{id}` behind a danger `ConfirmDialog` |

`ObjectsPage` takes an optional `pageSize` (the module applies its own
default and upper bound).

## Notes

- The object view carries no declaration name (the module stores none), so
  the list shows the object's `id`, not the uploaded file's name.
- The objectstore refusals travel as RFC 9457 problem objects (a `type`,
  no `code`), which the api-client maps to the module code derived from
  that `type`: `objectstore.bad_request` / `objectstore.not_found` /
  `objectstore.conflict`, and `authz.forbidden` for a refused write. The
  namespace's error text resolves each to its own bilingual leaf (plus
  the transport codes).
- Everything the page renders is bilingual (`zh-CN` + `en-US`) through the
  `objectstore-ui` namespace; override wording by registering your own
  bundle under the same namespace.

## License

Private, unpublished workspace package.
