/**
 * DashboardPage in a real browser: the recent-activity table at the
 * sm/md/lg floor widths with fixed data, and the overview's layout at
 * 768px. The KPI rows derive from the roster read, so the same fixed
 * roster feeds both panels.
 */
import { describe, expect, it } from 'vitest'
import { screen } from '@testing-library/react'
import { page } from 'vitest/browser'
import { DashboardPage } from './DashboardPage.js'
import { AdminFeatureProvider } from './internal/features.js'
import { renderWithProviders } from '../test-utils/render.js'
import {
  accountFixture,
  createAdminOperations,
  makeAdminBackend,
  recordFixture,
} from '../test-utils/operations.js'
import { expectNoHorizontalOverflow } from '../test-utils/browser-overflow.js'
import { adminUiResources } from './resources.js'
import type { AdminAuditRecord } from './operations.js'

const FLOOR_WIDTHS = [600, 900, 1200] as const

const TENANTS = [
  { alias: 'acme', displayName: 'Acme', kind: 'business' as const, status: 'active' as const },
  { id: 'beta', alias: 'Beta', status: 'active' as const },
  { id: 'old', alias: 'Old', status: 'disabled' as const },
]

/** The shipped en-US dashboard strings the overview assertions read. */
const DASHBOARD = (
  adminUiResources['en-US'] as unknown as {
    readonly dashboard: {
      readonly kpi: {
        readonly tenants: string
        readonly active: string
        readonly disabled: string
        readonly deleted: string
      }
      readonly recent: {
        readonly column: { readonly object: string; readonly outcome: string }
      }
    }
  }
).dashboard

const RECORDS: readonly AdminAuditRecord[] = [
  recordFixture({
    id: 'record-identifier-with-a-long-value-0001',
    action: 'authn.account.identity.add',
    object: 'account/account-identifier-with-a-long-value-0001',
  }),
  recordFixture({
    id: 'record-identifier-with-a-long-value-0002',
    action: 'tenant.membership.remove',
    outcome: 'denied',
  }),
]

describe('DashboardPage horizontal overflow in Chromium', () => {
  for (const width of FLOOR_WIDTHS) {
    it(`keeps the recent-activity table inside a ${width}px viewport`, async () => {
      await page.viewport(width, 900)

      const operations = createAdminOperations(
        makeAdminBackend({
          listTenants: () => Promise.resolve(TENANTS),
          listRecords: () =>
            Promise.resolve({ entries: [...RECORDS], next: '' }),
          getAccount: () =>
            Promise.resolve(accountFixture({ display_name: 'Ada Lovelace' })),
        }),
      )
      // The recent-activity table exists only where the process serves
      // audit.admin, so the suite declares the feature present.
      renderWithProviders(
        <AdminFeatureProvider hasFeature={() => true}>
          <DashboardPage />
        </AdminFeatureProvider>,
        { operations, language: 'en-US' },
      )

      await screen.findAllByText('Ada Lovelace')
      await expectNoHorizontalOverflow()
    })
  }
})

/** A viewport width pinned for the overview case. */
const OVERVIEW_WIDTH = 768

/** Positions within this many pixels count as the same grid track. */
const TRACK_TOLERANCE = 2

/** The distinct values a set of positions occupies, in document order. */
function distinctPositions(values: readonly number[]): number[] {
  const positions: number[] = []
  for (const value of values) {
    if (!positions.some((seen) => Math.abs(seen - value) <= TRACK_TOLERANCE)) {
      positions.push(value)
    }
  }
  return positions
}

/** How many positions fall on one track. */
function countOnTrack(
  values: readonly number[],
  track: number,
): number {
  return values.filter((value) => Math.abs(value - track) <= TRACK_TOLERANCE)
    .length
}

describe('DashboardPage overview at 768px in Chromium', () => {
  async function renderOverview() {
    await page.viewport(OVERVIEW_WIDTH, 900)
    const operations = createAdminOperations(
      makeAdminBackend({
        listTenants: () => Promise.resolve(TENANTS),
        listRecords: () =>
          Promise.resolve({ entries: [...RECORDS], next: '' }),
        getAccount: () =>
          Promise.resolve(accountFixture({ display_name: 'Ada Lovelace' })),
      }),
    )
    renderWithProviders(
      <AdminFeatureProvider hasFeature={() => true}>
        <DashboardPage />
      </AdminFeatureProvider>,
      { operations, language: 'en-US' },
    )
    // Wait for both reads behind the overview to settle.
    await screen.findByText('denied')
    await screen.findByText(DASHBOARD.kpi.tenants)
  }

  it('lays the four KPI cards out as 2x2', async () => {
    await renderOverview()

    const cards = (
      [
        DASHBOARD.kpi.tenants,
        DASHBOARD.kpi.active,
        DASHBOARD.kpi.disabled,
        DASHBOARD.kpi.deleted,
      ] as const
    ).map((label) => {
      const card = screen.getByText(label).closest('li')
      expect(card).not.toBeNull()
      return card as HTMLElement
    })

    const boxes = cards.map((card) => card.getBoundingClientRect())
    for (const box of boxes) {
      expect(box.width).toBeGreaterThan(0)
      expect(box.height).toBeGreaterThan(0)
    }

    const lefts = distinctPositions(boxes.map((box) => box.left))
    const tops = distinctPositions(boxes.map((box) => box.top))

    expect(lefts).toHaveLength(2)
    expect(tops).toHaveLength(2)
    for (const left of lefts) {
      expect(countOnTrack(boxes.map((box) => box.left), left)).toBe(2)
    }
    for (const top of tops) {
      expect(countOnTrack(boxes.map((box) => box.top), top)).toBe(2)
    }
  })

  it('keeps the recent activity outcome column and drops the object column', async () => {
    await renderOverview()

    const outcomeHeader = screen.getByText(DASHBOARD.recent.column.outcome)
    const objectHeader = screen.getByText(DASHBOARD.recent.column.object)

    expect(outcomeHeader).toBeVisible()
    expect(
      getComputedStyle(outcomeHeader.closest('th') as HTMLElement).display,
    ).toBe('table-cell')
    expect(objectHeader).not.toBeVisible()
    expect(
      getComputedStyle(objectHeader.closest('th') as HTMLElement).display,
    ).toBe('none')

    // The result column is not only labelled but carries the row's value.
    expect(screen.getByText('denied')).toBeVisible()
  })
})
