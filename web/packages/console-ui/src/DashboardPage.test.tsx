/**
 * DashboardPage: the console landing.
 *
 * It derives its KPI row from the roster read (a count of tenants and the
 * active/disabled split) and its recent-activity list from the audit
 * query's newest page. Each panel renders through the console async
 * section, so a refused read (403) shows the permission placeholder
 * without a retry and any other failure the retryable error placeholder.
 */

import { describe, expect, it } from 'vitest'
import { act, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { DashboardPage } from './DashboardPage.js'
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
  recordFixture,
} from '../test-utils/operations.js'
import { expectNoAxeViolations } from '../test-utils/axe.js'
import zhCN from './locales/zh-CN.json' with { type: 'json' }

const TENANTS = [
  { id: 'acme', alias: 'Acme', status: 'active' as const },
  { id: 'beta', alias: 'Beta', status: 'active' as const },
  { id: 'old', alias: 'Old', status: 'disabled' as const },
]

describe('DashboardPage', () => {
  it('raises its refresh action to the 44px floor under a coarse pointer', async () => {
    const operations = createConsoleOperations(
      makeConsoleBackend({ listTenants: () => Promise.resolve(TENANTS) }),
    )
    renderWithProviders(<DashboardPage />, { operations })
    const refresh = await screen.findByRole('button', { name: '刷新' })
    expect(hasCoarsePointerTouchTarget(refresh)).toBe(true)
  })

  it('summarizes the roster in the KPI row', async () => {
    const operations = createConsoleOperations(
      makeConsoleBackend({ listTenants: () => Promise.resolve(TENANTS) }),
    )
    renderWithProviders(<DashboardPage />, { operations })

    expect(await screen.findByText('租户总数')).toBeInTheDocument()
    // total / active / disabled, derived from the whole roster read.
    expect(screen.getByText('3')).toBeInTheDocument()
    expect(screen.getByText('2')).toBeInTheDocument()
    expect(screen.getByText('1')).toBeInTheDocument()
  })

  it('lists the newest audit activity', async () => {
    const operations = createConsoleOperations(
      makeConsoleBackend({
        listRecords: () =>
          Promise.resolve({
            entries: [recordFixture({ id: 'rec-9', action: 'tenant.read' })],
            next: '',
          }),
      }),
    )
    renderWithProviders(<DashboardPage />, { operations })
    const recent = await screen.findByRole('heading', { level: 2, name: '最近活动' })
    expect(recent).toBeInTheDocument()
    expect(await screen.findByText('tenant.read')).toBeInTheDocument()
  })

  it('renders the recent activity actor as the account display name', async () => {
    const operations = createConsoleOperations(
      makeConsoleBackend({
        listRecords: () =>
          Promise.resolve({
            entries: [recordFixture({ id: 'rec-9' })],
            next: '',
          }),
        getAccount: () =>
          Promise.resolve(accountFixture({ display_name: 'Ada Lovelace' })),
      }),
    )
    renderWithProviders(<DashboardPage />, { operations })
    expect(await screen.findByText('Ada Lovelace')).toBeInTheDocument()
    expect(screen.queryByText('acc-1')).not.toBeInTheDocument()
  })

  it('marks a recent activity actor whose account read is refused as unresolved', async () => {
    const operations = createConsoleOperations(
      makeConsoleBackend({
        listRecords: () =>
          Promise.resolve({
            entries: [recordFixture({ id: 'rec-9' })],
            next: '',
          }),
        getAccount: () => Promise.reject(new Error('forbidden')),
      }),
    )
    renderWithProviders(<DashboardPage />, { operations })
    // The cell keeps the recorded id but labels it unresolved rather than
    // passing it off as a resolved (or system) actor.
    const cell = await screen.findByLabelText(/身份未解析/)
    expect(cell).toHaveTextContent('acc-1')
    expect(cell).toHaveAttribute('title', '身份未解析（需账户读取权限）')
  })

  it('renders recent activity with the audit page columns and priorities', async () => {
    const operations = createConsoleOperations(
      makeConsoleBackend({
        listRecords: () =>
          Promise.resolve({
            entries: [recordFixture({ id: 'rec-9', action: 'tenant.read' })],
            next: '',
          }),
      }),
    )
    renderWithProviders(<DashboardPage />, { operations })
    await screen.findByText('tenant.read')
    // The list carries the same set as the audit query page: action and
    // object identify the record, so the object column outranks the actor
    // and the outcome, which share the md tier so a failure stays visible.
    expect(
      columnVisibility(screen.getByRole('columnheader', { name: '对象' })),
    ).toEqual({ hiddenAt: 0, visibleFrom: 600 })
    // The body cell carries its own copy of the rule; assert it too so a
    // header/body divergence cannot pass with only the header checked.
    expect(
      columnVisibility(screen.getByText('acme').closest('td')!),
    ).toEqual({ hiddenAt: 0, visibleFrom: 600 })
    expect(
      columnVisibility(screen.getByRole('columnheader', { name: '操作者' })),
    ).toEqual({ hiddenAt: 0, visibleFrom: 900 })
    expect(
      columnVisibility(screen.getByRole('columnheader', { name: '结果' })),
    ).toEqual({ hiddenAt: 0, visibleFrom: 900 })
    expect(
      columnVisibility(screen.getByRole('columnheader', { name: '时间' })),
    ).toBeNull()
    expect(
      columnVisibility(screen.getByRole('columnheader', { name: '动作' })),
    ).toBeNull()
  })

  it('caps the recent-activity action at the primary cap and the object and actor flat', async () => {
    const action = 'pkg.objectstore.metadata.create'
    const object = '/api/v1/objects/a-very-long-registered-route'
    const operations = createConsoleOperations(
      makeConsoleBackend({
        listRecords: () =>
          Promise.resolve({
            entries: [recordFixture({ id: 'rec-9', action, object })],
            next: '',
          }),
      }),
    )
    renderWithProviders(<DashboardPage />, { operations })
    await screen.findByText(action)

    // The action is the primary identity: its cap grows at md. The cap
    // sits on the wrapper that holds the text and the copy control
    // together, so the control is counted inside the budget.
    const actionCell = screen.getByText(action).closest('td')
    expect(actionCell).toHaveAttribute('title', action)
    expect(containerMaxWidthOf(actionCell!.firstElementChild!)).toEqual(
      new Map([
        [0, 160],
        [900, 200],
      ]),
    )

    // The object and the actor hold a flat cap that does not grow.
    const objectCell = screen.getByText(object).closest('td')
    expect(objectCell).toHaveAttribute('title', object)
    expect(objectCell!.firstElementChild).toHaveStyle({ maxWidth: '160px' })
    expect(
      containerMaxWidthOf(objectCell!.firstElementChild!),
    ).toEqual(new Map())

    const actorText = await screen.findByText('Operator')
    const actorCell = actorText.closest('td')
    expect(actorCell).toHaveAttribute('title', 'acc-1')
    expect(actorCell!.firstElementChild).toHaveStyle({ maxWidth: '160px' })
    expect(
      containerMaxWidthOf(actorCell!.firstElementChild!),
    ).toEqual(new Map())
  })

  it('derives the platform health row from the windowed audit read', async () => {
    const operations = createConsoleOperations(
      makeConsoleBackend({
        // The sign-in health read narrows by the login action; the activity
        // read carries only the window. Each answers with its own page.
        listRecords: (params) =>
          params?.action?.includes('authn.login')
            ? Promise.resolve({
                entries: [
                  recordFixture({ id: 'l1', action: 'authn.login' }),
                  recordFixture({ id: 'l2', action: 'authn.login' }),
                ],
                next: '',
              })
            : Promise.resolve({
                entries: [recordFixture({ id: 'r1' })],
                next: '',
              }),
      }),
    )
    renderWithProviders(<DashboardPage />, { operations })
    const health = await screen.findByRole('heading', {
      level: 2,
      name: '平台健康度',
    })
    const section = health.closest('section') as HTMLElement
    expect(
      await within(section).findByText('近 24 小时登录（成功）'),
    ).toBeInTheDocument()
    expect(within(section).getByText('2')).toBeInTheDocument()
    expect(within(section).getByText('近 24 小时审计活动')).toBeInTheDocument()
    expect(within(section).getByText('1')).toBeInTheDocument()
  })

  it('reports a windowed health count as a lower bound when the page has a next cursor', async () => {
    const operations = createConsoleOperations(
      makeConsoleBackend({
        listRecords: (params) =>
          params?.action?.includes('authn.login')
            ? Promise.resolve({
                entries: [recordFixture({ id: 'l1', action: 'authn.login' })],
                next: 'cur-2',
              })
            : Promise.resolve({ entries: [], next: '' }),
      }),
    )
    renderWithProviders(<DashboardPage />, { operations })
    const health = await screen.findByRole('heading', {
      level: 2,
      name: '平台健康度',
    })
    const section = health.closest('section') as HTMLElement
    // A page that carries a next cursor is a lower bound, shown with a "+".
    expect(await within(section).findByText('1+')).toBeInTheDocument()
  })

  it('moves the platform-health window forward on a refresh', async () => {
    const healthWindows: string[] = []
    const operations = createConsoleOperations(
      makeConsoleBackend({
        listRecords: (params) => {
          if (params?.since !== undefined) {
            healthWindows.push(params.since)
          }
          return Promise.resolve({
            entries: params?.action?.includes('authn.login')
              ? [recordFixture({ id: 'l1', action: 'authn.login' })]
              : [],
            next: '',
          })
        },
      }),
    )
    const user = userEvent.setup()
    renderWithProviders(<DashboardPage />, { operations })
    const health = await screen.findByRole('heading', {
      level: 2,
      name: zhCN.dashboard.health.title,
    })
    const section = health.closest('section') as HTMLElement
    await within(section).findByText(zhCN.dashboard.health.activity)
    const initial = healthWindows[0]!
    // Both health reads share the window they were opened with.
    expect(healthWindows).toEqual([initial, initial])

    await user.click(
      screen.getByRole('button', { name: zhCN.common.refresh }),
    )

    // The refresh re-reads both health reads under a later window start
    // rather than re-reading the old window again.
    await waitFor(() => {
      expect(
        healthWindows.filter(
          (since) => Date.parse(since) > Date.parse(initial),
        ),
      ).toHaveLength(2)
    })
    expect(
      new Set(
        healthWindows.filter((since) => Date.parse(since) > Date.parse(initial)),
      ).size,
    ).toBe(1)
  })

  it('moves the platform-health window forward when the tab becomes visible', async () => {
    const healthWindows: string[] = []
    const operations = createConsoleOperations(
      makeConsoleBackend({
        listRecords: (params) => {
          if (params?.since !== undefined) {
            healthWindows.push(params.since)
          }
          return Promise.resolve({ entries: [], next: '' })
        },
      }),
    )
    renderWithProviders(<DashboardPage />, { operations })
    const health = await screen.findByRole('heading', {
      level: 2,
      name: zhCN.dashboard.health.title,
    })
    const section = health.closest('section') as HTMLElement
    await within(section).findByText(zhCN.dashboard.health.activity)
    const initial = healthWindows[0]!
    try {
      act(() => {
        setDocumentVisibility('hidden')
        window.dispatchEvent(new Event('visibilitychange'))
      })
      // Going hidden is not a moment the page reads.
      expect(
        healthWindows.filter((since) => Date.parse(since) > Date.parse(initial)),
      ).toHaveLength(0)

      act(() => {
        setDocumentVisibility('visible')
        window.dispatchEvent(new Event('visibilitychange'))
      })

      await waitFor(() => {
        expect(
          healthWindows.filter(
            (since) => Date.parse(since) > Date.parse(initial),
          ),
        ).toHaveLength(2)
      })
      expect(
        new Set(
          healthWindows.filter(
            (since) => Date.parse(since) > Date.parse(initial),
          ),
        ).size,
      ).toBe(1)
    } finally {
      Reflect.deleteProperty(document, 'visibilityState')
    }
  })

  it('moves the platform-health window forward when the connection comes back', async () => {
    const healthWindows: string[] = []
    const operations = createConsoleOperations(
      makeConsoleBackend({
        listRecords: (params) => {
          if (params?.since !== undefined) {
            healthWindows.push(params.since)
          }
          return Promise.resolve({ entries: [], next: '' })
        },
      }),
    )
    renderWithProviders(<DashboardPage />, { operations })
    const health = await screen.findByRole('heading', {
      level: 2,
      name: zhCN.dashboard.health.title,
    })
    const section = health.closest('section') as HTMLElement
    await within(section).findByText(zhCN.dashboard.health.activity)
    const initial = healthWindows[0]!

    act(() => {
      window.dispatchEvent(new Event('offline'))
    })
    // Losing the connection is not a moment the page reads.
    expect(
      healthWindows.filter((since) => Date.parse(since) > Date.parse(initial)),
    ).toHaveLength(0)

    act(() => {
      window.dispatchEvent(new Event('online'))
    })

    await waitFor(() => {
      expect(
        healthWindows.filter(
          (since) => Date.parse(since) > Date.parse(initial),
        ),
      ).toHaveLength(2)
    })
    expect(
      new Set(
        healthWindows.filter((since) => Date.parse(since) > Date.parse(initial)),
      ).size,
    ).toBe(1)
  })

  it('keeps the settled health numbers while the new window is in flight', async () => {
    let initialActivitySince: string | undefined
    let pendingActivitySince: string | undefined
    let releaseActivity: (() => void) | undefined
    let activityReads = 0
    const operations = createConsoleOperations(
      makeConsoleBackend({
        listRecords: (params) => {
          if (params?.action?.includes('authn.login')) {
            return Promise.resolve({
              entries: [
                recordFixture({ id: 'l1', action: 'authn.login' }),
                recordFixture({ id: 'l2', action: 'authn.login' }),
              ],
              next: '',
            })
          }
          if (params?.since !== undefined) {
            activityReads += 1
            if (activityReads === 1) {
              initialActivitySince = params.since
              return Promise.resolve({
                entries: [recordFixture({ id: 'a1' })],
                next: '',
              })
            }
            pendingActivitySince = params.since
            return new Promise((resolve) => {
              releaseActivity = () =>
                resolve({
                  entries: [
                    recordFixture({ id: 'a2' }),
                    recordFixture({ id: 'a3' }),
                    recordFixture({ id: 'a4' }),
                  ],
                  next: '',
                })
            })
          }
          return Promise.resolve({ entries: [], next: '' })
        },
      }),
    )
    const user = userEvent.setup()
    renderWithProviders(<DashboardPage />, { operations })
    const health = await screen.findByRole('heading', {
      level: 2,
      name: zhCN.dashboard.health.title,
    })
    const section = health.closest('section') as HTMLElement
    // Settled: two successful sign-ins and one audit record.
    expect(await within(section).findByText('2')).toBeInTheDocument()
    expect(within(section).getByText('1')).toBeInTheDocument()

    await user.click(
      screen.getByRole('button', { name: zhCN.common.refresh }),
    )

    // The new window's read is in flight: the settled numbers stay on
    // screen, and the in-flight read carries the advanced window.
    await waitFor(() => expect(releaseActivity).toBeDefined())
    expect(Date.parse(pendingActivitySince!)).toBeGreaterThan(
      Date.parse(initialActivitySince!),
    )
    expect(within(section).getByText('1')).toBeInTheDocument()
    expect(
      within(section).queryByRole('status', {
        name: zhCN.dashboard.health.loading,
      }),
    ).toBeNull()

    await act(async () => {
      releaseActivity?.()
    })
    expect(await within(section).findByText('3')).toBeInTheDocument()
  })

  it('renders the retryable placeholder when the health read fails', async () => {
    const operations = createConsoleOperations(
      makeConsoleBackend({
        listRecords: (params) =>
          params?.action?.includes('authn.login')
            ? Promise.reject(new Error('boom'))
            : Promise.resolve({ entries: [], next: '' }),
      }),
    )
    renderWithProviders(<DashboardPage />, { operations })
    expect(await screen.findByText('平台健康度加载失败')).toBeInTheDocument()
    expect(screen.getByText('审计面未能完成这次请求。')).toBeInTheDocument()
    expect(screen.getByRole('button', { name: '重试' })).toBeInTheDocument()
  })

  it('renders the permission placeholder with no retry when the roster read is refused with 403', async () => {
    const operations = createConsoleOperations(
      makeConsoleBackend({
        listTenants: () => Promise.reject({ status: 403, code: 'client.http.403' }),
      }),
    )
    renderWithProviders(<DashboardPage />, { operations })
    expect(await screen.findByText('权限不足')).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: '重试' })).toBeNull()
  })

  it('renders the retryable error placeholder when the roster read fails otherwise', async () => {
    const operations = createConsoleOperations(
      makeConsoleBackend({
        listTenants: () => Promise.reject(new Error('boom')),
      }),
    )
    renderWithProviders(<DashboardPage />, { operations })
    expect(await screen.findByText('概览加载失败')).toBeInTheDocument()
    expect(screen.getByText('数据面未能完成这次请求。')).toBeInTheDocument()
    expect(screen.getByRole('button', { name: '重试' })).toBeInTheDocument()
  })

  it('has no axe violations', async () => {
    const operations = createConsoleOperations(
      makeConsoleBackend({ listTenants: () => Promise.resolve(TENANTS) }),
    )
    renderWithProviders(<DashboardPage />, { operations })
    await screen.findByText('租户总数')
    await expectNoAxeViolations()
  })
})

/** Overrides jsdom's visibility state for the visibility cases; the caller
 * removes the own property afterwards so the prototype's getter is back. */
function setDocumentVisibility(state: 'visible' | 'hidden'): void {
  Object.defineProperty(document, 'visibilityState', {
    configurable: true,
    get: () => state,
  })
}
