/**
 * The upload queue's orchestration: host-owned rows and the three-step
 * transfer each picked file runs.
 *
 * ui-kit's FileUploader is fully controlled -- it renders the rows the
 * host hands it and reports picks, retries and removals up. This hook is
 * the host side of that contract: it owns the row list (one row per
 * picked file, stable across re-renders), keeps the picked File beside
 * each row so a retry can re-run without re-picking, and drives the
 * objectstore's three-step upload over the generated operations: declare
 * (POST /objects) -> upload the bytes (PUT /objects/{id}/content) ->
 * complete (POST /objects/{id}/complete). A landed transfer flips the row
 * to succeeded, re-reads the tenant's object list and reports the
 * completed object (with the File that produced it) through `onUploaded`;
 * a failure at any step flips the row to failed with that failure's code
 * resolved through the namespace's error text (a non-ApiError throw
 * collapses to the unknown fallback) and leaves the earlier step's
 * declaration in place (the module's own semantics: a declared object
 * whose upload failed stays pending until its window closes).
 *
 * The hook is tenant-scoped. It must run under the host's
 * `TenantProvider`: the settled transfer invalidates the query subtree of
 * the tenant that context names, and the generated operations travel
 * through the client the host bound, whose `tenantProvider` announces the
 * same tenant as the request header.
 *
 * The upload's byte leg is the generated content operation with the File
 * as its body -- a Blob, which the api-client transport sends verbatim
 * (never JSON-serialized), so the bytes reach the store unchanged. The
 * transfer runs in host code, exactly as FileUploader's contract
 * requires; the component never sees the network.
 */

import { useRef, useState } from 'react'
import { errorCodeOf } from '@speed/ui-kit'
import type { FileUploaderRow } from '@speed/ui-kit'
import type { ObjectstoreObject } from '@speed/api-sdk'
import {
  declarationForFile,
  useCompleteObject,
  useCreateObject,
  useInvalidateObjects,
  useUploadObjectContent,
} from './internal/queries.js'
import { useObjectstoreUiErrorText } from './internal/error-text.js'

/** The upload surface's controlled state and its callbacks. */
export interface ObjectUploads {
  /** The rows to render, oldest first. */
  readonly rows: readonly FileUploaderRow[]
  /** Starts a transfer for each picked or dropped file. */
  readonly selectFiles: (files: readonly File[]) => void
  /** Re-runs a failed row's transfer. */
  readonly retry: (rowId: string) => void
  /** Drops a settled row from the queue. */
  readonly remove: (rowId: string) => void
}

/** Options for `useObjectUploads`. */
export interface UseObjectUploadsOptions {
  /**
   * Called once a transfer completes, with the completed object's view
   * and the File that produced it. The latest function passed is the one
   * invoked, so an inline arrow does not restart the queue. A callback
   * that throws does not reopen the settled row: the transfer has already
   * landed and its promise stays resolved, so the host handles its own
   * failure inside the callback.
   */
  readonly onUploaded?: (object: ObjectstoreObject, file: File) => void
}

/** Owns the upload queue and drives each file's three-step transfer. */
export function useObjectUploads(
  options: UseObjectUploadsOptions = {},
): ObjectUploads {
  const resolveErrorText = useObjectstoreUiErrorText()
  const create = useCreateObject()
  const upload = useUploadObjectContent()
  const complete = useCompleteObject()
  const invalidate = useInvalidateObjects()
  const [rows, setRows] = useState<readonly FileUploaderRow[]>([])
  // A monotonic row id: the row survives re-renders and a retry keys on it.
  const sequence = useRef(0)
  // The picked File behind each row, so a retry re-runs without re-picking
  // (FileUploader's contract never lets the component hold a File).
  const files = useRef(new Map<string, File>())
  // The latest callback: an inline arrow identity changes every render and
  // must not restart anything the queue already holds.
  const onUploaded = useRef(options.onUploaded)
  onUploaded.current = options.onUploaded

  const settle = (rowId: string, patch: Partial<FileUploaderRow>): void => {
    setRows((current) =>
      current.map((row) => (row.id === rowId ? { ...row, ...patch } : row)),
    )
  }

  const runTransfer = async (rowId: string, file: File): Promise<void> => {
    settle(rowId, { status: 'uploading', progress: undefined, error: undefined })
    let completed: ObjectstoreObject | null = null
    try {
      const created = await create.mutateAsync({ data: declarationForFile(file) })
      const id = created.object.id
      await upload.mutateAsync({ id, data: file })
      completed = (await complete.mutateAsync({ id })).object
      settle(rowId, { status: 'succeeded' })
      invalidate()
    } catch (error) {
      settle(rowId, {
        status: 'failed',
        error: resolveErrorText(errorCodeOf(error)),
      })
      return
    }
    // A host callback that throws must not turn the landed transfer into a
    // failed row: the transfer already settled above. The callback is the
    // queue's last step, so its failure stops at its own boundary -- the
    // package neither reopens the row nor routes the exception, and the
    // transfer's promise stays resolved. A host that needs to react to its
    // own failure handles it inside the callback.
    if (completed !== null) {
      try {
        onUploaded.current?.(completed, file)
      } catch {
        // Intentionally swallowed; see above.
      }
    }
  }

  const selectFiles = (selected: readonly File[]): void => {
    const added: FileUploaderRow[] = []
    for (const file of selected) {
      sequence.current += 1
      const rowId = `upload-${sequence.current}`
      files.current.set(rowId, file)
      added.push({ id: rowId, name: file.name, status: 'uploading' })
    }
    setRows((current) => [...current, ...added])
    for (const row of added) {
      const file = files.current.get(row.id)
      if (file !== undefined) {
        void runTransfer(row.id, file)
      }
    }
  }

  const retry = (rowId: string): void => {
    const file = files.current.get(rowId)
    if (file !== undefined) {
      void runTransfer(rowId, file)
    }
  }

  const remove = (rowId: string): void => {
    files.current.delete(rowId)
    setRows((current) => current.filter((row) => row.id !== rowId))
  }

  return { rows, selectFiles, retry, remove }
}
