/**
 * DashboardPage: the admin landing.
 *
 * It derives its KPI row from the roster read (a count of tenants and the
 * active/disabled split) and its recent-activity list from the audit
 * query's newest page. Each panel renders through the admin async
 * section, so a refused read (403) shows the permission placeholder
 * without a retry and any other failure the retryable error placeholder.
 */

import { describe, expect, it, vi } from 'vitest'
import { act, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { focusManager } from '@tanstack/react-query'
import type { FeatureName } from '@speed/api-sdk/features'
import { DashboardPage } from './DashboardPage.js'
import type { DashboardPageProps } from './DashboardPage.js'
import { AdminFeatureProvider } from './internal/features.js'
import { TEST_TIME_ZONE } from '@speed/test-utils/render'
import { renderWithProviders } from '../test-utils/render.js'
import type { RenderWithProvidersOptions } from '../test-utils/render.js'
import {
  columnVisibility,
  containerMaxWidthOf,
} from '../test-utils/container-visibility.js'
import { hasCoarsePointerTouchTarget } from '@speed/test-utils/emitted-css'
import {
  accountFixture,
  createAdminOperations,
  makeAdminBackend,
  recordFixture,
  tenantFixture,
} from '../test-utils/operations.js'
import { expectNoAxeViolations } from '../test-utils/axe.js'
import zhCN from './locales/zh-CN.json' with { type: 'json' }

const COPY = zhCN.common as {
  readonly updatedAt: string
  readonly refreshed: string
  readonly refresh: string
}

/** The marker a refused account read leaves on the recent-activity actor. */

const TENANTS = [
  tenantFixture({ name: 'acme', display_name: 'Acme', status: 'active' }),
  tenantFixture({ name: 'beta', display_name: 'Beta', status: 'active' }),
  tenantFixture({ name: 'old', display_name: 'Old', status: 'disabled' }),
]

/**
 * Renders the page under the shell's feature answer. The audit-backed half
 * of the dashboard exists only where audit.admin is present, so a suite
 * that expects it must say so; the default answer serves every feature.
 */
function renderDashboard(
  options: RenderWithProvidersOptions,
  props: DashboardPageProps = {},
  hasFeature: (name: FeatureName) => boolean = () => true,
): ReturnType<typeof renderWithProviders> {
  return renderWithProviders(
    <AdminFeatureProvider hasFeature={hasFeature}>
      <DashboardPage {...props} />
    </AdminFeatureProvider>,
    options,
  )
}

describe('DashboardPage', () => {
  it('raises its refresh action to the 44px floor under a coarse pointer', async () => {
    const operations = createAdminOperations(
      makeAdminBackend({ listTenants: () => Promise.resolve(TENANTS) }),
    )
    renderDashboard({ operations })
    const refresh = await screen.findByRole('button', { name: zhCN.common.refresh })
    expect(hasCoarsePointerTouchTarget(refresh)).toBe(true)
  })

  it('summarizes the roster in the KPI row, tombstones included', async () => {
    // The roster read carries tombstones. The four counts partition it, so the
    // deleted count is what keeps active, disabled and deleted adding up to the
    // total instead of leaving a tombstone unaccounted for.
    const tenants = [
      ...TENANTS,
      tenantFixture({ name: 'gone', display_name: 'Gone', status: 'deleted' }),
    ]
    const operations = createAdminOperations(
      makeAdminBackend({ listTenants: () => Promise.resolve(tenants) }),
    )
    renderDashboard({ operations })

    expect(await screen.findByText(zhCN.dashboard.kpi.tenants)).toBeInTheDocument()
    const count = (label: string, value: number) => {
      const card = screen.getByText(label).closest('li')
      if (card === null) {
        throw new Error(`the KPI card ${label} has no list item`)
      }
      expect(within(card as HTMLElement).getByText(String(value))).toBeInTheDocument()
    }
    // 4 = 2 active + 1 disabled + 1 deleted, all from the whole roster read.
    count(zhCN.dashboard.kpi.tenants, 4)
    count(zhCN.dashboard.kpi.active, 2)
    count(zhCN.dashboard.kpi.disabled, 1)
    count(zhCN.dashboard.kpi.deleted, 1)
  })

  it('lists the newest audit activity', async () => {
    const operations = createAdminOperations(
      makeAdminBackend({
        listRecords: () =>
          Promise.resolve({
            entries: [recordFixture({ id: 'rec-9', action: 'tenant.read' })],
            next: '',
          }),
      }),
    )
    renderDashboard({ operations })
    const recent = await screen.findByRole('heading', { level: 2, name: zhCN.dashboard.recent.title })
    expect(recent).toBeInTheDocument()
    expect(await screen.findByText('tenant.read')).toBeInTheDocument()
  })

  it('renders the recent activity actor as the account display name', async () => {
    const operations = createAdminOperations(
      makeAdminBackend({
        listRecords: () =>
          Promise.resolve({
            entries: [recordFixture({ id: 'rec-9' })],
            next: '',
          }),
        getAccount: () =>
          Promise.resolve(accountFixture({ display_name: 'Ada Lovelace' })),
      }),
    )
    renderDashboard({ operations })
    expect(await screen.findByText('Ada Lovelace')).toBeInTheDocument()
    expect(screen.queryByText('acc-1')).not.toBeInTheDocument()
  })

  it('marks a recent activity actor whose account read is refused as unresolved', async () => {
    const operations = createAdminOperations(
      makeAdminBackend({
        listRecords: () =>
          Promise.resolve({
            entries: [recordFixture({ id: 'rec-9' })],
            next: '',
          }),
        getAccount: () => Promise.reject(new Error('forbidden')),
      }),
    )
    renderDashboard({ operations })
    // The cell renders the neutral copy with the unresolved note; the
    // recorded id is never the visible fallback.
    const cell = await screen.findByTitle(zhCN.common.accountUnresolved)
    expect(cell).toHaveTextContent(zhCN.common.accountUnavailable)
    expect(cell).not.toHaveTextContent('acc-1')
    expect(cell).toHaveAttribute('title', zhCN.common.accountUnresolved)
  })

  it('renders recent activity with the audit page columns and priorities', async () => {
    const operations = createAdminOperations(
      makeAdminBackend({
        listRecords: () =>
          Promise.resolve({
            entries: [recordFixture({ id: 'rec-9', action: 'tenant.read' })],
            next: '',
          }),
      }),
    )
    renderDashboard({ operations })
    await screen.findByText('tenant.read')
    // The list carries the same set as the audit query page: the outcome
    // outranks everything so a failure stays visible on the narrowest
    // table that still shows a prioritized column, the actor follows at
    // md, and the object is the first column shed.
    expect(
      columnVisibility(screen.getByRole('columnheader', { name: zhCN.dashboard.recent.column.object })),
    ).toEqual({ hiddenAt: 0, visibleFrom: 1200 })
    // The body cell carries its own copy of the rule; assert it too so a
    // header/body divergence cannot pass with only the header checked.
    expect(
      columnVisibility(screen.getByText('acme').closest('td')!),
    ).toEqual({ hiddenAt: 0, visibleFrom: 1200 })
    expect(
      columnVisibility(screen.getByRole('columnheader', { name: zhCN.dashboard.recent.column.actor })),
    ).toEqual({ hiddenAt: 0, visibleFrom: 900 })
    expect(
      columnVisibility(screen.getByRole('columnheader', { name: zhCN.dashboard.recent.column.outcome })),
    ).toEqual({ hiddenAt: 0, visibleFrom: 600 })
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
    const operations = createAdminOperations(
      makeAdminBackend({
        listRecords: () =>
          Promise.resolve({
            entries: [recordFixture({ id: 'rec-9', action, object })],
            next: '',
          }),
      }),
    )
    renderDashboard({ operations })
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
    expect(actorCell).not.toHaveAttribute('title', 'acc-1')
    expect(
      within(actorCell as HTMLElement).queryByRole('button'),
    ).toBeNull()
    expect(actorCell!.firstElementChild).toHaveStyle({ maxWidth: '160px' })
    expect(
      containerMaxWidthOf(actorCell!.firstElementChild!),
    ).toEqual(new Map())
  })

  it('derives the platform health row from the windowed audit read', async () => {
    const operations = createAdminOperations(
      makeAdminBackend({
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
    renderDashboard({ operations })
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
    const operations = createAdminOperations(
      makeAdminBackend({
        listRecords: (params) =>
          params?.action?.includes('authn.login')
            ? Promise.resolve({
                entries: [recordFixture({ id: 'l1', action: 'authn.login' })],
                next: 'cur-2',
              })
            : Promise.resolve({ entries: [], next: '' }),
      }),
    )
    renderDashboard({ operations })
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
    const operations = createAdminOperations(
      makeAdminBackend({
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
    renderDashboard({ operations })
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
    const operations = createAdminOperations(
      makeAdminBackend({
        listRecords: (params) => {
          if (params?.since !== undefined) {
            healthWindows.push(params.since)
          }
          return Promise.resolve({ entries: [], next: '' })
        },
      }),
    )
    renderDashboard({ operations })
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
    const operations = createAdminOperations(
      makeAdminBackend({
        listRecords: (params) => {
          if (params?.since !== undefined) {
            healthWindows.push(params.since)
          }
          return Promise.resolve({ entries: [], next: '' })
        },
      }),
    )
    renderDashboard({ operations })
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
    const operations = createAdminOperations(
      makeAdminBackend({
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
    renderDashboard({ operations })
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
    const operations = createAdminOperations(
      makeAdminBackend({
        listRecords: (params) =>
          params?.action?.includes('authn.login')
            ? Promise.reject(new Error('boom'))
            : Promise.resolve({ entries: [], next: '' }),
      }),
    )
    renderDashboard({ operations })
    expect(await screen.findByText(zhCN.dashboard.health.error.title)).toBeInTheDocument()
    expect(screen.getByText(zhCN.dashboard.health.error.description)).toBeInTheDocument()
    expect(screen.getByRole('button', { name: zhCN.dashboard.retry })).toBeInTheDocument()
  })

  it('renders the permission placeholder with no retry when the roster read is refused with 403', async () => {
    const operations = createAdminOperations(
      makeAdminBackend({
        listTenants: () => Promise.reject({ status: 403, code: 'client.http.403' }),
      }),
    )
    renderDashboard({ operations })
    expect(await screen.findByText(zhCN.forbidden.title)).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: zhCN.dashboard.retry })).toBeNull()
  })

  it('renders the retryable error placeholder when the roster read fails otherwise', async () => {
    const operations = createAdminOperations(
      makeAdminBackend({
        listTenants: () => Promise.reject(new Error('boom')),
      }),
    )
    renderDashboard({ operations })
    expect(await screen.findByText(zhCN.dashboard.error.title)).toBeInTheDocument()
    expect(screen.getByText(zhCN.dashboard.error.description)).toBeInTheDocument()
    expect(screen.getByRole('button', { name: zhCN.dashboard.retry })).toBeInTheDocument()
  })

  it('shows the oldest settle time in the refresh action group', async () => {
    const operations = createAdminOperations(
      makeAdminBackend({
        listTenants: () => Promise.resolve(TENANTS),
        listRecords: () =>
          Promise.resolve({
            entries: [recordFixture({ id: 'rec-9', action: 'tenant.read' })],
            next: '',
          }),
      }),
    )
    const { queryClient } = renderDashboard({ operations })
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
        timeZone: TEST_TIME_ZONE,
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
    const operations = createAdminOperations(
      makeAdminBackend({
        listTenants: () => Promise.resolve(TENANTS),
        listRecords,
      }),
    )
    renderDashboard({ operations })
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

  it('omits the audit-backed half and reads no audit records when audit.admin is absent', async () => {
    const listRecords = vi.fn(() => Promise.resolve({ entries: [], next: '' }))
    const operations = createAdminOperations(
      makeAdminBackend({
        listTenants: () => Promise.resolve(TENANTS),
        listRecords,
      }),
    )
    renderDashboard({ operations }, {}, () => false)

    // The roster row is not gated; the health row and the recent list are.
    expect(
      await screen.findByText(zhCN.dashboard.kpi.tenants),
    ).toBeInTheDocument()
    expect(
      screen.queryByRole('heading', {
        level: 2,
        name: zhCN.dashboard.health.title,
      }),
    ).toBeNull()
    expect(
      screen.queryByRole('heading', {
        level: 2,
        name: zhCN.dashboard.recent.title,
      }),
    ).toBeNull()
    // A feature-absent deployment serves no audit query, so the page issues
    // no audit read: the reads live in the section that is not mounted.
    await act(async () => {
      await new Promise((resolve) => setTimeout(resolve, 0))
    })
    expect(listRecords).not.toHaveBeenCalled()
  })

  it('links a recent-activity row to its audit detail below the admin root', async () => {
    const operations = createAdminOperations(
      makeAdminBackend({
        listRecords: () =>
          Promise.resolve({
            entries: [recordFixture({ id: 'rec-9', action: 'tenant.read' })],
            next: '',
          }),
      }),
    )
    renderDashboard({ operations }, { rootHref: '/admin' })

    expect(
      await screen.findByRole('link', { name: 'tenant.read' }),
    ).toHaveAttribute('href', '/admin/audit/rec-9')
  })

  it('normalises an origin-mounted admin root before appending the detail path', async () => {
    const operations = createAdminOperations(
      makeAdminBackend({
        listRecords: () =>
          Promise.resolve({
            entries: [recordFixture({ id: 'rec-9', action: 'tenant.read' })],
            next: '',
          }),
      }),
    )
    // The admin mounted at the origin matches the dashboard at '/', which
    // contributes no prefix below it.
    renderDashboard({ operations }, { rootHref: '/' })

    expect(
      await screen.findByRole('link', { name: 'tenant.read' }),
    ).toHaveAttribute('href', '/audit/rec-9')
  })

  it('renders host content after the page sections', async () => {
    const operations = createAdminOperations(
      makeAdminBackend({
        listTenants: () => Promise.resolve(TENANTS),
        listRecords: () =>
          Promise.resolve({
            entries: [recordFixture({ id: 'rec-9' })],
            next: '',
          }),
      }),
    )
    renderDashboard(
      { operations },
      { children: <p>Host section</p> },
    )

    const recent = await screen.findByRole('heading', {
      level: 2,
      name: zhCN.dashboard.recent.title,
    })
    const host = await screen.findByText('Host section')
    expect(
      recent.compareDocumentPosition(host) & Node.DOCUMENT_POSITION_FOLLOWING,
    ).toBeTruthy()
  })

  it('has no axe violations', async () => {
    const operations = createAdminOperations(
      makeAdminBackend({ listTenants: () => Promise.resolve(TENANTS) }),
    )
    renderDashboard({ operations })
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
