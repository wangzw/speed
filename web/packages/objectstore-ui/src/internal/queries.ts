/**
 * The object page's reads and writes, over the generated objectstore
 * operations of @speed/api-sdk.
 *
 * Every read runs on the host's QueryClient, under a tenant-namespaced
 * key: the generated constructor's key is wrapped with the framework's
 * tenantQueryKey (auth-core's useTenantQueryKey), so the tenant id is the
 * key's outermost segment and switching tenants never serves the previous
 * tenant's rows. The read's `enabled` gate is the same tenant id: with no
 * tenant (no provider, or a null one) the read does not fire, failing
 * closed rather than reading a tenant-less object store. The generated
 * response models are narrowed through models.ts, and the queryKey
 * constructor is passed beside `select` (orval's option type requires
 * it), keeping the hook's cache entry and every invalidation on one key.
 *
 * The writes are the generated mutations, re-exported so call sites name
 * one module. Invalidation evicts the whole tenant prefix (list and
 * details alike) after a write lands, so the list re-reads the committed
 * rows.
 */

import {
  useQueryClient,
  type UseQueryResult,
} from '@tanstack/react-query'
import {
  getObjectstoreMetadataGetObjectQueryKey,
  getObjectstoreMetadataListObjectsQueryKey,
  objectstoreMetadataGetObjectContent,
  useObjectstoreMetadataCompleteObject,
  useObjectstoreMetadataCreateObject,
  useObjectstoreMetadataDeleteObject,
  useObjectstoreMetadataGetObject,
  useObjectstoreMetadataListObjects,
  useObjectstoreMetadataUploadObjectContent,
} from '@speed/api-sdk'
import type {
  ObjectstoreDeclaration,
  ObjectstoreMetadataListObjectsParams,
  Problem,
} from '@speed/api-sdk'
import { tenantQueryKey, useCurrentTenant, useTenantQueryKey } from '@speed/auth-core'
import { asObjectDetail, asObjectPage } from './models.js'
import type { ObjectDetail, ObjectPage } from './models.js'

/** The list read and the tenant that namespaces it. */
export interface ObjectsListQuery {
  /** The tenant the read is namespaced under, or null (the read is disabled). */
  readonly tenantId: string | null
  readonly query: UseQueryResult<ObjectPage, Problem>
}

/** One page of the tenant's objects (GET /api/v1/objects). */
export function useObjectsListQuery(
  params: ObjectstoreMetadataListObjectsParams,
): ObjectsListQuery {
  const bareKey = getObjectstoreMetadataListObjectsQueryKey(params)
  const { tenantId, queryKey } = useTenantQueryKey(bareKey)
  const query = useObjectstoreMetadataListObjects<ObjectPage, Problem>(params, {
    query: {
      queryKey,
      enabled: tenantId !== null,
      select: asObjectPage,
    },
  })
  return { tenantId, query }
}

/** The detail read and the tenant that namespaces it. */
export interface ObjectDetailQuery {
  /** The tenant the read is namespaced under, or null (the read is disabled). */
  readonly tenantId: string | null
  readonly query: UseQueryResult<ObjectDetail, Problem>
}

/**
 * One object's metadata (GET /api/v1/objects/{id}). The read is disabled
 * when no id is given or no tenant is in play, so a closed dialog fetches
 * nothing.
 */
export function useObjectDetailQuery(id: string | null): ObjectDetailQuery {
  const bareKey = getObjectstoreMetadataGetObjectQueryKey(id ?? '')
  const { tenantId, queryKey } = useTenantQueryKey(bareKey)
  const query = useObjectstoreMetadataGetObject<ObjectDetail, Problem>(
    id ?? '',
    {
      query: {
        queryKey,
        enabled: tenantId !== null && id !== null,
        select: asObjectDetail,
      },
    },
  )
  return { tenantId, query }
}

/** Declares an object and opens its upload window (POST /api/v1/objects). */
export function useCreateObject() {
  return useObjectstoreMetadataCreateObject()
}

/** Stores a blob as the object's bytes (PUT /api/v1/objects/{id}/content). */
export function useUploadObjectContent() {
  return useObjectstoreMetadataUploadObjectContent()
}

/** Moves an object to completed (POST /api/v1/objects/{id}/complete). */
export function useCompleteObject() {
  return useObjectstoreMetadataCompleteObject()
}

/** Removes an object's bytes and tombstones its row (DELETE /api/v1/objects/{id}). */
export function useDeleteObject() {
  return useObjectstoreMetadataDeleteObject()
}

/** Fetches an object's bytes as a Blob (GET /api/v1/objects/{id}/content). */
export function getObjectContent(id: string): Promise<Blob> {
  return objectstoreMetadataGetObjectContent(id)
}

/** Re-reads the current tenant's object subtree after a write lands. */
export function useInvalidateObjects(): () => void {
  const queryClient = useQueryClient()
  const current = useCurrentTenant()
  const tenantId = current?.tenantId ?? null
  return () => {
    if (tenantId === null) {
      return
    }
    void queryClient.invalidateQueries({ queryKey: tenantQueryKey(tenantId) })
  }
}

/** The declaration an upload starts from a picked file. */
export function declarationForFile(file: File): ObjectstoreDeclaration {
  return {
    name: file.name,
    size: file.size,
    content_type: file.type !== '' ? file.type : 'application/octet-stream',
  }
}

export type { ObjectstoreMetadataListObjectsParams }
