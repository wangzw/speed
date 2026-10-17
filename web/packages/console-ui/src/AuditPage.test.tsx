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

import { describe, expect, it, vi } from 'vitest'
import { screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { AuditPage } from './AuditPage.js'
import type { ConsoleAuditRecord } from './operations.js'
import { renderWithProviders } from '../test-utils/render.js'
import { columnVisibility } from '../test-utils/container-visibility.js'
import { hasCoarsePointerTouchTarget } from '@speed/test-utils/emitted-css'
import {
  accountFixture,
  createConsoleOperations,
  makeConsoleBackend,
} from '../test-utils/operations.js'
import { expectNoAxeViolations } from '../test-utils/axe.js'

const record: ConsoleAuditRecord = {
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

const page = (entries: readonly ConsoleAuditRecord[], next = '') => ({
  entries: [...entries],
  next,
})

describe('AuditPage', () => {
  it('raises its toolbar and pager actions to the 44px floor under a coarse pointer', async () => {
    const operations = createConsoleOperations(
      makeConsoleBackend({
        listRecords: () => Promise.resolve(page([record], 'next')),
      }),
    )
    renderWithProviders(<AuditPage />, { operations })
    const gated = [
      await screen.findByRole('button', { name: '刷新' }),
      screen.getByRole('button', { name: '上一页' }),
      screen.getByRole('button', { name: '下一页' }),
    ]
    for (const control of gated) {
      expect(hasCoarsePointerTouchTarget(control)).toBe(true)
    }
  })

  it('renders the settled empty state when no record matches', async () => {
    const operations = createConsoleOperations(makeConsoleBackend())
    renderWithProviders(<AuditPage />, { operations })
    expect(await screen.findByText('暂无审计记录')).toBeInTheDocument()
  })

  it('lists records', async () => {
    const operations = createConsoleOperations(
      makeConsoleBackend({ listRecords: () => Promise.resolve(page([record])) }),
    )
    renderWithProviders(<AuditPage />, { operations })
    expect(await screen.findByText('tenant.create')).toBeInTheDocument()
    expect(screen.getByText('tenant/acme')).toBeInTheDocument()
    expect(screen.getByText('ok')).toBeInTheDocument()
  })

  it('renders records as a table with column headers', async () => {
    const operations = createConsoleOperations(
      makeConsoleBackend({ listRecords: () => Promise.resolve(page([record])) }),
    )
    renderWithProviders(<AuditPage />, { operations })
    await screen.findByText('tenant.create')
    for (const name of ['时间', '动作', '结果', '操作者', '对象']) {
      expect(screen.getByRole('columnheader', { name })).toBeInTheDocument()
    }
  })

  it('keeps the object column visible in a laptop-wide container and sheds the outcome first', async () => {
    const operations = createConsoleOperations(
      makeConsoleBackend({ listRecords: () => Promise.resolve(page([record])) }),
    )
    renderWithProviders(<AuditPage />, { operations })
    await screen.findByText('tenant.create')
    // The table's container is 1184px wide beside the console nav at a
    // 1512px viewport: the object column (and the actor) must clear that
    // width, while the outcome column degrades below it. Under a still
    // narrower container the object column hides again below sm -- the
    // restore is the low end of its tier, not an always-on column.
    expect(
      columnVisibility(screen.getByRole('columnheader', { name: '对象' })),
    ).toEqual({ hiddenAt: 0, visibleFrom: 600 })
    expect(
      columnVisibility(screen.getByRole('columnheader', { name: '操作者' })),
    ).toEqual({ hiddenAt: 0, visibleFrom: 900 })
    expect(
      columnVisibility(screen.getByRole('columnheader', { name: '结果' })),
    ).toEqual({ hiddenAt: 0, visibleFrom: 1200 })
    // The anchors never hide.
    expect(
      columnVisibility(screen.getByRole('columnheader', { name: '时间' })),
    ).toBeNull()
    expect(
      columnVisibility(screen.getByRole('columnheader', { name: '动作' })),
    ).toBeNull()
  })

  it('formats the recorded time in UTC in the current language', async () => {
    const operations = createConsoleOperations(
      makeConsoleBackend({ listRecords: () => Promise.resolve(page([record])) }),
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
    const operations = createConsoleOperations(
      makeConsoleBackend({
        listRecords: () =>
          Promise.resolve(page([{ ...record, occurred_at: 'not-a-date' }])),
      }),
    )
    renderWithProviders(<AuditPage />, { operations })
    expect(await screen.findByText('tenant.create')).toBeInTheDocument()
    expect(screen.queryByText('not-a-date')).not.toBeInTheDocument()
  })

  it('renders the actor as the account display name', async () => {
    const operations = createConsoleOperations(
      makeConsoleBackend({
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
    const operations = createConsoleOperations(
      makeConsoleBackend({
        listRecords: () =>
          Promise.resolve(page([{ ...record, actor: 'system' }])),
        getAccount: () => Promise.reject(new Error('not an account')),
      }),
    )
    renderWithProviders(<AuditPage />, { operations })
    expect(await screen.findByText('system')).toBeInTheDocument()
  })

  it('marks an actor whose account read is refused as unresolved', async () => {
    const operations = createConsoleOperations(
      makeConsoleBackend({
        listRecords: () => Promise.resolve(page([record])),
        getAccount: () => Promise.reject(new Error('forbidden')),
      }),
    )
    renderWithProviders(<AuditPage />, { operations })
    // The cell shows the recorded id, but labelled as unresolved rather
    // than passed off as a resolved (or system) name.
    const cell = await screen.findByLabelText(/身份未解析/)
    expect(cell).toHaveTextContent('acc-1')
    expect(cell).toHaveAttribute('title', '身份未解析（需账户读取权限）')
  })

  it('applies the action filter server-side on submit', async () => {
    const user = userEvent.setup()
    const seen: { action?: string[] } = {}
    const operations = createConsoleOperations(
      makeConsoleBackend({
        listRecords: (params) => {
          seen.action = params?.action
          return Promise.resolve(page([record]))
        },
      }),
    )
    renderWithProviders(<AuditPage />, { operations })
    await screen.findByText('tenant.create')
    await user.type(
      screen.getByLabelText('动作筛选'),
      'tenant.create, account.delete',
    )
    await user.click(screen.getByRole('button', { name: '应用' }))
    await vi.waitFor(() => {
      expect(seen.action).toEqual(['tenant.create', 'account.delete'])
    })
  })

  it('applies the outcome and actor filters server-side on submit', async () => {
    const user = userEvent.setup()
    const seen: { outcome?: string[]; actor?: string[] } = {}
    const operations = createConsoleOperations(
      makeConsoleBackend({
        listRecords: (params) => {
          seen.outcome = params?.outcome
          seen.actor = params?.actor
          return Promise.resolve(page([record]))
        },
      }),
    )
    renderWithProviders(<AuditPage />, { operations })
    await screen.findByText('tenant.create')
    await user.type(screen.getByLabelText('结果筛选'), 'ok, denied')
    await user.type(screen.getByLabelText('操作者筛选'), 'acc-1')
    await user.click(screen.getByRole('button', { name: '应用' }))
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
    const operations = createConsoleOperations(
      makeConsoleBackend({
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
    await user.type(screen.getByLabelText('动作筛选'), 'tenant.create')
    await user.type(screen.getByLabelText('结果筛选'), 'ok')
    await user.type(screen.getByLabelText('操作者筛选'), 'acc-1')
    await user.click(screen.getByRole('button', { name: '应用' }))
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
    await user.click(screen.getByRole('button', { name: '重置' }))
    await vi.waitFor(() => {
      const last = requests.at(-1)
      expect(last?.action ?? []).toEqual([])
      expect(last?.outcome ?? []).toEqual([])
      expect(last?.actor ?? []).toEqual([])
    })
  })

  it('pages forward with the cursor the read answered with', async () => {
    const user = userEvent.setup()
    const operations = createConsoleOperations(
      makeConsoleBackend({
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
    await user.click(screen.getByRole('button', { name: '下一页' }))
    expect(await screen.findByText('tenant.disable')).toBeInTheDocument()
    expect(screen.getByRole('button', { name: '上一页' })).toBeEnabled()
  })

  it('renders the retryable error placeholder when the read fails', async () => {
    const operations = createConsoleOperations(
      makeConsoleBackend({
        listRecords: () => Promise.reject(new Error('boom')),
      }),
    )
    renderWithProviders(<AuditPage />, { operations })
    expect(await screen.findByText('审计记录加载失败')).toBeInTheDocument()
    expect(screen.getByRole('button', { name: '重试' })).toBeInTheDocument()
  })

  it('renders the permission placeholder with no retry when the read is refused with 403', async () => {
    const operations = createConsoleOperations(
      makeConsoleBackend({
        listRecords: () => Promise.reject({ status: 403, code: 'client.http.403' }),
      }),
    )
    renderWithProviders(<AuditPage />, { operations })
    expect(await screen.findByText('权限不足')).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: '重试' })).toBeNull()
  })

  it('has no axe violations', async () => {
    const operations = createConsoleOperations(
      makeConsoleBackend({ listRecords: () => Promise.resolve(page([record])) }),
    )
    renderWithProviders(<AuditPage />, { operations })
    await screen.findByText('tenant.create')
    await expectNoAxeViolations()
  })
})
