/**
 * DashboardPage in a real browser: the recent-activity table at the
 * sm/md/lg floor widths with fixed data. The KPI row derives from the
 * roster read, so the same fixed roster feeds both panels.
 */
import { describe, it } from 'vitest'
import { screen } from '@testing-library/react'
import { page } from 'vitest/browser'
import { DashboardPage } from './DashboardPage.js'
import { ConsoleFeatureProvider } from './internal/features.js'
import { renderWithProviders } from '../test-utils/render.js'
import {
  accountFixture,
  createConsoleOperations,
  makeConsoleBackend,
  recordFixture,
} from '../test-utils/operations.js'
import { expectNoHorizontalOverflow } from '../test-utils/browser-overflow.js'
import type { ConsoleAuditRecord } from './operations.js'

const FLOOR_WIDTHS = [600, 900, 1200] as const

const TENANTS = [
  { id: 'acme', alias: 'Acme', status: 'active' as const },
  { id: 'beta', alias: 'Beta', status: 'active' as const },
  { id: 'old', alias: 'Old', status: 'disabled' as const },
]

const RECORDS: readonly ConsoleAuditRecord[] = [
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

      const operations = createConsoleOperations(
        makeConsoleBackend({
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
        <ConsoleFeatureProvider hasFeature={() => true}>
          <DashboardPage />
        </ConsoleFeatureProvider>,
        { operations, language: 'en-US' },
      )

      await screen.findAllByText('Ada Lovelace')
      await expectNoHorizontalOverflow()
    })
  }
})
