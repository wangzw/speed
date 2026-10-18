/**
 * The README usage example, compiled and executed by the suite.
 *
 * The suite executes BOTH documented compositions: the README's Quick
 * start composes host pages from the public exports and registers the
 * ui-kit namespace at bootstrap, and its "Write feedback" section
 * composes the write-feedback scope. This file renders both through the
 * same host tree (renderWithProviders) and asserts what the pages show,
 * so the documented usage cannot drift from the API -- the package suite
 * compiles and runs them. The upload panel
 * the Quick start documents joins the members page on the same host
 * screen: the host owns the queue as `rows` state and every File it
 * picked (a retry hands the same bytes back), runs each transfer in its
 * own transport code -- one logical POST per file against the labelled
 * placeholder URL, answered by a scripted fetch stub returning genuine
 * Response objects -- and patches row statuses and progress into the
 * state FileUploader renders. FileUploader itself holds no rows, no
 * Files and no transport: every abort (cancel, unmount) is the host's
 * AbortController doing the aborting. The stub stands in for the host's
 * generated api-sdk storage call: the merged API document ships no
 * storage fragment, so no generated storage operation exists and ui-kit
 * itself ships no endpoint -- the placeholder URL below is labelled as
 * such.
 *
 * Host-content strings (titles, headers, the transport's error text) are
 * English fixtures on purpose: they stand in for a host's own
 * translations and are data in a test file (exempt from the
 * no-literal-text rule), not rendered product text. Assertions derive
 * every built-in string from the locale bundles, never inline
 * translations.
 */

import { readFileSync } from 'node:fs'
import { join } from 'node:path'
import { act, fireEvent, within } from '@testing-library/react'
import { useEffect, useRef, useState } from 'react'
import { afterEach, describe, expect, it, vi } from 'vitest'
import {
  CLIENT_TRANSPORT_ERROR_CODES,
  createErrorTextResolver,
  registerNamespace,
  switchLanguage,
  useTranslation,
} from '@speed/i18n'
import Button from '@mui/material/Button'
import TextField from '@mui/material/TextField'
import zhCN from './locales/zh-CN.json' with { type: 'json' }
import enUS from './locales/en-US.json' with { type: 'json' }
import { createUiKitI18n, renderWithProviders } from '../test-utils/render.js'
import { expectNoAxeViolations } from '../test-utils/axe.js'
import { selectFiles } from '../test-utils/file-input.js'
import { ConfirmDialog } from './components/ConfirmDialog.js'
import { DataTable } from './components/DataTable.js'
import type { DataTableColumn, DataTableSort } from './components/DataTable.js'
import { EmptyState } from './components/EmptyState.js'
import { FileUploader } from './components/FileUploader.js'
import type { FileUploaderRow } from './components/FileUploader.js'
import { PageHeader } from './components/PageHeader.js'
import { useWriteFeedback, WriteFeedbackRegion } from './index.js'

interface Member {
  id: number
  name: string
  credits: number
}

const MEMBERS: readonly Member[] = [
  { id: 1, name: 'Ada', credits: 120 },
  { id: 2, name: 'Grace', credits: 80 },
  { id: 3, name: 'Katherine', credits: 240 },
]

const COLUMNS: readonly DataTableColumn<Member>[] = [
  { id: 'name', header: 'Name', sortable: true, cell: (row) => row.name },
  {
    id: 'credits',
    header: 'Credits',
    align: 'right',
    cell: (row) => String(row.credits),
  },
]

/**
 * The README's example host page: it owns every piece of state the
 * components report through props -- sort, selection, filter and
 * pagination are all controlled from here.
 */
