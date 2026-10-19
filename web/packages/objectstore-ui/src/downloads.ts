/**
 * The download orchestration: one object's bytes, fetched and saved.
 *
 * `useObjectDownload` returns the async operation a surface invokes for a
 * row's download control: read the object's bytes through the generated
 * content operation (GET /objects/{id}/content, resolved as a Blob),
 * derive the file's name from the object view -- the module's normalized
 * name when it has one, otherwise the object id plus a MIME-derived
 * extension -- and hand the bytes to the browser under that name. A
 * failure rejects with the transport's ApiError, so the caller reports it
 * through its own feedback scope beside its other page writes.
 *
 * The hook is tenant-scoped through the client the host bound: the
 * generated operation announces the host's current tenant through that
 * client's `tenantProvider`, which the host wires from the same
 * `TenantProvider` context the object page reads its query keys from. It
 * reads no context of its own and performs no network work besides the
 * one generated operation.
 */

import { useCallback } from 'react'
import type { ObjectstoreObject } from '@speed/api-sdk'
import { getObjectContent } from './internal/queries.js'
import { saveBlob } from './internal/download.js'
import { downloadFilename } from './internal/format.js'

/** Returns the operation that fetches and saves one object's bytes. */
export function useObjectDownload(): (
  object: ObjectstoreObject,
) => Promise<void> {
  return useCallback(async (object) => {
    const blob = await getObjectContent(object.id)
    saveBlob(blob, downloadFilename(object.name, object.id, object.mime))
  }, [])
}
