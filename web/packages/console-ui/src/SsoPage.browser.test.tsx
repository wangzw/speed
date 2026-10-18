/**
 * SsoPage in a real browser: the SSO provider table at the sm/md/lg
 * floor widths with fixed data. The tenant is seeded through the query
 * parameter the page opens on.
 */
import { afterEach, describe, it } from 'vitest'
import { screen } from '@testing-library/react'
import { page } from 'vitest/browser'
import { SsoPage } from './SsoPage.js'
import { renderWithProviders } from '../test-utils/render.js'
import {
  createConsoleOperations,
  makeConsoleBackend,
  providerFixture,
} from '../test-utils/operations.js'
import { expectNoHorizontalOverflow } from '../test-utils/browser-overflow.js'
import { clearTenant, seedTenant } from '../test-utils/tenant-url.js'

const FLOOR_WIDTHS = [600, 900, 1200] as const

const TENANTS = [{ id: 'acme', alias: 'Acme', status: 'active' as const }]

const PROVIDERS = [
  providerFixture({ name: 'Acme Corporate Identity Provider' }),
]

afterEach(clearTenant)

describe('SsoPage horizontal overflow in Chromium', () => {
  for (const width of FLOOR_WIDTHS) {
    it(`keeps the provider table inside a ${width}px viewport`, async () => {
      await page.viewport(width, 900)
      seedTenant('acme')

      const operations = createConsoleOperations(
        makeConsoleBackend({
          listTenants: () => Promise.resolve(TENANTS),
          listSsoProviders: () => Promise.resolve(PROVIDERS),
        }),
      )
      renderWithProviders(<SsoPage />, { operations, language: 'en-US' })

      await screen.findByText('Acme Corporate Identity Provider')
      await expectNoHorizontalOverflow()
    })
  }
})