function MembersPage() {
  const [sort, setSort] = useState<DataTableSort | null>(null)
  const [selected, setSelected] = useState<readonly (string | number)[]>([])
  const [query, setQuery] = useState('')
  const visible = MEMBERS.filter((member) =>
    member.name.toLowerCase().includes(query.toLowerCase()),
  )
  return (
    <>
      <PageHeader
        title="Members"
        description="Everyone with access to this workspace."
      />
      <DataTable
        rows={visible}
        columns={COLUMNS}
        rowKey={(row) => row.id}
        sort={sort}
        onSortChange={setSort}
        selectedRowKeys={selected}
        onSelectionChange={setSelected}
        filter={{ value: query, onValueChange: setQuery }}
        pagination={{
          page: 0,
          rowsPerPage: 10,
          count: MEMBERS.length,
          onPageChange: vi.fn(),
          onRowsPerPageChange: vi.fn(),
        }}
      />
    </>
  )
}

/**
 * The upload endpoint the host's transport sends its one round trip
 * per file to. A real host's upload code calls a generated api-sdk
 * storage operation here -- the merged API document ships no storage
 * fragment, so no such operation exists -- and this is an explicitly
 * labelled fixture placeholder: ui-kit ships no endpoint, and the suite
 * pins the transport shape without pretending to know a wire protocol.
 */
const STORAGE_UPLOAD_URL = 'https://uploads.example.test/objects'

/** True when the given rejection is an abort signal's AbortError. */
function isAbortError(error: unknown): boolean {
  if (error instanceof DOMException) {
    return error.name === 'AbortError'
  }
  return error instanceof Error && error.name === 'AbortError'
}

/**
 * The host's upload transport: one logical POST of the picked bytes per
 * file to the host's storage endpoint, reporting progress slices around
 * the round trip and translating an unsuccessful answer into the row's
 * error text. The fetch is the host's own call -- FileUploader never
 * sees a URL, a signal or a body.
 */
async function uploadToStorage(
  file: File,
  signal: AbortSignal,
  onProgress: (fraction: number) => void,
): Promise<void> {
  onProgress(0.25)
  const body = await file.arrayBuffer()
  const response = await fetch(STORAGE_UPLOAD_URL, {
    method: 'POST',
    headers: { 'content-type': file.type },
    body,
    signal,
  })
  if (!response.ok) {
    throw new Error(`The upload answered ${response.status}.`)
  }
  onProgress(0.75)
}

/**
 * The example upload panel: the host that owns the queue. `rows` is host
 * state -- FileUploader renders exactly what this component writes --
 * and each picked File stays in a host map, because a retry hands the
 * same bytes back to the transport. One AbortController per transfer
 * gives the host both cancellations (abort + drop the row) and the
 * unmount abort; FileUploader itself contributes no state, no fetch and
 * no transfer.
 */
function UploadPanel() {
  const [rows, setRows] = useState<readonly FileUploaderRow[]>([])
  const filesRef = useRef(new Map<string, File>())
  const controllersRef = useRef(new Map<string, AbortController>())
  const nextIdRef = useRef(1)

  // Leaving the screen aborts every transfer this panel owns.
  useEffect(() => {
    const controllers = controllersRef.current
    return () => {
      for (const controller of controllers.values()) {
        controller.abort()
      }
    }
  }, [])

  const patchRow = (id: string, patch: Partial<FileUploaderRow>): void => {
    setRows((current) =>
      current.map((row) => (row.id === id ? { ...row, ...patch } : row)),
    )
  }

  const dropRow = (id: string): void => {
    setRows((current) => current.filter((row) => row.id !== id))
  }

  const transfer = (
    id: string,
    file: File,
    controller: AbortController,
  ): void => {
    uploadToStorage(file, controller.signal, (progress) =>
      patchRow(id, { progress }),
    )
      .then(() => {
        controllersRef.current.delete(id)
        filesRef.current.delete(id)
        patchRow(id, { status: 'succeeded' })
      })
      .catch((error: unknown) => {
        controllersRef.current.delete(id)
        if (isAbortError(error)) {
          // Cancelled (its row was already dropped) or the panel
          // unmounted: there is nothing left to render.
          return
        }
        patchRow(id, {
          status: 'failed',
          error: error instanceof Error ? error.message : String(error),
        })
      })
  }

  const handleSelectFiles = (files: readonly File[]): void => {
    for (const file of files) {
      const id = `upload-${nextIdRef.current}`
      nextIdRef.current += 1
      filesRef.current.set(id, file)
      const controller = new AbortController()
      controllersRef.current.set(id, controller)
      setRows((current) => [
        ...current,
        { id, name: file.name, status: 'uploading' },
      ])
      transfer(id, file, controller)
    }
  }

  const handleCancel = (id: string): void => {
    controllersRef.current.get(id)?.abort()
    filesRef.current.delete(id)
    dropRow(id)
  }

  const handleRetry = (id: string): void => {
    const file = filesRef.current.get(id)
    if (file === undefined) {
      return
    }
    const controller = new AbortController()
    controllersRef.current.set(id, controller)
    patchRow(id, {
      status: 'uploading',
      progress: undefined,
      error: undefined,
    })
    transfer(id, file, controller)
  }

  const handleRemove = (id: string): void => {
    filesRef.current.delete(id)
    dropRow(id)
  }

  return (
    <FileUploader
      multiple
      rows={rows}
      onSelectFiles={handleSelectFiles}
      onCancel={handleCancel}
      onRetry={handleRetry}
      onRemove={handleRemove}
    />
  )
}

