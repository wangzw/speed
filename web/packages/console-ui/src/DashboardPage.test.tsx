/**
 * DashboardPage: the console landing.
 *
 * It derives its KPI row from the roster read (a count of tenants and the
 * active/disabled split) and its recent-activity list from the audit
 * query's newest page. Each panel renders through the console async
 * section, so a refused read (403) shows the permission placeholder
 * without a retry and any other failure the retryable error placeholder.
 */

import { describe, expect, it, vi } from 'vitest'
import { act, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { focusManager } from '@tanstack/react-query'
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

const COPY = zhCN.common as {
  readonly updatedAt: string
  readonly refreshed: string
  readonly refresh: string
}

/** The marker a refused account read leaves on the recent-activity actor. */
const UNRESOLVED_ACTOR = `acc-1 (${zhCN.common.accountUnresolved})`

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
    const refresh = await screen.findByRole('button', { name: zhCN.common.refresh })
    expect(hasCoarsePointerTouchTarget(refresh)).toBe(true)
  })

  it('summarizes the roster in the KPI row', async () => {
    const operations = createConsoleOperations(
      makeConsoleBackend({ listTenants: () => Promise.resolve(TENANTS) }),
    )
    renderWithProviders(<DashboardPage />, { operations })

    expect(await screen.findByText(zhCN.dashboard.kpi.tenants)).toBeInTheDocument()
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
    const recent = await screen.findByRole('heading', { level: 2, name: zhCN.dashboard.recent.title })
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
    const cell = await screen.findByLabelText(UNRESOLVED_ACTOR)
    expect(cell).toHaveTextContent('acc-1')
    expect(cell).toHaveAttribute('title', zhCN.common.accountUnresolved)
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
      columnVisibility(screen.getByRole('columnheader', { name: zhCN.dashboard.recent.column.object })),
    ).toEqual({ hiddenAt: 0, visibleFrom: 600 })
    // The body cell carries its own copy of the rule; assert it too so a
    // header/body divergence cannot pass with only the header checked.
    expect(
      columnVisibility(screen.getByText('acme').closest('td')!),
    ).toEqual({ hiddenAt: 0, visibleFrom: 600 })
    expect(
      columnVisibility(screen.getByRole('columnheader', { name: zhCN.dashboard.recent.column.actor })),
    ).toEqual({ hiddenAt: 0, visibleFrom: 900 })
    expect(
      columnVisibility(screen.getByRole('columnheader', { name: zhCN.dashboard.recent.column.outcome })),
    ).toEqual({ hiddenAt: 0, visibleFrom: 900 })
    expect(
      columnVisibility(screen.getByRole('columnheader', { name: zhCN.dashboard.recent.column.time })),
    ).toBeNull()
    expect(
      columnVisibility(screen.getByRole('columnheader', { name: zhCN.dashboard.recent.column.action })),
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
      name: zhCN.dashboard.health.title,
    })
    const section = health.closest('section') as HTMLElement
    expect(
      await within(section).findByText(zhCN.dashboard.health.logins),
    ).toBeInTheDocument()
    expect(within(section).getByText('2')).toBeInTheDocument()
    expect(within(section).getByText(zhCN.dashboard.health.activity)).toBeInTheDocument()
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
      name: zhCN.dashboard.health.title,
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
    expect(await screen.findByText(zhCN.dashboard.health.error.title)).toBeInTheDocument()
    expect(screen.getByText(zhCN.dashboard.health.error.description)).toBeInTheDocument()
    expect(screen.getByRole('button', { name: zhCN.dashboard.retry })).toBeInTheDocument()
  })

  it('renders the permission placeholder with no retry when the roster read is refused with 403', async () => {
    const operations = createConsoleOperations(
      makeConsoleBackend({
        listTenants: () => Promise.reject({ status: 403, code: 'client.http.403' }),
      }),
    )
    renderWithProviders(<DashboardPage />, { operations })
    expect(await screen.findByText(zhCN.forbidden.title)).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: zhCN.dashboard.retry })).toBeNull()
  })

  it('renders the retryable error placeholder when the roster read fails otherwise', async () => {
    const operations = createConsoleOperations(
      makeConsoleBackend({
        listTenants: () => Promise.reject(new Error('boom')),
      }),
    )
    renderWithProviders(<DashboardPage />, { operations })
    expect(await screen.findByText(zhCN.dashboard.error.title)).toBeInTheDocument()
    expect(screen.getByText(zhCN.dashboard.error.description)).toBeInTheDocument()
    expect(screen.getByRole('button', { name: zhCN.dashboard.retry })).toBeInTheDocument()
  })

  it('shows the oldest settle time in the refresh action group', async () => {
    const operations = createConsoleOperations(
      makeConsoleBackend({
        listTenants: () => Promise.resolve(TENANTS),
        listRecords: () =>
          Promise.resolve({
            entries: [recordFixture({ id: 'rec-9', action: 'tenant.read' })],
            next: '',
          }),
      }),
    )
    const { queryClient } = renderWithProviders(<DashboardPage />, { operations })
    await screen.findByText('tenant.read')

    // The page's body reads: the roster and the three audit reads (the
    // recent list and the two health windows). The account identity read
    // inside the actor cell is not a body read and does not count.
    const bodyReads = () =>
      queryClient.getQueryCache().getAll().filter((query) => {
        const key = query.queryKey
        return (
          Array.isArray(key) &&
          key[0] === 'console-test' &&
          (key[1] === 'tenants' || key[1] === 'audit')
        )
      })
    await waitFor(() => {
      const states = bodyReads()
      expect(states).toHaveLength(4)
      expect(states.every((state) => state.state.dataUpdatedAt > 0)).toBe(true)
    })
    const oldest = Math.min(
      ...bodyReads().map((state) => state.state.dataUpdatedAt),
    )
    const expected = COPY.updatedAt.replace(
      '{{time}}',
      new Intl.DateTimeFormat('zh-CN', {
        dateStyle: 'medium',
        timeStyle: 'long',
        timeZone: 'UTC',
      }).format(new Date(oldest)),
    )
    const header = screen.getByRole('banner')
    const label = await within(header).findByText(expected)
    const refresh = within(header).getByRole('button', { name: COPY.refresh })
    expect(
      label.compareDocumentPosition(refresh) & Node.DOCUMENT_POSITION_FOLLOWING,
    ).toBeTruthy()
  })

  it('does not announce an automatic re-read', async () => {
    const listRecords = vi.fn(() => Promise.resolve({ entries: [], next: '' }))
    const operations = createConsoleOperations(
      makeConsoleBackend({
        listTenants: () => Promise.resolve(TENANTS),
        listRecords,
      }),
    )
    renderWithProviders(<DashboardPage />, { operations })
    await waitFor(() => expect(listRecords).toHaveBeenCalledTimes(3))

    const header = screen.getByRole('banner')
    const status = within(header).getByRole('status')
    expect(status.textContent).toBe('')

    // The tab becoming visible again is an automatic re-read moment: the
    // page moves the health window and the query client re-reads on the
    // visibility change, and neither announces.
    act(() => {
      focusManager.setFocused(false)
    })
    act(() => {
      focusManager.setFocused(true)
    })
    await waitFor(() =>
      expect(listRecords.mock.calls.length).toBeGreaterThan(3),
    )
    expect(status.textContent).toBe('')
  })

  it('has no axe violations', async () => {
    const operations = createConsoleOperations(
      makeConsoleBackend({ listTenants: () => Promise.resolve(TENANTS) }),
    )
    renderWithProviders(<DashboardPage />, { operations })
    await screen.findByText(zhCN.dashboard.kpi.tenants)
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
