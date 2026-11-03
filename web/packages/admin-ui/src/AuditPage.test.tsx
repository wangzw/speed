/**
 * AuditPage: the audit-query page.
 *
 * Its read renders the settled records as a table, the recorded time
 * through Intl in the current language and explicitly in UTC (the zone the
 * view carries), the actor column as a readable identity resolved through
 * the account read (falling back to the recorded value, marked unresolved
 * when the read is refused), the server-side filters applied on submit,
 * cursor pagination, and a failed read settling into the retryable error
 * placeholder.
 */

import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { act, fireEvent, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import type { AuditAdminRecordsExportParams } from '@speed/api-sdk'
import { AuditPage } from './AuditPage.js'
import type { AdminAuditRecord } from './operations.js'
import { renderWithProviders } from '../test-utils/render.js'
import { captureDownloads } from '../test-utils/download.js'
import {
  columnVisibility,
  containerMaxWidthOf,
} from '../test-utils/container-visibility.js'
import { hasCoarsePointerTouchTarget } from '@speed/test-utils/emitted-css'
import {
  accountFixture,
  createAdminOperations,
  makeAdminBackend,
} from '../test-utils/operations.js'
import { expectNoAxeViolations } from '../test-utils/axe.js'
import zhCN from './locales/zh-CN.json' with { type: 'json' }
import enUS from './locales/en-US.json' with { type: 'json' }

const COPY = zhCN.common as { readonly updatedAt: string; readonly refreshed: string; readonly refresh: string }

const record: AdminAuditRecord = {
  id: '42',
  tenant: 'acme',
  occurred_at: '2026-10-15T12:00:00Z',
  actor: 'acc-1',
  source: 'console',
  action: 'tenant.create',
  object: 'tenant/acme',
  outcome: 'ok',
  purpose: '',
  detail: {},
}

const page = (entries: readonly AdminAuditRecord[], next = '') => ({
  entries: [...entries],
  next,
})

describe('AuditPage', () => {
  it('raises its toolbar and pager actions to the 44px floor under a coarse pointer', async () => {
    const operations = createAdminOperations(
      makeAdminBackend({
        listRecords: () => Promise.resolve(page([record], 'next')),
      }),
    )
    renderWithProviders(<AuditPage />, { operations })
    const gated = [
      await screen.findByRole('button', { name: COPY.refresh }),
      screen.getByRole('button', { name: zhCN.audit.prevPage }),
      screen.getByRole('button', { name: zhCN.audit.nextPage }),
    ]
    for (const control of gated) {
      expect(hasCoarsePointerTouchTarget(control)).toBe(true)
    }
  })

  it('renders the settled empty state when no record matches', async () => {
    const operations = createAdminOperations(makeAdminBackend())
    renderWithProviders(<AuditPage />, { operations })
    expect(await screen.findByText(zhCN.audit.empty.title)).toBeInTheDocument()
  })

  it('lists records', async () => {
    const operations = createAdminOperations(
      makeAdminBackend({ listRecords: () => Promise.resolve(page([record])) }),
    )
    renderWithProviders(<AuditPage />, { operations })
    expect(await screen.findByText('tenant.create')).toBeInTheDocument()
    expect(screen.getByText('tenant/acme')).toBeInTheDocument()
    expect(screen.getByText('ok')).toBeInTheDocument()
  })

  it('renders records as a table with column headers', async () => {
    const operations = createAdminOperations(
      makeAdminBackend({ listRecords: () => Promise.resolve(page([record])) }),
    )
    renderWithProviders(<AuditPage />, { operations })
    await screen.findByText('tenant.create')
    for (const name of [
      zhCN.audit.column.time,
      zhCN.audit.column.action,
      zhCN.audit.column.outcome,
      zhCN.audit.column.actor,
      zhCN.audit.column.object,
    ]) {
      expect(screen.getByRole('columnheader', { name })).toBeInTheDocument()
    }
  })

  it('keeps the outcome visible in a laptop-wide container alongside the actor', async () => {
    const operations = createAdminOperations(
      makeAdminBackend({ listRecords: () => Promise.resolve(page([record])) }),
    )
    renderWithProviders(<AuditPage />, { operations })
    await screen.findByText('tenant.create')
    // The table's container is 1184px wide beside the console nav at a
    // 1512px viewport: the object column is the first shed (it needs lg),
    // while the actor and the outcome both clear that width, so a failed
    // record is distinguishable without widening the window. Below sm the
    // outcome hides too, leaving only the two anchor columns.
    expect(
      columnVisibility(
        screen.getByRole('columnheader', { name: zhCN.audit.column.object }),
      ),
    ).toEqual({ hiddenAt: 0, visibleFrom: 1200 })
    // The header and the body each apply the column's visibility rule on
    // their own, so assert the body cells too: a divergence between the two
    // would otherwise pass with only the header checked.
    expect(
      columnVisibility(screen.getByText('tenant/acme').closest('td')!),
    ).toEqual({ hiddenAt: 0, visibleFrom: 1200 })
    expect(
      columnVisibility(
        screen.getByRole('columnheader', { name: zhCN.audit.column.actor }),
      ),
    ).toEqual({ hiddenAt: 0, visibleFrom: 900 })
    expect(
      columnVisibility(
        screen.getByRole('columnheader', { name: zhCN.audit.column.outcome }),
      ),
    ).toEqual({ hiddenAt: 0, visibleFrom: 600 })
    expect(
      columnVisibility(screen.getByText('ok').closest('td')!),
    ).toEqual({ hiddenAt: 0, visibleFrom: 600 })
    // The anchors never hide.
    expect(
      columnVisibility(
        screen.getByRole('columnheader', { name: zhCN.audit.column.time }),
      ),
    ).toBeNull()
    expect(
      columnVisibility(
        screen.getByRole('columnheader', { name: zhCN.audit.column.action }),
      ),
    ).toBeNull()
  })

  it('caps the action column with the primary cap and the object and actor flat', async () => {
    const action = 'pkg.objectstore.metadata.create'
    const object = '/api/v1/objects/a-very-long-registered-route'
    const operations = createAdminOperations(
      makeAdminBackend({
        listRecords: () =>
          Promise.resolve(page([{ ...record, action, object }])),
      }),
    )
    renderWithProviders(<AuditPage />, { operations })
    await screen.findByText(action)

    const primaryCap = new Map([
      [0, 160],
      [900, 200],
    ])
    // The action is the primary identity: its cap grows at md. The cap
    // sits on the wrapper that holds the text and the copy control
    // together, so the control is counted inside the budget.
    const actionCell = screen.getByText(action).closest('td')
    expect(actionCell).toHaveAttribute('title', action)
    expect(containerMaxWidthOf(actionCell!.firstElementChild!)).toEqual(
      primaryCap,
    )

    // The object and the actor are long-text columns that are not the
    // primary identity: a flat cap that does not grow with the container.
    const objectCell = screen.getByText(object).closest('td')
    expect(objectCell).toHaveAttribute('title', object)
    expect(objectCell!.firstElementChild).toHaveStyle({ maxWidth: '160px' })
    expect(
      containerMaxWidthOf(objectCell!.firstElementChild!),
    ).toEqual(new Map())

    // The actor renders its resolved display name inside the capped box;
    // the complete recorded identifier stays on the cell title.
    const actorText = await screen.findByText('Operator')
    const actorCell = actorText.closest('td')
    expect(actorCell).toHaveAttribute('title', 'acc-1')
    expect(actorCell!.firstElementChild).toHaveStyle({ maxWidth: '160px' })
    expect(
      containerMaxWidthOf(actorCell!.firstElementChild!),
    ).toEqual(new Map())
  })

  it('formats the recorded time in UTC in the current language', async () => {
    const operations = createAdminOperations(
      makeAdminBackend({ listRecords: () => Promise.resolve(page([record])) }),
    )
    renderWithProviders(<AuditPage />, { operations })
    await screen.findByText('tenant.create')
    const expected = new Intl.DateTimeFormat('zh-CN', {
      dateStyle: 'medium',
      timeStyle: 'short',
      timeZone: 'UTC',
    }).format(new Date(record.occurred_at))
    expect(screen.getByText(expected)).toBeInTheDocument()
  })

  it('renders no time for a malformed timestamp', async () => {
    const operations = createAdminOperations(
      makeAdminBackend({
        listRecords: () =>
          Promise.resolve(page([{ ...record, occurred_at: 'not-a-date' }])),
      }),
    )
    renderWithProviders(<AuditPage />, { operations })
    expect(await screen.findByText('tenant.create')).toBeInTheDocument()
    expect(screen.queryByText('not-a-date')).not.toBeInTheDocument()
  })

  it('renders the actor as the account display name', async () => {
    const operations = createAdminOperations(
      makeAdminBackend({
        listRecords: () => Promise.resolve(page([record])),
        getAccount: () =>
          Promise.resolve(accountFixture({ display_name: 'Ada Lovelace' })),
      }),
    )
    renderWithProviders(<AuditPage />, { operations })
    expect(await screen.findByText('Ada Lovelace')).toBeInTheDocument()
    expect(screen.queryByText('acc-1')).not.toBeInTheDocument()
  })

  it('falls back to the recorded actor when the account read fails', async () => {
    const operations = createAdminOperations(
      makeAdminBackend({
        listRecords: () =>
          Promise.resolve(page([{ ...record, actor: 'system' }])),
        getAccount: () => Promise.reject(new Error('not an account')),
      }),
    )
    renderWithProviders(<AuditPage />, { operations })
    expect(await screen.findByText('system')).toBeInTheDocument()
  })

  it('marks an actor whose account read is refused as unresolved', async () => {
    const operations = createAdminOperations(
      makeAdminBackend({
        listRecords: () => Promise.resolve(page([record])),
        getAccount: () => Promise.reject(new Error('forbidden')),
      }),
    )
    renderWithProviders(<AuditPage />, { operations })
    // The cell shows the recorded id, but labelled as unresolved rather
    // than passed off as a resolved (or system) name.
    const cell = await screen.findByTitle(zhCN.common.accountUnresolved)
    expect(cell).toHaveTextContent('acc-1')
    expect(cell).toHaveAttribute('title', zhCN.common.accountUnresolved)
  })

  it('applies the action filter server-side on submit', async () => {
    const user = userEvent.setup()
    const seen: { action?: string[] } = {}
    const operations = createAdminOperations(
      makeAdminBackend({
        listRecords: (params) => {
          seen.action = params?.action
          return Promise.resolve(page([record]))
        },
      }),
    )
    renderWithProviders(<AuditPage />, { operations })
    await screen.findByText('tenant.create')
    await user.type(
      screen.getByLabelText(zhCN.audit.filters.action),
      'tenant.create, account.delete',
    )
    await user.click(screen.getByRole('button', { name: zhCN.audit.filters.apply }))
    await vi.waitFor(() => {
      expect(seen.action).toEqual(['tenant.create', 'account.delete'])
    })
  })

  it('applies the outcome and actor filters server-side on submit', async () => {
    const user = userEvent.setup()
    const seen: { outcome?: string[]; actor?: string[] } = {}
    const operations = createAdminOperations(
      makeAdminBackend({
        listRecords: (params) => {
          seen.outcome = params?.outcome
          seen.actor = params?.actor
          return Promise.resolve(page([record]))
        },
      }),
    )
    renderWithProviders(<AuditPage />, { operations })
    await screen.findByText('tenant.create')
    await user.type(screen.getByLabelText(zhCN.audit.filters.outcome), 'ok, denied')
    await user.type(screen.getByLabelText(zhCN.audit.filters.actor), 'acc-1')
    await user.click(screen.getByRole('button', { name: zhCN.audit.filters.apply }))
    await vi.waitFor(() => {
      expect(seen.outcome).toEqual(['ok', 'denied'])
      expect(seen.actor).toEqual(['acc-1'])
    })
  })

  it('clears the action, outcome and actor filters on reset', async () => {
    const user = userEvent.setup()
    const requests: Array<{
      action?: string[]
      outcome?: string[]
      actor?: string[]
    }> = []
    const operations = createAdminOperations(
      makeAdminBackend({
        listRecords: (params) => {
          requests.push({
            action: params?.action,
            outcome: params?.outcome,
            actor: params?.actor,
          })
          return Promise.resolve(page([record]))
        },
      }),
    )
    renderWithProviders(<AuditPage />, { operations })
    await screen.findByText('tenant.create')
    await user.type(screen.getByLabelText(zhCN.audit.filters.action), 'tenant.create')
    await user.type(screen.getByLabelText(zhCN.audit.filters.outcome), 'ok')
    await user.type(screen.getByLabelText(zhCN.audit.filters.actor), 'acc-1')
    await user.click(screen.getByRole('button', { name: zhCN.audit.filters.apply }))
    await vi.waitFor(() => {
      expect(
        requests.some(
          (request) =>
            request.action?.[0] === 'tenant.create' &&
            request.outcome?.[0] === 'ok' &&
            request.actor?.[0] === 'acc-1',
        ),
      ).toBe(true)
    })
    await user.click(screen.getByRole('button', { name: zhCN.audit.filters.reset }))
    await vi.waitFor(() => {
      const last = requests.at(-1)
      expect(last?.action ?? []).toEqual([])
      expect(last?.outcome ?? []).toEqual([])
      expect(last?.actor ?? []).toEqual([])
    })
  })

  it('pages forward with the cursor the read answered with', async () => {
    const user = userEvent.setup()
    const operations = createAdminOperations(
      makeAdminBackend({
        listRecords: (params) =>
          Promise.resolve(
            params?.cursor === 'c1'
              ? page([{ ...record, id: '43', action: 'tenant.disable' }])
              : page([record], 'c1'),
          ),
      }),
    )
    renderWithProviders(<AuditPage />, { operations })
    await screen.findByText('tenant.create')
    await user.click(screen.getByRole('button', { name: zhCN.audit.nextPage }))
    expect(await screen.findByText('tenant.disable')).toBeInTheDocument()
    expect(
      screen.getByRole('button', { name: zhCN.audit.prevPage }),
    ).toBeEnabled()
  })

  it('renders the retryable error placeholder when the read fails', async () => {
    const operations = createAdminOperations(
      makeAdminBackend({
        listRecords: () => Promise.reject(new Error('boom')),
      }),
    )
    renderWithProviders(<AuditPage />, { operations })
    expect(await screen.findByText(zhCN.audit.error.title)).toBeInTheDocument()
    expect(screen.getByText(zhCN.audit.error.description)).toBeInTheDocument()
    expect(
      screen.getByRole('button', { name: zhCN.audit.retry }),
    ).toBeInTheDocument()
  })

  it('renders the permission placeholder with no retry when the read is refused with 403', async () => {
    const operations = createAdminOperations(
      makeAdminBackend({
        listRecords: () => Promise.reject({ status: 403, code: 'client.http.403' }),
      }),
    )
    renderWithProviders(<AuditPage />, { operations })
    expect(await screen.findByText(zhCN.forbidden.title)).toBeInTheDocument()
    expect(
      screen.queryByRole('button', { name: zhCN.audit.retry }),
    ).toBeNull()
  })

  it('shows the update time in the action group only once a read has settled', async () => {
    let resolveRead!: (value: ReturnType<typeof page>) => void
    const operations = createAdminOperations(
      makeAdminBackend({
        listRecords: () =>
          new Promise<ReturnType<typeof page>>((resolve) => {
            resolveRead = resolve
          }),
      }),
    )
    const { queryClient } = renderWithProviders(<AuditPage />, { operations })
    const header = await screen.findByRole('banner')
    const prefix = COPY.updatedAt.split('{{time}}')[0] ?? COPY.updatedAt
    expect(
      within(header).queryByText((content) => content.startsWith(prefix)),
    ).toBeNull()
    // The completion region is mounted empty before anything is refreshed.
    expect(within(header).getByRole('status').textContent).toBe('')

    await act(async () => {
      resolveRead(page([record]))
      await Promise.resolve()
    })
    await screen.findByText('tenant.create')

    const state = queryClient
      .getQueryCache()
      .getAll()
      .find(
        (query) =>
          Array.isArray(query.queryKey) &&
          query.queryKey[0] === 'console-test' &&
          query.queryKey[1] === 'audit',
      )
    const expected = COPY.updatedAt.replace(
      '{{time}}',
      new Intl.DateTimeFormat('zh-CN', {
        dateStyle: 'medium',
        timeStyle: 'long',
        timeZone: 'UTC',
      }).format(new Date(state!.state.dataUpdatedAt)),
    )
    const label = await within(header).findByText(expected)
    // The label sits in the refresh action's group, before the control.
    const refresh = within(header).getByRole('button', { name: COPY.refresh })
    expect(
      label.compareDocumentPosition(refresh) & Node.DOCUMENT_POSITION_FOLLOWING,
    ).toBeTruthy()
  })

  it('announces every completed refresh, clearing the region on each click', async () => {
    const pending: ((value: ReturnType<typeof page>) => void)[] = []
    let calls = 0
    const operations = createAdminOperations(
      makeAdminBackend({
        listRecords: () => {
          calls += 1
          return calls === 1
            ? Promise.resolve(page([record]))
            : new Promise<ReturnType<typeof page>>((resolve) => {
                pending.push(resolve)
              })
        },
      }),
    )
    renderWithProviders(<AuditPage />, { operations })
    await screen.findByText('tenant.create')

    const header = screen.getByRole('banner')
    const refresh = within(header).getByRole('button', { name: COPY.refresh })
    const status = within(header).getByRole('status')
    const user = userEvent.setup()

    await user.click(refresh)
    expect(status.textContent).toBe('')
    await act(async () => {
      pending.shift()!(page([record]))
      await Promise.resolve()
    })
    await waitFor(() => expect(status.textContent).toBe(COPY.refreshed))

    await user.click(refresh)
    // Cleared at the click, so the identical text is filled in again and
    // announced a second time.
    expect(status.textContent).toBe('')
    await act(async () => {
      pending.shift()!(page([record]))
      await Promise.resolve()
    })
    await waitFor(() => expect(status.textContent).toBe(COPY.refreshed))
  })

  it('does not announce a refresh whose read fails', async () => {
    let calls = 0
    const operations = createAdminOperations(
      makeAdminBackend({
        listRecords: () => {
          calls += 1
          return calls === 1
            ? Promise.resolve(page([record]))
            : Promise.reject(new Error('boom'))
        },
      }),
    )
    renderWithProviders(<AuditPage />, { operations })
    await screen.findByText('tenant.create')

    const header = screen.getByRole('banner')
    const user = userEvent.setup()
    await user.click(within(header).getByRole('button', { name: COPY.refresh }))
    await waitFor(() =>
      expect(
        within(header).getByRole('button', { name: COPY.refresh }),
      ).not.toHaveAttribute('aria-busy'),
    )
    // The failure is carried by the stale hint, not by an announcement.
    expect(within(header).getByRole('status').textContent).toBe('')
  })

  it('has no axe violations', async () => {
    const operations = createAdminOperations(
      makeAdminBackend({ listRecords: () => Promise.resolve(page([record])) }),
    )
    renderWithProviders(<AuditPage />, { operations })
    await screen.findByText('tenant.create')
    await expectNoAxeViolations()
  })

  describe('export', () => {
    let capture: ReturnType<typeof captureDownloads>

    beforeEach(() => {
      capture = captureDownloads()
    })

    afterEach(() => {
      capture.restore()
    })

    it('renders the export control in the filter toolbar, outside the filter group', async () => {
      const operations = createAdminOperations(makeAdminBackend())
      renderWithProviders(<AuditPage />, { operations })
      const control = await screen.findByRole('button', {
        name: zhCN.audit.export.label,
      })
      const filters = screen.getByRole('group', {
        name: zhCN.audit.filters.label,
      })
      expect(filters).not.toContainElement(control)
      expect(filters.parentElement).toContainElement(control)
    })

    it('states the export scope beside the control, whether or not an export is running', async () => {
      const user = userEvent.setup()
      let resolveExport!: (blob: Blob) => void
      const operations = createAdminOperations(
        makeAdminBackend({
          listRecords: () => Promise.resolve(page([record], 'next')),
          exportRecords: () =>
            new Promise<Blob>((resolve) => {
              resolveExport = resolve
            }),
        }),
      )
      renderWithProviders(<AuditPage />, { operations })
      const control = await screen.findByRole('button', {
        name: zhCN.audit.export.label,
      })
      // The explanation the operator needs before acting: the file covers
      // every matching record, not the page on screen. Its text comes from
      // the bundle and it sits with the control it explains.
      const note = screen.getByText(zhCN.audit.export.note)
      expect(control.parentElement).toContainElement(note)
      expect(control).toHaveAccessibleDescription(zhCN.audit.export.note)
      expect(control).not.toHaveAttribute('aria-busy')

      await user.click(control)
      await vi.waitFor(() => {
        expect(control).toHaveAttribute('aria-busy', 'true')
      })
      expect(screen.getByText(zhCN.audit.export.note)).toBeInTheDocument()

      await act(async () => {
        resolveExport(new Blob([]))
      })
      await vi.waitFor(() => {
        expect(control).not.toHaveAttribute('aria-busy')
      })
      // The explanation is not a progress message: it stays after the
      // export settles.
      expect(screen.getByText(zhCN.audit.export.note)).toBeInTheDocument()
    })

    it('states the export scope from the English bundle in the English UI', async () => {
      const operations = createAdminOperations(makeAdminBackend())
      renderWithProviders(<AuditPage />, { operations, language: 'en-US' })
      const control = await screen.findByRole('button', {
        name: enUS.audit.export.label,
      })
      expect(screen.getByText(enUS.audit.export.note)).toBeInTheDocument()
      expect(control).toHaveAccessibleDescription(enUS.audit.export.note)
    })

    it('renders no export control when the bundle carries no export member', async () => {
      const operations = { ...createAdminOperations(makeAdminBackend()) }
      delete operations.exportAuditRecords
      renderWithProviders(<AuditPage />, { operations })
      expect(await screen.findByText(zhCN.audit.empty.title)).toBeInTheDocument()
      expect(
        screen.queryByRole('button', { name: zhCN.audit.export.label }),
      ).toBeNull()
    })

    it('exports the applied filters, never the drafts or a page window', async () => {
      const user = userEvent.setup()
      const seen: AuditAdminRecordsExportParams[] = []
      const operations = createAdminOperations(
        makeAdminBackend({
          listRecords: () => Promise.resolve(page([record])),
          exportRecords: (params) => {
            seen.push(params ?? {})
            return Promise.resolve(new Blob([]))
          },
        }),
      )
      renderWithProviders(<AuditPage />, { operations })
      await screen.findByText('tenant.create')
      const control = screen.getByRole('button', {
        name: zhCN.audit.export.label,
      })

      // A typed filter that was never applied does not reach the export.
      await user.type(
        screen.getByLabelText(zhCN.audit.filters.action),
        'tenant.delete',
      )
      await user.click(control)
      await vi.waitFor(() => expect(seen).toHaveLength(1))
      expect(seen[0]).toEqual({})

      // Apply that filter, then change the drafts without applying again:
      // the export carries the applied set, not the newer drafts.
      await user.click(
        screen.getByRole('button', { name: zhCN.audit.filters.apply }),
      )
      await screen.findByText('tenant.create')
      await user.clear(screen.getByLabelText(zhCN.audit.filters.action))
      await user.type(
        screen.getByLabelText(zhCN.audit.filters.action),
        'tenant.remove',
      )
      await user.click(control)
      await vi.waitFor(() => expect(seen).toHaveLength(2))
      expect(seen[1]).toEqual({ action: ['tenant.delete'] })
      // An export is not a page: it carries no cursor and no page size.
      expect(seen[1]).not.toHaveProperty('cursor')
      expect(seen[1]).not.toHaveProperty('limit')
    })

    it('saves the streamed export under its UTC instant', async () => {
      const user = userEvent.setup()
      const content = '{"id":"42"}\n'
      const operations = createAdminOperations(
        makeAdminBackend({
          listRecords: () => Promise.resolve(page([record])),
          exportRecords: () =>
            Promise.resolve(
              new Blob([content], { type: 'application/x-ndjson' }),
            ),
        }),
      )
      renderWithProviders(<AuditPage />, { operations })
      await screen.findByText('tenant.create')

      await user.click(
        screen.getByRole('button', { name: zhCN.audit.export.label }),
      )
      await vi.waitFor(() => expect(capture.downloads()).toHaveLength(1))

      const download = capture.downloads()[0]!
      const match = /^audit-records-(\d{4})(\d{2})(\d{2})T(\d{2})(\d{2})(\d{2})Z\.ndjson$/.exec(
        download.fileName,
      )
      expect(match).not.toBeNull()
      const [, year, month, day, hour, minute, second] = match!
      const instant = Date.UTC(
        Number(year),
        Number(month) - 1,
        Number(day),
        Number(hour),
        Number(minute),
        Number(second),
      )
      // The name is the export's own instant, read in UTC.
      expect(Math.abs(instant - Date.now())).toBeLessThan(5 * 60 * 1000)
      expect(await capture.blobs()[0]!.text()).toBe(content)
      // A download is its own confirmation; the page adds none.
      expect(screen.queryByRole('alert')).toBeNull()
    })

    it('marks the control busy and starts no second export while one is in flight', async () => {
      const user = userEvent.setup()
      let resolveExport!: (blob: Blob) => void
      let calls = 0
      const operations = createAdminOperations(
        makeAdminBackend({
          listRecords: () => Promise.resolve(page([record])),
          exportRecords: () => {
            calls += 1
            return new Promise<Blob>((resolve) => {
              resolveExport = resolve
            })
          },
        }),
      )
      renderWithProviders(<AuditPage />, { operations })
      await screen.findByText('tenant.create')
      const control = screen.getByRole('button', {
        name: zhCN.audit.export.label,
      })

      await user.click(control)
      await vi.waitFor(() => expect(calls).toBe(1))
      expect(control).toHaveAttribute('aria-busy', 'true')

      // A second activation while the first is in flight issues nothing.
      fireEvent.click(control)
      expect(calls).toBe(1)

      await act(async () => {
        resolveExport(new Blob([]))
      })
      await vi.waitFor(() => {
        expect(control).not.toHaveAttribute('aria-busy')
      })
      expect(control).toBeEnabled()
    })

    it.each([
      { status: 403, text: zhCN.errors.client.httpForbidden },
      {
        status: 503,
        text: zhCN.errors.client.http.replace('{{status}}', '503'),
      },
    ])(
      'reports a $status refusal in the toolbar and restores the control',
      async ({ status, text }) => {
        const user = userEvent.setup()
        const operations = createAdminOperations(
          makeAdminBackend({
            listRecords: () => Promise.resolve(page([record])),
            exportRecords: () =>
              Promise.reject({ status, code: `client.http.${status}` }),
          }),
        )
        renderWithProviders(<AuditPage />, { operations })
        await screen.findByText('tenant.create')
        const control = screen.getByRole('button', {
          name: zhCN.audit.export.label,
        })

        await user.click(control)
        expect(await screen.findByText(text)).toBeInTheDocument()
        expect(control).toBeEnabled()
        expect(control).not.toHaveAttribute('aria-busy')
        // The failure belongs to the action, not to the read's placeholder.
        expect(screen.queryByText(zhCN.audit.error.title)).toBeNull()
      },
    )
  })
})