describe('README usage example', () => {
  it('renders the quick-start composition with the zh-CN built-in texts', async () => {
    const utils = renderWithProviders(<MembersPage />)
    expect(
      utils.getByRole('heading', { level: 1, name: 'Members' }),
    ).toBeInTheDocument()
    expect(
      utils.getByText('Everyone with access to this workspace.'),
    ).toBeInTheDocument()
    expect(
      utils.getByRole('columnheader', { name: 'Name' }),
    ).toBeInTheDocument()
    expect(utils.getByText('Ada')).toBeInTheDocument()
    expect(utils.getByText('Katherine')).toBeInTheDocument()
    expect(
      utils.getByLabelText(zhCN.dataTable.selectAllRows),
    ).toBeInTheDocument()
    expect(utils.getByText(zhCN.dataTable.rowsPerPage)).toBeInTheDocument()
    await expectNoAxeViolations()
  })

  it('follows a language switch into the en-US built-in texts', async () => {
    const utils = renderWithProviders(<MembersPage />)
    expect(
      utils.getByLabelText(zhCN.dataTable.selectAllRows),
    ).toBeInTheDocument()
    await act(async () => {
      await switchLanguage(utils.i18n, 'en-US')
    })
    expect(
      utils.getByLabelText(enUS.dataTable.selectAllRows),
    ).toBeInTheDocument()
    expect(utils.getByText(enUS.dataTable.rowsPerPage)).toBeInTheDocument()
    expect(
      utils.queryByLabelText(zhCN.dataTable.selectAllRows),
    ).not.toBeInTheDocument()
    expect(utils.queryByText(zhCN.dataTable.rowsPerPage)).not.toBeInTheDocument()
  })

  it('documents the write-feedback resolver composition the suite executes', () => {
    // The package's test commands run with the package root as the working
    // directory (`pnpm test` here, or `pnpm --filter @speed/ui-kit test`).
    const readme = readFileSync(join(process.cwd(), 'README.md'), 'utf8')
    const sectionStart = readme.indexOf('### Write feedback')
    expect(sectionStart).toBeGreaterThanOrEqual(0)
    const fenceStart = readme.indexOf('```tsx', sectionStart)
    expect(fenceStart).toBeGreaterThan(sectionStart)
    const blockStart = fenceStart + '```tsx'.length
    const fenceEnd = readme.indexOf('```', blockStart)
    expect(fenceEnd).toBeGreaterThan(blockStart)
    const block = readme.slice(blockStart, fenceEnd)

    // The documented composition resolves failure text through the shared
    // resolver -- the same call the executed TenantNameSection makes.
    expect(block).toContain('createErrorTextResolver(')
    // A hand-built template resolver would render raw `errors.*` keys, so
    // the documented composition must never carry one.
    expect(block).not.toContain('t(`errors.${')
  })

  it('renders the stock empty and confirm states the README shows', async () => {
    const onCancel = vi.fn()
    const utils = renderWithProviders(
      <>
        <DataTable rows={[]} columns={COLUMNS} />
        <EmptyState variant="error" />
        <ConfirmDialog open onConfirm={vi.fn()} onCancel={onCancel} />
      </>,
    )
    expect(utils.getByText(zhCN.emptyState.empty.title)).toBeInTheDocument()
    expect(utils.getByText(zhCN.emptyState.error.title)).toBeInTheDocument()
    fireEvent.click(
      utils.getByRole('button', { name: zhCN.confirmDialog.cancelLabel }),
    )
    expect(onCancel).toHaveBeenCalledTimes(1)
    await expectNoAxeViolations()
  })
})

