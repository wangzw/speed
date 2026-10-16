/**
 * The object storage page: @speed/objectstore-ui's page at the tenant area's
 * /objects route.
 *
 * The capability UI package ships the page itself -- the object list, the
 * upload surface over ui-kit's FileUploader, the details dialog, download
 * and the confirmed delete -- as one component over the generated
 * objectstore operations. The host's part is the route (src/routes.tsx) and
 * the navigation label; this file is the seam between them, so the route
 * table names a page module rather than importing the package inline, the
 * same shape the account and home pages take.
 *
 * The page reads the current tenant from the context the tenant shell mounts
 * (src/app.tsx), so every object query is namespaced under the tenant the
 * viewer is acting in, and every request announces it through the client's
 * tenantProvider.
 */

import type { ReactElement } from 'react'
import { ObjectsPage } from '@speed/objectstore-ui'

/** The tenant area's object storage page. */
export function ObjectStoreHostPage(): ReactElement {
  return <ObjectsPage />
}
