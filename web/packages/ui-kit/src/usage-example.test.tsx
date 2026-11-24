
import { readFileSync } from 'node:fs'
import { dirname, join } from 'node:path'
import { fileURLToPath } from 'node:url'
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
import {
  ActionButton,
  useWriteFeedback,
  WriteFeedbackRegion,
} from './index.js'

const README_PATH = join(
  dirname(fileURLToPath(import.meta.url)),
  '..',
  'README.md',
)

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

const STORAGE_UPLOAD_URL = 'https://uploads.example.test/objects'

function isAbortError(error: unknown): boolean {
  if (error instanceof DOMException) {
    return error.name === 'AbortError'
  }
  return error instanceof Error && error.name === 'AbortError'
}

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

function UploadPanel() {
  const [rows, setRows] = useState<readonly FileUploaderRow[]>([])
  const filesRef = useRef(new Map<string, File>())
  const controllersRef = useRef(new Map<string, AbortController>())
  const nextIdRef = useRef(1)

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
    const readme = readFileSync(README_PATH, 'utf8')
    const sectionStart = readme.indexOf('### Write feedback')
    expect(sectionStart).toBeGreaterThanOrEqual(0)
    const fenceStart = readme.indexOf('```tsx', sectionStart)
    expect(fenceStart).toBeGreaterThan(sectionStart)
    const blockStart = fenceStart + '```tsx'.length
    const fenceEnd = readme.indexOf('```', blockStart)
    expect(fenceEnd).toBeGreaterThan(blockStart)
    const block = readme.slice(blockStart, fenceEnd)

    expect(block).toContain('createErrorTextResolver(')
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

    const transport = scriptedStorageTransport()
    vi.stubGlobal('fetch', transport.fetchMock)

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
    expect(utils.queryByRole('listitem')).not.toBeInTheDocument()
    expect(utils.queryByRole('status')).not.toBeInTheDocument()

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

    await act(async () => {
      transport.gates[0]!(201)
    })
    expect(utils.getByText(zhCN.fileUploader.statusSucceeded)).toBeInTheDocument()
    expect(utils.getByRole('status')).toHaveTextContent(
      zhCN.fileUploader.announceUploaded.replace('{{name}}', portrait.name),
    )

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

    const portraitRow = utils.getByText(portrait.name).closest('li')!
    fireEvent.click(
      within(portraitRow).getByRole('button', {
        name: zhCN.fileUploader.actionRemove,
      }),
    )
    expect(utils.queryByText(portrait.name)).not.toBeInTheDocument()
    expect(utils.getByText(closeup.name)).toBeInTheDocument()
    expect(utils.getByText('Katherine')).toBeInTheDocument()

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

    utils.unmount()
    await act(async () => {})
    expect(transport.uploadCalls[0]!.signal?.aborted).toBe(true)
    await expectAbort(transport.pending[0]!)
  })
})

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

const TENANT_ERROR_CODES: ReadonlySet<string> = new Set([
  ...CLIENT_TRANSPORT_ERROR_CODES,
  'tenant.name_required',
])

let renameFailure: 'network' | 'plain' | null = null
let renamedName: string | null = null

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
      <ActionButton
        busy={feedback.pending}
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
      </ActionButton>
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

    expect(renamedName).toBe('Acme')
    expect(utils.getByRole('status').textContent).toBe(
      TENANT_BUNDLES['zh-CN'].renamed,
    )

    await act(async () => {
      await switchLanguage(utils.i18n, 'en-US')
    })
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