/**
 * The scripted transport stand-in the documented host transport sends
 * its round trips to: each request is recorded (url, declared content
 * type, body bytes, signal) and parked until the test opens its gate,
 * so every in-flight state is observable; opening a gate answers the
 * parked request with a genuine Response of the given status. An abort
 * on the request's signal rejects it the way fetch rejects an aborted
 * request, so the host's abort paths are exercised for real.
 */
interface ScriptedStorageTransport {
  readonly uploadCalls: readonly RecordedRequest[]
  readonly gates: readonly ((status: number) => void)[]
  readonly pending: readonly Promise<number>[]
  readonly fetchMock: (
    input: RequestInfo | URL,
    init?: RequestInit,
  ) => Promise<Response>
}

interface RecordedRequest {
  readonly url: string
  readonly contentType: string | null
  readonly bytes: ArrayBuffer
  readonly signal: AbortSignal | null
}

function scriptedStorageTransport(): ScriptedStorageTransport {
  const uploadCalls: RecordedRequest[] = []
  const gates: ((status: number) => void)[] = []
  const pending: Promise<number>[] = []
  const fetchMock = vi.fn(
    async (input: RequestInfo | URL, init?: RequestInit): Promise<Response> => {
      uploadCalls.push({
        url:
          typeof input === 'string'
            ? input
            : input instanceof URL
              ? input.href
              : input.url,
        contentType:
          typeof init?.headers === 'object' && init.headers !== null
            ? ((init.headers as Record<string, string>)['content-type'] ?? null)
            : null,
        bytes: init?.body as ArrayBuffer,
        signal: init?.signal ?? null,
      })
      const roundTrip = new Promise<number>((resolve, reject) => {
        const signal = init?.signal
        const onAbort = (): void => {
          signal?.removeEventListener('abort', onAbort)
          // fetch rejects an aborted request with an AbortError; the
          // fixture mirrors that shape with a plain Error carrying the
          // name, so the host's abort handling is the real code path.
          const error = new Error('The operation was aborted.')
          error.name = 'AbortError'
          reject(error)
        }
        if (signal !== null && signal !== undefined) {
          if (signal.aborted) {
            onAbort()
            return
          }
          signal.addEventListener('abort', onAbort)
        }
        gates.push((status) => {
          signal?.removeEventListener('abort', onAbort)
          resolve(status)
        })
      })
      pending.push(roundTrip)
      const status = await roundTrip
      return new Response(
        status === 201
          ? JSON.stringify({ id: 'upload-ok' })
          : JSON.stringify({ error: 'busy' }),
        { status, headers: { 'content-type': 'application/json' } },
      )
    },
  )
  return { uploadCalls, gates, pending, fetchMock }
}

/**
 * The upload half of the README Quick start, executed: the documented
 * UploadPanel on the same host screen as the members page, its journeys
 * driven over the scripted transport stand-in -- one logical round trip
 * per picked file, in the order the files were picked, every state
 * transition patched into the rows the panel owns.
 */
