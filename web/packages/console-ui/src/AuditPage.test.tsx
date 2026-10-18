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
import { act, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { AuditPage } from './AuditPage.js'
import type { ConsoleAuditRecord } from './operations.js'
import { renderWithProviders } from '../test-utils/render.js'
import {
  columnVisibility,
  containerMaxWidthOf,
} from '../test-utils/container-visibility.js'
import { hasCoarsePointerTouchTarget } from '@speed/test-utils/emitted-css'
import {
  accountFixture,
  createConsoleOperations,
  makeConsoleBackend,
} from '../test-utils/operations.js'
import { expectNoAxeViolations } from '../test-utils/axe.js'
import zhCN from './locales/zh-CN.json' with { type: 'json' }

const COPY = zhCN.common as { readonly updatedAt: string; readonly refreshed: string; readonly refresh: string }

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

  it('keeps the outcome visible in a laptop-wide container alongside the actor', async () => {
    const operations = createConsoleOperations(
      makeConsoleBackend({ listRecords: () => Promise.resolve(page([record])) }),
    )
    renderWithProviders(<AuditPage />, { operations })
    await screen.findByText('tenant.create')
    // The table's container is 1184px wide beside the console nav at a
    // 1512px viewport: the object, the actor AND the outcome must all clear
    // that width, so a failed record is distinguishable without widening
    // the window. Under a still narrower container the object column hides
    // again below sm -- the restore is the low end of its tier, not an
    // always-on column.
    expect(
      columnVisibility(screen.getByRole('columnheader', { name: '对象' })),
    ).toEqual({ hiddenAt: 0, visibleFrom: 600 })
    // The header and the body each apply the column's visibility rule on
    // their own, so assert the body cells too: a divergence between the two
    // would otherwise pass with only the header checked.
    expect(
      columnVisibility(screen.getByText('tenant/acme').closest('td')!),
    ).toEqual({ hiddenAt: 0, visibleFrom: 600 })
    expect(
      columnVisibility(screen.getByRole('columnheader', { name: '操作者' })),
    ).toEqual({ hiddenAt: 0, visibleFrom: 900 })
    expect(
      columnVisibility(screen.getByRole('columnheader', { name: '结果' })),
    ).toEqual({ hiddenAt: 0, visibleFrom: 900 })
    expect(
      columnVisibility(screen.getByText('ok').closest('td')!),
    ).toEqual({ hiddenAt: 0, visibleFrom: 900 })
    // The anchors never hide.
    expect(
      columnVisibility(screen.getByRole('columnheader', { name: '时间' })),
    ).toBeNull()
    expect(
      columnVisibility(screen.getByRole('columnheader', { name: '动作' })),
    ).toBeNull()
  })

  it('caps the action column with the primary cap and the object and actor flat', async () => {
    const action = 'pkg.objectstore.metadata.create'
    const object = '/api/v1/objects/a-very-long-registered-route'
    const operations = createConsoleOperations(
      makeConsoleBackend({
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
    expect(screen.getByText('审计面未能完成这次请求。')).toBeInTheDocument()
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

  it('shows the update time in the action group only once a read has settled', async () => {
    let resolveRead!: (value: ReturnType<typeof page>) => void
    const operations = createConsoleOperations(
      makeConsoleBackend({
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
    const operations = createConsoleOperations(
      makeConsoleBackend({
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
    const operations = createConsoleOperations(
      makeConsoleBackend({
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
    const operations = createConsoleOperations(
      makeConsoleBackend({ listRecords: () => Promise.resolve(page([record])) }),
    )
    renderWithProviders(<AuditPage />, { operations })
    await screen.findByText('tenant.create')
    await expectNoAxeViolations()
  })
})
