/**
 * Public entry of @speed/objectstore-ui.
 *
 * The tenant object store's face in the browser: `ObjectsPage` renders the
 * capability's routes as one page -- a cursor-paginated list of the
 * tenant's stored objects, an upload surface over ui-kit's FileUploader
 * that runs the module's three-step transfer (declare via POST /objects,
 * upload the bytes via PUT /objects/{id}/content, complete via
 * POST /objects/{id}/complete), a per-object details dialog reading
 * GET /objects/{id}, a download of GET /objects/{id}/content, and a
 * confirmed delete via DELETE /objects/{id}. Every built-in string renders
 * from the bilingual objectstore-ui namespace
 * (OBJECTSTORE_UI_NAMESPACE + objectstoreUiResources, which the host
 * registers alongside ui-kit's).
 *
 * The page is controlled by the host's composition, not by the package:
 * this package imports no router and owns no path. It reads the current
 * tenant from auth-core's tenant context (the host mounts the tenant
 * provider), namespacing every query key under it, and its reads and
 * writes go through the generated objectstore operations of @speed/api-sdk
 * over the host's QueryClient -- nothing here builds a client, attaches a
 * session, reads storage, navigates or touches the network directly. The
 * upload's byte leg is the generated content operation with the picked File
 * as its body, which the transport sends verbatim. Helpers shared between
 * the components live in src/internal/ and are deliberately not exported.
 *
 * What the object view carries is what the page can show: the routes answer
 * an object's `id`, `name`, `state`, `size`, `mime`, `checksum_sha256`,
 * `expires_at` and timestamps. The name is the label the module normalized
 * once at declaration; the list shows it in its own one-line column (the
 * "unnamed" copy when it is empty), the details dialog shows it in full, and
 * a downloaded file is named after it, falling back to the object id with a
 * MIME-derived extension when it is empty.
 */

export { OBJECTSTORE_UI_NAMESPACE, objectstoreUiResources } from './resources.js'
export { ObjectsPage, type ObjectsPageProps } from './ObjectsPage.js'
export {
  ObjectDetailDialog,
  type ObjectDetailDialogProps,
} from './ObjectDetailDialog.js'