describe('README upload panel', () => {
  afterEach(() => {
    vi.unstubAllGlobals()
  })

  it('runs the documented upload journey over the scripted host transport', async () => {
    const portrait = new File(['portrait scan bytes: 0x0118'], 'portrait.png', {
      type: 'image/png',
    })
    const closeup = new File(['closeup scan bytes: 0x0023'], 'closeup.jpg', {
      type: 'image/jpeg',
    })

    // Request index 1 is closeup.jpg's first attempt: the transport's
    // answer of 503 turns into the row's host-written error text, so the
    // retry leg proves a failed row recovers through the widget's retry
    // affordance. All other requests answer 201.
    const transport = scriptedStorageTransport()
    vi.stubGlobal('fetch', transport.fetchMock)

    // The upload panel shares the host screen with the members page.
    const utils = renderWithProviders(
      <>
        <MembersPage />
        <UploadPanel />
      </>,
    )
    expect(
      utils.getByRole('heading', { level: 1, name: 'Members' }),
    ).toBeInTheDocument()
    expect(utils.getByText('Ada')).toBeInTheDocument()
    // An empty queue renders no rows and no live region.
    expect(utils.queryByRole('listitem')).not.toBeInTheDocument()
    expect(utils.queryByRole('status')).not.toBeInTheDocument()

    // Pick two files: both rows join the host-owned queue and their
    // transfers launch in pick order, each reporting its first progress
    // slice before its round trip goes out.
    const input = utils.getByLabelText(zhCN.fileUploader.chooseFiles) as HTMLInputElement
    await act(async () => {
      selectFiles(input, [portrait, closeup])
    })
    expect(utils.getAllByRole('listitem')).toHaveLength(2)
    const uploadingBars = utils.getAllByRole('progressbar', {
      name: zhCN.fileUploader.statusUploading,
    })
    expect(uploadingBars).toHaveLength(2)
    for (const bar of uploadingBars) {
      expect(bar).toHaveAttribute('aria-valuenow', '25')
    }
    expect(utils.getByRole('status')).toHaveTextContent('')
    expect(transport.uploadCalls).toHaveLength(2)
    const portraitCall = transport.uploadCalls[0]!
    expect(portraitCall.url).toBe(STORAGE_UPLOAD_URL)
    expect(portraitCall.contentType).toBe('image/png')
    expect(new Uint8Array(portraitCall.bytes)).toEqual(
      new Uint8Array(await portrait.arrayBuffer()),
    )
    expect(transport.uploadCalls[1]!.contentType).toBe('image/jpeg')

    // Complete the first round trip: portrait.png settles succeeded and
    // the live region announces it.
    await act(async () => {
      transport.gates[0]!(201)
    })
    expect(utils.getByText(zhCN.fileUploader.statusSucceeded)).toBeInTheDocument()
    expect(utils.getByRole('status')).toHaveTextContent(
      zhCN.fileUploader.announceUploaded.replace('{{name}}', portrait.name),
    )

    // The second round trip answers 503: closeup.jpg fails, showing the
    // host-written error text and the retry affordance.
    await act(async () => {
      transport.gates[1]!(503)
    })
    expect(utils.getByText('The upload answered 503.')).toBeInTheDocument()
    expect(utils.getByText(zhCN.fileUploader.statusFailed)).toBeInTheDocument()
    expect(
      utils.getByRole('button', { name: zhCN.fileUploader.actionRetry }),
    ).toBeInTheDocument()
    expect(utils.getByRole('status')).toHaveTextContent(
      zhCN.fileUploader.announceFailed.replace('{{name}}', closeup.name),
    )
    expect(utils.queryByRole('progressbar')).not.toBeInTheDocument()

    // Retry the failed row: the host hands the same File back to its
    // transport, the row returns to uploading (clearing the live region,
    // so an identical later failure would re-announce) and the third
    // request goes out -- closeup.jpg's bytes, in order.
    fireEvent.click(
      utils.getByRole('button', { name: zhCN.fileUploader.actionRetry }),
    )
    await act(async () => {})
    expect(utils.getByText(zhCN.fileUploader.statusUploading)).toBeInTheDocument()
    expect(
      utils.getByRole('progressbar', { name: zhCN.fileUploader.statusUploading }),
    ).toHaveAttribute('aria-valuenow', '25')
    expect(utils.getByRole('status')).toHaveTextContent('')
    expect(transport.uploadCalls).toHaveLength(3)
    const closeupCall = transport.uploadCalls[2]!
    expect(closeupCall.url).toBe(STORAGE_UPLOAD_URL)
    expect(closeupCall.contentType).toBe('image/jpeg')
    expect(new Uint8Array(closeupCall.bytes)).toEqual(
      new Uint8Array(await closeup.arrayBuffer()),
    )

    await act(async () => {
      transport.gates[2]!(201)
    })
    expect(utils.getAllByText(zhCN.fileUploader.statusSucceeded)).toHaveLength(2)
    expect(utils.getByRole('status')).toHaveTextContent(
      zhCN.fileUploader.announceUploaded.replace('{{name}}', closeup.name),
    )

    // Remove the first row: the queue keeps the second, and the members
    // page next to it never noticed.
    const portraitRow = utils.getByText(portrait.name).closest('li')!
    fireEvent.click(
      within(portraitRow).getByRole('button', {
        name: zhCN.fileUploader.actionRemove,
      }),
    )
    expect(utils.queryByText(portrait.name)).not.toBeInTheDocument()
    expect(utils.getByText(closeup.name)).toBeInTheDocument()
    expect(utils.getByText('Katherine')).toBeInTheDocument()

    // Language switch leg: the remaining row's built-in texts, the picker
    // label and the standing announcement follow the bundle.
    await act(async () => {
      await switchLanguage(utils.i18n, 'en-US')
    })
    expect(utils.getByText(enUS.fileUploader.statusSucceeded)).toBeInTheDocument()
    expect(
      utils.queryByText(zhCN.fileUploader.statusSucceeded),
    ).not.toBeInTheDocument()
    expect(
      utils.getByRole('button', { name: enUS.fileUploader.actionRemove }),
    ).toBeInTheDocument()
    expect(
      utils.getByLabelText(enUS.fileUploader.chooseFiles),
    ).toBeInTheDocument()
    expect(utils.getByRole('status')).toHaveTextContent(
      enUS.fileUploader.announceUploaded.replace('{{name}}', closeup.name),
    )
    await expectNoAxeViolations()
  })

  it('cancels a row mid-flight: the host aborts the transfer and drops the row', async () => {
    const bitewing = new File(['bitewing scan bytes: 0x0091'], 'bitewing.png', {
      type: 'image/png',
    })
    const transport = scriptedStorageTransport()
    vi.stubGlobal('fetch', transport.fetchMock)
    const utils = renderWithProviders(<UploadPanel />)
    const input = utils.getByLabelText(zhCN.fileUploader.chooseFiles) as HTMLInputElement
    await act(async () => {
      selectFiles(input, [bitewing])
    })
    expect(utils.getAllByRole('listitem')).toHaveLength(1)
    expect(transport.uploadCalls).toHaveLength(1)
    expect(transport.uploadCalls[0]!.signal?.aborted).toBe(false)

    // The row's cancel affordance reports up: the host aborts its own
    // controller and drops the row from the queue it owns. The parked
    // round trip rejects with the abort, and the host's transport treats
    // that rejection as the row going away -- nothing renders failed.
    fireEvent.click(
      utils.getByRole('button', { name: zhCN.fileUploader.actionCancel }),
    )
    await act(async () => {})
    expect(transport.uploadCalls[0]!.signal?.aborted).toBe(true)
    expect(utils.queryByRole('listitem')).not.toBeInTheDocument()
    expect(utils.queryByRole('status')).not.toBeInTheDocument()
    await expectAbort(transport.pending[0]!)
  })

  it('aborts in-flight uploads when the panel unmounts', async () => {
    const survey = new File(['survey scan bytes: 0x00a7'], 'survey.png', {
      type: 'image/png',
    })
    const transport = scriptedStorageTransport()
    vi.stubGlobal('fetch', transport.fetchMock)
    const utils = renderWithProviders(<UploadPanel />)
    const input = utils.getByLabelText(zhCN.fileUploader.chooseFiles) as HTMLInputElement
    await act(async () => {
      selectFiles(input, [survey])
    })
    expect(utils.getAllByRole('listitem')).toHaveLength(1)
    expect(transport.uploadCalls).toHaveLength(1)
    expect(transport.uploadCalls[0]!.signal?.aborted).toBe(false)

    // Leaving the screen runs the panel's unmount cleanup: every transfer
    // it owns aborts, and the host transport treats the abort as the row
    // going away with the panel -- nothing re-renders, nothing errors.
    utils.unmount()
    await act(async () => {})
    expect(transport.uploadCalls[0]!.signal?.aborted).toBe(true)
    await expectAbort(transport.pending[0]!)
  })
})

