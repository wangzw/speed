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
 * to succeeded and re-reads the tenant's object list; a failure at any
 * step flips it to failed with a host-written message and leaves the
 * earlier step's declaration in place (the module's own semantics: a
 * declared object whose upload failed stays pending until its window
 * closes).
 *
 * The upload's byte leg is the generated content operation with the File
 * as its body -- a Blob, which the api-client transport sends verbatim
 * (never JSON-serialized), so the bytes reach the store unchanged. The
 * transfer runs in host code, exactly as FileUploader's contract
 * requires; the component never sees the network.
 */

import { useRef, useState } from 'react'
import type { FileUploaderRow } from '@speed/ui-kit'
import {
  declarationForFile,
  useCompleteObject,
  useCreateObject,
  useInvalidateObjects,
  useUploadObjectContent,
} from './queries.js'
import { useObjectstoreUiTranslation } from './translation.js'

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

/** Owns the upload queue and drives each file's three-step transfer. */
export function useObjectUploads(): ObjectUploads {
  const { t } = useObjectstoreUiTranslation()
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

  const settle = (rowId: string, patch: Partial<FileUploaderRow>): void => {
    setRows((current) =>
      current.map((row) => (row.id === rowId ? { ...row, ...patch } : row)),
    )
  }

  const runTransfer = async (rowId: string, file: File): Promise<void> => {
    settle(rowId, { status: 'uploading', progress: undefined, error: undefined })
    try {
      const created = await create.mutateAsync({ data: declarationForFile(file) })
      const id = created.object.id
      await upload.mutateAsync({ id, data: file })
      await complete.mutateAsync({ id })
      settle(rowId, { status: 'succeeded' })
      invalidate()
    } catch {
      settle(rowId, { status: 'failed', error: t('upload.rowError') })
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
