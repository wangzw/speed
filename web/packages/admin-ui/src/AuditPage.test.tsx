import { useState } from 'react'
import { afterEach, beforeEach, describe, expect, it } from 'vitest'
import { waitForCondition } from '@speed/test-utils/wait'
import { act, fireEvent, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import type { AuditAdminRecordsExportParams } from '@speed/api-sdk'
import { DisplayTimeZoneProvider } from '@speed/ui-kit'
import { AuditPage } from './AuditPage.js'
import type { AdminAuditRecord } from './operations.js'
import { TEST_TIME_ZONE } from '@speed/test-utils/render'
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

function ZoneSwitcher({
  zones,
}: {
  readonly zones: readonly string[]
}) {
  const [index, setIndex] = useState(0)
  return (
    <DisplayTimeZoneProvider timeZone={zones[index]}>
      <button
        type="button"
        onClick={() =>
          setIndex((current) => Math.min(current + 1, zones.length - 1))
        }
      >
        switch-display-zone
      </button>
      <AuditPage />
    </DisplayTimeZoneProvider>
  )
}

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
    expect(
      columnVisibility(
        screen.getByRole('columnheader', { name: zhCN.audit.column.object }),
      ),
    ).toEqual({ hiddenAt: 0, visibleFrom: 1200 })
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
    const actionCell = screen.getByText(action).closest('td')
    expect(actionCell).toHaveAttribute('title', action)
    expect(containerMaxWidthOf(actionCell!.firstElementChild!)).toEqual(
      primaryCap,
    )

    const objectCell = screen.getByText(object).closest('td')
    expect(objectCell).toHaveAttribute('title', object)
    expect(objectCell!.firstElementChild).toHaveStyle({ maxWidth: '160px' })
    expect(
      containerMaxWidthOf(objectCell!.firstElementChild!),
    ).toEqual(new Map())

    const actorText = await screen.findByText('Operator')
    const actorCell = actorText.closest('td')
    expect(actorCell).not.toHaveAttribute('title', 'acc-1')
    expect(
      within(actorCell as HTMLElement).queryByRole('button'),
    ).toBeNull()
    expect(actorCell!.firstElementChild).toHaveStyle({ maxWidth: '160px' })
    expect(
      containerMaxWidthOf(actorCell!.firstElementChild!),
    ).toEqual(new Map())
  })

  it("formats the recorded time in the harness's default display zone", async () => {
    const operations = createAdminOperations(
      makeAdminBackend({ listRecords: () => Promise.resolve(page([record])) }),
    )
    renderWithProviders(<AuditPage />, { operations })
    await screen.findByText('tenant.create')
    const expected = new Intl.DateTimeFormat('zh-CN', {
      dateStyle: 'medium',
      timeStyle: 'short',
      timeZone: TEST_TIME_ZONE,
    }).format(new Date(record.occurred_at))
    expect(screen.getByText(expected)).toBeInTheDocument()
  })

  it('formats the recorded time in a display zone other than the browser zone', async () => {
    const operations = createAdminOperations(
      makeAdminBackend({ listRecords: () => Promise.resolve(page([record])) }),
    )
    renderWithProviders(<AuditPage />, {
      operations,
      timeZone: 'Asia/Tokyo',
    })
    await screen.findByText('tenant.create')
    const expected = new Intl.DateTimeFormat('zh-CN', {
      dateStyle: 'medium',
      timeStyle: 'short',
      timeZone: 'Asia/Tokyo',
    }).format(new Date(record.occurred_at))
    expect(screen.getByText(expected)).toBeInTheDocument()
  })

  it('reads the since and until inputs in the display zone', async () => {
    const user = userEvent.setup()
    const seen: { since?: string; until?: string } = {}
    const operations = createAdminOperations(
      makeAdminBackend({
        listRecords: (params) => {
          seen.since = params?.since
          seen.until = params?.until
          return Promise.resolve(page([record]))
        },
      }),
    )
    renderWithProviders(<AuditPage />, {
      operations,
      timeZone: 'Asia/Tokyo',
    })
    await screen.findByText('tenant.create')
    fireEvent.change(screen.getByLabelText(zhCN.audit.filters.since), {
      target: { value: '2026-10-15T09:30' },
    })
    fireEvent.change(screen.getByLabelText(zhCN.audit.filters.until), {
      target: { value: '2026-10-15T10:00' },
    })
    await user.click(screen.getByRole('button', { name: zhCN.audit.filters.apply }))
    await waitForCondition(() => {
      expect(seen.since).toBe('2026-10-15T00:30:00.000Z')
      expect(seen.until).toBe('2026-10-15T01:00:00.000Z')
    })
  })

  it('refuses a since reading the display zone skips instead of dropping the bound', async () => {
    const user = userEvent.setup()
    const seen: Array<{ since?: string }> = []
    const operations = createAdminOperations(
      makeAdminBackend({
        listRecords: (params) => {
          seen.push({ since: params?.since })
          return Promise.resolve(page([record]))
        },
      }),
    )
    const { queryClient } = renderWithProviders(<AuditPage />, {
      operations,
      timeZone: 'America/New_York',
    })
    await screen.findByText('tenant.create')
    const since = screen.getByLabelText(zhCN.audit.filters.since)
    const apply = screen.getByRole('button', {
      name: zhCN.audit.filters.apply,
    })

    fireEvent.change(since, { target: { value: '2026-03-08T03:30' } })
    await user.click(apply)
    await waitForCondition(() => {
      expect(seen.at(-1)?.since).toBe('2026-03-08T07:30:00.000Z')
    })

    const requestsBeforeRefusal = seen.length
    fireEvent.change(since, { target: { value: '2026-03-08T02:30' } })
    await user.click(apply)

    expect(
      await screen.findByText(zhCN.audit.filters.timeUnavailable),
    ).toBeInTheDocument()
    expect(since).toHaveAttribute('aria-invalid', 'true')
    expect(since).toHaveFocus()
    await waitFor(() => expect(queryClient.isFetching()).toBe(0))
    expect(seen).toHaveLength(requestsBeforeRefusal)
  })

  it('reports an until reading the display zone skips on its own field', async () => {
    const user = userEvent.setup()
    const operations = createAdminOperations(
      makeAdminBackend({ listRecords: () => Promise.resolve(page([record])) }),
    )
    renderWithProviders(<AuditPage />, {
      operations,
      timeZone: 'America/New_York',
    })
    await screen.findByText('tenant.create')
    const until = screen.getByLabelText(zhCN.audit.filters.until)
    fireEvent.change(until, { target: { value: '2026-03-08T02:30' } })
    await user.click(
      screen.getByRole('button', { name: zhCN.audit.filters.apply }),
    )
    expect(
      await screen.findByText(zhCN.audit.filters.timeUnavailable),
    ).toBeInTheDocument()
    expect(until).toHaveAttribute('aria-invalid', 'true')
    expect(until).toHaveFocus()
    expect(screen.getByLabelText(zhCN.audit.filters.since)).not.toHaveAttribute(
      'aria-invalid',
      'true',
    )
  })

  it('names an out-of-range reading as out of range and focuses its field', async () => {
    const user = userEvent.setup()
    const operations = createAdminOperations(
      makeAdminBackend({ listRecords: () => Promise.resolve(page([record])) }),
    )
    renderWithProviders(<AuditPage />, { operations })
    await screen.findByText('tenant.create')
    const since = screen.getByLabelText(zhCN.audit.filters.since)
    const until = screen.getByLabelText(zhCN.audit.filters.until)

    fireEvent.change(since, { target: { value: '10000-01-01T00:00' } })
    fireEvent.change(until, { target: { value: '2026-03-08T02:30' } })
    await user.click(
      screen.getByRole('button', { name: zhCN.audit.filters.apply }),
    )

    expect(
      await screen.findByText(zhCN.audit.filters.timeOutOfRange),
    ).toBeInTheDocument()
    expect(
      await screen.findByText(zhCN.audit.filters.timeUnavailable),
    ).toBeInTheDocument()
    expect(since).toHaveAttribute('aria-invalid', 'true')
    expect(since).toHaveFocus()
    expect(until).not.toHaveFocus()
  })

  it('translates a field refusal when it renders, not when it is set', async () => {
    const user = userEvent.setup()
    const operations = createAdminOperations(
      makeAdminBackend({ listRecords: () => Promise.resolve(page([record])) }),
    )
    const { i18n } = renderWithProviders(<AuditPage />, {
      operations,
      timeZone: 'America/New_York',
    })
    await screen.findByText('tenant.create')

    fireEvent.change(screen.getByLabelText(zhCN.audit.filters.since), {
      target: { value: '2026-03-08T02:30' },
    })
    await user.click(
      screen.getByRole('button', { name: zhCN.audit.filters.apply }),
    )
    expect(
      await screen.findByText(zhCN.audit.filters.timeUnavailable),
    ).toBeInTheDocument()

    await act(async () => {
      await i18n.changeLanguage('en-US')
    })
    expect(
      await screen.findByText(enUS.audit.filters.timeUnavailable),
    ).toBeInTheDocument()
    expect(
      screen.queryByText(zhCN.audit.filters.timeUnavailable),
    ).not.toBeInTheDocument()
  })

  it('resolves an overlapping since to the earlier instant', async () => {
    const user = userEvent.setup()
    const seen: { since?: string } = {}
    const operations = createAdminOperations(
      makeAdminBackend({
        listRecords: (params) => {
          seen.since = params?.since
          return Promise.resolve(page([record]))
        },
      }),
    )
    renderWithProviders(<AuditPage />, {
      operations,
      timeZone: 'America/New_York',
    })
    await screen.findByText('tenant.create')

    fireEvent.change(screen.getByLabelText(zhCN.audit.filters.since), {
      target: { value: '2026-11-01T01:30' },
    })
    await user.click(
      screen.getByRole('button', { name: zhCN.audit.filters.apply }),
    )
    await waitForCondition(() => {
      expect(seen.since).toBe('2026-11-01T05:30:00.000Z')
    })
  })

  it('resolves an overlapping until to the later instant', async () => {
    const user = userEvent.setup()
    const seen: { until?: string } = {}
    const operations = createAdminOperations(
      makeAdminBackend({
        listRecords: (params) => {
          seen.until = params?.until
          return Promise.resolve(page([record]))
        },
      }),
    )
    renderWithProviders(<AuditPage />, {
      operations,
      timeZone: 'America/New_York',
    })
    await screen.findByText('tenant.create')

    fireEvent.change(screen.getByLabelText(zhCN.audit.filters.until), {
      target: { value: '2026-11-01T01:30' },
    })
    await user.click(
      screen.getByRole('button', { name: zhCN.audit.filters.apply }),
    )
    await waitForCondition(() => {
      expect(seen.until).toBe('2026-11-01T06:30:00.000Z')
    })
  })

  it('keeps the applied instant when a regenerated draft reads the repeated hour', async () => {
    const user = userEvent.setup()
    const seen: Array<{ since?: string }> = []
    const exports: AuditAdminRecordsExportParams[] = []
    const operations = createAdminOperations(
      makeAdminBackend({
        listRecords: (params) => {
          seen.push({ since: params?.since })
          return Promise.resolve(page([record]))
        },
        exportRecords: (params) => {
          exports.push(params ?? {})
          return Promise.resolve(new Blob([]))
        },
      }),
    )
    const { queryClient } = renderWithProviders(
      <ZoneSwitcher zones={['UTC', 'America/New_York']} />,
      { operations },
    )
    await screen.findByText('tenant.create')
    const since = screen.getByLabelText(zhCN.audit.filters.since)
    const apply = screen.getByRole('button', { name: zhCN.audit.filters.apply })

    fireEvent.change(since, { target: { value: '2026-11-01T06:30' } })
    await user.click(apply)
    await waitForCondition(() => {
      expect(seen.at(-1)?.since).toBe('2026-11-01T06:30:00.000Z')
    })

    await user.click(
      screen.getByRole('button', { name: 'switch-display-zone' }),
    )
    await waitForCondition(() => {
      expect(since).toHaveValue('2026-11-01T01:30')
    })
    const control = screen.getByRole('button', {
      name: zhCN.audit.export.label,
    })
    expect(control).not.toHaveAttribute('aria-disabled', 'true')
    expect(
      screen.queryByText(zhCN.audit.export.applyFirst),
    ).not.toBeInTheDocument()

    await user.click(apply)
    await waitFor(() => expect(queryClient.isFetching()).toBe(0))
    expect(
      seen.filter((params) => params.since === '2026-11-01T05:30:00.000Z'),
    ).toHaveLength(0)
    await user.click(control)
    await waitForCondition(() => expect(exports).toHaveLength(1))
    expect(exports[0]?.since).toBe('2026-11-01T06:30:00.000Z')
  })

  it('keeps an applied upper bound when a regenerated draft reads the repeated hour', async () => {
    const user = userEvent.setup()
    const seen: Array<{ until?: string }> = []
    const exports: AuditAdminRecordsExportParams[] = []
    const operations = createAdminOperations(
      makeAdminBackend({
        listRecords: (params) => {
          seen.push({ until: params?.until })
          return Promise.resolve(page([record]))
        },
        exportRecords: (params) => {
          exports.push(params ?? {})
          return Promise.resolve(new Blob([]))
        },
      }),
    )
    renderWithProviders(
      <ZoneSwitcher zones={['UTC', 'America/New_York']} />,
      { operations },
    )
    await screen.findByText('tenant.create')
    const until = screen.getByLabelText(zhCN.audit.filters.until)
    const apply = screen.getByRole('button', { name: zhCN.audit.filters.apply })

    fireEvent.change(until, { target: { value: '2026-11-01T05:30' } })
    await user.click(apply)
    await waitForCondition(() => {
      expect(seen.at(-1)?.until).toBe('2026-11-01T05:30:00.000Z')
    })

    await user.click(
      screen.getByRole('button', { name: 'switch-display-zone' }),
    )
    await waitForCondition(() => {
      expect(until).toHaveValue('2026-11-01T01:30')
    })
    const control = screen.getByRole('button', {
      name: zhCN.audit.export.label,
    })
    expect(control).not.toHaveAttribute('aria-disabled', 'true')
    expect(
      screen.queryByText(zhCN.audit.export.applyFirst),
    ).not.toBeInTheDocument()

    const requestsBefore = seen.length
    await user.click(apply)
    await user.click(screen.getByRole('button', { name: COPY.refresh }))
    await waitForCondition(() => {
      expect(seen.length).toBeGreaterThan(requestsBefore)
      expect(seen.at(-1)?.until).toBe('2026-11-01T05:30:00.000Z')
    })
    expect(
      seen
        .slice(requestsBefore)
        .filter((params) => params.until === '2026-11-01T06:30:00.000Z'),
    ).toHaveLength(0)
    await user.click(control)
    await waitForCondition(() => expect(exports).toHaveLength(1))
    expect(exports[0]?.until).toBe('2026-11-01T05:30:00.000Z')
  })

  it('regenerates the applied time drafts when the display zone changes', async () => {
    const user = userEvent.setup()
    const seen: { since?: string } = {}
    const exports: AuditAdminRecordsExportParams[] = []
    const operations = createAdminOperations(
      makeAdminBackend({
        listRecords: (params) => {
          seen.since = params?.since
          return Promise.resolve(page([record]))
        },
        exportRecords: (params) => {
          exports.push(params ?? {})
          return Promise.resolve(new Blob([]))
        },
      }),
    )
    renderWithProviders(<ZoneSwitcher zones={['America/New_York', 'Asia/Tokyo']} />, {
      operations,
    })
    await screen.findByText('tenant.create')
    const since = screen.getByLabelText(zhCN.audit.filters.since)
    fireEvent.change(since, { target: { value: '2026-11-01T01:30' } })
    await user.click(
      screen.getByRole('button', { name: zhCN.audit.filters.apply }),
    )
    await waitForCondition(() => {
      expect(seen.since).toBe('2026-11-01T05:30:00.000Z')
    })

    await user.click(
      screen.getByRole('button', { name: 'switch-display-zone' }),
    )
    await waitForCondition(() => {
      expect(since).toHaveValue('2026-11-01T14:30')
    })
    const control = screen.getByRole('button', {
      name: zhCN.audit.export.label,
    })
    expect(control).not.toHaveAttribute('aria-disabled', 'true')
    await user.click(control)
    await waitForCondition(() => expect(exports).toHaveLength(1))
    expect(exports[0]?.since).toBe('2026-11-01T05:30:00.000Z')
  })

  it('recomputes the unavailable marker against the new display zone', async () => {
    const user = userEvent.setup()
    const operations = createAdminOperations(
      makeAdminBackend({ listRecords: () => Promise.resolve(page([record])) }),
    )
    renderWithProviders(<ZoneSwitcher zones={['UTC', 'America/New_York']} />, {
      operations,
    })
    await screen.findByText('tenant.create')
    const since = screen.getByLabelText(zhCN.audit.filters.since)
    fireEvent.change(since, { target: { value: '2026-03-08T02:30' } })
    expect(
      screen.queryByText(zhCN.audit.filters.timeUnavailable),
    ).not.toBeInTheDocument()

    await user.click(
      screen.getByRole('button', { name: 'switch-display-zone' }),
    )
    expect(
      await screen.findByText(zhCN.audit.filters.timeUnavailable),
    ).toBeInTheDocument()
    expect(since).toHaveAttribute('aria-invalid', 'true')
    expect(since).toHaveValue('2026-03-08T02:30')
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

  it('renders the neutral copy when the account read fails, never the recorded actor', async () => {
    const operations = createAdminOperations(
      makeAdminBackend({
        listRecords: () =>
          Promise.resolve(page([{ ...record, actor: 'system' }])),
        getAccount: () => Promise.reject(new Error('not an account')),
      }),
    )
    renderWithProviders(<AuditPage />, { operations })
    expect(
      await screen.findByText(zhCN.common.accountUnavailable),
    ).toBeInTheDocument()
    expect(screen.queryByText('system')).toBeNull()
  })

  it('marks an actor whose account read is refused as unresolved', async () => {
    const operations = createAdminOperations(
      makeAdminBackend({
        listRecords: () => Promise.resolve(page([record])),
        getAccount: () => Promise.reject(new Error('forbidden')),
      }),
    )
    renderWithProviders(<AuditPage />, { operations })
    const cell = await screen.findByTitle(zhCN.common.accountUnresolved)
    expect(cell).toHaveTextContent(zhCN.common.accountUnavailable)
    expect(cell).not.toHaveTextContent('acc-1')
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
    await waitForCondition(() => {
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
    await waitForCondition(() => {
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
    await waitForCondition(() => {
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
    await waitForCondition(() => {
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
    ).not.toHaveAttribute('aria-disabled', 'true')
  })

  it('keeps the pager focusable, refusing the first page’s previous control without a reason', async () => {
    const user = userEvent.setup()
    const requests: Array<{ cursor?: string }> = []
    const operations = createAdminOperations(
      makeAdminBackend({
        listRecords: (params) => {
          requests.push({ cursor: params?.cursor })
          return Promise.resolve(page([record], 'c1'))
        },
      }),
    )
    renderWithProviders(<AuditPage />, { operations })
    await screen.findByText('tenant.create')
    const prev = screen.getByRole('button', { name: zhCN.audit.prevPage })
    expect(prev).toHaveAttribute('aria-disabled', 'true')
    expect(prev).not.toHaveAttribute('aria-describedby')
    prev.focus()
    expect(prev).toHaveFocus()

    const requestsBefore = requests.length
    await user.keyboard('{Enter}')
    await user.keyboard(' ')
    expect(fireEvent.click(prev)).toBe(false)
    await waitFor(() => expect(requests.length).toBe(requestsBefore))
    expect(prev).toHaveFocus()
  })

  it('keeps focus on the next control when it reaches the last page', async () => {
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
    const next = screen.getByRole('button', { name: zhCN.audit.nextPage })
    expect(next).not.toHaveAttribute('aria-disabled', 'true')

    await user.click(next)
    expect(await screen.findByText('tenant.disable')).toBeInTheDocument()
    await waitForCondition(() => {
      expect(next).toHaveAttribute('aria-disabled', 'true')
    })
    expect(next).toHaveFocus()
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
        timeZone: TEST_TIME_ZONE,
      }).format(new Date(state!.state.dataUpdatedAt)),
    )
    const label = await within(header).findByText(expected)
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
      const note = screen.getByText(zhCN.audit.export.note)
      expect(control.parentElement).toContainElement(note)
      expect(control).toHaveAccessibleDescription(zhCN.audit.export.note)
      expect(control).not.toHaveAttribute('aria-busy')

      await user.click(control)
      await waitForCondition(() => {
        expect(control).toHaveAttribute('aria-busy', 'true')
      })
      expect(screen.getByText(zhCN.audit.export.note)).toBeInTheDocument()

      await act(async () => {
        resolveExport(new Blob([]))
      })
      await waitForCondition(() => {
        expect(control).not.toHaveAttribute('aria-busy')
      })
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

    it('gates the export on the applied filters, never on unapplied drafts', async () => {
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
      renderWithProviders(<AuditPage />, {
        operations,
        timeZone: 'Asia/Tokyo',
      })
      await screen.findByText('tenant.create')
      const control = screen.getByRole('button', {
        name: zhCN.audit.export.label,
      })
      const since = screen.getByLabelText(zhCN.audit.filters.since)
      const apply = screen.getByRole('button', {
        name: zhCN.audit.filters.apply,
      })

      fireEvent.change(since, { target: { value: '2026-10-15T09:30' } })
      await user.click(apply)
      expect(control).not.toHaveAttribute('aria-disabled', 'true')
      await user.click(control)
      await waitForCondition(() => expect(seen).toHaveLength(1))
      expect(seen[0]).toEqual({ since: '2026-10-15T00:30:00.000Z' })

      fireEvent.change(since, { target: { value: '2026-10-15T10:30' } })
      expect(control).toHaveAttribute('aria-disabled', 'true')
      expect(
        screen.getByText(zhCN.audit.export.applyFirst),
      ).toBeInTheDocument()
      expect(control).toHaveAccessibleDescription(
        `${zhCN.audit.export.note} ${zhCN.audit.export.applyFirst}`,
      )
      fireEvent.click(control)
      await act(async () => {
        await Promise.resolve()
      })
      expect(seen).toHaveLength(1)

      await user.click(apply)
      expect(control).not.toHaveAttribute('aria-disabled', 'true')
      await user.click(control)
      await waitForCondition(() => expect(seen).toHaveLength(2))
      expect(seen[1]).toEqual({ since: '2026-10-15T01:30:00.000Z' })
      expect(seen[1]).not.toHaveProperty('cursor')
      expect(seen[1]).not.toHaveProperty('limit')

      for (const [label, value] of [
        [zhCN.audit.filters.action, 'tenant.create'],
        [zhCN.audit.filters.outcome, 'failed'],
        [zhCN.audit.filters.actor, 'acc-1'],
      ] as const) {
        fireEvent.change(screen.getByLabelText(label), {
          target: { value },
        })
        expect(control).toHaveAttribute('aria-disabled', 'true')
        expect(
          screen.getByText(zhCN.audit.export.applyFirst),
        ).toBeInTheDocument()
        await user.click(apply)
        expect(control).not.toHaveAttribute('aria-disabled', 'true')
      }
    })

    it('does not export while the since field holds a reading with no instant', async () => {
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
      const { queryClient } = renderWithProviders(<AuditPage />, {
        operations,
        timeZone: 'America/New_York',
      })
      await screen.findByText('tenant.create')

      fireEvent.change(screen.getByLabelText(zhCN.audit.filters.since), {
        target: { value: '2026-03-08T02:30' },
      })
      await user.click(
        screen.getByRole('button', { name: zhCN.audit.filters.apply }),
      )
      expect(
        await screen.findByText(zhCN.audit.filters.timeUnavailable),
      ).toBeInTheDocument()

      const control = screen.getByRole('button', {
        name: zhCN.audit.export.label,
      })
      expect(control).toHaveAttribute('aria-disabled', 'true')
      expect(
        screen.getByText(zhCN.audit.export.applyFirst),
      ).toBeInTheDocument()
      fireEvent.click(control)
      await waitFor(() => expect(queryClient.isFetching()).toBe(0))
      expect(seen).toHaveLength(0)
    })

    it('clears a previous export failure when a later apply is refused', async () => {
      const user = userEvent.setup()
      const operations = createAdminOperations(
        makeAdminBackend({
          listRecords: () => Promise.resolve(page([record])),
          exportRecords: () =>
            Promise.reject({ status: 403, code: 'client.http.403' }),
        }),
      )
      renderWithProviders(<AuditPage />, {
        operations,
        timeZone: 'America/New_York',
      })
      await screen.findByText('tenant.create')

      await user.click(
        screen.getByRole('button', { name: zhCN.audit.export.label }),
      )
      expect(
        await screen.findByText(zhCN.errors.client.httpForbidden),
      ).toBeInTheDocument()

      fireEvent.change(screen.getByLabelText(zhCN.audit.filters.since), {
        target: { value: '2026-03-08T02:30' },
      })
      await user.click(
        screen.getByRole('button', { name: zhCN.audit.filters.apply }),
      )
      expect(
        await screen.findByText(zhCN.audit.filters.timeUnavailable),
      ).toBeInTheDocument()
      expect(
        screen.queryByText(zhCN.errors.client.httpForbidden),
      ).toBeNull()
    })

    it('clears a previous export failure on reset', async () => {
      const user = userEvent.setup()
      const operations = createAdminOperations(
        makeAdminBackend({
          listRecords: () => Promise.resolve(page([record])),
          exportRecords: () =>
            Promise.reject({ status: 403, code: 'client.http.403' }),
        }),
      )
      renderWithProviders(<AuditPage />, {
        operations,
        timeZone: 'America/New_York',
      })
      await screen.findByText('tenant.create')

      await user.click(
        screen.getByRole('button', { name: zhCN.audit.export.label }),
      )
      expect(
        await screen.findByText(zhCN.errors.client.httpForbidden),
      ).toBeInTheDocument()

      await user.click(
        screen.getByRole('button', { name: zhCN.audit.filters.reset }),
      )
      await waitForCondition(() => {
        expect(
          screen.queryByText(zhCN.errors.client.httpForbidden),
        ).toBeNull()
      })
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
      await waitForCondition(() => expect(capture.downloads()).toHaveLength(1))

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
      expect(Math.abs(instant - Date.now())).toBeLessThan(5 * 60 * 1000)
      expect(await capture.blobs()[0]!.text()).toBe(content)
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
      const { queryClient } = renderWithProviders(<AuditPage />, { operations })
      await screen.findByText('tenant.create')
      const control = screen.getByRole('button', {
        name: zhCN.audit.export.label,
      })

      await user.click(control)
      await waitForCondition(() => expect(calls).toBe(1))
      expect(control).toHaveAttribute('aria-busy', 'true')

      fireEvent.click(control)
      await waitFor(() => expect(queryClient.isFetching()).toBe(0))
      expect(calls).toBe(1)

      await act(async () => {
        resolveExport(new Blob([]))
      })
      await waitForCondition(() => {
        expect(control).not.toHaveAttribute('aria-busy')
      })
      expect(control).not.toHaveAttribute('aria-disabled', 'true')
    })

    it('keeps the export control focusable while an export runs and after it settles', async () => {
      const user = userEvent.setup()
      let resolveExport!: (blob: Blob) => void
      const operations = createAdminOperations(
        makeAdminBackend({
          listRecords: () => Promise.resolve(page([record])),
          exportRecords: () =>
            new Promise<Blob>((resolve) => {
              resolveExport = resolve
            }),
        }),
      )
      renderWithProviders(<AuditPage />, { operations })
      await screen.findByText('tenant.create')
      const control = screen.getByRole('button', {
        name: zhCN.audit.export.label,
      })
      control.focus()
      await user.keyboard('{Enter}')
      await waitForCondition(() => {
        expect(control).toHaveAttribute('aria-busy', 'true')
      })
      expect(control).toHaveAttribute('aria-disabled', 'true')
      expect(control).toHaveFocus()

      await act(async () => {
        resolveExport(new Blob([]))
      })
      await waitForCondition(() => {
        expect(control).not.toHaveAttribute('aria-busy')
      })
      expect(control).not.toHaveAttribute('aria-disabled', 'true')
      expect(control).toHaveFocus()
    })

    it('keeps the export control focusable while the drafts are unapplied and refuses activation', async () => {
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
      fireEvent.change(screen.getByLabelText(zhCN.audit.filters.since), {
        target: { value: '2026-10-15T09:30' },
      })

      expect(control).toHaveAttribute('aria-disabled', 'true')
      expect(control).toHaveAccessibleDescription(
        `${zhCN.audit.export.note} ${zhCN.audit.export.applyFirst}`,
      )
      control.focus()
      expect(control).toHaveFocus()

      await user.click(control)
      await user.keyboard('{Enter}')
      await user.keyboard(' ')
      await act(async () => {
        await Promise.resolve()
      })
      expect(seen).toHaveLength(0)
      expect(control).toHaveFocus()
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
        expect(control).not.toHaveAttribute('aria-disabled', 'true')
        expect(control).not.toHaveAttribute('aria-busy')
        expect(screen.queryByText(zhCN.audit.error.title)).toBeNull()
      },
    )
  })
})