/** Asserts that the parked round trip rejected with the abort signal's error. */
async function expectAbort(promise: Promise<number>): Promise<void> {
  const rejection: unknown = await promise.then(
    () => {
      throw new Error('expected the parked request to abort')
    },
    (error: unknown) => error,
  )
  expect(rejection).toMatchObject({ name: 'AbortError' })
}

const TENANT_NAMESPACE = 'tenant'

// The host's own namespace, written here as the host would write it. The
// zh-CN values are pseudo-translations: each one stays visibly distinct
// from its en-US counterpart, so a case that asserts the message captured
// at succeed time still catches the wrong bundle rendering.
const TENANT_BUNDLES = {
  'zh-CN': {
    save: '[zh-CN] Save',
    nameLabel: '[zh-CN] Tenant name',
    renamed: '[zh-CN] Tenant name updated',
    errors: {
      'tenant.name_required': '[zh-CN] Enter a tenant name.',
      'client.network': '[zh-CN] No network connection. Check your connection and try again.',
      unknown: '[zh-CN] Something went wrong. Please try again later.',
    },
  },
  'en-US': {
    save: 'Save',
    nameLabel: 'Tenant name',
    renamed: 'Tenant name updated',
    errors: {
      'tenant.name_required': 'Enter a tenant name.',
      'client.network': 'No network connection. Check your connection and try again.',
      unknown: 'Something went wrong. Please try again later.',
    },
  },
} as const

