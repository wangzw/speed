/**
 * TenantRosterPage in a real browser: the roster table at the sm/md/lg
 * floor widths with fixed data. The invariant is horizontal containment
 * -- the page and the table's own scroll container stay inside their
 * widths -- which only a browser can measure (container queries, column
 * priorities and real font metrics all take part).
 */
import { describe, it } from 'vitest'
import { screen } from '@testing-library/react'
import { page } from 'vitest/browser'
import { TenantRosterPage } from './TenantRosterPage.js'
import { renderWithProviders } from '../test-utils/render.js'
import {
  createAdminOperations,
  makeAdminBackend,
  tenantFixture,
} from '../test-utils/operations.js'
import { expectNoHorizontalOverflow } from '../test-utils/browser-overflow.js'

const FLOOR_WIDTHS = [600, 900, 1200] as const

const TENANTS = [
  tenantFixture({
    alias: 'acme-corporation',
    displayName: 'Acme Corporation',
    status: 'active',
  }),
  tenantFixture({
    alias: 'beta-industries',
    displayName: 'Beta Industries',
    status: 'disabled',
    disabledBy: 'operator',
  }),
]

describe('TenantRosterPage horizontal overflow in Chromium', () => {
  for (const width of FLOOR_WIDTHS) {
    it(`keeps the roster table inside a ${width}px viewport`, async () => {
      await page.viewport(width, 900)
      const operations = createAdminOperations(
        makeAdminBackend({
          listTenants: () => Promise.resolve(TENANTS),
        }),
      )
      renderWithProviders(<TenantRosterPage />, { operations, language: 'en-US' })

      await screen.findByText('Acme Corporation')
      await expectNoHorizontalOverflow()
    })
  }
})