/**
 * The read-only whitelist of codes the tenant namespace carries text for:
 * the attempt's own refusal codes plus the transport codes every write can
 * reach. Anything else resolves to `errors.unknown`, so a raw key can
 * never reach the alert.
 */
const TENANT_ERROR_CODES: ReadonlySet<string> = new Set([
  ...CLIENT_TRANSPORT_ERROR_CODES,
  'tenant.name_required',
])

/** What the next renameTenant call does, set by the failure cases. */
let renameFailure: 'network' | 'plain' | null = null
/** The name the last call received, asserted against the README's 'Acme'. */
let renamedName: string | null = null

/**
 * The host's own API call, standing in for the generated api-sdk operation
 * the README composition calls. The composition owns the transport; the
 * write-feedback scope only settles the promise it returns.
 */
async function renameTenant(name: string): Promise<void> {
  renamedName = name
  await Promise.resolve()
  if (renameFailure === 'network') {
    throw { code: 'client.network' }
  }
  if (renameFailure === 'plain') {
    throw new Error('transport exploded')
  }
}

/** The README's write-feedback composition: one scope per control group. */
function TenantNameSection() {
  const feedback = useWriteFeedback()
  const { t } = useTranslation(TENANT_NAMESPACE)
  const [name, setName] = useState('Acme')

  return (
    <section>
      <TextField
        label={t('nameLabel')}
        value={name}
        onChange={(event) => setName(event.target.value)}
      />
      <Button
        loading={feedback.pending}
        disabled={feedback.pending}
        onClick={() => {
          void feedback.run(async () => {
            if (name.trim() === '') {
              throw { code: 'tenant.name_required' }
            }
            await renameTenant(name)
            return t('renamed')
          })
        }}
      >
        {t('save')}
      </Button>
      <WriteFeedbackRegion
        outcome={feedback.outcome}
        resolve={createErrorTextResolver(t, TENANT_ERROR_CODES)}
        sx={{ marginTop: 2 }}
      />
    </section>
  )
}

describe('README write-feedback composition', () => {
  function renderSection() {
    renameFailure = null
    renamedName = null
    const i18n = createUiKitI18n()
    registerNamespace(i18n, TENANT_NAMESPACE, TENANT_BUNDLES)
    return renderWithProviders(<TenantNameSection />, { i18n })
  }

  it('confirms a settled write and keeps the message across a language switch', async () => {
    const utils = renderSection()
    fireEvent.click(
      utils.getByRole('button', { name: TENANT_BUNDLES['zh-CN'].save }),
    )
    await act(async () => {})

    // The documented call passes the field's name through to the host's API
    // operation.
    expect(renamedName).toBe('Acme')
    expect(utils.getByRole('status').textContent).toBe(
      TENANT_BUNDLES['zh-CN'].renamed,
    )

    await act(async () => {
      await switchLanguage(utils.i18n, 'en-US')
    })
    // The success message is the host's translation captured at succeed
    // time; the control labels follow the new language, the message does not.
    expect(utils.getByRole('status').textContent).toBe(
      TENANT_BUNDLES['zh-CN'].renamed,
    )
    expect(
      utils.getByRole('button', { name: TENANT_BUNDLES['en-US'].save }),
    ).toBeInTheDocument()
  })

  it('reports a failed attempt through the host resolver in the active language', async () => {
    const utils = renderSection()
    fireEvent.change(utils.getByLabelText(TENANT_BUNDLES['zh-CN'].nameLabel), {
      target: { value: '' },
    })
    fireEvent.click(
      utils.getByRole('button', { name: TENANT_BUNDLES['zh-CN'].save }),
    )
    await act(async () => {})

    expect(utils.getByRole('alert')).toHaveTextContent(
      TENANT_BUNDLES['zh-CN'].errors['tenant.name_required'],
    )
    expect(utils.getByRole('status').textContent).toBe('')

    await act(async () => {
      await switchLanguage(utils.i18n, 'en-US')
    })
    expect(utils.getByRole('alert')).toHaveTextContent(
      TENANT_BUNDLES['en-US'].errors['tenant.name_required'],
    )
  })

  it('renders the unknown fallback for a plain throw, never a raw key', async () => {
    const utils = renderSection()
    renameFailure = 'plain'
    fireEvent.click(
      utils.getByRole('button', { name: TENANT_BUNDLES['zh-CN'].save }),
    )
    await act(async () => {})

    const alert = utils.getByRole('alert')
    expect(alert).toHaveTextContent(
      TENANT_BUNDLES['zh-CN'].errors.unknown,
    )
    // A hand-built `errors.${code}` resolver would render the raw key here.
    expect(alert).not.toHaveTextContent('errors.client.unknown')
  })

  it('renders the transport text for a client.network failure, never a raw key', async () => {
    const utils = renderSection()
    renameFailure = 'network'
    fireEvent.click(
      utils.getByRole('button', { name: TENANT_BUNDLES['zh-CN'].save }),
    )
    await act(async () => {})

    const alert = utils.getByRole('alert')
    expect(alert).toHaveTextContent(
      TENANT_BUNDLES['zh-CN'].errors['client.network'],
    )
    expect(alert).not.toHaveTextContent('errors.client.network')
  })
})
